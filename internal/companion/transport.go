package companion

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Handler supplies device callbacks invoked by the authenticated TLS Companion Server.
type Handler struct {
	GetPermissions      func() Permissions
	GetStatus           func() DeviceStatus
	OnClipboard         func(ClipboardMessage) error
	OnNotificationReply func(NotificationReply) error
	OnAppContinuity     func(AppContinuityRequest) (AppContinuityResponse, error)
	OnFilePush          func(name string, data []byte, shaHex string) (FilePushResponse, error)
	OnDisplayGuard      func(DisplayGuardRequest) (DisplayGuardResponse, error)
	OnMediaFrame        func(MediaFrameRequest) (MediaFrameResponse, error)
	OnMeshJoin          func(MeshJoinRequest) (MeshJoinResponse, error)
	// OnPairRequest asks the phone's user to allow an adb-connected desktop; it blocks until
	// they answer (or the host gives up) and reports whether they allowed it.
	OnPairRequest func(PairRequest) bool
}

// Server serves authenticated TLS 1.3 Companion NDJSON requests.
type Server struct {
	cert        tls.Certificate
	fingerprint string
	model       string
	androidRel  string
	sdkInt      int
	handler     Handler

	mu        sync.Mutex
	token     string
	listeners []net.Listener
	closed    bool
	wg        sync.WaitGroup
}

// GenerateSelfSignedCert creates an in-memory ECDSA P-256 certificate and colon-separated SHA-256 fingerprint.
func GenerateSelfSignedCert(commonName string) (tls.Certificate, string, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNum, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	now := time.Now().Add(-1 * time.Hour)
	tmpl := x509.Certificate{
		SerialNumber: serialNum,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now,
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"androfuse.local"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	fp := certFingerprint(der)
	cert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
		Leaf:        leaf,
	}
	return cert, fp, nil
}

func certFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// NewServer constructs an authenticated TLS 1.3 Companion Server.
func NewServer(token, deviceModel, androidRel string, sdk int, handler Handler) (*Server, error) {
	if strings.TrimSpace(token) == "" {
		var err error
		token, err = GeneratePairingToken()
		if err != nil {
			return nil, err
		}
	}
	cert, fp, err := GenerateSelfSignedCert("AndroFuse Companion")
	if err != nil {
		return nil, err
	}
	return &Server{
		cert:        cert,
		fingerprint: fp,
		token:       strings.TrimSpace(token),
		model:       deviceModel,
		androidRel:  androidRel,
		sdkInt:      sdk,
		handler:     handler,
	}, nil
}

// Listen adds a TLS 1.3 listener on addr: a TCP address (for example `0.0.0.0:34567` or
// `127.0.0.1:0`), or `@name` for a Linux abstract local socket, which `adb forward
// localabstract:name` reaches without a fixed TCP port (see LocalSocket).
// Notes: call it once per address before Serve; every listener shares the certificate and token.
func (s *Server) Listen(addr string) error {
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{s.cert},
	}
	network := "tcp"
	if strings.HasPrefix(addr, "@") {
		network = "unix"
	}
	raw, err := net.Listen(network, addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.listeners = append(s.listeners, tls.NewListener(raw, cfg))
	s.closed = false
	s.mu.Unlock()
	return nil
}

// Serve accepts authenticated TLS clients on every listener until ctx is cancelled or Close is
// called.
// Returns: nil after Close, or the first accept error that was not caused by closing.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	lns := append([]net.Listener(nil), s.listeners...)
	s.mu.Unlock()
	if len(lns) == 0 {
		return errors.New("companion server not listening")
	}

	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	errs := make(chan error, len(lns))
	for _, ln := range lns {
		go func() { errs <- s.accept(ctx, ln) }()
	}
	var first error
	for range lns {
		if err := <-errs; err != nil && first == nil {
			first = err
			_ = s.Close()
		}
	}
	s.wg.Wait()
	return first
}

// accept serves connections from ln until it is closed.
func (s *Server) accept(ctx context.Context, ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleConn(ctx, c)
		}(conn)
	}
}

