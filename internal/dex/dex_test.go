package dex

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/lease"
	"github.com/YuanZhe-99/AndroFuse/internal/scrcpy"
)

func TestParseProbeAndReadyPayload(t *testing.T) {
	raw := `noise line
AFDEX1|PROBE|{"manufacturer":"samsung","model":"SM-F971U1","sdk":37,"oneui":"90000","sem":"3701","has_sem_builder":true,"has_set_ap":true,"has_set_mode":true,"has_connect_with_config":true,"has_disconnect_wifi_display":true,"probe_error":"","displays":[{"id":0,"name":"Built-in Screen","width":2448,"height":1848,"dpi":420,"type":1,"flags":3}]}`
	rep, err := ParseProbeOutput(raw)
	if err != nil {
		t.Fatalf("ParseProbeOutput: %v", err)
	}
	if rep.SDK != 37 || !rep.HasSemBuilder || !rep.HasConnectWithConfig || len(rep.Displays) != 1 {
		t.Fatalf("unexpected probe report: %+v", rep)
	}

	ready, err := ParseReadyPayload("7|1920|1080|160|3|131072|41234")
	if err != nil {
		t.Fatalf("ParseReadyPayload: %v", err)
	}
	if ready.DisplayID != 7 || ready.Width != 1920 || ready.Height != 1080 || ready.DPI != 160 || ready.RemotePID != 41234 {
		t.Fatalf("unexpected ReadyInfo: %+v", ready)
	}
}

func TestEvaluateTruthOneUI9AndFallbacks(t *testing.T) {
	samOneUI9 := adb.DeviceProfile{
		Manufacturer:    "samsung",
		OneUIVersion:    "90000",
		HasDeskLauncher: false, // Merged on One UI 9
	}
	probe := ProbeReport{
		HasSemBuilder:        true,
		HasConnectWithConfig: true,
	}

	vNative := EvaluateTruth(samOneUI9, probe, "AndroFuseDeX", BackendADBLoopback)
	if !vNative.IsGenuineDeX || vNative.Backend != BackendADBLoopback {
		t.Fatalf("expected One UI 9 loopback to qualify as genuine DeX, got %+v", vNative)
	}

	vFallback := EvaluateTruth(samOneUI9, probe, "", BackendVirtualFallback)
	if vFallback.IsGenuineDeX || vFallback.Backend != BackendVirtualFallback {
		t.Fatalf("plain --new-display must never report genuine DeX (INV-06): %+v", vFallback)
	}

	nonSam := EvaluateTruth(adb.DeviceProfile{Manufacturer: "Google"}, probe, "AndroFuseDeX", BackendADBLoopback)
	if nonSam.IsGenuineDeX {
		t.Fatalf("non-Samsung device must never report genuine DeX: %+v", nonSam)
	}
}

