package scrcpy

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/lease"

	"github.com/YuanZhe-99/AndroFuse/internal/sysproc"
)

// LaunchConfig configures a single scrcpy-server v5.0.1 session (Mode 1/2/3).
//
// Notes: DisplayID and NewDisplay are mutually exclusive on scrcpy-server CLI;
// StartAppPackage is sent over the control socket via TYPE_START_APP after attach.
type LaunchConfig struct {
	SessionID             string
	Serial                string
	DisplayID             int
	NewDisplay            string // e.g. "1280x800/160" or ""
	FlexDisplay           bool
	HideSystemDecorations bool
	KeepContentOnClose    bool
	StartAppPackage       string
	VideoSource           string // "" or "display", or "camera"
	CameraFacing          string // "front", "back", "external"
	CameraSize            string // e.g. "1280x720"
	CameraFPS             int
	AudioSource           string // "" or "output", or "mic"
	CodecID               uint32 // 0 defaults to CodecH264
	MaxSize               int
	MaxFPS                float64
	VideoBitRate          int
	EnableControl         bool
	EnableUHID            bool
	Audio                 bool
	InitialWidth          uint32
	InitialHeight         uint32
	OnFrame               func(core.NV12Frame)
	OnClipboard           func(string)
	OnAudioPCM            func([]byte)
}

// SessionStats captures honest runtime telemetry for a session (INV-05).
type SessionStats struct {
	DeviceName          string  `json:"device_name"`
	CodecID             uint32  `json:"codec_id"`
	NegotiatedWidth     uint32  `json:"negotiated_width"`
	NegotiatedHeight    uint32  `json:"negotiated_height"`
	SessionCount        int     `json:"session_count"`
	ClientResizedAck    bool    `json:"client_resized_ack"`
	PacketsReceived     int     `json:"packets_received"`
	FramesDecoded       int     `json:"frames_decoded"`
	AvgDecodeUs         int64   `json:"avg_decode_us"`
	MeasuredFPS         float64 `json:"measured_fps"`
	HardwareAccelerated bool    `json:"hardware_accelerated"`
}

// Session manages sockets, native decoding, control events, and teardown for one
// scrcpy-server v5.0.1 stream.
type Session struct {
	cfg        LaunchConfig
	adb        *adb.Client
	lib        *core.Lib
	leases     *lease.Store
	scid       uint32
	fwdPort    int
	serverCmd  *exec.Cmd
	videoConn  net.Conn
	audioConn  net.Conn
	ctrlConn   net.Conn
	decoder    *core.Decoder
	ring       core.NV12Ring
	lastConfig []byte
	accessUnit []byte
	readBuf    []byte
	audioBuf   []byte

	recMu    sync.Mutex
	recorder *MP4Recorder

	ctrlMu           sync.Mutex
	mu               sync.Mutex
	stats            SessionStats
	lastFrame        time.Time // when the last frame was decoded; Stats reports 0 fps once it is stale
	lastInput        time.Time // when the last touch, scroll or key went to the phone (stallGuard)
	lastErr          error
	doneCh           chan struct{}
	errCh            chan error
	closed           bool
	virtualDisplayID int
	vdReadyCh        chan struct{}
}

