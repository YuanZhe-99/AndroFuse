package gui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// TestPeersForPhone checks that relays pass through and a wildcard listener becomes one dialable
// address per LAN IPv4 of this computer, never the wildcard itself.
func TestPeersForPhone(t *testing.T) {
	cfg := mesh.Config{
		Peers:     []string{"tcp://relay.example.org:11010"},
		Listeners: []string{"tcp://0.0.0.0:11010", "udp://192.0.2.7:11011"},
	}
	got := peersForPhone(cfg)
	if got[0] != "tcp://relay.example.org:11010" {
		t.Fatalf("relay must come first, got %v", got)
	}
	var lan int
	for _, p := range got {
		if strings.Contains(p, "0.0.0.0") {
			t.Fatalf("wildcard listener leaked to the phone: %v", got)
		}
		if strings.HasPrefix(p, "tcp://") && strings.HasSuffix(p, ":11010") && p != cfg.Peers[0] {
			lan++
		}
	}
	if lan != len(lanIPv4()) {
		t.Fatalf("want one address per LAN IPv4 (%d), got %d in %v", len(lanIPv4()), lan, got)
	}
	if got[len(got)-1] != "udp://192.0.2.7:11011" {
		t.Fatalf("specific listener must pass through unchanged, got %v", got)
	}
}

func TestFormatPairCode(t *testing.T) {
	if got := formatPairCode("123456"); got != "123 456" {
		t.Fatalf("formatPairCode(123456) = %q, want %q", got, "123 456")
	}
	if got := formatPairCode(" 98765 "); got != "98765" {
		t.Fatalf("formatPairCode(98765) = %q, want %q", got, "98765")
	}
}

func TestRememberMeshPhoneAndLosslessMergeWithADB(t *testing.T) {
	dir := t.TempDir()
	a := New("test", dir)
	res := companion.PairResponse{
		Token:           "tok-mesh-1", //gitleaks:allow
		CertFingerprint: "fp-mesh-abc",
		DeviceName:      "Galaxy Z Fold8",
		Manufacturer:    "samsung",
		Brand:           "samsung",
		AndroidRelease:  "17",
		MeshHost:        "AF-p-77889900 Galaxy Z Fold8",
		Port:            44123,
		Status: &companion.DeviceStatus{
			BatteryPct:      82,
			NetworkType:     "cellular",
			WirelessDebugOn: false,
		},
	}

	meshHash := a.rememberMeshPhone("77889900", "Pixel", res)
	if meshHash != "mesh:77889900" {
		t.Fatalf("rememberMeshPhone hash = %q, want mesh:77889900", meshHash)
	}
	r, ok := a.RememberedFor(meshHash)
	if !ok || r.Companion == nil || r.Maker != "samsung" || r.Android != "17" || r.Name != "Galaxy Z Fold8" {
		t.Fatalf("remembered mesh phone mismatch: %+v ok=%v", r, ok)
	}
	a.mu.Lock()
	for i := range a.remembered {
		if a.remembered[i].HardwareHash == meshHash {
			a.remembered[i].Alias = "My Fold"
			a.remembered[i].AutoReconnect = false
		}
	}
	// Simulate an unrelated remembered device whose marketing name is identical.
	a.remembered = append(a.remembered, RememberedDevice{
		HardwareHash: "other-real-hash",
		Name:         "Galaxy Z Fold8",
		Maker:        "samsung",
		Companion:    &CompanionBinding{Fingerprint: "fp-other", MeshHost: "AF-p-deadbeef Other"},
	})
	merged := a.mergeMeshRememberedLocked("realhw-serial-01", "fp-mesh-abc", "AF-p-77889900 Galaxy Z Fold8")
	a.mu.Unlock()
	if !merged {
		t.Fatal("expected mergeMeshRememberedLocked to merge mesh:77889900 into realhw-serial-01")
	}
	if _, stillMesh := a.RememberedFor(meshHash); stillMesh {
		t.Fatalf("provisional %s entry should be removed after merge", meshHash)
	}
	real, ok := a.RememberedFor("realhw-serial-01")
	if !ok || real.Alias != "My Fold" || real.AutoReconnect || real.Companion == nil || real.Companion.Fingerprint != "fp-mesh-abc" {
		t.Fatalf("merged device mismatch: %+v ok=%v", real, ok)
	}
	if _, otherStillPresent := a.RememberedFor("other-real-hash"); !otherStillPresent {
		t.Fatal("unrelated phone must stay intact after merge")
	}
}

