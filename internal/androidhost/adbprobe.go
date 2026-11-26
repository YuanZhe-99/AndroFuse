package androidhost

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// probeLocalADB asks 127.0.0.1:port, from the Companion's own app process, whether adbd listens
// there (adb.ProbeADB), so the cross-network beacon never advertises a wrong port.
func probeLocalADB(port int) (adb.Answer, error) {
	return adb.ProbeADB(context.Background(), net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
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
		case ans == adb.AnswerOpen:
			msg = L.BeaconPlain()
		case err != nil:
			msg = L.BeaconUnverified()
		}
		if ans != adb.AnswerOpen {
			h.SetWirelessADBPort(port)
		}
		h.mu.Lock()
		h.beaconCheck = msg
		h.appendAuditLocked(fmt.Sprintf("wireless debugging port %d checked: %s", port, [...]string{"no adb", "Wireless debugging (TLS)", "plain adb tcpip, refused"}[ans]))
		h.mu.Unlock()
		h.notify()
	}()
}
