package gui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
)

// RememberedDevice is a phone AndroFuse has connected to, kept in `<stateDir>/devices.json` keyed
// by its HardwareHash, so the Devices page can list it while offline, reconnect it and forget it.
type RememberedDevice struct {
	HardwareHash string `json:"hardware_hash"`
	Name         string `json:"name"`
	// Endpoint is the last TLS Wireless Debugging `host:port` it was reached on.
	Endpoint      string            `json:"endpoint,omitempty"`
	AutoReconnect bool              `json:"auto_reconnect"`
	Companion     *CompanionBinding `json:"companion,omitempty"`
	LastSeen      time.Time         `json:"last_seen"`
}

// CompanionBinding is how the desktop reaches a paired phone's Companion again without adb: its
// random TCP port, pinned certificate and DPAPI-sealed token (`INV-08`), and the EasyTier host
// name its mesh route carries.
type CompanionBinding struct {
	Port        int    `json:"port"`
	Fingerprint string `json:"fingerprint"`
	SealedToken []byte `json:"sealed_token"`
	MeshHost    string `json:"mesh_host,omitempty"`
}

// devicesFile is the device memory's path, or "" without a state directory.
func (a *App) devicesFile() string {
	if a.stateDir == "" {
		return ""
	}
	return filepath.Join(a.stateDir, "devices.json")
}

