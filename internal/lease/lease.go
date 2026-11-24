// Package lease persists the atomic virtual-display, DeX loopback, UHID, and
// ADB tunnel lease ledger (leases.json) so crashes or disconnects recover cleanly
// without leaving ghost displays on the Android device (INV-03).
package lease

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Kind identifies which runtime resource a lease protects.
type Kind string

const (
	KindMirror         Kind = "mirror"
	KindVirtualDisplay Kind = "virtual_display"
	KindNativeDeX      Kind = "native_dex"
)

// Lease records one active virtual display, loopback, or tunnel lease.
//
// Notes: Stores SHA-256 `DeviceHash` rather than raw ADB serials (INV-08).
type Lease struct {
	SessionID     string    `json:"session_id"`
	DeviceHash    string    `json:"device_hash"`
	Kind          Kind      `json:"kind"`
	Token         string    `json:"token,omitempty"`
	DisplayID     int       `json:"display_id,omitempty"`
	ForwardPort   int       `json:"forward_port,omitempty"`
	ReverseSocket string    `json:"reverse_socket,omitempty"`
	AppPackage    string    `json:"app_package,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Store atomically persists active leases to leases.json.
type Store struct {
	path   string
	mu     sync.Mutex
	leases []Lease
}

// Open loads (or initializes) a lease ledger at path.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Acquire records or updates a lease and flushes leases.json atomically before
// the caller creates device-side state (INV-03).
func (s *Store) Acquire(l Lease) error {
	if l.SessionID == "" {
		return errors.New("lease session_id is required")
	}
	l.UpdatedAt = time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := slices.IndexFunc(s.leases, func(cur Lease) bool {
		return cur.SessionID == l.SessionID
	})
	if idx >= 0 {
		s.leases[idx] = l
	} else {
		s.leases = append(s.leases, l)
	}
	return s.saveLocked()
}

// Release removes a lease after teardown completes and persists leases.json.
func (s *Store) Release(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := slices.DeleteFunc(slices.Clone(s.leases), func(cur Lease) bool {
		return cur.SessionID == sessionID
	})
	if len(filtered) == len(s.leases) {
		return nil
	}
	s.leases = filtered
	return s.saveLocked()
}

// List returns a snapshot of currently persisted leases.
func (s *Store) List() []Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.leases)
}

// RecoverAll runs fn against every persisted lease and releases each lease for
// which fn returns nil, returning any combined errors while retaining unrecovered leases.
func (s *Store) RecoverAll(fn func(Lease) error) error {
	snapshot := s.List()
	var firstErr error
	for _, l := range snapshot {
		if err := fn(l); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		_ = s.Release(l.SessionID)
	}
	return firstErr
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.leases = nil
		return nil
	}
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		s.leases = nil
		return nil
	}
	var items []Lease
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	s.leases = items
	return nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.leases, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
