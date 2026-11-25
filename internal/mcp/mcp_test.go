package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestServerDefaultDisabledAndAuthRequired(t *testing.T) {
	srv := NewServer(Handlers{}, time.Minute)
	if st := srv.Status(); st.Enabled {
		t.Fatalf("expected server disabled by default")
	}

	st, err := srv.Enable(Allowlist{ListDevices: true})
	if err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	defer func() { _ = srv.EmergencyStop() }()

	if !st.Enabled || !strings.HasPrefix(st.Addr, "127.0.0.1:") || st.Token == "" {
		t.Fatalf("unexpected enabled status: %+v", st)
	}

	url := "http://" + st.Addr
	reqBody := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	resp, err := http.Post(url, "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("unauthenticated post failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("wrong-token post failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-token status = %d, want 401", resp.StatusCode)
	}
}

func TestServerAllowlistEmergencyStopAndAuditScrubbing(t *testing.T) {
	calledLaunch := false
	srv := NewServer(Handlers{
		ListDevices: func(context.Context) (any, error) {
			return []string{"dev-alpha"}, nil
		},
		RunDiagnostics: func(context.Context) (any, error) {
			return map[string]any{"adb": true}, nil
		},
		LaunchApp: func(context.Context, string, string) (any, error) {
			calledLaunch = true
			return map[string]any{"launched": true}, nil
		},
	}, time.Minute)

	st, err := srv.Enable(Allowlist{ListDevices: true, RunDiagnostics: false, LaunchApp: false})
	if err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	url := "http://" + st.Addr

	// Call allowlisted tool.
	resBody := rpcCall(t, url, st.Token, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "list_devices"},
	})
	if !bytes.Contains(resBody, []byte("dev-alpha")) {
		t.Fatalf("expected list_devices result, got %s", resBody)
	}

	// Blocked by allowlist.
	blockedBody := rpcCall(t, url, st.Token, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "launch_app",
			"arguments": map[string]any{"device_id": "SENSITIVE-SERIAL", "package_name": "com.example.app"},
		},
	})
	if !bytes.Contains(blockedBody, []byte("tool disabled by allowlist")) || calledLaunch {
		t.Fatalf("expected launch_app blocked, got %s", blockedBody)
	}

	// Verify audit scrubbing does not contain bearer token or device serial.
	status := srv.Status()
	rawAudit, _ := json.Marshal(status.Audit)
	if bytes.Contains(rawAudit, []byte(st.Token)) || bytes.Contains(rawAudit, []byte("SENSITIVE-SERIAL")) {
		t.Fatalf("audit leaked secret or serial: %s", rawAudit)
	}

	if err := srv.EmergencyStop(); err != nil {
		t.Fatalf("EmergencyStop() error = %v", err)
	}
	if after := srv.Status(); after.Enabled || after.Token != "" {
		t.Fatalf("expected server stopped and token cleared, got %+v", after)
	}
}

func TestServerTTLExpiration(t *testing.T) {
	srv := NewServer(Handlers{}, 20*time.Millisecond)
	st, err := srv.Enable(Allowlist{ListDevices: true})
	if err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	time.Sleep(45 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodPost, "http://"+st.Addr, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+st.Token)
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("expected expired token rejection, got 200")
		}
	}
	if after := srv.Status(); after.Enabled {
		t.Fatalf("expected server disabled after TTL expiry")
	}
}

func rpcCall(t *testing.T, url, token string, payload map[string]any) []byte {
	t.Helper()
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("rpcCall error = %v", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return buf.Bytes()
}
