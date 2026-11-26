//go:build windows

package scrcpy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/lease"
)

// TestLiveSoak keeps one mirroring session decoding for ANDROFUSE_LIVE_SOAK (for example 30m)
// while the device's screen keeps changing, and logs measured fps, decode time, this process's
// CPU and memory every minute (PLAN §10 acceptance gate 5). Frames are decoded but not drawn.
//
// Run with ANDROFUSE_LIVE_SOAK=30m and one online adb device (ANDROFUSE_LIVE_SERIAL picks one).
func TestLiveSoak(t *testing.T) {
	dur, err := time.ParseDuration(os.Getenv("ANDROFUSE_LIVE_SOAK"))
	if err != nil || dur <= 0 {
		t.Skip("set ANDROFUSE_LIVE_SOAK=30m with an online device to run the soak test")
	}
	libPath, _ := filepath.Abs(filepath.Join("..", "..", "core", "target", "debug", core.LibraryName()))
	lib, err := core.Open(libPath)
	if err != nil {
		t.Fatalf("core.Open: %v", err)
	}
	defer lib.Stop()
	if _, err := lib.Start(core.Config{Version: "0.1.0", StateDir: t.TempDir(), Profile: "soak"}); err != nil {
		t.Fatal(err)
	}
	client, err := adb.FindClient()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), dur+5*time.Minute)
	defer cancel()
	serial := os.Getenv("ANDROFUSE_LIVE_SERIAL")
	if serial == "" {
		devs, err := client.Devices(ctx)
		if err != nil || len(devs) == 0 {
			t.Fatalf("no adb device: %v", err)
		}
		serial = devs[0].Serial
	}
	store, err := lease.Open(filepath.Join(t.TempDir(), "leases.json"))
	if err != nil {
		t.Fatal(err)
	}

	s, err := StartADB(ctx, client, lib, store, LaunchConfig{
		SessionID: "soak", Serial: serial, DisplayID: 0, MaxSize: 1280, MaxFPS: 60,
		VideoBitRate: 8_000_000, EnableControl: true, InitialWidth: 1080, InitialHeight: 1920,
		OnFrame: func(core.NV12Frame) {},
	})
	if err != nil {
		t.Fatalf("StartADB: %v", err)
	}
	defer s.Close()

	// Keep the screen awake and changing: scroll the main Settings list up and down (`-S` restarts Settings so a
	// leftover sub-page cannot leave nothing to scroll). scrcpy sends no
	// frames for a still or sleeping screen, so the per-minute frame count is the real rate.
	_, _ = client.Shell(ctx, serial, "svc power stayon true; input keyevent KEYCODE_WAKEUP; wm dismiss-keyguard; am start -S -a android.settings.SETTINGS")
	defer func() { _, _ = client.Shell(context.Background(), serial, "svc power stayon false") }()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(700 * time.Millisecond):
			}
			from, to := "1600", "500"
			if i%6 >= 3 {
				from, to = "500", "1600"
			}
			_, _ = client.Shell(ctx, serial, "input swipe 540 "+from+" 540 "+to+" 400")
		}
	}()

	start := time.Now()
	cpu0 := processCPU()
	var minFPS, maxFPS float64 = 1e9, 0 // frames per second over each minute
	var maxRSS uint64
	var lastFrames int
	for time.Since(start) < dur {
		time.Sleep(time.Minute)
		st := s.Stats()
		rss := workingSetMB()
		maxRSS = max(maxRSS, rss)
		fps := float64(st.FramesDecoded-lastFrames) / 60
		minFPS, maxFPS = min(minFPS, fps), max(maxFPS, fps)
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		t.Logf("t=%3.0fmin frames=%d (+%d) fps=%.1f (now %.1f) decode=%.2fms hw=%v cpu=%.1f%% rss=%dMB go-heap=%dMB",
			time.Since(start).Minutes(), st.FramesDecoded, st.FramesDecoded-lastFrames, fps, st.MeasuredFPS,
			float64(st.AvgDecodeUs)/1000, st.HardwareAccelerated,
			100*(processCPU()-cpu0).Seconds()/time.Since(start).Seconds()/float64(runtime.NumCPU()), rss, ms.HeapAlloc>>20)
		if st.FramesDecoded == lastFrames {
			t.Fatalf("no frames decoded in the last minute: %+v", st)
		}
		lastFrames = st.FramesDecoded
	}
	t.Logf("soak %s done: frames=%d fps per minute min/max=%.1f/%.1f peak rss=%dMB", dur, lastFrames, minFPS, maxFPS, maxRSS)
}

// processCPU returns the CPU time this process has used, user and kernel.
func processCPU() time.Duration {
	var c, e, k, u syscall.Filetime
	h, err := syscall.GetCurrentProcess()
	if err != nil || syscall.GetProcessTimes(h, &c, &e, &k, &u) != nil {
		return 0
	}
	ticks := func(f syscall.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return time.Duration((ticks(k) + ticks(u)) * 100)
}

// workingSetMB returns this process's working set from tasklist, in MB.
func workingSetMB() uint64 {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(os.Getpid()), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return 0
	}
	f := strings.Split(strings.TrimSpace(string(out)), "\",\"")
	if len(f) < 5 {
		return 0
	}
	kb := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, f[4])
	n, _ := strconv.ParseUint(kb, 10, 64)
	return n / 1024
}