// BuildServerArgs constructs valid scrcpy-server v5.0.1 key=value options.
//
// Notes: Never combines display_id and new_display (scrcpy asserts mutual exclusion),
// and omits client-only CLI flags like mouse=uhid or start_app.
func BuildServerArgs(scid uint32, cfg LaunchConfig) []string {
	codecName := "h264"
	if cfg.CodecID == CodecH265 {
		codecName = "h265"
	}
	bitRate := cfg.VideoBitRate
	if bitRate <= 0 {
		bitRate = 8_000_000
	}
	ctrl := cfg.EnableControl
	if cfg.EnableUHID || cfg.FlexDisplay || cfg.StartAppPackage != "" {
		ctrl = true
	}
	isCamera := cfg.VideoSource == "camera"
	if isCamera {
		ctrl = false
	}

	args := []string{
		ServerVersion,
		fmt.Sprintf("scid=%08x", scid),
		"log_level=info",
		"tunnel_forward=true",
		"audio=" + strconv.FormatBool(cfg.Audio),
		"video=true",
		"control=" + strconv.FormatBool(ctrl),
		"video_codec=" + codecName,
		fmt.Sprintf("video_bit_rate=%d", bitRate),
		"cleanup=true",
	}
	if isCamera {
		args = append(args, "video_source=camera")
		if cfg.CameraFacing != "" {
			args = append(args, "camera_facing="+cfg.CameraFacing)
		}
		if cfg.CameraSize != "" {
			args = append(args, "camera_size="+cfg.CameraSize)
		}
		if cfg.CameraFPS > 0 {
			args = append(args, fmt.Sprintf("camera_fps=%d", cfg.CameraFPS))
		}
	} else {
		// Independent app windows wake only their own virtual display via AppGuard so Display 0 stays dark.
		powerOn := cfg.NewDisplay == "" || cfg.StartAppPackage == ""
		args = append(args, "power_on="+strconv.FormatBool(powerOn))
	}
	if cfg.Audio {
		args = append(args, "audio_codec=raw")
		if cfg.AudioSource != "" {
			args = append(args, "audio_source="+cfg.AudioSource)
		}
	}
	if !isCamera {
		if cfg.MaxSize > 0 {
			args = append(args, fmt.Sprintf("max_size=%d", cfg.MaxSize))
		}
		if cfg.MaxFPS > 0 {
			args = append(args, fmt.Sprintf("max_fps=%.2f", cfg.MaxFPS))
		}
		if cfg.NewDisplay != "" {
			args = append(args, "new_display="+cfg.NewDisplay)
			if cfg.FlexDisplay {
				args = append(args, "flex_display=true")
			}
			if cfg.HideSystemDecorations {
				args = append(args, "vd_system_decorations=false")
			}
			if cfg.KeepContentOnClose {
				args = append(args, "vd_destroy_content=false")
			}
		} else if cfg.DisplayID > 0 {
			args = append(args, fmt.Sprintf("display_id=%d", cfg.DisplayID))
		}
	}
	return args
}