// ListenAndServe binds addr and serves until ctx expires or Close is called.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	if err := s.Listen(addr); err != nil {
		return err
	}
	return s.Serve(ctx)
}

// Close closes every TLS listener and waits for active client handlers.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	lns := s.listeners
	s.listeners = nil
	s.mu.Unlock()
	var err error
	for _, ln := range lns {
		err = errors.Join(err, ln.Close())
	}
	s.wg.Wait()
	return err
}

// Addr returns the bound TCP listener address, or "" if stopped or listening only locally.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ln := range s.listeners {
		if ln.Addr().Network() == "tcp" {
			return ln.Addr().String()
		}
	}
	return ""
}

// Fingerprint returns the server's SHA-256 certificate fingerprint.
func (s *Server) Fingerprint() string {
	return s.fingerprint
}

// Token returns the current pairing token.
func (s *Server) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

// RotateToken replaces the pairing token with a fresh cryptographic token.
func (s *Server) RotateToken() (string, error) {
	tok, err := GeneratePairingToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.token = tok
	s.mu.Unlock()
	return tok, nil
}

func (s *Server) permissions() Permissions {
	if s.handler.GetPermissions != nil {
		return s.handler.GetPermissions()
	}
	return DefaultPermissions()
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 24<<20)
	enc := json.NewEncoder(conn)

	if !sc.Scan() {
		return
	}
	var authEnv Envelope
	if err := json.Unmarshal(sc.Bytes(), &authEnv); err == nil && authEnv.Type == "pair.request" {
		s.handlePair(conn, enc, authEnv)
		return
	}
	if authEnv.Type != "auth" {
		_ = enc.Encode(Envelope{Type: "auth.res", Error: "auth frame required"})
		return
	}
	var authReq AuthRequest
	if err := json.Unmarshal(authEnv.Payload, &authReq); err != nil {
		_ = enc.Encode(Envelope{ID: authEnv.ID, Type: "auth.res", Error: "invalid auth payload"})
		return
	}
	s.mu.Lock()
	expectedTok := s.token
	s.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(authReq.Token), []byte(expectedTok)) != 1 {
		_ = writeEnvelope(enc, authEnv.ID, "auth.res", AuthResponse{OK: false, Error: "invalid pairing token"}, "invalid pairing token")
		return
	}
	_ = conn.SetDeadline(time.Time{})
	_ = writeEnvelope(enc, authEnv.ID, "auth.res", AuthResponse{
		OK:              true,
		DeviceModel:     s.model,
		AndroidRelease:  s.androidRel,
		SDKInt:          s.sdkInt,
		CertFingerprint: s.fingerprint,
		Permissions:     s.permissions(),
	}, "")

	for sc.Scan() {
		if ctx.Err() != nil {
			return
		}
		var env Envelope
		if err := json.Unmarshal(sc.Bytes(), &env); err != nil {
			_ = enc.Encode(Envelope{Type: "error", Error: fmt.Sprintf("invalid json: %v", err)})
			continue
		}
		resp := s.dispatch(env)
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

func (s *Server) dispatch(env Envelope) Envelope {
	perms := s.permissions()
	switch env.Type {
	case "status.get":
		if !perms.Telemetry {
			return Envelope{ID: env.ID, Type: "status.res", Error: "telemetry permission disabled"}
		}
		var st DeviceStatus
		if s.handler.GetStatus != nil {
			st = s.handler.GetStatus()
		}
		if !perms.ADBBeacon {
			st.WirelessADBPort = 0
		}
		return mustEnvelope(env.ID, "status.res", st)

	case "clipboard.set":
		if !perms.Clipboard {
			return Envelope{ID: env.ID, Type: "clipboard.res", Error: "clipboard permission disabled"}
		}
		var msg ClipboardMessage
		if err := json.Unmarshal(env.Payload, &msg); err != nil {
			return Envelope{ID: env.ID, Type: "clipboard.res", Error: err.Error()}
		}
		if s.handler.OnClipboard != nil {
			if err := s.handler.OnClipboard(msg); err != nil {
				return Envelope{ID: env.ID, Type: "clipboard.res", Error: err.Error()}
			}
		}
		return mustEnvelope(env.ID, "clipboard.res", map[string]any{"ok": true})

	case "notification.reply":
		if !perms.Notifications {
			return Envelope{ID: env.ID, Type: "notification.reply.res", Error: "notification permission disabled"}
		}
		var rep NotificationReply
		if err := json.Unmarshal(env.Payload, &rep); err != nil {
			return Envelope{ID: env.ID, Type: "notification.reply.res", Error: err.Error()}
		}
		if s.handler.OnNotificationReply != nil {
			if err := s.handler.OnNotificationReply(rep); err != nil {
				return Envelope{ID: env.ID, Type: "notification.reply.res", Error: err.Error()}
			}
		}
		return mustEnvelope(env.ID, "notification.reply.res", map[string]any{"ok": true})

	case "app.continuity":
		if !perms.AppContinuity {
			return Envelope{ID: env.ID, Type: "app.continuity.res", Error: "app continuity permission disabled"}
		}
		var req AppContinuityRequest
		if err := json.Unmarshal(env.Payload, &req); err != nil {
			return Envelope{ID: env.ID, Type: "app.continuity.res", Error: err.Error()}
		}
		if s.handler.OnAppContinuity == nil {
			return Envelope{ID: env.ID, Type: "app.continuity.res", Error: "app continuity unavailable"}
		}
		res, err := s.handler.OnAppContinuity(req)
		if err != nil {
			return Envelope{ID: env.ID, Type: "app.continuity.res", Error: err.Error()}
		}
		return mustEnvelope(env.ID, "app.continuity.res", res)

	case "file.push":
		if !perms.FileTransfer {
			return Envelope{ID: env.ID, Type: "file.push.res", Error: "file transfer permission disabled"}
		}
		var req FilePushRequest
		if err := json.Unmarshal(env.Payload, &req); err != nil {
			return Envelope{ID: env.ID, Type: "file.push.res", Error: err.Error()}
		}
		cleanName, err := SanitizeFileName(req.Name)
		if err != nil {
			return Envelope{ID: env.ID, Type: "file.push.res", Error: err.Error()}
		}
		if req.Size < 0 || req.Size > MaxFilePushBytes {
			return Envelope{ID: env.ID, Type: "file.push.res", Error: "file size out of bounds"}
		}
		raw, err := base64.StdEncoding.DecodeString(req.DataBase64)
		if err != nil {
			return Envelope{ID: env.ID, Type: "file.push.res", Error: fmt.Sprintf("decode file payload: %v", err)}
		}
		if len(raw) > MaxFilePushBytes {
			return Envelope{ID: env.ID, Type: "file.push.res", Error: "decoded file exceeds size cap"}
		}
		sum := sha256.Sum256(raw)
		sumHex := hex.EncodeToString(sum[:])
		if req.SHA256 != "" && !strings.EqualFold(req.SHA256, sumHex) {
			return Envelope{ID: env.ID, Type: "file.push.res", Error: "file SHA-256 mismatch"}
		}
		if s.handler.OnFilePush == nil {
			return mustEnvelope(env.ID, "file.push.res", FilePushResponse{
				SavedPath:    cleanName,
				SHA256:       sumHex,
				BytesWritten: int64(len(raw)),
			})
		}
		res, err := s.handler.OnFilePush(cleanName, raw, sumHex)
		if err != nil {
			return Envelope{ID: env.ID, Type: "file.push.res", Error: err.Error()}
		}
		return mustEnvelope(env.ID, "file.push.res", res)

	case "mesh.join":
		var req MeshJoinRequest
		if err := json.Unmarshal(env.Payload, &req); err != nil {
			return Envelope{ID: env.ID, Type: "mesh.join.res", Error: err.Error()}
		}
		if s.handler.OnMeshJoin == nil {
			return Envelope{ID: env.ID, Type: "mesh.join.res", Error: "this companion cannot join a mesh"}
		}
		res, err := s.handler.OnMeshJoin(req)
		if err != nil {
			return Envelope{ID: env.ID, Type: "mesh.join.res", Error: err.Error()}
		}
		return mustEnvelope(env.ID, "mesh.join.res", res)

	case "display.guard":
		if !perms.DisplayGuard {
			return Envelope{ID: env.ID, Type: "display.guard.res", Error: "display guard permission disabled"}
		}
		var req DisplayGuardRequest
		if err := json.Unmarshal(env.Payload, &req); err != nil {
			return Envelope{ID: env.ID, Type: "display.guard.res", Error: err.Error()}
		}
		if s.handler.OnDisplayGuard == nil {
			return mustEnvelope(env.ID, "display.guard.res", DisplayGuardResponse{CleanedCount: 0, Message: "no orphan displays found"})
		}
		res, err := s.handler.OnDisplayGuard(req)
		if err != nil {
			return Envelope{ID: env.ID, Type: "display.guard.res", Error: err.Error()}
		}
		return mustEnvelope(env.ID, "display.guard.res", res)

	case "av.push":
		if !perms.MediaBridge {
			return Envelope{ID: env.ID, Type: "av.push.res", Error: "media bridge permission disabled"}
		}
		var req MediaFrameRequest
		if err := json.Unmarshal(env.Payload, &req); err != nil {
			return Envelope{ID: env.ID, Type: "av.push.res", Error: err.Error()}
		}
		if req.Kind != "mic_pcm" && req.Kind != "camera_jpeg" {
			return Envelope{ID: env.ID, Type: "av.push.res", Error: "unsupported media frame kind"}
		}
		raw, err := base64.StdEncoding.DecodeString(req.DataBase64)
		if err != nil || len(raw) == 0 || len(raw) > MaxMediaFrameBytes {
			return Envelope{ID: env.ID, Type: "av.push.res", Error: "invalid or oversized media frame payload"}
		}
		if s.handler.OnMediaFrame == nil {
			return mustEnvelope(env.ID, "av.push.res", MediaFrameResponse{
				OK:            true,
				ReceivedBytes: len(raw),
				Message:       "accepted",
			})
		}
		res, err := s.handler.OnMediaFrame(req)
		if err != nil {
			return Envelope{ID: env.ID, Type: "av.push.res", Error: err.Error()}
		}
		return mustEnvelope(env.ID, "av.push.res", res)

	default:
		return Envelope{ID: env.ID, Type: "error", Error: fmt.Sprintf("unknown request %q", env.Type)}
	}
}

func mustEnvelope(id, typ string, payload any) Envelope {
	b, _ := json.Marshal(payload)
	return Envelope{ID: id, Type: typ, Payload: b}
}

func writeEnvelope(enc *json.Encoder, id, typ string, payload any, errMsg string) error {
	b, _ := json.Marshal(payload)
	return enc.Encode(Envelope{ID: id, Type: typ, Payload: b, Error: errMsg})
}

// Client is an authenticated TLS 1.3 connection to an Android Companion server.
type Client struct {
	conn *tls.Conn
	sc   *bufio.Scanner
	enc  *json.Encoder
	peer AuthResponse

	mu    sync.Mutex
	reqID atomic.Uint64
}

// Dial connects to addr over TLS 1.3 (verifying expectedFingerprint when supplied) and authenticates with token.
func Dial(ctx context.Context, addr, token, expectedFingerprint string) (*Client, error) {
	dialer := &tls.Dialer{
		Config: &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true, // Verified explicitly via SHA-256 fingerprint pin below (INV-07).
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("missing companion peer certificate")
				}
				gotFP := certFingerprint(cs.PeerCertificates[0].Raw)
				if expectedFingerprint != "" && !strings.EqualFold(strings.TrimSpace(expectedFingerprint), gotFP) {
					return fmt.Errorf("companion cert fingerprint mismatch: got %s", gotFP)
				}
				return nil
			},
		},
	}
	rawConn, err := dialer.DialContext(ctx, "tcp", strings.TrimSpace(addr))
	if err != nil {
		return nil, err
	}
	tlsConn := rawConn.(*tls.Conn)
	sc := bufio.NewScanner(tlsConn)
	sc.Buffer(make([]byte, 0, 64*1024), 24<<20)

	c := &Client{
		conn: tlsConn,
		sc:   sc,
		enc:  json.NewEncoder(tlsConn),
	}
	var authRes AuthResponse
	if err := c.call(ctx, "auth", AuthRequest{
		Token:           strings.TrimSpace(token),
		ClientName:      "AndroFuse-Desktop",
		ProtocolVersion: ProtocolVersion,
	}, &authRes); err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	if !authRes.OK {
		_ = tlsConn.Close()
		msg := authRes.Error
		if msg == "" {
			msg = "companion authentication failed"
		}
		return nil, errors.New(msg)
	}
	c.peer = authRes
	return c, nil
}

