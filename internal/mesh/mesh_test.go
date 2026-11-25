package mesh

import (
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/core"
)

// openBuiltCore loads core/target/debug's androfuse_core, skipping when it has not been built.
func openBuiltCore(t *testing.T) *core.Lib {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "core", "target", "debug", core.LibraryName()))
	if err != nil {
		t.Fatal(err)
	}
	lib, err := core.Open(path)
	if err != nil {
		t.Skipf("built core not available (%v); run cargo build first", err)
	}
	t.Cleanup(lib.Stop)
	if _, err := lib.Start(core.Config{Version: "0.1.0", StateDir: t.TempDir(), Profile: "mesh-test"}); err != nil {
		t.Fatalf("core.Start: %v", err)
	}
	return lib
}

// freePort returns a currently unused loopback TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestEmbeddedMeshLifecycle starts the real embedded EasyTier instance (no peers), checks that a
// port forward listens on loopback only, and that Stop closes it (`INV-07`).
func TestEmbeddedMeshLifecycle(t *testing.T) {
	m := NewManager(openBuiltCore(t))

	if st := m.Status(); st.Running || st.Engine == "" {
		t.Fatalf("fresh mesh must be stopped with an engine label, got %+v", st)
	}
	if err := m.Start(Config{NetworkName: "n"}); err == nil {
		t.Fatal("a disabled Config must be refused (INV-07)")
	}
	if err := m.Start(Config{Enabled: true, NetworkName: "n", PortForwards: []PortForward{{LocalPort: 1, RemoteHost: "phone.local", RemotePort: 2}}}); err == nil {
		t.Fatal("a non-IP forward target must be refused")
	}

	port := freePort(t)
	err := m.Start(Config{
		Enabled:       true,
		NetworkName:   "androfuse-test-" + strconv.Itoa(port),
		NetworkSecret: "test-secret",
		DeviceName:    "androfuse-test",
		PortForwards:  []PortForward{{LocalPort: port, RemoteHost: "10.126.126.2", RemotePort: 37000}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st := m.Status(); !st.Running {
		t.Fatalf("mesh not running after Start: %+v", st)
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	var conn net.Conn
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if conn, err = net.DialTimeout("tcp", addr, time.Second); err == nil {
			break
		}
	}
	if conn == nil {
		t.Fatalf("port forward %s is not listening: %v", addr, err)
	}
	conn.Close()

	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if st := m.Status(); st.Running {
		t.Fatalf("mesh still running after Stop: %+v", st)
	}
	time.Sleep(300 * time.Millisecond)
	if c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		c.Close()
		t.Fatalf("port forward %s still listening after Stop", addr)
	}
}