// StartADB pushes scrcpy-server-v5.0.1, acquires its lease, forwards the abstract
// socket, launches app_process, and connects directly to TCP without spawning scrcpy.exe.
func StartADB(ctx context.Context, client *adb.Client, lib *core.Lib, store *lease.Store, cfg LaunchConfig) (*Session, error) {
	if client == nil || lib == nil {
		return nil, errors.New("adb client and core library are required")
	}
	serverPath, err := FindCachedServer()
	if err != nil {
		return nil, err
	}
	scid, err := RandomSCID()
	if err != nil {
		return nil, err
	}
	if cfg.CodecID == 0 {
		cfg.CodecID = CodecH264
	}
	if cfg.SessionID == "" {
		cfg.SessionID = fmt.Sprintf("scrcpy-%08x", scid)
	}
	if err := client.PushFile(ctx, cfg.Serial, serverPath, RemoteSessionServerPath); err != nil {
		return nil, fmt.Errorf("push scrcpy-server: %w", err)
	}

	port, err := client.ForwardTCP(ctx, cfg.Serial, 0, SocketName(scid))
	if err != nil {
		return nil, fmt.Errorf("forward %s: %w", SocketName(scid), err)
	}
	if store != nil {
		kind := lease.KindMirror
		if cfg.NewDisplay != "" {
			kind = lease.KindVirtualDisplay
		}
		if err := store.Acquire(lease.Lease{
			SessionID:   cfg.SessionID,
			DeviceHash:  adb.DeviceHash(cfg.Serial),
			Kind:        kind,
			DisplayID:   cfg.DisplayID,
			ForwardPort: port,
			AppPackage:  cfg.StartAppPackage,
		}); err != nil {
			_ = client.RemoveForward(context.Background(), cfg.Serial, port)
			return nil, err
		}
	}

	serverArgs := BuildServerArgs(scid, cfg)
	shellCmd := fmt.Sprintf("CLASSPATH=%s app_process / com.genymobile.scrcpy.Server %s",
		RemoteSessionServerPath, strings.Join(serverArgs, " "))
	cmd := exec.Command(client.Path, "-s", cfg.Serial, "shell", shellCmd)
	sysproc.HideConsole(cmd)
	var outBuf serverLogWriter
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	if err := cmd.Start(); err != nil {
		_ = client.RemoveForward(context.Background(), cfg.Serial, port)
		if store != nil {
			_ = store.Release(cfg.SessionID)
		}
		return nil, fmt.Errorf("start scrcpy-server: %w", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	videoConn, err := dialWithRetry(ctx, addr, 10*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = client.RemoveForward(context.Background(), cfg.Serial, port)
		if store != nil {
			_ = store.Release(cfg.SessionID)
		}
		return nil, fmt.Errorf("connect video socket (%s): %w (%s)", addr, err, outBuf.String())
	}

	var audioConn net.Conn
	if cfg.Audio {
		audioConn, err = net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			_ = videoConn.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			_ = client.RemoveForward(context.Background(), cfg.Serial, port)
			if store != nil {
				_ = store.Release(cfg.SessionID)
			}
			return nil, fmt.Errorf("connect audio socket: %w", err)
		}
	}

	ctrlNeeded := cfg.VideoSource != "camera" && (cfg.EnableControl || cfg.EnableUHID || cfg.FlexDisplay || cfg.StartAppPackage != "")
	var ctrlConn net.Conn
	if ctrlNeeded {
		ctrlConn, err = net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			if audioConn != nil {
				_ = audioConn.Close()
			}
			_ = videoConn.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			_ = client.RemoveForward(context.Background(), cfg.Serial, port)
			if store != nil {
				_ = store.Release(cfg.SessionID)
			}
			return nil, fmt.Errorf("connect control socket: %w", err)
		}
	}

	s, err := StartFromConns(lib, store, client, scid, port, cmd, videoConn, ctrlConn, true, cfg)
	if err != nil {
		if audioConn != nil {
			_ = audioConn.Close()
		}
		return nil, err
	}
	outBuf.attachSession(s)
	if audioConn != nil {
		s.mu.Lock()
		s.audioConn = audioConn
		s.mu.Unlock()
		go s.audioLoop()
	}
	return s, nil
}

// StartFromConns attaches a Session over already-opened video/control sockets
// (used by both live ADB sessions and offline MockServer replay tests).
func StartFromConns(
	lib *core.Lib,
	store *lease.Store,
	client *adb.Client,
	scid uint32,
	fwdPort int,
	cmd *exec.Cmd,
	videoConn net.Conn,
	ctrlConn net.Conn,
	hasDummyByte bool,
	cfg LaunchConfig,
) (*Session, error) {
	hs, err := ReadVideoHandshake(videoConn, hasDummyByte)
	if err != nil {
		if ctrlConn != nil {
			_ = ctrlConn.Close()
		}
		_ = videoConn.Close()
		return nil, err
	}
	w, h := cfg.InitialWidth, cfg.InitialHeight
	if w == 0 || h == 0 {
		w, h = 1280, 720
	}
	dec, err := lib.OpenDecoder(hs.CodecID, w, h)
	if err != nil {
		if ctrlConn != nil {
			_ = ctrlConn.Close()
		}
		_ = videoConn.Close()
		return nil, err
	}

	s := &Session{
		cfg:       cfg,
		adb:       client,
		lib:       lib,
		leases:    store,
		scid:      scid,
		fwdPort:   fwdPort,
		serverCmd: cmd,
		videoConn: videoConn,
		ctrlConn:  ctrlConn,
		decoder:   dec,
		stats: SessionStats{
			DeviceName: hs.DeviceName,
			CodecID:    hs.CodecID,
		},
		doneCh:    make(chan struct{}),
		errCh:     make(chan error, 1),
		vdReadyCh: make(chan struct{}),
	}

	if ctrlConn != nil {
		if cfg.EnableUHID {
			if err := s.SendControl(EncodeUhidCreate(1, 0x18d1, 0xaf01, "AndroFuse Mouse", UHIDMouseReportDesc)); err != nil {
				s.Close()
				return nil, err
			}
			if err := s.SendControl(EncodeUhidCreate(2, 0x18d1, 0xaf02, "AndroFuse Keyboard", UHIDKeyboardReportDesc)); err != nil {
				s.Close()
				return nil, err
			}
		}
		if cfg.StartAppPackage != "" {
			if err := s.SendControl(EncodeStartApp(cfg.StartAppPackage)); err != nil {
				s.Close()
				return nil, err
			}
		}
		go s.controlReadLoop()
		if cfg.StartAppPackage != "" {
			go s.stallGuard()
		}
	}

	go s.videoLoop()
	return s, nil
}

