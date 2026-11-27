package gui

import (
	"bytes"
	"context"
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
	L "github.com/YuanZhe-99/AndroFuse/locales"
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
	a.netViews["net-home"] = netView{Status: mesh.Status{Running: true}, Info: mesh.Info{IPv4: "10.126.126.1", NAT: "FullCone", Peers: []mesh.Peer{{Hostname: "AndroFuse-phone-0a1b2c3d Galaxy Z Fold8", IPv4: "10.126.126.12", Direct: true, Latency: 3}, {Hostname: "AndroFuse-phone-77aa01ff Pixel 8 Pro", IPv4: "10.126.126.13", Latency: 48}, {Hostname: "PublicServer_CloudCone-S196V", Direct: true, Latency: 23}, {Hostname: "AndroFuse-pc-1234abcd STUDIO", IPv4: "10.126.126.2", Direct: true, Latency: 5}}}}
	a.remembered = []RememberedDevice{
		{HardwareHash: "0123456789abcdef", Name: "Galaxy Z Fold8", Alias: "My Fold", Maker: "samsung", Model: "SM-F971U1", Android: "17", AutoReconnect: true, Companion: &CompanionBinding{MeshHost: "AndroFuse-phone-0a1b2c3d Fold"}},
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

// TestOthersInNetworksShowsOnlyAndroFusePhones checks the Home list of network phones: the
// remembered Fold (renamed since) and the computer are left out, and so is the public relay
// server, which is no AndroFuse device.
func TestOthersInNetworksShowsOnlyAndroFusePhones(t *testing.T) {
	a := testApp(t)
	others := a.othersInNetworks()
	if len(others) != 1 || others[0].Name != "Pixel 8 Pro" || others[0].IPv4 != "10.126.126.13" {
		t.Fatalf("othersInNetworks = %+v", others)
	}
	if got := a.meshPeerName("AndroFuse-phone-0a1b2c3d Galaxy Z Fold8"); got != "My Fold" {
		t.Fatalf("meshPeerName(remembered) = %q", got)
	}
	if got := a.meshPeerName("AndroFuse-pc-1234abcd STUDIO"); got != "STUDIO" {
		t.Fatalf("meshPeerName(pc) = %q", got)
	}
	if h := a.meshHostname(); !strings.HasPrefix(h, "AF-c-") || h != a.meshHostname() {
		t.Fatalf("meshHostname = %q, not kept", h)
	}
}

// TestCameraSessionLetsGoBeforeSwitching checks what switching cameras waits on: Close signals
// closed once the session let go of everything, and the header names the camera, not "front".
func TestCameraSessionLetsGoBeforeSwitching(t *testing.T) {
	a := testApp(t)
	sw := newSessionWindow(a, SessionSpec{ID: "cam1", DeviceKey: "0123456789abcdef", Mode: SessionModeCamera, CameraFacing: "front"})
	a.sessions["cam1"] = sw
	sw.Close()
	select {
	case <-sw.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close never signalled closed")
	}
	if cameraWord("front") == cameraWord("back") || cameraWord("front") == "front" {
		t.Fatal("cameraWord does not name the cameras")
	}
}

func TestConnectionModeAndDeviceAppsCache(t *testing.T) {
	dir := t.TempDir()
	a := New("test", dir)
	if a.connectionMode() != ConnBothADB || !a.autoSwitchOn() || a.prefersCompanion() || !a.allowsWirelessADB() {
		t.Fatalf("unexpected defaults: mode=%q auto=%v prefComp=%v allowADB=%v", a.connectionMode(), a.autoSwitchOn(), a.prefersCompanion(), a.allowsWirelessADB())
	}

	a.mu.Lock()
	a.prefs.ConnectionMode = ConnBothCompanion
	a.savePrefs()
	a.mu.Unlock()
	b := New("test", dir)
	if b.connectionMode() != ConnBothCompanion || !b.autoSwitchOn() || !b.prefersCompanion() || !b.allowsWirelessADB() {
		t.Fatalf("unexpected both_companion state: mode=%q auto=%v prefComp=%v allowADB=%v", b.connectionMode(), b.autoSwitchOn(), b.prefersCompanion(), b.allowsWirelessADB())
	}

	b.mu.Lock()
	b.prefs.ConnectionMode = ConnCompanionOnly
	b.savePrefs()
	b.mu.Unlock()
	if b.autoSwitchOn() || !b.prefersCompanion() || b.allowsWirelessADB() {
		t.Fatalf("companion_only must disable wireless ADB and auto-switch")
	}

	apps := []adb.AppEntry{
		{Package: "com.whatsapp", Label: "WhatsApp", Resizeable: true},
		{Package: "com.android.settings", Label: "Settings", System: true, Resizeable: true},
	}
	b.saveDeviceApps("hw-cached", apps)
	c := New("test", dir)
	got := c.appsForDevice("hw-cached")
	if len(got) != 2 || got[0].Package != "com.whatsapp" {
		t.Fatalf("cached apps after reload = %+v", got)
	}
}

func TestOfflineAppsPickerAndStatusStripRender(t *testing.T) {
	a := testApp(t)
	a.devices = nil // force offlineCard with paired Companion + cached apps
	a.saveDeviceApps("0123456789abcdef", []adb.AppEntry{
		{Package: "com.android.settings", Label: "Settings", Resizeable: true},
		{Package: "com.android.calculator2", Label: "Calculator", Resizeable: true},
	})
	a.picker = pickerState{Open: true, Kind: openAsApp, Device: "0123456789abcdef"}

	sw := newSessionWindow(a, SessionSpec{
		ID:           "dapp-test",
		DeviceKey:    "0123456789abcdef",
		Title:        "Calculator",
		Mode:         SessionModeApp,
		Transport:    "Companion",
		BackendLabel: "Calculator",
		AllowResize:  true,
		AppPackage:   "com.android.calculator2",
	})
	sw.reconnecting = true
	sw.reconnectMsg = L.SessionReconnecting()

	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocaleFunc(func() string { return lang })
		for _, dark := range []bool{false, true} {
			ttHome := ui.NewTester(a.view, mainDefW, mainDefH)
			ttHome.SetDark(dark)
			ttHome.Frame()
			if ttHome.Image().Bounds().Dx() != mainDefW {
				t.Fatalf("unexpected offline apps view bounds: %v", ttHome.Image().Bounds())
			}
			ttSess := ui.NewTester(sw.view, appWinPortraitW, appWinPortraitH)
			ttSess.SetDark(dark)
			ttSess.Frame()
			if ttSess.Image().Bounds().Dx() != appWinPortraitW {
				t.Fatalf("unexpected statusStrip session bounds: %v", ttSess.Image().Bounds())
			}
		}
	}
}

func TestWirelessEndpointAndPortScanCooldown(t *testing.T) {
	dir := t.TempDir()
	a := New("test", dir)
	a.remembered = []RememberedDevice{
		{HardwareHash: "hw-fold8", Name: "Fold8", Endpoint: "192.168.156.54:43139", AutoReconnect: true},
	}
	a.saveDevicesLocked()

	a.noteWirelessEndpoint("hw-fold8", "192.168.156.54:39543")
	reloaded := New("test", dir)
	r, ok := reloaded.RememberedFor("hw-fold8")
	if !ok || r.Endpoint != "192.168.156.54:39543" {
		t.Fatalf("persisted endpoint = %q, want 192.168.156.54:39543", r.Endpoint)
	}
	// Loopback tunnel endpoints must never overwrite the phone's Wi-Fi endpoint.
	reloaded.noteWirelessEndpoint("hw-fold8", "127.0.0.1:50001")
	if r2, _ := reloaded.RememberedFor("hw-fold8"); r2.Endpoint != "192.168.156.54:39543" {
		t.Fatalf("loopback endpoint overwrote Wi-Fi endpoint: %q", r2.Endpoint)
	}

	if !reloaded.portScanDue("hw-fold8") {
		t.Fatal("first portScanDue should allow scan")
	}
	if reloaded.portScanDue("hw-fold8") {
		t.Fatal("immediate second portScanDue must respect cooldown")
	}
}

func TestPhoneNeedsActionAndOfflineAppsLoadingRender(t *testing.T) {
	// companion_only still pulls up ADB once if APK hash mismatches or Enhanced mode daemon is down.
	nAPK, nDaemon, wADB := phoneNeedsAction(
		companion.DeviceStatus{AppBuildHash: "old", DaemonReady: false},
		"new", false, false,
	)
	if !nAPK || !nDaemon || wADB {
		t.Fatalf("companion_only with outdated APK+down daemon = (%v, %v, %v), want (true, true, false)", nAPK, nDaemon, wADB)
	}
	// both_adb wants ADB when onlineADB is false even if APK & daemon match.
	nAPK, nDaemon, wADB = phoneNeedsAction(
		companion.DeviceStatus{AppBuildHash: "new", DaemonReady: true},
		"new", false, true,
	)
	if nAPK || nDaemon || !wADB {
		t.Fatalf("both_adb without ADB = (%v, %v, %v), want (false, false, true)", nAPK, nDaemon, wADB)
	}

	// Render offlineCard when DaemonReady && DaemonTrustedDisplay is true even before apps cache exists,
	// plus the appPickerLoading state.
	a := testApp(t)
	a.devices = nil
	a.peerStatus["0123456789abcdef"] = companion.DeviceStatus{
		BatteryPct:           92,
		NetworkType:          "wifi",
		DaemonReady:          true,
		DaemonTrustedDisplay: true,
	}
	a.picker = pickerState{Open: true, Kind: openAsApp, Device: "0123456789abcdef"}
	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocaleFunc(func() string { return lang })
		for _, dark := range []bool{false, true} {
			tt := ui.NewTester(a.view, mainDefW, mainDefH)
			tt.SetDark(dark)
			tt.Frame()
			if tt.Image().Bounds().Dx() != mainDefW {
				t.Fatalf("unexpected frame bounds: %v", tt.Image().Bounds())
			}
		}
	}
}