// PeerInfo returns the authenticated server metadata and granted permission set.
func (c *Client) PeerInfo() AuthResponse {
	return c.peer
}

// Close closes the underlying TLS 1.3 connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// GetStatus queries live battery, thermal, foreground package, and wireless ADB port beacon.
func (c *Client) GetStatus(ctx context.Context) (DeviceStatus, error) {
	var st DeviceStatus
	err := c.call(ctx, "status.get", struct{}{}, &st)
	return st, err
}

// SendClipboard pushes clipboard text to the Companion host.
func (c *Client) SendClipboard(ctx context.Context, seq uint64, text string) error {
	var out struct {
		OK bool `json:"ok"`
	}
	return c.call(ctx, "clipboard.set", ClipboardMessage{Seq: seq, Text: text, Source: "desktop"}, &out)
}

// RequestContinuity asks Companion to hand off or return a foreground application package.
func (c *Client) RequestContinuity(ctx context.Context, action, pkg string) (AppContinuityResponse, error) {
	var res AppContinuityResponse
	err := c.call(ctx, "app.continuity", AppContinuityRequest{Action: action, Package: pkg}, &res)
	return res, err
}

// ReplyNotification submits an inline reply for a synced notification ID.
func (c *Client) ReplyNotification(ctx context.Context, id, text string) error {
	var out struct {
		OK bool `json:"ok"`
	}
	return c.call(ctx, "notification.reply", NotificationReply{ID: id, ReplyText: text}, &out)
}