// stallGuard revives an app window whose picture stopped: when nothing has decoded 3 s after the
// stream started, or input went to the phone and no frame followed within 1.5 s, it asks for a
// fresh encoder session (TYPE_RESET_VIDEO), at most once every 5 s.
// Notes: on Samsung One UI an app window opened while the phone slept could stop drawing until the
// phone was locked and unlocked; a capture reset makes the daemon (or, over adb,
// repowerVirtualDisplay) power-cycle the virtual display, which brings the picture back.
func (s *Session) stallGuard() {
	started := time.Now()
	var lastReset time.Time
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.doneCh:
			return
		case now := <-t.C:
			s.mu.Lock()
			frame, input := s.lastFrame, s.lastInput
			s.mu.Unlock()
			if stalled(now, started, frame, input) && now.Sub(lastReset) >= 5*time.Second {
				lastReset = now
				_ = s.SendControl(EncodeResetVideo())
			}
		}
	}
}

// stalled reports whether an app window's picture looks stuck at now: no frame 3 s after the start,
// or input 1.5 s ago that no frame has followed.
func stalled(now, started, lastFrame, lastInput time.Time) bool {
	if lastFrame.IsZero() {
		return now.Sub(started) >= 3*time.Second
	}
	return lastInput.After(lastFrame) && now.Sub(lastInput) >= 1500*time.Millisecond
}

// SendControl writes one encoded control packet on the scrcpy control socket.
func (s *Session) SendControl(pkt []byte) error {
	s.mu.Lock()
	c := s.ctrlConn
	s.mu.Unlock()
	if c == nil {
		return errors.New("control socket unavailable")
	}
	if len(pkt) > 0 && (pkt[0] == MsgInjectKeycode || pkt[0] == MsgInjectTouch || pkt[0] == MsgInjectScroll) {
		s.mu.Lock()
		s.lastInput = time.Now()
		s.mu.Unlock()
	}
	s.ctrlMu.Lock()
	defer s.ctrlMu.Unlock()
	_ = c.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	_, err := c.Write(pkt)
	_ = c.SetWriteDeadline(time.Time{})
	return err
}

// Resize requests a dynamic flex-display size update (TYPE_RESIZE_DISPLAY).
func (s *Session) Resize(width, height uint16) error {
	if err := s.SendControl(EncodeResizeDisplay(width, height)); err != nil {
		return err
	}
	// A flex resize restarts capture without logging it.
	s.repowerVirtualDisplay(500 * time.Millisecond)
	return nil
}

