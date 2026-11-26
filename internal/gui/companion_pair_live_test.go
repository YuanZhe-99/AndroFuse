package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
)

// TestLiveCompanionPairAndMeshJoin drives first pairing end to end against a phone or emulator:
// install the Companion APK over adb, pair after the phone's user taps Allow, hand over the
// desktop's EasyTier network, and wait until the desktop's embedded mesh sees the phone.
//
// Run with ANDROFUSE_LIVE_COMPANION=<adb serial>; ANDROFUSE_LIVE_MESH_PEER is the address the
// phone dials to reach this desktop (default: the emulator's host loopback 10.0.2.2 on the random mesh port).
func TestLiveCompanionPairAndMeshJoin(t *testing.T) {
	serial := os.Getenv("ANDROFUSE_LIVE_COMPANION")
	if serial == "" {
		t.Skip("set ANDROFUSE_LIVE_COMPANION=<adb serial> to run the live pairing + mesh test")
	}
	peer := os.Getenv("ANDROFUSE_LIVE_MESH_PEER")
	apk, err := filepath.Abs(filepath.Join("..", "..", "build", fmt.Sprintf("AndroFuse-%s-android-arm64.apk", version.Version)))
	if err != nil {
		t.Fatal(err)
	}
	libPath, _ := filepath.Abs(filepath.Join("..", "..", "core", "target", "debug", core.LibraryName()))
	lib, err := core.Open(libPath)
	if err != nil {
		t.Fatalf("core.Open: %v", err)
	}
	defer lib.Stop()
	if _, err := lib.Start(core.Config{Version: version.Version, StateDir: t.TempDir(), Profile: "live-pair"}); err != nil {
		t.Fatal(err)
	}

	port, err := companion.PickPort(0)
	if err != nil {
		t.Fatal(err)
	}
	desk := mesh.NewManager(lib)
	network := fmt.Sprintf("androfuse-e2e-%d", time.Now().Unix())
	cfg := mesh.Config{Enabled: true, NetworkName: network, NetworkSecret: "e2e-secret", DeviceName: mesh.Hostname(mesh.KindPC, "e2e00001", "e2e"), Listeners: []string{fmt.Sprintf("tcp://127.0.0.1:%d", port)}}
	if peer == "" {
		peer = fmt.Sprintf("tcp://10.0.2.2:%d", port)
	}
	if err := desk.Start(cfg); err != nil {
		t.Fatalf("desktop mesh: %v", err)
	}
	if st := desk.Status(""); st.LastError != "" {
		t.Fatalf("desktop mesh reports: %s", st.LastError)
	}
	defer desk.Stop("")

	client, err := adb.FindClient()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c, addr, res, err := pairOverADB(ctx, client, serial, apk, func(s string) { t.Log(s) }, nil)
	if err != nil {
		t.Fatalf("pairOverADB: %v", err)
	}
	defer c.Close()
	t.Logf("paired over %s, fingerprint %s…", addr, res.CertFingerprint[:11])

	jr, err := c.JoinMesh(ctx, companion.MeshJoinRequest{NetworkName: network, NetworkSecret: cfg.NetworkSecret, Peers: []string{peer}})
	if err != nil || !jr.Running {
		t.Fatalf("JoinMesh = %+v, %v", jr, err)
	}

	for deadline := time.Now().Add(90 * time.Second); ; time.Sleep(2 * time.Second) {
		var st struct {
			Info json.RawMessage `json:"info"`
		}
		if err := lib.Call("mesh.status", nil, &st); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(st.Info), "Android") {
			t.Logf("desktop mesh sees the phone (%d bytes of running info)", len(st.Info))
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("phone never appeared in the desktop mesh; running info: %.600s", st.Info)
		}
	}
}
