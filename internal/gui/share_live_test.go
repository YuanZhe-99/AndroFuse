package gui

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
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

// tapText taps the first on-screen element whose text or description matches one of words, using
// uiautomator; it reports whether it tapped. Test helper for the consent dialogs a person answers.
func tapText(ctx context.Context, client *adb.Client, serial string, words ...string) bool {
	if _, err := client.Shell(ctx, serial, "uiautomator dump /sdcard/af-ui.xml >/dev/null 2>&1"); err != nil {
		return false
	}
	raw, err := client.Shell(ctx, serial, "cat /sdcard/af-ui.xml")
	if err != nil {
		return false
	}
	type node struct {
		Text   string `xml:"text,attr"`
		Desc   string `xml:"content-desc,attr"`
		Bounds string `xml:"bounds,attr"`
		Nodes  []node `xml:"node"`
	}
	var root node
	if xml.Unmarshal([]byte(raw), &root) != nil {
		return false
	}
	re := regexp.MustCompile(`\[(\d+),(\d+)\]\[(\d+),(\d+)\]`)
	var walk func(n node) bool
	walk = func(n node) bool {
		for _, w := range words {
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
	return walk(root)
}

// TestLiveCompanionScreenShare drives Companion screen sharing end to end against a phone or
// emulator: pair over adb (answering the Companion's Allow), turn on the phone's "Share the
// screen on mobile data" permission (run-as on a debug build), ask for the screen, answer Android's
// capture consent, and decode frames on this computer with the scrcpy decoder.
//
// Run with ANDROFUSE_LIVE_SHARE=<adb serial>, after building build/AndroFuse-<version>-android-arm64.apk
// and core/target/debug's library.
func TestLiveCompanionScreenShare(t *testing.T) {
	serial := os.Getenv("ANDROFUSE_LIVE_SHARE")
	if serial == "" {
		t.Skip("set ANDROFUSE_LIVE_SHARE=<adb serial> to run the live screen-sharing test")
	}
	apk, _ := filepath.Abs(filepath.Join("..", "..", "build", fmt.Sprintf("AndroFuse-%s-android-arm64.apk", version.Version)))
	libPath, _ := filepath.Abs(filepath.Join("..", "..", "core", "target", "debug", core.LibraryName()))
	lib, err := core.Open(libPath)
	if err != nil {
		t.Fatalf("core.Open: %v", err)
	}
	defer lib.Stop()
	if _, err := lib.Start(core.Config{Version: version.Version, StateDir: t.TempDir(), Profile: "live-share"}); err != nil {
		t.Fatal(err)
	}
	client, err := adb.FindClient()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	// Answer the Companion's pairing prompt and, later, Android's capture consent.
	stopTapping := make(chan struct{})
	defer close(stopTapping)
	go func() {
		for {
			select {
			case <-stopTapping:
				return
			case <-time.After(1500 * time.Millisecond):
			}
			// Android 16 asks "Share one app" or "Share entire screen" first; the desktop needs the
			// screen, so pick it, then press its button. Older versions show "Start now".
			switch {
			case tapText(ctx, client, serial, "Share screen", "Start now", "Start", "共享屏幕", "立即开始", "开始"):
			case tapText(ctx, client, serial, "Share entire screen", "共享整个屏幕"):
			case tapText(ctx, client, serial, "Share one app", "共享一个应用"):
			default:
				tapText(ctx, client, serial, "Allow", "允许")
			}
		}
	}()

	c, _, res, err := pairOverADB(ctx, client, serial, apk, func(s string) { t.Log(s) }, nil)
	if err != nil {
		t.Fatalf("pairOverADB: %v", err)
	}
	_ = c.Close()
	t.Logf("paired, fingerprint %s…", res.CertFingerprint[:11])

	const pkg = "com.yuanzhe.androfuse"
	// Turn on "Share the screen on mobile data" in the Companion's Permissions tab.
	_, _ = client.Shell(ctx, serial, "am start -a android.intent.action.VIEW -d androfuse://pair "+pkg)
	time.Sleep(2 * time.Second)
	turnedOn := false
	for i := 0; i < 6 && !turnedOn; i++ {
		tapText(ctx, client, serial, "Permissions", "权限")
		time.Sleep(1500 * time.Millisecond)
		turnedOn = tapSwitch(ctx, client, serial, "Share the screen on mobile data", "移动网络下共享屏幕")
		if !turnedOn {
			_, _ = client.Shell(ctx, serial, "input swipe 500 1600 500 700 300")
		}
	}
	if !turnedOn {
		t.Fatal("could not turn on the screen-sharing permission")
	}
	port, err := client.ForwardPort(ctx, serial, 0, "localabstract:"+companion.LocalSocket)
	if err != nil {
		t.Fatal(err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	var conn net.Conn
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(time.Second) {
		s, err := companion.DialShare(ctx, addr, res.Token, res.CertFingerprint)
		if err == nil {
			conn = s
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("DialShare: %v", err)
		}
		t.Logf("share not ready yet: %v", err)
	}
	var frames atomic.Int64
	var sess *scrcpy.Session
	sess, err = scrcpy.StartFromConns(lib, nil, nil, 0, 0, nil, conn, nil, false, scrcpy.LaunchConfig{
		SessionID: "live-share", InitialWidth: 720, InitialHeight: 1280,
		OnFrame: func(core.NV12Frame) { frames.Add(1) },
	})
	if err != nil {
		t.Fatalf("StartFromConns: %v", err)
	}
	defer sess.Close()
	// Keep the screen changing so the encoder sends frames.
	go func() {
		for i := 0; i < 20 && frames.Load() < 30; i++ {
			_, _ = client.Shell(ctx, serial, "input swipe 500 1500 500 600 200")
			time.Sleep(700 * time.Millisecond)
		}
	}()
	for deadline := time.Now().Add(45 * time.Second); frames.Load() < 30 && time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
	}
	st := sess.Stats()
	t.Logf("decoded %d frames, %dx%d, %.1f fps, hw=%v, decode %.2f ms", frames.Load(), st.NegotiatedWidth, st.NegotiatedHeight, st.MeasuredFPS, st.HardwareAccelerated, float64(st.AvgDecodeUs)/1000)
	if frames.Load() < 5 {
		t.Fatalf("only %d frames decoded", frames.Load())
	}
	// Home through the accessibility service (turned on here as its user would in Settings).
	_, _ = client.Shell(ctx, serial, "settings put secure enabled_accessibility_services "+pkg+"/dev.mygo.AndroFuseA11yService; settings put secure accessibility_enabled 1")
	time.Sleep(3 * time.Second)
	_, _ = client.Shell(ctx, serial, "am start -a android.intent.action.VIEW -d androfuse://pair "+pkg)
	time.Sleep(2 * time.Second)
	keys, err := companion.Dial(ctx, addr, res.Token, res.CertFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Close()
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(2 * time.Second) {
		err := keys.Key(ctx, "home")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Key(home): %v", err)
		}
	}
	time.Sleep(2 * time.Second)
	top, _ := client.Shell(ctx, serial, "dumpsys activity activities | grep -m1 topResumedActivity")
	if strings.Contains(top, pkg) {
		t.Fatalf("Home did not leave the Companion: %s", top)
	}
	t.Logf("Home through the Companion: %s", strings.TrimSpace(top))
	_, _ = client.Shell(ctx, serial, "settings delete secure enabled_accessibility_services; am startservice -a stop "+pkg+"/dev.mygo.AndroFuseShareService")
}

// tapSwitch taps the switch labeled with one of words when it is off; it reports whether the
// switch is on afterwards (or already was).
func tapSwitch(ctx context.Context, client *adb.Client, serial string, words ...string) bool {
	if _, err := client.Shell(ctx, serial, "uiautomator dump /sdcard/af-ui.xml >/dev/null 2>&1"); err != nil {
		return false
	}
	raw, _ := client.Shell(ctx, serial, "cat /sdcard/af-ui.xml")
	re := regexp.MustCompile(`<node [^>]*text="([^"]*)"[^>]*checkable="true" checked="(true|false)"[^>]*bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"`)
	for _, m := range re.FindAllStringSubmatch(raw, -1) {
		for _, w := range words {
			if m[1] != w {
				continue
			}
			if m[2] == "true" {
				return true
			}
			x1, _ := strconv.Atoi(m[3])
			y1, _ := strconv.Atoi(m[4])
			x2, _ := strconv.Atoi(m[5])
			y2, _ := strconv.Atoi(m[6])
			_, _ = client.Shell(ctx, serial, fmt.Sprintf("input tap %d %d", (x1+x2)/2, (y1+y2)/2))
			return true
		}
	}
	return false
}
