package gui

import (
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
	SessionModeMiracastDeX SessionMode = "miracast_dex"
	SessionModeDeXFallback SessionMode = "dex_fallback"
)

// SessionSpec configures a live interactive desktop stream window.
type SessionSpec struct {
	ID           string
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
}

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

	mu             sync.Mutex
	services       ui.Services
	scrcpySess     *scrcpy.Session
	dexSess        *dex.LoopbackSession
	miracastSess   *dex.MiracastSession
	audioPlayer    *core.AudioPlayer
	audioMuted     bool
	recording      bool
	recordPath     string
	lastClipText   string
	clipSeq        uint64
	noteTimer      *time.Timer
	touchMode      bool
	screenOff      bool
	hudCollapsed   bool
	focusHudNext   bool
	hudCloseHandle ui.Handle
	statusNote     string
	negotiatedW    int
	negotiatedH    int
	measuredFPS    float64
	avgDecodeUs    int64
	hwAccelerated  bool
	codecLabel     string
	surfaceBox     ui.Rect
	lastResizeBox  ui.Rect
	resizeTimer    *time.Timer
	closing        bool
}

func newSessionWindow(app *App, spec SessionSpec) *SessionWindow {
	sw := &SessionWindow{
		app:          app,
		spec:         spec,
		bmp:          ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1))),
		writing:      0,
		ready:        -1,
		shown:        -1,
		hudCollapsed: spec.Mode == SessionModeApp,
	}
	return sw
}

