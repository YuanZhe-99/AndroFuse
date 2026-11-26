// Package mesh drives the optional, default-disabled EasyTier user-space mesh that is embedded in
// the Rust core (`androfuse_call` methods `mesh.start` / `mesh.stop` / `mesh.status`, ABI v6).
// It never installs a TAP/TUN adapter or takes Android's VpnService; remote services are reached
// through loopback port forwards only (`INV-07`), and secrets are never logged (`INV-08`).
// The desktop runs one EasyTier instance per joined network; the Companion runs one.
package mesh

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"github.com/YuanZhe-99/AndroFuse/internal/core"
)

// PortForward maps one local loopback TCP port across the mesh to a peer's mesh IP and port.
type PortForward struct {
	LocalPort  int    `json:"local_port"`
	RemoteHost string `json:"remote_host"`
	RemotePort int    `json:"remote_port"`
}

// Config configures one mesh network. It is only applied when Enabled is true (`INV-07`).
type Config struct {
	// Instance names the EasyTier instance (one per network); empty is the single default one.
	Instance      string   `json:"instance,omitempty"`
	Enabled       bool     `json:"enabled"`
	NetworkName   string   `json:"network_name"`
	NetworkSecret string   `json:"-"`
	DeviceName    string   `json:"device_name,omitempty"`
	Peers         []string `json:"peers,omitempty"`
	// Listeners are addresses this node accepts connections on; empty by default (`INV-07`).
	Listeners    []string      `json:"listeners,omitempty"`
	PortForwards []PortForward `json:"port_forwards,omitempty"`
	// LowPower trades connection set-up speed for fewer background wakeups (see the core's
	// MeshConfig.low_power); the Companion uses it on battery.
	LowPower bool `json:"low_power,omitempty"`
	// DisableP2P never builds direct links; LatencyFirst prefers the lowest-latency route.
	DisableP2P   bool `json:"disable_p2p,omitempty"`
	LatencyFirst bool `json:"latency_first,omitempty"`
}