// repowerVirtualDisplay power-cycles this direct ADB session's app-window virtual display through the
// display service (`cmd display power-off`, then `power-reset`) after delay; a no-op for other
// sessions (the Enhanced mode daemon does the same itself, Daemon.java).
// Notes: on Samsung One UI (Galaxy Z Fold8, Android 17), when an app window opened while the phone
// slept, every capture reset (a flex resize, or RESET_VIDEO) left SurfaceFlinger's copy of the
// display powered off while the display service still counted it on, so the window drew nothing
// until the phone was locked and unlocked.
// Side effects: one `adb shell` on the session's device.
func (s *Session) repowerVirtualDisplay(delay time.Duration) {
	id := s.VirtualDisplayID()
	if s.adb == nil || id <= 0 || s.cfg.StartAppPackage == "" {
		return
	}
	go func() {
		select {
		case <-s.doneCh:
			return
		case <-time.After(delay):
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.adb.Shell(ctx, s.cfg.Serial, fmt.Sprintf("cmd display power-off %d; cmd display power-reset %d", id, id))
	}()
}

// InjectTouch sends an absolute touch or mouse MotionEvent onto the session display (INV-12).
func (s *Session) InjectTouch(action byte, pointerID uint64, x, y int32, screenW, screenH, pressure uint16, actionButton, buttons uint32) error {
	return s.SendControl(EncodeInjectTouch(action, pointerID, x, y, screenW, screenH, pressure, actionButton, buttons))
}

// InjectScroll sends a horizontal/vertical scroll event at (x, y) on the session display.
func (s *Session) InjectScroll(x, y int32, screenW, screenH uint16, hScroll, vScroll float64, buttons uint32) error {
	return s.SendControl(EncodeInjectScroll(x, y, screenW, screenH, hScroll, vScroll, buttons))
}

// InjectKeycode sends a key down/up event with Android meta modifier flags.
func (s *Session) InjectKeycode(action byte, keycode, repeat, metaState uint32) error {
	return s.SendControl(EncodeInjectKeycode(action, keycode, repeat, metaState))
}

// TapKeycode sends a press and release for a single Android keycode (e.g. Back, Home, Recents).
func (s *Session) TapKeycode(keycode uint32) error {
	if err := s.InjectKeycode(KeyActionDown, keycode, 0, 0); err != nil {
		return err
	}
	return s.InjectKeycode(KeyActionUp, keycode, 0, 0)
}

// InjectText inserts UTF-8 text via scrcpy TYPE_INJECT_TEXT (1).
func (s *Session) InjectText(text string) error {
	if text == "" {
		return nil
	}
	return s.SendControl(EncodeInjectText(text))
}

// SetClipboard pushes UTF-8 text to the Android device clipboard via TYPE_SET_CLIPBOARD (9).
func (s *Session) SetClipboard(seq uint64, paste bool, text string) error {
	return s.SendControl(EncodeSetClipboard(seq, paste, text))
}

// StartRecording starts direct H.264 fragmented-MP4 recording at path without re-encoding.
func (s *Session) StartRecording(path string) error {
	st := s.Stats()
	if st.CodecID != 0 && st.CodecID != CodecH264 {
		return fmt.Errorf("direct MP4 recording requires H.264 stream (got codec 0x%08x)", st.CodecID)
	}
	rec, err := NewMP4Recorder(path, st.NegotiatedWidth, st.NegotiatedHeight)
	if err != nil {
		return err
	}
	s.mu.Lock()
	cfg := append([]byte(nil), s.lastConfig...)
	s.mu.Unlock()
	if len(cfg) > 0 {
		_ = rec.WriteAnnexB(cfg, 0, true, false)
	}

	s.recMu.Lock()
	if s.recorder != nil {
		_ = s.recorder.Close()
	}
	s.recorder = rec
	s.recMu.Unlock()
	_ = s.SendControl(EncodeResetVideo())
	return nil
}

// StopRecording flushes and closes the active MP4 recording, if any.
func (s *Session) StopRecording() error {
	s.recMu.Lock()
	rec := s.recorder
	s.recorder = nil
	s.recMu.Unlock()
	if rec == nil {
		return nil
	}
	return rec.Close()
}

// IsRecording reports whether direct MP4 recording is currently active.
func (s *Session) IsRecording() bool {
	s.recMu.Lock()
	active := s.recorder != nil
	s.recMu.Unlock()
	return active
}

// SetDisplayPower toggles the device's physical panel power while keeping mirroring active.
func (s *Session) SetDisplayPower(on bool) error {
	return s.SendControl(EncodeSetDisplayPower(on))
}

// RotateDevice requests device orientation rotation via TYPE_ROTATE_DEVICE (11).
func (s *Session) RotateDevice() error {
	return s.SendControl(EncodeRotateDevice())
}

// MoveUHIDMouse injects a relative HID mouse movement packet onto UHID device 1.
func (s *Session) MoveUHIDMouse(buttons uint8, dx, dy, wheel int8) error {
	return s.SendControl(EncodeUhidInput(1, []byte{buttons & 0x1F, byte(dx), byte(dy), byte(wheel)}))
}

// Done closes when the video read loop exits.
func (s *Session) Done() <-chan struct{} {
	return s.doneCh
}

// Err returns the terminal error of the video loop once Done() closes (nil on clean EOF/close).
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if errors.Is(s.lastErr, io.EOF) {
		return nil
	}
	return s.lastErr
}

