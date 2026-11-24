// Package scrcpy implements the scrcpy-server v5.0.1 wire protocol,
// session lifecycle, virtual-display/flex-display orchestration, and sanitized
// protocol replay fixtures for offline tests.
package scrcpy

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ServerVersion is the pinned upstream scrcpy-server wire protocol version.
	ServerVersion = "5.0.1"
	// RemoteServerPath is the non-world-writable device location used for app_process.
	RemoteServerPath = "/data/local/tmp/androfuse-scrcpy-server.jar"
	// ExpectedServerSHA256 is the verified SHA-256 digest of official scrcpy-server-v5.0.1.
	ExpectedServerSHA256 = "764eb6f79811d5211fe9df341120882ba9994c7a61b897d7bf3fb662e53bc536"

	DeviceNameLen      = 64
	PacketHeaderLen    = 12
	MaxMediaPacketSize = 32 * 1024 * 1024
)

// Video/audio codec wire identifiers sent on socket handshake.
const (
	CodecH264 uint32 = 0x68323634
	CodecH265 uint32 = 0x68323635
	CodecAV1  uint32 = 0x00617631
	CodecOpus uint32 = 0x6f707573
	CodecAAC  uint32 = 0x00616163
	CodecFLAC uint32 = 0x666c6163
	CodecRaw  uint32 = 0x00726177
)

// Scrcpy v5.0.1 ControlMessage type IDs (ControlMessage.java).
const (
	MsgInjectKeycode  byte = 0
	MsgInjectText     byte = 1
	MsgInjectTouch    byte = 2
	MsgInjectScroll   byte = 3
	MsgBackOrScreenOn byte = 4
	MsgGetClipboard   byte = 8
	MsgSetClipboard   byte = 9
	MsgSetDisplayPow  byte = 10
	MsgRotateDevice   byte = 11
	MsgUhidCreate     byte = 12
	MsgUhidInput      byte = 13
	MsgUhidDestroy    byte = 14
	MsgStartApp       byte = 16
	MsgResetVideo     byte = 17
	MsgResizeDisplay  byte = 21
)

// Android MotionEvent action, button, and special pointer IDs used by MsgInjectTouch.
const (
	ActionDown          byte = 0
	ActionUp            byte = 1
	ActionMove          byte = 2
	ActionHoverMove     byte = 7
	ActionButtonPress   byte = 11
	ActionButtonRelease byte = 12

	PointerIDMouse         uint64 = ^uint64(0)
	PointerIDGenericFinger uint64 = ^uint64(1)

	ButtonPrimary   uint32 = 1 << 0
	ButtonSecondary uint32 = 1 << 1
	ButtonTertiary  uint32 = 1 << 2
)

// Android KeyEvent actions, meta bitmasks, and common navigation/modifier keycodes.
const (
	KeyActionDown byte = 0
	KeyActionUp   byte = 1

	MetaAltOn   uint32 = 0x00000002
	MetaShiftOn uint32 = 0x00000001
	MetaCtrlOn  uint32 = 0x00001000
	MetaMetaOn  uint32 = 0x00010000

	KeycodeHome       uint32 = 3
	KeycodeBack       uint32 = 4
	Keycode0          uint32 = 7
	KeycodeA          uint32 = 29
	KeycodeDPadUp     uint32 = 19
	KeycodeDPadDown   uint32 = 20
	KeycodeDPadLeft   uint32 = 21
	KeycodeDPadRight  uint32 = 22
	KeycodePower      uint32 = 26
	KeycodeTab        uint32 = 61
	KeycodeSpace      uint32 = 62
	KeycodeEnter      uint32 = 66
	KeycodeDel        uint32 = 67
	KeycodeGrave      uint32 = 68
	KeycodeMinus      uint32 = 69
	KeycodeEquals     uint32 = 70
	KeycodeLeftBrack  uint32 = 71
	KeycodeRightBrack uint32 = 72
	KeycodeBackslash  uint32 = 73
	KeycodeSemicolon  uint32 = 74
	KeycodeApostrophe uint32 = 75
	KeycodeSlash      uint32 = 76
	KeycodeComma      uint32 = 55
	KeycodePeriod     uint32 = 56
	KeycodePageUp     uint32 = 92
	KeycodePageDown   uint32 = 93
	KeycodeEscape     uint32 = 111
	KeycodeForwardDel uint32 = 112
	KeycodeMoveHome   uint32 = 122
	KeycodeMoveEnd    uint32 = 123
	KeycodeInsert     uint32 = 124
	KeycodeF1         uint32 = 131
	KeycodeAppSwitch  uint32 = 187
	KeycodeWakeup     uint32 = 224
)

