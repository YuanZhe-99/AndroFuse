package androidhost

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
)

// MeshSettings is the EasyTier network a paired desktop handed over (PLAN §9.1-1).
// The secret is stored only sealed with the Android Keystore vault key (`INV-08`).
type MeshSettings struct {
	NetworkName  string   `json:"network_name"`
	Peers        []string `json:"peers,omitempty"`
	SealedSecret []byte   `json:"sealed_secret"`
	// FullSpeed turns the low-power mesh off (it is on by default on the phone).
	FullSpeed bool `json:"full_speed,omitempty"`
}

// pairPrompt is an adb pairing request waiting for the phone's user.
type pairPrompt struct {
	req    companion.PairRequest
	answer chan bool
}

// meshRuntime is the phone's embedded core and mesh, opened on first use.
type meshRuntime struct {
	lib *core.Lib
	mgr *mesh.Manager
	err string
}

// JoinMesh starts the embedded mesh in the desktop's network and remembers it, so it rejoins
// whenever the Companion starts again.
// Side effects: seals and persists the secret; opens outbound EasyTier connections.
func (h *Host) JoinMesh(req companion.MeshJoinRequest) (companion.MeshJoinResponse, error) {
	return h.joinMesh(req, "from paired computer")
}

// joinMesh is JoinMesh; from says who chose the network, for the audit log.
func (h *Host) joinMesh(req companion.MeshJoinRequest, from string) (companion.MeshJoinResponse, error) {
	name := strings.TrimSpace(req.NetworkName)
	if name == "" {
		return companion.MeshJoinResponse{}, errors.New("network name is required")
	}
	sealed, err := seal([]byte(req.NetworkSecret))
	if err != nil {
		return companion.MeshJoinResponse{}, err
	}
	h.mu.Lock()
	full := h.cfg.Mesh != nil && h.cfg.Mesh.FullSpeed
	h.mu.Unlock()
	running, err := h.startMesh(name, req.NetworkSecret, req.Peers, full)
	h.mu.Lock()
	h.cfg.Mesh = &MeshSettings{NetworkName: name, Peers: req.Peers, SealedSecret: sealed, FullSpeed: full}
	h.appendAuditLocked(fmt.Sprintf("joined mesh %q %s", name, from))
	h.saveConfigLocked()
	h.mu.Unlock()
	h.notify()
	if err != nil {
		return companion.MeshJoinResponse{Running: false, Message: err.Error()}, nil
	}
	return companion.MeshJoinResponse{Running: running}, nil
}

// resumeMesh rejoins the remembered network; a no-op when none is saved.
func (h *Host) resumeMesh() {
	h.mu.Lock()
	s := h.cfg.Mesh
	h.mu.Unlock()
	if s == nil {
		return
	}
	secret, err := unseal(s.SealedSecret)
	if err != nil {
		h.setMeshErr(err)
		return
	}
	_, _ = h.startMesh(s.NetworkName, string(secret), s.Peers, s.FullSpeed)
}

// onNetworkChanged records a ConnectivityManager network transition and schedules a debounced
// EasyTier mesh rebind when switching between live transports (e.g., cellular <-> wifi).
func (h *Host) onNetworkChanged(kind string) {
	h.mu.Lock()
	prev := h.network
	h.network = kind
	shouldRebind := prev != "" && prev != kind && kind != "" && kind != "none" && h.cfg.Mesh != nil
	if shouldRebind {
		if h.meshRebindTimer != nil {
			h.meshRebindTimer.Stop()
		}
		delay := h.meshRebindDelay
		if delay <= 0 {
			delay = 700 * time.Millisecond
		}
		fn := h.resumeMesh
		if h.onMeshRebind != nil {
			fn = h.onMeshRebind
		}
		h.meshRebindTimer = time.AfterFunc(delay, fn)
		h.appendAuditLocked(fmt.Sprintf("network switched (%s -> %s), refreshing mesh", prev, kind))
	}
	h.mu.Unlock()
	h.notify()
}

// LeaveMesh stops the embedded mesh and forgets the network.
func (h *Host) LeaveMesh() {
	h.mu.Lock()
	mgr := h.mesh.mgr
	h.cfg.Mesh = nil
	h.mesh.err = ""
	h.appendAuditLocked("left mesh")
	h.saveConfigLocked()
	h.mu.Unlock()
	if mgr != nil {
		_ = mgr.Stop("")
	}
	h.notify()
}

// MeshState reports the remembered network and whether its mesh is running, for the view.
func (h *Host) MeshState() (name string, running bool, errMsg string) {
	h.mu.Lock()
	mgr := h.mesh.mgr
	if h.cfg.Mesh != nil {
		name = h.cfg.Mesh.NetworkName
	}
	errMsg = h.mesh.err
	h.mu.Unlock()
	if mgr != nil {
		st := mgr.Status("")
		running = st.Running
		if errMsg == "" {
			errMsg = st.LastError
		}
	}
	return name, running, errMsg
}