// Status summarizes one mesh network for the UI.
type Status struct {
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
	Engine    string `json:"engine,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// Peer is another node a running network routes to.
type Peer struct {
	Hostname string
	IPv4     string  // its mesh address, "" until it has one
	Direct   bool    // connected directly rather than through a relay
	Latency  float64 // milliseconds; 0 when not measured yet
}

// HostPrefix starts the EasyTier hostname of every AndroFuse device. Other nodes in a network,
// such as public relay servers or EasyTier on other machines, have none and are never shown.
const HostPrefix = "AndroFuse-"

// Kinds of AndroFuse device in a hostname.
const (
	KindPhone = "phone"
	KindPC    = "pc"
)

// Hostname is the name an AndroFuse device joins every network under:
// "AndroFuse-<kind>-<id> <name>", where id is the device's own random hex (stable while it keeps
// its settings) and name what its person calls it.
func Hostname(kind, id, name string) string {
	h := HostPrefix + kind + "-" + id
	if name = strings.Join(strings.Fields(name), " "); name != "" {
		h += " " + name
	}
	return h
}

// ParseHostname reads an AndroFuse hostname back. EasyTier hostnames are whatever a node chose,
// so name is only a label.
// Returns: ok false for a node that is no AndroFuse device.
func ParseHostname(h string) (kind, id, name string, ok bool) {
	rest, found := strings.CutPrefix(h, HostPrefix)
	if !found {
		return "", "", "", false
	}
	kind, rest, found = strings.Cut(rest, "-")
	if !found || (kind != KindPhone && kind != KindPC) {
		return "", "", "", false
	}
	id, name, _ = strings.Cut(rest, " ")
	if id == "" {
		return "", "", "", false
	}
	return kind, id, name, true
}

// SameDevice reports whether hostnames a and b name the same AndroFuse device: the same kind and
// id, whatever name each carries now; other hostnames only when they are equal.
func SameDevice(a, b string) bool {
	ka, ia, _, oka := ParseHostname(a)
	kb, ib, _, okb := ParseHostname(b)
	if oka && okb {
		return ka == kb && ia == ib
	}
	return a == b
}

// NewDeviceID draws the random id a device keeps in its hostname.
func NewDeviceID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Info is what a running network knows about itself and its peers (never logged, `INV-08`).
type Info struct {
	IPv4  string // this node's mesh address
	NAT   string // EasyTier's UDP NAT type name, for example "Symmetric"; "" when unknown
	Peers []Peer
}

// startParams is the `mesh.start` wire payload; unlike Config it carries the secret to the core.
type startParams struct {
	Instance      string        `json:"instance,omitempty"`
	NetworkName   string        `json:"network_name"`
	NetworkSecret string        `json:"network_secret"`
	DeviceName    string        `json:"device_name,omitempty"`
	Peers         []string      `json:"peers,omitempty"`
	Listeners     []string      `json:"listeners,omitempty"`
	PortForwards  []PortForward `json:"port_forwards,omitempty"`
	LowPower      bool          `json:"low_power,omitempty"`
	DisableP2P    bool          `json:"disable_p2p,omitempty"`
	LatencyFirst  bool          `json:"latency_first,omitempty"`
}

type instanceRef struct {
	Instance string `json:"instance,omitempty"`
}

type ipv4Addr struct {
	Address *struct {
		Addr uint32 `json:"addr"`
	} `json:"address"`
}

func (a *ipv4Addr) String() string {
	if a == nil || a.Address == nil || a.Address.Addr == 0 {
		return ""
	}
	v := a.Address.Addr
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}).String()
}

// coreStatus mirrors the core's MeshStatus result. Info, EasyTier's running info, is read only
// for the UI and peer lookups and never logged (`INV-08`).
type coreStatus struct {
	Running bool   `json:"running"`
	Engine  string `json:"engine"`
	Error   string `json:"error,omitempty"`
	Info    struct {
		MyNode struct {
			VirtualIPv4 *ipv4Addr `json:"virtual_ipv4"`
			Stun        struct {
				UDPNAT string `json:"udp_nat_type"`
			} `json:"stun_info"`
		} `json:"my_node_info"`
		Routes []struct {
			Hostname string    `json:"hostname"`
			IPv4     *ipv4Addr `json:"ipv4_addr"`
		} `json:"routes"`
		Pairs []struct {
			Peer *struct {
				Conns []struct {
					Stats struct {
						LatencyUs string `json:"latency_us"`
					} `json:"stats"`
				} `json:"conns"`
				Direct []any `json:"directly_connected_conns"`
			} `json:"peer"`
			Route *struct {
				Hostname    string    `json:"hostname"`
				IPv4        *ipv4Addr `json:"ipv4_addr"`
				Cost        int       `json:"cost"`
				PathLatency float64   `json:"path_latency"`
			} `json:"route"`
		} `json:"peer_route_pairs"`
	} `json:"info"`
}

// Manager controls the embedded mesh networks of a loaded core library.
type Manager struct {
	lib    *core.Lib
	mu     sync.Mutex
	status map[string]Status
}

// NewManager returns an idle Manager for lib; nothing runs until Start is called.
func NewManager(lib *core.Lib) *Manager {
	return &Manager{lib: lib, status: map[string]Status{}}
}

// Start applies cfg to its network's instance, replacing a running instance of the same name and
// leaving other networks alone.
// Returns: the core's validation or start error (also kept in Status(cfg.Instance).LastError).
// Side effects: opens outbound peer connections and 127.0.0.1 listeners for cfg.PortForwards.
func (m *Manager) Start(cfg Config) error {
	if !cfg.Enabled {
		return errors.New("the mesh is disabled by default (INV-07)")
	}
	var st coreStatus
	err := m.lib.Call("mesh.start", startParams{
		Instance:      cfg.Instance,
		NetworkName:   cfg.NetworkName,
		NetworkSecret: cfg.NetworkSecret,
		DeviceName:    cfg.DeviceName,
		Peers:         cfg.Peers,
		Listeners:     cfg.Listeners,
		PortForwards:  cfg.PortForwards,
		LowPower:      cfg.LowPower,
		DisableP2P:    cfg.DisableP2P,
		LatencyFirst:  cfg.LatencyFirst,
	}, &st)
	s := Status{Enabled: err == nil, Running: st.Running, Engine: st.Engine, LastError: st.Error}
	if err != nil {
		s.LastError = err.Error()
	}
	m.mu.Lock()
	m.status[cfg.Instance] = s
	m.mu.Unlock()
	return err
}

// Stop shuts one network's instance down; stopping one that is not running is not an error.
func (m *Manager) Stop(instance string) error {
	var st coreStatus
	err := m.lib.Call("mesh.stop", instanceRef{Instance: instance}, &st)
	s := Status{Running: st.Running, Engine: st.Engine}
	if err != nil {
		s.LastError = err.Error()
	}
	m.mu.Lock()
	m.status[instance] = s
	m.mu.Unlock()
	return err
}

// read asks the core for one instance's status and running info.
func (m *Manager) read(instance string) (coreStatus, error) {
	var st coreStatus
	err := m.lib.Call("mesh.status", instanceRef{Instance: instance}, &st)
	return st, err
}

// Status refreshes and returns one network's state from the core.
func (m *Manager) Status(instance string) Status {
	st, err := m.read(instance)
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status[instance]
	if err == nil {
		s.Running, s.Engine, s.LastError = st.Running, st.Engine, st.Error
	}
	m.status[instance] = s
	return s
}

// Info returns what a running network knows about itself and its peers.
// Returns: false while the network is down.
func (m *Manager) Info(instance string) (Info, bool) {
	st, err := m.read(instance)
	if err != nil || !st.Running {
		return Info{}, false
	}
	out := Info{IPv4: st.Info.MyNode.VirtualIPv4.String(), NAT: st.Info.MyNode.Stun.UDPNAT}
	for _, p := range st.Info.Pairs {
		if p.Route == nil {
			continue
		}
		peer := Peer{Hostname: p.Route.Hostname, IPv4: p.Route.IPv4.String(), Direct: p.Route.Cost == 1, Latency: p.Route.PathLatency}
		if p.Peer != nil && len(p.Peer.Direct) > 0 {
			peer.Direct = true
			for _, c := range p.Peer.Conns {
				if us, err := strconv.ParseFloat(c.Stats.LatencyUs, 64); err == nil && us > 0 {
					peer.Latency = us / 1000
					break
				}
			}
		}
		out.Peers = append(out.Peers, peer)
	}
	return out, true
}

// PeerIPv4 returns the mesh IPv4 address of the peer that joined instance's network as hostname
// (the same AndroFuse device under any name, SameDevice).
// Returns: false while the network is down, the peer is unknown, or it has no address yet.
func (m *Manager) PeerIPv4(instance, hostname string) (string, bool) {
	st, err := m.read(instance)
	if err != nil || !st.Running {
		return "", false
	}
	for _, r := range st.Info.Routes {
		if SameDevice(r.Hostname, hostname) {
			if ip := r.IPv4.String(); ip != "" {
				return ip, true
			}
		}
	}
	for _, p := range st.Info.Pairs {
		if p.Route != nil && SameDevice(p.Route.Hostname, hostname) {
			if ip := p.Route.IPv4.String(); ip != "" {
				return ip, true
			}
		}
	}
	return "", false
}
