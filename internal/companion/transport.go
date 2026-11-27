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
	"encoding/pem"
	"errors"
	"fmt"
	"io"
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
	// Identity is the server's kept certificate (MarshalIdentity), so the fingerprint a desktop pinned
	// when pairing still matches after the Companion restarts; nil draws a new one.
	Identity       *tls.Certificate
	GetPermissions func() Permissions
	// MeshHost returns the EasyTier hostname the phone joins networks under; nil for none.
	MeshHost            func() string
	GetStatus           func() DeviceStatus
	OnClipboard         func(ClipboardMessage) error
	OnNotificationReply func(NotificationReply) error
	OnAppContinuity     func(AppContinuityRequest) (AppContinuityResponse, error)
	OnFilePush          func(name string, data []byte, shaHex string) (FilePushResponse, error)
	OnDisplayGuard      func(DisplayGuardRequest) (DisplayGuardResponse, error)
	OnMediaFrame        func(MediaFrameRequest) (MediaFrameResponse, error)
	OnMeshJoin          func(MeshJoinRequest) (MeshJoinResponse, error)
	// OnTunnel opens a loopback connection to this phone's Wireless debugging port for a
	// "tunnel.open" request; it refuses any other port (`INV-07`).
	OnTunnel func(port int) (net.Conn, error)
	// OnShare starts screen sharing (after Android's consent) and returns a connection carrying
	// the H.264 stream in scrcpy framing.
	OnShare func() (net.Conn, error)
	// OnKey runs "back", "home" or "recents" on the phone while it shares its screen.
	OnKey func(action string) error
	// OnDaemonStream opens a scrcpy-server video (and optional control) stream through the
	// persistent UID 2000 daemon for a `daemon.open` request (`PLAN` §9.2).
	OnDaemonStream func(DaemonStreamRequest) (sessionID string, video net.Conn, ctrl net.Conn, closeFn func(), err error)
	// OnListApps returns launchable applications on the phone for an `apps.list` request.
	OnListApps func() ([]DaemonAppEntry, error)
	// OnPairRequest asks the phone's user to allow an adb-connected desktop; it blocks until
	// they answer (or the host gives up) and reports whether they allowed it.
	OnPairRequest func(PairRequest) bool
	// IssueToken returns a new token for the computer req names once its pairing was allowed,
	// and CheckToken whether token belongs to a paired computer (recording that it connected);
	// both nil means one shared token (the Server's own) for every computer.
	IssueToken func(req PairRequest) (string, error)
	CheckToken func(req AuthRequest) bool
}

// Server serves authenticated TLS 1.3 Companion NDJSON requests.
type Server struct {
	cert        tls.Certificate
	fingerprint string
	model       string
	androidRel  string
	sdkInt      int
	handler     Handler

	mu          sync.Mutex
	token       string
	listeners   []net.Listener
	pendingCtrl map[string]pendingCtrlEntry
	closed      bool
	wg          sync.WaitGroup
}

type pendingCtrlEntry struct {
	conn net.Conn
	stop func()
}

