package gui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/dex"
	"github.com/YuanZhe-99/AndroFuse/internal/mcp"
	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
	"github.com/YuanZhe-99/AndroFuse/internal/scrcpy"
	"github.com/YuanZhe-99/AndroFuse/internal/vm"
	_ "github.com/YuanZhe-99/AndroFuse/locales"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a := New("test", t.TempDir())
	a.compAddr = "100.64.0.2:38412"
	a.compConnected = true
	a.compStatus = companion.DeviceStatus{
		BatteryPct:        88,
		Charging:          true,
		TempC:             33.4,
		NetworkType:       "vpn",
		ForegroundPackage: "com.android.chrome",
		WirelessADBPort:   41235,
	}
	a.vmStatus = vm.Status{
		State:          vm.StateStopped,
		NativeABI:      "arm64-v8a",
		SystemImagePkg: "system-images;android-36;google_apis;arm64-v8a",
		VMRoot:         filepath.Join(t.TempDir(), "vm"),
		AVDName:        "Pixel_9a_API_36",
		AvailableAVDs:  []string{"Pixel_9a_API_36"},
		UsingHostSDK:   true,
	}
	a.mcpStatus = mcp.Status{
		Enabled: false,
		Audit: []mcp.AuditEntry{
			{Method: "server.emergency_stop", Allowed: true, Outcome: "revoked"},
		},
	}
	a.cameraBridgeURL = "http://127.0.0.1:27190/camera/test.mjpeg"
	a.compHash = "0123456789abcdef"
	a.prefs.Mesh = &MeshPrefs{Networks: []MeshNetwork{{ID: "net-home", Label: "Home", NetworkName: "home-mesh", Join: true, ListenPort: 31452}, {ID: "net-office", NetworkName: "office"}}, Accept: true, PairNetwork: "net-home"}
	a.netViews["net-home"] = netView{Status: mesh.Status{Running: true}, Info: mesh.Info{IPv4: "10.126.126.1", NAT: "FullCone", Peers: []mesh.Peer{{Hostname: "SM-F971U1", IPv4: "10.126.126.12", Direct: true, Latency: 3}, {Hostname: "Pixel 8 Pro", IPv4: "10.126.126.13", Latency: 48}}}}
	a.remembered = []RememberedDevice{
		{HardwareHash: "0123456789abcdef", Name: "Galaxy Z Fold8", Alias: "My Fold", Maker: "samsung", Model: "SM-F971U1", Android: "17", AutoReconnect: true, Companion: &CompanionBinding{MeshHost: "SM-F971U1"}},
		{HardwareHash: "xiaomi-14", Name: "Xiaomi 14", Maker: "Xiaomi", Model: "23127PN0CC", Android: "15", AutoReconnect: true},
	}
	a.diagReady = true
	a.diag = HostDiagnostics{
		CoreLoaded: true,
		CoreABI:    core.ABIVersion,
		SelfCheck: core.SelfCheck{
			OS:                     "windows",
			ProcessArch:            "arm64",
			NativeArch:             "arm64",
			OSBuild:                26300,
			MediaFoundationPresent: true,
			H264HWDecoder:          true,
			H264AnyDecoder:         true,
			HEVCHWDecoder:          true,
			HEVCAnyDecoder:         true,
			WiFiDirectAPI:          true,
			MiracastIEProvider:     true,
		},
		ADBFound:   true,
		ADBVersion: "Android Debug Bridge version 1.0.41",
	}
	a.mdnsServices = []adb.MDNSService{
		{
			Instance:    "adb-***",
			ServiceType: "_adb-tls-connect._tcp",
			Addr:        "127.0.0.1:41235",
			MaskedAddr:  "127.*.*.*:41235",
			CanConnect:  true,
		},
	}
	a.devices = []DiscoveredDevice{
		{
			HardwareHash: "0123456789abcdef",
			Target: adb.Device{
				Serial:     "TESTUSB001",
				MaskedID:   "TES****001",
				DeviceHash: "0123456789abcdef",
				State:      "device",
				Model:      "Galaxy Fold",
				Transport:  adb.TransportUSB,
			},
			Transports: []adb.Device{
				{
					Serial:     "TESTUSB001",
					MaskedID:   "TES****001",
					DeviceHash: "0123456789abcdef",
					State:      "device",
					Model:      "Galaxy Fold",
					Transport:  adb.TransportUSB,
				},
				{
					Serial:     "192.168.1.40:41235",
					MaskedID:   "192.*.*.*:41235",
					DeviceHash: "wificonnhash0001",
					State:      "device",
					Model:      "Galaxy Fold",
					Transport:  adb.TransportTLSWiFi,
				},
			},
			Profile: adb.DeviceProfile{
				HardwareHash:   "0123456789abcdef",
				Manufacturer:   "samsung",
				Model:          "SM-F971U1",
				MarketName:     "Galaxy Z Fold8",
				AndroidRelease: "17",
				SDKInt:         37,
				OneUIVersion:   "90000",
				AVCEncoderMax:  16,
			},
			DexProbe: dex.ProbeReport{
				HasSemBuilder:        true,
				HasConnectWithConfig: true,
			},
			TruthDeX: dex.TruthVerdict{
				Backend:      dex.BackendADBLoopback,
				IsGenuineDeX: true,
				Label:        "Samsung Native DeX (ADB Loopback)",
			},
			Route: dex.SelectRoute(
				adb.DeviceProfile{Manufacturer: "samsung", OneUIVersion: "90000"},
				dex.ProbeReport{HasSemBuilder: true, HasConnectWithConfig: true},
				adb.TransportUSB,
				dex.RouteAuto,
			),
			Apps: []adb.AppEntry{
				{Package: "com.android.settings", Label: "Settings", System: true, Resizeable: true},
				{Package: "com.android.calculator2", Label: "Calculator", System: false, Resizeable: true},
			},
			Probed: true,
		},
		{
			HardwareHash: "fedcba9876543210",
			Target: adb.Device{
				Serial:     "emulator-5554",
				MaskedID:   "emu****554",
				DeviceHash: "fedcba9876543210",
				State:      "device",
				Model:      "Pixel_9a_API_36",
				Transport:  adb.TransportEmulator,
			},
			Transports: []adb.Device{
				{
					Serial:     "emulator-5554",
					MaskedID:   "emu****554",
					DeviceHash: "fedcba9876543210",
					State:      "device",
					Model:      "Pixel_9a_API_36",
					Transport:  adb.TransportEmulator,
				},
			},
			Profile: adb.DeviceProfile{
				HardwareHash:   "fedcba9876543210",
				Manufacturer:   "Google",
				Model:          "sdk_gphone64_arm64",
				Emulator:       true,
				AndroidRelease: "16",
				SDKInt:         36,
			},
			TruthDeX: dex.TruthVerdict{
				Backend:      dex.BackendVirtualFallback,
				IsGenuineDeX: false,
				Label:        "Virtual Desktop Fallback (Non-Samsung)",
			},
			Route: dex.SelectRoute(
				adb.DeviceProfile{Manufacturer: "Google"},
				dex.ProbeReport{},
				adb.TransportEmulator,
				dex.RouteAuto,
			),
			Apps: []adb.AppEntry{
				{Package: "com.android.settings", Label: "Settings", System: true, Resizeable: true},
				{Package: "com.google.android.deskclock", Label: "Clock", System: true, Resizeable: false},
			},
			Probed: true,
		},
	}
	return a
}

