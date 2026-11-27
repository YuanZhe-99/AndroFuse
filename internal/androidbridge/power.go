package androidbridge

import (
	"sync"
	"time"
)

// DaemonStatus summarizes the attached persistent UID 2000 daemon (`PLAN` §9.2).
type DaemonStatus struct {
	Ready             bool `json:"ready"`
	PID               int  `json:"pid"`
	HasTrustedDisplay bool `json:"has_trusted_display"`
	FDVerified        bool `json:"fd_verified"`
}

// DaemonSessionConfig configures a UID 2000 daemon scrcpy-server stream (`PLAN` §9.2 Phase 2).
type DaemonSessionConfig struct {
	MaxSize               int    `json:"maxSize"`
	BitRate               int    `json:"bitRate"`
	MaxFPS                int    `json:"maxFps"`
	Codec                 string `json:"codec"`
	NewDisplay            string `json:"newDisplay"`
	HideSystemDecorations bool   `json:"hideDecorations"`
	FlexDisplay           bool   `json:"flexDisplay"`
	DisplayID             int    `json:"displayId"`
	StartApp              string `json:"startApp"`
	Control               bool   `json:"control"`
}

// DaemonSessionHandles holds the local abstract sockets bridging a daemon session.
type DaemonSessionHandles struct {
	OK          bool   `json:"ok"`
	SessionID   string `json:"session_id"`
	VideoSocket string `json:"video_socket"`
	CtrlSocket  string `json:"ctrl_socket"`
	Error       string `json:"error"`
}

// Power policy (PLAN §9.1-3).
// activeIdle releases the wake/Wi-Fi lock this long after the last transfer activity
// (a variable so tests can shorten it).
var activeIdle = 30 * time.Second

const (
	// activeHardTimeout is the native lock timeout; it is renewed while activity continues, so a
	// lost release can hold the CPU for at most this long.
	activeHardTimeout = 2 * time.Minute
	// activeRenewAfter is how old an acquire may get before activity renews it.
	activeRenewAfter = time.Minute
)

// lease tracks the transfer "activity lease" (partial wake lock + Wi-Fi lock).
var lease struct {
	mu       sync.Mutex
	held     bool
	acquired time.Time
	idle     *time.Timer
}

// TouchActive marks transfer activity (file push, media frame, clipboard) and holds the
// wake/Wi-Fi lock until activeIdle passes without another touch.
// Side effects: on Android acquires or renews the native locks; elsewhere only updates state.
func TouchActive() {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	now := time.Now()
	if !lease.held || now.Sub(lease.acquired) >= activeRenewAfter {
		acquireActive(activeHardTimeout)
		lease.held = true
		lease.acquired = now
	}
	if lease.idle != nil {
		lease.idle.Stop()
	}
	lease.idle = time.AfterFunc(activeIdle, releaseLease)
}

// ReleaseActive drops the activity lease immediately (session ended, server stopped).
func ReleaseActive() {
	lease.mu.Lock()
	if lease.idle != nil {
		lease.idle.Stop()
		lease.idle = nil
	}
	lease.mu.Unlock()
	releaseLease()
}

// ActiveHeld reports whether the activity lease is currently held.
func ActiveHeld() bool {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return lease.held
}

func releaseLease() {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.held {
		releaseActive()
		lease.held = false
	}
}
