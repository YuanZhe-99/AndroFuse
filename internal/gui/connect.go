package gui

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// connectPhase is where Home's "Connect a phone" stands.
type connectPhase int

const (
	connectIdle     connectPhase = iota
	connectLooking               // finding the phone's port (progress)
	connectNeedPair              // Wireless debugging answers but this computer is not paired yet
	connectNoAnswer              // nothing answered; the user may type the port shown on the phone
)

// Open-as kinds of Home's "Connect a phone" and the + picker.
const (
	openAsDeX = iota
	openAsScreen
	openAsApp
	openAsCamera
)

// connectFlow is the state of Home's "Connect a phone" card.
type connectFlow struct {
	Input      string // what the user typed: an IP address, a domain or a phone's name
	OpenAs     int
	Phase      connectPhase
	Progress   float64
	Host       string
	Port       int // the Wireless debugging port found, 0 when none
	PairCode   string
	PairPort   string
	ManualPort string
	Notice     string
	cancel     context.CancelFunc
}

// splitAddress splits what the user typed into a host and an optional port.
// Returns: port 0 when none was typed; ok false when there is no usable host.
func splitAddress(s string) (host string, port int, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, false
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", 0, false
		}
		return h, n, h != ""
	}
	return strings.Trim(s, "[]"), 0, true
}

// looksLikeAddress reports whether s is an IP address or a domain rather than a phone's name.
func looksLikeAddress(s string) bool {
	s = strings.TrimSpace(s)
	if _, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
		return true
	}
	if _, _, err := net.SplitHostPort(s); err == nil {
		return true
	}
	return strings.Contains(s, ".") && !strings.Contains(s, " ")
}

// validPort parses a port the user typed (1024–65535).
func validPort(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return n, err == nil && n >= 1024 && n <= 65535
}

