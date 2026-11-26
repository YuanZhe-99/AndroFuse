package gui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// maxNetworks is how many networks the desktop keeps (one EasyTier instance each, like Kyokai).
const maxNetworks = 8

// MeshNetwork is one EasyTier network kept in gui.json (Settings › Network). The secret is stored
// only sealed with DPAPI for the current Windows user (`INV-08`); Join remembers whether the user
// joined it, which is off until they switch it on (`INV-07`).
type MeshNetwork struct {
	ID           string   `json:"id"` // EasyTier instance name, "net-<hex>"
	Label        string   `json:"label,omitempty"`
	NetworkName  string   `json:"network_name"`
	Servers      []string `json:"servers,omitempty"`
	SealedSecret []byte   `json:"sealed_secret,omitempty"`
	Join         bool     `json:"join"`
	// ListenPort is this network's port for direct connections while Settings › Network accepts
	// them: a random high port chosen the first time and kept while it stays free.
	ListenPort int `json:"listen_port,omitempty"`
}

// shownName is what the interface calls a network.
func (n MeshNetwork) shownName() string {
	if n.Label != "" {
		return n.Label
	}
	return n.NetworkName
}

// MeshPrefs holds the desktop's networks and the settings shared by all of them.
type MeshPrefs struct {
	Networks []MeshNetwork `json:"networks,omitempty"`
	// Accept lets other devices connect directly to every joined network's ListenPort.
	Accept bool `json:"accept,omitempty"`
	// NoDirect never builds direct links (Settings › Network › Try direct connections, off).
	NoDirect     bool `json:"no_direct,omitempty"`
	LatencyFirst bool `json:"latency_first,omitempty"`
	// PairNetwork is the ID of the network a newly paired phone joins ("" for none).
	PairNetwork string `json:"pair_network,omitempty"`
	// DeviceID is the random id in this computer's EasyTier hostname (mesh.Hostname).
	DeviceID string `json:"device_id,omitempty"`

	// Legacy single network (before V3), migrated into Networks on load.
	On           bool   `json:"on,omitempty"`
	NetworkName  string `json:"network_name,omitempty"`
	Peer         string `json:"peer,omitempty"`
	SealedSecret []byte `json:"sealed_secret,omitempty"`
	ListenPort   int    `json:"listen_port,omitempty"`
}

// netView is what the UI shows about one network, refreshed in the background (pollNetworks).
type netView struct {
	Status mesh.Status
	Info   mesh.Info
	Err    string // the last start error in words, when joining failed
}

// newNetworkID returns a fresh EasyTier instance name.
func newNetworkID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "net-" + hex.EncodeToString(b[:])
}

// migrateMeshLocked turns a pre-V3 single network into the first entry of Networks; the caller
// holds a.mu.
func (a *App) migrateMeshLocked() {
	p := a.prefs.Mesh
	if p == nil || p.NetworkName == "" {
		return
	}
	n := MeshNetwork{ID: newNetworkID(), NetworkName: p.NetworkName, SealedSecret: p.SealedSecret, Join: p.On, ListenPort: p.ListenPort}
	if p.Peer != "" {
		n.Servers = []string{p.Peer}
	}
	p.Networks = append(p.Networks, n)
	if p.PairNetwork == "" {
		p.PairNetwork = n.ID
	}
	p.On, p.NetworkName, p.Peer, p.SealedSecret, p.ListenPort = false, "", "", nil, 0
	a.savePrefs()
}

// meshHostname is the EasyTier hostname this computer joins every network under: its kept random
// id and its Windows computer name.
// Side effects: draws and saves DeviceID the first time.
func (a *App) meshHostname() string {
	name, _ := os.Hostname()
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.meshPrefsLocked()
	if p.DeviceID == "" {
		p.DeviceID = mesh.NewDeviceID()
		a.savePrefs()
	}
	return mesh.Hostname(mesh.KindPC, p.DeviceID, name)
}