// ErrFingerprint means the Companion answered with another certificate than the one pinned at
// pairing: it was reinstalled, or is another phone.
var ErrFingerprint = errors.New("companion cert fingerprint mismatch")

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
		NotAfter:     now.Add(20 * 365 * 24 * time.Hour), // kept for the pairing's life; peers pin it, not its dates
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
	var cert tls.Certificate
	var fp string
	if handler.Identity != nil && len(handler.Identity.Certificate) > 0 {
		cert, fp = *handler.Identity, certFingerprint(handler.Identity.Certificate[0])
	} else {
		var err error
		if cert, fp, err = GenerateSelfSignedCert("AndroFuse Companion"); err != nil {
			return nil, err
		}
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

// meshHost is the phone's EasyTier hostname from the handler, "" when it has none.
func (s *Server) meshHost() string {
	if s.handler.MeshHost != nil {
		return s.handler.MeshHost()
	}
	return ""
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
	if !s.tokenOK(authReq) {
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
		MeshHost:        s.meshHost(),
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
		if env.Type == "tunnel.open" {
			tuneStreamConn(conn)
			s.serveTunnel(conn, enc, env)
			return
		}
		if env.Type == "share.open" {
			tuneStreamConn(conn)
			s.serveShare(conn, enc, env)
			return
		}
		if env.Type == "daemon.open" {
			tuneStreamConn(conn)
			s.serveDaemonOpen(conn, enc, env)
			return
		}
		if env.Type == "daemon.ctrl" {
			tuneStreamConn(conn)
			s.serveDaemonCtrl(conn, enc, env)
			return
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
	case "input.key":
		var req KeyRequest
		_ = json.Unmarshal(env.Payload, &req)
		if !perms.ScreenShare || s.handler.OnKey == nil {
			return Envelope{ID: env.ID, Type: "input.res", Error: "screen sharing permission disabled"}
		}
		if err := s.handler.OnKey(req.Action); err != nil {
			return Envelope{ID: env.ID, Type: "input.res", Error: err.Error()}
		}
		return Envelope{ID: env.ID, Type: "input.res"}
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

	case "apps.list":
		if s.handler.OnListApps == nil {
			return Envelope{ID: env.ID, Type: "apps.list.res", Error: "app list unavailable"}
		}
		apps, err := s.handler.OnListApps()
		if err != nil {
			return Envelope{ID: env.ID, Type: "apps.list.res", Error: err.Error()}
		}
		return mustEnvelope(env.ID, "apps.list.res", AppListResponse{Apps: apps})

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
					return fmt.Errorf("%w: got %s", ErrFingerprint, gotFP)
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
		ClientName:      clientName(),
		ComputerID:      clientID(),
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
	tuneStreamConn(tlsConn)
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

// ListApps queries launchable applications on the phone through the attached Enhanced mode daemon.
func (c *Client) ListApps(ctx context.Context) ([]DaemonAppEntry, error) {
	var res AppListResponse
	if err := c.call(ctx, "apps.list", struct{}{}, &res); err != nil {
		return nil, err
	}
	return res.Apps, nil
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
	tok, err := s.issueToken(req)
	if err != nil {
		_ = enc.Encode(Envelope{ID: env.ID, Type: "pair.res", Error: err.Error()})
		return
	}
	_ = writeEnvelope(enc, env.ID, "pair.res", PairResponse{Token: tok, CertFingerprint: s.fingerprint, Port: s.tcpPort(), Model: s.model, MeshHost: s.meshHost()}, "")
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
	if err := c.call(ctx, "pair.request", PairRequest{ClientName: clientName, Code: code, ComputerID: clientID()}, &res); err != nil {
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

// tcpPort returns the port of the server's TCP listener, or 0 without one.
func (s *Server) tcpPort() int {
	if ap, err := netip.ParseAddrPort(s.Addr()); err == nil {
		return int(ap.Port())
	}
	return 0
}

// TunnelRequest asks the Companion to join this connection to its own Wireless debugging port.
type TunnelRequest struct {
	Port int `json:"port"`
}

// serveTunnel answers "tunnel.open": after a successful reply the connection stops carrying
// NDJSON and becomes a byte pipe to the phone's Wireless debugging port, until either side closes.
// Notes: needs the cross-network port helper permission; the handler only accepts the port that
// answers as Wireless debugging (`INV-07`). The desktop sends nothing until it reads the reply, so
// no tunnel bytes sit in the line scanner.
func (s *Server) serveTunnel(conn net.Conn, enc *json.Encoder, env Envelope) {
	var req TunnelRequest
	_ = json.Unmarshal(env.Payload, &req)
	if !s.permissions().ADBBeacon || s.handler.OnTunnel == nil {
		_ = enc.Encode(Envelope{ID: env.ID, Type: "tunnel.res", Error: "cross-network port helper permission disabled"})
		return
	}
	target, err := s.handler.OnTunnel(req.Port)
	if err != nil {
		_ = enc.Encode(Envelope{ID: env.ID, Type: "tunnel.res", Error: err.Error()})
		return
	}
	defer target.Close()
	if err := enc.Encode(Envelope{ID: env.ID, Type: "tunnel.res"}); err != nil {
		return
	}
	pipe(conn, target)
}

// tuneStreamConn disables Nagle's algorithm and enables fast TCP keepalive on TCP/TLS stream
// sockets so small packets flow immediately and half-open cellular/Wi-Fi switches fail fast.
func tuneStreamConn(c net.Conn) {
	if tc, ok := c.(*tls.Conn); ok {
		c = tc.NetConn()
	}
	if tcp, ok := c.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(3 * time.Second)
	}
}

// tuneVideoConn applies low-latency stream socket options and caps kernel send buffering on video
// streams so slow 5G or relay paths exert backpressure instead of queuing seconds of frames.
func tuneVideoConn(c net.Conn) {
	tuneStreamConn(c)
	if tc, ok := c.(*tls.Conn); ok {
		c = tc.NetConn()
	}
	if tcp, ok := c.(*net.TCPConn); ok {
		_ = tcp.SetWriteBuffer(64 * 1024)
	}
}

// pipe copies both ways between a and b until one side ends, then closes both.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		buf := make([]byte, 32*1024)
		_, _ = io.CopyBuffer(a, b, buf)
		done <- struct{}{}
	}()
	go func() {
		buf := make([]byte, 32*1024)
		_, _ = io.CopyBuffer(b, a, buf)
		done <- struct{}{}
	}()
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}

// DialTunnel opens a new authenticated connection to the Companion at addr and turns it into a
// byte pipe to the phone's Wireless debugging port (see serveTunnel).
// Returns: the connection, which the caller closes.
func DialTunnel(ctx context.Context, addr, token, fingerprint string, port int) (net.Conn, error) {
	c, err := Dial(ctx, addr, token, fingerprint)
	if err != nil {
		return nil, err
	}
	if err := c.call(ctx, "tunnel.open", TunnelRequest{Port: port}, nil); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c.conn, nil
}

// ServeTunnels listens on a free loopback port and joins each connection to a new tunnel to the
// Companion at addr, so adb can reach the phone's Wireless debugging port as 127.0.0.1:<port>.
// Returns: the loopback address; the listener and its tunnels stop when ctx ends.
func ServeTunnels(ctx context.Context, addr, token, fingerprint string, port int) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
				remote, err := DialTunnel(dctx, addr, token, fingerprint, port)
				cancel()
				if err != nil {
					_ = local.Close()
					return
				}
				pipe(local, remote)
			}()
		}
	}()
	return ln.Addr().String(), nil
}

