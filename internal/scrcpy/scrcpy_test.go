package scrcpy

import (
	"bytes"
	"context"
	"image"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/lease"
)

func TestBuildServerArgsAndEncoders(t *testing.T) {
	args := BuildServerArgs(0x1234abcd, LaunchConfig{
		NewDisplay:            "1280x800/160",
		FlexDisplay:           true,
		HideSystemDecorations: true,
		KeepContentOnClose:    true,
		StartAppPackage:       "com.sec.android.app.clockpackage",
		DisplayID:             4, // must be ignored when NewDisplay is set
	})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "display_id=") {
		t.Fatalf("unexpected display_id alongside new_display: %s", joined)
	}
	if strings.Contains(joined, "start_app=") || strings.Contains(joined, "uhid") {
		t.Fatalf("unexpected control-only flag in server args: %s", joined)
	}
	if !strings.Contains(joined, "power_on=false") || strings.Contains(joined, "power_on=true") {
		t.Fatalf("expected power_on=false for independent app virtual display: %s", joined)
	}
	mirrorJoined := strings.Join(BuildServerArgs(0x1234abcd, LaunchConfig{}), " ")
	if !strings.Contains(mirrorJoined, "power_on=true") {
		t.Fatalf("expected power_on=true for phone mirror: %s", mirrorJoined)
	}
	desktopJoined := strings.Join(BuildServerArgs(0x1234abcd, LaunchConfig{NewDisplay: "1920x1080/160"}), " ")
	if !strings.Contains(desktopJoined, "power_on=true") {
		t.Fatalf("expected power_on=true for virtual desktop without start_app: %s", desktopJoined)
	}
	if !strings.Contains(joined, "new_display=1280x800/160") ||
		!strings.Contains(joined, "flex_display=true") ||
		!strings.Contains(joined, "vd_system_decorations=false") ||
		!strings.Contains(joined, "vd_destroy_content=false") {
		t.Fatalf("missing virtual display args: %s", joined)
	}

	uhid := EncodeUhidCreate(1, 0x18d1, 0xaf01, "mouse", UHIDMouseReportDesc)
	if uhid[0] != MsgUhidCreate || len(uhid) != 10+len("mouse")+len(UHIDMouseReportDesc) {
		t.Fatalf("unexpected UhidCreate encoding len=%d", len(uhid))
	}
	rsz := EncodeResizeDisplay(1440, 900)
	if !bytes.Equal(rsz, []byte{MsgResizeDisplay, 0x05, 0xa0, 0x03, 0x84}) {
		t.Fatalf("unexpected ResizeDisplay bytes: %x", rsz)
	}
	appPkt := EncodeStartApp("+com.sec.android.app.clockpackage")
	if appPkt[0] != MsgStartApp || appPkt[1] != 33 {
		t.Fatalf("unexpected StartApp packet header: %x", appPkt[:2])
	}

	camArgs := BuildServerArgs(0xabcdef01, LaunchConfig{
		VideoSource:   "camera",
		CameraFacing:  "back",
		CameraSize:    "1280x720",
		CameraFPS:     30,
		Audio:         true,
		AudioSource:   "mic",
		EnableControl: true, // must be forced false for camera stream
		DisplayID:     2,
	})
	camJoined := strings.Join(camArgs, " ")
	if !strings.Contains(camJoined, "video_source=camera") ||
		!strings.Contains(camJoined, "camera_facing=back") ||
		!strings.Contains(camJoined, "camera_size=1280x720") ||
		!strings.Contains(camJoined, "camera_fps=30") ||
		!strings.Contains(camJoined, "audio_source=mic") ||
		!strings.Contains(camJoined, "control=false") {
		t.Fatalf("missing camera/mic server args: %s", camJoined)
	}
	if strings.Contains(camJoined, "display_id=") ||
		strings.Contains(camJoined, "new_display=") ||
		strings.Contains(camJoined, "power_on=true") {
		t.Fatalf("unexpected display/power args in camera mode: %s", camJoined)
	}

	keyPkt := EncodeInjectKeycode(KeyActionDown, KeycodeBack, 0, MetaCtrlOn)
	if len(keyPkt) != 14 || keyPkt[0] != MsgInjectKeycode || keyPkt[1] != KeyActionDown {
		t.Fatalf("unexpected InjectKeycode packet: %x", keyPkt)
	}
	touchPkt := EncodeInjectTouch(ActionDown, PointerIDMouse, 640, 360, 1920, 1080, 0xFFFF, ButtonSecondary, ButtonSecondary)
	if len(touchPkt) != 32 || touchPkt[0] != MsgInjectTouch || touchPkt[2] != 0xFF || touchPkt[9] != 0xFF {
		t.Fatalf("unexpected InjectTouch packet: %x", touchPkt)
	}
	scrollPkt := EncodeInjectScroll(640, 360, 1920, 1080, 0, -1, 0)
	if len(scrollPkt) != 21 || scrollPkt[0] != MsgInjectScroll || scrollPkt[15] != 0x80 || scrollPkt[16] != 0x01 {
		t.Fatalf("unexpected InjectScroll packet: %x", scrollPkt)
	}
	txtPkt := EncodeInjectText("你好")
	if txtPkt[0] != MsgInjectText || txtPkt[4] != 6 || string(txtPkt[5:]) != "你好" {
		t.Fatalf("unexpected InjectText packet: %x", txtPkt)
	}

	var w serverLogWriter
	s := &Session{vdReadyCh: make(chan struct{}), doneCh: make(chan struct{})}
	_, _ = w.Write([]byte("[server] INFO: Device: Samsung\n[server] INFO: New display: 640x1120/220 (id=14)\n"))
	w.attachSession(s)
	if id, ok := s.WaitVirtualDisplayID(time.Second); !ok || id != 14 {
		t.Fatalf("WaitVirtualDisplayID = (%d, %v), want (14, true)", id, ok)
	}
}

