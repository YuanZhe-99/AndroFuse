package androidbridge

import "testing"

func TestProbeSafeByDefault(t *testing.T) {
	c := Probe()
	if c.Platform == "" {
		t.Fatalf("expected non-empty Platform")
	}
}