// UHIDMouseReportDesc is a standard 5-button relative USB HID mouse descriptor.
var UHIDMouseReportDesc = []byte{
	0x05, 0x01, 0x09, 0x02, 0xA1, 0x01, 0x09, 0x01, 0xA1, 0x00,
	0x05, 0x09, 0x19, 0x01, 0x29, 0x05, 0x15, 0x00, 0x25, 0x01,
	0x95, 0x05, 0x75, 0x01, 0x81, 0x02, 0x95, 0x01, 0x75, 0x03,
	0x81, 0x01, 0x05, 0x01, 0x09, 0x30, 0x09, 0x31, 0x09, 0x38,
	0x15, 0x81, 0x25, 0x7F, 0x75, 0x08, 0x95, 0x03, 0x81, 0x06,
	0xC0, 0xC0,
}

// UHIDKeyboardReportDesc is a standard 8-byte USB HID boot keyboard descriptor.
var UHIDKeyboardReportDesc = []byte{
	0x05, 0x01, 0x09, 0x06, 0xA1, 0x01,
	0x05, 0x07, 0x19, 0xE0, 0x29, 0xE7, 0x15, 0x00, 0x25, 0x01, 0x75, 0x01, 0x95, 0x08, 0x81, 0x02,
	0x95, 0x01, 0x75, 0x08, 0x81, 0x01,
	0x95, 0x05, 0x75, 0x01, 0x05, 0x08, 0x19, 0x01, 0x29, 0x05, 0x91, 0x02,
	0x95, 0x01, 0x75, 0x03, 0x91, 0x01,
	0x95, 0x06, 0x75, 0x08, 0x15, 0x00, 0x25, 0x65, 0x05, 0x07, 0x19, 0x00, 0x29, 0x65, 0x81, 0x00,
	0xC0,
}

// Handshake contains the initial metadata read from the first scrcpy socket.
type Handshake struct {
	DeviceName string
	CodecID    uint32
}

// VideoPacket represents one parsed session or media packet on the video socket.
type VideoPacket struct {
	IsSession     bool
	ClientResized bool
	Width         uint32
	Height        uint32
	IsConfig      bool
	IsKeyframe    bool
	PtsUs         uint64
	Payload       []byte
}

// DeviceMessage represents a control-socket notification sent from device to client.
type DeviceMessage struct {
	Type     byte
	Text     string
	Sequence uint64
	ID       uint16
	Data     []byte
}

// RandomSCID returns a random non-negative 31-bit scrcpy socket discriminator.
func RandomSCID() (uint32, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(0x7FFFFFFF))
	if err != nil {
		return 0, err
	}
	return uint32(n.Int64()), nil
}

// SocketName returns the Linux abstract socket name used by scrcpy-server.
func SocketName(scid uint32) string {
	return fmt.Sprintf("scrcpy_%08x", scid)
}

