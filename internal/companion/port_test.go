package companion

import (
	"fmt"
	"net"
	"slices"
	"testing"
)

// TestPickPort checks the port is a random high port below the dynamic range, that a free saved
// port is kept, and that a busy one is replaced.
func TestPickPort(t *testing.T) {
	p, err := PickPort(0)
	if err != nil || p < PortMin || p > PortMax {
		t.Fatalf("PickPort(0) = %d, %v; want a port in [%d, %d]", p, err, PortMin, PortMax)
	}
	if got, err := PickPort(p); err != nil || got != p {
		t.Fatalf("a free saved port must be kept: got %d, %v, want %d", got, err, p)
	}
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got, err := PickPort(p); err != nil || got == p {
		t.Fatalf("a busy saved port must be replaced: got %d, %v", got, err)
	}
}

// TestDevicePortsAreStableAndInRange checks that a device id always gives the same distinct
// ports in range, so the desktop finds the phone's port from its id, and that ids differ.
func TestDevicePortsAreStableAndInRange(t *testing.T) {
	a, b := DevicePorts("0a1b2c3d"), DevicePorts("0a1b2c3d")
	if !slices.Equal(a, b) || len(a) != devicePortCount {
		t.Fatalf("DevicePorts not stable: %v %v", a, b)
	}
	seen := map[int]bool{}
	for _, p := range a {
		if p < PortMin || p > PortMax || seen[p] {
			t.Fatalf("bad port %d in %v", p, a)
		}
		seen[p] = true
	}
	if slices.Equal(a, DevicePorts("ffee0011")) {
		t.Fatal("two ids gave the same ports")
	}
	p, err := PickDevicePort("0a1b2c3d")
	if err != nil || !slices.Contains(a, p) {
		t.Fatalf("PickDevicePort = %d, %v; want one of %v", p, err, a)
	}
}

// TestKeptIdentitySurvivesRestart checks that a Companion started again with its kept identity
// keeps the fingerprint a desktop pinned at pairing, and that one without draws a new one.
func TestKeptIdentitySurvivesRestart(t *testing.T) {
	cert, fp, err := GenerateSelfSignedCert("AndroFuse Companion")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalIdentity(cert)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := ParseIdentity(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewServer("", "m", "16", 36, Handler{Identity: &kept})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewServer("", "m", "16", 36, Handler{Identity: &kept})
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint() != fp || b.Fingerprint() != fp {
		t.Fatalf("fingerprints %s, %s; pinned %s", a.Fingerprint(), b.Fingerprint(), fp)
	}
	c, err := NewServer("", "m", "16", 36, Handler{})
	if err != nil || c.Fingerprint() == fp {
		t.Fatalf("a server without an identity reused it: %v", err)
	}
}