func TestDirectLANRoutesAndAdaptiveStreamProfile(t *testing.T) {
	a := testApp(t)
	hash := "hw-route-test"
	a.remembered = []RememberedDevice{
		{
			HardwareHash: hash,
			Name:         "Fold8",
			Endpoint:     "192.168.156.55:39543",
			Companion: &CompanionBinding{
				Port:     43856,
				MeshHost: "AF-p-01234567 Fold8",
			},
		},
	}
	a.peerStatus[hash] = companion.DeviceStatus{WifiIPv4: "192.168.156.54"}
	a.rt.meshCfgs = map[string]mesh.Config{
		"net1": {
			PortForwards: []mesh.PortForward{
				{LocalPort: 51234, RemoteHost: "10.126.126.2", RemotePort: 43856},
			},
		},
	}
	a.netViews = map[string]netView{
		"net1": {
			Info: mesh.Info{
				Peers: []mesh.Peer{
					{Hostname: "AF-p-01234567 Fold8", Direct: false, Latency: 95},
				},
			},
		},
	}
	a.diag.SelfCheck.HEVCHWDecoder = true

	routes, err := a.companionRoutes(context.Background(), hash, *a.remembered[0].Companion)
	if err != nil {
		t.Fatalf("companionRoutes returned error: %v", err)
	}
	if len(routes) < 2 || !routes[0].directLAN || routes[0].addr != "192.168.156.54:43856" {
		t.Fatalf("expected WifiIPv4 direct LAN route first, got %+v", routes)
	}
	if a.isMeshRoute(routes[0].addr) || !a.isMeshRoute("127.0.0.1:51234") {
		t.Fatalf("isMeshRoute misclassified directLAN=%v mesh=%v", a.isMeshRoute(routes[0].addr), a.isMeshRoute("127.0.0.1:51234"))
	}

	// Direct LAN / ADB stays full-speed regardless of relayed mesh peer state.
	br, fps, sz, codec, _, relayed := a.companionStreamProfile("AF-p-01234567 Fold8", "192.168.156.54:43856", false)
	if relayed || br != 6_000_000 || fps != 60 || sz != 0 || codec != "h264" {
		t.Fatalf("direct LAN mirror profile = (%d, %d, %d, %s, relayed=%v)", br, fps, sz, codec, relayed)
	}

	// Relayed 5G mesh path tightens bitrate/resolution and enables H.265.
	br, fps, sz, codec, _, relayed = a.companionStreamProfile("AF-p-01234567 Fold8", "127.0.0.1:51234", false)
	if !relayed || br != 1_000_000 || fps != 30 || sz != 960 || codec != "h265" {
		t.Fatalf("relayed 5G mirror profile = (%d, %d, %d, %s, relayed=%v)", br, fps, sz, codec, relayed)
	}
	brD, fpsD, _, codecD, _, relayedD := a.companionStreamProfile("AF-p-01234567 Fold8", "127.0.0.1:51234", true)
	if !relayedD || brD != 1_500_000 || fpsD != 30 || codecD != "h265" {
		t.Fatalf("relayed 5G desktop profile = (%d, %d, %s, relayed=%v)", brD, fpsD, codecD, relayedD)
	}

	// WAN P2P mesh path uses balanced 2.5/2.0 Mbps @ 30 fps.
	a.netViews["net1"].Info.Peers[0].Direct = true
	a.netViews["net1"].Info.Peers[0].Latency = 55
	brP, fpsP, szP, codecP, _, relayedP := a.companionStreamProfile("AF-p-01234567 Fold8", "127.0.0.1:51234", false)
	if relayedP || brP != 2_000_000 || fpsP != 30 || szP != 1080 || codecP != "h265" {
		t.Fatalf("WAN P2P mirror profile = (%d, %d, %d, %s, relayed=%v)", brP, fpsP, szP, codecP, relayedP)
	}
}