// Stats returns a snapshot of honest runtime video telemetry (INV-05).
// Notes: scrcpy sends no frames while the screen is still, so MeasuredFPS drops to 0 once no frame
// has arrived for a second instead of repeating the rate of the last burst.
func (s *Session) Stats() SessionStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stats
	if time.Since(s.lastFrame) > time.Second {
		st.MeasuredFPS = 0
	}
	return st
}

// WaitDecoded blocks until at least minFrames are decoded or timeout expires.
func (s *Session) WaitDecoded(minFrames int, timeout time.Duration) (SessionStats, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st := s.Stats()
		if st.FramesDecoded >= minFrames {
			return st, nil
		}
		select {
		case err := <-s.errCh:
			if err != nil && !errors.Is(err, io.EOF) {
				return st, err
			}
			return s.Stats(), nil
		default:
		}
		time.Sleep(15 * time.Millisecond)
	}
	st := s.Stats()
	if st.FramesDecoded >= minFrames {
		return st, nil
	}
	return st, fmt.Errorf("timed out waiting for %d decoded frames (got %d packets, %d decoded)", minFrames, st.PacketsReceived, st.FramesDecoded)
}

// Close tears down sockets, waits for scrcpy-server cleanup, removes ADB port forwards,
// destroys the native decoder, and releases the session lease.
func (s *Session) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	ctrl := s.ctrlConn
	aud := s.audioConn
	vid := s.videoConn
	dec := s.decoder
	cmd := s.serverCmd
	s.mu.Unlock()

	_ = s.StopRecording()
	if ctrl != nil {
		_ = ctrl.Close()
	}
	if aud != nil {
		_ = aud.Close()
	}
	if vid != nil {
		_ = vid.Close()
	}
	<-s.doneCh

	if cmd != nil && cmd.Process != nil {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	if dec != nil {
		dec.Close()
	}
	if s.adb != nil && s.fwdPort > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.adb.RemoveForward(ctx, s.cfg.Serial, s.fwdPort)
		cancel()
	}
	if s.leases != nil && s.cfg.SessionID != "" {
		_ = s.leases.Release(s.cfg.SessionID)
	}
}

func (s *Session) controlReadLoop() {
	for {
		msg, err := ReadDeviceMessage(s.ctrlConn)
		if err != nil {
			return
		}
		if msg.Type == 0 && s.cfg.OnClipboard != nil {
			s.cfg.OnClipboard(msg.Text)
		}
	}
}

func (s *Session) audioLoop() {
	s.mu.Lock()
	conn := s.audioConn
	s.mu.Unlock()
	if conn == nil {
		return
	}
	defer conn.Close()

	var codecBuf [4]byte
	if _, err := io.ReadFull(conn, codecBuf[:]); err != nil {
		return
	}
	codecID := binary.BigEndian.Uint32(codecBuf[:])
	// scrcpy-server writes 0x00000000 (disabled) or 0x00000001 (errored) when audio is unavailable.
	if codecID != CodecRaw {
		return
	}
	for {
		pkt, err := ReadVideoPacket(conn, &s.audioBuf)
		if err != nil {
			return
		}
		if pkt.IsConfig || len(pkt.Payload) == 0 {
			continue
		}
		if s.cfg.OnAudioPCM != nil {
			s.cfg.OnAudioPCM(pkt.Payload)
		}
	}
}

