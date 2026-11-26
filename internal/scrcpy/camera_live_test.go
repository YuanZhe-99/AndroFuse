//go:build windows

package scrcpy

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/lease"
)

// TestLiveCameraSwitch opens the rear camera, closes it the way a pane does, and opens the front
// one straight after, as Switch camera does; both must deliver frames.
//
// Run with ANDROFUSE_LIVE_CAMERA=1 and one online adb device (ANDROFUSE_LIVE_SERIAL picks one).
func TestLiveCameraSwitch(t *testing.T) {
	if os.Getenv("ANDROFUSE_LIVE_CAMERA") == "" {
		t.Skip("set ANDROFUSE_LIVE_CAMERA=1 with an online phone to run")
	}
	libPath, _ := filepath.Abs(filepath.Join("..", "..", "core", "target", "debug", core.LibraryName()))
	lib, err := core.Open(libPath)
	if err != nil {
		t.Fatalf("core.Open: %v", err)
	}
	defer lib.Stop()
	if _, err := lib.Start(core.Config{Version: "0.1.0", StateDir: t.TempDir(), Profile: "camera"}); err != nil {
		t.Fatal(err)
	}
	client, err := adb.FindClient()
	if err != nil {
		t.Fatal(err)
	}
	serial := os.Getenv("ANDROFUSE_LIVE_SERIAL")
	if serial == "" {
		devs, err := client.Devices(context.Background())
		if err != nil || len(devs) == 0 {
			t.Fatalf("no adb device: %v", err)
		}
		serial = devs[0].Serial
	}
	store, err := lease.Open(filepath.Join(t.TempDir(), "leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, facing := range []string{"back", "front", "back"} {
		var frames atomic.Int64
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		s, err := StartADB(ctx, client, lib, store, LaunchConfig{
			SessionID: "cam-" + facing, Serial: serial, VideoSource: "camera", CameraFacing: facing,
			CameraSize: "1280x720", CameraFPS: 30, VideoBitRate: 6_000_000, Audio: true, AudioSource: "mic",
			InitialWidth: 1280, InitialHeight: 720,
			OnFrame:    func(core.NV12Frame) { frames.Add(1) },
			OnAudioPCM: func([]byte) {},
		})
		cancel()
		if err != nil {
			t.Fatalf("%s camera: StartADB after %v: %v", facing, time.Since(start), err)
		}
		deadline := time.Now().Add(8 * time.Second)
		for frames.Load() < 10 && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		t.Logf("%s camera: %d frames, started in %v", facing, frames.Load(), time.Since(start))
		closeStart := time.Now()
		s.Close()
		t.Logf("%s camera: Close took %v", facing, time.Since(closeStart))
		if frames.Load() < 10 {
			t.Fatalf("%s camera delivered %d frames", facing, frames.Load())
		}
	}
}