func TestReplaySanitizedFixtureWithMyGoUpdateYUV(t *testing.T) {
	libPath, err := filepath.Abs(filepath.Join("..", "..", "core", "target", "debug", core.LibraryName()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(libPath); err != nil {
		t.Skipf("built %s not present: %v", core.LibraryName(), err)
	}

	lib, err := core.Open(libPath)
	if err != nil {
		t.Fatalf("core.Open: %v", err)
	}
	defer lib.Stop()
	if _, err := lib.Start(core.Config{Version: "0.1.0", StateDir: t.TempDir(), Profile: "test"}); err != nil {
		t.Fatalf("core.Start: %v", err)
	}

	fix, err := LoadFixture(filepath.Join("testdata", "fold8_scrcpy_session.json"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	mock, err := StartMockFixture(fix)
	if err != nil {
		t.Fatalf("StartMockFixture: %v", err)
	}
	defer mock.Close()

	addr := mock.Listener.Addr().String()
	vConn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial video: %v", err)
	}
	cConn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		_ = vConn.Close()
		t.Fatalf("dial control: %v", err)
	}

	var mu sync.Mutex
	var gotYUV *core.NV12Frame
	sess, err := StartFromConns(lib, nil, nil, 0x01020304, 0, nil, vConn, cConn, true, LaunchConfig{
		InitialWidth:    fix.InitialWidth,
		InitialHeight:   fix.InitialHeight,
		EnableUHID:      true,
		StartAppPackage: "+com.sec.android.app.clockpackage",
		OnFrame: func(f core.NV12Frame) {
			mu.Lock()
			cp := f
			gotYUV = &cp
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("StartFromConns: %v", err)
	}
	defer sess.Close()

	if err := sess.MoveUHIDMouse(0, 4, -2, 0); err != nil {
		t.Fatalf("MoveUHIDMouse: %v", err)
	}
	if err := sess.Resize(1440, 900); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st := sess.Stats()
		if st.ClientResizedAck && st.NegotiatedWidth == 1440 && st.NegotiatedHeight == 900 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	st := sess.Stats()
	if st.DeviceName != fix.DeviceName || st.PacketsReceived < 6 {
		t.Fatalf("unexpected fixture replay stats: %+v", st)
	}
	if !st.ClientResizedAck || st.NegotiatedWidth != 1440 || st.NegotiatedHeight != 900 {
		t.Fatalf("expected flex resize ack to 1440x900, got %+v", st)
	}

	// Verify decoded NV12 frame renders directly via MyGo ui.Bitmap.UpdateYUV.
	mu.Lock()
	f := gotYUV
	mu.Unlock()
	if st.FramesDecoded < 1 || f == nil || len(f.Y) == 0 {
		t.Fatalf("expected >= 1 decoded 64x48 NV12 frame from Media Foundation, got stats=%+v frame=%v", st, f != nil)
	}
	bmp := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1)))
	bmp.UpdateYUV(&ui.YUVImage{
		Format:    ui.NV12,
		Width:     f.Width,
		Height:    f.Height,
		Planes:    [3][]byte{f.Y, f.UV, nil},
		Strides:   [3]int{f.YStride, f.UVStride, 0},
		Matrix:    ui.BT709,
		FullRange: false,
	})
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Image(c.Key("replay-frame"), bmp).Fill()
	}, 320, 240)
	tt.Frame()
	img := tt.Image()
	if img.Bounds().Dx() != 320 || img.Bounds().Dy() != 240 {
		t.Fatalf("unexpected MyGo UpdateYUV bounds: %v", img.Bounds())
	}
}