func TestParseAppsOutputAndIconCache(t *testing.T) {
	// Valid 1x1 transparent PNG base64.
	const onePxPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	raw := strings.Join([]string{
		`AFDEX1|APP|{"package":"org.chromium.webapk.A4ccf86b27d69a6e1_v2","activity":".WebApk","label":"Noise","system":false,"resizeable":true,"icon_b64":"` + onePxPNG + `"}`,
		`AFDEX1|APP|{"package":"com.android.settings","activity":".Settings","label":"Settings","system":true,"resizeable":true,"icon_b64":"` + onePxPNG + `"}`,
		`AFDEX1|APP|{"package":"com.example.chat","activity":".Main","label":"Chat App","system":false,"resizeable":false,"icon_b64":"` + onePxPNG + `"}`,
		`AFDEX1|APPS_DONE|3`,
	}, "\n")

	cacheDir := filepath.Join(t.TempDir(), "icons")
	apps, err := ParseAppsOutput(raw, cacheDir)
	if err != nil {
		t.Fatalf("ParseAppsOutput: %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("expected 2 filtered apps, got %d (%+v)", len(apps), apps)
	}
	if apps[0].Package != "com.example.chat" || apps[0].Label != "Chat App" || apps[0].Resizeable || apps[0].System {
		t.Fatalf("unexpected first app: %+v", apps[0])
	}
	if apps[1].Package != "com.android.settings" || !apps[1].System || !apps[1].Resizeable {
		t.Fatalf("unexpected second app: %+v", apps[1])
	}
	for _, ap := range apps {
		if ap.IconPath == "" {
			t.Fatalf("expected cached icon path for %s", ap.Package)
		}
		data, err := os.ReadFile(ap.IconPath)
		if err != nil || len(data) < 8 {
			t.Fatalf("expected PNG on disk at %s: err=%v len=%d", ap.IconPath, err, len(data))
		}
	}
}

// TestLiveEmulatorAppWindowsAndIcons verifies real app label + 96x96 PNG icon
// extraction and concurrent chromeless flex-display app windows on Pixel_9a_API_36
// when ANDROFUSE_LIVE_EMULATOR=1 is set.
func TestLiveEmulatorAppWindowsAndIcons(t *testing.T) {
	if os.Getenv("ANDROFUSE_LIVE_EMULATOR") != "1" {
		t.Skip("set ANDROFUSE_LIVE_EMULATOR=1 with a booted Android emulator to run live AVD app window tests")
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
	if _, err := lib.Start(core.Config{Version: "0.1.0", StateDir: t.TempDir(), Profile: "live-emu"}); err != nil {
		t.Fatalf("core.Start: %v", err)
	}

	client, err := adb.FindClient()
	if err != nil {
		t.Fatalf("FindClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	devs, err := client.Devices(ctx)
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	var serial string
	for _, d := range devs {
		if d.State == "device" && d.Transport == adb.TransportEmulator {
			serial = d.Serial
			break
		}
	}
	if serial == "" {
		t.Skipf("no online emulator-* device found (%+v)", devs)
	}

	iconDir := filepath.Join(t.TempDir(), "icon-cache")
	apps, err := ListAppsWithIcons(ctx, client, serial, iconDir)
	if err != nil {
		t.Fatalf("ListAppsWithIcons: %v", err)
	}
	if len(apps) < 5 {
		t.Fatalf("expected >= 5 launchable apps on emulator, got %d", len(apps))
	}
	iconOK := 0
	for _, ap := range apps {
		if adb.IsNoisyAppPackage(ap.Package) {
			t.Fatalf("unexpected noisy package leaked past filter: %s", ap.Package)
		}
		if ap.IconPath != "" {
			f, err := os.Open(ap.IconPath)
			if err != nil {
				t.Fatalf("open icon %s: %v", ap.IconPath, err)
			}
			cfg, err := png.DecodeConfig(f)
			_ = f.Close()
			if err != nil || cfg.Width != 96 || cfg.Height != 96 {
				t.Fatalf("invalid PNG icon %s: cfg=%+v err=%v", ap.IconPath, cfg, err)
			}
			iconOK++
		}
	}
	if iconOK < 3 {
		t.Fatalf("expected >= 3 decoded 96x96 PNG icons, got %d", iconOK)
	}
	t.Logf("AVD ListAppsWithIcons: count=%d icons_96x96=%d sample=%q (%s)", len(apps), iconOK, apps[0].Label, apps[0].Package)

	var pkg1, pkg2 string
	preferred := []string{"com.android.settings", "com.google.android.deskclock", "com.android.deskclock", "com.android.contacts"}
	for _, want := range preferred {
		for _, ap := range apps {
			if ap.Package == want {
				if pkg1 == "" {
					pkg1 = ap.Package
				} else if pkg2 == "" && ap.Package != pkg1 {
					pkg2 = ap.Package
				}
			}
		}
	}
	for _, ap := range apps {
		if pkg1 != "" && pkg2 != "" {
			break
		}
		if pkg1 == "" {
			pkg1 = ap.Package
		} else if pkg2 == "" && ap.Package != pkg1 {
			pkg2 = ap.Package
		}
	}

	store, err := lease.Open(filepath.Join(t.TempDir(), "leases.json"))
	if err != nil {
		t.Fatalf("lease.Open: %v", err)
	}

	s1, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
		SessionID:             "emu-app1",
		Serial:                serial,
		NewDisplay:            "544x960/200",
		FlexDisplay:           true,
		HideSystemDecorations: true,
		StartAppPackage:       "+" + pkg1,
		MaxFPS:                60,
		EnableControl:         true,
		Audio:                 true,
		InitialWidth:          544,
		InitialHeight:         960,
	})
	if err != nil {
		t.Fatalf("StartADB app1 (%s): %v", pkg1, err)
	}
	defer s1.Close()

	s2, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
		SessionID:             "emu-app2",
		Serial:                serial,
		NewDisplay:            "544x960/200",
		FlexDisplay:           true,
		HideSystemDecorations: true,
		StartAppPackage:       "+" + pkg2,
		MaxFPS:                60,
		EnableControl:         true,
		InitialWidth:          544,
		InitialHeight:         960,
	})
	if err != nil {
		t.Fatalf("StartADB app2 (%s): %v", pkg2, err)
	}
	defer s2.Close()

	st1, err := s1.WaitDecoded(2, 15*time.Second)
	if err != nil {
		t.Fatalf("app1 WaitDecoded: %v (stats=%+v)", err, st1)
	}
	st2, err := s2.WaitDecoded(2, 15*time.Second)
	if err != nil {
		t.Fatalf("app2 WaitDecoded: %v (stats=%+v)", err, st2)
	}

	if err := s1.Resize(720, 1280); err != nil {
		t.Fatalf("app1 Resize(720x1280): %v", err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		st1 = s1.Stats()
		if st1.ClientResizedAck && st1.NegotiatedWidth == 720 && st1.NegotiatedHeight == 1280 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("AVD CHROMELESS CONCURRENT APPS PASS: app1(%s)=%dx%d(resizedAck=%v,hw=%v) app2(%s)=%dx%d(hw=%v)",
		pkg1, st1.NegotiatedWidth, st1.NegotiatedHeight, st1.ClientResizedAck, st1.HardwareAccelerated,
		pkg2, st2.NegotiatedWidth, st2.NegotiatedHeight, st2.HardwareAccelerated)
	if !st1.ClientResizedAck || st1.NegotiatedWidth != 720 || st1.NegotiatedHeight != 1280 {
		t.Fatalf("expected flex resize ack to 720x1280, got %+v", st1)
	}

	s2.Close()
	s1.Close()

	if rem := store.List(); len(rem) != 0 {
		t.Fatalf("residual leases after emulator teardown: %+v", rem)
	}
	dispOut, _ := client.Shell(ctx, serial, "dumpsys display | grep -E 'DisplayDeviceInfo'")
	if strings.Contains(dispOut, "scrcpy") {
		t.Fatalf("residual scrcpy virtual display on emulator:\n%s", dispOut)
	}
}

// TestLiveFold8NativeDeXS2 validates ADB Native DeX loopback (Spike S2) and concurrent
// DeX + 2 Independent App Windows + Display 0 Mirroring on a connected Fold 8 when
// ANDROFUSE_LIVE_DEVICE=1 is set, verifying full cleanup afterward (INV-03).
func TestLiveFold8NativeDeXS2(t *testing.T) {
	if os.Getenv("ANDROFUSE_LIVE_DEVICE") != "1" {
		t.Skip("set ANDROFUSE_LIVE_DEVICE=1 with an attached Samsung DeX phone to run Spike S2/S4")
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
	if _, err := lib.Start(core.Config{Version: "0.1.0", StateDir: t.TempDir(), Profile: "live-dex"}); err != nil {
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

	prof, err := client.ProbeDevice(ctx, serial)
	if err != nil {
		t.Fatalf("ProbeDevice: %v", err)
	}
	probe, err := PushAndProbe(ctx, client, serial)
	if err != nil {
		t.Fatalf("PushAndProbe: %v", err)
	}
	t.Logf("Fold 8 probe: sdk=%d oneui=%s sem=%s builder=%v connect=%v avc_max=%d hevc_max=%d",
		probe.SDK, probe.OneUI, probe.Sem, probe.HasSemBuilder, probe.HasConnectWithConfig,
		prof.AVCEncoderMax, prof.HEVCEncoderMax)

	dexSess, err := StartNativeLoopback(ctx, client, store, serial, 1920, 1080, 160, 0)
	if err != nil {
		t.Fatalf("StartNativeLoopback: %v", err)
	}
	defer dexSess.Stop()

	ready := dexSess.Ready()
	if ready.DisplayID <= 0 {
		t.Fatalf("invalid DeX DisplayID: %+v", ready)
	}
	if err := dexSess.Ping(4 * time.Second); err != nil {
		t.Fatalf("dexSess.Ping: %v", err)
	}

	// Verify truth verdict & WifiDisplayStatus while live.
	liveProf, err := client.ProbeDevice(ctx, serial)
	if err != nil {
		t.Fatalf("live ProbeDevice: %v", err)
	}
	truth := EvaluateTruth(liveProf, probe, "AndroFuseDeX", BackendADBLoopback)
	if !truth.IsGenuineDeX {
		t.Fatalf("expected IsGenuineDeX=true, got %+v (wfd_state=%d)", truth, liveProf.WifiDisplayState)
	}

	// Attach scrcpy v5.0.1 + UHID mouse/keyboard to the Native DeX display.
	dexVideo, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
		SessionID:     "s2-dex-video",
		Serial:        serial,
		DisplayID:     ready.DisplayID,
		MaxFPS:        60,
		VideoBitRate:  12_000_000,
		EnableControl: true,
		EnableUHID:    true,
		InitialWidth:  uint32(ready.Width),
		InitialHeight: uint32(ready.Height),
	})
	if err != nil {
		t.Fatalf("scrcpy StartADB(DeX display %d): %v", ready.DisplayID, err)
	}
	defer dexVideo.Close()

	// Exercise UHID mouse move (no click).
	if err := dexVideo.MoveUHIDMouse(0, 8, 8, 0); err != nil {
		t.Fatalf("MoveUHIDMouse on DeX: %v", err)
	}
	stDex, err := dexVideo.WaitDecoded(3, 10*time.Second)
	if err != nil {
		t.Fatalf("DeX WaitDecoded: %v (stats=%+v)", err, stDex)
	}

	// Concurrently attach 2 Independent App Windows + Display 0 Mirror while Native DeX is running (Spike S4 4-stream!).
	app1, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
		SessionID:       "s4-dex-app1",
		Serial:          serial,
		NewDisplay:      "1080x720/160",
		FlexDisplay:     true,
		StartAppPackage: "com.sec.android.app.popupcalculator",
		MaxFPS:          60,
		InitialWidth:    1080,
		InitialHeight:   720,
	})
	if err != nil {
		t.Fatalf("Spike S4 concurrent app1 while DeX active: %v", err)
	}
	defer app1.Close()

	app2, err := scrcpy.StartADB(ctx, client, lib, store, LaunchConfigApp("s4-dex-app2", serial, "com.sec.android.app.clockpackage"))
	if err != nil {
		t.Fatalf("Spike S4 concurrent app2 while DeX active: %v", err)
	}
	defer app2.Close()

	mirror0, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
		SessionID:     "s4-dex-mirror0",
		Serial:        serial,
		DisplayID:     0,
		MaxSize:       1080,
		MaxFPS:        60,
		InitialWidth:  1080,
		InitialHeight: 800,
	})
	if err != nil {
		t.Fatalf("Spike S4 concurrent mirror0 while DeX+2apps active: %v", err)
	}
	defer mirror0.Close()

	stApp1, err := app1.WaitDecoded(2, 10*time.Second)
	if err != nil {
		t.Fatalf("concurrent app1 decode: %v", err)
	}
	stApp2, err := app2.WaitDecoded(2, 10*time.Second)
	if err != nil {
		t.Fatalf("concurrent app2 decode: %v", err)
	}
	stMirror, err := mirror0.WaitDecoded(2, 10*time.Second)
	if err != nil {
		t.Fatalf("concurrent mirror0 decode: %v", err)
	}
	t.Logf("Spike S2+S4 4-STREAM LIVE PASS: DeX=%dx%d(id=%d,wfd=%d,dec=%d,hw=%v) App1=%dx%d App2=%dx%d Mirror=%dx%d",
		stDex.NegotiatedWidth, stDex.NegotiatedHeight, ready.DisplayID, liveProf.WifiDisplayState, stDex.FramesDecoded, stDex.HardwareAccelerated,
		stApp1.NegotiatedWidth, stApp1.NegotiatedHeight,
		stApp2.NegotiatedWidth, stApp2.NegotiatedHeight,
		stMirror.NegotiatedWidth, stMirror.NegotiatedHeight)

	// Explicitly tear down all 4 sessions and verify zero ghost displays remain (INV-03).
	mirror0.Close()
	app2.Close()
	app1.Close()
	dexVideo.Close()
	dexSess.Stop()

	if rem := store.List(); len(rem) != 0 {
		t.Fatalf("residual leases after 4-stream teardown: %+v", rem)
	}
	dispOut, _ := client.Shell(ctx, serial, "dumpsys display | grep -E 'DisplayDeviceInfo|mCurrentStatus=WifiDisplayStatus'")
	if strings.Contains(dispOut, "AndroFuseDeX") || strings.Contains(dispOut, "scrcpy") {
		t.Fatalf("residual virtual/DeX display detected after teardown:\n%s", dispOut)
	}
}

