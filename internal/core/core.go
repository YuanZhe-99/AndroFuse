// Package core loads the Rust core library (androfuse_core) with purego on
// Unix/Android or LoadLibraryEx on Windows and calls its C ABI
// (doc/en-us/architecture.md, "FFI"): JSON control requests in/out and zero-copy
// planar NV12 frames into caller-owned ring buffers for ui.Bitmap.UpdateYUV.
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
const ABIVersion = 3

// Codec identifiers matching scrcpy-server v5.0.1 and androfuse-core.
const (
	CodecH264 uint32 = 0x68323634
	CodecH265 uint32 = 0x68323635
)

// Decoder PullNV12 status codes.
const (
	PullOK             = 0
	PullBufferTooSmall = 1
	PullNeedMoreInput  = 2
)

// Live Miracast receiver state values returned by MiracastHandle.PollInto.
const (
	MiracastListening   uint32 = 0
	MiracastNegotiating uint32 = 1
	MiracastStreaming   uint32 = 2
	MiracastStopped     uint32 = 3
)

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
	abiVersion    uintptr
	start         uintptr
	call          uintptr
	free          uintptr
	stop          uintptr
	decoderOpen   uintptr
	decoderPush   uintptr
	decoderPull   uintptr
	decoderClose  uintptr
	miracastStart uintptr
	miracastPoll  uintptr
	miracastInput uintptr
	miracastStop  uintptr
	mu            sync.RWMutex
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

// MiracastSinkTestResult holds the result of a passive-safe Wi-Fi Direct /
// Miracast IE Sink advertisement lifecycle test ("miracast.test_sink").
type MiracastSinkTestResult struct {
	Port7236Available bool     `json:"port_7236_available"`
	PublisherStarted  bool     `json:"publisher_started"`
	PublisherStatus   string   `json:"publisher_status"`
	WFDHandleOpened   bool     `json:"wfd_handle_opened"`
	IEEnableError     int32    `json:"ie_enable_error"`
	IESinkError       int32    `json:"ie_sink_error"`
	CleanedUp         bool     `json:"cleaned_up"`
	Notes             []string `json:"notes"`
}

// FrameMeta mirrors C-ABI `FrameMeta` populated by `androfuse_decoder_pull_nv12`.
type FrameMeta struct {
	Width          uint32
	Height         uint32
	YStride        uint32
	UVStride       uint32
	UVOffset       uint32
	TotalLen       uint32
	PtsUs          uint64
	DecodeUs       uint32
	Hardware       uint8
	SurfaceChanged uint8
	Reserved       [2]uint8
}

// NV12Frame references the front NV12 slice pair inside an NV12Ring.
//
// Notes: Planes stay valid without copying for ui.Bitmap.UpdateYUV until the
// next AcquireBack/Commit cycle.
type NV12Frame struct {
	Width    int
	Height   int
	Y        []byte
	UV       []byte
	YStride  int
	UVStride int
	PtsUs    uint64
	DecodeUs uint32
	Hardware bool
}

// NV12Ring maintains two reusable byte buffers so a background decode goroutine
// writes into the back slot while the UI thread samples the front slot.
//
// Notes: Steady-state frames perform zero heap allocations once warmed to size.
type NV12Ring struct {
	slots [2][]byte
	front int
}

// NextBack returns a writable capacity-reused back buffer of at least minCap bytes.
func (r *NV12Ring) NextBack(minCap int) []byte {
	back := 1 - r.front
	if cap(r.slots[back]) < minCap {
		r.slots[back] = make([]byte, minCap)
	} else {
		r.slots[back] = r.slots[back][:minCap]
	}
	return r.slots[back]
}

// Commit swaps front and back slots and returns an NV12Frame view over the new front slot.
func (r *NV12Ring) Commit(meta FrameMeta) NV12Frame {
	back := 1 - r.front
	r.front = back
	buf := r.slots[back][:meta.TotalLen]
	return NV12Frame{
		Width:    int(meta.Width),
		Height:   int(meta.Height),
		Y:        buf[:meta.UVOffset],
		UV:       buf[meta.UVOffset:meta.TotalLen],
		YStride:  int(meta.YStride),
		UVStride: int(meta.UVStride),
		PtsUs:    meta.PtsUs,
		DecodeUs: meta.DecodeUs,
		Hardware: meta.Hardware != 0,
	}
}