// TestLiveFold8Spikes executes Spike S1 (Display 0 H.264/HEVC -> MF NV12 -> MyGo UpdateYUV)
// and Spike S4 (concurrent Display 0 + 2 flex_display app windows) on a connected phone when
// ANDROFUSE_LIVE_DEVICE=1 is set.
func TestLiveFold8Spikes(t *testing.T) {
	if os.Getenv("ANDROFUSE_LIVE_DEVICE") != "1" {
		t.Skip("set ANDROFUSE_LIVE_DEVICE=1 with an attached phone to run live Fold 8 spikes")
	}
	libPath, err := filepath.Abs(filepath.Join("..", "..", "core", "target", "debug", core.LibraryName()))
	if err != nil {
		t.Fatal(err)
	}
	lib, err := core.Open(libPath)
	if err != nil {
		t.Fatalf("core.Open: %v", err)
	}
	defer lib.Stop()
	if _, err := lib.Start(core.Config{Version: "0.1.0", StateDir: t.TempDir(), Profile: "live"}); err != nil {
		t.Fatalf("core.Start: %v", err)
	}

	client, err := adb.FindClient()
	if err != nil {
		t.Fatalf("FindClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	devs, err := client.Devices(ctx)
	if err != nil || len(devs) == 0 {
		t.Fatalf("no ADB device found: devs=%+v err=%v", devs, err)
	}
	serial := devs[0].Serial
	store, err := lease.Open(filepath.Join(t.TempDir(), "leases.json"))
	if err != nil {
		t.Fatalf("lease.Open: %v", err)
	}

	// Spike S1: H.264 & HEVC Display 0 validation + MyGo UpdateYUV upload check.
	for _, codec := range []struct {
		name string
		id   uint32
	}{
		{"h264", CodecH264},
		{"hevc", CodecH265},
	} {
		var rendered bool
		var rMu sync.Mutex
		s, err := StartADB(ctx, client, lib, store, LaunchConfig{
			SessionID:     "s1-" + codec.name,
			Serial:        serial,
			DisplayID:     0,
			CodecID:       codec.id,
			MaxSize:       1280,
			MaxFPS:        60,
			EnableControl: true,
			EnableUHID:    false,
			InitialWidth:  1280,
			InitialHeight: 960,
			OnFrame: func(f core.NV12Frame) {
				rMu.Lock()
				defer rMu.Unlock()
				if rendered || f.Width <= 0 || f.Height <= 0 {
					return
				}
				bmp := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1)))
				bmp.UpdateYUV(&ui.YUVImage{
					Format:  ui.NV12,
					Width:   f.Width,
					Height:  f.Height,
					Planes:  [3][]byte{f.Y, f.UV, nil},
					Strides: [3]int{f.YStride, f.UVStride, 0},
					Matrix:  ui.BT709,
				})
				tt := ui.NewTester(func(c *ui.Context) {
					ui.Image(c.Key("s1-video"), bmp).Fill()
				}, 640, 480)
				tt.Frame()
				rendered = true
			},
		})
		if err != nil {
			t.Fatalf("Spike S1 StartADB(%s): %v", codec.name, err)
		}
		// Trigger a harmless D-Pad movement or clipboard request? No screen change needed; wait frames.
		st, wErr := s.WaitDecoded(3, 8*time.Second)
		s.Close()
		if wErr != nil {
			t.Fatalf("Spike S1 %s WaitDecoded: %v (stats=%+v)", codec.name, wErr, st)
		}
		t.Logf("Spike S1 [%s]: device=%q negotiated=%dx%d decoded=%d fps=%.1f avg_decode_us=%d hw=%v",
			codec.name, st.DeviceName, st.NegotiatedWidth, st.NegotiatedHeight,
			st.FramesDecoded, st.MeasuredFPS, st.AvgDecodeUs, st.HardwareAccelerated)
	}

	// Spike S4 (Scrcpy portion): concurrent Display 0 + 2 independent flex_display app windows + live ResizeDisplay.
	w1, err := StartADB(ctx, client, lib, store, LaunchConfig{
		SessionID:       "s4-calc",
		Serial:          serial,
		NewDisplay:      "1080x720/160",
		FlexDisplay:     true,
		StartAppPackage: "com.sec.android.app.popupcalculator",
		MaxFPS:          60,
		InitialWidth:    1080,
		InitialHeight:   720,
	})
	if err != nil {
		t.Fatalf("Spike S4 w1: %v", err)
	}
	defer w1.Close()

	w2, err := StartADB(ctx, client, lib, store, LaunchConfig{
		SessionID:       "s4-clock",
		Serial:          serial,
		NewDisplay:      "1080x720/160",
		FlexDisplay:     true,
		StartAppPackage: "com.sec.android.app.clockpackage",
		MaxFPS:          60,
		InitialWidth:    1080,
		InitialHeight:   720,
	})
	if err != nil {
		t.Fatalf("Spike S4 w2: %v", err)
	}
	defer w2.Close()

	m0, err := StartADB(ctx, client, lib, store, LaunchConfig{
		SessionID:     "s4-mirror0",
		Serial:        serial,
		DisplayID:     0,
		MaxSize:       1080,
		MaxFPS:        60,
		InitialWidth:  1080,
		InitialHeight: 800,
	})
	if err != nil {
		t.Fatalf("Spike S4 mirror0: %v", err)
	}
	defer m0.Close()

	st1, err := w1.WaitDecoded(2, 10*time.Second)
	if err != nil {
		t.Fatalf("Spike S4 w1 initial decode: %v (%+v)", err, st1)
	}
	if err := w1.Resize(1280, 800); err != nil {
		t.Fatalf("Spike S4 w1 Resize: %v", err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		st1 = w1.Stats()
		if st1.ClientResizedAck && st1.NegotiatedWidth == 1280 && st1.NegotiatedHeight == 800 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	st2, err := w2.WaitDecoded(2, 10*time.Second)
	if err != nil {
		t.Fatalf("Spike S4 w2 decode: %v (%+v)", err, st2)
	}
	stm, err := m0.WaitDecoded(2, 10*time.Second)
	if err != nil {
		t.Fatalf("Spike S4 m0 decode: %v (%+v)", err, stm)
	}
	t.Logf("Spike S4 concurrent streams verified: calc=%dx%d(resizedAck=%v) clock=%dx%d mirror=%dx%d",
		st1.NegotiatedWidth, st1.NegotiatedHeight, st1.ClientResizedAck,
		st2.NegotiatedWidth, st2.NegotiatedHeight, stm.NegotiatedWidth, stm.NegotiatedHeight)

	m0.Close()
	w2.Close()
	w1.Close()

	if rem := store.List(); len(rem) != 0 {
		t.Fatalf("expected empty lease store after teardown, got %+v", rem)
	}
}