func (s *Session) videoLoop() {
	defer close(s.doneCh)
	started := time.Now()
	winStart := started
	var winFrames int
	var totalDecodeUs int64

	recordFrame := func(frame core.NV12Frame) {
		totalDecodeUs += int64(frame.DecodeUs)
		winFrames++
		now := time.Now()
		s.mu.Lock()
		s.stats.FramesDecoded++
		s.lastFrame = now
		if frame.Width > 0 && frame.Height > 0 {
			s.stats.NegotiatedWidth = uint32(frame.Width)
			s.stats.NegotiatedHeight = uint32(frame.Height)
		}
		s.stats.HardwareAccelerated = frame.Hardware
		if s.stats.FramesDecoded > 0 {
			s.stats.AvgDecodeUs = totalDecodeUs / int64(s.stats.FramesDecoded)
		}
		if dt := now.Sub(winStart).Seconds(); dt >= 0.5 {
			s.stats.MeasuredFPS = float64(winFrames) / dt
			winStart = now
			winFrames = 0
		} else if tot := now.Sub(started).Seconds(); tot > 0 && s.stats.MeasuredFPS == 0 {
			s.stats.MeasuredFPS = float64(s.stats.FramesDecoded) / tot
		}
		s.mu.Unlock()
		if s.cfg.OnFrame != nil {
			s.cfg.OnFrame(frame)
		}
	}

	failLoop := func(err error) {
		s.mu.Lock()
		s.lastErr = err
		s.mu.Unlock()
		select {
		case s.errCh <- err:
		default:
		}
	}

	for {
		pkt, err := ReadVideoPacket(s.videoConn, &s.readBuf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = s.decoder.Flush()
				if frame, ok, pErr := s.decoder.PullInto(&s.ring); pErr == nil && ok {
					recordFrame(frame)
				}
			}
			failLoop(err)
			return
		}
		if pkt.IsSession {
			s.mu.Lock()
			s.stats.NegotiatedWidth = pkt.Width
			s.stats.NegotiatedHeight = pkt.Height
			s.stats.SessionCount++
			if pkt.ClientResized {
				s.stats.ClientResizedAck = true
			}
			s.mu.Unlock()
			continue
		}

		s.mu.Lock()
		s.stats.PacketsReceived++
		s.mu.Unlock()

		if pkt.IsConfig {
			s.lastConfig = append(s.lastConfig[:0], pkt.Payload...)
			s.recMu.Lock()
			if s.recorder != nil {
				st := s.Stats()
				s.recorder.SetDimensions(st.NegotiatedWidth, st.NegotiatedHeight)
				_ = s.recorder.WriteAnnexB(pkt.Payload, pkt.PtsUs, true, false)
			}
			s.recMu.Unlock()
			continue
		}

		s.recMu.Lock()
		if s.recorder != nil {
			_ = s.recorder.WriteAnnexB(pkt.Payload, pkt.PtsUs, false, pkt.IsKeyframe)
		}
		s.recMu.Unlock()

		s.accessUnit = s.accessUnit[:0]
		if pkt.IsKeyframe && len(s.lastConfig) > 0 && !bytes.HasPrefix(pkt.Payload, s.lastConfig) {
			s.accessUnit = append(s.accessUnit, s.lastConfig...)
		}
		s.accessUnit = append(s.accessUnit, pkt.Payload...)

		if err := s.decoder.Push(s.accessUnit, pkt.PtsUs, pkt.IsKeyframe); err != nil {
			failLoop(err)
			return
		}
		frame, ok, err := s.decoder.PullInto(&s.ring)
		if err != nil {
			failLoop(err)
			return
		}
		if ok {
			recordFrame(frame)
		}
	}
}

