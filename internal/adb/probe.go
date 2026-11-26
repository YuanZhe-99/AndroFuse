package adb

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Answer is how an address answered the adb handshake.
type Answer int

const (
	AnswerNone Answer = iota // nothing listens there, or it is not adbd
	AnswerTLS                // adbd asks for TLS (STLS): a Wireless debugging port
	AnswerOpen               // adbd answers without TLS (AUTH or CNXN): plain adb tcpip
)

// ProbeADB asks addr whether adbd listens there by sending the adb CNXN handshake and reading the
// reply header.
// Notes: it never authenticates or pairs; it only tells a Wireless debugging port (STLS) apart from
// plain adb tcpip and from any other service (`INV-07`).
func ProbeADB(ctx context.Context, addr string) (Answer, error) {
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return AnswerNone, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("host::\x00")
	var sum uint32
	for _, b := range payload {
		sum += uint32(b)
	}
	const cnxn = 0x4e584e43
	hdr := make([]byte, 24)
	for i, v := range []uint32{cnxn, 0x01000001, 1 << 20, uint32(len(payload)), sum, cnxn ^ 0xffffffff} {
		binary.LittleEndian.PutUint32(hdr[i*4:], v)
	}
	if _, err := conn.Write(append(hdr, payload...)); err != nil {
		return AnswerNone, err
	}
	reply := make([]byte, 24)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return AnswerNone, err
	}
	switch cmd := reply[:4]; {
	case bytes.Equal(cmd, []byte("STLS")):
		return AnswerTLS, nil
	case bytes.Equal(cmd, []byte("AUTH")), bytes.Equal(cmd, []byte("CNXN")):
		return AnswerOpen, nil
	default:
		return AnswerNone, fmt.Errorf("%s answered %q, not adb", MaskEndpoint(addr), cmd)
	}
}

// Wireless debugging listens on a port the kernel picks from its ephemeral range.
const (
	WirelessPortMin = 30000
	WirelessPortMax = 60999
)

// FindWirelessPort looks for the Wireless debugging port on host: it opens TCP connections to every
// port from WirelessPortMin to WirelessPortMax, many at a time, and asks each port that accepts
// with ProbeADB.
// Inputs: progress, when set, receives the share of ports tried so far (0–1).
// Returns: the first port answering STLS, or 0 when none does before ctx ends.
// Notes: a plain adb tcpip port is never returned (`INV-07`). Only for addresses this computer can
// route to directly (a LAN or the user's own VPN); a mesh address needs a forward per port.
func FindWirelessPort(ctx context.Context, host string, progress func(float64)) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ports := make(chan int)
	var found atomic.Int64
	var tried atomic.Int64
	total := float64(WirelessPortMax - WirelessPortMin + 1)
	var wg sync.WaitGroup
	// Windows retries a refused connect for about two seconds, so a short timeout and many dials at
	// once keep the search near twenty seconds; a phone on the LAN answers well within it.
	for range 768 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := net.Dialer{Timeout: 450 * time.Millisecond}
			for p := range ports {
				addr := net.JoinHostPort(host, strconv.Itoa(p))
				if conn, err := d.DialContext(ctx, "tcp", addr); err == nil {
					conn.Close()
					if ans, _ := ProbeADB(ctx, addr); ans == AnswerTLS && found.CompareAndSwap(0, int64(p)) {
						cancel()
					}
				}
				tried.Add(1)
			}
		}()
	}
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if progress != nil {
					progress(float64(tried.Load()) / total)
				}
			}
		}
	}()
feed:
	for p := WirelessPortMin; p <= WirelessPortMax; p++ {
		select {
		case ports <- p:
		case <-ctx.Done():
			break feed
		}
	}
	close(ports)
	wg.Wait()
	return int(found.Load())
}
