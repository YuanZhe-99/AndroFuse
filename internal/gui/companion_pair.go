package gui

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/dex"
	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// companionAPK locates the Companion installer: in companion/ next to the executable (packaged
// builds), next to it, or in build/ of a development checkout. Returns "" when there is none.
func companionAPK() string {
	name := fmt.Sprintf("AndroFuse-%s-android-arm64.apk", version.Version)
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "companion", name), filepath.Join(dir, name))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "build", name))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// PairCompanionOverADB installs the Companion on the adb device serial, pairs with it once the
// phone's user allows the code shown on both screens, and, when joinMesh is set and the desktop
// mesh is running, hands that mesh to the phone (PLAN §9.1-1).
// Side effects: see pairOverADB; replaces the current Companion connection; progress goes to the
// Companion card's notice.
func (a *App) PairCompanionOverADB(serial, model string, joinMesh bool) {
	note := func(s string) { a.update(func() { a.compNotice = s }) }
	go func() {
		client, _, err := a.ensureADBAndLeases()
		if err != nil {
			note(err.Error())
			return
		}
		a.mu.Lock()
		apk := a.customAPK
		a.mu.Unlock()
		if apk == "" {
			apk = companionAPK()
		}
		if apk == "" {
			note(L.CompanionApkMissing())
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		defer a.update(func() { a.pairingCode = "" })
		c, addr, res, err := pairOverADB(ctx, client, serial, apk, note, func(code string) {
			a.update(func() { a.pairingCode, a.pairingModel = code, model })
		})
		if err != nil {
			note(err.Error())
			return
		}
		st, _ := c.GetStatus(ctx)
		a.rt.coreMu.Lock()
		old := a.rt.compClient
		a.rt.compClient = c
		a.rt.coreMu.Unlock()
		if old != nil {
			_ = old.Close()
		}
		cfg, inNet := a.pairNetworkConfig()
		a.mu.Lock()
		hash := ""
		for _, d := range a.devices {
			for _, tr := range d.Transports {
				if tr.Serial == serial {
					hash = d.HardwareHash
				}
			}
		}
		a.mu.Unlock()
		a.update(func() {
			a.compConnected = true
			a.compHash = hash
			a.compStatus = st
			a.compAddr = addr
			a.compToken = res.Token
			a.compNotice = L.CompanionPaired(model)
		})
		a.notify(L.CompanionPaired(model))
		if hash != "" && res.Port > 0 {
			meshHost := res.MeshHost
			if meshHost == "" {
				meshHost = res.Model // a Companion from before mesh.Hostname
			}
			a.bindCompanion(hash, res.Port, res.CertFingerprint, res.Token, meshHost)
		}
		if dErr := dex.StartDaemon(ctx, client, serial, companionPackage); dErr != nil {
			_ = a.failed(L.FailCompanion(), dErr)
		}
		if !joinMesh || !inNet {
			a.RefreshDevices()
			return
		}
		jr, err := c.JoinMesh(ctx, companion.MeshJoinRequest{
			NetworkName:   cfg.NetworkName,
			NetworkSecret: cfg.NetworkSecret,
			Peers:         peersForPhone(cfg),
		})
		switch {
		case err != nil:
			note(err.Error())
		case jr.Message != "":
			note(jr.Message)
		default:
			note(L.CompanionMeshJoined(model, a.pairNetworkName()))
		}
	}()
}

// pairOverADB installs apk on serial, starts its Companion service with the secret-free
// `androfuse://pair` link, forwards its local socket and requests pairing with a fresh code
// (reported through progress), retrying while the phone's service starts.
// Returns: the authenticated client, its forwarded loopback address and the pairing result.
// Side effects: `adb install -r`, `am start`, an `adb forward` kept for the session.
func pairOverADB(ctx context.Context, client *adb.Client, serial, apk string, progress, onCode func(string)) (*companion.Client, string, companion.PairResponse, error) {
	var res companion.PairResponse
	progress(L.CompanionInstalling())
	if out, err := client.Run(ctx, "-s", serial, "install", "-r", apk); err != nil || !strings.Contains(out, "Success") {
		return nil, "", res, fmt.Errorf("install companion: %s %v", strings.TrimSpace(out), err)
	}
	_, _ = client.Shell(ctx, serial, "am start -a android.intent.action.VIEW -d androfuse://pair com.yuanzhe.androfuse")
	port, err := client.ForwardPort(ctx, serial, 0, "localabstract:"+companion.LocalSocket)
	if err != nil {
		return nil, "", res, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	code, err := pairingCode()
	if err != nil {
		return nil, "", res, err
	}
	progress(L.CompanionConfirmCode(code))
	if onCode != nil {
		onCode(code)
	}
	host, _ := os.Hostname()
	for deadline := time.Now().Add(companion.PairWait + 30*time.Second); ; {
		pctx, pcancel := context.WithTimeout(ctx, companion.PairWait+5*time.Second)
		res, err = companion.RequestPairing(pctx, addr, "AndroFuse · "+host, code)
		pcancel()
		// Until the phone's service is up the forward refuses or drops the connection; retry.
		if err == nil || strings.Contains(err.Error(), "declined") || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return nil, "", res, err
	}
	c, err := companion.Dial(ctx, addr, res.Token, res.CertFingerprint)
	if err != nil {
		return nil, "", res, err
	}
	return c, addr, res, nil
}

// pairingCode returns a random 6-digit code for the user to compare on both screens.
func pairingCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// peersForPhone lists how the phone reaches this desktop's mesh: the desktop's relays, plus each
// of its listeners with a wildcard host replaced by this computer's LAN addresses.
func peersForPhone(cfg mesh.Config) []string {
	peers := append([]string(nil), cfg.Peers...)
	for _, l := range cfg.Listeners {
		u, err := url.Parse(l)
		if err != nil || u.Port() == "" {
			continue
		}
		switch u.Hostname() {
		case "", "0.0.0.0", "::":
			for _, ip := range lanIPv4() {
				peers = append(peers, fmt.Sprintf("%s://%s", u.Scheme, net.JoinHostPort(ip, u.Port())))
			}
		default:
			peers = append(peers, l)
		}
	}
	return peers
}

// lanIPv4 returns this computer's non-loopback IPv4 addresses.
func lanIPv4() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			out = append(out, ipn.IP.String())
		}
	}
	return out
}

