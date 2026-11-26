package gui

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/avbridge"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/dex"
	"github.com/YuanZhe-99/AndroFuse/internal/scrcpy"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// SessionMode identifies what work mode an interactive SessionWindow drives.
type SessionMode string

const (
	SessionModeDeX         SessionMode = "dex"
	SessionModeMirror      SessionMode = "mirror"
	SessionModeApp         SessionMode = "app"
	SessionModeCamera      SessionMode = "camera"
	SessionModeMiracastDeX SessionMode = "miracast_dex"
	SessionModeDeXFallback SessionMode = "dex_fallback"
	// SessionModeShare is the Companion's screen sharing on mobile data (MediaProjection), when
	// Wireless debugging cannot run.
	SessionModeShare SessionMode = "share"
)

// SessionSpec configures a live interactive desktop stream window.
type SessionSpec struct {
	ID           string
	DeviceKey    string // Unified HardwareHash for stream accounting & handoff (INV-02)
	Serial       string
	Title        string
	Mode         SessionMode
	Transport    string
	BackendLabel string
	TargetSpec   string // e.g. "1920x1080 @ 60 fps" (INV-05)
	IsDeX        bool
	IsMiracast   bool
	AllowResize  bool
	ScreenOffCap bool
	AppIcon      *ui.Bitmap
	AppPackage   string
	// CameraFacing is "front" or "back" for a camera session.
	CameraFacing string
	OnClose      func()
	// Open says where the session first shows (OpenDefault follows Settings › Windows).
	Open OpenIn
}

// OpenIn says where a new session shows.
type OpenIn int

const (
	OpenDefault OpenIn = iota // Settings › Windows: a main-window pane unless set otherwise
	OpenPane                  // a pane of the main window
	OpenSplit                 // a pane beside the one showing now
	OpenWindow                // its own window
)

type nv12Slot struct {
	buf      []byte
	yLen     int
	totalLen int
	w, h     int
	yStride  int
	uvStride int
	hw       bool
	decodeUs uint32
}

// SessionWindow is an independent native MyGo window displaying a live NV12
// stream via ui.Bitmap.UpdateYUV (`INV-04`) with honest telemetry HUD (`INV-05`),
// dynamic resize, and per-window input forwarding (`INV-12`).
type SessionWindow struct {
	app  *App
	spec SessionSpec
	win  *mygo.Window

	surface ui.Handle
	bmp     *ui.Bitmap
	input   InputTracker

	frameMu      sync.Mutex
	slots        [3]nv12Slot
	writing      int
	ready        int
	shown        int
	updateQueued atomic.Bool

	mu            sync.Mutex
	services      ui.Services
	scrcpySess    *scrcpy.Session
	dexSess       *dex.LoopbackSession
	miracastSess  *dex.MiracastSession
	audioPlayer   *core.AudioPlayer
	audioMuted    bool
	recording     bool
	recordPath    string
	lastClipText  string
	clipSeq       uint64
	noteTimer     *time.Timer
	touchMode     bool
	screenOff     bool
	telemOpen     bool                  // the header's telemetry popover
	shareKey      func(action string)   // Back, Home, Recents for a Companion screen-sharing session
	closed        chan struct{}         // closed once Close released the stream, its leases and audio
	camBridge     *avbridge.MJPEGServer // a camera session's loopback MJPEG bridge (set before streaming)
	switching     bool                  // Switch camera is restarting the stream
	statusNote    string
	negotiatedW   int
	negotiatedH   int
	measuredFPS   float64
	avgDecodeUs   int64
	hwAccelerated bool
	codecLabel    string
	surfaceBox    ui.Rect
	lastResizeBox ui.Rect
	resizeTimer   *time.Timer
	closing       bool
	// docked sessions draw in a main-window pane; docking is set while their own window
	// closes to dock, so closing it does not end the session.
	docked     bool
	docking    bool
	paneHandle ui.Handle // the main-window pane, for its committed size
}

func newSessionWindow(app *App, spec SessionSpec) *SessionWindow {
	sw := &SessionWindow{
		app:     app,
		spec:    spec,
		bmp:     ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1))),
		writing: 0,
		ready:   -1,
		shown:   -1,
		closed:  make(chan struct{}),
	}
	return sw
}