func LaunchConfigApp(sessionID, serial, pkg string) scrcpy.LaunchConfig {
	return scrcpy.LaunchConfig{
		SessionID:       sessionID,
		Serial:          serial,
		NewDisplay:      "1080x720/160",
		FlexDisplay:     true,
		StartAppPackage: pkg,
		MaxFPS:          60,
		InitialWidth:    1080,
		InitialHeight:   720,
	}
}

// TestLiveFold8WirelessAndInteractiveDeX connects over TLS Wi-Fi ADB (`_adb-tls-connect._tcp`),
// launches Samsung Native DeX, validates interactive PointerIDMouse/scroll/Recents injection,
// tests dynamic DeX display resize, tests Wireless Mirror + SetDisplayPower + Independent App Window,
// and asserts zero residual ghost displays or forwards (`INV-03`).
func TestLiveFold8WirelessAndInteractiveDeX(t *testing.T) {
	if os.Getenv("ANDROFUSE_LIVE_DEVICE") != "1" {
		t.Skip("set ANDROFUSE_LIVE_DEVICE=1 with an attached Fold 8 on Wi-Fi to run wireless + interactive DeX tests")
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
	if _, err := lib.Start(core.Config{Version: "0.1.0", StateDir: t.TempDir(), Profile: "live-wireless-dex"}); err != nil {
		t.Fatalf("core.Start: %v", err)
	}

	client, err := adb.FindClient()
	if err != nil {
		t.Fatalf("FindClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	svcs, err := client.MDNSServices(ctx)
	if err != nil {
		t.Fatalf("MDNSServices: %v", err)
	}
	var connectEndpoint string
	var maskedEndpoint string
	for _, s := range svcs {
		if s.CanConnect {
			connectEndpoint = s.Addr
			maskedEndpoint = s.MaskedAddr
			t.Logf("mDNS discovered TLS connect endpoint: %s (%s)", s.MaskedAddr, s.ServiceType)
			break
		}
	}
	if connectEndpoint == "" {
		t.Skip("no active _adb-tls-connect._tcp mDNS endpoint found on LAN")
	}

	if _, err := client.ConnectTLS(ctx, connectEndpoint, false); err != nil {
		t.Fatalf("ConnectTLS(%s): %v", maskedEndpoint, err)
	}

	devs, err := client.Devices(ctx)
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	var wifiSerial string
	hasUSB := false
	for _, d := range devs {
		if d.State != "device" {
			continue
		}
		if d.Transport == adb.TransportUSB {
			hasUSB = true
		}
		if d.Transport == adb.TransportTLSWiFi && wifiSerial == "" {
			wifiSerial = d.Serial
			t.Logf("selected wireless target: %s model=%s transport=%s", d.MaskedID, d.Model, d.Transport)
		}
	}
	t.Logf("transport summary: hasUSB=%v hasTLSWiFi=%v", hasUSB, wifiSerial != "")
	if wifiSerial == "" {
		t.Fatalf("connected %s did not appear as online TLS Wi-Fi ADB device (%+v)", maskedEndpoint, devs)
	}

	store, err := lease.Open(filepath.Join(t.TempDir(), "leases.json"))
	if err != nil {
		t.Fatalf("lease.Open: %v", err)
	}

	// Verify app list enumeration over wireless ADB (`--user 0`).
	apps, err := client.ListLaunchableApps(ctx, wifiSerial)
	if err != nil || len(apps) == 0 {
		t.Fatalf("ListLaunchableApps over TLS Wi-Fi failed: err=%v count=%d", err, len(apps))
	}
	t.Logf("wireless ListLaunchableApps: count=%d first=%s", len(apps), apps[0].Package)

	// Start Native Samsung DeX Loopback over TLS Wi-Fi ADB.
	dexSess, err := StartNativeLoopback(ctx, client, store, wifiSerial, 1920, 1080, 160, 5)
	if err != nil {
		t.Fatalf("wireless StartNativeLoopback: %v", err)
	}
	defer dexSess.Stop()

	ready := dexSess.Ready()
	if ready.DisplayID <= 0 {
		t.Fatalf("invalid DeX DisplayID: %+v", ready)
	}

	var renderedMu sync.Mutex
	var renderedOK bool
	dexVideo, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
		SessionID:     "wifi-dex-video",
		Serial:        wifiSerial,
		DisplayID:     ready.DisplayID,
		MaxFPS:        60,
		VideoBitRate:  12_000_000,
		EnableControl: true,
		EnableUHID:    true,
		InitialWidth:  uint32(ready.Width),
		InitialHeight: uint32(ready.Height),
		OnFrame: func(f core.NV12Frame) {
			renderedMu.Lock()
			defer renderedMu.Unlock()
			if renderedOK || f.Width <= 0 || f.Height <= 0 {
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
				ui.Image(c.Key("wifi-dex-frame"), bmp).Fill()
			}, 640, 360)
			tt.Frame()
			renderedOK = true
		},
	})
	if err != nil {
		t.Fatalf("wireless scrcpy StartADB(DeX id=%d): %v", ready.DisplayID, err)
	}
	defer dexVideo.Close()

	st0, err := dexVideo.WaitDecoded(3, 12*time.Second)
	if err != nil {
		t.Fatalf("wireless DeX WaitDecoded: %v (stats=%+v)", err, st0)
	}

	// Inspect One UI 9 DeX display windowing tokens without leaking PII.
	winShell := "dumpsys window displays | grep -E 'Display: mDisplayId=" +
		strconv.Itoa(ready.DisplayID) +
		"|Taskbar|Desktop|StatusBar|NavigationBar|Desk' | head -n 20"
	winOut, _ := client.Shell(ctx, wifiSerial, winShell)
	t.Logf("One UI 9 DeX display %d window surface summary:\n%s", ready.DisplayID, strings.TrimSpace(winOut))

	// Exercise interactive PointerIDMouse hover, left-click, right-click, scroll, and Recents on DeX display.
	sw, sh := uint16(ready.Width), uint16(ready.Height)
	if err := dexVideo.InjectTouch(scrcpy.ActionHoverMove, scrcpy.PointerIDMouse, 960, 540, sw, sh, 0, 0, 0); err != nil {
		t.Fatalf("PointerIDMouse hover: %v", err)
	}
	if err := dexVideo.InjectTouch(scrcpy.ActionDown, scrcpy.PointerIDMouse, 960, 540, sw, sh, 0xFFFF, scrcpy.ButtonPrimary, scrcpy.ButtonPrimary); err != nil {
		t.Fatalf("PointerIDMouse down: %v", err)
	}
	if err := dexVideo.InjectTouch(scrcpy.ActionUp, scrcpy.PointerIDMouse, 960, 540, sw, sh, 0, scrcpy.ButtonPrimary, 0); err != nil {
		t.Fatalf("PointerIDMouse up: %v", err)
	}
	if err := dexVideo.InjectTouch(scrcpy.ActionDown, scrcpy.PointerIDMouse, 960, 540, sw, sh, 0xFFFF, scrcpy.ButtonSecondary, scrcpy.ButtonSecondary); err != nil {
		t.Fatalf("PointerIDMouse right down: %v", err)
	}
	if err := dexVideo.InjectTouch(scrcpy.ActionUp, scrcpy.PointerIDMouse, 960, 540, sw, sh, 0, scrcpy.ButtonSecondary, 0); err != nil {
		t.Fatalf("PointerIDMouse right up: %v", err)
	}
	if err := dexVideo.InjectScroll(960, 540, sw, sh, 0, -0.5, 0); err != nil {
		t.Fatalf("InjectScroll down: %v", err)
	}
	if err := dexVideo.InjectScroll(960, 540, sw, sh, 0, 0.5, 0); err != nil {
		t.Fatalf("InjectScroll up: %v", err)
	}
	if err := dexVideo.TapKeycode(scrcpy.KeycodeAppSwitch); err != nil {
		t.Fatalf("TapKeycode Recents: %v", err)
	}
	time.Sleep(350 * time.Millisecond)
	_ = dexVideo.TapKeycode(scrcpy.KeycodeEscape)
	time.Sleep(250 * time.Millisecond)

	// Test dynamic DeX display RESIZE command and observe whether scrcpy auto-reconfigures.
	resized, err := dexSess.Resize(1600, 900, 160, 8*time.Second)
	if err != nil {
		t.Fatalf("dexSess.Resize(1600x900@160): %v", err)
	}
	if resized.Width != 1600 || resized.Height != 900 || resized.DPI != 160 {
		t.Fatalf("unexpected RESIZED metrics: %+v", resized)
	}
	deadline := time.Now().Add(3 * time.Second)
	var stAfterResize scrcpy.SessionStats
	for time.Now().Before(deadline) {
		stAfterResize = dexVideo.Stats()
		if stAfterResize.NegotiatedWidth == 1600 && stAfterResize.NegotiatedHeight == 900 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	renderedMu.Lock()
	rOK := renderedOK
	renderedMu.Unlock()
	t.Logf("WIRELESS DEX RESULT: id=%d init=%dx%d dec=%d fps=%.1f decode_us=%d hw=%v updateYUV=%v | after_wm_resize=%dx%d (sessions=%d)",
		ready.DisplayID, st0.NegotiatedWidth, st0.NegotiatedHeight, st0.FramesDecoded, st0.MeasuredFPS,
		st0.AvgDecodeUs, st0.HardwareAccelerated, rOK,
		stAfterResize.NegotiatedWidth, stAfterResize.NegotiatedHeight, stAfterResize.SessionCount)
	if !rOK {
		t.Fatal("expected UpdateYUV render on wireless DeX stream")
	}

	// Wireless TLS Wi-Fi Phone Mirror (Display 0) + SetDisplayPower(false/true) + Independent App Window.
	mirror0, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
		SessionID:     "wifi-mirror0",
		Serial:        wifiSerial,
		DisplayID:     0,
		MaxSize:       1080,
		MaxFPS:        60,
		EnableControl: true,
		InitialWidth:  1080,
		InitialHeight: 814,
	})
	if err != nil {
		t.Fatalf("wireless mirror0 StartADB: %v", err)
	}
	defer mirror0.Close()

	appWin, err := scrcpy.StartADB(ctx, client, lib, store, LaunchConfigApp("wifi-app-calc", wifiSerial, "com.sec.android.app.popupcalculator"))
	if err != nil {
		t.Fatalf("wireless appWin StartADB: %v", err)
	}
	defer appWin.Close()

	stM, err := mirror0.WaitDecoded(2, 10*time.Second)
	if err != nil {
		t.Fatalf("wireless mirror0 WaitDecoded: %v", err)
	}
	stA, err := appWin.WaitDecoded(2, 10*time.Second)
	if err != nil {
		t.Fatalf("wireless appWin WaitDecoded: %v", err)
	}
	if err := mirror0.SetDisplayPower(false); err != nil {
		t.Fatalf("SetDisplayPower(false): %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := mirror0.SetDisplayPower(true); err != nil {
		t.Fatalf("SetDisplayPower(true): %v", err)
	}
	t.Logf("WIRELESS SCRCPY PASS: mirror0=%dx%d(fps=%.1f,us=%d,hw=%v) appCalc=%dx%d(fps=%.1f,us=%d,hw=%v)",
		stM.NegotiatedWidth, stM.NegotiatedHeight, stM.MeasuredFPS, stM.AvgDecodeUs, stM.HardwareAccelerated,
		stA.NegotiatedWidth, stA.NegotiatedHeight, stA.MeasuredFPS, stA.AvgDecodeUs, stA.HardwareAccelerated)

	appWin.Close()
	mirror0.Close()
	dexVideo.Close()
	dexSess.Stop()

	if rem := store.List(); len(rem) != 0 {
		t.Fatalf("residual leases after wireless teardown: %+v", rem)
	}
	dispOut, _ := client.Shell(ctx, wifiSerial, "dumpsys display | grep -E 'DisplayDeviceInfo|mCurrentStatus=WifiDisplayStatus'")
	if strings.Contains(dispOut, "AndroFuseDeX") || strings.Contains(dispOut, "scrcpy") {
		t.Fatalf("residual virtual/DeX display detected after wireless teardown:\n%s", dispOut)
	}
}
