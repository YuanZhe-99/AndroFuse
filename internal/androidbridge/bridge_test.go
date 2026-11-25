package androidbridge

import (
	"testing"
	"time"
)

func TestProbeSafeByDefault(t *testing.T) {
	c := Probe()
	if c.Platform == "" {
		t.Fatalf("expected non-empty Platform")
	}

	SyncCompanionWidget(true, 41235, 86)
	ws := CurrentWidgetState()
	if !ws.CompanionActive || ws.WirelessADBPort != 41235 || ws.BatteryPct != 86 {
		t.Fatalf("unexpected widget state: %+v", ws)
	}
}

// TestActivityLeaseReleasesWhenIdle checks the wake/Wi-Fi lease is held only while transfers
// keep touching it and drops on its own after the idle window (PLAN §9.1-3).
func TestActivityLeaseReleasesWhenIdle(t *testing.T) {
	old := activeIdle
	activeIdle = 80 * time.Millisecond
	defer func() { activeIdle = old }()

	if ActiveHeld() {
		t.Fatal("lease must start released")
	}
	TouchActive()
	for range 3 {
		time.Sleep(40 * time.Millisecond)
		TouchActive() // continuing activity keeps it held
		if !ActiveHeld() {
			t.Fatal("lease dropped while activity continued")
		}
	}
	time.Sleep(200 * time.Millisecond)
	if ActiveHeld() {
		t.Fatal("lease still held after the idle window")
	}
	TouchActive()
	ReleaseActive()
	if ActiveHeld() {
		t.Fatal("ReleaseActive must drop the lease immediately")
	}
}