// KeyRequest is "back", "home" or "recents" for a phone sharing its screen.
type KeyRequest struct {
	Action string `json:"action"`
}

// serveShare answers "share.open": once the phone's user allows capture, the connection stops
// carrying NDJSON and carries the screen's H.264 stream in scrcpy framing until either side ends.
func (s *Server) serveShare(conn net.Conn, enc *json.Encoder, env Envelope) {
	if !s.permissions().ScreenShare || s.handler.OnShare == nil {
		_ = enc.Encode(Envelope{ID: env.ID, Type: "share.res", Error: "screen sharing permission disabled"})
		return
	}
	_ = conn.SetDeadline(time.Time{})
	stream, err := s.handler.OnShare()
	if err != nil {
		_ = enc.Encode(Envelope{ID: env.ID, Type: "share.res", Error: err.Error()})
		return
	}
	defer stream.Close()
	if err := enc.Encode(Envelope{ID: env.ID, Type: "share.res"}); err != nil {
		return
	}
	tuneVideoConn(conn)
	pipe(conn, stream)
}

// DialShare opens a new authenticated connection to the Companion at addr and asks it to share its
// screen; it waits while the phone's user answers Android's consent dialog.
// Returns: the stream (scrcpy framing: device name, codec, a session packet, media packets).
func DialShare(ctx context.Context, addr, token, fingerprint string) (net.Conn, error) {
	c, err := Dial(ctx, addr, token, fingerprint)
	if err != nil {
		return nil, err
	}
	wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := c.call(wait, "share.open", struct{}{}, nil); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c.conn, nil
}

// Key runs Back, Home or Recents on a phone sharing its screen.
func (c *Client) Key(ctx context.Context, action string) error {
	return c.call(ctx, "input.key", KeyRequest{Action: action}, nil)
}

func (s *Server) serveDaemonOpen(conn net.Conn, enc *json.Encoder, env Envelope) {
	if s.handler.OnDaemonStream == nil {
		_ = enc.Encode(Envelope{ID: env.ID, Type: "daemon.open.res", Error: "uid=2000 daemon streaming is unavailable"})
		return
	}
	var req DaemonStreamRequest
	_ = json.Unmarshal(env.Payload, &req)
	_ = conn.SetDeadline(time.Time{})
	sid, video, ctrl, closeFn, err := s.handler.OnDaemonStream(req)
	if err != nil {
		_ = enc.Encode(Envelope{ID: env.ID, Type: "daemon.open.res", Error: err.Error()})
		return
	}
	defer func() {
		_ = video.Close()
		if closeFn != nil {
			closeFn()
		}
	}()
	if ctrl != nil {
		s.mu.Lock()
		if s.pendingCtrl == nil {
			s.pendingCtrl = make(map[string]pendingCtrlEntry)
		}
		s.pendingCtrl[sid] = pendingCtrlEntry{conn: ctrl, stop: closeFn}
		s.mu.Unlock()
		time.AfterFunc(15*time.Second, func() {
			s.mu.Lock()
			if e, ok := s.pendingCtrl[sid]; ok {
				delete(s.pendingCtrl, sid)
				s.mu.Unlock()
				_ = e.conn.Close()
				return
			}
			s.mu.Unlock()
		})
	}
	if err := writeEnvelope(enc, env.ID, "daemon.open.res", DaemonStreamResponse{SessionID: sid}, ""); err != nil {
		if ctrl != nil {
			s.mu.Lock()
			delete(s.pendingCtrl, sid)
			s.mu.Unlock()
			_ = ctrl.Close()
		}
		return
	}
	tuneVideoConn(conn)
	pipe(conn, video)
}