// FindCachedServer resolves scrcpy-server-v5.0.1 from ANDROFUSE_SCRCPY_SERVER,
// executable directory, or repo `.dev/cache/scrcpy-server-v5.0.1`.
func FindCachedServer() (string, error) {
	if env := strings.TrimSpace(os.Getenv("ANDROFUSE_SCRCPY_SERVER")); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "scrcpy-server"),
			filepath.Join(dir, "scrcpy-server-v"+ServerVersion),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		cur := wd
		for range 6 {
			candidates = append(candidates, filepath.Join(cur, ".dev", "cache", "scrcpy-server-v"+ServerVersion))
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("scrcpy-server-v5.0.1 not found (run `go run ./tools/fetchdeps`)")
}

// ReadVideoHandshake reads optional forward dummy byte, 64-byte UTF-8 device name,
// and 4-byte big-endian codec ID from r.
func ReadVideoHandshake(r io.Reader, hasDummyByte bool) (Handshake, error) {
	if hasDummyByte {
		var dummy [1]byte
		if _, err := io.ReadFull(r, dummy[:]); err != nil {
			return Handshake{}, fmt.Errorf("read dummy byte: %w", err)
		}
	}
	var raw [DeviceNameLen + 4]byte
	if _, err := io.ReadFull(r, raw[:]); err != nil {
		return Handshake{}, fmt.Errorf("read video handshake: %w", err)
	}
	nameBytes := raw[:DeviceNameLen]
	if idx := strings.IndexByte(string(nameBytes), 0); idx >= 0 {
		nameBytes = nameBytes[:idx]
	}
	return Handshake{
		DeviceName: string(nameBytes),
		CodecID:    binary.BigEndian.Uint32(raw[DeviceNameLen:]),
	}, nil
}

// ReadVideoPacket reads a single 12-byte session or media packet (plus payload) from r.
func ReadVideoPacket(r io.Reader, reusableBuf *[]byte) (VideoPacket, error) {
	var hdr [PacketHeaderLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return VideoPacket{}, err
	}
	if hdr[0]&0x80 != 0 {
		return VideoPacket{
			IsSession:     true,
			ClientResized: (hdr[3] & 0x01) != 0,
			Width:         binary.BigEndian.Uint32(hdr[4:8]),
			Height:        binary.BigEndian.Uint32(hdr[8:12]),
		}, nil
	}
	ptsFlags := binary.BigEndian.Uint64(hdr[0:8])
	size := int(binary.BigEndian.Uint32(hdr[8:12]))
	if size < 0 || size > MaxMediaPacketSize {
		return VideoPacket{}, fmt.Errorf("invalid media packet size %d", size)
	}
	if cap(*reusableBuf) < size {
		*reusableBuf = make([]byte, size)
	}
	payload := (*reusableBuf)[:size]
	if size > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return VideoPacket{}, err
		}
	}
	return VideoPacket{
		IsConfig:   ((ptsFlags >> 62) & 1) != 0,
		IsKeyframe: ((ptsFlags >> 61) & 1) != 0,
		PtsUs:      ptsFlags & 0x1FFFFFFFFFFFFFFF,
		Payload:    payload,
	}, nil
}

// EncodeUhidCreate serializes TYPE_UHID_CREATE (12).
func EncodeUhidCreate(id, vendorID, productID uint16, name string, reportDesc []byte) []byte {
	nb := []byte(name)
	if len(nb) > 127 {
		nb = nb[:127]
	}
	buf := make([]byte, 1+2+2+2+1+len(nb)+2+len(reportDesc))
	buf[0] = MsgUhidCreate
	binary.BigEndian.PutUint16(buf[1:3], id)
	binary.BigEndian.PutUint16(buf[3:5], vendorID)
	binary.BigEndian.PutUint16(buf[5:7], productID)
	buf[7] = byte(len(nb))
	copy(buf[8:], nb)
	off := 8 + len(nb)
	binary.BigEndian.PutUint16(buf[off:off+2], uint16(len(reportDesc)))
	copy(buf[off+2:], reportDesc)
	return buf
}