func TestEncoderHeadroom(t *testing.T) {
	used, limit, atLimit, nearLimit := EncoderHeadroom(adb.DeviceProfile{AVCEncoderMax: 4}, 3)
	if used != 3 || limit != 4 || atLimit || !nearLimit {
		t.Fatalf("unexpected near-limit headroom: used=%d limit=%d at=%v near=%v", used, limit, atLimit, nearLimit)
	}
	used, limit, atLimit, nearLimit = EncoderHeadroom(adb.DeviceProfile{}, 4)
	if used != 4 || limit != 4 || !atLimit || nearLimit {
		t.Fatalf("unexpected default at-limit headroom: used=%d limit=%d at=%v near=%v", used, limit, atLimit, nearLimit)
	}
}

// TestViewsRender builds every view — Home, each Settings page, search, the + picker, one pane
// and a split — in both languages, both appearances, at the default size, the minimum size (the
// rail) and a narrow window. With SCREENSHOT_DIR set it writes each frame there as a PNG.
func TestViewsRender(t *testing.T) {
	views := []string{"home", "search", "picker", "pane", "split"}
	for _, p := range []string{setGeneral, setPhones, setCamera, setNetwork, setVM, setSecurity, setAbout} {
		views = append(views, "settings-"+p)
	}
	sizes := []struct {
		name string
		w, h int
	}{
		{"desktop", mainDefW, mainDefH},
		{"rail", mainMinW, mainMinH},
		{"narrow", 600, 800},
	}
	dir := os.Getenv("SCREENSHOT_DIR")
	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocaleFunc(func() string { return lang })
		for _, sz := range sizes {
			for _, dark := range []bool{false, true} {
				for _, v := range views {
					a := testApp(t)
					dockTestSessions(a)
					a.split = nil
					switch {
					case v == "search":
						a.searchOpen, a.searchQuery = true, "set"
					case v == "picker":
						a.picker.Open = true
					case v == "pane":
						a.page = panePrefix + "s1"
					case v == "split":
						a.split = &paneSplit{A: "s1", B: "s2", Percent: 50}
						a.page = pageSplit
					case strings.HasPrefix(v, "settings-"):
						a.settingsOpen, a.settingsPage = true, strings.TrimPrefix(v, "settings-")
						a.netForm = &netForm{ID: "net-office", NetworkName: "office", Servers: "tcp://relay.example.com:11010"}
					default:
						a.renaming, a.renameText = "0123456789abcdef", "My Fold"
						a.connect = connectFlow{Input: "100.64.0.12", Phase: connectNoAnswer, Host: "100.64.0.12"}
					}
					tt := ui.NewTester(a.view, sz.w, sz.h)
					tt.SetDark(dark)
					tt.Frame()
					tt.Frame()
					img := tt.Image()
					if img.Bounds().Dx() != sz.w || img.Bounds().Dy() != sz.h {
						t.Fatalf("unexpected frame bounds for %s/%s/%s: %v", lang, sz.name, v, img.Bounds())
					}
					if dir != "" {
						mode := "light"
						if dark {
							mode = "dark"
						}
						f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s_%s_%s_%s.png", lang, sz.name, mode, v)))
						if err != nil {
							t.Fatal(err)
						}
						_ = png.Encode(f, img)
						f.Close()
					}
				}
			}
		}
	}
}

