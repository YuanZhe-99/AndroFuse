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
	name := strings.TrimSpace(req.NetworkName)
	if name == "" {
		return companion.MeshJoinResponse{}, errors.New("network name is required")
	}
	sealed, err := seal([]byte(req.NetworkSecret))
	if err != nil {
		return companion.MeshJoinResponse{}, err
	}
	running, err := h.startMesh(name, req.NetworkSecret, req.Peers)
	h.mu.Lock()
	h.cfg.Mesh = &MeshSettings{NetworkName: name, Peers: req.Peers, SealedSecret: sealed}
	h.appendAuditLocked(fmt.Sprintf("joined mesh %q from paired computer", name))
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
	_, _ = h.startMesh(s.NetworkName, string(secret), s.Peers)
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
		_ = mgr.Stop()
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
		st := mgr.Status()
		running = st.Running
		if errMsg == "" {
			errMsg = st.LastError
		}
	}
	return name, running, errMsg
}

// startMesh opens the core on first use and starts the mesh with no listeners (the phone only
// dials out, `INV-07`).
func (h *Host) startMesh(name, secret string, peers []string) (bool, error) {
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

	model, _, _ := deviceIdentity()
	err := mgr.Start(mesh.Config{Enabled: true, NetworkName: name, NetworkSecret: secret, DeviceName: model, Peers: peers})
	h.setMeshErr(err)
	return err == nil && mgr.Status().Running, err
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