// loadDevices reads devices.json into a.remembered; a missing or damaged file leaves it empty.
func (a *App) loadDevices() {
	a.remembered = nil
	p := a.devicesFile()
	if p == "" {
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var list []RememberedDevice
	if json.Unmarshal(raw, &list) == nil {
		a.remembered = list
	}
}

// saveDevicesLocked writes a.remembered atomically; the caller holds a.mu.
func (a *App) saveDevicesLocked() {
	p := a.devicesFile()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	raw, err := json.MarshalIndent(a.remembered, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(p+".tmp", raw, 0o600); err == nil {
		_ = os.Rename(p+".tmp", p)
	}
}

// rememberedIndexLocked returns the index of hash in a.remembered, or -1.
func (a *App) rememberedIndexLocked(hash string) int {
	return slices.IndexFunc(a.remembered, func(r RememberedDevice) bool { return r.HardwareHash == hash })
}

// rememberDevices records the online devices of a scan: new phones are added with automatic
// reconnect on, known ones get their name, last TLS endpoint and last-seen time refreshed.
// Side effects: saves devices.json when anything but the last-seen time changed, or hourly.
func (a *App) rememberDevices(devs []DiscoveredDevice, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	changed := false
	for _, d := range devs {
		if d.HardwareHash == "" || !d.Probed || d.Target.State != "device" || d.Target.Transport == adb.TransportEmulator {
			continue
		}
		name := strings.TrimSpace(d.Profile.Model)
		if name == "" {
			name = d.Target.Model
		}
		endpoint := ""
		for _, tr := range d.Transports {
			if tr.Transport == adb.TransportTLSWiFi && strings.Contains(tr.Serial, ":") {
				endpoint = tr.Serial
			}
		}
		i := a.rememberedIndexLocked(d.HardwareHash)
		if i < 0 {
			a.remembered = append(a.remembered, RememberedDevice{HardwareHash: d.HardwareHash, Name: name, Endpoint: endpoint, AutoReconnect: true, LastSeen: now})
			changed = true
			continue
		}
		r := &a.remembered[i]
		if name != "" && r.Name != name {
			r.Name, changed = name, true
		}
		if endpoint != "" && r.Endpoint != endpoint {
			r.Endpoint, changed = endpoint, true
		}
		if now.Sub(r.LastSeen) > time.Hour {
			changed = true
		}
		r.LastSeen = now
	}
	if changed {
		a.saveDevicesLocked()
	}
}

// offlineRemembered returns the remembered phones not among devs, most recently seen first.
func (a *App) offlineRemembered(devs []DiscoveredDevice) []RememberedDevice {
	online := make(map[string]bool, len(devs))
	for _, d := range devs {
		online[d.HardwareHash] = true
	}
	a.mu.Lock()
	var out []RememberedDevice
	for _, r := range a.remembered {
		if !online[r.HardwareHash] {
			out = append(out, r)
		}
	}
	a.mu.Unlock()
	slices.SortFunc(out, func(x, y RememberedDevice) int { return y.LastSeen.Compare(x.LastSeen) })
	return out
}

// RememberedFor returns the remembered entry for hash.
func (a *App) RememberedFor(hash string) (RememberedDevice, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i := a.rememberedIndexLocked(hash); i >= 0 {
		return a.remembered[i], true
	}
	return RememberedDevice{}, false
}

// SetAutoReconnect turns a remembered phone's automatic reconnect on or off.
// Side effects: saves devices.json.
func (a *App) SetAutoReconnect(hash string, on bool) {
	a.update(func() {
		if i := a.rememberedIndexLocked(hash); i >= 0 {
			a.remembered[i].AutoReconnect = on
			a.saveDevicesLocked()
		}
	})
}

// ForgetDevice removes a remembered phone and returns it, so an Undo can put it back with
// RestoreDevice.
// Side effects: saves devices.json; drops the phone's TLS Wi-Fi ADB connection when one is open.
func (a *App) ForgetDevice(hash string) (RememberedDevice, bool) {
	var gone RememberedDevice
	found := false
	var wireless []string
	a.update(func() {
		i := a.rememberedIndexLocked(hash)
		if i < 0 {
			return
		}
		gone, found = a.remembered[i], true
		a.remembered = slices.Delete(a.remembered, i, i+1)
		a.saveDevicesLocked()
		for _, d := range a.devices {
			if d.HardwareHash != hash {
				continue
			}
			for _, tr := range d.Transports {
				if tr.Transport == adb.TransportTLSWiFi {
					wireless = append(wireless, tr.Serial)
				}
			}
		}
	})
	for _, s := range wireless {
		a.DisconnectWireless(s)
	}
	return gone, found
}

// RestoreDevice puts back a device ForgetDevice removed.
func (a *App) RestoreDevice(r RememberedDevice) {
	a.update(func() {
		if a.rememberedIndexLocked(r.HardwareHash) < 0 {
			a.remembered = append(a.remembered, r)
			a.saveDevicesLocked()
		}
	})
}

// bindCompanion saves how to reach hash's Companion again after a pairing.
// Side effects: seals token with DPAPI and saves devices.json.
func (a *App) bindCompanion(hash string, port int, fingerprint, token, meshHost string) {
	sealed, err := sealSecret([]byte(token))
	if err != nil {
		return
	}
	a.update(func() {
		if i := a.rememberedIndexLocked(hash); i >= 0 {
			a.remembered[i].Companion = &CompanionBinding{Port: port, Fingerprint: fingerprint, SealedToken: sealed, MeshHost: meshHost}
			a.saveDevicesLocked()
		}
	})
}

// reconnectRemembered asks adb to reconnect remembered phones with automatic reconnect on that
// are not online: through this phone's own mDNS Wireless Debugging instance first, then its last
// endpoint (`INV-07`: TLS only).
// Returns: true when one of them connected, so the caller scans again.
func (a *App) reconnectRemembered(ctx context.Context, client *adb.Client, online map[string]bool, svcs []adb.MDNSService) bool {
	a.mu.Lock()
	var want []RememberedDevice
	for _, r := range a.remembered {
		if r.AutoReconnect && !online[r.HardwareHash] {
			want = append(want, r)
		}
	}
	a.mu.Unlock()
	connected := false
	for _, r := range want {
		done := false
		for _, s := range svcs {
			if s.CanConnect && s.HardwareHash == r.HardwareHash {
				if _, err := client.ConnectTLS(ctx, s.Addr, false); err == nil {
					done = true
					break
				}
			}
		}
		if !done && r.Endpoint != "" {
			_, err := client.ConnectTLS(ctx, r.Endpoint, false)
			done = err == nil
		}
		connected = connected || done
	}
	return connected
}

// onlineHashes returns the HardwareHashes of the online devices among devs, from their own hash
// or, for a serial the last scan already matched (a TLS transport), from that scan.
func (a *App) onlineHashes(devs []adb.Device) map[string]bool {
	a.mu.Lock()
	known := make(map[string]string)
	for _, d := range a.devices {
		for _, tr := range d.Transports {
			known[tr.Serial] = d.HardwareHash
		}
	}
	a.mu.Unlock()
	out := make(map[string]bool, len(devs))
	for _, d := range devs {
		if d.State != "device" {
			continue
		}
		out[d.DeviceHash] = true
		if h := known[d.Serial]; h != "" {
			out[h] = true
		}
	}
	return out
}