func (sw *SessionWindow) openWindow(w, h int) {
	opts := mygo.WindowOptions{
		Title:     sw.spec.Title,
		Width:     w,
		Height:    h,
		MinWidth:  sessionMinW,
		MinHeight: sessionMinH,
		Content:   ui.View(sw.view),
	}
	if sw.spec.Mode == SessionModeApp && runtime.GOOS == "windows" {
		opts.TitleBarStyle = mygo.TitleBarHidden
		opts.TitleBarHeight = int(appTitleBarH)
		opts.HideWindowButtons = true
		opts.MinWidth = appMinResizePx
		opts.MinHeight = appMinResizePx
	}
	win := mygo.NewWindow(opts)
	sw.mu.Lock()
	sw.win = win
	sw.mu.Unlock()

	win.OnClosed(func() {
		sw.Close()
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
	sw.openWindow(w, h)
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
	sw.openWindow(sessionDefW, sessionDefH)
	if m != nil {
		go func() {
			<-m.Done()
			sw.setStatusNote(L.SessionEnded())
		}()
	}
}

func (sw *SessionWindow) watchScrcpyEnd(s *scrcpy.Session) {
	<-s.Done()
	if err := s.Err(); err != nil {
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
	win := sw.win
	sw.mu.Unlock()
	if win != nil {
		win.Update(func() {})
	}
}

func (sw *SessionWindow) flashNote(note string) {
	sw.mu.Lock()
	if sw.noteTimer != nil {
		sw.noteTimer.Stop()
	}
	sw.statusNote = note
	win := sw.win
	sw.noteTimer = time.AfterFunc(2500*time.Millisecond, func() {
		sw.mu.Lock()
		if sw.statusNote == note {
			sw.statusNote = ""
		}
		w := sw.win
		sw.mu.Unlock()
		if w != nil {
			w.Update(func() {})
		}
	})
	sw.mu.Unlock()
	if win != nil {
		win.Update(func() {})
	}
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
	win := sw.win
	sw.mu.Unlock()

	if win != nil && !sw.updateQueued.Swap(true) {
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
	}()
}

// FocusWindow raises an already-open session window.
func (sw *SessionWindow) FocusWindow() {
	sw.mu.Lock()
	w := sw.win
	sw.mu.Unlock()
	if w != nil {
		w.Show()
		w.Focus()
	}
}

// view renders the telemetry HUD + interactive NV12 D3D11 surface.
func (sw *SessionWindow) view(c *ui.Context) {
	sw.present()

	k := tok(c)
	c.Root().Background(ui.Hex(stageBgHex))

	sw.mu.Lock()
	sw.services = c.Services()
	collapsed := sw.hudCollapsed
	focusHud := sw.focusHudNext
	sw.focusHudNext = false
	note := sw.statusNote
	negW, negH := sw.negotiatedW, sw.negotiatedH
	fps, decUs := sw.measuredFPS, sw.avgDecodeUs
	hw := sw.hwAccelerated
	codec := sw.codecLabel
	touchMode := sw.touchMode
	screenOff := sw.screenOff
	audioMuted := sw.audioMuted
	recording := sw.recording
	win := sw.win
	s := sw.scrcpySess
	sw.mu.Unlock()

	if codec == "" {
		codec = "H.264"
	}
	hwTag := "SW"
	if hw {
		hwTag = "HW"
	}
	resText := "--"
	if negW > 0 && negH > 0 {
		resText = fmt.Sprintf("%d×%d", negW, negH)
	}
	telem := fmt.Sprintf("%s · %s · %.1f fps · %s [%s] · %.1f ms",
		sw.spec.BackendLabel, resText, fps, codec, hwTag, float64(decUs)/1000.0)

	ui.Column(c.Key("session-root")).Fill().AlignItems(ui.Stretch).Children(func() {
		if sw.spec.Mode == SessionModeApp {
			sw.appTitleBar(c, k)
		}
		if !collapsed {
			ui.Row(c.Key("session-hud")).Height(hudHeight).Shrink(0).FillWidth().
				Background(ui.Hex(hudBgDark)).Padding(6, 12).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Row(c.Key("hud-transport-pill")).Padding(badgePadV, badgePadH).Radius(pillRadius).
					Background(k.brandSoft).Children(func() {
					ui.Text(c, sw.spec.Transport).FontSize(sizeSmall).TextColor(k.brand).Bold().SingleLine()
				})
				ui.Text(c, telem).FontSize(sizeSmall).TextColor(ui.Hex("#E6EEF2")).Grow(1).SingleLine().
					Tooltip(L.TelemetryTargetPrefix(sw.spec.TargetSpec))

				if s != nil {
					modeLbl := L.InputModeMouse()
					if touchMode {
						modeLbl = L.InputModeTouch()
					}
					if ui.Button(c.Key("hud-input-mode"), modeLbl).Clicked() {
						sw.mu.Lock()
						sw.touchMode = !sw.touchMode
						sw.mu.Unlock()
						sw.surface.Focus(c)
					}
					if ui.Button(c.Key("hud-back"), "").Tooltip(L.ActionBack()).Children(func() {
						ui.Icon(c, iconBack).Size(14, 14)
					}).Clicked() {
						go func() { _ = s.TapKeycode(scrcpy.KeycodeBack) }()
						sw.surface.Focus(c)
					}
					if ui.Button(c.Key("hud-home"), "").Tooltip(L.ActionHome()).Children(func() {
						ui.Icon(c, iconHome).Size(14, 14)
					}).Clicked() {
						go func() { _ = s.TapKeycode(scrcpy.KeycodeHome) }()
						sw.surface.Focus(c)
					}
					if ui.Button(c.Key("hud-recents"), "").Tooltip(L.ActionRecents()).Children(func() {
						ui.Icon(c, iconRecents).Size(14, 14)
					}).Clicked() {
						go func() { _ = s.TapKeycode(scrcpy.KeycodeAppSwitch) }()
						sw.surface.Focus(c)
					}
					if !sw.spec.IsDeX {
						if ui.Button(c.Key("hud-rotate"), "").Tooltip(L.ActionRotate()).Children(func() {
							ui.Icon(c, iconRotate).Size(14, 14)
						}).Clicked() {
							go func() { _ = s.RotateDevice() }()
							sw.surface.Focus(c)
						}
					}
					if sw.spec.ScreenOffCap {
						if ui.Button(c.Key("hud-screenoff"), "").Tooltip(L.ActionScreenOff()).Children(func() {
							ui.Icon(c, iconScreenOff).Size(14, 14)
						}).Clicked() {
							next := !screenOff
							sw.mu.Lock()
							sw.screenOff = next
							sw.mu.Unlock()
							go func() { _ = s.SetDisplayPower(!next) }()
							sw.surface.Focus(c)
						}
					}
					if ui.Button(c.Key("hud-shot"), "").Tooltip(L.ActionScreenshot()).Children(func() {
						ui.Icon(c, iconCamera).Size(14, 14)
					}).Clicked() {
						sw.Screenshot()
						sw.surface.Focus(c)
					}
					recTip := L.ActionRecordStart()
					if recording {
						recTip = L.ActionRecordStop()
					}
					if ui.Button(c.Key("hud-rec"), "").Tooltip(recTip).Children(func() {
						rcColor := ui.Hex("#CBD5E1")
						if recording {
							rcColor = k.bad
						}
						ui.Icon(c, iconRecord).Size(14, 14).TextColor(rcColor)
					}).Clicked() {
						sw.ToggleRecording()
						sw.surface.Focus(c)
					}
					if ui.Button(c.Key("hud-clip"), "").Tooltip(L.ActionSyncClipboard()).Children(func() {
						ui.Icon(c, iconClipboard).Size(14, 14)
					}).Clicked() {
						sw.SyncClipboardToDevice(c.ReadClipboard())
						sw.surface.Focus(c)
					}
					muteIcon := iconVolumeOn
					muteTip := L.ActionMuteAudio()
					if audioMuted {
						muteIcon = iconVolumeOff
						muteTip = L.ActionUnmuteAudio()
					}
					if ui.Button(c.Key("hud-audio"), "").Tooltip(muteTip).Children(func() {
						ui.Icon(c, muteIcon).Size(14, 14)
					}).Clicked() {
						sw.mu.Lock()
						sw.audioMuted = !sw.audioMuted
						sw.mu.Unlock()
						sw.surface.Focus(c)
					}
				}
				if win != nil {
					if ui.Button(c.Key("hud-fullscreen"), "").Tooltip(L.ActionFullscreen()).Children(func() {
						ui.Icon(c, iconFullscreen).Size(14, 14)
					}).Clicked() {
						win.SetFullScreen(!win.IsFullScreen())
						sw.surface.Focus(c)
					}
				}
				if ui.Button(c.Key("hud-hide"), L.HudHide()).Clicked() {
					sw.mu.Lock()
					sw.hudCollapsed = true
					sw.mu.Unlock()
					sw.surface.Focus(c)
				}
				if ui.Button(c.Key("hud-close"), L.ActionDisconnect()).Bind(&sw.hudCloseHandle).Clicked() {
					sw.Close()
				}
			})
			if focusHud {
				sw.hudCloseHandle.Focus(c)
			}
		}

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
					ui.Text(c, sw.spec.BackendLabel).Bold().FontSize(sizeHeading).TextColor(ui.Hex("#FFFFFF"))
					ui.Text(c, note).FontSize(sizeBody).TextColor(ui.Hex("#B8F1D2"))
				})
			}
			if collapsed && sw.spec.Mode != SessionModeApp {
				ui.Row(c.Key("hud-pill-open")).Absolute().Top(10).Right(10).Children(func() {
					if ui.Button(c.Key("hud-show"), L.HudShow()).Clicked() {
						sw.mu.Lock()
						sw.hudCollapsed = false
						sw.mu.Unlock()
						sw.surface.Focus(c)
					}
				})
			}
		})
	})
}