// ConnectByAddress runs Home's "Connect a phone": a phone's name opens it; an address with a port
// connects to it; an address alone is looked up — a remembered phone's last port, then its
// Companion (which knows the Wireless debugging port), then a search of the usual port range —
// and only when all of that fails does the card ask for the port shown on the phone.
// Side effects: adb connect; progress and outcome land in a.connect; the phone opens as the card's
// "Open as" once it is online.
func (a *App) ConnectByAddress() {
	a.mu.Lock()
	in, openAs := a.connect.Input, a.connect.OpenAs
	if a.connect.cancel != nil {
		a.connect.cancel()
	}
	a.mu.Unlock()
	in = strings.TrimSpace(in)
	if in == "" {
		return
	}
	if d, ok := a.onlineByName(in); ok {
		a.openAs(d, openAs)
		a.resetConnect()
		return
	}
	if !looksLikeAddress(in) {
		if r, ok := a.rememberedByName(in); ok {
			a.connectRemembered(r, openAs)
			return
		}
		a.update(func() { a.connect.Notice = L.ConnectNoSuchPhone(in) })
		return
	}
	host, port, ok := splitAddress(in)
	if !ok {
		a.update(func() { a.connect.Notice = L.ConnectBadAddress() })
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	a.update(func() {
		a.connect.cancel = cancel
		a.connect.Phase, a.connect.Progress, a.connect.Host, a.connect.Port, a.connect.Notice = connectLooking, 0, host, 0, ""
	})
	go func() {
		defer cancel()
		serial, needPair, err := a.findAndConnect(ctx, host, port)
		switch {
		case ctx.Err() != nil && serial == "":
			a.resetConnect()
		case serial != "":
			a.afterConnect(serial, openAs)
		case needPair:
			a.update(func() { a.connect.Phase = connectNeedPair })
		default:
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			a.update(func() { a.connect.Phase, a.connect.Notice = connectNoAnswer, msg })
		}
	}()
}

// findAndConnect connects adb to the phone at host, finding its port when port is 0.
// Returns: the adb serial on success; needPair when Wireless debugging answers but refused this
// computer (pair first); the last error otherwise.
func (a *App) findAndConnect(ctx context.Context, host string, port int) (string, bool, error) {
	client, _, err := a.ensureADBAndLeases()
	if err != nil {
		return "", false, err
	}
	try := func(p int) (string, error) {
		addr := net.JoinHostPort(host, strconv.Itoa(p))
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := client.ConnectTLS(cctx, addr, false); err != nil {
			return "", err
		}
		return addr, nil
	}
	if port > 0 {
		s, err := try(port)
		if err == nil {
			return s, false, nil
		}
		if ans, _ := adb.ProbeADB(ctx, net.JoinHostPort(host, strconv.Itoa(port))); ans == adb.AnswerTLS {
			a.update(func() { a.connect.Port = port })
			return "", true, err
		}
		return "", false, err
	}

	// 1. A remembered phone last reached at this address.
	for _, r := range a.rememberedList() {
		if h, p, err := net.SplitHostPort(r.Endpoint); err == nil && strings.EqualFold(h, host) {
			if n, _ := strconv.Atoi(p); n > 0 {
				if s, err := try(n); err == nil {
					return s, false, nil
				}
			}
		}
	}
	// 2. A paired Companion at this address, or the phone holding this address in a network.
	if r, ok := a.rememberedAtAddress(host); ok && r.Companion != nil {
		if s, err := a.adbOverCompanion(ctx, r.HardwareHash); err == nil {
			return s, false, nil
		}
	}
	// 3. The usual Wireless debugging range, on addresses this computer can route to.
	if a.isMeshAddress(host) {
		return "", false, errors.New(L.ConnectMeshNeedsCompanion())
	}
	found := adb.FindWirelessPort(ctx, host, func(p float64) {
		a.update(func() { a.connect.Progress = p })
	})
	if found == 0 {
		return "", false, nil
	}
	a.update(func() { a.connect.Port = found })
	s, err := try(found)
	if err != nil {
		return "", true, err
	}
	return s, false, nil
}

// PairAndConnect pairs with the code and pairing port the phone shows, then connects to the
// Wireless debugging port found earlier (or searches again).
func (a *App) PairAndConnect() {
	a.mu.Lock()
	f := a.connect
	a.mu.Unlock()
	pp, okPort := validPort(f.PairPort)
	code := strings.ReplaceAll(strings.TrimSpace(f.PairCode), " ", "")
	if !okPort || !validPairCode(code) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	a.update(func() {
		a.connect.cancel = cancel
		a.connect.Phase, a.connect.Progress, a.connect.Notice = connectLooking, 0, ""
	})
	go func() {
		defer cancel()
		client, _, err := a.ensureADBAndLeases()
		if err == nil {
			pctx, pcancel := context.WithTimeout(ctx, 15*time.Second)
			_, err = client.PairTLS(pctx, net.JoinHostPort(f.Host, strconv.Itoa(pp)), code)
			pcancel()
		}
		if err != nil {
			a.update(func() { a.connect.Phase, a.connect.Notice = connectNeedPair, err.Error() })
			return
		}
		serial, needPair, err := a.findAndConnect(ctx, f.Host, f.Port)
		switch {
		case serial != "":
			a.afterConnect(serial, f.OpenAs)
		case needPair:
			a.update(func() { a.connect.Phase = connectNeedPair })
		default:
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			a.update(func() { a.connect.Phase, a.connect.Notice = connectNoAnswer, msg })
		}
	}()
}

// ConnectManualPort connects to the port the user read off the phone's Wireless debugging page.
func (a *App) ConnectManualPort() {
	a.mu.Lock()
	f := a.connect
	a.mu.Unlock()
	p, ok := validPort(f.ManualPort)
	if !ok {
		return
	}
	a.update(func() { a.connect.Phase, a.connect.Notice = connectLooking, "" })
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		serial, needPair, err := a.findAndConnect(ctx, f.Host, p)
		switch {
		case serial != "":
			a.afterConnect(serial, f.OpenAs)
		case needPair:
			a.update(func() { a.connect.Phase = connectNeedPair })
		default:
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			a.update(func() { a.connect.Phase, a.connect.Notice = connectNoAnswer, msg })
		}
	}()
}