// show puts a newly attached session where spec.Open (or Settings › Windows) says: a pane of
// the main window, beside the pane showing now, or its own window of size w×h.
func (sw *SessionWindow) show(w, h int) {
	open := sw.spec.Open
	if open == OpenDefault {
		open = OpenPane
		if sw.app.opensInWindow(sw.spec.Mode) {
			open = OpenWindow
		}
	}
	if open == OpenWindow {
		sw.openWindow(w, h)
		return
	}
	sw.mu.Lock()
	sw.docked = true
	sw.mu.Unlock()
	sw.app.dockSession(sw.spec.ID, open == OpenSplit)
}

// PopOut moves a docked session into its own window without interrupting the stream.
func (sw *SessionWindow) PopOut() {
	sw.mu.Lock()
	if !sw.docked || sw.closing {
		sw.mu.Unlock()
		return
	}
	sw.docked = false
	sw.mu.Unlock()
	sw.app.undockSession(sw.spec.ID)
	sw.openWindow(sessionDefW, sessionDefH)
}

// Dock moves a popped-out session back into a main-window pane without interrupting the stream.
func (sw *SessionWindow) Dock() {
	sw.mu.Lock()
	win := sw.win
	if sw.docked || win == nil || sw.closing {
		sw.mu.Unlock()
		return
	}
	sw.win = nil
	sw.docking = true
	sw.docked = true
	sw.mu.Unlock()
	sw.app.dockSession(sw.spec.ID, false)
	win.Close()
}

// Docked reports whether the session draws in a main-window pane.
func (sw *SessionWindow) Docked() bool {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.docked
}

// hostWindow is the window that draws sw: its own when popped out, else the main window.
func (sw *SessionWindow) hostWindow() *mygo.Window {
	sw.mu.Lock()
	win, docked := sw.win, sw.docked
	sw.mu.Unlock()
	if docked {
		return sw.app.mainWindow()
	}
	return win
}

// openWindow opens sw's own window of size w×h.
func (sw *SessionWindow) openWindow(w, h int) {
	opts := mygo.WindowOptions{
		Title:     sw.spec.Title,
		Width:     w,
		Height:    h,
		MinWidth:  sessionMinW,
		MinHeight: sessionMinH,
		Content:   ui.View(sw.view),
	}
	if runtime.GOOS == "windows" {
		opts.TitleBarStyle = mygo.TitleBarHidden
		opts.TitleBarHeight = int(paneHeaderH)
		opts.HideWindowButtons = true
	}
	if sw.spec.Mode == SessionModeApp {
		opts.MinWidth = appMinResizePx
		opts.MinHeight = appMinResizePx
	}
	win := mygo.NewWindow(opts)
	sw.mu.Lock()
	sw.win = win
	sw.mu.Unlock()

	win.OnClosed(func() {
		sw.mu.Lock()
		docking := sw.docking
		sw.docking = false
		sw.mu.Unlock()
		if !docking {
			sw.Close()
		}
	})
	win.Show()
	win.Focus()
}

// attachScrcpy binds a live scrcpy session (plus optional Samsung DeX loopback) to sw.
func (sw *SessionWindow) attachScrcpy(s *scrcpy.Session, d *dex.LoopbackSession, w, h int) {
	sw.mu.Lock()
	sw.scrcpySess = s
	sw.dexSess = d
	if s != nil {
		st := s.Stats()
		sw.codecLabel = codecName(st.CodecID)
		if st.NegotiatedWidth > 0 && st.NegotiatedHeight > 0 {
			sw.negotiatedW = int(st.NegotiatedWidth)
			sw.negotiatedH = int(st.NegotiatedHeight)
		}
	}
	sw.mu.Unlock()
	if w <= 0 || h <= 0 {
		w, h = sessionDefW, sessionDefH
	}
	sw.show(w, h)
	if s != nil {
		go sw.watchScrcpyEnd(s)
	}
}