// PushFile uploads a bounded file payload with SHA-256 integrity verification.
func (c *Client) PushFile(ctx context.Context, name string, data []byte) (FilePushResponse, error) {
	clean, err := SanitizeFileName(name)
	if err != nil {
		return FilePushResponse{}, err
	}
	if len(data) > MaxFilePushBytes {
		return FilePushResponse{}, fmt.Errorf("file size %d exceeds %d limit", len(data), MaxFilePushBytes)
	}
	sum := sha256.Sum256(data)
	req := FilePushRequest{
		Name:       clean,
		Size:       int64(len(data)),
		SHA256:     hex.EncodeToString(sum[:]),
		DataBase64: base64.StdEncoding.EncodeToString(data),
	}
	var res FilePushResponse
	err = c.call(ctx, "file.push", req, &res)
	return res, err
}

// RunDisplayGuard triggers virtual display cleanup via Companion (`INV-03`).
func (c *Client) RunDisplayGuard(ctx context.Context) (DisplayGuardResponse, error) {
	var res DisplayGuardResponse
	err := c.call(ctx, "display.guard", DisplayGuardRequest{Action: "cleanup_orphans"}, &res)
	return res, err
}

// handlePair answers a `pair.request` frame: only over the local socket or loopback (`adb
// forward`) and only after the phone's user allowed it, it returns the pairing token and
// certificate fingerprint.
func (s *Server) handlePair(conn net.Conn, enc *json.Encoder, env Envelope) {
	if !isLocal(conn.RemoteAddr()) {
		_ = enc.Encode(Envelope{ID: env.ID, Type: "pair.res", Error: "pairing requests are only accepted over adb"})
		return
	}
	var req PairRequest
	if err := json.Unmarshal(env.Payload, &req); err != nil || s.handler.OnPairRequest == nil {
		_ = enc.Encode(Envelope{ID: env.ID, Type: "pair.res", Error: "pairing is not available"})
		return
	}
	_ = conn.SetDeadline(time.Now().Add(PairWait + 10*time.Second))
	if !s.handler.OnPairRequest(req) {
		_ = enc.Encode(Envelope{ID: env.ID, Type: "pair.res", Error: "pairing was declined on the phone"})
		return
	}
	s.mu.Lock()
	tok := s.token
	s.mu.Unlock()
	_ = writeEnvelope(enc, env.ID, "pair.res", PairResponse{Token: tok, CertFingerprint: s.fingerprint}, "")
}

