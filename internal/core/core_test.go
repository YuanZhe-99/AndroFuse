package core

import (
	"encoding/base64"
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

func TestNV12RingZeroAllocSteadyState(t *testing.T) {
	var ring NV12Ring
	const w, h = 64, 64
	const total = w*h + w*(h/2)

	// Warm both slots once.
	for range 2 {
		buf := ring.NextBack(total)
		buf[0] = 0x10
		buf[w*h] = 0x80
		ring.Commit(FrameMeta{
			Width:    w,
			Height:   h,
			YStride:  w,
			UVStride: w,
			UVOffset: w * h,
			TotalLen: total,
		})
	}

	allocs := testing.AllocsPerRun(100, func() {
		buf := ring.NextBack(total)
		buf[0] = 0x20
		frame := ring.Commit(FrameMeta{
			Width:    w,
			Height:   h,
			YStride:  w,
			UVStride: w,
			UVOffset: w * h,
			TotalLen: total,
		})
		if len(frame.Y) != w*h || len(frame.UV) != w*(h/2) {
			t.Fatalf("unexpected NV12 plane lengths: y=%d uv=%d", len(frame.Y), len(frame.UV))
		}
	})
	if allocs != 0 {
		t.Fatalf("steady-state NV12Ring allocated %v times per frame, want 0", allocs)
	}
}

func findBuiltLib() string {
	candidates := []string{
		filepath.Join("..", "..", "core", "target", "debug", LibraryName()),
		filepath.Join("..", "..", "core", "target", "release", LibraryName()),
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return ""
}

func TestBuiltCoreFFI(t *testing.T) {
	libPath := findBuiltLib()
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

	if runtime.GOOS == "windows" {
		dec, err := lib.OpenDecoder(CodecH264, 64, 48)
		if err != nil {
			t.Fatalf("OpenDecoder(H264): %v", err)
		}
		defer dec.Close()

		rawFix, err := os.ReadFile(filepath.Join("..", "scrcpy", "testdata", "fold8_scrcpy_session.json"))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		var fix struct {
			Packets []struct {
				IsSession  bool   `json:"is_session"`
				IsConfig   bool   `json:"is_config"`
				IsKeyframe bool   `json:"is_keyframe"`
				PtsUs      uint64 `json:"pts_us"`
				PayloadB64 string `json:"payload_b64"`
			} `json:"packets"`
		}
		if err := json.Unmarshal(rawFix, &fix); err != nil {
			t.Fatalf("unmarshal fixture: %v", err)
		}
		var csd []byte
		var ring NV12Ring
		decoded := 0
		for _, p := range fix.Packets {
			if p.IsSession {
				continue
			}
			b, err := base64.StdEncoding.DecodeString(p.PayloadB64)
			if err != nil {
				t.Fatalf("decode b64: %v", err)
			}
			if p.IsConfig {
				csd = b
				continue
			}
			au := b
			if p.IsKeyframe {
				au = append(append([]byte(nil), csd...), b...)
			}
			if err := dec.Push(au, p.PtsUs, decoded == 0 && p.IsKeyframe); err != nil {
				t.Fatalf("Push: %v", err)
			}
			if frame, ok, err := dec.PullInto(&ring); err != nil {
				t.Fatalf("PullInto: %v", err)
			} else if ok {
				if frame.Width != 64 || frame.Height != 48 {
					t.Fatalf("unexpected decoded frame size: %dx%d", frame.Width, frame.Height)
				}
				decoded++
			}
		}
		if err := dec.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}
		if frame, ok, err := dec.PullInto(&ring); err != nil {
			t.Fatalf("PullInto after Flush: %v", err)
		} else if ok {
			if frame.Width != 64 || frame.Height != 48 {
				t.Fatalf("unexpected flushed frame size: %dx%d", frame.Width, frame.Height)
			}
			decoded++
		}
		if decoded == 0 {
			t.Fatal("expected >= 1 decoded 64x48 NV12 frame")
		}
	}
}