// attachMiracast binds a live Wireless Miracast DeX receiver session to sw.
func (sw *SessionWindow) attachMiracast(m *dex.MiracastSession) {
	sw.mu.Lock()
	sw.miracastSess = m
	sw.codecLabel = "H.264"
	sw.statusNote = L.MiracastWaitingNote()
	sw.mu.Unlock()
	sw.show(sessionDefW, sessionDefH)
	if m != nil {
		go func() {
			<-m.Done()
			sw.setStatusNote(L.SessionEnded())
		}()
	}
}

func (sw *SessionWindow) swapScrcpy(newSess *scrcpy.Session, newTransport, newSerial string) {
	sw.mu.Lock()
	if sw.closing {
		sw.mu.Unlock()
		if newSess != nil {
			newSess.Close()
		}
		return
	}
	sw.scrcpySess = newSess
	sw.spec.Transport = newTransport
	sw.spec.Serial = newSerial
	if newSess != nil {
		st := newSess.Stats()
		sw.codecLabel = codecName(st.CodecID)
		if st.NegotiatedWidth > 0 && st.NegotiatedHeight > 0 {
			sw.negotiatedW = int(st.NegotiatedWidth)
			sw.negotiatedH = int(st.NegotiatedHeight)
		}
	}
	sw.mu.Unlock()
	sw.redraw()
	if newSess != nil {
		go sw.watchScrcpyEnd(newSess)
	}
}

func (sw *SessionWindow) watchScrcpyEnd(s *scrcpy.Session) {
	<-s.Done()
	sw.mu.Lock()
	closing := sw.closing
	isCurrent := sw.scrcpySess == s
	canHandoff := sw.spec.Mode == SessionModeDeX &&
		sw.dexSess != nil &&
		sw.dexSess.Ready().CtrlPort > 0 &&
		sw.app != nil
	sw.mu.Unlock()
	if closing || !isCurrent || sw.switchingNow() {
		return
	}
	if canHandoff && sw.app.handoffOn() {
		sw.attemptDeXGraceHandoff(s)
		return
	}
	if err := s.Err(); err != nil {
		if sw.app != nil {
			sw.setStatusNote(sw.app.failed(L.SessionEnded(), err))
		} else {
			sw.setStatusNote(L.SessionEnded())
		}
		return
	}
	sw.setStatusNote(L.SessionEnded())
}

func (sw *SessionWindow) attemptDeXGraceHandoff(failedSess *scrcpy.Session) {
	app := sw.app
	client, store, err := app.ensureADBAndLeases()
	if err != nil {
		sw.setStatusNote(L.SessionEnded())
		return
	}
	app.rt.coreMu.Lock()
	lib, lErr := app.ensureCoreLocked()
	app.rt.coreMu.Unlock()
	if lErr != nil {
		sw.setStatusNote(L.SessionEnded())
		return
	}

	sw.mu.Lock()
	dSess := sw.dexSess
	devKey := sw.spec.DeviceKey
	prevSerial := sw.spec.Serial
	sw.mu.Unlock()
	if dSess == nil {
		sw.setStatusNote(L.SessionEnded())
		return
	}

	graceSec := dSess.GraceSec()
	if graceSec <= 0 {
		graceSec = 30
	}
	deadline := time.Now().Add(time.Duration(graceSec) * time.Second)
	for time.Now().Before(deadline) {
		sw.mu.Lock()
		stillSame := !sw.closing && sw.scrcpySess == failedSess
		sw.mu.Unlock()
		if !stillSame {
			return
		}
		rem := max(int(time.Until(deadline).Seconds())+1, 1)
		sw.setStatusNote(L.DeXReconnecting(rem))

		qCtx, qCancel := context.WithTimeout(context.Background(), 8*time.Second)
		cand, ok := app.findLiveTransportFor(qCtx, client, devKey, prevSerial)
		if !ok && app.connectWirelessFor(qCtx, client, devKey) {
			cand, ok = app.findLiveTransportFor(qCtx, client, devKey, prevSerial)
		}
		qCancel()
		if ok {
			rCtx, rCancel := context.WithTimeout(context.Background(), 12*time.Second)
			ready, rErr := dSess.Reattach(rCtx, cand.Serial)
			if rErr == nil {
				var newSess *scrcpy.Session
				newSess, vErr := scrcpy.StartADB(rCtx, client, lib, store, scrcpy.LaunchConfig{
					SessionID:     sw.spec.ID + "-vid",
					Serial:        cand.Serial,
					DisplayID:     ready.DisplayID,
					MaxFPS:        60,
					VideoBitRate:  12_000_000,
					EnableControl: true,
					Audio:         true,
					InitialWidth:  uint32(ready.Width),
					InitialHeight: uint32(ready.Height),
					OnFrame: func(f core.NV12Frame) {
						if newSess != nil {
							st := newSess.Stats()
							sw.PushNV12(f, st.MeasuredFPS, st.AvgDecodeUs)
						} else {
							sw.PushNV12(f, 0, int64(f.DecodeUs))
						}
					},
					OnClipboard: sw.OnDeviceClipboard,
					OnAudioPCM:  sw.PushAudioPCM,
				})
				rCancel()
				if vErr == nil {
					tLbl := transportLabel(cand.Transport)
					sw.swapScrcpy(newSess, tLbl, cand.Serial)
					sw.flashNote(L.DeXReattached(tLbl))
					app.RefreshDevices()
					return
				}
			} else {
				rCancel()
			}
		}
		time.Sleep(1500 * time.Millisecond)
	}
	if err := failedSess.Err(); err != nil {
		sw.setStatusNote(err.Error())
		return
	}
	sw.setStatusNote(L.SessionEnded())
}