// RequestPairing asks the phone at addr (an `adb forward` of the Companion LocalSocket) to pair, showing
// clientName and code there; it waits for the phone's user to answer.
// Returns: the pairing token and the certificate fingerprint, already checked against the TLS
// connection, for Dial.
func RequestPairing(ctx context.Context, addr, clientName, code string) (PairResponse, error) {
	var gotFP string
	dialer := &tls.Dialer{Config: &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // The fingerprint is returned to the caller and pinned for Dial.
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("missing companion peer certificate")
			}
			gotFP = certFingerprint(cs.PeerCertificates[0].Raw)
			return nil
		},
	}}
	raw, err := dialer.DialContext(ctx, "tcp", strings.TrimSpace(addr))
	if err != nil {
		return PairResponse{}, err
	}
	conn := raw.(*tls.Conn)
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	c := &Client{conn: conn, sc: sc, enc: json.NewEncoder(conn)}
	var res PairResponse
	if err := c.call(ctx, "pair.request", PairRequest{ClientName: clientName, Code: code}, &res); err != nil {
		return PairResponse{}, err
	}
	if !strings.EqualFold(res.CertFingerprint, gotFP) {
		return PairResponse{}, errors.New("companion certificate changed during pairing")
	}
	return res, nil
}

// JoinMesh asks the phone to join the desktop's EasyTier network (sent only when the user chose
// to during pairing, `INV-07`).
func (c *Client) JoinMesh(ctx context.Context, req MeshJoinRequest) (MeshJoinResponse, error) {
	var res MeshJoinResponse
	err := c.call(ctx, "mesh.join", req, &res)
	return res, err
}