// CancelConnect stops a running search and resets the card.
func (a *App) CancelConnect() {
	a.mu.Lock()
	c := a.connect.cancel
	a.mu.Unlock()
	if c != nil {
		c()
	}
	a.resetConnect()
}

func (a *App) resetConnect() {
	a.update(func() {
		in, openAs := a.connect.Input, a.connect.OpenAs
		a.connect = connectFlow{Input: in, OpenAs: openAs}
	})
}

// afterConnect clears the card and opens the phone at serial as openAs once a scan shows it.
func (a *App) afterConnect(serial string, openAs int) {
	a.update(func() {
		a.connect = connectFlow{OpenAs: openAs}
		a.pendingOpen = &pendingOpen{Serial: serial, OpenAs: openAs}
	})
	a.RefreshDevices()
}

// pendingOpen is a phone to open as soon as a device scan lists it.
type pendingOpen struct {
	Serial string
	OpenAs int
}

// openPending opens the pending phone when devs list it; called after each scan.
func (a *App) openPending(devs []DiscoveredDevice) {
	a.mu.Lock()
	p := a.pendingOpen
	a.mu.Unlock()
	if p == nil {
		return
	}
	for _, d := range devs {
		for _, tr := range d.Transports {
			if tr.Serial == p.Serial && tr.State == "device" {
				a.mu.Lock()
				a.pendingOpen = nil
				a.mu.Unlock()
				a.openAs(d, p.OpenAs)
				return
			}
		}
	}
}

// openAs opens d as kind (an openAs* constant), falling back to the phone screen when the phone
// cannot do it (Samsung DeX on another maker).
func (a *App) openAs(d DiscoveredDevice, kind int) {
	switch kind {
	case openAsDeX:
		if genuineDeX(d) {
			a.LaunchDeX(d, false, OpenDefault)
			return
		}
		a.LaunchMirror(d, OpenDefault)
	case openAsApp:
		a.openPicker(openAsApp, d.HardwareHash)
	case openAsCamera:
		a.LaunchPhoneCamera(d, a.cameraFacing(), OpenDefault)
	default:
		a.LaunchMirror(d, OpenDefault)
	}
}

// onlineByName finds an online device by the name the interface shows for it.
func (a *App) onlineByName(name string) (DiscoveredDevice, bool) {
	a.mu.Lock()
	devs := append([]DiscoveredDevice(nil), a.devices...)
	a.mu.Unlock()
	for _, d := range devs {
		if d.Target.State == "device" && strings.EqualFold(a.phoneName(d), name) {
			return d, true
		}
	}
	return DiscoveredDevice{}, false
}

func (a *App) rememberedList() []RememberedDevice {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]RememberedDevice(nil), a.remembered...)
}

// rememberedByName finds a remembered phone by the name the interface shows for it.
func (a *App) rememberedByName(name string) (RememberedDevice, bool) {
	for _, r := range a.rememberedList() {
		if strings.EqualFold(r.displayName(), name) {
			return r, true
		}
	}
	return RememberedDevice{}, false
}

// rememberedAtAddress finds the remembered phone last reached at host, or the one whose mesh name
// holds host in a running network.
func (a *App) rememberedAtAddress(host string) (RememberedDevice, bool) {
	list := a.rememberedList()
	for _, r := range list {
		if h, _, err := net.SplitHostPort(r.Endpoint); err == nil && strings.EqualFold(h, host) {
			return r, true
		}
	}
	for _, r := range list {
		if r.Companion == nil || r.Companion.MeshHost == "" {
			continue
		}
		if _, ip, ok := a.meshPeerIPv4(r.Companion.MeshHost); ok && ip == host {
			return r, true
		}
	}
	return RememberedDevice{}, false
}