// startMesh opens the core on first use and starts the mesh with no listeners (the phone only
// dials out, `INV-07`), in low-power mode unless fullSpeed.
func (h *Host) startMesh(name, secret string, peers []string, fullSpeed bool) (bool, error) {
	h.mu.Lock()
	if h.mesh.lib == nil {
		lib, err := openCore(h.stateDir)
		if err != nil {
			h.mesh.err = err.Error()
			h.mu.Unlock()
			h.notify()
			return false, err
		}
		h.mesh.lib = lib
		h.mesh.mgr = mesh.NewManager(lib)
	}
	mgr := h.mesh.mgr
	h.mu.Unlock()

	err := mgr.Start(mesh.Config{Enabled: true, NetworkName: name, NetworkSecret: secret, DeviceName: h.meshHost(), Peers: peers, LowPower: !fullSpeed})
	h.setMeshErr(err)
	return err == nil && mgr.Status("").Running, err
}

func (h *Host) setMeshErr(err error) {
	h.mu.Lock()
	h.mesh.err = ""
	if err != nil {
		h.mesh.err = err.Error()
	}
	h.mu.Unlock()
	h.notify()
}

// askPair shows an adb pairing request to the phone's user and waits for the answer.
// Returns: true only when the user allowed it within companion.PairWait; a second request while
// one is showing is declined.
func (h *Host) askPair(req companion.PairRequest) bool {
	p := &pairPrompt{req: req, answer: make(chan bool, 1)}
	h.mu.Lock()
	if h.pair != nil {
		h.mu.Unlock()
		return false
	}
	h.pair = p
	h.mu.Unlock()
	h.notify()

	var ok bool
	select {
	case ok = <-p.answer:
	case <-time.After(companion.PairWait):
	}
	h.mu.Lock()
	if h.pair == p {
		h.pair = nil
	}
	h.appendAuditLocked(fmt.Sprintf("pairing with %q over adb: allowed=%v", req.ClientName, ok))
	h.mu.Unlock()
	h.notify()
	return ok
}

// AnswerPair delivers the user's answer to the pairing request on screen.
func (h *Host) AnswerPair(allow bool) {
	h.mu.Lock()
	p := h.pair
	h.pair = nil
	h.mu.Unlock()
	if p != nil {
		p.answer <- allow
	}
	h.notify()
}

// PendingPair returns the pairing request waiting for the user, if any.
func (h *Host) PendingPair() (companion.PairRequest, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pair == nil {
		return companion.PairRequest{}, false
	}
	return h.pair.req, true
}

// SetMeshFullSpeed switches the joined mesh between low-power (the default) and full speed.
// Side effects: saves the choice and restarts the running mesh with it.
func (h *Host) SetMeshFullSpeed(full bool) {
	h.mu.Lock()
	if h.cfg.Mesh == nil || h.cfg.Mesh.FullSpeed == full {
		h.mu.Unlock()
		return
	}
	h.cfg.Mesh.FullSpeed = full
	h.saveConfigLocked()
	h.mu.Unlock()
	h.notify()
	go h.resumeMesh()
}

// MeshFullSpeed reports whether the joined mesh runs at full speed instead of low-power.
func (h *Host) MeshFullSpeed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.Mesh != nil && h.cfg.Mesh.FullSpeed
}

// meshHost is the EasyTier hostname the phone joins networks under: its kept random id and the
// name its person gave it, so desktops show it by that name and tell it from relay servers.
// Side effects: draws and saves MeshID the first time.
func (h *Host) meshHost() string {
	name, _, _, _ := deviceIdentity()
	return mesh.Hostname(mesh.KindPhone, h.meshID(), name)
}

// meshID is the phone's kept random device id, in its mesh hostname and behind its Companion port
// (companion.DevicePorts).
// Side effects: draws and saves MeshID the first time.
func (h *Host) meshID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg.MeshID == "" {
		h.cfg.MeshID = mesh.NewDeviceID()
		h.saveConfigLocked()
	}
	return h.cfg.MeshID
}

// meshForm is what the phone's own network editor holds: the network name, its servers (one per
// line) and secret, and why the last save failed.
type meshForm struct {
	Name, Servers, Secret, Err string
}

// editMesh opens the network editor with the remembered network's name and servers (the secret
// is never shown again, so it must be typed).
func (h *Host) editMesh() {
	f := &meshForm{}
	h.mu.Lock()
	if s := h.cfg.Mesh; s != nil {
		f.Name, f.Servers = s.NetworkName, strings.Join(s.Peers, "\n")
	}
	h.mu.Unlock()
	h.meshForm = f
}

// saveMeshForm joins the network the phone's person typed, in place of the one a computer handed
// over, and closes the editor; a failure stays in the editor.
// Side effects: see JoinMesh; runs off the UI thread and redraws when done.
func (h *Host) saveMeshForm(f meshForm) {
	var servers []string
	for _, s := range strings.Split(f.Servers, "\n") {
		if s = strings.TrimSpace(s); s != "" {
			servers = append(servers, s)
		}
	}
	res, err := h.joinMesh(companion.MeshJoinRequest{NetworkName: f.Name, NetworkSecret: f.Secret, Peers: servers}, "on this phone")
	if err == nil && res.Message != "" {
		err = errors.New(res.Message)
	}
	h.mu.Lock()
	if err != nil {
		f.Err = err.Error()
		h.meshForm = &f
	} else {
		h.meshForm = nil
	}
	h.mu.Unlock()
	h.notify()
}
