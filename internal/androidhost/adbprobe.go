package androidhost

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// adbAnswer is how this phone's own adbd answered on a local port.
type adbAnswer int

const (
	adbNone adbAnswer = iota // nothing listens, or it is not adbd
	adbTLS                   // adbd asks for TLS (STLS): the Wireless debugging port
	adbOpen                  // adbd answers without TLS (AUTH or CNXN): adb tcpip
)

// probeLocalADB asks 127.0.0.1:port, from the Companion's own app process, whether adbd listens
// there, by sending the adb CNXN handshake and reading the reply header (PLAN §9.1-2).
// Notes: it never authenticates or pairs; it only tells a Wireless debugging port (STLS) apart
// from any other service, so the cross-network beacon never advertises a wrong port.
func probeLocalADB(port int) (adbAnswer, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		return adbNone, err
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
		return adbNone, err
	}
	reply := make([]byte, 24)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return adbNone, err
	}
	switch cmd := reply[:4]; {
	case bytes.Equal(cmd, []byte("STLS")):
		return adbTLS, nil
	case bytes.Equal(cmd, []byte("AUTH")), bytes.Equal(cmd, []byte("CNXN")):
		return adbOpen, nil
	default:
		return adbNone, fmt.Errorf("port %d answered %q, not adb", port, cmd)
	}
}

// SaveWirelessADBPort checks port with probeLocalADB before the beacon advertises it: a
// Wireless debugging port is saved; plain adb tcpip is refused (`INV-07`); a port nothing answers
// on from the app is saved with a warning, since the system may only start adbd later.
// Side effects: runs off the UI thread; the outcome shows on the Security tab.
func (h *Host) SaveWirelessADBPort(port int) {
	go func() {
		ans, err := probeLocalADB(port)
		msg := L.BeaconVerified()
		switch {
		case ans == adbOpen:
			msg = L.BeaconPlain()
		case err != nil:
			msg = L.BeaconUnverified()
		}
		if ans != adbOpen {
			h.SetWirelessADBPort(port)
		}
		h.mu.Lock()
		h.beaconCheck = msg
		h.appendAuditLocked(fmt.Sprintf("wireless debugging port %d checked: %s", port, [...]string{"no adb", "Wireless debugging (TLS)", "plain adb tcpip, refused"}[ans]))
		h.mu.Unlock()
		h.notify()
	}()
}
