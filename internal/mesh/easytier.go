// Package mesh manages optional, default-disabled userspace EasyTier (`--no-tun`)
// cross-network and 5G relay connectivity without occupying Android VpnService
// or Windows TAP/TUN adapters (`INV-07`, `INV-08`).
package mesh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// PortForward maps one local loopback TCP port across the userspace EasyTier mesh.
type PortForward struct {
	LocalPort  int    `json:"local_port"`
	RemoteHost string `json:"remote_host"`
	RemotePort int    `json:"remote_port"`
}

// Config configures optional userspace EasyTier (`--no-tun`) execution (`INV-07`).
type Config struct {
	Enabled       bool          `json:"enabled"`
	BinaryPath    string        `json:"binary_path,omitempty"`
	NetworkName   string        `json:"network_name"`
	NetworkSecret string        `json:"-"`
	DeviceName    string        `json:"device_name,omitempty"`
	Peers         []string      `json:"peers,omitempty"`
	PortForwards  []PortForward `json:"port_forwards,omitempty"`
}

// Status summarizes runtime state of the userspace mesh supervisor.
type Status struct {
	Enabled     bool     `json:"enabled"`
	Running     bool     `json:"running"`
	BinaryFound bool     `json:"binary_found"`
	BinaryPath  string   `json:"binary_path,omitempty"`
	DeviceName  string   `json:"device_name,omitempty"`
	ScrubbedCmd []string `json:"scrubbed_cmd,omitempty"`
	LastError   string   `json:"last_error,omitempty"`
}

// BuildArgs validates cfg and builds argument-vector CLI flags for `easytier-core --no-tun` (`INV-07`).
//
// Notes: Refuses to build arguments unless cfg.Enabled is true.
func BuildArgs(cfg Config) ([]string, error) {
	if !cfg.Enabled {
		return nil, errors.New("userspace mesh is disabled by default (INV-07)")
	}
	netName := strings.TrimSpace(cfg.NetworkName)
	if netName == "" {
		return nil, errors.New("network_name is required")
	}
	args := []string{
		"--no-tun",
		"--network-name", netName,
	}
	if sec := strings.TrimSpace(cfg.NetworkSecret); sec != "" {
		args = append(args, "--network-secret", sec)
	}
	if dev := strings.TrimSpace(cfg.DeviceName); dev != "" {
		args = append(args, "--hostname", dev)
	}
	for _, p := range cfg.Peers {
		p = strings.TrimSpace(p)
		if p != "" {
			args = append(args, "-p", p)
		}
	}
	for _, pf := range cfg.PortForwards {
		if pf.LocalPort <= 0 || pf.LocalPort > 65535 || pf.RemotePort <= 0 || pf.RemotePort > 65535 {
			return nil, fmt.Errorf("invalid port forward %+v", pf)
		}
		rh := strings.TrimSpace(pf.RemoteHost)
		if rh == "" {
			return nil, errors.New("port forward remote_host is required")
		}
		args = append(args, "--port-forward", fmt.Sprintf("tcp://127.0.0.1:%d/%s:%d", pf.LocalPort, rh, pf.RemotePort))
	}
	return args, nil
}

// ScrubArgs returns a copy of args with network secrets and peer endpoints redacted (`INV-08`).
func ScrubArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		if out[i] == "--network-secret" && i+1 < len(out) {
			out[i+1] = "****"
			i++
		} else if out[i] == "-p" && i+1 < len(out) {
			out[i+1] = "<peer-redacted>"
			i++
		}
	}
	return out
}

// FindBinary locates an optional user-provided `easytier-core` executable.
func FindBinary(explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		if st, err := os.Stat(explicit); err == nil && !st.IsDir() {
			return explicit
		}
	}
	exeName := "easytier-core"
	if runtime.GOOS == "windows" {
		exeName = "easytier-core.exe"
	}
	if p, err := exec.LookPath(exeName); err == nil {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), exeName)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
	}
	return ""
}

// Manager supervises an optional external userspace `easytier-core --no-tun` process.
type Manager struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool
	status  Status
}

// NewManager creates an idle userspace mesh Manager (disabled until started explicitly).
func NewManager() *Manager {
	bin := FindBinary("")
	return &Manager{
		status: Status{
			BinaryFound: bin != "",
			BinaryPath:  bin,
		},
	}
}

// Status returns a snapshot of the current mesh supervisor state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil && m.cmd.ProcessState != nil && m.cmd.ProcessState.Exited() {
		m.cmd = nil
		m.running = false
		m.status.Running = false
	}
	st := m.status
	st.ScrubbedCmd = append([]string(nil), m.status.ScrubbedCmd...)
	return st
}

// Start launches `easytier-core --no-tun` when cfg.Enabled is true (`INV-07`).
func (m *Manager) Start(ctx context.Context, cfg Config) error {
	args, err := BuildArgs(cfg)
	if err != nil {
		return err
	}
	bin := FindBinary(cfg.BinaryPath)
	if bin == "" {
		return errors.New("easytier-core binary not found (optional external install required)")
	}
	_ = m.Stop()

	cmd := exec.CommandContext(ctx, bin, args...)
	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		m.status.Enabled = true
		m.status.BinaryFound = true
		m.status.BinaryPath = bin
		m.status.LastError = err.Error()
		m.mu.Unlock()
		return err
	}

	m.mu.Lock()
	m.cmd = cmd
	m.running = true
	m.status = Status{
		Enabled:     true,
		Running:     true,
		BinaryFound: true,
		BinaryPath:  bin,
		DeviceName:  cfg.DeviceName + ":" + strconv.Itoa(len(cfg.PortForwards)),
		ScrubbedCmd: ScrubArgs(args),
	}
	m.mu.Unlock()

	go func() {
		waitErr := cmd.Wait()
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
			m.running = false
			m.status.Running = false
			if waitErr != nil && ctx.Err() == nil {
				m.status.LastError = waitErr.Error()
			}
		}
		m.mu.Unlock()
	}()
	return nil
}

// Stop terminates any running userspace EasyTier child process cleanly.
func (m *Manager) Stop() error {
	m.mu.Lock()
	cmd := m.cmd
	m.cmd = nil
	m.running = false
	m.status.Running = false
	m.status.Enabled = false
	m.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return nil
}
