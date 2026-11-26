package androidhost

import (
	"io"
	"net"
	"testing"
)

// TestProbeLocalADB checks that a Wireless debugging adbd (STLS), plain adb (AUTH) and another
// service are told apart from the CNXN reply.
func TestProbeLocalADB(t *testing.T) {
	serve := func(reply string) int {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			_, _ = io.ReadFull(c, make([]byte, 24+7))
			hdr := make([]byte, 24)
			copy(hdr, reply)
			_, _ = c.Write(hdr)
		}()
		return ln.Addr().(*net.TCPAddr).Port
	}
	if a, err := probeLocalADB(serve("STLS")); a != adbTLS || err != nil {
		t.Fatalf("STLS: %v %v", a, err)
	}
	if a, err := probeLocalADB(serve("AUTH")); a != adbOpen || err != nil {
		t.Fatalf("AUTH: %v %v", a, err)
	}
	if a, err := probeLocalADB(serve("HTTP")); a != adbNone || err == nil {
		t.Fatalf("HTTP: %v %v", a, err)
	}
}
