package gui

import (
	"strings"
	"testing"

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