// meshPrefsLocked returns the network settings, creating them; the caller holds a.mu.
func (a *App) meshPrefsLocked() *MeshPrefs {
	if a.prefs.Mesh == nil {
		a.prefs.Mesh = &MeshPrefs{}
	}
	return a.prefs.Mesh
}

// networks returns a copy of the kept networks.
func (a *App) networks() []MeshNetwork {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.prefs.Mesh == nil {
		return nil
	}
	return slices.Clone(a.prefs.Mesh.Networks)
}

// network returns the kept network id.
func (a *App) network(id string) (MeshNetwork, bool) {
	for _, n := range a.networks() {
		if n.ID == id {
			return n, true
		}
	}
	return MeshNetwork{}, false
}

// ensureMeshMgr returns the mesh manager, loading the core on first use.
func (a *App) ensureMeshMgr() (*mesh.Manager, error) {
	a.rt.coreMu.Lock()
	defer a.rt.coreMu.Unlock()
	lib, err := a.ensureCoreLocked()
	if err != nil {
		return nil, err
	}
	if a.rt.meshMgr == nil {
		a.rt.meshMgr = mesh.NewManager(lib)
		a.rt.meshCfgs = map[string]mesh.Config{}
	}
	return a.rt.meshMgr, nil
}

// JoinNetwork starts the network id (the Join switch) and remembers it as joined.
// Side effects: opens outbound connections to its servers and, while Settings › Network accepts
// direct connections, TCP and UDP listeners on its random high port (see companion.PickPort).
func (a *App) JoinNetwork(id string) {
	n, ok := a.network(id)
	if !ok {
		return
	}
	mgr, err := a.ensureMeshMgr()
	if err != nil {
		a.setNetErr(id, err.Error())
		return
	}
	secret := ""
	if len(n.SealedSecret) > 0 {
		b, err := openSecret(n.SealedSecret)
		if err != nil {
			a.setNetErr(id, err.Error())
			return
		}
		secret = string(b)
	}
	a.mu.Lock()
	p := *a.meshPrefsLocked()
	a.mu.Unlock()
	cfg := mesh.Config{Instance: n.ID, Enabled: true, NetworkName: n.NetworkName, NetworkSecret: secret, DeviceName: a.meshHostname(),
		Peers: n.Servers, DisableP2P: p.NoDirect, LatencyFirst: p.LatencyFirst}
	if p.Accept {
		port, err := companion.PickPort(n.ListenPort)
		if err != nil {
			a.setNetErr(id, err.Error())
			return
		}
		n.ListenPort = port
		cfg.Listeners = []string{fmt.Sprintf("tcp://0.0.0.0:%d", port), fmt.Sprintf("udp://0.0.0.0:%d", port)}
	}
	a.rt.coreMu.Lock()
	if old, ok := a.rt.meshCfgs[id]; ok {
		cfg.PortForwards = old.PortForwards // keep the Companion forwards across a restart
	}
	a.rt.coreMu.Unlock()
	err = mgr.Start(cfg)
	a.update(func() {
		if i := a.netIndexLocked(id); i >= 0 {
			a.prefs.Mesh.Networks[i].Join = true
			a.prefs.Mesh.Networks[i].ListenPort = n.ListenPort
			a.savePrefs()
		}
		v := a.netViews[id]
		v.Status = mgr.Status(id)
		v.Err = ""
		if err != nil {
			v.Err = err.Error()
		}
		a.netViews[id] = v
	})
	if err == nil {
		a.rt.coreMu.Lock()
		a.rt.meshCfgs[id] = cfg
		a.rt.coreMu.Unlock()
	}
	a.refreshTray()
}

// LeaveNetwork stops the network id and remembers it as not joined.
func (a *App) LeaveNetwork(id string) {
	a.rt.coreMu.Lock()
	mgr := a.rt.meshMgr
	delete(a.rt.meshCfgs, id)
	a.rt.coreMu.Unlock()
	var err error
	if mgr != nil {
		err = mgr.Stop(id)
	}
	a.update(func() {
		if i := a.netIndexLocked(id); i >= 0 {
			a.prefs.Mesh.Networks[i].Join = false
			a.savePrefs()
		}
		v := netView{}
		if err != nil {
			v.Err = err.Error()
		}
		a.netViews[id] = v
	})
	a.refreshTray()
}

