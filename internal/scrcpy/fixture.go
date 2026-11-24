package scrcpy

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"sync"
	"time"
)

// Fixture stores a scrubbed, offline-replayable scrcpy v5.0.1 wire trace with
// zero raw device serials or user screen imagery (INV-08).
type Fixture struct {
	DeviceName    string          `json:"device_name"`
	CodecID       uint32          `json:"codec_id"`
	InitialWidth  uint32          `json:"initial_width"`
	InitialHeight uint32          `json:"initial_height"`
	Packets       []FixturePacket `json:"packets"`
}

// FixturePacket encodes one recorded session or media packet.
type FixturePacket struct {
	IsSession     bool   `json:"is_session,omitempty"`
	ClientResized bool   `json:"client_resized,omitempty"`
	Width         uint32 `json:"width,omitempty"`
	Height        uint32 `json:"height,omitempty"`
	IsConfig      bool   `json:"is_config,omitempty"`
	IsKeyframe    bool   `json:"is_keyframe,omitempty"`
	PtsUs         uint64 `json:"pts_us,omitempty"`
	PayloadB64    string `json:"payload_b64,omitempty"`
}

// LoadFixture reads a sanitized protocol replay JSON file.
func LoadFixture(path string) (Fixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, err
	}
	var f Fixture
	err = json.Unmarshal(raw, &f)
	return f, err
}

// ServeMockFixture opens loopback TCP sockets emulating scrcpy-server v5.0.1
// behavior for a sanitized Fixture, recording control messages sent by the client
// and answering TYPE_RESIZE_DISPLAY with a Session(client_resized=true) packet.
type ServeMockFixture struct {
	Listener    net.Listener
	mu          sync.Mutex
	ReceivedCtl [][]byte
	done        chan struct{}
}

// StartMockFixture starts a loopback mock server speaking scrcpy-server v5.0.1 wire protocol.
func StartMockFixture(f Fixture) (*ServeMockFixture, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	m := &ServeMockFixture{
		Listener: ln,
		done:     make(chan struct{}),
	}
	go m.serve(f)
	return m, nil
}

// Close stops the mock server listener.
func (m *ServeMockFixture) Close() {
	_ = m.Listener.Close()
	<-m.done
}

// Controls returns a snapshot of all control packets sent by the client.
func (m *ServeMockFixture) Controls() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.ReceivedCtl))
	copy(out, m.ReceivedCtl)
	return out
}

func (m *ServeMockFixture) serve(f Fixture) {
	defer close(m.done)
	vConn, err := m.Listener.Accept()
	if err != nil {
		return
	}
	defer vConn.Close()

	// Forward dummy byte + 64-byte device name + 4-byte codec ID.
	var hs [1 + DeviceNameLen + 4]byte
	copy(hs[1:1+DeviceNameLen], f.DeviceName)
	binary.BigEndian.PutUint32(hs[1+DeviceNameLen:], f.CodecID)
	if _, err := vConn.Write(hs[:]); err != nil {
		return
	}

	cConn, err := m.Listener.Accept()
	if err != nil {
		return
	}
	defer cConn.Close()

	resizeCh := make(chan [2]uint32, 4)
	ctrlDone := make(chan struct{})
	go func() {
		defer close(ctrlDone)
		buf := make([]byte, 512)
		var acc []byte
		for {
			n, rErr := cConn.Read(buf)
			if rErr != nil {
				return
			}
			if n > 0 {
				cp := append([]byte(nil), buf[:n]...)
				m.mu.Lock()
				m.ReceivedCtl = append(m.ReceivedCtl, cp)
				m.mu.Unlock()
				acc = append(acc, buf[:n]...)
				acc = extractMockControls(acc, resizeCh)
			}
		}
	}()

	for _, pkt := range f.Packets {
		if err := writeFixturePacket(vConn, pkt); err != nil {
			return
		}
	}

	// Wait for ResizeDisplay command during replay tests and emit ack session packet.
	select {
	case sz := <-resizeCh:
		_ = writeFixturePacket(vConn, FixturePacket{
			IsSession:     true,
			ClientResized: true,
			Width:         sz[0],
			Height:        sz[1],
		})
	case <-ctrlDone:
	case <-time.After(2 * time.Second):
	}
}

func writeFixturePacket(w net.Conn, pkt FixturePacket) error {
	var hdr [PacketHeaderLen]byte
	if pkt.IsSession {
		hdr[0] = 0x80
		if pkt.ClientResized {
			hdr[3] = 0x01
		}
		binary.BigEndian.PutUint32(hdr[4:8], pkt.Width)
		binary.BigEndian.PutUint32(hdr[8:12], pkt.Height)
		_, err := w.Write(hdr[:])
		return err
	}
	var raw []byte
	if pkt.PayloadB64 != "" {
		var err error
		raw, err = base64.StdEncoding.DecodeString(pkt.PayloadB64)
		if err != nil {
			return err
		}
	}
	ptsFlags := pkt.PtsUs & 0x1FFFFFFFFFFFFFFF
	if pkt.IsConfig {
		ptsFlags |= 1 << 62
	}
	if pkt.IsKeyframe {
		ptsFlags |= 1 << 61
	}
	binary.BigEndian.PutUint64(hdr[0:8], ptsFlags)
	binary.BigEndian.PutUint32(hdr[8:12], uint32(len(raw)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(raw) > 0 {
		if _, err := w.Write(raw); err != nil {
			return err
		}
	}
	return nil
}

func extractMockControls(acc []byte, resizeCh chan<- [2]uint32) []byte {
	for len(acc) > 0 {
		switch acc[0] {
		case MsgUhidCreate:
			if len(acc) < 8 {
				return acc
			}
			nameLen := int(acc[7])
			if len(acc) < 8+nameLen+2 {
				return acc
			}
			descLen := int(binary.BigEndian.Uint16(acc[8+nameLen : 8+nameLen+2]))
			total := 8 + nameLen + 2 + descLen
			if len(acc) < total {
				return acc
			}
			acc = acc[total:]
		case MsgUhidInput:
			if len(acc) < 5 {
				return acc
			}
			n := int(binary.BigEndian.Uint16(acc[3:5]))
			if len(acc) < 5+n {
				return acc
			}
			acc = acc[5+n:]
		case MsgUhidDestroy:
			if len(acc) < 3 {
				return acc
			}
			acc = acc[3:]
		case MsgStartApp:
			if len(acc) < 2 {
				return acc
			}
			n := int(acc[1])
			if len(acc) < 2+n {
				return acc
			}
			acc = acc[2+n:]
		case MsgResizeDisplay:
			if len(acc) < 5 {
				return acc
			}
			w := uint32(binary.BigEndian.Uint16(acc[1:3]))
			h := uint32(binary.BigEndian.Uint16(acc[3:5]))
			select {
			case resizeCh <- [2]uint32{w, h}:
			default:
			}
			acc = acc[5:]
		default:
			acc = acc[1:]
		}
	}
	return acc
}