func TestMeshPairEndToEndLoopback(t *testing.T) {
	var asked companion.PairRequest
	srv, err := companion.NewServer("", "SM-F971U1", "17", 37, companion.Handler{
		MeshHost:    func() string { return "AF-p-aabbccdd Fold8" },
		GetIdentity: func() (string, string, string) { return "Galaxy Z Fold8", "samsung", "samsung" },
		GetStatus: func() companion.DeviceStatus {
			return companion.DeviceStatus{
				BatteryPct:          91,
				NetworkType:         "wifi",
				WirelessADBPort:     41235,
				WirelessADBPairPort: 42345,
				USBDebugOn:          true,
				WirelessDebugOn:     true,
			}
		},
		OnPairRequest: func(req companion.PairRequest) bool {
			asked = req
			return req.UseSAS && companion.ValidPairCode(req.Code)
		},
		IssueToken: func(req companion.PairRequest) (string, error) {
			return "issued-" + req.Code, nil
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Listen("127.0.0.1:0"); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer func() { _ = srv.Close() }()

	var desktopCode string
	res, err := companion.RequestPairingSAS(ctx, srv.Addr(), "AndroFuse · TESTPC", func(code string) {
		desktopCode = code
	})
	if err != nil {
		t.Fatalf("RequestPairingSAS: %v", err)
	}
	if !companion.ValidPairCode(desktopCode) || !asked.UseSAS || asked.Code != desktopCode {
		t.Fatalf("SAS code mismatch desktop=%q phone=%+v", desktopCode, asked)
	}
	if res.Token != "issued-"+desktopCode || res.Manufacturer != "samsung" || res.Status == nil || res.Status.WirelessADBPairPort != 42345 {
		t.Fatalf("unexpected PairResponse: %+v", res)
	}

	a := New("test", t.TempDir())
	hash := a.rememberMeshPhone("aabbccdd", "Fallback", res)
	if hash != "mesh:aabbccdd" {
		t.Fatalf("hash = %q, want mesh:aabbccdd", hash)
	}
	if st, ok := a.peerStatusFor(hash); !ok || st.WirelessADBPairPort != 42345 {
		t.Fatalf("expected seeded peerStatus WirelessADBPairPort=42345, got %+v ok=%v", st, ok)
	}
}

func TestCacheKeySanitizationAndAppIconMigration(t *testing.T) {
	a := New("test", t.TempDir())
	meshHash := "mesh:0a1b2c3d"
	dir := a.deviceIconDir(meshHash)
	if strings.Contains(filepath.Base(dir), ":") {
		t.Fatalf("deviceIconDir leaked ':' on NTFS path: %q", dir)
	}
	if !a.needsCompanionAppFetch(meshHash) {
		t.Fatal("expected empty cache to need Companion app fetch")
	}
	apps := []adb.AppEntry{{Package: "com.android.chrome", Label: "Chrome", System: false, Resizeable: true}}
	a.saveDeviceApps(meshHash, apps)
	if !a.needsCompanionAppFetch(meshHash) {
		t.Fatal("expected app cache without PNG icon files to trigger Companion icon refresh")
	}
	// Seed cached PNG icon under mesh_0a1b2c3d and migrate to real HardwareHash.
	iconPath := filepath.Join(dir, "com.android.chrome.png")
	if err := os.WriteFile(iconPath, []byte{0x89, 'P', 'N', 'G'}, 0o600); err != nil {
		t.Fatalf("WriteFile icon: %v", err)
	}
	apps[0].IconPath = iconPath
	a.saveDeviceApps(meshHash, apps)
	if a.needsCompanionAppFetch(meshHash) {
		t.Fatal("expected app cache with non-empty PNG icon not to need icon refresh")
	}
	a.remembered = []RememberedDevice{{
		HardwareHash: meshHash,
		Name:         "Fold",
		Companion:    &CompanionBinding{Fingerprint: "fp-mig", MeshHost: "AF-p-0a1b2c3d Fold"},
	}}
	a.mu.Lock()
	merged := a.mergeMeshRememberedLocked("real-hw-123", "fp-mig", "AF-p-0a1b2c3d Fold")
	a.mu.Unlock()
	if !merged {
		t.Fatal("expected mergeMeshRememberedLocked to merge")
	}
	migrated := a.appsForDevice("real-hw-123")
	if len(migrated) != 1 || migrated[0].IconPath == "" {
		t.Fatalf("expected migrated app icon path, got %+v", migrated)
	}
	if _, err := os.Stat(migrated[0].IconPath); err != nil {
		t.Fatalf("expected migrated icon file to exist: %v", err)
	}
}

// TestBareAppListKeepsNamesAndIcons checks that a package-name-only list (the adb fallback) takes
// the names and icons already cached, and that such a list asks Enhanced mode for a new one.
func TestBareAppListKeepsNamesAndIcons(t *testing.T) {
	a := testApp(t)
	a.devices = nil // a computer that reaches the phone through Companion only
	hash := "0123456789abcdef"
	dir := a.deviceIconDir(hash)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	icon := filepath.Join(dir, "com.sec.android.app.clockpackage.png")
	if err := os.WriteFile(icon, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	cached := []adb.AppEntry{{Package: "com.sec.android.app.clockpackage", Label: "Clock", IconPath: icon, System: true}}
	bare := []adb.AppEntry{
		{Package: "com.sec.android.app.clockpackage", Label: "Clockpackage", Resizeable: true},
		{Package: "org.telegram.messenger", Label: "Messenger", Resizeable: true},
	}
	got := withCachedDetails(bare, cached)
	if got[0].Label != "Clock" || got[0].IconPath != icon || !got[0].System {
		t.Fatalf("cached name and icon must survive a bare list, got %+v", got[0])
	}
	if got[1].Label != "Messenger" || got[1].IconPath != "" {
		t.Fatalf("an app the cache lacks stays as listed, got %+v", got[1])
	}

	a.saveDeviceApps(hash, bare)
	if !a.needsCompanionAppFetch(hash) {
		t.Fatal("a list without icons must be fetched again")
	}
	a.saveDeviceApps(hash, got)
	if !a.needsCompanionAppFetch(hash) {
		t.Fatal("half the apps without icons must still be fetched again")
	}
	a.saveDeviceApps(hash, got[:1])
	if a.needsCompanionAppFetch(hash) {
		t.Fatal("a list with its icons needs no new fetch")
	}
}

// TestFetchAppsReportsFailure checks that a reload with no way to reach the phone says so, and
// that an automatic fetch waits after the last attempt instead of retrying every frame.
func TestFetchAppsReportsFailure(t *testing.T) {
	a := testApp(t)
	a.fetchApps("xiaomi-14", true)
	st := a.appsFetchFor("xiaomi-14")
	if st.Busy || st.At.IsZero() || !strings.Contains(st.Err, L.AppsLoadFailed()) {
		t.Fatalf("a failed reload must end with plain words, got %+v", st)
	}
	if logs := a.recentErrors(); len(logs) == 0 || !strings.Contains(logs[len(logs)-1], errNoAppSource.Error()) {
		t.Fatalf("Save logs must keep why, got %q", logs)
	}

	a.appsFetch["0123456789abcdef"] = appsFetchState{Err: "earlier", At: time.Now()}
	a.fetchApps("0123456789abcdef", false)
	if st := a.appsFetchFor("0123456789abcdef"); st.Err != "earlier" || st.Busy {
		t.Fatalf("an automatic fetch right after the last one must wait, got %+v", st)
	}
}

// TestAppPickerReloadAndFailure checks that the picker's app grid offers Reload apps and shows
// why the last fetch failed, also in place of the endless loading row when nothing is cached.
func TestAppPickerReloadAndFailure(t *testing.T) {
	for _, cachedList := range []bool{true, false} {
		a := testApp(t)
		a.devices = nil
		hash := "0123456789abcdef"
		if cachedList {
			a.saveDeviceApps(hash, []adb.AppEntry{{Package: "org.telegram.messenger", Label: "Telegram"}})
		}
		a.appsFetch[hash] = appsFetchState{Err: "Fetch failed here.", At: time.Now()}
		a.picker = pickerState{Open: true, Kind: openAsApp, Device: hash}
		tt := ui.NewTester(a.view, mainDefW, mainDefH)
		tt.Frame()
		tt.Frame()
		if _, ok := tt.Find(L.ActionReloadApps()); !ok {
			t.Fatalf("cached=%v: the app grid must offer %q", cachedList, L.ActionReloadApps())
		}
		if !tt.HasText("Fetch failed here.") {
			t.Fatalf("cached=%v: the app grid must say why the last fetch failed", cachedList)
		}
		if !cachedList && tt.HasText(L.PickerLoadingApps()) {
			t.Fatal("a failed fetch must replace the loading row")
		}
	}
}
