package dex

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/scrcpy"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
)

// TestLiveEmulatorDaemon verifies the persistent UID 2000 daemon (`PLAN` §9.2 Phase 1 & 2) on
// an Android emulator (`Pixel_9a_API_36`):
//   - Install/start Companion APK, launch detached `dexhelper daemon` as UID 2000 shell
//   - Verify Binder handshake, `ADD_TRUSTED_DISPLAY`, `Os.socketpair` `ParcelFileDescriptor` echo,
//     and zero `avc: denied` for androfuse
//   - Stream interactive phone mirroring (`daemon.open` + `daemon.ctrl` -> `scrcpy.StartFromConns`)
//     with live NV12 decoding + `InjectKeycode(Home)`
//   - Stream a chromeless independent app window (`544x960/200`, `vd_system_decorations=false`,
//     `flex_display=true`, `StartApp("com.android.settings")`) and verify zero residual `scrcpy`
//     processes or virtual displays after close (`INV-03`).
//
// Run with `ANDROFUSE_LIVE_EMULATOR=1` against a running `emulator-*`.
func TestLiveEmulatorDaemon(t *testing.T) {
	if os.Getenv("ANDROFUSE_LIVE_EMULATOR") != "1" {
		t.Skip("set ANDROFUSE_LIVE_EMULATOR=1 to run the live emulator UID 2000 daemon test")
	}
	client, err := adb.FindClient()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	devs, err := client.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var serial string
	for _, d := range devs {
		if strings.HasPrefix(d.Serial, "emulator-") && d.State == "device" {
			serial = d.Serial
			break
		}
	}
	if serial == "" {
		t.Skip("no running Android emulator attached")
	}

	lib := openDaemonLiveCore(t)
	defer lib.Stop()
	if _, err := lib.Start(core.Config{Version: version.Version, StateDir: t.TempDir(), Profile: "live-daemon"}); err != nil {
		t.Fatal(err)
	}

	apk, err := filepath.Abs(filepath.Join("..", "..", "build", fmt.Sprintf("AndroFuse-%s-android-arm64.apk", version.Version)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(apk); err != nil {
		t.Fatalf("Companion APK missing (%v); run tools/buildapk first", err)
	}
	if _, err := client.Run(ctx, "-s", serial, "install", "-r", apk); err != nil {
		t.Fatalf("adb install -r: %v", err)
	}

	const pkg = "com.yuanzhe.androfuse"
	_, _ = client.Shell(ctx, serial, "logcat -c")
	_, _ = client.Shell(ctx, serial, "pkill -f com.androfuse.dexhelper.Main; pkill -f com.genymobile.scrcpy.Server; rm -f /data/local/tmp/androfuse-daemon.lock")
	if _, err := client.Shell(ctx, serial, "am start -a android.intent.action.VIEW -d androfuse://pair "+pkg); err != nil {
		t.Fatalf("am start Companion: %v", err)
	}

	var fwdPort int
	var pairRes companion.PairResponse
	stopTapping := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopTapping:
				return
			case <-time.After(1 * time.Second):
				tapAllow(ctx, client, serial)
			}
		}
	}()
	defer close(stopTapping)

	for deadline := time.Now().Add(45 * time.Second); ; time.Sleep(time.Second) {
		p, err := client.ForwardPort(ctx, serial, 0, "localabstract:"+companion.LocalSocket)
		if err == nil {
			fwdPort = p
			pr, pErr := companion.RequestPairing(ctx, fmt.Sprintf("127.0.0.1:%d", fwdPort), "emulator-daemon-test", "123456")
			if pErr == nil {
				pairRes = pr
				break
			}
			t.Logf("RequestPairing retrying: %v", pErr)
			_ = client.RemoveForward(ctx, serial, fwdPort)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out pairing Companion on %s", serial)
		}
	}
	defer func() { _ = client.RemoveForward(ctx, serial, fwdPort) }()
	addr := fmt.Sprintf("127.0.0.1:%d", fwdPort)

	if err := StartDaemon(ctx, client, serial, pkg); err != nil {
		t.Fatalf("StartDaemon: %v", err)
	}
	defer func() {
		_, _ = client.Shell(ctx, serial, "pkill -f com.androfuse.dexhelper.Main; pkill -f com.genymobile.scrcpy.Server; rm -f /data/local/tmp/androfuse-daemon.lock")
	}()

	comp, err := companion.Dial(ctx, addr, pairRes.Token, pairRes.CertFingerprint)
	if err != nil {
		t.Fatalf("companion.Dial: %v", err)
	}
	defer comp.Close()

	var st companion.DeviceStatus
	for deadline := time.Now().Add(25 * time.Second); ; time.Sleep(500 * time.Millisecond) {
		st, err = comp.GetStatus(ctx)
		if err == nil && st.DaemonReady && st.DaemonPID > 0 && st.DaemonFDVerified {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not become ready+fd_verified; last status=%+v err=%v", st, err)
		}
	}
	t.Logf("UID 2000 daemon live: pid=%d trusted_display=%v fd_verified=%v", st.DaemonPID, st.DaemonTrustedDisplay, st.DaemonFDVerified)
	if !st.DaemonTrustedDisplay {
		t.Fatalf("expected DaemonTrustedDisplay=true on Android 16 emulator, got false (status=%+v)", st)
	}

	// Phase 2A: Interactive phone mirroring via `daemon.open` + `daemon.ctrl`.
	vidConn, ctrlConn, err := companion.DialDaemonStream(ctx, addr, pairRes.Token, pairRes.CertFingerprint, companion.DaemonStreamRequest{
		MaxSize: 720,
		BitRate: 4_000_000,
		MaxFPS:  30,
		Codec:   "h264",
		Control: true,
	})
	if err != nil {
		t.Fatalf("DialDaemonStream mirror: %v", err)
	}
	var mirrorFrames atomic.Int64
	mirrorSess, err := scrcpy.StartFromConns(lib, nil, nil, 0, 0, nil, vidConn, ctrlConn, true, scrcpy.LaunchConfig{
		SessionID:     "daemon-mirror",
		EnableControl: true,
		InitialWidth:  720,
		InitialHeight: 1280,
		OnFrame:       func(core.NV12Frame) { mirrorFrames.Add(1) },
	})
	if err != nil {
		t.Fatalf("StartFromConns mirror: %v", err)
	}
	if err := mirrorSess.InjectKeycode(scrcpy.KeyActionDown, scrcpy.KeycodeHome, 0, 0); err != nil {
		mirrorSess.Close()
		t.Fatalf("InjectKeycode(Home down): %v", err)
	}
	if err := mirrorSess.InjectKeycode(scrcpy.KeyActionUp, scrcpy.KeycodeHome, 0, 0); err != nil {
		mirrorSess.Close()
		t.Fatalf("InjectKeycode(Home up): %v", err)
	}
	for deadline := time.Now().Add(20 * time.Second); mirrorFrames.Load() < 5; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			mirrorSess.Close()
			t.Fatalf("mirror via daemon produced %d frames (< 5)", mirrorFrames.Load())
		}
	}
	t.Logf("Daemon interactive mirror: %d decoded frames", mirrorFrames.Load())
	mirrorSess.Close()

	// Phase 2B: Chromeless independent app window (`544x960/200`) via `daemon.open`.
	appVid, appCtrl, err := companion.DialDaemonStream(ctx, addr, pairRes.Token, pairRes.CertFingerprint, companion.DaemonStreamRequest{
		BitRate:               4_000_000,
		MaxFPS:                30,
		Codec:                 "h264",
		NewDisplay:            "544x960/200",
		HideSystemDecorations: true,
		FlexDisplay:           true,
		Control:               true,
	})
	if err != nil {
		t.Fatalf("DialDaemonStream app window: %v", err)
	}
	var appFrames atomic.Int64
	appSess, err := scrcpy.StartFromConns(lib, nil, nil, 0, 0, nil, appVid, appCtrl, true, scrcpy.LaunchConfig{
		SessionID:             "daemon-app",
		NewDisplay:            "544x960/200",
		HideSystemDecorations: true,
		FlexDisplay:           true,
		StartAppPackage:       "com.android.settings",
		EnableControl:         true,
		InitialWidth:          544,
		InitialHeight:         960,
		OnFrame:               func(core.NV12Frame) { appFrames.Add(1) },
	})
	if err != nil {
		t.Fatalf("StartFromConns app window: %v", err)
	}
	for deadline := time.Now().Add(25 * time.Second); appFrames.Load() < 5; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			appSess.Close()
			t.Fatalf("app window via daemon produced %d frames (< 5)", appFrames.Load())
		}
	}
	stApp := appSess.Stats()
	t.Logf("Daemon app window live: frames=%d size=%dx%d hw=%v", appFrames.Load(), stApp.NegotiatedWidth, stApp.NegotiatedHeight, stApp.HardwareAccelerated)
	appSess.Close()

	// Verify clean teardown (`INV-03`): zero scrcpy processes or residual virtual displays.
	time.Sleep(1500 * time.Millisecond)
	if ps, _ := client.Shell(ctx, serial, "ps -A -o NAME,ARGS | grep scrcpy | grep -v grep"); strings.TrimSpace(ps) != "" {
		t.Fatalf("residual scrcpy process after daemon close:\n%s", ps)
	}
	if disp, _ := client.Shell(ctx, serial, "dumpsys display | grep -i scrcpy"); strings.TrimSpace(disp) != "" {
		t.Fatalf("residual scrcpy virtual display after daemon close:\n%s", disp)
	}

	out, _ := client.Shell(ctx, serial, "logcat -d -s auditd:* | grep -i avc || true")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "androfuse") && strings.Contains(line, "avc:") {
			t.Fatalf("unexpected SELinux denial for androfuse: %s", line)
		}
	}
}

