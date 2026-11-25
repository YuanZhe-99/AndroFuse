package avbridge

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/companion"
)

func TestProbeAndMJPEGStream(t *testing.T) {
	cap := ProbeVirtualCamera()
	if cap.OS == "" || cap.Notes == "" {
		t.Fatalf("unexpected ProbeVirtualCamera: %+v", cap)
	}

	srv := NewMJPEGServer()
	url, err := srv.Start()
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = srv.Close() }()
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("expected loopback URL, got %s", url)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET mjpeg error = %v", err)
	}
	defer resp.Body.Close()

	// Wait until client registers, then push NV12 frame.
	for i := 0; i < 30 && srv.Stats().ActiveClients == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	srv.PushNV12(16, 16, 16, 16, make([]byte, 256), make([]byte, 128))

	rd := bufio.NewReader(resp.Body)
	line, err := rd.ReadString('\n')
	if err != nil || !strings.Contains(line, "--androfuse-mjpeg-frame") {
		t.Fatalf("expected MJPEG boundary line, got %q err=%v", line, err)
	}
	if st := srv.Stats(); st.FramesBroadcast < 1 || st.LastWidth != 16 {
		t.Fatalf("unexpected MJPEG stats: %+v", st)
	}
}

func TestUplinkBridgePermissionGate(t *testing.T) {
	var mediaAllowed atomic.Bool
	srv, err := companion.NewServer("PAIR1234", "Pixel", "16", 36, companion.Handler{
		GetPermissions: func() companion.Permissions {
			p := companion.DefaultPermissions()
			p.MediaBridge = mediaAllowed.Load()
			return p
		},
		OnMediaFrame: func(req companion.MediaFrameRequest) (companion.MediaFrameResponse, error) {
			return companion.MediaFrameResponse{OK: true, ReceivedBytes: len(req.DataBase64)}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewServer error = %v", err)
	}
	if err := srv.Listen("127.0.0.1:0"); err != nil {
		t.Fatalf("Listen error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer func() { _ = srv.Close() }()

	cl, err := companion.Dial(ctx, srv.Addr(), "PAIR1234", srv.Fingerprint())
	if err != nil {
		t.Fatalf("Dial error = %v", err)
	}
	defer func() { _ = cl.Close() }()

	bridge := NewUplinkBridge(cl)
	bridge.Start()

	if err := bridge.PushMicPCM(ctx, 48000, 2, []byte{1, 2, 3, 4}); err == nil {
		t.Fatalf("expected media_bridge denied by default")
	}

	mediaAllowed.Store(true)
	if err := bridge.PushMicPCM(ctx, 48000, 2, []byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("expected mic push success, got %v", err)
	}
	if err := bridge.PushCameraJPEG(ctx, 640, 480, []byte{0xff, 0xd8, 0xff, 0xd9}); err != nil {
		t.Fatalf("expected camera push success, got %v", err)
	}
	st := bridge.Stats()
	if st.AudioFrames != 1 || st.VideoFrames != 1 || st.BytesSent != 8 {
		t.Fatalf("unexpected uplink stats: %+v", st)
	}
}

type mockPCMReader struct {
	chunks int
}

func (m *mockPCMReader) ReadPCM(dst []byte) (int, error) {
	if m.chunks <= 0 {
		return 0, nil
	}
	m.chunks--
	n := min(len(dst), 640)
	for i := 0; i < n; i++ {
		dst[i] = byte(i)
	}
	return n, nil
}

func (m *mockPCMReader) Close() {}

func TestUplinkBridgeStreamMic(t *testing.T) {
	srv, err := companion.NewServer("PAIR5678", "Pixel", "16", 36, companion.Handler{
		GetPermissions: func() companion.Permissions {
			p := companion.DefaultPermissions()
			p.MediaBridge = true
			return p
		},
	})
	if err != nil {
		t.Fatalf("NewServer error = %v", err)
	}
	if err := srv.Listen("127.0.0.1:0"); err != nil {
		t.Fatalf("Listen error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer func() { _ = srv.Close() }()

	cl, err := companion.Dial(ctx, srv.Addr(), "PAIR5678", srv.Fingerprint())
	if err != nil {
		t.Fatalf("Dial error = %v", err)
	}
	defer func() { _ = cl.Close() }()

	bridge := NewUplinkBridge(cl)
	bridge.Start()
	streamCtx, streamCancel := context.WithTimeout(ctx, 120*time.Millisecond)
	defer streamCancel()
	_ = bridge.StreamMic(streamCtx, 48000, 1, &mockPCMReader{chunks: 2})

	st := bridge.Stats()
	if st.AudioFrames != 2 || st.BytesSent != 1280 {
		t.Fatalf("unexpected streamed mic stats: %+v", st)
	}
}