// netIndexLocked returns the index of network id, or -1; the caller holds a.mu.
func (a *App) netIndexLocked(id string) int {
	if a.prefs.Mesh == nil {
		return -1
	}
	return slices.IndexFunc(a.prefs.Mesh.Networks, func(n MeshNetwork) bool { return n.ID == id })
}

func (a *App) setNetErr(id, msg string) {
	a.update(func() {
		v := a.netViews[id]
		v.Err = msg
		a.netViews[id] = v
	})
}

// netForm is the Add network / Edit form under a network's row.
type netForm struct {
	ID          string // "" while adding
	Label       string
	NetworkName string
	Servers     string // one URL per line
	Secret      string // write-only: empty keeps the stored secret
}

// weakSecret reports whether a network secret is too short to keep strangers out.
func weakSecret(s string) bool { return s != "" && len([]rune(s)) < 12 }

// SaveNetwork adds or updates a network from the form.
// Returns: a sentence for the form when the settings cannot be saved.
// Side effects: saves gui.json (the secret sealed with DPAPI); a joined network restarts with them.
func (a *App) SaveNetwork(f netForm) error {
	name := strings.TrimSpace(f.NetworkName)
	if name == "" {
		return errors.New(L.NetNameRequired())
	}
	var servers []string
	for _, l := range strings.Split(f.Servers, "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		if !strings.Contains(l, "://") {
			return errors.New(L.NetServerInvalid(l))
		}
		servers = append(servers, l)
	}
	var sealed []byte
	if f.Secret != "" {
		s, err := sealSecret([]byte(f.Secret))
		if err != nil {
			return err
		}
		sealed = s
	}
	rejoin := ""
	var saveErr error
	a.update(func() {
		p := a.meshPrefsLocked()
		i := a.netIndexLocked(f.ID)
		if i < 0 {
			if len(p.Networks) >= maxNetworks {
				saveErr = errors.New(L.NetTooMany(maxNetworks))
				return
			}
			if f.Secret == "" {
				saveErr = errors.New(L.NetSecretRequired())
				return
			}
			p.Networks = append(p.Networks, MeshNetwork{ID: newNetworkID()})
			i = len(p.Networks) - 1
			if p.PairNetwork == "" {
				p.PairNetwork = p.Networks[i].ID
			}
		}
		n := &p.Networks[i]
		n.Label, n.NetworkName, n.Servers = strings.TrimSpace(f.Label), name, servers
		if sealed != nil {
			n.SealedSecret = sealed
		}
		a.savePrefs()
		if n.Join {
			rejoin = n.ID
		}
	})
	if saveErr != nil {
		return saveErr
	}
	if rejoin != "" {
		go a.JoinNetwork(rejoin)
	}
	return nil
}

// RemoveNetwork leaves the network id and forgets its servers, name and secret.
func (a *App) RemoveNetwork(id string) {
	a.LeaveNetwork(id)
	a.update(func() {
		p := a.meshPrefsLocked()
		if i := a.netIndexLocked(id); i >= 0 {
			p.Networks = slices.Delete(p.Networks, i, i+1)
		}
		if p.PairNetwork == id {
			p.PairNetwork = ""
			if len(p.Networks) > 0 {
				p.PairNetwork = p.Networks[0].ID
			}
		}
		delete(a.netViews, id)
		a.savePrefs()
	})
}

// SetNetworkOptions applies the settings shared by every network and restarts the joined ones.
func (a *App) SetNetworkOptions(fn func(p *MeshPrefs)) {
	var joined []string
	a.update(func() {
		p := a.meshPrefsLocked()
		fn(p)
		a.savePrefs()
		for _, n := range p.Networks {
			if n.Join {
				joined = append(joined, n.ID)
			}
		}
	})
	go func() {
		for _, id := range joined {
			a.JoinNetwork(id)
		}
	}()
}

