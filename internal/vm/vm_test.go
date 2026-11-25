package vm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeABIForArch(t *testing.T) {
	abi, pkg, err := NativeABIForArch("arm64")
	if err != nil || abi != "arm64-v8a" || !strings.Contains(pkg, "arm64-v8a") {
		t.Fatalf("arm64 mismatch: abi=%q pkg=%q err=%v", abi, pkg, err)
	}
	abi, pkg, err = NativeABIForArch("amd64")
	if err != nil || abi != "x86_64" || !strings.Contains(pkg, "x86_64") {
		t.Fatalf("amd64 mismatch: abi=%q pkg=%q err=%v", abi, pkg, err)
	}
	if _, _, err := NativeABIForArch("386"); err == nil {
		t.Fatal("expected error for unsupported 386 architecture")
	}
}

func TestParseCmdlineToolsManifest(t *testing.T) {
	const fixture = `<?xml version="1.0" encoding="UTF-8"?>
<repository xmlns="http://schemas.android.com/sdk/android/repo/repository2/03">
  <remotePackage path="cmdline-tools;latest">
    <archives>
      <archive>
        <complete>
          <size>123456</size>
          <checksum type="sha256">abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789</checksum>
          <url>commandlinetools-linux-latest.zip</url>
        </complete>
        <host-os>linux</host-os>
      </archive>
      <archive>
        <complete>
          <size>987654</size>
          <checksum type="sha256">0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef</checksum>
          <url>commandlinetools-win-999999_latest.zip</url>
        </complete>
        <host-os>windows</host-os>
      </archive>
    </archives>
  </remotePackage>
</repository>`

	ar, err := ParseCmdlineToolsManifest([]byte(fixture))
	if err != nil {
		t.Fatalf("ParseCmdlineToolsManifest: %v", err)
	}
	wantURL := "https://dl.google.com/android/repository/commandlinetools-win-999999_latest.zip"
	if ar.URL != wantURL || ar.ChecksumType != "sha256" || ar.Size != 987654 {
		t.Fatalf("unexpected parsed archive: %+v", ar)
	}
}

func TestManagerStatusAndDeleteManaged(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vm")
	sample := filepath.Join(root, "sdk", "sample.bin")
	if err := os.MkdirAll(filepath.Dir(sample), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sample, make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(root, nil, nil)
	st := mgr.Status(context.Background())
	if st.VMRoot != root || st.StorageBytes < 2048 {
		t.Fatalf("unexpected managed VM status: %+v", st)
	}
	if err := mgr.DeleteManaged(context.Background()); err != nil {
		t.Fatalf("DeleteManaged: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("expected managed root removed, err=%v", err)
	}
	stAfter := mgr.Status(context.Background())
	if stAfter.StorageBytes != 0 {
		t.Fatalf("expected StorageBytes == 0 after DeleteManaged, got %d", stAfter.StorageBytes)
	}
}

// TestLiveHeadlessVMStartStop boots an installed AVD headlessly (`-no-window`),
// waits for `sys.boot_completed=1`, and shuts it down cleanly via `Manager.Stop`.
func TestLiveHeadlessVMStartStop(t *testing.T) {
	if os.Getenv("ANDROFUSE_LIVE_VM") != "1" {
		t.Skip("set ANDROFUSE_LIVE_VM=1 with a local AVD (e.g. Pixel_9a_API_36) to test headless VM boot & teardown")
	}
	mgr := NewManager(t.TempDir(), nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	initSt := mgr.Status(ctx)
	if len(initSt.AvailableAVDs) == 0 {
		t.Skipf("no AVD available on host (%+v)", initSt)
	}
	t.Logf("Booting headless AVD=%s (abi=%s hostSDK=%v)", initSt.AVDName, initSt.NativeABI, initSt.UsingHostSDK)

	st, err := mgr.Start(ctx, initSt.AVDName)
	if err != nil {
		t.Fatalf("Manager.Start: %v", err)
	}
	defer func() {
		_ = mgr.Stop(context.Background())
	}()
	if st.State != StateRunning || st.RunningSerial == "" {
		t.Fatalf("expected StateRunning with serial, got %+v", st)
	}
	t.Logf("Headless VM LIVE PASS: serial=%s avd=%s", st.RunningSerial, st.AVDName)

	if err := mgr.Stop(context.Background()); err != nil {
		t.Fatalf("Manager.Stop: %v", err)
	}
	after := mgr.Status(context.Background())
	if after.State == StateRunning || after.RunningSerial != "" {
		t.Fatalf("expected clean VM shutdown without residual emulator, got %+v", after)
	}
}
