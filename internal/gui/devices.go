package gui

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/dex"
	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
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

// bindCompanion saves how to reach hash's Companion again after a pairing, merging any earlier
// `mesh:<id>` entry whose fingerprint or EasyTier identity matches this physical device.
// Side effects: seals token with DPAPI and saves devices.json.
func (a *App) bindCompanion(hash string, port int, fingerprint, token, meshHost string) {
	sealed, err := sealSecret([]byte(token))
	if err != nil {
		return
	}
	a.update(func() {
		a.mergeMeshRememberedLocked(hash, fingerprint, meshHost)
		if i := a.rememberedIndexLocked(hash); i >= 0 {
			a.remembered[i].Companion = &CompanionBinding{Port: port, Fingerprint: fingerprint, SealedToken: sealed, MeshHost: meshHost}
			a.saveDevicesLocked()
		}
	})
}

// rememberMeshPhone stores or updates a cross-network paired phone before it has ever been
// connected over ADB, keyed provisionally by `"mesh:" + devID`. When ADB later connects to the
// same phone, mergeMeshRememberedLocked upgrades this entry to its hardware hash without loss.
// Returns: the remembered HardwareHash under which the phone is stored.
// Side effects: seals the pairing token with DPAPI, clears pairingWait, and saves devices.json.
func (a *App) rememberMeshPhone(devID, fallbackName string, res companion.PairResponse) string {
	sealed, err := sealSecret([]byte(res.Token))
	if err != nil {
		return ""
	}
	provHash := "mesh:" + devID
	name := strings.TrimSpace(res.DeviceName)
	if name == "" {
		name = strings.TrimSpace(res.Model)
	}
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	if name == "" {
		name = "Android"
	}
	meshHost := strings.TrimSpace(res.MeshHost)
	if meshHost == "" && devID != "" {
		meshHost = mesh.Hostname(mesh.KindPhone, devID, name)
	}
	var finalHash string
	a.update(func() {
		idx := -1
		for i, r := range a.remembered {
			if r.HardwareHash == provHash {
				idx = i
				break
			}
			if r.Companion != nil && ((res.CertFingerprint != "" && strings.EqualFold(r.Companion.Fingerprint, res.CertFingerprint)) ||
				(meshHost != "" && mesh.SameDevice(r.Companion.MeshHost, meshHost))) {
				idx = i
				break
			}
		}
		if idx < 0 {
			a.remembered = append(a.remembered, RememberedDevice{
				HardwareHash:  provHash,
				AutoReconnect: true,
			})
			idx = len(a.remembered) - 1
		}
		r := &a.remembered[idx]
		r.Name = name
		if res.Manufacturer != "" {
			r.Maker = res.Manufacturer
		}
		if res.Brand != "" {
			r.Brand = res.Brand
		}
		if res.Model != "" {
			r.Model = res.Model
		}
		if res.AndroidRelease != "" {
			r.Android = res.AndroidRelease
		}
		r.AutoReconnect = true
		r.LastSeen = time.Now()
		r.Companion = &CompanionBinding{
			Port:        res.Port,
			Fingerprint: res.CertFingerprint,
			SealedToken: sealed,
			MeshHost:    meshHost,
		}
		finalHash = r.HardwareHash
		if res.Status != nil {
			if a.peerStatus == nil {
				a.peerStatus = make(map[string]companion.DeviceStatus)
			}
			a.peerStatus[finalHash] = *res.Status
		}
		a.saveDevicesLocked()
	})
	return finalHash
}