// Decoder wraps an opaque native Media Foundation H.264/HEVC decoder handle.
type Decoder struct {
	lib    *Lib
	handle uint64
	mu     sync.Mutex
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
	if l.decoderOpen, err = resolve("androfuse_decoder_open"); err != nil {
		return nil, err
	}
	if l.decoderPush, err = resolve("androfuse_decoder_push"); err != nil {
		return nil, err
	}
	if l.decoderPull, err = resolve("androfuse_decoder_pull_nv12"); err != nil {
		return nil, err
	}
	if l.decoderClose, err = resolve("androfuse_decoder_close"); err != nil {
		return nil, err
	}
	if l.miracastStart, err = resolve("androfuse_miracast_start"); err != nil {
		return nil, err
	}
	if l.miracastPoll, err = resolve("androfuse_miracast_poll_nv12"); err != nil {
		return nil, err
	}
	if l.miracastInput, err = resolve("androfuse_miracast_input"); err != nil {
		return nil, err
	}
	if l.miracastStop, err = resolve("androfuse_miracast_stop"); err != nil {
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

// MiracastTestSink starts and immediately tears down Windows Wi-Fi Direct
// Sink + IE provider advertisement to verify wireless DeX driver readiness.
func (l *Lib) MiracastTestSink() (MiracastSinkTestResult, error) {
	var out MiracastSinkTestResult
	if err := l.Call("miracast.test_sink", nil, &out); err != nil {
		return MiracastSinkTestResult{}, err
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

// OpenDecoder creates a native H.264 or HEVC decoder initialized to width x height.
func (l *Lib) OpenDecoder(codecID, width, height uint32) (*Decoder, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var handle uint64
	rc := int32(syscallN(
		l.decoderOpen,
		uintptr(codecID),
		uintptr(width),
		uintptr(height),
		uintptr(unsafe.Pointer(&handle)),
	))
	if rc != 0 || handle == 0 {
		return nil, fmt.Errorf("androfuse_decoder_open(0x%08x, %dx%d) failed: rc=%d", codecID, width, height, rc)
	}
	return &Decoder{lib: l, handle: handle}, nil
}

// Push submits one encoded video access unit to the decoder.
func (d *Decoder) Push(data []byte, ptsUs uint64, discontinuity bool) error {
	var flags uint32
	if discontinuity {
		flags = 1
	}
	return d.PushFlags(data, ptsUs, flags)
}

// Flush drains queued frames from the decoder without closing it.
func (d *Decoder) Flush() error {
	return d.PushFlags(nil, 0, 2)
}

// PushFlags submits an encoded access unit with raw C-ABI flags (bit 0: discontinuity, bit 1: drain).
func (d *Decoder) PushFlags(data []byte, ptsUs uint64, flags uint32) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handle == 0 {
		return errors.New("decoder closed")
	}
	var ptr uintptr
	if len(data) > 0 {
		ptr = uintptr(unsafe.Pointer(unsafe.SliceData(data)))
	}
	rc := int32(syscallN(
		d.lib.decoderPush,
		uintptr(d.handle),
		ptr,
		uintptr(len(data)),
		uintptr(ptsUs),
		uintptr(flags),
	))
	runtime.KeepAlive(data)
	if rc != 0 {
		return fmt.Errorf("androfuse_decoder_push failed: rc=%d", rc)
	}
	return nil
}

// PullInto pulls the latest decoded NV12 frame into ring without allocating in steady state.
//
// Returns: (frame, true, nil) when a frame was produced; (_, false, nil) when more input is needed.
func (d *Decoder) PullInto(ring *NV12Ring) (NV12Frame, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handle == 0 {
		return NV12Frame{}, false, errors.New("decoder closed")
	}

	var meta FrameMeta
	buf := ring.NextBack(0)
	if cap(buf) > 0 {
		buf = buf[:cap(buf)]
	}
	var dstPtr uintptr
	if len(buf) > 0 {
		dstPtr = uintptr(unsafe.Pointer(unsafe.SliceData(buf)))
	}
	rc := int32(syscallN(
		d.lib.decoderPull,
		uintptr(d.handle),
		dstPtr,
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&meta)),
	))
	runtime.KeepAlive(buf)

	if rc == PullBufferTooSmall && meta.TotalLen > 0 {
		buf = ring.NextBack(int(meta.TotalLen))
		dstPtr = uintptr(unsafe.Pointer(unsafe.SliceData(buf)))
		rc = int32(syscallN(
			d.lib.decoderPull,
			uintptr(d.handle),
			dstPtr,
			uintptr(len(buf)),
			uintptr(unsafe.Pointer(&meta)),
		))
		runtime.KeepAlive(buf)
	}

	switch rc {
	case PullOK:
		return ring.Commit(meta), true, nil
	case PullNeedMoreInput:
		return NV12Frame{}, false, nil
	default:
		return NV12Frame{}, false, fmt.Errorf("androfuse_decoder_pull_nv12 failed: rc=%d", rc)
	}
}

// Close releases the native decoder handle.
func (d *Decoder) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handle == 0 {
		return
	}
	syscallN(d.lib.decoderClose, uintptr(d.handle))
	d.handle = 0
}

