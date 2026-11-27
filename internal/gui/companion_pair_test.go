package gui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
)

// TestPeersForPhone checks that relays pass through and a wildcard listener becomes one dialable
// address per LAN IPv4 of this computer, never the wildcard itself.
func TestPeersForPhone(t *testing.T) {
	cfg := mesh.Config{
		Peers:     []string{"tcp://relay.example.org:11010"},
		Listeners: []string{"tcp://0.0.0.0:11010", "udp://192.0.2.7:11011"},
	}
	got := peersForPhone(cfg)
	if got[0] != "tcp://relay.example.org:11010" {
		t.Fatalf("relay must come first, got %v", got)
	}
	var lan int
	for _, p := range got {
		if strings.Contains(p, "0.0.0.0") {
			t.Fatalf("wildcard listener leaked to the phone: %v", got)
		}
		if strings.HasPrefix(p, "tcp://") && strings.HasSuffix(p, ":11010") && p != cfg.Peers[0] {
			lan++
		}
	}
	if lan != len(lanIPv4()) {
		t.Fatalf("want one address per LAN IPv4 (%d), got %d in %v", len(lanIPv4()), lan, got)
	}
	if got[len(got)-1] != "udp://192.0.2.7:11011" {
		t.Fatalf("specific listener must pass through unchanged, got %v", got)
	}
}

func TestFormatPairCode(t *testing.T) {
	if got := formatPairCode("123456"); got != "123 456" {
		t.Fatalf("formatPairCode(123456) = %q, want %q", got, "123 456")
	}
	if got := formatPairCode(" 98765 "); got != "98765" {
		t.Fatalf("formatPairCode(98765) = %q, want %q", got, "98765")
	}
}

func TestRememberMeshPhoneAndLosslessMergeWithADB(t *testing.T) {
	dir := t.TempDir()
	a := New("test", dir)
	res := companion.PairResponse{
		Token:           "tok-mesh-1", //gitleaks:allow
		CertFingerprint: "fp-mesh-abc",
		DeviceName:      "Galaxy Z Fold8",
		Manufacturer:    "samsung",
		Brand:           "samsung",
		AndroidRelease:  "17",
		MeshHost:        "AF-p-77889900 Galaxy Z Fold8",
		Port:            44123,
		Status: &companion.DeviceStatus{
			BatteryPct:      82,
			NetworkType:     "cellular",
			WirelessDebugOn: false,
		},
	}

	meshHash := a.rememberMeshPhone("77889900", "Pixel", res)
	if meshHash != "mesh:77889900" {
		t.Fatalf("rememberMeshPhone hash = %q, want mesh:77889900", meshHash)
	}
	r, ok := a.RememberedFor(meshHash)
	if !ok || r.Companion == nil || r.Maker != "samsung" || r.Android != "17" || r.Name != "Galaxy Z Fold8" {
		t.Fatalf("remembered mesh phone mismatch: %+v ok=%v", r, ok)
	}
	a.mu.Lock()
	for i := range a.remembered {
		if a.remembered[i].HardwareHash == meshHash {
			a.remembered[i].Alias = "My Fold"
			a.remembered[i].AutoReconnect = false
		}
	}
	// Simulate an unrelated remembered device whose marketing name is identical.
	a.remembered = append(a.remembered, RememberedDevice{
		HardwareHash: "other-real-hash",
		Name:         "Galaxy Z Fold8",
		Maker:        "samsung",
		Companion:    &CompanionBinding{Fingerprint: "fp-other", MeshHost: "AF-p-deadbeef Other"},
	})
	merged := a.mergeMeshRememberedLocked("realhw-serial-01", "fp-mesh-abc", "AF-p-77889900 Galaxy Z Fold8")
	a.mu.Unlock()
	if !merged {
		t.Fatal("expected mergeMeshRememberedLocked to merge mesh:77889900 into realhw-serial-01")
	}
	if _, stillMesh := a.RememberedFor(meshHash); stillMesh {
		t.Fatalf("provisional %s entry should be removed after merge", meshHash)
	}
	real, ok := a.RememberedFor("realhw-serial-01")
	if !ok || real.Alias != "My Fold" || real.AutoReconnect || real.Companion == nil || real.Companion.Fingerprint != "fp-mesh-abc" {
		t.Fatalf("merged device mismatch: %+v ok=%v", real, ok)
	}
	if _, otherStillPresent := a.RememberedFor("other-real-hash"); !otherStillPresent {
		t.Fatal("unrelated phone must stay intact after merge")
	}
}

func TestMeshPairEndToEndLoopback(t *testing.T) {
	srv, err := companion.NewServer("", "SM-F971U1", "17", 37, companion.Handler{
		MeshHost:    func() string { return "AF-p-aabbccdd Fold8" },
		GetIdentity: func() (string, string, string) { return "Galaxy Z Fold8", "samsung", "samsung" },
		GetStatus: func() companion.DeviceStatus {
			return companion.DeviceStatus{
				BatteryPct:          91,
				NetworkType:         "wifi",
				WirelessADBPort:     41235,
				WirelessADBPairPort: 42345,
				USBDebugOn:          true,
				WirelessDebugOn:     true,
			}
		},
		OnPairRequest: func(req companion.PairRequest) bool {
			return req.Code == "654321"
		},
		IssueToken: func(req companion.PairRequest) (string, error) {
			return "issued-" + req.Code, nil
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Listen("127.0.0.1:0"); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer func() { _ = srv.Close() }()

	res, err := companion.RequestPairing(ctx, srv.Addr(), "AndroFuse · TESTPC", "654321")
	if err != nil {
		t.Fatalf("RequestPairing: %v", err)
	}
	if res.Token != "issued-654321" || res.Manufacturer != "samsung" || res.Status == nil || res.Status.WirelessADBPairPort != 42345 {
		t.Fatalf("unexpected PairResponse: %+v", res)
	}

	a := New("test", t.TempDir())
	hash := a.rememberMeshPhone("aabbccdd", "Fallback", res)
	if hash != "mesh:aabbccdd" {
		t.Fatalf("hash = %q, want mesh:aabbccdd", hash)
	}
	if st, ok := a.peerStatusFor(hash); !ok || st.WirelessADBPairPort != 42345 {
		t.Fatalf("expected seeded peerStatus WirelessADBPairPort=42345, got %+v ok=%v", st, ok)
	}
}