// isMeshAddress reports whether host is a peer address in one of the running networks, which this
// computer reaches only through port forwards (no virtual adapter, `INV-07`).
func (a *App) isMeshAddress(host string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, v := range a.netViews {
		for _, p := range v.Info.Peers {
			if p.IPv4 == host {
				return true
			}
		}
	}
	return false
}

// connectRemembered reconnects an offline remembered phone: its last address first, then through
// its Companion (on adb, the LAN or a network).
func (a *App) connectRemembered(r RememberedDevice, openAs int) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	a.update(func() {
		a.connect.cancel = cancel
		a.connect.Phase, a.connect.Progress, a.connect.Notice = connectLooking, 0, ""
	})
	go func() {
		defer cancel()
		client, _, err := a.ensureADBAndLeases()
		if err == nil && r.Endpoint != "" {
			cctx, ccancel := context.WithTimeout(ctx, 10*time.Second)
			_, err = client.ConnectTLS(cctx, r.Endpoint, false)
			ccancel()
			if err == nil {
				a.afterConnect(r.Endpoint, openAs)
				return
			}
		}
		if r.Companion != nil {
			var serial string
			if serial, err = a.adbOverCompanion(ctx, r.HardwareHash); err == nil {
				a.afterConnect(serial, openAs)
				return
			}
		}
		msg := L.ConnectOfflinePhone(r.displayName())
		if err != nil {
			msg = err.Error()
		}
		a.update(func() { a.connect.Phase, a.connect.Notice = connectIdle, msg })
	}()
}

// adbOverCompanion connects adb to the remembered phone hash through its Companion: the Companion
// reports its Wireless debugging port and joins a loopback listener here to it over its own
// authenticated TLS link (companion.ServeTunnels), so it works on the LAN and across networks
// without a port forward per port (`INV-07`: only that port, only after it answers as Wireless
// debugging on the phone).
// Returns: the adb serial (127.0.0.1:<port>).
// Side effects: keeps the tunnel listener until AndroFuse quits or the phone is forgotten.
func (a *App) adbOverCompanion(ctx context.Context, hash string) (string, error) {
	r, ok := a.RememberedFor(hash)
	if !ok || r.Companion == nil {
		return "", errors.New(L.CompanionNotPaired())
	}
	b := *r.Companion
	tok, err := openSecret(b.SealedToken)
	if err != nil {
		return "", err
	}
	addr, err := a.companionRoute(ctx, hash, b)
	if err != nil {
		return "", err
	}
	var c *companion.Client
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Second) {
		dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		c, err = companion.Dial(dctx, addr, string(tok), b.Fingerprint)
		cancel()
		if err == nil || time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		return "", err
	}
	st, err := c.GetStatus(ctx)
	_ = c.Close()
	if err != nil {
		return "", err
	}
	if st.WirelessADBPort <= 0 {
		return "", errors.New(L.ConnectWirelessOff(r.displayName()))
	}
	a.rt.coreMu.Lock()
	if stop := a.rt.tunnels[hash]; stop != nil {
		stop()
	}
	tctx, stop := context.WithCancel(context.Background())
	if a.rt.tunnels == nil {
		a.rt.tunnels = map[string]context.CancelFunc{}
	}
	a.rt.tunnels[hash] = stop
	a.rt.coreMu.Unlock()
	local, err := companion.ServeTunnels(tctx, addr, string(tok), b.Fingerprint, st.WirelessADBPort)
	if err != nil {
		stop()
		return "", err
	}
	client, _, err := a.ensureADBAndLeases()
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := client.ConnectTLS(cctx, local, false); err != nil {
		return "", err
	}
	return local, nil
}

// stopTunnels closes every Wireless debugging tunnel (at quit).
func (a *App) stopTunnels() {
	a.rt.coreMu.Lock()
	defer a.rt.coreMu.Unlock()
	for h, stop := range a.rt.tunnels {
		stop()
		delete(a.rt.tunnels, h)
	}
}