func TestSessionWindowRenderAndInputMapping(t *testing.T) {
	box := ui.Rect{W: 1000, H: 600}
	// 16:9 (1920x1080) fills 1000x562.5 vertically centered -> oy = 18.75.
	ox, oy, w, h := FitBox(box, 1920, 1080)
	if math.Abs(float64(ox)) > 0.01 || math.Abs(float64(oy)-18.75) > 0.01 || math.Abs(float64(w)-1000) > 0.01 || math.Abs(float64(h)-562.5) > 0.01 {
		t.Fatalf("unexpected FitBox: ox=%v oy=%v w=%v h=%v", ox, oy, w, h)
	}

	// Click in top letterbox bar must report inside=false; center must map accurately.
	if _, _, inside := MapPointerToVideo(500, 5, box, 1920, 1080); inside {
		t.Fatal("expected top letterbox bar click to report inside=false")
	}
	vx, vy, inside := MapPointerToVideo(500, 300, box, 1920, 1080)
	if !inside || vx < 950 || vx > 970 || vy < 530 || vy > 550 {
		t.Fatalf("unexpected center MapPointerToVideo: (%d,%d) inside=%v", vx, vy, inside)
	}

	var tr InputTracker
	if btn := MapMyGoButton(1); btn != scrcpy.ButtonSecondary {
		t.Fatalf("right click button = %d, want %d", btn, scrcpy.ButtonSecondary)
	}
	down := tr.Translate(ui.InputEvent{Kind: ui.InputPointerDown, Button: 1, X: 500, Y: 300}, box, 1920, 1080)
	if down.Kind != ActionTouch || down.TouchAction != scrcpy.ActionDown || down.Button != scrcpy.ButtonSecondary {
		t.Fatalf("unexpected right-click translate: %+v", down)
	}
	_ = tr.Translate(ui.InputEvent{Kind: ui.InputPointerUp, Button: 1, X: 500, Y: 300}, box, 1920, 1080)

	// Space uses InjectKeycode once and suppresses duplicate InputText(" "); CJK uses InjectText.
	sp := tr.Translate(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeySpace}, box, 1920, 1080)
	if sp.Kind != ActionKey || sp.Keycode != scrcpy.KeycodeSpace {
		t.Fatalf("expected Space keycode action, got %+v", sp)
	}
	if dup := tr.Translate(ui.InputEvent{Kind: ui.InputText, Text: " "}, box, 1920, 1080); dup.Kind != ActionNone {
		t.Fatalf("expected suppressed duplicate space text, got %+v", dup)
	}
	ime := tr.Translate(ui.InputEvent{Kind: ui.InputText, Text: "中文"}, box, 1920, 1080)
	if ime.Kind != ActionText || ime.Text != "中文" {
		t.Fatalf("expected CJK IME text action, got %+v", ime)
	}
	// Plain 'a' keydown defers to InputText; Ctrl+C keydown sends KeyEvent.
	if plainA := tr.Translate(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyA}, box, 1920, 1080); plainA.Kind != ActionNone {
		t.Fatalf("expected plain KeyA keydown to defer to InputText, got %+v", plainA)
	}
	ctrlC := tr.Translate(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyC, Mods: ui.Ctrl}, box, 1920, 1080)
	if ctrlC.Kind != ActionKey || ctrlC.MetaState&scrcpy.MetaCtrlOn == 0 {
		t.Fatalf("expected Ctrl+C key event, got %+v", ctrlC)
	}
	esc := tr.Translate(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEscape, Mods: ui.Ctrl | ui.Alt}, box, 1920, 1080)
	if esc.Kind != ActionReleaseFocus || !esc.ReleaseFocus {
		t.Fatalf("expected Ctrl+Alt+Esc focus release, got %+v", esc)
	}

	// Render SessionWindow UI across en / zh-Hans and light / dark with an uploaded NV12 frame.
	sw := newSessionWindow(testApp(t), SessionSpec{
		ID:           "sess-test",
		Title:        "Galaxy Fold · Samsung DeX (TLS Wi-Fi)",
		Mode:         SessionModeDeX,
		Transport:    "TLS Wi-Fi",
		BackendLabel: "Samsung Native DeX (ADB Loopback)",
		TargetSpec:   "1920×1080 @ 60 fps",
		IsDeX:        true,
		AllowResize:  true,
		ScreenOffCap: true,
	})
	const vw, vh = 64, 48
	yuv := make([]byte, vw*vh*3/2)
	sw.PushNV12(core.NV12Frame{
		Width:    vw,
		Height:   vh,
		Y:        yuv[:vw*vh],
		UV:       yuv[vw*vh:],
		YStride:  vw,
		UVStride: vw,
		Hardware: true,
		DecodeUs: 2400,
	}, 59.8, 2400)

	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocaleFunc(func() string { return lang })
		for _, dark := range []bool{false, true} {
			tt := ui.NewTester(sw.view, 960, 640)
			tt.SetDark(dark)
			tt.Frame()
			img := tt.Image()
			if img.Bounds().Dx() != 960 || img.Bounds().Dy() != 640 {
				t.Fatalf("unexpected SessionWindow bounds: %v", img.Bounds())
			}
		}
	}

	// Render chromeless Independent App Window with a decoded PNG app icon.
	iconImg := image.NewRGBA(image.Rect(0, 0, 24, 24))
	for y := range 24 {
		for x := range 24 {
			iconImg.Set(x, y, color.RGBA{R: 29, G: 111, B: 79, A: 255})
		}
	}
	var iconBuf bytes.Buffer
	if err := png.Encode(&iconBuf, iconImg); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	iconPath := filepath.Join(t.TempDir(), "calc.png")
	if err := os.WriteFile(iconPath, iconBuf.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile icon: %v", err)
	}
	iconRaw, _ := os.ReadFile(iconPath)
	appIconBmp, err := ui.DecodeBitmap(iconRaw)
	if err != nil {
		t.Fatalf("ui.DecodeBitmap: %v", err)
	}
	appWin := newSessionWindow(testApp(t), SessionSpec{
		ID:           "app-test",
		Title:        "Calculator",
		Mode:         SessionModeApp,
		Transport:    "Emulator",
		BackendLabel: "Calculator",
		TargetSpec:   "540×960 flex · 60 fps",
		AllowResize:  true,
		AppIcon:      appIconBmp,
		AppPackage:   "com.android.calculator2",
	})
	appWin.PushNV12(core.NV12Frame{
		Width:    vw,
		Height:   vh,
		Y:        yuv[:vw*vh],
		UV:       yuv[vw*vh:],
		YStride:  vw,
		UVStride: vw,
		Hardware: true,
		DecodeUs: 1900,
	}, 60.0, 1900)
	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocaleFunc(func() string { return lang })
		for _, dark := range []bool{false, true} {
			tt := ui.NewTester(appWin.view, appWinPortraitW, appWinPortraitH)
			tt.SetDark(dark)
			tt.Frame()
			if tt.Image().Bounds().Dx() != appWinPortraitW {
				t.Fatalf("unexpected appWin bounds: %v", tt.Image().Bounds())
			}
		}
	}
}

