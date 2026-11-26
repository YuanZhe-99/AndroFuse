package companion

import (
	"fmt"
	"net"
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
