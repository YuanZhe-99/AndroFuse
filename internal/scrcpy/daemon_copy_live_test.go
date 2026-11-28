package scrcpy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/lease"
)

// TestLiveADBSessionKeepsDaemonServer checks that a direct ADB mirror session, which runs
// scrcpy-server with `cleanup=true` (the server deletes its own jar on exit), leaves the
// Enhanced mode daemon's pinned copy at RemoteServerPath in place. Before the two copies were
// split, every ADB session deleted the daemon's jar and all later daemon sessions crashed with
// ClassNotFoundException.
//
// Run with ANDROFUSE_LIVE_SERIAL=<adb serial> (an emulator is enough).
func TestLiveADBSessionKeepsDaemonServer(t *testing.T) {
	serial := os.Getenv("ANDROFUSE_LIVE_SERIAL")
	if serial == "" {
		t.Skip("set ANDROFUSE_LIVE_SERIAL=<adb serial> to run the live daemon-copy test")
	}
	libPath, _ := filepath.Abs(filepath.Join("..", "..", "core", "target", "debug", core.LibraryName()))
	lib, err := core.Open(libPath)
	if err != nil {
		t.Fatalf("core.Open: %v", err)
	}
	defer lib.Stop()
	if _, err := lib.Start(core.Config{Version: "0.0.0", StateDir: t.TempDir(), Profile: "daemon-copy"}); err != nil {
		t.Fatal(err)
	}
	client, err := adb.FindClient()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := FindCachedServer()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// The daemon's copy, as dex.StartDaemon leaves it.
	if err := client.PushFile(ctx, serial, srv, RemoteServerPath); err != nil {
		t.Fatal(err)
	}
	sum := func(p string) string {
		out, _ := client.Shell(ctx, serial, "sha256sum "+p+" 2>/dev/null")
		f := strings.Fields(out)
		if len(f) == 0 {
			return ""
		}
		return f[0]
	}
	if got := sum(RemoteServerPath); got != ExpectedServerSHA256 {
		t.Fatalf("daemon copy before = %q", got)
	}
	store, err := lease.Open(filepath.Join(t.TempDir(), "leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := StartADB(ctx, client, lib, store, LaunchConfig{
		SessionID: "daemon-copy", Serial: serial, MaxSize: 720, MaxFPS: 30,
		VideoBitRate: 2_000_000, InitialWidth: 720, InitialHeight: 1280,
		OnFrame: func(core.NV12Frame) {},
	})
	if err != nil {
		t.Fatalf("StartADB: %v", err)
	}
	_, _ = client.Shell(ctx, serial, "input keyevent KEYCODE_WAKEUP; am start -a android.settings.SETTINGS")
	time.Sleep(3 * time.Second)
	frames := s.Stats().FramesDecoded
	s.Close()
	// scrcpy's cleanup process deletes the session copy once the server exits.
	deadline := time.Now().Add(10 * time.Second)
	for sum(RemoteSessionServerPath) != "" && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	t.Logf("session decoded %d frames; session copy left: %q", frames, sum(RemoteSessionServerPath))
	if frames == 0 {
		t.Fatal("the ADB session decoded no frames")
	}
	if got := sum(RemoteServerPath); got != ExpectedServerSHA256 {
		t.Fatalf("an ADB session removed or changed the daemon's scrcpy-server copy: sha256 = %q", got)
	}
}
