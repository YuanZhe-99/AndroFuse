package gui

import (
	"os"
	"strconv"
	"testing"

	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
)

// TestAddressParsing checks how Home's "Connect a phone" reads what the user types.
func TestAddressParsing(t *testing.T) {
	for _, tc := range []struct {
		in   string
		host string
		port int
		ok   bool
	}{
		{"100.64.0.12", "100.64.0.12", 0, true},
		{"192.168.1.20:41235", "192.168.1.20", 41235, true},
		{"phone.example.com", "phone.example.com", 0, true},
		{"[fd7a::1]:37000", "fd7a::1", 37000, true},
		{"192.168.1.20:99999", "", 0, false},
		{"  ", "", 0, false},
	} {
		h, p, ok := splitAddress(tc.in)
		if h != tc.host || p != tc.port || ok != tc.ok {
			t.Errorf("splitAddress(%q) = %q %d %v", tc.in, h, p, ok)
		}
	}
	for in, want := range map[string]bool{"100.64.0.12": true, "phone.example.com": true, "My Fold": false, "Pixel 8 Pro": false, "fd7a::1": true} {
		if got := looksLikeAddress(in); got != want {
			t.Errorf("looksLikeAddress(%q) = %v", in, got)
		}
	}
	if _, ok := validPort("41235"); !ok {
		t.Error("41235 is a valid port")
	}
	if _, ok := validPort("80"); ok {
		t.Error("ports below 1024 are refused")
	}
}

// TestPhoneLogos checks the maker matching: whole words, makers before product lines.
func TestPhoneLogos(t *testing.T) {
	for _, tc := range []struct {
		fields []string
		want   string
	}{
		{[]string{"samsung", "SM-F971U1"}, "samsung"},
		{[]string{"", "", "Galaxy Z Fold8"}, "samsung"},
		{[]string{"Google", "Pixel 8 Pro"}, "google"},
		{[]string{"Xiaomi", "23127PN0CC"}, "xiaomi"},
		{[]string{"", "redmi", "Redmi Note 13"}, "xiaomi"},
		{[]string{"", "", "Mi11 Ultra"}, "xiaomi"},
		{[]string{"", "", "Mint Phone"}, ""},
		{[]string{"motorola", "moto g"}, "lenovo"},
		{[]string{"OnePlus", "CPH2581"}, ""},
	} {
		if got := phoneLogoName(tc.fields...); got != tc.want {
			t.Errorf("phoneLogoName(%q) = %q, want %q", tc.fields, got, tc.want)
		}
	}
}

// TestDiagnosticsLeaveNetworksRunning guards against the self-check stopping the core it shares
// with the networks: the core's stop also stops every EasyTier network, which once left the
// computer's network down seconds after every launch. Needs the built core (ANDROFUSE_CORE_LIB
// or core/target).
func TestDiagnosticsLeaveNetworksRunning(t *testing.T) {
	if locateCoreLibrary() == "" {
		t.Skip("needs the built core library")
	}
	a := New("diag-test", t.TempDir())
	mgr, err := a.ensureMeshMgr()
	if err != nil {
		t.Fatal(err)
	}
	cfg := mesh.Config{Instance: "net-diag", Enabled: true, NetworkName: "androfuse-diag-" + strconv.Itoa(os.Getpid()), NetworkSecret: "diag", DeviceName: mesh.Hostname(mesh.KindPC, "d1a90001", "test")}
	if err := mgr.Start(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Stop("net-diag") })
	if d := a.hostDiagnostics(); !d.CoreLoaded {
		t.Fatalf("self-check did not use the core: %q", d.CoreError)
	}
	if !mgr.Status("net-diag").Running {
		t.Fatal("the self-check stopped the running network")
	}
}