// EncodeUhidInput serializes TYPE_UHID_INPUT (13).
func EncodeUhidInput(id uint16, report []byte) []byte {
	buf := make([]byte, 1+2+2+len(report))
	buf[0] = MsgUhidInput
	binary.BigEndian.PutUint16(buf[1:3], id)
	binary.BigEndian.PutUint16(buf[3:5], uint16(len(report)))
	copy(buf[5:], report)
	return buf
}

// EncodeUhidDestroy serializes TYPE_UHID_DESTROY (14).
func EncodeUhidDestroy(id uint16) []byte {
	buf := make([]byte, 3)
	buf[0] = MsgUhidDestroy
	binary.BigEndian.PutUint16(buf[1:3], id)
	return buf
}

// EncodeStartApp serializes TYPE_START_APP (16).
func EncodeStartApp(pkg string) []byte {
	pb := []byte(pkg)
	if len(pb) > 255 {
		pb = pb[:255]
	}
	buf := make([]byte, 2+len(pb))
	buf[0] = MsgStartApp
	buf[1] = byte(len(pb))
	copy(buf[2:], pb)
	return buf
}

// EncodeResizeDisplay serializes TYPE_RESIZE_DISPLAY (21).
func EncodeResizeDisplay(width, height uint16) []byte {
	buf := make([]byte, 5)
	buf[0] = MsgResizeDisplay
	binary.BigEndian.PutUint16(buf[1:3], width)
	binary.BigEndian.PutUint16(buf[3:5], height)
	return buf
}

// EncodeSetDisplayPower serializes TYPE_SET_DISPLAY_POWER (10).
func EncodeSetDisplayPower(on bool) []byte {
	var b byte
	if on {
		b = 1
	}
	return []byte{MsgSetDisplayPow, b}
}

// EncodeSetClipboard serializes TYPE_SET_CLIPBOARD (9).
func EncodeSetClipboard(seq uint64, paste bool, text string) []byte {
	tb := []byte(text)
	buf := make([]byte, 1+8+1+4+len(tb))
	buf[0] = MsgSetClipboard
	binary.BigEndian.PutUint64(buf[1:9], seq)
	if paste {
		buf[9] = 1
	}
	binary.BigEndian.PutUint32(buf[10:14], uint32(len(tb)))
	copy(buf[14:], tb)
	return buf
}

// EncodeInjectKeycode serializes TYPE_INJECT_KEYCODE (0) in 14 bytes.
func EncodeInjectKeycode(action byte, keycode, repeat, metaState uint32) []byte {
	var buf [14]byte
	buf[0] = MsgInjectKeycode
	buf[1] = action
	binary.BigEndian.PutUint32(buf[2:6], keycode)
	binary.BigEndian.PutUint32(buf[6:10], repeat)
	binary.BigEndian.PutUint32(buf[10:14], metaState)
	return buf[:]
}

// EncodeInjectText serializes TYPE_INJECT_TEXT (1), UTF-8 rune-safely bounded to 300 bytes.
func EncodeInjectText(text string) []byte {
	if len(text) > 300 {
		b := []byte(text)
		cut := 300
		for cut > 0 && (b[cut]&0xC0) == 0x80 {
			cut--
		}
		text = string(b[:cut])
	}
	buf := make([]byte, 1+4+len(text))
	buf[0] = MsgInjectText
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(text)))
	copy(buf[5:], text)
	return buf
}

// EncodeInjectTouch serializes TYPE_INJECT_TOUCH_EVENT (2) in 32 bytes.
//
// Inputs: action (ActionDown/Up/Move/HoverMove/ButtonPress/Release), pointerID
// (`PointerIDMouse` routes absolute cursor events directly to display_id;
// `PointerIDGenericFinger` emulates touchscreen input), (x, y) inside screenW x screenH.
func EncodeInjectTouch(
	action byte,
	pointerID uint64,
	x, y int32,
	screenW, screenH, pressure uint16,
	actionButton, buttons uint32,
) []byte {
	var buf [32]byte
	buf[0] = MsgInjectTouch
	buf[1] = action
	binary.BigEndian.PutUint64(buf[2:10], pointerID)
	binary.BigEndian.PutUint32(buf[10:14], uint32(x))
	binary.BigEndian.PutUint32(buf[14:18], uint32(y))
	binary.BigEndian.PutUint16(buf[18:20], screenW)
	binary.BigEndian.PutUint16(buf[20:22], screenH)
	binary.BigEndian.PutUint16(buf[22:24], pressure)
	binary.BigEndian.PutUint32(buf[24:28], actionButton)
	binary.BigEndian.PutUint32(buf[28:32], buttons)
	return buf[:]
}

