package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLibraryName(t *testing.T) {
	got := LibraryName()
	switch runtime.GOOS {
	case "windows":
		if got != "androfuse_core.dll" {
			t.Fatalf("LibraryName() = %q", got)
		}
	case "darwin":
		if got != "libandrofuse_core.dylib" {
			t.Fatalf("LibraryName() = %q", got)
		}
	default:
		if got != "libandrofuse_core.so" {
			t.Fatalf("LibraryName() = %q", got)
		}
	}
}

func TestSelfCheckJSON(t *testing.T) {
	raw := []byte(`{"os":"windows","process_arch":"arm64","native_arch":"arm64","is_emulated":false,"os_build":26300,"media_foundation_present":true,"h264_hw_decoder":true,"h264_any_decoder":true,"hevc_hw_decoder":true,"hevc_any_decoder":true,"wifi_direct_api":true,"miracast_ie_provider":true,"notes":[]}`)
	var sc SelfCheck
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sc.NativeArch != "arm64" || sc.IsEmulated || !sc.H264HWDecoder {
		t.Fatalf("unexpected SelfCheck: %+v", sc)
	}
}

func TestBuiltCoreFFI(t *testing.T) {
	candidates := []string{
		filepath.Join("..", "..", "core", "target", "debug", LibraryName()),
		filepath.Join("..", "..", "core", "target", "release", LibraryName()),
	}
	var libPath string
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			libPath = abs
			break
		}
	}
	if libPath == "" {
		t.Skipf("built %s not found; build core first", LibraryName())
	}

	lib, err := Open(libPath)
	if err != nil {
		t.Fatalf("Open(%q): %v", libPath, err)
	}
	defer lib.Stop()

	st, err := lib.Start(Config{Version: "0.1.0", StateDir: t.TempDir(), Profile: "test"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st.ABI != ABIVersion || st.Version != "0.1.0" {
		t.Fatalf("unexpected status: %+v", st)
	}

	sc, err := lib.SelfCheck()
	if err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	if sc.OS != runtime.GOOS {
		t.Fatalf("SelfCheck OS = %q, want %q", sc.OS, runtime.GOOS)
	}
	if runtime.GOOS == "windows" && runtime.GOARCH == "arm64" {
		if sc.NativeArch != "arm64" || sc.IsEmulated {
			t.Fatalf("expected native arm64 without emulation, got %+v", sc)
		}
	}
}