// MiracastHandle wraps an active Windows Wi-Fi Direct / Miracast DeX receiver in androfuse_core.
type MiracastHandle struct {
	lib    *Lib
	handle uint64
	mu     sync.Mutex
}

// StartMiracast starts a Miracast sink listener on rtspPort (0 uses 7236).
func (l *Lib) StartMiracast(rtspPort uint16) (*MiracastHandle, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var handle uint64
	rc := int32(syscallN(l.miracastStart, uintptr(rtspPort), uintptr(unsafe.Pointer(&handle))))
	if rc != 0 || handle == 0 {
		return nil, fmt.Errorf("androfuse_miracast_start(%d) failed: rc=%d", rtspPort, rc)
	}
	return &MiracastHandle{lib: l, handle: handle}, nil
}

// PollInto polls the latest decoded Miracast NV12 frame into ring and reports receiver state.
func (m *MiracastHandle) PollInto(ring *NV12Ring) (NV12Frame, uint32, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handle == 0 {
		return NV12Frame{}, MiracastStopped, false, errors.New("miracast handle closed")
	}
	var meta FrameMeta
	var state uint32
	buf := ring.NextBack(0)
	if cap(buf) > 0 {
		buf = buf[:cap(buf)]
	}
	var dstPtr uintptr
	if len(buf) > 0 {
		dstPtr = uintptr(unsafe.Pointer(unsafe.SliceData(buf)))
	}
	rc := int32(syscallN(
		m.lib.miracastPoll,
		uintptr(m.handle),
		dstPtr,
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&meta)),
		uintptr(unsafe.Pointer(&state)),
	))
	runtime.KeepAlive(buf)
	if rc == PullBufferTooSmall && meta.TotalLen > 0 {
		buf = ring.NextBack(int(meta.TotalLen))
		dstPtr = uintptr(unsafe.Pointer(unsafe.SliceData(buf)))
		rc = int32(syscallN(
			m.lib.miracastPoll,
			uintptr(m.handle),
			dstPtr,
			uintptr(len(buf)),
			uintptr(unsafe.Pointer(&meta)),
			uintptr(unsafe.Pointer(&state)),
		))
		runtime.KeepAlive(buf)
	}
	switch rc {
	case PullOK:
		return ring.Commit(meta), state, true, nil
	case PullNeedMoreInput:
		return NV12Frame{}, state, false, nil
	default:
		return NV12Frame{}, state, false, fmt.Errorf("androfuse_miracast_poll_nv12 failed: rc=%d", rc)
	}
}

// SendPointer forwards a UIBC GENERIC pointer action (`0=down, 1=up, 2=move`) in normalized [0..65535] coordinates.
func (m *MiracastHandle) SendPointer(action uint8, xNorm, yNorm uint16) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handle == 0 {
		return errors.New("miracast handle closed")
	}
	rc := int32(syscallN(m.lib.miracastInput, uintptr(m.handle), uintptr(action), uintptr(xNorm), uintptr(yNorm)))
	if rc != 0 {
		return fmt.Errorf("androfuse_miracast_input failed: rc=%d", rc)
	}
	return nil
}

// Close tears down the Miracast sink advertisement, RTSP/RTP sockets, and IE state cleanly.
func (m *MiracastHandle) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handle == 0 {
		return
	}
	syscallN(m.lib.miracastStop, uintptr(m.handle))
	m.handle = 0
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
