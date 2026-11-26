package gui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
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
		c, addr, res, err := pairOverADB(ctx, client, serial, apk, note)
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
		a.update(func() {
			a.compConnected = true
			a.compStatus = st
			a.compAddr = addr
			a.compToken = res.Token
			a.compNotice = L.CompanionPaired(model)
		})

		a.mu.Lock()
		cfg := a.rt.meshCfg
		hash := ""
		for _, d := range a.devices {
			for _, tr := range d.Transports {
				if tr.Serial == serial {
					hash = d.HardwareHash
				}
			}
		}
		a.mu.Unlock()
		if hash != "" && res.Port > 0 {
			a.bindCompanion(hash, res.Port, res.CertFingerprint, res.Token, res.Model)
		}
		if !joinMesh || cfg.NetworkName == "" {
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
			note(L.CompanionMeshJoined(model, cfg.NetworkName))
		}
	}()
}

// pairOverADB installs apk on serial, starts its Companion service with the secret-free
// `androfuse://pair` link, forwards its local socket and requests pairing with a fresh code
// (reported through progress), retrying while the phone's service starts.
// Returns: the authenticated client, its forwarded loopback address and the pairing result.
// Side effects: `adb install -r`, `am start`, an `adb forward` kept for the session.
func pairOverADB(ctx context.Context, client *adb.Client, serial, apk string, progress func(string)) (*companion.Client, string, companion.PairResponse, error) {
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
		addr, err := a.companionRoute(ctx, hash, b)
		if err != nil {
			note(err.Error())
			return
		}
		var c *companion.Client
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Second) {
			dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
			c, err = companion.Dial(dctx, addr, string(tok), b.Fingerprint)
			dcancel()
			// A new mesh forward needs a moment while the instance reconnects.
			if err == nil || time.Now().After(deadline) {
				break
			}
		}
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
		a.update(func() {
			a.compConnected = true
			a.compStatus = st
			a.compAddr = addr
			a.compToken = string(tok)
			a.compNotice = L.CompanionPaired(r.Name)
		})
	}()
}

// companionRoute returns a loopback address that reaches b's Companion: an adb forward of its
// local socket when the phone is on adb, else a mesh port forward to its mesh IPv4.
func (a *App) companionRoute(ctx context.Context, hash string, b CompanionBinding) (string, error) {
	a.mu.Lock()
	serial := ""
	for _, d := range a.devices {
		if d.HardwareHash == hash && d.Target.State == "device" {
			serial = d.Target.Serial
		}
	}
	cfg := a.rt.meshCfg
	a.mu.Unlock()
	if serial != "" {
		client, _, err := a.ensureADBAndLeases()
		if err != nil {
			return "", err
		}
		port, err := client.ForwardPort(ctx, serial, 0, "localabstract:"+companion.LocalSocket)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("127.0.0.1:%d", port), nil
	}

	a.rt.coreMu.Lock()
	mgr := a.rt.meshMgr
	a.rt.coreMu.Unlock()
	if mgr == nil || cfg.NetworkName == "" || b.MeshHost == "" {
		return "", errors.New(L.CompanionNoRoute())
	}
	ip, ok := mgr.PeerIPv4(b.MeshHost)
	if !ok {
		return "", errors.New(L.CompanionNoRoute())
	}
	for _, pf := range cfg.PortForwards {
		if pf.RemoteHost == ip && pf.RemotePort == b.Port {
			return fmt.Sprintf("127.0.0.1:%d", pf.LocalPort), nil
		}
	}
	local, err := companion.PickPort(0)
	if err != nil {
		return "", err
	}
	cfg.PortForwards = append(cfg.PortForwards, mesh.PortForward{LocalPort: local, RemoteHost: ip, RemotePort: b.Port})
	if err := mgr.Start(cfg); err != nil {
		return "", err
	}
	a.mu.Lock()
	a.rt.meshCfg = cfg
	a.meshStatus = mgr.Status()
	a.mu.Unlock()
	return fmt.Sprintf("127.0.0.1:%d", local), nil
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