// TestLiveFold8Daemon probes One UI 9 / Android 17 capabilities on a live non-emulator device
// (`SM_F971U1`) without overwriting a mismatched release-signed Companion APK (`INSTALL_FAILED_UPDATE_INCOMPATIBLE`).
// When the installed Companion includes `AndroFuseDaemonProvider`, it launches `dexhelper daemon`
// and verifies Binder + `Os.socketpair` FD transfer, mirroring, and virtual desktop streaming
// cleanly without residual processes or virtual displays (`INV-03`).
func TestLiveFold8Daemon(t *testing.T) {
	if os.Getenv("ANDROFUSE_LIVE_DEVICE") != "1" {
		t.Skip("set ANDROFUSE_LIVE_DEVICE=1 to run the live Fold 8 UID 2000 daemon test")
	}
	client, err := adb.FindClient()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	devs, err := client.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var serial string
	for _, d := range devs {
		if !strings.HasPrefix(d.Serial, "emulator-") && d.State == "device" {
			serial = d.Serial
			break
		}
	}
	if serial == "" {
		t.Skip("no live physical Android device attached")
	}

	shellPerm, _ := client.Shell(ctx, serial, "dumpsys package com.android.shell | grep ADD_TRUSTED_DISPLAY || true")
	hasShellTrusted := strings.Contains(shellPerm, "ADD_TRUSTED_DISPLAY") && strings.Contains(shellPerm, "granted=true")
	t.Logf("Fold 8 (%s) shell ADD_TRUSTED_DISPLAY granted=%v (%s)", serial, hasShellTrusted, strings.TrimSpace(shellPerm))

	const pkg = "com.yuanzhe.androfuse"
	apk, err := filepath.Abs(filepath.Join("..", "..", "build", fmt.Sprintf("AndroFuse-%s-android-arm64.apk", version.Version)))
	if err == nil {
		if _, sErr := os.Stat(apk); sErr == nil {
			if out, iErr := client.Run(ctx, "-s", serial, "install", "-r", apk); iErr != nil {
				if strings.Contains(out, "INSTALL_FAILED_UPDATE_INCOMPATIBLE") || strings.Contains(iErr.Error(), "INSTALL_FAILED_UPDATE_INCOMPATIBLE") {
					t.Logf("skipping debug APK overwrite on %s (release-signed Companion kept intact)", serial)
				} else {
					t.Fatalf("adb install -r on %s: %v (%s)", serial, iErr, out)
				}
			}
		}
	}

	provOut, _ := client.Shell(ctx, serial, "dumpsys package "+pkg+" | grep -i AndroFuseDaemonProvider || true")
	if !strings.Contains(provOut, "AndroFuseDaemonProvider") {
		t.Logf("installed Companion on %s lacks AndroFuseDaemonProvider (release v0.1.0 APK kept intact); install v0.2.0-dev APK to complete Knox FD + DeX test", serial)
		return
	}

	lib := openDaemonLiveCore(t)
	defer lib.Stop()
	if _, err := lib.Start(core.Config{Version: version.Version, StateDir: t.TempDir(), Profile: "live-fold8-daemon"}); err != nil {
		t.Fatal(err)
	}

	_, _ = client.Shell(ctx, serial, "logcat -c")
	_, _ = client.Shell(ctx, serial, "pkill -f com.androfuse.dexhelper.Main; pkill -f com.genymobile.scrcpy.Server; rm -f /data/local/tmp/androfuse-daemon.lock")
	if _, err := client.Shell(ctx, serial, "am start -a android.intent.action.VIEW -d androfuse://pair "+pkg); err != nil {
		t.Fatalf("am start Companion: %v", err)
	}

	var fwdPort int
	var pairRes companion.PairResponse
	stopTapping := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopTapping:
				return
			case <-time.After(1 * time.Second):
				tapAllow(ctx, client, serial)
			}
		}
	}()
	defer close(stopTapping)

	for deadline := time.Now().Add(45 * time.Second); ; time.Sleep(time.Second) {
		p, err := client.ForwardPort(ctx, serial, 0, "localabstract:"+companion.LocalSocket)
		if err == nil {
			fwdPort = p
			pr, pErr := companion.RequestPairing(ctx, fmt.Sprintf("127.0.0.1:%d", fwdPort), "fold8-daemon-test", "123456")
			if pErr == nil {
				pairRes = pr
				break
			}
			t.Logf("RequestPairing retrying: %v", pErr)
			_ = client.RemoveForward(ctx, serial, fwdPort)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out pairing Companion on %s", serial)
		}
	}
	defer func() { _ = client.RemoveForward(ctx, serial, fwdPort) }()
	addr := fmt.Sprintf("127.0.0.1:%d", fwdPort)

	if err := StartDaemon(ctx, client, serial, pkg); err != nil {
		t.Fatalf("StartDaemon: %v", err)
	}
	defer func() {
		_, _ = client.Shell(ctx, serial, "pkill -f com.androfuse.dexhelper.Main; pkill -f com.genymobile.scrcpy.Server; rm -f /data/local/tmp/androfuse-daemon.lock")
	}()

	comp, err := companion.Dial(ctx, addr, pairRes.Token, pairRes.CertFingerprint)
	if err != nil {
		t.Fatalf("companion.Dial: %v", err)
	}
	defer comp.Close()

	var st companion.DeviceStatus
	for deadline := time.Now().Add(25 * time.Second); ; time.Sleep(500 * time.Millisecond) {
		st, err = comp.GetStatus(ctx)
		if err == nil && st.DaemonReady && st.DaemonPID > 0 && st.DaemonFDVerified {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Fold 8 daemon did not become ready+fd_verified; last status=%+v err=%v", st, err)
		}
	}
	t.Logf("Fold 8 UID 2000 daemon live: pid=%d trusted_display=%v fd_verified=%v", st.DaemonPID, st.DaemonTrustedDisplay, st.DaemonFDVerified)

	vidConn, ctrlConn, err := companion.DialDaemonStream(ctx, addr, pairRes.Token, pairRes.CertFingerprint, companion.DaemonStreamRequest{
		MaxSize: 720,
		BitRate: 4_000_000,
		MaxFPS:  30,
		Codec:   "h264",
		Control: true,
	})
	if err != nil {
		t.Fatalf("DialDaemonStream Fold 8 mirror: %v", err)
	}
	var mirrorFrames atomic.Int64
	mirrorSess, err := scrcpy.StartFromConns(lib, nil, nil, 0, 0, nil, vidConn, ctrlConn, true, scrcpy.LaunchConfig{
		SessionID:     "fold8-daemon-mirror",
		EnableControl: true,
		InitialWidth:  720,
		InitialHeight: 1280,
		OnFrame:       func(core.NV12Frame) { mirrorFrames.Add(1) },
	})
	if err != nil {
		t.Fatalf("StartFromConns Fold 8 mirror: %v", err)
	}
	for deadline := time.Now().Add(20 * time.Second); mirrorFrames.Load() < 5; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			mirrorSess.Close()
			t.Fatalf("Fold 8 daemon mirror produced %d frames (< 5)", mirrorFrames.Load())
		}
	}
	t.Logf("Fold 8 daemon mirror: %d decoded frames", mirrorFrames.Load())
	mirrorSess.Close()

	if st.DaemonTrustedDisplay {
		nativeVid, nativeCtrl, nErr := companion.DialDaemonStream(ctx, addr, pairRes.Token, pairRes.CertFingerprint, companion.DaemonStreamRequest{
			BitRate:    8_000_000,
			MaxFPS:     60,
			Codec:      "h264",
			NewDisplay: "dex:1920x1080/160",
			Control:    true,
		})
		if nErr != nil {
			t.Fatalf("DialDaemonStream Fold 8 Native DeX: %v", nErr)
		}
		var nativeFrames atomic.Int64
		nativeSess, nErr := scrcpy.StartFromConns(lib, nil, nil, 0, 0, nil, nativeVid, nativeCtrl, true, scrcpy.LaunchConfig{
			SessionID:     "fold8-daemon-native-dex",
			EnableControl: true,
			InitialWidth:  1920,
			InitialHeight: 1080,
			OnFrame:       func(core.NV12Frame) { nativeFrames.Add(1) },
		})
		if nErr != nil {
			t.Fatalf("StartFromConns Fold 8 Native DeX: %v", nErr)
		}
		for deadline := time.Now().Add(25 * time.Second); nativeFrames.Load() < 5; time.Sleep(200 * time.Millisecond) {
			if time.Now().After(deadline) {
				nativeSess.Close()
				t.Fatalf("Fold 8 daemon Native DeX produced %d frames (< 5)", nativeFrames.Load())
			}
		}
		stN := nativeSess.Stats()
		t.Logf("Fold 8 daemon Native DeX: frames=%d size=%dx%d hw=%v", nativeFrames.Load(), stN.NegotiatedWidth, stN.NegotiatedHeight, stN.HardwareAccelerated)
		nativeSess.Close()

		dexVid, dexCtrl, err := companion.DialDaemonStream(ctx, addr, pairRes.Token, pairRes.CertFingerprint, companion.DaemonStreamRequest{
			BitRate:     8_000_000,
			MaxFPS:      60,
			Codec:       "h264",
			NewDisplay:  "1920x1080/200",
			FlexDisplay: true,
			Control:     true,
		})
		if err != nil {
			t.Fatalf("DialDaemonStream Fold 8 virtual desktop: %v", err)
		}
		var dexFrames atomic.Int64
		dexSess, err := scrcpy.StartFromConns(lib, nil, nil, 0, 0, nil, dexVid, dexCtrl, true, scrcpy.LaunchConfig{
			SessionID:     "fold8-daemon-desktop",
			NewDisplay:    "1920x1080/200",
			FlexDisplay:   true,
			EnableControl: true,
			InitialWidth:  1920,
			InitialHeight: 1080,
			OnFrame:       func(core.NV12Frame) { dexFrames.Add(1) },
		})
		if err != nil {
			t.Fatalf("StartFromConns Fold 8 desktop: %v", err)
		}
		for deadline := time.Now().Add(25 * time.Second); dexFrames.Load() < 5; time.Sleep(200 * time.Millisecond) {
			if time.Now().After(deadline) {
				dexSess.Close()
				t.Fatalf("Fold 8 daemon virtual desktop produced %d frames (< 5)", dexFrames.Load())
			}
		}
		stD := dexSess.Stats()
		t.Logf("Fold 8 daemon virtual desktop: frames=%d size=%dx%d hw=%v", dexFrames.Load(), stD.NegotiatedWidth, stD.NegotiatedHeight, stD.HardwareAccelerated)
		dexSess.Close()
	}

	time.Sleep(1500 * time.Millisecond)
	if ps, _ := client.Shell(ctx, serial, "ps -A -o NAME,ARGS | grep scrcpy | grep -v grep"); strings.TrimSpace(ps) != "" {
		t.Fatalf("residual scrcpy process on Fold 8:\n%s", ps)
	}
	if disp, _ := client.Shell(ctx, serial, "dumpsys display | grep -i scrcpy"); strings.TrimSpace(disp) != "" {
		t.Fatalf("residual scrcpy display on Fold 8:\n%s", disp)
	}

	avcOut, _ := client.Shell(ctx, serial, "logcat -d -s auditd:* | grep -i avc || true")
	for _, line := range strings.Split(avcOut, "\n") {
		if strings.Contains(line, "androfuse") && strings.Contains(line, "avc:") {
			t.Fatalf("unexpected SELinux denial on Fold 8: %s", line)
		}
	}
}