// ReconnectCompanion connects again to the paired Companion of the remembered phone hash, without
// a new pairing: over adb's local socket while the phone is on adb, otherwise through the mesh,
// where a loopback port forward reaches the phone's random Companion port at its mesh address
// (`INV-07`: no TUN, nothing listens beyond 127.0.0.1).
// Side effects: may restart the running mesh with the added forward; replaces the current
// Companion connection; progress goes to the Companion card's notice.
func (a *App) ReconnectCompanion(hash string) {
	note := func(s string) { a.update(func() { a.compNotice = s }) }
	r, ok := a.RememberedFor(hash)
	if !ok || r.Companion == nil {
		note(L.CompanionNotPaired())
		return
	}
	b := *r.Companion
	go func() {
		tok, err := openSecret(b.SealedToken)
		if err != nil {
			note(err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		c, addr, err := a.findCompanion(ctx, hash, b, string(tok))
		if err != nil {
			note(a.failed(L.FailCompanion(), err))
			return
		}
		st, _ := c.GetStatus(ctx)
		a.rt.coreMu.Lock()
		old := a.rt.compClient
		a.rt.compClient = c
		a.rt.coreMu.Unlock()
		if old != nil {
			_ = old.Close()
		}
		a.update(func() {
			a.compConnected = true
			a.compHash = hash
			a.compStatus = st
			a.compAddr = addr
			a.compToken = string(tok)
			a.compNotice = L.CompanionPaired(r.Name)
		})
	}()
}

// companionRoute is an address that reaches a phone's Companion, the Companion port behind it
// (0 for adb's local socket, which has none), and whether it rides EasyTier or a direct LAN IP.
type companionRoute struct {
	addr      string
	port      int
	isMesh    bool
	directLAN bool
}

// companionRoutes returns addresses that may reach b's Companion in priority order:
//  1. adb forward of its local socket when the phone is on adb,
//  2. direct Wi-Fi / LAN TCP to the phone's reported WifiIPv4 or remembered endpoint host (TLS 1.3
//     with certificate fingerprint pinning and pairing token auth),
//  3. EasyTier mesh loopback port forwards to its mesh IPv4 at the saved port and candidate
//     companion.DevicePorts(id).
func (a *App) companionRoutes(ctx context.Context, hash string, b CompanionBinding) ([]companionRoute, error) {
	a.mu.Lock()
	serial := ""
	for _, d := range a.devices {
		if d.HardwareHash == hash && d.Target.State == "device" {
			serial = d.Target.Serial
		}
	}
	a.mu.Unlock()
	var routes []companionRoute
	if serial != "" {
		if client, _, err := a.ensureADBAndLeases(); err == nil {
			fctx, fcancel := context.WithTimeout(ctx, 2*time.Second)
			port, fErr := client.ForwardPort(fctx, serial, 0, "localabstract:"+companion.LocalSocket)
			fcancel()
			if fErr == nil {
				routes = append(routes, companionRoute{addr: fmt.Sprintf("127.0.0.1:%d", port)})
			}
		}
	}

	var ports []int
	if b.Port > 0 {
		ports = append(ports, b.Port)
	}
	if _, devID, _, ok := mesh.ParseHostname(b.MeshHost); ok {
		for _, p := range companion.DevicePorts(devID) {
			if !slices.Contains(ports, p) {
				ports = append(ports, p)
			}
		}
	}

	if len(ports) > 0 {
		var hosts []string
		addLANHost := func(raw string) {
			ip := net.ParseIP(strings.TrimSpace(raw))
			if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() {
				return
			}
			s := ip.String()
			if !slices.Contains(hosts, s) {
				hosts = append(hosts, s)
			}
		}
		if st, ok := a.peerStatusFor(hash); ok {
			addLANHost(st.WifiIPv4)
		}
		if r, ok := a.RememberedFor(hash); ok && r.Endpoint != "" {
			if h, _, err := net.SplitHostPort(r.Endpoint); err == nil {
				addLANHost(h)
			}
		}
		lanPorts := ports
		if len(lanPorts) > 2 {
			lanPorts = lanPorts[:2]
		}
		for _, h := range hosts {
			for _, p := range lanPorts {
				routes = append(routes, companionRoute{
					addr:      net.JoinHostPort(h, fmt.Sprintf("%d", p)),
					port:      p,
					directLAN: true,
				})
			}
		}
	}

	// Right after a network starts or restarts, its routes take a moment to list the phone again.
	if b.MeshHost != "" {
		id, ip, ok := a.meshPeerIPv4(b.MeshHost)
		if !ok && len(routes) == 0 {
			for deadline := time.Now().Add(5 * time.Second); !ok && time.Now().Before(deadline) && ctx.Err() == nil; {
				time.Sleep(300 * time.Millisecond)
				id, ip, ok = a.meshPeerIPv4(b.MeshHost)
			}
		}
		if ok && len(ports) > 0 {
			if locals, err := a.meshForwards(id, ip, ports); err == nil {
				for i, p := range ports {
					routes = append(routes, companionRoute{
						addr:   fmt.Sprintf("127.0.0.1:%d", locals[i]),
						port:   p,
						isMesh: true,
					})
				}
			}
		}
	}
	if len(routes) == 0 {
		return nil, errors.New(L.CompanionNoRoute())
	}
	return routes, nil
}

// isMeshRoute reports whether addr matches one of the active EasyTier loopback port forwards.
func (a *App) isMeshRoute(addr string) bool {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || host != "127.0.0.1" {
		return false
	}
	a.rt.coreMu.Lock()
	defer a.rt.coreMu.Unlock()
	for _, cfg := range a.rt.meshCfgs {
		for _, pf := range cfg.PortForwards {
			if fmt.Sprintf("%d", pf.LocalPort) == portStr {
				return true
			}
		}
	}
	return false
}

// findCompanion connects to hash's Companion over the first of companionRoutes that answers,
// trying again for up to 12 s while a new mesh forward settles.
// Returns: the connected client and the address that reached it.
// Side effects: saves the port that answered when it is not the saved one, and the hostname the
// Companion reported (noteMeshHost).
func (a *App) findCompanion(ctx context.Context, hash string, b CompanionBinding, tok string) (*companion.Client, string, error) {
	routes, err := a.companionRoutes(ctx, hash, b)
	if err != nil {
		return nil, "", err
	}
	skipLAN := make(map[string]bool)
	for deadline := time.Now().Add(12 * time.Second); ; time.Sleep(500 * time.Millisecond) {
		tried := false
		for _, r := range routes {
			if r.directLAN && skipLAN[r.addr] {
				continue
			}
			tried = true
			timeout := 2500 * time.Millisecond
			if r.directLAN {
				timeout = 1000 * time.Millisecond
			}
			dctx, cancel := context.WithTimeout(ctx, timeout)
			c, dErr := companion.Dial(dctx, r.addr, tok, b.Fingerprint)
			cancel()
			if dErr == nil {
				a.noteMeshHost(hash, c.PeerInfo().MeshHost)
				if r.port > 0 && r.port != b.Port {
					a.noteCompanionPort(hash, r.port)
				}
				return c, r.addr, nil
			}
			if errors.Is(dErr, companion.ErrFingerprint) {
				return nil, "", dErr // another certificate: retrying will not help
			}
			if r.directLAN {
				skipLAN[r.addr] = true
			}
			err = dErr
		}
		if !tried || time.Now().After(deadline) || ctx.Err() != nil {
			if err == nil {
				err = errors.New(L.CompanionNoRoute())
			}
			return nil, "", err
		}
	}
}

// ChooseCompanionAPK lets the user pick a Companion installer when none ships next to the app.
// Side effects: opens the system file dialog off the UI thread; the choice is used by later
// PairCompanionOverADB calls of this run.
func (a *App) ChooseCompanionAPK() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.mainWindow(),
			Title:   L.CompanionChooseApk(),
			Filters: []mygo.FileFilter{{Name: "Android", Extensions: []string{"apk"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.update(func() {
			a.customAPK = paths[0]
			a.compNotice = L.CompanionApkChosen(filepath.Base(paths[0]))
		})
	}()
}

// companionPackage is the Companion's Android application id.
const companionPackage = "com.yuanzhe.androfuse"

// syncCompanions checks, once each time a phone comes online over adb (USB, Wi-Fi or an address
// typed in), that its Companion is current, paired and in the network (syncCompanion).
func (a *App) syncCompanions(devs []DiscoveredDevice) {
	online := map[string]bool{}
	var todo []DiscoveredDevice
	a.rt.coreMu.Lock()
	if a.rt.synced == nil {
		a.rt.synced = map[string]bool{}
	}
	for _, d := range devs {
		if d.HardwareHash == "" || !d.Probed || d.Target.State != "device" || isEmulator(d) {
			continue
		}
		online[d.HardwareHash] = true
		if !a.rt.synced[d.HardwareHash] {
			a.rt.synced[d.HardwareHash] = true
			todo = append(todo, d)
		}
	}
	for h := range a.rt.synced {
		if !online[h] {
			delete(a.rt.synced, h)
		}
	}
	a.rt.coreMu.Unlock()
	for _, d := range todo {
		go a.syncCompanion(d)
	}
}

// syncCompanion brings d's installed Companion up to date: it installs the bundled Companion
// over an older or different one, pairs with it when this computer has not (the code shows on
// both screens, as for a first pairing), and, once paired, hands it the pairing network again
// when the phone is not in it. A phone without the Companion is left alone here; the first
// connection offers to install it (firstConnect).
// Side effects: `adb install -r` and `am start` on the phone; may pair and hand over the network;
// failures go to Save logs only.
func (a *App) syncCompanion(d DiscoveredDevice) {
	client, _, err := a.ensureADBAndLeases()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	serial, name := d.Target.Serial, a.phoneName(d)
	out, _ := client.Shell(ctx, serial, "pm path "+companionPackage)
	installed := ""
	for _, l := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(l), "package:"); ok && strings.HasSuffix(p, "/base.apk") {
			installed = p
		}
	}
	a.mu.Lock()
	apk := a.customAPK
	a.mu.Unlock()
	if apk == "" {
		apk = companionAPK()
	}
	current := installed != "" && (apk == "" || sameAPK(ctx, client, serial, installed, apk))
	a.update(func() {
		if a.compInstalled == nil {
			a.compInstalled = map[string]compInstall{}
		}
		a.compInstalled[d.HardwareHash] = compInstall{Installed: installed != "", Current: current}
	})
	if installed == "" {
		return
	}
	r, _ := a.RememberedFor(d.HardwareHash)
	if r.Companion == nil {
		a.PairCompanionOverADB(serial, name, a.pairNetworkName() != "") // also installs the bundled one
		return
	}
	if !current {
		a.updateCompanion(ctx, client, d.HardwareHash, serial, name, apk)
	}
	cfg, inNet := a.pairNetworkConfig()
	tok, err := openSecret(r.Companion.SealedToken)
	if err != nil {
		return
	}
	c, _, err := a.findCompanion(ctx, d.HardwareHash, *r.Companion, string(tok))
	if errors.Is(err, companion.ErrFingerprint) {
		// The Companion has a new identity (reinstalled, or a build before it kept one): pair again.
		a.PairCompanionOverADB(serial, name, a.pairNetworkName() != "")
		return
	}
	if err != nil {
		a.failed(L.FailCompanion(), err)
		return
	}
	defer c.Close()
	st, err := c.GetStatus(ctx)
	if err == nil && !st.DaemonReady {
		if dErr := dex.StartDaemon(ctx, client, serial, companionPackage); dErr != nil {
			_ = a.failed(L.FailCompanion(), dErr)
		}
	}
	if err != nil || !inNet || (st.MeshRunning && st.MeshNetwork == cfg.NetworkName) {
		return
	}
	jr, err := c.JoinMesh(ctx, companion.MeshJoinRequest{NetworkName: cfg.NetworkName, NetworkSecret: cfg.NetworkSecret, Peers: peersForPhone(cfg)})
	switch {
	case err != nil:
		a.failed(L.FailCompanion(), err)
	case jr.Message != "":
		a.failed(L.FailCompanion(), errors.New(jr.Message))
	default:
		a.notify(L.CompanionMeshJoined(name, a.pairNetworkName()))
	}
}

// sameAPK reports whether the phone's installed Companion at path is byte for byte the APK at
// local (adb installs a single APK unchanged as base.apk).
func sameAPK(ctx context.Context, client *adb.Client, serial, path, local string) bool {
	raw, err := os.ReadFile(local)
	if err != nil {
		return true // nothing to update from
	}
	sum := sha256.Sum256(raw)
	out, err := client.Shell(ctx, serial, "sha256sum "+path)
	if err != nil {
		return true
	}
	remote, _, _ := strings.Cut(strings.TrimSpace(out), " ")
	return strings.EqualFold(remote, hex.EncodeToString(sum[:]))
}

// compInstall is what the last check found of a phone's Companion: installed, and whether it is
// the one bundled with this AndroFuse.
type compInstall struct {
	Installed, Current bool
}

// updateCompanion installs apk over the Companion on serial and starts it again, keeping its
// pairings and settings (adb install -r keeps the app's data).
// Side effects: progress and failure show on hash's phone card; a notification when done.
func (a *App) updateCompanion(ctx context.Context, client *adb.Client, hash, serial, name, apk string) {
	a.setCardWork(hash, cardWork{Busy: L.CompanionUpdating(name)})
	out, err := client.Run(ctx, "-s", serial, "install", "-r", apk)
	if err != nil || !strings.Contains(out, "Success") {
		a.setCardWork(hash, cardWork{Note: a.failed(L.CompanionUpdateFailed(name), fmt.Errorf("install -r: %s %v", strings.TrimSpace(out), err))})
		return
	}
	_, _ = client.Shell(ctx, serial, "am start -n "+companionPackage+"/dev.mygo.MyGoActivity")
	a.setCardWork(hash, cardWork{})
	a.update(func() { a.compInstalled[hash] = compInstall{Installed: true, Current: true} })
	a.notify(L.CompanionUpdated(name))
}

// UpdateCompanion installs the bundled Companion over d's (the phone card's Update item).
func (a *App) UpdateCompanion(d DiscoveredDevice) {
	go func() {
		client, _, err := a.ensureADBAndLeases()
		if err != nil {
			return
		}
		a.mu.Lock()
		apk := a.customAPK
		a.mu.Unlock()
		if apk == "" {
			apk = companionAPK()
		}
		if apk == "" {
			a.setCardWork(d.HardwareHash, cardWork{Note: L.CompanionApkMissing()})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		a.updateCompanion(ctx, client, d.HardwareHash, d.Target.Serial, a.phoneName(d), apk)
	}()
}

func (a *App) localCompanionAPK() (path, shaHex string) {
	a.mu.Lock()
	apk := a.customAPK
	a.mu.Unlock()
	if apk == "" {
		apk = companionAPK()
	}
	if apk == "" {
		return "", ""
	}
	a.rt.coreMu.Lock()
	if a.rt.localAPKPath == apk && a.rt.localAPKHash != "" {
		h := a.rt.localAPKHash
		a.rt.coreMu.Unlock()
		return apk, h
	}
	a.rt.coreMu.Unlock()

	raw, err := os.ReadFile(apk)
	if err != nil {
		return apk, ""
	}
	sum := sha256.Sum256(raw)
	h := hex.EncodeToString(sum[:])
	a.rt.coreMu.Lock()
	a.rt.localAPKPath = apk
	a.rt.localAPKHash = h
	a.rt.coreMu.Unlock()
	return apk, h
}

func (a *App) peerStatusFor(hash string) (companion.DeviceStatus, bool) {
	if hash == "" {
		return companion.DeviceStatus{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if st, ok := a.peerStatus[hash]; ok {
		return st, true
	}
	if a.compConnected && a.compHash == hash {
		return a.compStatus, true
	}
	return companion.DeviceStatus{}, false
}

func (a *App) hasOnlineADB(hash string) bool {
	if hash == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range a.devices {
		if (d.HardwareHash == hash || d.Target.DeviceHash == hash) && d.Target.State == "device" {
			return true
		}
	}
	return false
}

func (a *App) maintDue(hash string, cooldown time.Duration) bool {
	if hash == "" {
		return false
	}
	a.rt.coreMu.Lock()
	defer a.rt.coreMu.Unlock()
	if a.rt.maintTried == nil {
		a.rt.maintTried = map[string]time.Time{}
	}
	if time.Since(a.rt.maintTried[hash]) < cooldown {
		return false
	}
	a.rt.maintTried[hash] = time.Now()
	return true
}

// phoneNeedsAction decides whether the desktop should pull up ADB, update the Companion APK,
// or activate Enhanced mode on a remembered paired phone from its reported Companion status.
func phoneNeedsAction(st companion.DeviceStatus, localHash string, onlineADB, allowsWirelessADB bool) (needsAPK, needsDaemon, wantsADB bool) {
	needsAPK = localHash != "" && st.AppBuildHash != "" && !strings.EqualFold(st.AppBuildHash, localHash)
	needsDaemon = !st.DaemonReady
	wantsADB = allowsWirelessADB && !onlineADB
	return needsAPK, needsDaemon, wantsADB
}

// fetchCompanionApps populates the per-device app list directly from Companion Enhanced mode
// (`apps.list`), reusing any cached icon PNGs on disk so Independent App Windows work without ADB.
func (a *App) fetchCompanionApps(hash string) {
	r, ok := a.RememberedFor(hash)
	if !ok || r.Companion == nil {
		return
	}
	a.rt.coreMu.Lock()
	if a.rt.fetchingApps == nil {
		a.rt.fetchingApps = map[string]bool{}
	}
	if a.rt.fetchingApps[hash] {
		a.rt.coreMu.Unlock()
		return
	}
	a.rt.fetchingApps[hash] = true
	a.rt.coreMu.Unlock()
	defer func() {
		a.rt.coreMu.Lock()
		delete(a.rt.fetchingApps, hash)
		a.rt.coreMu.Unlock()
	}()

	tok, err := openSecret(r.Companion.SealedToken)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, _, err := a.findCompanion(ctx, hash, *r.Companion, string(tok))
	if err != nil {
		return
	}
	list, lErr := c.ListApps(ctx)
	_ = c.Close()
	if lErr != nil || len(list) == 0 {
		return
	}
	iconDir := a.deviceIconDir(hash)
	apps := make([]adb.AppEntry, 0, len(list))
	decoded := make(map[string]*ui.Bitmap)
	for _, ap := range list {
		entry := adb.AppEntry{
			Package:    ap.Package,
			Label:      ap.Label,
			System:     ap.System,
			Resizeable: ap.Resizeable,
		}
		if iconDir != "" {
			p := filepath.Join(iconDir, ap.Package+".png")
			if _, sErr := os.Stat(p); sErr == nil {
				entry.IconPath = p
				if raw, rErr := os.ReadFile(p); rErr == nil {
					if bmp, bErr := ui.DecodeBitmap(raw); bErr == nil {
						decoded[p] = bmp
					}
				}
			}
		}
		apps = append(apps, entry)
	}
	a.saveDeviceApps(hash, apps)
	a.update(func() {
		if a.appIcons == nil {
			a.appIcons = make(map[string]*ui.Bitmap)
		}
		for p, bmp := range decoded {
			a.appIcons[p] = bmp
		}
	})
}

func (a *App) pollCompanionHealth(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Second):
	}
	run := func() {
		for _, r := range a.rememberedList() {
			if r.Companion == nil {
				continue
			}
			_, inNet := a.networkWay(r)
			if !inNet && !a.hasOnlineADB(r.HardwareHash) {
				continue
			}
			a.checkCompanionHealthOnce(ctx, r)
		}
	}
	run()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

func (a *App) checkCompanionHealthOnce(ctx context.Context, r RememberedDevice) {
	if r.Companion == nil {
		return
	}
	tok, err := openSecret(r.Companion.SealedToken)
	if err != nil {
		return
	}
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	c, _, err := a.findCompanion(dctx, r.HardwareHash, *r.Companion, string(tok))
	dcancel()
	if err != nil || c == nil {
		return
	}
	sctx, scancel := context.WithTimeout(ctx, 3*time.Second)
	st, err := c.GetStatus(sctx)
	_ = c.Close()
	scancel()
	if err != nil {
		return
	}
	a.update(func() {
		if a.peerStatus == nil {
			a.peerStatus = make(map[string]companion.DeviceStatus)
		}
		a.peerStatus[r.HardwareHash] = st
		if a.compHash == r.HardwareHash {
			a.compStatus = st
		}
	})
	a.ensurePhoneReady(ctx, r, st)
}

func (a *App) ensurePhoneReady(ctx context.Context, r RememberedDevice, st companion.DeviceStatus) {
	if st.DaemonReady && len(a.appsForDevice(r.HardwareHash)) == 0 {
		go a.fetchCompanionApps(r.HardwareHash)
	}
	apkPath, localHash := a.localCompanionAPK()
	onlineADB := a.hasOnlineADB(r.HardwareHash)
	needsAPK, needsDaemon, wantsADB := phoneNeedsAction(st, localHash, onlineADB, a.allowsWirelessADB())
	if apkPath == "" {
		needsAPK = false
	}
	if !needsAPK && !needsDaemon && !wantsADB {
		return
	}
	if !onlineADB && st.NetworkType != "wifi" {
		return
	}
	if !a.maintDue(r.HardwareHash, 45*time.Second) {
		return
	}
	go func() {
		mctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		client, _, err := a.ensureADBAndLeases()
		if err != nil {
			return
		}
		cand, ok := a.discoverAndConnectADB(mctx, r.HardwareHash, true)
		if !ok {
			return
		}
		if needsAPK {
			a.updateCompanion(mctx, client, r.HardwareHash, cand.Serial, r.displayName(), apkPath)
		}
		if needsDaemon || needsAPK {
			a.setCardWork(r.HardwareHash, cardWork{Busy: L.CardActivatingEnhanced(r.displayName())})
			dErr := dex.StartDaemon(mctx, client, cand.Serial, companionPackage)
			if dErr != nil {
				a.setCardWork(r.HardwareHash, cardWork{Note: a.failed(L.FailCompanion(), dErr)})
			} else {
				a.setCardWork(r.HardwareHash, cardWork{})
			}
		}
		if !a.allowsWirelessADB() && strings.Contains(cand.Serial, ":") && (needsAPK || needsDaemon) {
			dctx, dcancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = client.Disconnect(dctx, cand.Serial)
			dcancel()
			return
		}
		a.RefreshDevices()
	}()
}
