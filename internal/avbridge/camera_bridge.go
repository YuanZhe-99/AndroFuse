// Package avbridge implements AndroFuse's M5 bidirectional audio/video bridge:
// loopback-only MJPEG phone-camera bridging, honest Windows virtual-camera API
// probing (`MFCreateVirtualCamera`), and encrypted PC-to-Android mic/camera uplink.
package avbridge

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// VirtualCameraCapability describes OS virtual camera API availability without overclaiming.
type VirtualCameraCapability struct {
	OS                 string `json:"os"`
	MFVirtualCameraAPI bool   `json:"mfVirtualCameraApi"`
	Notes              string `json:"notes"`
}

// ProbeVirtualCamera inspects whether the host OS exposes Windows 11 MFCreateVirtualCamera.
func ProbeVirtualCamera() VirtualCameraCapability {
	return probeVirtualCameraOS()
}

// MJPEGStats reports runtime counts for the local phone-camera MJPEG bridge.
type MJPEGStats struct {
	Running         bool   `json:"running"`
	URL             string `json:"url,omitempty"`
	ActiveClients   int    `json:"activeClients"`
	FramesBroadcast uint64 `json:"framesBroadcast"`
	LastWidth       int    `json:"lastWidth"`
	LastHeight      int    `json:"lastHeight"`
}

// MJPEGServer serves decoded phone-camera NV12 frames as a token-gated local
// multipart/x-mixed-replace MJPEG stream strictly on 127.0.0.1 (`INV-07`).
type MJPEGServer struct {
	token string

	mu          sync.Mutex
	running     bool
	url         string
	ln          net.Listener
	httpSrv     *http.Server
	clients     map[chan []byte]struct{}
	lastW       int
	lastH       int
	framesTotal atomic.Uint64
}

// NewMJPEGServer constructs a stopped loopback MJPEG bridge ready to Start.
func NewMJPEGServer() *MJPEGServer {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return &MJPEGServer{
		token:   hex.EncodeToString(raw[:]),
		clients: make(map[chan []byte]struct{}),
	}
}

// Start binds an ephemeral loopback HTTP listener (`127.0.0.1:0`) and returns the stream URL.
// Side effects: Starts an HTTP server goroutine that cleans up on Close.
func (s *MJPEGServer) Start() (string, error) {
	s.mu.Lock()
	if s.running && s.url != "" {
		u := s.url
		s.mu.Unlock()
		return u, nil
	}
	s.mu.Unlock()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("avbridge: listen loopback: %w", err)
	}
	pathPrefix := "/camera/" + s.token + ".mjpeg"
	mux := http.NewServeMux()
	mux.HandleFunc(pathPrefix, s.handleStream)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	url := "http://" + ln.Addr().String() + pathPrefix

	s.mu.Lock()
	s.running = true
	s.url = url
	s.ln = ln
	s.httpSrv = srv
	s.mu.Unlock()

	go func() {
		_ = srv.Serve(ln)
	}()
	return url, nil
}

// URL returns the active loopback stream URL, or "" if stopped.
func (s *MJPEGServer) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return ""
	}
	return s.url
}

// Stats returns a snapshot of the MJPEG bridge state and broadcast counters.
func (s *MJPEGServer) Stats() MJPEGStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return MJPEGStats{
		Running:         s.running,
		URL:             s.url,
		ActiveClients:   len(s.clients),
		FramesBroadcast: s.framesTotal.Load(),
		LastWidth:       s.lastW,
		LastHeight:      s.lastH,
	}
}

// Close shuts down the HTTP server and closes all active subscriber channels.
func (s *MJPEGServer) Close() error {
	s.mu.Lock()
	srv := s.httpSrv
	ln := s.ln
	s.running = false
	s.url = ""
	s.httpSrv = nil
	s.ln = nil
	for ch := range s.clients {
		delete(s.clients, ch)
		close(ch)
	}
	s.mu.Unlock()

	if srv != nil {
		return srv.Close()
	}
	if ln != nil {
		return ln.Close()
	}
	return nil
}

// PushNV12 converts planar NV12 frames into JPEG when at least one local consumer
// is attached, skipping encoding completely when idle (`INV-04`).
func (s *MJPEGServer) PushNV12(width, height, yStride, uvStride int, yPlane, uvPlane []byte) {
	if width <= 0 || height <= 0 || len(yPlane) == 0 || len(uvPlane) == 0 {
		return
	}
	s.mu.Lock()
	running := s.running
	clientCount := len(s.clients)
	s.lastW = width
	s.lastH = height
	s.mu.Unlock()
	if !running || clientCount == 0 {
		return
	}

	jpg, err := encodeNV12JPEG(width, height, yStride, uvStride, yPlane, uvPlane)
	if err != nil {
		return
	}

	s.mu.Lock()
	for ch := range s.clients {
		select {
		case ch <- jpg:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- jpg:
			default:
			}
		}
	}
	s.mu.Unlock()
	s.framesTotal.Add(1)
}

func (s *MJPEGServer) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	const boundary = "androfuse-mjpeg-frame"
	ch := make(chan []byte, 2)
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		http.Error(w, "stream stopped", http.StatusServiceUnavailable)
		return
	}
	s.clients[ch] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if _, ok := s.clients[ch]; ok {
			delete(s.clients, ch)
			close(ch)
		}
		s.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+boundary)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case jpg, ok := <-ch:
			if !ok {
				return
			}
			header := fmt.Sprintf("--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", boundary, len(jpg))
			if _, err := w.Write([]byte(header)); err != nil {
				return
			}
			if _, err := w.Write(jpg); err != nil {
				return
			}
			if _, err := w.Write([]byte("\r\n")); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func encodeNV12JPEG(w, h, yStride, uvStride int, yPlane, uvPlane []byte) ([]byte, error) {
	chromaW := (w + 1) / 2
	chromaH := (h + 1) / 2
	if w <= 0 || h <= 0 || yStride < w || uvStride < chromaW*2 {
		return nil, fmt.Errorf("invalid NV12 geometry %dx%d", w, h)
	}
	if len(yPlane) < h*yStride || len(uvPlane) < chromaH*uvStride {
		return nil, fmt.Errorf("truncated NV12 frame")
	}
	cb := make([]byte, chromaW*chromaH)
	cr := make([]byte, chromaW*chromaH)
	for row := 0; row < chromaH; row++ {
		srcRow := uvPlane[row*uvStride:]
		dstBase := row * chromaW
		for col := 0; col < chromaW; col++ {
			cb[dstBase+col] = srcRow[col*2]
			cr[dstBase+col] = srcRow[col*2+1]
		}
	}
	img := &image.YCbCr{
		Y:              yPlane,
		Cb:             cb,
		Cr:             cr,
		YStride:        yStride,
		CStride:        chromaW,
		SubsampleRatio: image.YCbCrSubsampleRatio420,
		Rect:           image.Rect(0, 0, w, h),
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