func openDaemonLiveCore(t *testing.T) *core.Lib {
	t.Helper()
	name := core.LibraryName()
	for _, rel := range []string{
		filepath.Join("..", "..", "core", "target", "debug", name),
		filepath.Join("..", "..", "core", "target", "release", name),
		filepath.Join("..", "..", "build", name),
	} {
		p, err := filepath.Abs(rel)
		if err != nil {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			lib, err := core.Open(p)
			if err == nil {
				return lib
			}
		}
	}
	t.Fatalf("could not locate or open %s", name)
	return nil
}

func tapAllow(ctx context.Context, client *adb.Client, serial string) {
	if _, err := client.Shell(ctx, serial, "uiautomator dump /sdcard/af-daemon-ui.xml >/dev/null 2>&1"); err != nil {
		return
	}
	raw, err := client.Shell(ctx, serial, "cat /sdcard/af-daemon-ui.xml")
	if err != nil {
		return
	}
	type node struct {
		Text   string `xml:"text,attr"`
		Desc   string `xml:"content-desc,attr"`
		Bounds string `xml:"bounds,attr"`
		Nodes  []node `xml:"node"`
	}
	var root node
	if xml.Unmarshal([]byte(raw), &root) != nil {
		return
	}
	re := regexp.MustCompile(`\[(\d+),(\d+)\]\[(\d+),(\d+)\]`)
	var walk func(n node) bool
	walk = func(n node) bool {
		for _, w := range []string{"Allow", "允许"} {
			if strings.EqualFold(strings.TrimSpace(n.Text), w) || strings.EqualFold(strings.TrimSpace(n.Desc), w) {
				if m := re.FindStringSubmatch(n.Bounds); m != nil {
					x1, _ := strconv.Atoi(m[1])
					y1, _ := strconv.Atoi(m[2])
					x2, _ := strconv.Atoi(m[3])
					y2, _ := strconv.Atoi(m[4])
					_, _ = client.Shell(ctx, serial, fmt.Sprintf("input tap %d %d", (x1+x2)/2, (y1+y2)/2))
					return true
				}
			}
		}
		for _, c := range n.Nodes {
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(root)
}
