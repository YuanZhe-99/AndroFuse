package mesh

import (
	"context"
	"strings"
	"testing"
)

func TestBuildArgsAndScrub(t *testing.T) {
	if _, err := BuildArgs(Config{Enabled: false, NetworkName: "net"}); err == nil {
		t.Fatal("expected disabled Config to be rejected (INV-07)")
	}
	args, err := BuildArgs(Config{
		Enabled:       true,
		NetworkName:   "androfuse-mesh",
		NetworkSecret: "super-secret-value",
		DeviceName:    "win-arm64",
		Peers:         []string{"tcp://203.0.113.10:11010"},
		PortForwards: []PortForward{
			{LocalPort: 27189, RemoteHost: "10.144.144.2", RemotePort: 27189},
		},
	})
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--no-tun") || !strings.Contains(joined, "--port-forward tcp://127.0.0.1:27189/10.144.144.2:27189") {
		t.Fatalf("unexpected args: %v", args)
	}
	scrubbed := strings.Join(ScrubArgs(args), " ")
	if strings.Contains(scrubbed, "super-secret-value") || strings.Contains(scrubbed, "203.0.113.10") {
		t.Fatalf("ScrubArgs leaked sensitive secret or peer IP: %s", scrubbed)
	}

	mgr := NewManager()
	if err := mgr.Start(context.Background(), Config{Enabled: false}); err == nil {
		t.Fatal("expected Start with disabled config to fail")
	}
}
