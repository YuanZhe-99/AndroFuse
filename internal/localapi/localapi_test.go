package localapi

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/lease"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
	"github.com/YuanZhe-99/AndroFuse/internal/vm"
)

func TestServerPingStatusAndLeases(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "svc.sock")
	store, err := lease.Open(filepath.Join(dir, "leases.json"))
	if err != nil {
		t.Fatalf("lease.Open: %v", err)
	}
	if err := store.Acquire(lease.Lease{
		SessionID:  "stale-1",
		DeviceHash: "deadbeef",
		Kind:       lease.KindMirror,
	}); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	vmMgr := vm.NewManager(filepath.Join(dir, "vm"), nil, nil)
	srv, err := Listen(Options{
		SocketPath: sock,
		StateDir:   dir,
		Profile:    "test",
		Leases:     store,
		VM:         vmMgr,
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	callCtx, callCancel := context.WithTimeout(ctx, 3*time.Second)
	defer callCancel()

	resp, err := Call(callCtx, sock, Request{ID: "1", Method: "ping"})
	if err != nil || !resp.OK {
		t.Fatalf("ping failed: resp=%+v err=%v", resp, err)
	}
	m := resp.Result.(map[string]any)
	if m["version"] != version.Version {
		t.Fatalf("unexpected ping version: %v", m["version"])
	}

	stResp, err := Call(callCtx, sock, Request{ID: "2", Method: "status"})
	if err != nil || !stResp.OK {
		t.Fatalf("status failed: resp=%+v err=%v", stResp, err)
	}

	recResp, err := Call(callCtx, sock, Request{ID: "3", Method: "leases.recover"})
	if err != nil || !recResp.OK {
		t.Fatalf("leases.recover failed: resp=%+v err=%v", recResp, err)
	}
	if rem := store.List(); len(rem) != 0 {
		t.Fatalf("expected 0 leases after recover, got %+v", rem)
	}

	vmResp, err := Call(callCtx, sock, Request{ID: "4", Method: "vm.status"})
	if err != nil || !vmResp.OK {
		t.Fatalf("vm.status failed: resp=%+v err=%v", vmResp, err)
	}
	vmResMap, ok := vmResp.Result.(map[string]any)
	if !ok || vmResMap["native_abi"] == "" {
		t.Fatalf("unexpected vm.status payload: %+v", vmResp.Result)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve exit error: %v", err)
	}
}