// resumeNetworks joins the networks the user left joined, at launch.
func (a *App) resumeNetworks() {
	for _, n := range a.networks() {
		if n.Join {
			a.JoinNetwork(n.ID)
		}
	}
}

// pollNetworks refreshes the joined networks' state and peers every few seconds while AndroFuse
// runs, so Home, Settings › Network and the tray show them without asking the core per frame.
func (a *App) pollNetworks(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.rt.coreMu.Lock()
		mgr := a.rt.meshMgr
		ids := make([]string, 0, len(a.rt.meshCfgs))
		for id := range a.rt.meshCfgs {
			ids = append(ids, id)
		}
		a.rt.coreMu.Unlock()
		if mgr == nil || len(ids) == 0 {
			continue
		}
		views := make(map[string]netView, len(ids))
		for _, id := range ids {
			v := netView{Status: mgr.Status(id)}
			v.Info, _ = mgr.Info(id)
			views[id] = v
		}
		a.update(func() {
			for id, v := range views {
				v.Err = a.netViews[id].Err
				if v.Status.Running {
					v.Err = ""
				}
				a.netViews[id] = v
			}
		})
	}
}

// meshPeerIPv4 finds hostname in the running networks.
// Returns: the network ID and the peer's mesh address.
func (a *App) meshPeerIPv4(hostname string) (string, string, bool) {
	if hostname == "" {
		return "", "", false
	}
	a.rt.coreMu.Lock()
	mgr := a.rt.meshMgr
	var ids []string
	for id := range a.rt.meshCfgs {
		ids = append(ids, id)
	}
	a.rt.coreMu.Unlock()
	if mgr == nil {
		return "", "", false
	}
	slices.Sort(ids)
	for _, id := range ids {
		if ip, ok := mgr.PeerIPv4(id, hostname); ok {
			return id, ip, true
		}
	}
	return "", "", false
}

// meshForward returns a loopback port that reaches ip:port through network id, adding the forward
// (and restarting that network's instance) when it is new.
func (a *App) meshForward(id, ip string, port int) (int, error) {
	a.rt.coreMu.Lock()
	mgr := a.rt.meshMgr
	cfg, ok := a.rt.meshCfgs[id]
	a.rt.coreMu.Unlock()
	if mgr == nil || !ok {
		return 0, errors.New(L.CompanionNoRoute())
	}
	for _, pf := range cfg.PortForwards {
		if pf.RemoteHost == ip && pf.RemotePort == port {
			return pf.LocalPort, nil
		}
	}
	local, err := companion.PickPort(0)
	if err != nil {
		return 0, err
	}
	cfg.PortForwards = append(slices.Clone(cfg.PortForwards), mesh.PortForward{LocalPort: local, RemoteHost: ip, RemotePort: port})
	if err := mgr.Start(cfg); err != nil {
		return 0, err
	}
	a.rt.coreMu.Lock()
	a.rt.meshCfgs[id] = cfg
	a.rt.coreMu.Unlock()
	return local, nil
}

// pairNetworkConfig returns the running network a newly paired phone joins, with its secret.
func (a *App) pairNetworkConfig() (mesh.Config, bool) {
	a.mu.Lock()
	id := ""
	if a.prefs.Mesh != nil {
		id = a.prefs.Mesh.PairNetwork
	}
	a.mu.Unlock()
	a.rt.coreMu.Lock()
	defer a.rt.coreMu.Unlock()
	cfg, ok := a.rt.meshCfgs[id]
	return cfg, ok && cfg.NetworkName != ""
}

// pairNetworkName is the shown name of the network a newly paired phone would join, or "" when
// that network is not joined.
func (a *App) pairNetworkName() string {
	cfg, ok := a.pairNetworkConfig()
	if !ok {
		return ""
	}
	if n, found := a.network(cfg.Instance); found {
		return n.shownName()
	}
	return cfg.NetworkName
}