// dockTestSessions opens two docked sessions, s1 and s2, split side by side for pageSplit.
func dockTestSessions(a *App) {
	for _, id := range []string{"s1", "s2"} {
		sw := newSessionWindow(a, SessionSpec{ID: id, DeviceKey: "0123456789abcdef", Mode: SessionModeMirror, Title: "Pixel " + id, Transport: "USB", BackendLabel: "Phone mirror"})
		sw.docked = true
		sw.negotiatedW, sw.negotiatedH, sw.measuredFPS = 1080, 2400, 59.8
		a.sessions[id] = sw
	}
	a.split = &paneSplit{A: "s1", B: "s2", Percent: 50}
}

// TestPaneDocking checks that docking, splitting, popping out and closing keep the main window
// on a page that exists.
func TestPaneDocking(t *testing.T) {
	a := testApp(t)
	dockTestSessions(a)
	a.split = nil
	a.dockLocked("s1", false)
	if a.page != panePrefix+"s1" {
		t.Fatalf("dock s1: page = %q", a.page)
	}
	a.dockLocked("s2", true)
	if a.page != pageSplit || a.split == nil || a.split.A != "s1" || a.split.B != "s2" {
		t.Fatalf("dock s2 beside s1: page = %q split = %+v", a.page, a.split)
	}
	a.undockSession("s1") // s1 pops out
	if a.page != panePrefix+"s2" || a.split != nil {
		t.Fatalf("pop out s1: page = %q split = %+v", a.page, a.split)
	}
	a.removeSession("s2")
	if a.page != pageHome {
		t.Fatalf("close s2: page = %q", a.page)
	}
}