func (sw *SessionWindow) setStatusNote(note string) {
	sw.mu.Lock()
	if sw.noteTimer != nil {
		sw.noteTimer.Stop()
		sw.noteTimer = nil
	}
	sw.statusNote = note
	sw.mu.Unlock()
	sw.redraw()
}

func (sw *SessionWindow) flashNote(note string) {
	sw.mu.Lock()
	if sw.noteTimer != nil {
		sw.noteTimer.Stop()
	}
	sw.statusNote = note
	sw.noteTimer = time.AfterFunc(2500*time.Millisecond, func() {
		sw.mu.Lock()
		if sw.statusNote == note {
			sw.statusNote = ""
		}
		sw.mu.Unlock()
		sw.redraw()
	})
	sw.mu.Unlock()
	sw.redraw()
}

func (sw *SessionWindow) attachAudio(ap *core.AudioPlayer) {
	sw.mu.Lock()
	sw.audioPlayer = ap
	sw.mu.Unlock()
}

// PushAudioPCM plays incoming s16le PCM frames through the WASAPI player unless muted.
func (sw *SessionWindow) PushAudioPCM(pcm []byte) {
	if len(pcm) == 0 {
		return
	}
	sw.mu.Lock()
	muted := sw.audioMuted || sw.closing
	ap := sw.audioPlayer
	sw.mu.Unlock()
	if !muted && ap != nil {
		_ = ap.PushPCM(pcm)
	}
}

// OnDeviceClipboard syncs clipboard text received from Android into the Windows clipboard.
func (sw *SessionWindow) OnDeviceClipboard(text string) {
	if text == "" {
		return
	}
	sw.mu.Lock()
	if text == sw.lastClipText {
		sw.mu.Unlock()
		return
	}
	sw.lastClipText = text
	svc := sw.services
	sw.mu.Unlock()

	if svc != (ui.Services{}) {
		svc.WriteClipboard(text)
	}
	sw.flashNote(L.ClipboardSyncedFromDevice())
}

// SyncClipboardToDevice pushes text from the Windows clipboard to the Android device.
func (sw *SessionWindow) SyncClipboardToDevice(text string) {
	if text == "" {
		return
	}
	sw.mu.Lock()
	s := sw.scrcpySess
	sw.lastClipText = text
	sw.clipSeq++
	seq := sw.clipSeq
	sw.mu.Unlock()
	if s == nil {
		return
	}
	go func() {
		if err := s.SetClipboard(seq, false, text); err == nil {
			sw.flashNote(L.ClipboardSyncedToDevice())
		}
	}()
}