// mergeMeshRememberedLocked upgrades or folds any provisional `mesh:<devID>` remembered entry whose
// Companion certificate fingerprint or MeshHost matches the real hardware-hashed device realHash,
// preserving its Alias, CompanionBinding, cached status, and suppressing redundant firstConnect UI.
// Caller must hold a.mu.
func (a *App) mergeMeshRememberedLocked(realHash, fingerprint, meshHost string) bool {
	if realHash == "" || strings.HasPrefix(realHash, "mesh:") {
		return false
	}
	meshIdx := -1
	for i, r := range a.remembered {
		if !strings.HasPrefix(r.HardwareHash, "mesh:") || r.Companion == nil {
			continue
		}
		if fingerprint != "" && strings.EqualFold(r.Companion.Fingerprint, fingerprint) {
			meshIdx = i
			break
		}
		if meshHost != "" && r.Companion.MeshHost != "" && mesh.SameDevice(r.Companion.MeshHost, meshHost) {
			meshIdx = i
			break
		}
	}
	if meshIdx < 0 {
		return false
	}
	prov := a.remembered[meshIdx]
	oldHash := prov.HardwareHash
	realIdx := a.rememberedIndexLocked(realHash)
	if realIdx >= 0 {
		dst := &a.remembered[realIdx]
		if dst.Alias == "" && prov.Alias != "" {
			dst.Alias = prov.Alias
		}
		if prov.AutoReconnect {
			dst.AutoReconnect = true
		}
		if dst.Companion == nil {
			dst.Companion = prov.Companion
		} else if prov.Companion != nil {
			if dst.Companion.Port == 0 {
				dst.Companion.Port = prov.Companion.Port
			}
			if dst.Companion.MeshHost == "" {
				dst.Companion.MeshHost = prov.Companion.MeshHost
			}
		}
		a.remembered = slices.Delete(a.remembered, meshIdx, meshIdx+1)
	} else {
		a.remembered[meshIdx].HardwareHash = realHash
	}
	a.migrateCachedAppsLocked(oldHash, realHash)
	if a.peerStatus != nil {
		if st, ok := a.peerStatus[oldHash]; ok {
			if _, exists := a.peerStatus[realHash]; !exists {
				a.peerStatus[realHash] = st
			}
			delete(a.peerStatus, oldHash)
		}
	}
	if a.cardWork != nil {
		if cw, ok := a.cardWork[oldHash]; ok {
			if _, exists := a.cardWork[realHash]; !exists {
				a.cardWork[realHash] = cw
			}
			delete(a.cardWork, oldHash)
		}
	}
	if a.firstConnect != nil && a.firstConnect.Hash == realHash {
		a.firstConnect = nil
	}
	if a.compHash == oldHash {
		a.compHash = realHash
	}
	return true
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

// sanitizeCacheKey turns a device identifier (including provisional `mesh:<id>` keys containing
// `:`) into a single filesystem-safe directory name on Windows and Unix.
func sanitizeCacheKey(hash string) string {
	var b strings.Builder
	b.Grow(len(hash))
	for i := 0; i < len(hash); i++ {
		c := hash[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "device"
	}
	return b.String()
}

func sanitizeIconPkg(pkg string) string {
	return sanitizeCacheKey(pkg)
}

func (a *App) deviceIconDir(hash string) string {
	key := sanitizeCacheKey(hash)
	if a.stateDir == "" {
		return filepath.Join(os.TempDir(), "androfuse-icon-cache", key)
	}
	return filepath.Join(a.stateDir, "icon-cache", key)
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

// needsCompanionAppFetch reports whether hash lacks a cached app list or whether more than a
// quarter of its apps have no cached icon, which is how a list without names and icons looks
// (the adb fallback, `ListLaunchableApps`), so Enhanced mode should fetch it again.
func (a *App) needsCompanionAppFetch(hash string) bool {
	apps := a.appsForDevice(hash)
	return len(apps) == 0 || 4*appsWithoutIcon(apps) > len(apps)
}

// appsWithoutIcon counts the apps whose cached icon file is missing or empty.
func appsWithoutIcon(apps []adb.AppEntry) int {
	n := 0
	for _, ap := range apps {
		if ap.IconPath == "" {
			n++
		} else if info, err := os.Stat(ap.IconPath); err != nil || info.Size() == 0 {
			n++
		}
	}
	return n
}

// withCachedDetails returns fresh with every app it lists without an icon given the name and icon
// cached lists for it, so a package-name-only list never replaces names and icons already fetched.
func withCachedDetails(fresh, cached []adb.AppEntry) []adb.AppEntry {
	known := make(map[string]adb.AppEntry, len(cached))
	for _, ap := range cached {
		if ap.IconPath != "" {
			known[ap.Package] = ap
		}
	}
	out := make([]adb.AppEntry, len(fresh))
	for i, ap := range fresh {
		if old, ok := known[ap.Package]; ok && ap.IconPath == "" {
			ap.Label, ap.IconPath, ap.System, ap.Resizeable = old.Label, old.IconPath, old.System, old.Resizeable
		}
		out[i] = ap
	}
	return out
}

// appsFetchState is how fetching one phone's app list is going, for the + picker.
type appsFetchState struct {
	Busy bool
	Err  string    // plain words for the last failure (with where the details are); "" after a success
	At   time.Time // when the last fetchApps ended
}

// appsRetryAfter is how long an automatic app list fetch waits after the last one; the picker's
// reload button does not wait.
const appsRetryAfter = 20 * time.Second

// errNoAppSource is fetchApps' failure when the phone is neither online over adb nor paired with
// Companion.
var errNoAppSource = errors.New("the phone is not connected over adb or paired with Companion")

// fetchApps fetches hash's app list, names and icons. Automatic fetches (force false) go through
// Enhanced mode only (the adb scan lists apps itself) and wait appsRetryAfter after the last one;
// the picker's reload button (force true) tries adb first when the phone is online there, then
// Enhanced mode, and downloads every icon again.
// Side effects: replaces the cached list and icons; keeps the outcome in a.appsFetch and, for a
// failure, in the log Save logs writes.
func (a *App) fetchApps(hash string, force bool) {
	r, paired := a.RememberedFor(hash)
	paired = paired && r.Companion != nil
	if hash == "" || (!force && !paired) {
		return
	}
	a.mu.Lock()
	if a.appsFetch == nil {
		a.appsFetch = make(map[string]appsFetchState)
	}
	st := a.appsFetch[hash]
	if st.Busy || (!force && time.Since(st.At) < appsRetryAfter) {
		a.mu.Unlock()
		return
	}
	st.Busy = true
	a.appsFetch[hash] = st
	a.mu.Unlock()
	a.update(func() {})

	err := errNoAppSource
	if d, ok := a.adbOnline(hash); ok && force {
		err = a.refreshADBApps(hash, d.Target.Serial)
	}
	if err != nil && paired {
		err = a.fetchCompanionApps(hash, *r.Companion, force)
	}
	a.mu.Lock()
	st = a.appsFetch[hash]
	st.Busy, st.At = false, time.Now()
	a.appsFetch[hash] = st
	a.mu.Unlock()
	a.noteAppsResult(hash, err, force)
}

// noteAppsResult keeps how an app list fetch for hash went: a success clears the failure; a
// failure is logged when always is set or no failure is showing yet, so a periodic adb scan that
// keeps failing logs it once.
func (a *App) noteAppsResult(hash string, err error, always bool) {
	a.update(func() {
		if a.appsFetch == nil {
			a.appsFetch = make(map[string]appsFetchState)
		}
		st := a.appsFetch[hash]
		switch {
		case err == nil:
			st.Err = ""
		case always || st.Err == "":
			st.Err = a.failed(L.AppsLoadFailed(), err)
		}
		a.appsFetch[hash] = st
	})
}

// appsFetchFor returns how fetching hash's app list is going.
func (a *App) appsFetchFor(hash string) appsFetchState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.appsFetch[hash]
}

// adbOnline returns the scanned phone hash is, when it is online over adb.
func (a *App) adbOnline(hash string) (DiscoveredDevice, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range a.devices {
		if d.HardwareHash == hash && d.Target.State == "device" {
			return d, true
		}
	}
	return DiscoveredDevice{}, false
}

// adbApps lists serial's apps over adb with the helper (real names and icons into key's icon
// cache); when the helper fails it lists package names only, keeping the names and icons already
// cached for them (withCachedDetails), and returns the helper's error with that list.
// Side effects: saves the list as key's cache.
func (a *App) adbApps(ctx context.Context, client *adb.Client, serial, key string) ([]adb.AppEntry, map[string]*ui.Bitmap, error) {
	apps, err := dex.ListAppsWithIcons(ctx, client, serial, a.deviceIconDir(key))
	if err != nil {
		bare, bErr := client.ListLaunchableApps(ctx, serial)
		if bErr != nil {
			return nil, nil, err
		}
		a.mu.Lock()
		cached := a.loadDeviceAppsLocked(key)
		a.mu.Unlock()
		apps = withCachedDetails(bare, cached)
	}
	a.saveDeviceApps(key, apps)
	icons := make(map[string]*ui.Bitmap)
	for _, ap := range apps {
		if ap.IconPath == "" || icons[ap.IconPath] != nil {
			continue
		}
		if raw, rErr := os.ReadFile(ap.IconPath); rErr == nil {
			if bmp, bErr := ui.DecodeBitmap(raw); bErr == nil {
				icons[ap.IconPath] = bmp
			}
		}
	}
	return apps, icons, err
}

// refreshADBApps lists hash's apps again over adb and shows them on its scanned phone.
func (a *App) refreshADBApps(hash, serial string) error {
	client, _, err := a.ensureADBAndLeases()
	if err != nil {
		return err
	}
	ctx, cancel := a.adbWorkContext(40 * time.Second)
	defer cancel()
	apps, icons, err := a.adbApps(ctx, client, serial, hash)
	if len(apps) > 0 {
		a.showApps(hash, apps, icons)
	}
	return err
}

// showApps puts a fetched app list on hash's scanned phone and its icons in the icon cache, so
// a live list never hides a better one fetched another way.
func (a *App) showApps(hash string, apps []adb.AppEntry, icons map[string]*ui.Bitmap) {
	a.update(func() {
		for i := range a.devices {
			if a.devices[i].HardwareHash == hash {
				a.devices[i].Apps = apps
			}
		}
		if a.appIcons == nil {
			a.appIcons = make(map[string]*ui.Bitmap)
		}
		maps.Copy(a.appIcons, icons)
	})
}

// findCachedApp looks up the metadata/icon entry for pkg on hash.
func (a *App) findCachedApp(hash, pkg string) (adb.AppEntry, bool) {
	if hash == "" || pkg == "" {
		return adb.AppEntry{}, false
	}
	for _, ap := range a.appsForDevice(hash) {
		if ap.Package == pkg {
			return ap, true
		}
	}
	return adb.AppEntry{}, false
}

// appIconByPackage resolves the decoded bitmap icon for pkg on hash from memory or disk cache.
func (a *App) appIconByPackage(hash, pkg string) *ui.Bitmap {
	if pkg == "" {
		return nil
	}
	if ap, ok := a.findCachedApp(hash, pkg); ok && ap.IconPath != "" {
		if bmp := a.appIconFor(ap.IconPath); bmp != nil {
			return bmp
		}
	}
	if a.stateDir == "" || hash == "" {
		return nil
	}
	p := filepath.Join(a.deviceIconDir(hash), sanitizeIconPkg(pkg)+".png")
	if bmp := a.appIconFor(p); bmp != nil {
		return bmp
	}
	if raw, err := os.ReadFile(p); err == nil && len(raw) > 0 {
		if bmp, bErr := ui.DecodeBitmap(raw); bErr == nil {
			a.mu.Lock()
			if a.appIcons == nil {
				a.appIcons = make(map[string]*ui.Bitmap)
			}
			a.appIcons[p] = bmp
			a.mu.Unlock()
			return bmp
		}
	}
	return nil
}

// migrateCachedAppsLocked copies/migrates cached app metadata and PNG icons from provisional oldHash
// (`mesh:<id>`) into realHash (`ro.serialno` hash) while holding a.mu.
func (a *App) migrateCachedAppsLocked(oldHash, realHash string) {
	if oldHash == "" || realHash == "" || oldHash == realHash {
		return
	}
	apps := a.loadDeviceAppsLocked(oldHash)
	if len(apps) == 0 && a.cachedApps != nil {
		apps = a.cachedApps[oldHash]
	}
	if len(apps) == 0 {
		return
	}
	oldDir := a.deviceIconDir(oldHash)
	newDir := a.deviceIconDir(realHash)
	if a.stateDir != "" {
		_ = os.MkdirAll(newDir, 0o755)
	}
	migrated := make([]adb.AppEntry, 0, len(apps))
	for _, ap := range apps {
		srcPath := ap.IconPath
		if srcPath == "" && a.stateDir != "" {
			cand := filepath.Join(oldDir, sanitizeIconPkg(ap.Package)+".png")
			if _, err := os.Stat(cand); err == nil {
				srcPath = cand
			}
		}
		if srcPath != "" && a.stateDir != "" {
			dstPath := filepath.Join(newDir, sanitizeIconPkg(ap.Package)+".png")
			if _, err := os.Stat(dstPath); err != nil {
				if raw, rErr := os.ReadFile(srcPath); rErr == nil && len(raw) > 0 {
					if os.WriteFile(dstPath+".tmp", raw, 0o600) == nil {
						_ = os.Rename(dstPath+".tmp", dstPath)
					}
				}
			}
			if _, err := os.Stat(dstPath); err == nil {
				ap.IconPath = dstPath
				if a.appIcons != nil && a.appIcons[srcPath] != nil && a.appIcons[dstPath] == nil {
					a.appIcons[dstPath] = a.appIcons[srcPath]
				}
			}
		}
		migrated = append(migrated, ap)
	}
	if existing := a.loadDeviceAppsLocked(realHash); len(existing) == 0 {
		if a.cachedApps == nil {
			a.cachedApps = make(map[string][]adb.AppEntry)
		}
		a.cachedApps[realHash] = migrated
		if a.stateDir != "" {
			if raw, err := json.Marshal(migrated); err == nil {
				if os.WriteFile(filepath.Join(newDir, "apps.json.tmp"), raw, 0o600) == nil {
					_ = os.Rename(filepath.Join(newDir, "apps.json.tmp"), filepath.Join(newDir, "apps.json"))
				}
			}
		}
	}
	if a.cachedApps != nil {
		delete(a.cachedApps, oldHash)
	}
}