// TestRememberedDevices checks that online phones are remembered, survive a restart, are listed
// while offline, and that Forget can be undone.
func TestRememberedDevices(t *testing.T) {
	dir := t.TempDir()
	a := New("test", dir)
	fold := DiscoveredDevice{
		HardwareHash: "hw-fold", Probed: true,
		Target:     adb.Device{Serial: "R5", State: "device", Transport: adb.TransportUSB},
		Transports: []adb.Device{{Serial: "R5", State: "device", Transport: adb.TransportUSB}, {Serial: "192.0.2.4:37099", State: "device", Transport: adb.TransportTLSWiFi}},
		Profile:    adb.DeviceProfile{Model: "Galaxy Z Fold 8"},
	}
	a.rememberDevices([]DiscoveredDevice{fold}, time.Now())

	b := New("test", dir)
	r, ok := b.RememberedFor("hw-fold")
	if !ok || r.Name != "Galaxy Z Fold 8" || r.Endpoint != "192.0.2.4:37099" || !r.AutoReconnect {
		t.Fatalf("remembered after restart = %+v, %v", r, ok)
	}
	if off := b.offlineRemembered(nil); len(off) != 1 {
		t.Fatalf("offline list = %+v", off)
	}
	if off := b.offlineRemembered([]DiscoveredDevice{fold}); len(off) != 0 {
		t.Fatalf("an online phone must not be listed offline: %+v", off)
	}
	gone, ok := b.ForgetDevice("hw-fold")
	if _, still := b.RememberedFor("hw-fold"); !ok || still {
		t.Fatal("Forget must remove the phone")
	}
	b.RestoreDevice(gone)
	if _, back := New("test", dir).RememberedFor("hw-fold"); !back {
		t.Fatal("Undo must restore and save the phone")
	}
}