// Screenshot saves the currently rendered NV12 frame as a PNG under Pictures/AndroFuse.
func (sw *SessionWindow) Screenshot() {
	sw.frameMu.Lock()
	idx := sw.shown
	if idx < 0 || idx >= len(sw.slots) {
		sw.frameMu.Unlock()
		return
	}
	sl := sw.slots[idx]
	if sl.w <= 0 || sl.h <= 0 || sl.totalLen == 0 {
		sw.frameMu.Unlock()
		return
	}
	cp := make([]byte, sl.totalLen)
	copy(cp, sl.buf[:sl.totalLen])
	sw.frameMu.Unlock()

	go func() {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.TempDir()
		}
		outPath := filepath.Join(home, "Pictures", "AndroFuse",
			fmt.Sprintf("AndroFuse-%s.png", time.Now().Format("20060102-150405")))
		if err := SaveNV12AsPNG(outPath, sl.w, sl.h, sl.yStride, sl.uvStride, cp[:sl.yLen], cp[sl.yLen:]); err != nil {
			sw.flashNote(err.Error())
			return
		}
		sw.flashNote(L.ScreenshotSaved(filepath.Base(outPath)))
	}()
}

// ToggleRecording starts or stops direct fragmented-MP4 recording without re-encoding.
func (sw *SessionWindow) ToggleRecording() {
	sw.mu.Lock()
	s := sw.scrcpySess
	rec := sw.recording
	recPath := sw.recordPath
	sw.mu.Unlock()
	if s == nil {
		return
	}
	if rec {
		sw.mu.Lock()
		sw.recording = false
		sw.recordPath = ""
		sw.mu.Unlock()
		go func() {
			if err := s.StopRecording(); err != nil {
				sw.flashNote(err.Error())
				return
			}
			sw.flashNote(L.RecordingSaved(filepath.Base(recPath)))
		}()
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	outPath := filepath.Join(home, "Videos", "AndroFuse",
		fmt.Sprintf("AndroFuse-%s.mp4", time.Now().Format("20060102-150405")))
	sw.mu.Lock()
	sw.recording = true
	sw.recordPath = outPath
	sw.mu.Unlock()
	go func() {
		if err := s.StartRecording(outPath); err != nil {
			sw.mu.Lock()
			sw.recording = false
			sw.recordPath = ""
			sw.mu.Unlock()
			sw.flashNote(err.Error())
			return
		}
		sw.flashNote(L.RecordingStarted())
	}()
}

// PushNV12 copies frame planes into a triple-buffered preallocated slot and schedules
// UI-thread D3D11 UpdateYUV without per-frame steady-state heap allocations (INV-04).
func (sw *SessionWindow) PushNV12(f core.NV12Frame, fps float64, avgUs int64) {
	total := len(f.Y) + len(f.UV)
	if f.Width <= 0 || f.Height <= 0 || total == 0 {
		return
	}
	sw.frameMu.Lock()
	idx := sw.writing
	if idx == sw.shown || idx == sw.ready {
		idx = pickFreeSlot(sw.shown, sw.ready)
		sw.writing = idx
	}
	slot := &sw.slots[idx]
	if cap(slot.buf) < total {
		slot.buf = make([]byte, total)
	} else {
		slot.buf = slot.buf[:total]
	}
	copy(slot.buf[:len(f.Y)], f.Y)
	copy(slot.buf[len(f.Y):total], f.UV)
	slot.yLen = len(f.Y)
	slot.totalLen = total
	slot.w = f.Width
	slot.h = f.Height
	slot.yStride = f.YStride
	slot.uvStride = f.UVStride
	slot.hw = f.Hardware
	slot.decodeUs = f.DecodeUs
	sw.ready = idx
	sw.writing = pickFreeSlot(sw.shown, sw.ready)
	sw.frameMu.Unlock()

	sw.mu.Lock()
	sw.negotiatedW = f.Width
	sw.negotiatedH = f.Height
	sw.measuredFPS = fps
	sw.avgDecodeUs = avgUs
	sw.hwAccelerated = f.Hardware
	sw.statusNote = ""
	sw.mu.Unlock()

	if win := sw.hostWindow(); win != nil && !sw.updateQueued.Swap(true) {
		win.Update(func() {
			sw.updateQueued.Store(false)
			sw.present()
		})
	}
}

func pickFreeSlot(a, b int) int {
	for i := range 3 {
		if i != a && i != b {
			return i
		}
	}
	return 0
}

func (sw *SessionWindow) present() {
	sw.frameMu.Lock()
	if sw.ready < 0 {
		sw.frameMu.Unlock()
		return
	}
	idx := sw.ready
	sw.ready = -1
	sw.shown = idx
	slot := sw.slots[idx]
	sw.frameMu.Unlock()

	if slot.w <= 0 || slot.h <= 0 || slot.totalLen == 0 {
		return
	}
	sw.bmp.UpdateYUV(&ui.YUVImage{
		Format:    ui.NV12,
		Width:     slot.w,
		Height:    slot.h,
		Planes:    [3][]byte{slot.buf[:slot.yLen], slot.buf[slot.yLen:slot.totalLen]},
		Strides:   [3]int{slot.yStride, slot.uvStride},
		Matrix:    ui.BT709,
		FullRange: false,
	})
}

// Close tears down all owned session handles asynchronously off the UI loop and releases leases (INV-03).
func (sw *SessionWindow) Close() {
	sw.mu.Lock()
	if sw.closing {
		sw.mu.Unlock()
		return
	}
	sw.closing = true
	if sw.resizeTimer != nil {
		sw.resizeTimer.Stop()
		sw.resizeTimer = nil
	}
	if sw.noteTimer != nil {
		sw.noteTimer.Stop()
		sw.noteTimer = nil
	}
	s := sw.scrcpySess
	d := sw.dexSess
	m := sw.miracastSess
	ap := sw.audioPlayer
	sw.audioPlayer = nil
	win := sw.win
	sw.mu.Unlock()

	if sw.app != nil {
		sw.app.removeSession(sw.spec.ID)
	}
	if win != nil {
		win.Update(func() { win.Close() })
	}

	go func() {
		if s != nil {
			if sw.spec.ScreenOffCap && sw.screenOff {
				_ = s.SetDisplayPower(true)
			}
			s.Close()
		}
		if d != nil {
			d.Stop()
		}
		if m != nil {
			m.Close()
		}
		if ap != nil {
			ap.Close()
		}
		if sw.spec.OnClose != nil {
			sw.spec.OnClose()
		}
		close(sw.closed)
	}()
}

// redraw asks the window hosting sw to draw again.
func (sw *SessionWindow) redraw() {
	if w := sw.hostWindow(); w != nil {
		w.Update(func() {})
	}
}

// FocusWindow shows the session: its pane in the main window when docked, else its own window.
func (sw *SessionWindow) FocusWindow() {
	if sw.Docked() {
		sw.app.dockSession(sw.spec.ID, false)
		return
	}
	sw.mu.Lock()
	w := sw.win
	sw.mu.Unlock()
	if w != nil {
		w.Show()
		w.Focus()
	}
}

// view is the root of sw's own window: the same header as its pane (with dock back and the
// window's buttons), then the picture.
func (sw *SessionWindow) view(c *ui.Context) {
	c.Root().Background(ui.Hex(stageBgHex))
	t := c.Theme()
	ui.Column(c.Key("sess-own")).Fill().Background(t.Background).Children(func() {
		sw.app.sessionHeader(c, sw, nil, false, runtime.GOOS == "windows")
		sw.shareStrip(c)
		ui.Column(c.Key("sess-own-body")).Grow(1).FillWidth().MinHeight(0).Background(ui.Hex(stageBgHex)).Children(func() {
			sw.content(c, false)
		})
	})
}

// shareStrip explains, under the header of a Companion screen-sharing session, what works and
// what waits for Wi-Fi (`INV-09`: it is not the phone screen over Wireless debugging).
func (sw *SessionWindow) shareStrip(c *ui.Context) {
	if sw.spec.Mode != SessionModeShare {
		return
	}
	k := tok(c)
	ui.Row(c.Key("share-strip")).FillWidth().Shrink(0).Padding(8, 12).Gap(8).AlignItems(ui.Center).Background(k.sunk).Children(func() {
		ui.Icon(c, iconInfo).Size(14, 14).TextColor(k.text2)
		ui.Text(c, L.ShareStrip()).FontSize(sizeSmall).TextColor(k.text2).SingleLine().MinWidth(0)
	})
}

// content renders the interactive NV12 D3D11 surface; the telemetry and tools live in the header
// (sessionHeader), so nothing covers the picture.
func (sw *SessionWindow) content(c *ui.Context, embedded bool) {
	sw.present()

	sw.mu.Lock()
	sw.services = c.Services()
	note := sw.statusNote
	negW, negH := sw.negotiatedW, sw.negotiatedH
	sw.mu.Unlock()

	stage := ui.Box(c.Key("video-stage")).Grow(1).FillWidth().MinHeight(0).
		Background(ui.Hex(stageBgHex)).Focusable().AutoFocus().Bind(&sw.surface).
		HandleInput(func(ev ui.InputEvent) bool {
			return sw.handleInput(ev)
		})
	box := sw.surface.Bounds(c)
	sw.onStageBounds(box)

	stage.Children(func() {
		if negW > 0 && negH > 0 {
			ui.Image(c.Key("video-bmp"), sw.bmp).Fill().Fit(ui.Contain)
		}
		if note != "" {
			ui.Column(c.Key("stage-note")).Fill().Center().Padding(24).Gap(8).PassThrough().Children(func() {
				ui.Text(c, sw.app.paneLabel(sw)).Bold().FontSize(sizeHeading).TextColor(ui.Hex("#FFFFFF"))
				ui.Text(c, note).FontSize(sizeBody).TextColor(ui.Hex("#D6DEE6"))
			})
		}
	})
}

func (sw *SessionWindow) onStageBounds(box ui.Rect) {
	sw.mu.Lock()
	sw.surfaceBox = box
	allowResize := sw.spec.AllowResize && (sw.scrcpySess != nil || sw.dexSess != nil)
	lastBox := sw.lastResizeBox
	sw.mu.Unlock()

	if !allowResize || box.W < 240 || box.H < 200 {
		return
	}
	dw := box.W - lastBox.W
	if dw < 0 {
		dw = -dw
	}
	dh := box.H - lastBox.H
	if dh < 0 {
		dh = -dh
	}
	if dw < resizeMinDiff && dh < resizeMinDiff {
		return
	}
	minW, minH := 480, 360
	scale := float32(1)
	if sw.spec.Mode == SessionModeApp {
		minW, minH = appMinResizePx, appMinResizePx
		scale = appWinPixelScale
	}
	targetW := clampEven(int(box.W*scale), minW, 2560)
	targetH := clampEven(int(box.H*scale), minH, 1600)

	sw.mu.Lock()
	sw.lastResizeBox = box
	if sw.resizeTimer != nil {
		sw.resizeTimer.Stop()
	}
	sw.resizeTimer = time.AfterFunc(280*time.Millisecond, func() {
		sw.applyRemoteResize(targetW, targetH)
	})
	sw.mu.Unlock()
}

func (sw *SessionWindow) applyRemoteResize(w, h int) {
	sw.mu.Lock()
	if sw.closing {
		sw.mu.Unlock()
		return
	}
	s := sw.scrcpySess
	d := sw.dexSess
	negW, negH := sw.negotiatedW, sw.negotiatedH
	sw.mu.Unlock()

	if absDiff(w, negW) < 16 && absDiff(h, negH) < 16 {
		return
	}
	if d != nil {
		_, _ = d.Resize(w, h, 160, 4*time.Second)
		return
	}
	if s != nil && sw.spec.Mode == SessionModeApp {
		_ = s.Resize(uint16(w), uint16(h))
	}
}

func (sw *SessionWindow) handleInput(ev ui.InputEvent) bool {
	sw.mu.Lock()
	box := sw.surfaceBox
	vw, vh := sw.negotiatedW, sw.negotiatedH
	touchMode := sw.touchMode
	s := sw.scrcpySess
	m := sw.miracastSess
	win := sw.win
	sw.mu.Unlock()

	if box.W <= 0 || box.H <= 0 || vw <= 0 || vh <= 0 {
		if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyF11 && win != nil {
			win.SetFullScreen(!win.IsFullScreen())
			return true
		}
		return false
	}

	act := sw.input.Translate(ev, box, vw, vh)
	switch act.Kind {
	case ActionNone:
		return false
	case ActionToggleFullscreen:
		sw.toggleFullScreen()
		return true
	case ActionReleaseFocus:
		sw.mu.Lock()
		sw.statusNote = L.FocusReleasedHint()
		sw.mu.Unlock()
		sw.redraw()
		return true
	case ActionTouch:
		if m != nil {
			px := uint16(min(max(int(act.VideoX), 0), max(vw-1, 0)))
			py := uint16(min(max(int(act.VideoY), 0), max(vh-1, 0)))
			if act.TouchAction == scrcpy.ActionHoverMove {
				// UIBC GENERIC move is touch-drag; ignore passive hover when no button is down.
				return true
			}
			if act.Button == scrcpy.ButtonSecondary {
				if act.TouchAction == scrcpy.ActionDown {
					// Miracast DeX opens right-click context menu via ~720ms stationary press.
					go func() {
						_ = m.SendPointer(0, px, py)
						for range 12 {
							time.Sleep(60 * time.Millisecond)
							_ = m.SendPointer(2, px, py)
						}
						_ = m.SendPointer(1, px, py)
					}()
				}
				return true
			}
			var mAct uint8
			switch act.TouchAction {
			case scrcpy.ActionDown:
				mAct = 0
			case scrcpy.ActionUp:
				mAct = 1
			case scrcpy.ActionMove:
				mAct = 2
			}
			go func() { _ = m.SendPointer(mAct, px, py) }()
			return true
		}
		if s == nil {
			return false
		}
		pointerID := scrcpy.PointerIDMouse
		if touchMode {
			if act.TouchAction == scrcpy.ActionHoverMove {
				return true
			}
			pointerID = scrcpy.PointerIDGenericFinger
		}
		var pressure uint16
		if act.TouchAction == scrcpy.ActionDown || act.TouchAction == scrcpy.ActionMove {
			pressure = 0xFFFF
		}
		go func() {
			_ = s.InjectTouch(
				act.TouchAction, pointerID,
				act.VideoX, act.VideoY,
				uint16(vw), uint16(vh),
				pressure, act.Button, act.ButtonsHeld,
			)
		}()
		return true
	case ActionScroll:
		if m != nil {
			px := uint16(min(max(int(act.VideoX), 0), max(vw-1, 0)))
			startY := min(max(int(act.VideoY), 64), max(vh-64, 64))
			step := int(act.VScroll * 72)
			if step != 0 {
				go func() {
					_ = m.SendPointer(0, px, uint16(startY))
					cur := startY
					for range 4 {
						time.Sleep(14 * time.Millisecond)
						cur = min(max(cur+step/4, 0), max(vh-1, 0))
						_ = m.SendPointer(2, px, uint16(cur))
					}
					_ = m.SendPointer(1, px, uint16(cur))
				}()
			}
			return true
		}
		if s == nil {
			return false
		}
		go func() {
			_ = s.InjectScroll(
				act.VideoX, act.VideoY,
				uint16(vw), uint16(vh),
				act.HScroll, act.VScroll, act.ButtonsHeld,
			)
		}()
		return true
	case ActionKey:
		if s == nil {
			return false
		}
		if act.KeyAction == scrcpy.KeyActionDown &&
			ev.Key == ui.KeyV &&
			(ev.Mods&ui.Ctrl) != 0 {
			sw.mu.Lock()
			svc := sw.services
			sw.mu.Unlock()
			if svc != (ui.Services{}) {
				if clip := svc.ReadClipboard(); clip != "" {
					sw.SyncClipboardToDevice(clip)
				}
			}
		}
		go func() {
			_ = s.InjectKeycode(act.KeyAction, act.Keycode, 0, act.MetaState)
		}()
		return true
	case ActionText:
		if s == nil {
			return false
		}
		go func() {
			_ = s.InjectText(act.Text)
		}()
		return true
	}
	return false
}

func clampEven(v, lo, hi int) int {
	if v < lo {
		v = lo
	} else if v > hi {
		v = hi
	}
	return (v / resizeSnapPx) * resizeSnapPx
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

func codecName(id uint32) string {
	switch id {
	case core.CodecH265:
		return "H.265"
	case core.CodecH264:
		return "H.264"
	default:
		return "Video"
	}
}