func (sw *SessionWindow) appTitleBar(c *ui.Context, k tokens) {
	t := c.Theme()
	sw.mu.Lock()
	s := sw.scrcpySess
	win := sw.win
	collapsed := sw.hudCollapsed
	audioMuted := sw.audioMuted
	recording := sw.recording
	sw.mu.Unlock()

	ui.Row(c.Key("app-titlebar")).Height(appTitleBarH).Shrink(0).FillWidth().
		Background(ui.Hex(hudBgDark)).Padding(0, 6, 0, 10).Gap(6).AlignItems(ui.Center).Children(func() {
		if sw.spec.AppIcon != nil {
			ui.Image(c, sw.spec.AppIcon).Size(appTitleIconSz, appTitleIconSz).Radius(4)
		}
		ui.Text(c, sw.spec.Title).Bold().FontSize(sizeBody).TextColor(ui.Hex("#F1F5F9")).SingleLine().DragWindow()
		ui.Box(c.Key("app-tb-drag")).Grow(1).FillHeight().DragWindow()

		if s != nil {
			if ui.ButtonBase(c.Key("app-tb-back")).Size(28, 26).Radius(6).Center().Tooltip(L.ActionBack()).Children(func() {
				ui.Icon(c, iconBack).Size(14, 14).TextColor(ui.Hex("#CBD5E1"))
			}).Clicked() {
				go func() { _ = s.TapKeycode(scrcpy.KeycodeBack) }()
				sw.surface.Focus(c)
			}
			if ui.ButtonBase(c.Key("app-tb-rotate")).Size(28, 26).Radius(6).Center().Tooltip(L.ActionRotate()).Children(func() {
				ui.Icon(c, iconRotate).Size(13, 13).TextColor(ui.Hex("#CBD5E1"))
			}).Clicked() {
				go func() { _ = s.RotateDevice() }()
				sw.surface.Focus(c)
			}
			if ui.ButtonBase(c.Key("app-tb-shot")).Size(28, 26).Radius(6).Center().Tooltip(L.ActionScreenshot()).Children(func() {
				ui.Icon(c, iconCamera).Size(13, 13).TextColor(ui.Hex("#CBD5E1"))
			}).Clicked() {
				sw.Screenshot()
				sw.surface.Focus(c)
			}
			recTip := L.ActionRecordStart()
			recColor := ui.Hex("#CBD5E1")
			if recording {
				recTip = L.ActionRecordStop()
				recColor = k.bad
			}
			if ui.ButtonBase(c.Key("app-tb-rec")).Size(28, 26).Radius(6).Center().Tooltip(recTip).Children(func() {
				ui.Icon(c, iconRecord).Size(12, 12).TextColor(recColor)
			}).Clicked() {
				sw.ToggleRecording()
				sw.surface.Focus(c)
			}
			muteIcon := iconVolumeOn
			muteTip := L.ActionMuteAudio()
			if audioMuted {
				muteIcon = iconVolumeOff
				muteTip = L.ActionUnmuteAudio()
			}
			if ui.ButtonBase(c.Key("app-tb-audio")).Size(28, 26).Radius(6).Center().Tooltip(muteTip).Children(func() {
				ui.Icon(c, muteIcon).Size(13, 13).TextColor(ui.Hex("#CBD5E1"))
			}).Clicked() {
				sw.mu.Lock()
				sw.audioMuted = !sw.audioMuted
				sw.mu.Unlock()
				sw.surface.Focus(c)
			}
		}
		hudTip := L.HudShow()
		if !collapsed {
			hudTip = L.HudHide()
		}
		if ui.ButtonBase(c.Key("app-tb-hud")).Size(28, 26).Radius(6).Center().Tooltip(hudTip).Children(func() {
			ui.Icon(c, iconDiagnostics).Size(13, 13).TextColor(k.brand)
		}).Clicked() {
			sw.mu.Lock()
			sw.hudCollapsed = !sw.hudCollapsed
			sw.mu.Unlock()
			sw.surface.Focus(c)
		}

		if runtime.GOOS == "windows" {
			btn := func(name string, glyph *ui.SVG) ui.Element {
				b := ui.ButtonBase(c.Key("app-win-"+name)).Size(32, 26).Radius(6).Center().Shrink(0).Label(name)
				fg := ui.Hex("#CBD5E1")
				if b.Hovered() {
					if name == "close" {
						b.Background(closeHover)
						fg = closeHoverText
					} else {
						b.Background(t.SurfaceHover)
					}
				}
				b.Children(func() { ui.Icon(c, glyph).Size(winGlyph, winGlyph).TextColor(fg) })
				return b
			}
			if btn("min", winMinimize).Clicked() && win != nil {
				win.Minimize()
			}
			maximized := win != nil && win.IsMaximized()
			maxGlyph := winMaximize
			if maximized {
				maxGlyph = winRestore
			}
			if btn("max", maxGlyph).MaximizeButton().Clicked() && win != nil {
				if maximized {
					win.Unmaximize()
				} else {
					win.Maximize()
				}
			}
			if btn("close", winClose).Clicked() {
				sw.Close()
			}
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
		if win != nil {
			win.SetFullScreen(!win.IsFullScreen())
		}
		return true
	case ActionReleaseFocus:
		sw.mu.Lock()
		sw.hudCollapsed = false
		sw.focusHudNext = true
		sw.statusNote = L.FocusReleasedHint()
		w := sw.win
		sw.mu.Unlock()
		if w != nil {
			w.Update(func() {})
		}
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