func (s *Server) serveDaemonCtrl(conn net.Conn, enc *json.Encoder, env Envelope) {
	var req DaemonCtrlRequest
	_ = json.Unmarshal(env.Payload, &req)
	s.mu.Lock()
	e, ok := s.pendingCtrl[req.SessionID]
	if ok {
		delete(s.pendingCtrl, req.SessionID)
	}
	s.mu.Unlock()
	if !ok {
		_ = enc.Encode(Envelope{ID: env.ID, Type: "daemon.ctrl.res", Error: "daemon control session not found or timed out"})
		return
	}
	defer e.conn.Close()
	_ = conn.SetDeadline(time.Time{})
	if err := enc.Encode(Envelope{ID: env.ID, Type: "daemon.ctrl.res"}); err != nil {
		return
	}
	pipe(conn, e.conn)
}

// DialDaemonStream opens a scrcpy-server video (and optional control) stream through the phone's
// persistent UID 2000 daemon over Companion TLS 1.3 (`PLAN` §9.2 Phase 2).
func DialDaemonStream(ctx context.Context, addr, token, fingerprint string, req DaemonStreamRequest) (videoConn, ctrlConn net.Conn, err error) {
	vc, err := Dial(ctx, addr, token, fingerprint)
	if err != nil {
		return nil, nil, err
	}
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var res DaemonStreamResponse
	if err := vc.call(wait, "daemon.open", req, &res); err != nil {
		_ = vc.Close()
		return nil, nil, err
	}
	if !req.Control {
		return vc.conn, nil, nil
	}
	cc, err := Dial(ctx, addr, token, fingerprint)
	if err != nil {
		_ = vc.Close()
		return nil, nil, err
	}
	cWait, cCancel := context.WithTimeout(ctx, 10*time.Second)
	defer cCancel()
	if err := cc.call(cWait, "daemon.ctrl", DaemonCtrlRequest{SessionID: res.SessionID}, nil); err != nil {
		_ = cc.Close()
		_ = vc.Close()
		return nil, nil, err
	}
	return vc.conn, cc.conn, nil
}

// MarshalIdentity encodes cert (certificate and private key) as PEM, for the phone to keep sealed.
func MarshalIdentity(cert tls.Certificate) ([]byte, error) {
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		return nil, err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	return append(out, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})...), nil
}

// ParseIdentity decodes what MarshalIdentity wrote.
func ParseIdentity(b []byte) (tls.Certificate, error) {
	return tls.X509KeyPair(b, b)
}

// tokenOK checks an auth request's token: with the phone's own pairings (Handler.CheckToken), else
// against the one shared token, in constant time.
func (s *Server) tokenOK(req AuthRequest) bool {
	if s.handler.CheckToken != nil {
		return s.handler.CheckToken(req)
	}
	s.mu.Lock()
	expected := s.token
	s.mu.Unlock()
	return subtle.ConstantTimeCompare([]byte(req.Token), []byte(expected)) == 1
}

// issueToken is the token an allowed pairing returns: the computer's own (Handler.IssueToken),
// else the shared one.
func (s *Server) issueToken(req PairRequest) (string, error) {
	if s.handler.IssueToken != nil {
		return s.handler.IssueToken(req)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token, nil
}

// self is how this process introduces itself to Companions (SetClient).
var self struct {
	sync.Mutex
	id, name string
}

// SetClient sets the id and name this computer gives Companions when it pairs and connects, so a
// phone keeps one pairing per computer and lists it by name.
func SetClient(id, name string) {
	self.Lock()
	self.id, self.name = id, name
	self.Unlock()
}

func clientID() string {
	self.Lock()
	defer self.Unlock()
	return self.id
}

func clientName() string {
	self.Lock()
	defer self.Unlock()
	if self.name == "" {
		return "AndroFuse-Desktop"
	}
	return self.name
}
