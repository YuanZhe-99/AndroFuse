// Package mesh drives the optional, default-disabled EasyTier user-space mesh that is embedded in
// the Rust core (`androfuse_call` methods `mesh.start` / `mesh.stop` / `mesh.status`, ABI v6).
// It never installs a TAP/TUN adapter or takes Android's VpnService; remote services are reached
// through loopback port forwards only (`INV-07`), and secrets are never logged (`INV-08`).
package mesh

import (
	"errors"
	"sync"

	"github.com/YuanZhe-99/AndroFuse/internal/core"
)

// PortForward maps one local loopback TCP port across the mesh to a peer's mesh IP and port.
type PortForward struct {
	LocalPort  int    `json:"local_port"`
	RemoteHost string `json:"remote_host"`
	RemotePort int    `json:"remote_port"`
}

// Config configures the embedded mesh. It is only applied when Enabled is true (`INV-07`).
type Config struct {
	Enabled       bool     `json:"enabled"`
	NetworkName   string   `json:"network_name"`
	NetworkSecret string   `json:"-"`
	DeviceName    string   `json:"device_name,omitempty"`
	Peers         []string `json:"peers,omitempty"`
	// Listeners are addresses this node accepts connections on; empty by default (`INV-07`).
	Listeners    []string      `json:"listeners,omitempty"`
	PortForwards []PortForward `json:"port_forwards,omitempty"`
}

// Status summarizes the embedded mesh for the UI.
type Status struct {
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
	Engine    string `json:"engine,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// startParams is the `mesh.start` wire payload; unlike Config it carries the secret to the core.
type startParams struct {
	NetworkName   string        `json:"network_name"`
	NetworkSecret string        `json:"network_secret"`
	DeviceName    string        `json:"device_name,omitempty"`
	Peers         []string      `json:"peers,omitempty"`
	Listeners     []string      `json:"listeners,omitempty"`
	PortForwards  []PortForward `json:"port_forwards,omitempty"`
}

// coreStatus mirrors the core's MeshStatus result (running info is not surfaced, `INV-08`).
type coreStatus struct {
	Running bool   `json:"running"`
	Engine  string `json:"engine"`
	Error   string `json:"error,omitempty"`
}

// Manager controls the single embedded mesh instance of a loaded core library.
type Manager struct {
	lib    *core.Lib
	mu     sync.Mutex
	status Status
}

// NewManager returns an idle Manager for lib; nothing runs until Start is called.
func NewManager(lib *core.Lib) *Manager {
	return &Manager{lib: lib}
}

// Start applies cfg to the embedded mesh, replacing any running instance.
// Returns: the core's validation or start error (also kept in Status().LastError).
// Side effects: opens outbound peer connections and 127.0.0.1 listeners for cfg.PortForwards.
func (m *Manager) Start(cfg Config) error {
	if !cfg.Enabled {
		return errors.New("the mesh is disabled by default (INV-07)")
	}
	var st coreStatus
	err := m.lib.Call("mesh.start", startParams{
		NetworkName:   cfg.NetworkName,
		NetworkSecret: cfg.NetworkSecret,
		DeviceName:    cfg.DeviceName,
		Peers:         cfg.Peers,
		Listeners:     cfg.Listeners,
		PortForwards:  cfg.PortForwards,
	}, &st)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = Status{Enabled: err == nil, Running: st.Running, Engine: st.Engine, LastError: st.Error}
	if err != nil {
		m.status.LastError = err.Error()
	}
	return err
}

// Stop shuts the embedded mesh down; stopping an idle mesh is not an error.
func (m *Manager) Stop() error {
	var st coreStatus
	err := m.lib.Call("mesh.stop", nil, &st)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = Status{Running: st.Running, Engine: st.Engine}
	if err != nil {
		m.status.LastError = err.Error()
	}
	return err
}

// Status refreshes and returns the mesh state from the core.
func (m *Manager) Status() Status {
	var st coreStatus
	err := m.lib.Call("mesh.status", nil, &st)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.status.Running = st.Running
		m.status.Engine = st.Engine
		m.status.LastError = st.Error
	}
	return m.status
}