func dialWithRetry(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			// Wait briefly for the first readable byte (dummy byte) because adb forward accepts immediately.
			_ = c.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
			if tcp, ok := c.(*net.TCPConn); ok {
				_ = tcp.SetNoDelay(true)
			}
			var peek [1]byte
			_, pErr := c.Read(peek[:])
			if pErr == nil {
				_ = c.SetReadDeadline(time.Time{})
				// Wrap conn with already-consumed first byte preserved.
				return &prefixedConn{Conn: c, first: peek[0], unread: true}, nil
			}
			_ = c.Close()
			lastErr = pErr
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("scrcpy socket not ready: %w", lastErr)
}

type prefixedConn struct {
	net.Conn
	first  byte
	unread bool
}

func (p *prefixedConn) Read(b []byte) (int, error) {
	if p.unread && len(b) > 0 {
		b[0] = p.first
		p.unread = false
		if len(b) == 1 {
			return 1, nil
		}
		n, err := p.Conn.Read(b[1:])
		return n + 1, err
	}
	return p.Conn.Read(b)
}

var newDisplayIDPattern = regexp.MustCompile(`New display: .*?\(id=(\d+)\)`)

func parseNewDisplayID(line string) (int, bool) {
	m := newDisplayIDPattern.FindStringSubmatch(line)
	if len(m) != 2 {
		return 0, false
	}
	id, err := strconv.Atoi(m[1])
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (s *Session) setVirtualDisplayID(id int) {
	if id <= 0 {
		return
	}
	s.mu.Lock()
	if s.virtualDisplayID == 0 {
		s.virtualDisplayID = id
		if s.vdReadyCh != nil {
			close(s.vdReadyCh)
		}
	}
	s.mu.Unlock()
}

// VirtualDisplayID returns the virtual display ID logged by scrcpy-server `--new-display`,
// or 0 if the session uses an existing display or has not parsed the log line yet.
func (s *Session) VirtualDisplayID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.virtualDisplayID
}

// WaitVirtualDisplayID waits until scrcpy-server logs a new virtual display ID, the session
// ends, or timeout expires.
func (s *Session) WaitVirtualDisplayID(timeout time.Duration) (int, bool) {
	s.mu.Lock()
	id := s.virtualDisplayID
	ch := s.vdReadyCh
	done := s.doneCh
	s.mu.Unlock()
	if id > 0 {
		return id, true
	}
	if ch == nil {
		return 0, false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return s.VirtualDisplayID(), s.VirtualDisplayID() > 0
	case <-done:
		return s.VirtualDisplayID(), s.VirtualDisplayID() > 0
	case <-timer.C:
		return 0, false
	}
}

type serverLogWriter struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	lineBuf bytes.Buffer
	dispID  int
	sess    *Session
}

func (w *serverLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if w.buf.Len() < 8192 {
		rem := 8192 - w.buf.Len()
		if len(p) > rem {
			w.buf.Write(p[:rem])
		} else {
			w.buf.Write(p)
		}
	}
	var target *Session
	var foundID int
	var reset *Session
	for _, b := range p {
		if b == '\n' {
			if id, ok := parseNewDisplayID(w.lineBuf.String()); ok && w.dispID == 0 {
				w.dispID = id
				foundID = id
				target = w.sess
			}
			if strings.Contains(w.lineBuf.String(), "Video capture reset") {
				reset = w.sess
			}
			w.lineBuf.Reset()
		} else if b != '\r' {
			w.lineBuf.WriteByte(b)
		}
	}
	w.mu.Unlock()
	if target != nil && foundID > 0 {
		target.setVirtualDisplayID(foundID)
	}
	if reset != nil {
		reset.repowerVirtualDisplay(150 * time.Millisecond)
	}
	return len(p), nil
}

func (w *serverLogWriter) attachSession(s *Session) {
	if s == nil {
		return
	}
	w.mu.Lock()
	w.sess = s
	id := w.dispID
	if id == 0 && w.lineBuf.Len() > 0 {
		if parsed, ok := parseNewDisplayID(w.lineBuf.String()); ok {
			w.dispID = parsed
			id = parsed
		}
	}
	w.mu.Unlock()
	if id > 0 {
		s.setVirtualDisplayID(id)
	}
}

func (w *serverLogWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.TrimSpace(w.buf.String())
}
