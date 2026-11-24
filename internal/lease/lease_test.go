package lease

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquireRecoverReleaseRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases.json")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := st.List(); len(got) != 0 {
		t.Fatalf("expected empty initial ledger, got %+v", got)
	}

	if err := st.Acquire(Lease{
		SessionID:   "dex-1",
		DeviceHash:  "deadbeef",
		Kind:        KindNativeDeX,
		Token:       "tok1234",
		DisplayID:   4,
		ForwardPort: 27183,
	}); err != nil {
		t.Fatalf("Acquire dex-1: %v", err)
	}
	if err := st.Acquire(Lease{
		SessionID:   "app-1",
		DeviceHash:  "deadbeef",
		Kind:        KindVirtualDisplay,
		ForwardPort: 27184,
		AppPackage:  "com.sec.android.app.popupcalculator",
	}); err != nil {
		t.Fatalf("Acquire app-1: %v", err)
	}

	// Simulate process restart by reopening from disk.
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.List(); len(got) != 2 {
		t.Fatalf("expected 2 persisted leases, got %d", len(got))
	}

	errBoom := errors.New("simulated failure")
	if err := reopened.RecoverAll(func(l Lease) error {
		if l.SessionID == "app-1" {
			return errBoom
		}
		return nil
	}); !errors.Is(err, errBoom) {
		t.Fatalf("expected partial failure errBoom, got %v", err)
	}

	rem := reopened.List()
	if len(rem) != 1 || rem[0].SessionID != "app-1" {
		t.Fatalf("expected app-1 retained after partial recovery, got %+v", rem)
	}
	if err := reopened.Release("app-1"); err != nil {
		t.Fatalf("Release app-1: %v", err)
	}
	if len(reopened.List()) != 0 {
		t.Fatalf("expected empty ledger after final release")
	}
}
