// Package core loads the Rust core library (androfuse_core) with purego on
// Unix/Android or LoadLibraryEx on Windows and calls its C ABI
// (doc/en-us/architecture.md, "FFI"): JSON requests in, JSON responses out,
// integers and pointers only.
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"
)

// ABIVersion is the C ABI this package speaks; the library must report the
// same one.
const ABIVersion = 1

// LibraryName returns the dynamic library file name for the current platform.
func LibraryName() string {
	switch runtime.GOOS {
	case "windows":
		return "androfuse_core.dll"
	case "darwin":
		return "libandrofuse_core.dylib"
	default:
		return "libandrofuse_core.so"
	}
}

// LibraryPath returns the expected core library path next to an executable
// directory.
func LibraryPath(dir string) string {
	return filepath.Join(dir, LibraryName())
}

// Lib is a loaded instance of the Rust core library.
type Lib struct {
	abiVersion uintptr
	start      uintptr
	call       uintptr
	free       uintptr
	stop       uintptr
	mu         sync.RWMutex
}

// outSlot receives one call's response buffer on the heap rather than a
// goroutine stack, which may move between taking an address and the C call.
type outSlot struct {
	ptr *byte
	len uintptr
}

var outSlots = sync.Pool{New: func() any { return new(outSlot) }}

// Config is passed to androfuse_start to initialize the core.
type Config struct {
	Version  string `json:"version"`
	StateDir string `json:"state_dir"`
	Profile  string `json:"profile,omitempty"`
}

// Status describes the running core state returned by the "status" method.
type Status struct {
	Version  string `json:"version"`
	ABI      uint32 `json:"abi"`
	StateDir string `json:"state_dir"`
	Profile  string `json:"profile"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

// SelfCheck describes host architecture, Media Foundation hardware decode,
// and Wi-Fi Direct / Miracast readiness reported by "diagnostics.self_check".
type SelfCheck struct {
	OS                     string   `json:"os"`
	ProcessArch            string   `json:"process_arch"`
	NativeArch             string   `json:"native_arch"`
	IsEmulated             bool     `json:"is_emulated"`
	OSBuild                uint32   `json:"os_build"`
	MediaFoundationPresent bool     `json:"media_foundation_present"`
	H264HWDecoder          bool     `json:"h264_hw_decoder"`
	H264AnyDecoder         bool     `json:"h264_any_decoder"`
	HEVCHWDecoder          bool     `json:"hevc_hw_decoder"`
	HEVCAnyDecoder         bool     `json:"hevc_any_decoder"`
	WiFiDirectAPI          bool     `json:"wifi_direct_api"`
	MiracastIEProvider     bool     `json:"miracast_ie_provider"`
	Notes                  []string `json:"notes"`
}

type callEnvelope struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rawResponse struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// Open loads the core library at path and verifies its ABI version.
//
// Inputs: path, absolute path or Android SONAME.
// Returns: loaded Lib handle, or an error on missing symbols or ABI mismatch.
func Open(path string) (*Lib, error) {
	h, err := open(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	l := &Lib{}
	resolve := func(name string) (uintptr, error) {
		p, err := symbol(h, name)
		if err != nil {
			return 0, fmt.Errorf("symbol %s: %w", name, err)
		}
		return p, nil
	}
	if l.abiVersion, err = resolve("androfuse_abi_version"); err != nil {
		return nil, err
	}
	if l.start, err = resolve("androfuse_start"); err != nil {
		return nil, err
	}
	if l.call, err = resolve("androfuse_call"); err != nil {
		return nil, err
	}
	if l.free, err = resolve("androfuse_free"); err != nil {
		return nil, err
	}
	if l.stop, err = resolve("androfuse_stop"); err != nil {
		return nil, err
	}
	abi := uint32(syscallN(l.abiVersion))
	if abi != ABIVersion {
		return nil, fmt.Errorf("core ABI %d != expected %d", abi, ABIVersion)
	}
	return l, nil
}

// Start initializes the core with cfg.
func (l *Lib) Start(cfg Config) (Status, error) {
	req, err := json.Marshal(cfg)
	if err != nil {
		return Status{}, err
	}
	var out Status
	if err := l.invoke(l.start, req, &out); err != nil {
		return Status{}, err
	}
	return out, nil
}

// SelfCheck runs host environment self-diagnostics in the core.
func (l *Lib) SelfCheck() (SelfCheck, error) {
	var out SelfCheck
	if err := l.Call("diagnostics.self_check", nil, &out); err != nil {
		return SelfCheck{}, err
	}
	return out, nil
}

// Status returns the initialized core status.
func (l *Lib) Status() (Status, error) {
	var out Status
	if err := l.Call("status", nil, &out); err != nil {
		return Status{}, err
	}
	return out, nil
}

// Call invokes a JSON method on the core and decodes its result into out.
func (l *Lib) Call(method string, params any, out any) error {
	req, err := json.Marshal(callEnvelope{Method: method, Params: params})
	if err != nil {
		return err
	}
	return l.invoke(l.call, req, out)
}

// Stop stops the core and waits for in-flight calls to finish.
func (l *Lib) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	syscallN(l.stop)
}

func (l *Lib) invoke(fn uintptr, req []byte, out any) error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	slot := outSlots.Get().(*outSlot)
	slot.ptr = nil
	slot.len = 0
	defer func() {
		if slot.ptr != nil {
			syscallN(l.free, uintptr(unsafe.Pointer(slot.ptr)), slot.len)
		}
		slot.ptr = nil
		slot.len = 0
		outSlots.Put(slot)
	}()

	var inPtr uintptr
	if len(req) > 0 {
		inPtr = uintptr(unsafe.Pointer(unsafe.SliceData(req)))
	}
	rc := int32(syscallN(
		fn,
		inPtr,
		uintptr(len(req)),
		uintptr(unsafe.Pointer(&slot.ptr)),
		uintptr(unsafe.Pointer(&slot.len)),
	))
	runtime.KeepAlive(req)
	if rc != 0 || slot.ptr == nil {
		return fmt.Errorf("core call failed (rc=%d)", rc)
	}
	raw := unsafe.Slice(slot.ptr, int(slot.len))
	var env rawResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode core response: %w", err)
	}
	if !env.OK {
		if env.Error == "" {
			return errors.New("core returned unspecified error")
		}
		return errors.New(env.Error)
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("decode core result: %w", err)
		}
	}
	return nil
}