// EncodeInjectScroll serializes TYPE_INJECT_SCROLL_EVENT (3) in 21 bytes.
//
// Notes: scrcpy decodes `i16` fixed-point scroll values in `[-1.0, 1.0]` by dividing by 32768.0.
func EncodeInjectScroll(x, y int32, screenW, screenH uint16, hScroll, vScroll float64, buttons uint32) []byte {
	var buf [21]byte
	buf[0] = MsgInjectScroll
	binary.BigEndian.PutUint32(buf[1:5], uint32(x))
	binary.BigEndian.PutUint32(buf[5:9], uint32(y))
	binary.BigEndian.PutUint16(buf[9:11], screenW)
	binary.BigEndian.PutUint16(buf[11:13], screenH)
	binary.BigEndian.PutUint16(buf[13:15], uint16(clampScrollFixedPoint(hScroll)))
	binary.BigEndian.PutUint16(buf[15:17], uint16(clampScrollFixedPoint(vScroll)))
	binary.BigEndian.PutUint32(buf[17:21], buttons)
	return buf[:]
}

func clampScrollFixedPoint(v float64) int16 {
	if v > 1 {
		v = 1
	} else if v < -1 {
		v = -1
	}
	return int16(v * 32767)
}

// EncodeBackOrScreenOn serializes TYPE_BACK_OR_SCREEN_ON (4).
func EncodeBackOrScreenOn(action byte) []byte {
	return []byte{MsgBackOrScreenOn, action}
}

// EncodeRotateDevice serializes TYPE_ROTATE_DEVICE (11).
func EncodeRotateDevice() []byte {
	return []byte{MsgRotateDevice}
}

// EncodeResetVideo serializes TYPE_RESET_VIDEO (17).
func EncodeResetVideo() []byte {
	return []byte{MsgResetVideo}
}

// ReadDeviceMessage reads one device->client message from the control socket.
func ReadDeviceMessage(r io.Reader) (DeviceMessage, error) {
	var t [1]byte
	if _, err := io.ReadFull(r, t[:]); err != nil {
		return DeviceMessage{}, err
	}
	switch t[0] {
	case 0:
		var lenBuf [4]byte
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return DeviceMessage{}, err
		}
		n := binary.BigEndian.Uint32(lenBuf[:])
		if n > 256*1024 {
			return DeviceMessage{}, fmt.Errorf("clipboard message too large: %d", n)
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(r, data); err != nil {
			return DeviceMessage{}, err
		}
		return DeviceMessage{Type: 0, Text: string(data)}, nil
	case 1:
		var seqBuf [8]byte
		if _, err := io.ReadFull(r, seqBuf[:]); err != nil {
			return DeviceMessage{}, err
		}
		return DeviceMessage{Type: 1, Sequence: binary.BigEndian.Uint64(seqBuf[:])}, nil
	case 2:
		var hdr [4]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return DeviceMessage{}, err
		}
		id := binary.BigEndian.Uint16(hdr[0:2])
		n := binary.BigEndian.Uint16(hdr[2:4])
		data := make([]byte, n)
		if _, err := io.ReadFull(r, data); err != nil {
			return DeviceMessage{}, err
		}
		return DeviceMessage{Type: 2, ID: id, Data: data}, nil
	default:
		return DeviceMessage{}, fmt.Errorf("unknown scrcpy device message type %d", t[0])
	}
}
