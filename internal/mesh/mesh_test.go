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

	if st := m.Status(""); st.Running || st.Engine == "" {
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
	if st := m.Status(""); !st.Running {
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

	if err := m.Stop(""); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if st := m.Status(""); st.Running {
		t.Fatalf("mesh still running after Stop: %+v", st)
	}
	time.Sleep(300 * time.Millisecond)
	if c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		c.Close()
		t.Fatalf("port forward %s still listening after Stop", addr)
	}
}

// lanIPv4 returns this machine's outbound IPv4 address (nothing is sent), or skips.
func lanIPv4(t *testing.T) string {
	t.Helper()
	c, err := net.Dial("udp", "192.0.2.1:9")
	if err != nil {
		t.Skipf("no IPv4 route: %v", err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

// TestTwoNetworksSideBySide runs two instances of one network in this process, the second
// dialling the first on the LAN address (EasyTier binds outbound sockets to physical interfaces),
// and checks that each runs on its own and Info reports the other as a direct peer.
func TestTwoNetworksSideBySide(t *testing.T) {
	m := NewManager(openBuiltCore(t))
	port := freePort(t)
	base := Config{Enabled: true, NetworkName: "androfuse-two-" + strconv.Itoa(port), NetworkSecret: "two-networks"}
	a, b := base, base
	a.Instance, a.DeviceName, a.Listeners = "net-a", Hostname(KindPC, "0a1b2c3d", "STUDIO PC"), []string{"tcp://0.0.0.0:" + strconv.Itoa(port)}
	b.Instance, b.DeviceName, b.Peers = "net-b", "node-b", []string{"tcp://" + net.JoinHostPort(lanIPv4(t), strconv.Itoa(port))}
	if err := m.Start(a); err != nil {
		t.Fatalf("Start a: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop("net-a"); _ = m.Stop("net-b") })
	if err := m.Start(b); err != nil {
		t.Fatalf("Start b: %v", err)
	}
	var peer Peer
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline) && peer.Hostname == ""; time.Sleep(250 * time.Millisecond) {
		if info, ok := m.Info("net-b"); ok {
			for _, p := range info.Peers {
				if SameDevice(p.Hostname, Hostname(KindPC, "0a1b2c3d", "")) {
					peer = p
				}
			}
		}
	}
	if peer.Hostname == "" || !peer.Direct {
		t.Fatalf("net-b does not see net-a's node directly: %+v", peer)
	}
	if err := m.Stop("net-a"); err != nil {
		t.Fatal(err)
	}
	if m.Status("net-a").Running || !m.Status("net-b").Running {
		t.Fatal("stopping net-a must leave net-b running")
	}
}

// TestHostnameRoundTrip checks the compact AndroFuse hostname format, legacy parsing, and that
// other nodes (a public relay server, a plain EasyTier peer) are not taken for AndroFuse devices.
func TestHostnameRoundTrip(t *testing.T) {
	h := Hostname(KindPhone, "0a1b2c3d", "  Galaxy   Z Fold8 ")
	if h != "AF-p-0a1b2c3d Galaxy Z Fold8" {
		t.Fatalf("Hostname = %q", h)
	}
	if len(h) > 32 {
		t.Fatalf("Hostname exceeds EasyTier 32-char limit: %d (%q)", len(h), h)
	}
	if kind, id, name, ok := ParseHostname(h); !ok || kind != KindPhone || id != "0a1b2c3d" || name != "Galaxy Z Fold8" {
		t.Fatalf("ParseHostname(%q) = %q %q %q %v", h, kind, id, name, ok)
	}
	if kind, id, name, ok := ParseHostname("AndroFuse-phone-0a1b2c3d Fold"); !ok || kind != KindPhone || id != "0a1b2c3d" || name != "Fold" {
		t.Fatalf("legacy ParseHostname failed: %q %q %q %v", kind, id, name, ok)
	}
	if _, _, name, ok := ParseHostname(Hostname(KindPC, "ff", "")); !ok || name != "" {
		t.Fatalf("a nameless pc hostname did not parse")
	}
	for _, other := range []string{"PublicServer_CloudCone-S196V", "AF-", "AF-t-01 TV", "AF-p- x", "AndroFuse-", "AndroFuse-tv-01 TV", "AndroFuse-phone- x", "androfuse-desktop"} {
		if _, _, _, ok := ParseHostname(other); ok {
			t.Errorf("ParseHostname(%q) took it for an AndroFuse device", other)
		}
	}
	if a, b := NewDeviceID(), NewDeviceID(); len(a) != 8 || a == b {
		t.Fatalf("NewDeviceID = %q, %q", a, b)
	}
}

// TestSameDeviceIgnoresRenames checks that a renamed phone or legacy/compact format match is the
// same device and a relay is not.
func TestSameDeviceIgnoresRenames(t *testing.T) {
	if !SameDevice("AF-p-01ab Fold", "AF-p-01ab My Fold") {
		t.Fatal("a renamed phone was taken for another device")
	}
	if !SameDevice("AF-p-01ab Fold", "AndroFuse-phone-01ab My Fold") {
		t.Fatal("compact and legacy hostnames for the same phone did not match")
	}
	if SameDevice("AF-p-01ab Fold", "AF-c-01ab Fold") || SameDevice("AF-p-01ab", "Relay") {
		t.Fatal("different devices matched")
	}
	if !SameDevice("Relay", "Relay") {
		t.Fatal("equal plain hostnames did not match")
	}
}
