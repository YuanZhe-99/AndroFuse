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

var errMeshNeedsCompanion = errors.New("mesh needs companion")

// connectFailure formats a connection error into plain interface wording (`INV-08`).
func (a *App) connectFailure(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errMeshNeedsCompanion) {
		return L.ConnectMeshNeedsCompanion()
	}
	return a.failed(L.FailConnect(), err)
}

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

// unpairedMeshPhoneByQuery matches an unpaired phone seen in a running EasyTier network by IPv4 or name.
func (a *App) unpairedMeshPhoneByQuery(q string) (networkPhone, bool) {
	q = strings.TrimSpace(q)
	if q == "" {
		return networkPhone{}, false
	}
	for _, p := range a.othersInNetworks() {
		if (p.IPv4 != "" && p.IPv4 == q) || strings.EqualFold(p.Name, q) {
			return p, true
		}
	}
	return networkPhone{}, false
}

// ConnectByAddress runs Home's / Advanced wizard's address connect: a phone's name opens it,
// an unpaired EasyTier mesh phone triggers PairMeshPhone directly (without requiring local ADB),
// a paired mesh phone opens via its Companion, and other addresses resolve wireless ADB ports.
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
	if p, ok := a.unpairedMeshPhoneByQuery(in); ok {
		a.CloseAddWizard()
		a.PairMeshPhone(p)
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
	if port == 0 && a.isMeshAddress(host) {
		if r, ok := a.rememberedAtAddress(host); ok && r.Companion != nil {
			a.CloseAddWizard()
			a.connectRemembered(r, openAs)
			a.resetConnect()
			return
		}
		if p, ok := a.unpairedMeshPhoneByQuery(host); ok {
			a.CloseAddWizard()
			a.PairMeshPhone(p)
			a.resetConnect()
			return
		}
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
			a.update(func() { a.connect.Phase, a.connect.Notice = connectNoAnswer, a.connectFailure(err) })
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
		return "", false, errMeshNeedsCompanion
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
			a.update(func() { a.connect.Phase, a.connect.Notice = connectNeedPair, a.failed(L.FailPair(), err) })
			return
		}
		serial, needPair, err := a.findAndConnect(ctx, f.Host, f.Port)
		switch {
		case serial != "":
			a.afterConnect(serial, f.OpenAs)
		case needPair:
			a.update(func() { a.connect.Phase = connectNeedPair })
		default:
			a.update(func() { a.connect.Phase, a.connect.Notice = connectNoAnswer, a.connectFailure(err) })
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
			a.update(func() { a.connect.Phase, a.connect.Notice = connectNoAnswer, a.connectFailure(err) })
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

// connectRemembered reconnects an offline remembered phone according to Prefs.ConnectionMode:
// when Companion is preferred (companion_only / both_companion), it launches through Companion
// directly; when ADB is preferred (both_adb), it tries wireless ADB first and automatically falls
// back to Companion when auto-switch is enabled.
func (a *App) connectRemembered(r RememberedDevice, openAs int) {
	if a.prefersCompanion() && r.Companion != nil {
		switch openAs {
		case openAsApp:
			a.openPicker(openAsApp, r.HardwareHash)
		case openAsDeX:
			a.LaunchDaemonDesktop(r.HardwareHash, OpenDefault)
		default:
			a.LaunchShare(r.HardwareHash, OpenDefault)
		}
		return
	}
	if !a.allowsWirelessADB() {
		a.setCardWork(r.HardwareHash, cardWork{Note: L.CardCantReachShare(r.displayName())})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	a.update(func() { a.connect.cancel = cancel })
	a.setCardWork(r.HardwareHash, cardWork{Busy: L.CardConnecting(r.displayName())})
	go func() {
		defer cancel()
		client, _, err := a.ensureADBAndLeases()
		if err == nil && r.Endpoint != "" {
			cctx, ccancel := context.WithTimeout(ctx, 3*time.Second)
			_, err = client.ConnectTLS(cctx, r.Endpoint, false)
			ccancel()
			if err == nil {
				a.setCardWork(r.HardwareHash, cardWork{})
				a.afterConnect(r.Endpoint, openAs)
				return
			}
		}
		if r.Companion != nil {
			tctx, tcancel := context.WithTimeout(ctx, 10*time.Second)
			serial, cErr := a.adbOverCompanion(tctx, r.HardwareHash)
			tcancel()
			if cErr == nil {
				a.setCardWork(r.HardwareHash, cardWork{})
				a.afterConnect(serial, openAs)
				return
			}
			if a.autoSwitchOn() {
				// Wireless ADB failed or is disabled (e.g. switched to 5G); fall back seamlessly to
				// Companion streaming (daemon DeX/desktop, independent app, or mirror/share).
				a.setCardWork(r.HardwareHash, cardWork{Busy: L.CardSwitchingCompanion(r.displayName())})
				switch openAs {
				case openAsApp:
					a.setCardWork(r.HardwareHash, cardWork{})
					a.openPicker(openAsApp, r.HardwareHash)
				case openAsDeX:
					a.LaunchDaemonDesktop(r.HardwareHash, OpenDefault)
				default:
					a.LaunchShare(r.HardwareHash, OpenDefault)
				}
				return
			}
			err = cErr
		}
		plain := L.CardCantReach(r.displayName())
		if err == nil {
			err = errors.New("no Wireless debugging address and no Companion")
		}
		a.setCardWork(r.HardwareHash, cardWork{Note: a.failed(plain, err)})
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
	c, addr, err := a.findCompanion(ctx, hash, b, string(tok))
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
	client, _, err := a.ensureADBAndLeases()
	if err != nil {
		return "", err
	}
	// The port changes each time Wireless debugging turns on, so the remembered address is often
	// stale while the phone's Wi-Fi address may not be: when the phone is on Wi-Fi, try its
	// reported WifiIPv4 (and remembered host) with the current port first.
	if st.NetworkType == "wifi" {
		var wifiHosts []string
		if st.WifiIPv4 != "" && !a.isMeshAddress(st.WifiIPv4) && !strings.HasPrefix(st.WifiIPv4, "127.") {
			wifiHosts = append(wifiHosts, st.WifiIPv4)
		}
		if host, _, err := net.SplitHostPort(r.Endpoint); err == nil && host != "" && !strings.HasPrefix(host, "127.") && !a.isMeshAddress(host) {
			if len(wifiHosts) == 0 || !strings.EqualFold(wifiHosts[0], host) {
				wifiHosts = append(wifiHosts, host)
			}
		}
		for _, host := range wifiHosts {
			direct := net.JoinHostPort(host, strconv.Itoa(st.WirelessADBPort))
			dctx, dcancel := context.WithTimeout(ctx, 4*time.Second)
			if _, err := client.ConnectTLS(dctx, direct, false); err == nil {
				if prof, err := client.ProbeDevice(dctx, direct); err == nil && (prof.HardwareHash == hash || prof.DeviceHash == hash) {
					dcancel()
					a.noteWirelessEndpoint(hash, direct)
					return direct, nil
				}
				_ = client.Disconnect(dctx, direct)
			}
			dcancel()
		}
	}
	a.rt.coreMu.Lock()
	if stop := a.rt.tunnels[hash]; stop != nil {
		stop()
		// Close the replaced tunnel's adb connection too, or adb lists the phone twice.
		if old := a.rt.tunnelAddrs[hash]; old != "" {
			go func() {
				dctx, dcancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer dcancel()
				_ = client.Disconnect(dctx, old)
			}()
		}
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
	a.rt.coreMu.Lock()
	if a.rt.tunnelAddrs == nil {
		a.rt.tunnelAddrs = map[string]string{}
	}
	a.rt.tunnelAddrs[hash] = local
	a.rt.coreMu.Unlock()
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

// discoverAndConnectADB locates or re-establishes a healthy ADB transport for hardwareHash even
// when Wireless Debugging changed ports across Wi-Fi/5G switches or routed subnets.
// Order: (1) verify existing online ADB transport, (2) same-LAN mDNS, (3) Companion `status.get`
// (`wifi_ipv4` + `wireless_adb_port`) or rate-limited `adb.FindWirelessPort` scan when on Wi-Fi,
// (4) Companion ADB tunnel fallback, or (5) last remembered endpoint when Companion is offline.
func (a *App) discoverAndConnectADB(ctx context.Context, hash string, allowPortScan bool) (adb.Device, bool) {
	if hash == "" {
		return adb.Device{}, false
	}
	client, _, err := a.ensureADBAndLeases()
	if err != nil {
		return adb.Device{}, false
	}
	if cand, ok := a.findLiveTransportFor(ctx, client, hash, ""); ok {
		if cand.Transport == adb.TransportUSB || cand.Transport == adb.TransportEmulator {
			return cand, true
		}
		pctx, pcancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		_, sErr := client.Shell(pctx, cand.Serial, "echo")
		pcancel()
		if sErr == nil {
			a.noteWirelessEndpoint(hash, cand.Serial)
			return cand, true
		}
		dctx, dcancel := context.WithTimeout(ctx, 2*time.Second)
		_ = client.Disconnect(dctx, cand.Serial)
		dcancel()
	}
	if a.connectWirelessFor(ctx, client, hash) {
		if cand, ok := a.findLiveTransportFor(ctx, client, hash, ""); ok {
			a.noteWirelessEndpoint(hash, cand.Serial)
			return cand, true
		}
	}

	r, hasRem := a.RememberedFor(hash)
	if !hasRem {
		return adb.Device{}, false
	}

	tryVerifiedTLS := func(addr string, wait time.Duration) (adb.Device, bool) {
		dctx, dcancel := context.WithTimeout(ctx, wait)
		defer dcancel()
		if _, err := client.ConnectTLS(dctx, addr, false); err != nil {
			return adb.Device{}, false
		}
		if prof, pErr := client.ProbeDevice(dctx, addr); pErr == nil && (prof.HardwareHash == hash || prof.DeviceHash == hash) {
			a.noteWirelessEndpoint(hash, addr)
			if cand, ok := a.findLiveTransportFor(dctx, client, hash, ""); ok {
				return cand, true
			}
			return adb.Device{Serial: addr, State: "device", Transport: adb.TransportTLSWiFi, DeviceHash: hash}, true
		}
		_ = client.Disconnect(dctx, addr)
		return adb.Device{}, false
	}

	var remHost string
	if h, _, err := net.SplitHostPort(r.Endpoint); err == nil && h != "" && !strings.HasPrefix(h, "127.") && !a.isMeshAddress(h) {
		remHost = h
	}

	usedCompanion := false
	if r.Companion != nil {
		if tok, err := openSecret(r.Companion.SealedToken); err == nil {
			cctx, ccancel := context.WithTimeout(ctx, 4*time.Second)
			c, _, cerr := a.findCompanion(cctx, hash, *r.Companion, string(tok))
			ccancel()
			if cerr == nil && c != nil {
				sctx, scancel := context.WithTimeout(ctx, 3*time.Second)
				st, stErr := c.GetStatus(sctx)
				_ = c.Close()
				scancel()
				if stErr == nil {
					usedCompanion = true
					if st.NetworkType == "wifi" {
						var hosts []string
						if st.WifiIPv4 != "" && !a.isMeshAddress(st.WifiIPv4) && !strings.HasPrefix(st.WifiIPv4, "127.") {
							hosts = append(hosts, st.WifiIPv4)
						}
						if remHost != "" && (len(hosts) == 0 || !strings.EqualFold(hosts[0], remHost)) {
							hosts = append(hosts, remHost)
						}
						if st.WirelessADBPort > 0 {
							for _, h := range hosts {
								if cand, ok := tryVerifiedTLS(net.JoinHostPort(h, strconv.Itoa(st.WirelessADBPort)), 3*time.Second); ok {
									return cand, true
								}
							}
						}
						if allowPortScan && len(hosts) > 0 && a.portScanDue(hash) {
							scanCtx, scanCancel := context.WithTimeout(ctx, 11*time.Second)
							found := adb.FindWirelessPort(scanCtx, hosts[0], nil)
							scanCancel()
							if found > 0 {
								if cand, ok := tryVerifiedTLS(net.JoinHostPort(hosts[0], strconv.Itoa(found)), 3*time.Second); ok {
									return cand, true
								}
							}
						}
					}
					if st.WirelessADBPort > 0 {
						if serial, err := a.adbOverCompanion(ctx, hash); err == nil {
							if cand, ok := a.findLiveTransportFor(ctx, client, hash, ""); ok {
								return cand, true
							}
							if serial != "" {
								return adb.Device{Serial: serial, State: "device", Transport: adb.TransportTLSWiFi, DeviceHash: hash}, true
							}
						}
					}
				}
			}
		}
	}

	if !usedCompanion {
		if r.Endpoint != "" {
			if cand, ok := tryVerifiedTLS(r.Endpoint, 2500*time.Millisecond); ok {
				return cand, true
			}
		}
		if allowPortScan && remHost != "" && a.portScanDue(hash) {
			scanCtx, scanCancel := context.WithTimeout(ctx, 11*time.Second)
			found := adb.FindWirelessPort(scanCtx, remHost, nil)
			scanCancel()
			if found > 0 {
				if cand, ok := tryVerifiedTLS(net.JoinHostPort(remHost, strconv.Itoa(found)), 3*time.Second); ok {
					return cand, true
				}
			}
		}
	}
	return adb.Device{}, false
}