// PushMediaFrame delivers a PC mic PCM or webcam JPEG frame to Android Companion (`INV-09`).
func (c *Client) PushMediaFrame(ctx context.Context, req MediaFrameRequest) (MediaFrameResponse, error) {
	var res MediaFrameResponse
	err := c.call(ctx, "av.push", req, &res)
	return res, err
}

func (c *Client) call(ctx context.Context, typ string, payload any, dst any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
	} else {
		_ = c.conn.SetDeadline(time.Now().Add(8 * time.Second))
	}
	defer func() { _ = c.conn.SetDeadline(time.Time{}) }()

	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	id := fmt.Sprintf("r%d", c.reqID.Add(1))
	if err := c.enc.Encode(Envelope{ID: id, Type: typ, Payload: b}); err != nil {
		return err
	}
	if !c.sc.Scan() {
		if err := c.sc.Err(); err != nil {
			return err
		}
		return errors.New("companion closed connection")
	}
	var resp Envelope
	if err := json.Unmarshal(c.sc.Bytes(), &resp); err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	if dst != nil && len(resp.Payload) > 0 {
		if err := json.Unmarshal(resp.Payload, dst); err != nil {
			return err
		}
	}
	return nil
}

// isLocal reports whether a peer reached the server over the local socket or loopback, which is
// how `adb forward` delivers it.
func isLocal(a net.Addr) bool {
	if a.Network() == "unix" {
		return true
	}
	ap, err := netip.ParseAddrPort(a.String())
	return err == nil && ap.Addr().Unmap().IsLoopback()
}
