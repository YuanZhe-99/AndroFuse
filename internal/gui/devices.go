package gui

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// RememberedDevice is a phone AndroFuse has connected to, kept in `<stateDir>/devices.json` keyed
// by its HardwareHash, so the Devices page can list it while offline, reconnect it and forget it.
type RememberedDevice struct {
	HardwareHash string `json:"hardware_hash"`
	// Name is the phone's own name (see ownName); Alias, when set, is the name given on this
	// computer, never sent anywhere, and shown instead.
	Name  string `json:"name"`
	Alias string `json:"alias,omitempty"`
	// Maker, Brand and Model pick the avatar's mark; Android is the release shown under the name.
	Maker   string `json:"maker,omitempty"`
	Brand   string `json:"brand,omitempty"`
	Model   string `json:"model,omitempty"`
	Android string `json:"android,omitempty"`
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
		name := ownName(d)
		endpoint := ""
		for _, tr := range d.Transports {
			if tr.Transport == adb.TransportTLSWiFi && strings.Contains(tr.Serial, ":") && !strings.HasPrefix(tr.Serial, "127.0.0.1:") {
				endpoint = tr.Serial
			}
		}
		i := a.rememberedIndexLocked(d.HardwareHash)
		if i < 0 {
			a.remembered = append(a.remembered, RememberedDevice{HardwareHash: d.HardwareHash, AutoReconnect: true})
			i = len(a.remembered) - 1
			changed = true
			if !a.prefs.NoCompanionOffer && a.firstConnect == nil {
				a.firstConnect = &firstConnect{Hash: d.HardwareHash, Serial: d.Target.Serial, Name: name, Install: true, Join: true}
			}
		}
		r := &a.remembered[i]
		p := d.Profile
		if r.Maker != p.Manufacturer || r.Brand != p.Brand || r.Model != p.Model || r.Android != p.AndroidRelease {
			r.Maker, r.Brand, r.Model, r.Android, changed = p.Manufacturer, p.Brand, p.Model, p.AndroidRelease, true
		}
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

// noteMeshHost keeps the EasyTier hostname hash's Companion reported on connecting, so the desktop
// finds the phone in a network after it was renamed or updated.
// Side effects: saves devices.json when it changed.
func (a *App) noteMeshHost(hash, host string) {
	if host == "" {
		return
	}
	a.update(func() {
		if i := a.rememberedIndexLocked(hash); i >= 0 && a.remembered[i].Companion != nil && a.remembered[i].Companion.MeshHost != host {
			a.remembered[i].Companion.MeshHost = host
			a.saveDevicesLocked()
		}
	})
}

// reconnectRemembered asks adb to reconnect remembered phones with automatic reconnect on that
// are not online: through this phone's own mDNS Wireless Debugging instance first, then its last
// endpoint (`INV-07`: TLS only).
// Returns: true when one of them connected, so the caller scans again.
func (a *App) reconnectRemembered(ctx context.Context, client *adb.Client, online map[string]bool, svcs []adb.MDNSService) bool {
	if !a.allowsWirelessADB() {
		return false
	}
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
		// Last, dynamically discover any changed port via Companion (wifi_ipv4 + port or port scan / tunnel).
		if !done && r.Companion != nil && a.tunnelDue(r.HardwareHash) {
			_, done = a.discoverAndConnectADB(ctx, r.HardwareHash, true)
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

// SetAlias names the remembered phone hash on this computer only; an empty alias goes back to the
// phone's own name.
// Side effects: saves devices.json.
func (a *App) SetAlias(hash, alias string) {
	alias = strings.TrimSpace(alias)
	a.update(func() {
		if i := a.rememberedIndexLocked(hash); i >= 0 {
			a.remembered[i].Alias = alias
			a.saveDevicesLocked()
		}
	})
}

// isEmulator reports whether d is an Android emulator or virtual device.
func isEmulator(d DiscoveredDevice) bool {
	return d.Target.Transport == adb.TransportEmulator || d.Profile.Emulator
}

// ownName is the name a phone gives itself: the one set in its settings, else its marketing name,
// else its model; never an emulator's AVD name or a serial.
func ownName(d DiscoveredDevice) string {
	p := d.Profile
	for _, s := range []string{p.DeviceName, p.MarketName, p.Model, d.RememberedName, d.Target.Model} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return "Android"
}

// phoneName is what the interface calls an online device: the name given on this computer, else
// "Android emulator" for an emulator, else its own name.
func (a *App) phoneName(d DiscoveredDevice) string {
	if r, ok := a.RememberedFor(d.HardwareHash); ok && r.Alias != "" {
		return r.Alias
	}
	if isEmulator(d) {
		return L.EmulatorName()
	}
	return ownName(d)
}

// displayName is what the interface calls a remembered phone.
func (r RememberedDevice) displayName() string {
	if r.Alias != "" {
		return r.Alias
	}
	if r.Name != "" {
		return r.Name
	}
	return "Android"
}

// phoneDetail is the line under a phone's name: its model when the name differs from it, and its
// Android release ("Galaxy Z Fold8 · Android 17").
func phoneDetail(shown, model, release string) string {
	var parts []string
	if model != "" && !strings.EqualFold(model, shown) {
		parts = append(parts, model)
	}
	if release != "" {
		parts = append(parts, "Android "+release)
	}
	return strings.Join(parts, " · ")
}

// deviceModel is the product name of an online device for phoneDetail.
func deviceModel(d DiscoveredDevice) string {
	if isEmulator(d) {
		return ""
	}
	if d.Profile.MarketName != "" {
		return d.Profile.MarketName
	}
	return d.Profile.Model
}

// deviceLogo is the maker's mark for an online device, or nil.
func deviceLogo(d DiscoveredDevice) *ui.SVG {
	if isEmulator(d) {
		return nil
	}
	p := d.Profile
	return phoneLogo(p.Manufacturer, p.Brand, p.Model, p.MarketName, p.DeviceName)
}

// tunnelDue reports whether automatic reconnection may try hash's Companion tunnel now, and
// records the try (at most every 30 s per phone: a new mesh route restarts that network once).
func (a *App) tunnelDue(hash string) bool {
	a.rt.coreMu.Lock()
	defer a.rt.coreMu.Unlock()
	if a.rt.tunnelTried == nil {
		a.rt.tunnelTried = map[string]time.Time{}
	}
	if time.Since(a.rt.tunnelTried[hash]) < 30*time.Second {
		return false
	}
	a.rt.tunnelTried[hash] = time.Now()
	return true
}

// rememberedAt returns the hash and own name of the remembered phone last reached on serial's Wireless debugging address,
// so a phone that could not be probed this time still shows as itself rather than a second card.
func (a *App) rememberedAt(serial string) (hash, name string) {
	host, _, err := net.SplitHostPort(serial)
	if err != nil {
		return "", ""
	}
	for _, r := range a.rememberedList() {
		if h, _, err := net.SplitHostPort(r.Endpoint); err == nil && h == host {
			return r.HardwareHash, r.Name
		}
	}
	return "", ""
}

// dropDeadWireless disconnects the wireless adb connections adb still lists as offline (the
// phone left that network or turned Wireless debugging off) and leaves them out, so they do not
// show as phones of their own; adb keeps such entries until told to forget them.
func dropDeadWireless(ctx context.Context, client *adb.Client, devs []adb.Device) []adb.Device {
	out := devs[:0:0]
	for _, d := range devs {
		if d.State == "offline" && strings.Contains(d.Serial, ":") && d.Transport != adb.TransportEmulator {
			_ = client.Disconnect(ctx, d.Serial)
			continue
		}
		out = append(out, d)
	}
	return out
}

// noteCompanionPort saves the Companion port that answered for hash, after the phone moved to
// another of its ports (companion.DevicePorts).
// Side effects: saves devices.json.
func (a *App) noteCompanionPort(hash string, port int) {
	a.update(func() {
		if i := a.rememberedIndexLocked(hash); i >= 0 && a.remembered[i].Companion != nil {
			a.remembered[i].Companion.Port = port
			a.saveDevicesLocked()
		}
	})
}

// noteWirelessEndpoint saves the latest verified TLS Wireless debugging `host:port` for hash.
// Side effects: saves devices.json when the endpoint changed.
func (a *App) noteWirelessEndpoint(hash, endpoint string) {
	if hash == "" || endpoint == "" || strings.HasPrefix(endpoint, "127.0.0.1:") {
		return
	}
	a.update(func() {
		if i := a.rememberedIndexLocked(hash); i >= 0 && a.remembered[i].Endpoint != endpoint {
			a.remembered[i].Endpoint = endpoint
			a.saveDevicesLocked()
		}
	})
}

// portScanDue rate-limits full Wireless debugging port-range scans to at most once every 25 s per phone.
func (a *App) portScanDue(hash string) bool {
	if hash == "" {
		return false
	}
	a.rt.coreMu.Lock()
	defer a.rt.coreMu.Unlock()
	if a.rt.portScanTried == nil {
		a.rt.portScanTried = map[string]time.Time{}
	}
	if time.Since(a.rt.portScanTried[hash]) < 25*time.Second {
		return false
	}
	a.rt.portScanTried[hash] = time.Now()
	return true
}

func (a *App) deviceIconDir(hash string) string {
	if a.stateDir == "" {
		return filepath.Join(os.TempDir(), "androfuse-icon-cache", hash)
	}
	return filepath.Join(a.stateDir, "icon-cache", hash)
}

// saveDeviceApps persists a probed phone's launchable apps to `<iconDir>/apps.json` and updates
// the in-memory cache so Companion sessions can open Independent App Windows without ADB.
func (a *App) saveDeviceApps(hash string, apps []adb.AppEntry) {
	if hash == "" || len(apps) == 0 {
		return
	}
	cp := append([]adb.AppEntry(nil), apps...)
	a.mu.Lock()
	if a.cachedApps == nil {
		a.cachedApps = make(map[string][]adb.AppEntry)
	}
	a.cachedApps[hash] = cp
	a.mu.Unlock()
	if a.stateDir == "" {
		return
	}
	dir := a.deviceIconDir(hash)
	_ = os.MkdirAll(dir, 0o755)
	if raw, err := json.Marshal(cp); err == nil {
		tmp := filepath.Join(dir, "apps.json.tmp")
		if os.WriteFile(tmp, raw, 0o600) == nil {
			_ = os.Rename(tmp, filepath.Join(dir, "apps.json"))
		}
	}
}

func (a *App) loadDeviceAppsLocked(hash string) []adb.AppEntry {
	if hash == "" {
		return nil
	}
	if a.cachedApps != nil {
		if cached, ok := a.cachedApps[hash]; ok && len(cached) > 0 {
			return cached
		}
	}
	if a.stateDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(a.deviceIconDir(hash), "apps.json"))
	if err != nil {
		return nil
	}
	var apps []adb.AppEntry
	if json.Unmarshal(raw, &apps) != nil || len(apps) == 0 {
		return nil
	}
	if a.cachedApps == nil {
		a.cachedApps = make(map[string][]adb.AppEntry)
	}
	a.cachedApps[hash] = apps
	if a.appIcons == nil {
		a.appIcons = make(map[string]*ui.Bitmap)
	}
	for _, ap := range apps {
		if ap.IconPath == "" || a.appIcons[ap.IconPath] != nil {
			continue
		}
		if imgRaw, rErr := os.ReadFile(ap.IconPath); rErr == nil {
			if bmp, bErr := ui.DecodeBitmap(imgRaw); bErr == nil {
				a.appIcons[ap.IconPath] = bmp
			}
		}
	}
	return apps
}

// appsForDevice returns the launchable app list for hash, preferring live probed apps and falling
// back to the on-disk/memory cache for Companion sessions when ADB is offline.
func (a *App) appsForDevice(hash string) []adb.AppEntry {
	if hash == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range a.devices {
		if (d.HardwareHash == hash || d.Target.DeviceHash == hash) && len(d.Apps) > 0 {
			return d.Apps
		}
	}
	return a.loadDeviceAppsLocked(hash)
}
