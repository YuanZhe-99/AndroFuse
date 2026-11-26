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

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
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

// companionRoute is one loopback address that reaches a phone's Companion, and the Companion
// port behind it (0 for adb's local socket, which has none).
type companionRoute struct {
	addr string
	port int
}

// companionRoutes returns loopback addresses that may reach b's Companion: an adb forward of its
// local socket when the phone is on adb, else mesh port forwards to its mesh IPv4 at the saved
// port and at the ports its device id gives (companion.DevicePorts), added in one restart.
func (a *App) companionRoutes(ctx context.Context, hash string, b CompanionBinding) ([]companionRoute, error) {
	a.mu.Lock()
	serial := ""
	for _, d := range a.devices {
		if d.HardwareHash == hash && d.Target.State == "device" {
			serial = d.Target.Serial
		}
	}
	a.mu.Unlock()
	if serial != "" {
		client, _, err := a.ensureADBAndLeases()
		if err != nil {
			return nil, err
		}
		port, err := client.ForwardPort(ctx, serial, 0, "localabstract:"+companion.LocalSocket)
		if err != nil {
			return nil, err
		}
		return []companionRoute{{addr: fmt.Sprintf("127.0.0.1:%d", port)}}, nil
	}

	// Right after a network starts or restarts, its routes take a moment to list the phone again.
	id, ip, ok := a.meshPeerIPv4(b.MeshHost)
	for deadline := time.Now().Add(10 * time.Second); !ok && time.Now().Before(deadline) && ctx.Err() == nil; {
		time.Sleep(500 * time.Millisecond)
		id, ip, ok = a.meshPeerIPv4(b.MeshHost)
	}
	if !ok {
		return nil, errors.New(L.CompanionNoRoute())
	}
	ports := []int{}
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
	locals, err := a.meshForwards(id, ip, ports)
	if err != nil {
		return nil, err
	}
	out := make([]companionRoute, len(ports))
	for i, p := range ports {
		out[i] = companionRoute{addr: fmt.Sprintf("127.0.0.1:%d", locals[i]), port: p}
	}
	return out, nil
}

// findCompanion connects to hash's Companion over the first of companionRoutes that answers,
// trying again for up to 30 s while a new mesh forward settles.
// Returns: the connected client and the address that reached it.
// Side effects: saves the port that answered when it is not the saved one, and the hostname the
// Companion reported (noteMeshHost).
func (a *App) findCompanion(ctx context.Context, hash string, b CompanionBinding, tok string) (*companion.Client, string, error) {
	routes, err := a.companionRoutes(ctx, hash, b)
	if err != nil {
		return nil, "", err
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Second) {
		for _, r := range routes {
			dctx, cancel := context.WithTimeout(ctx, 4*time.Second)
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
			err = dErr
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
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
	if !inNet {
		return
	}
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
	if err != nil || (st.MeshRunning && st.MeshNetwork == cfg.NetworkName) {
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
