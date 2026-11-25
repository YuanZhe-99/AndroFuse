package androidbridge

import "testing"

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
