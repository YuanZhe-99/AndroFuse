// Package localapi implements the per-user AF_UNIX NDJSON server for
// androfuse-svc (`INV-02`, `INV-03`, `INV-08`).
package localapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/dex"
	"github.com/YuanZhe-99/AndroFuse/internal/lease"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
)

// Request is one NDJSON RPC line accepted by the localapi server.
type Request struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is one NDJSON RPC reply written by the localapi server.
type Response struct {
	ID     string `json:"id,omitempty"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Options configures a localapi Server instance.
type Options struct {
	SocketPath string
	StateDir   string
	Profile    string
	Leases     *lease.Store
	ADB        *adb.Client
}

// Server serves NDJSON commands over a per-user AF_UNIX socket.
type Server struct {
	opts     Options
	listener net.Listener
	mu       sync.Mutex
	closed   bool
	wg       sync.WaitGroup
}

// DefaultSocketPath returns the default per-user AF_UNIX socket path.
func DefaultSocketPath(profile string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := "AndroFuse"
	if profile != "" {
		dir += "-" + profile
	}
	return filepath.Join(base, dir, "androfuse-svc.sock"), nil
}

// Listen binds the AF_UNIX socket (removing any stale socket file first) and
// returns a ready Server.
//
// Side effects: Creates parent directory and binds the local Unix domain socket.
func Listen(opts Options) (*Server, error) {
	if strings.TrimSpace(opts.SocketPath) == "" {
		p, err := DefaultSocketPath(opts.Profile)
		if err != nil {
			return nil, err
		}
		opts.SocketPath = p
	}
	if err := os.MkdirAll(filepath.Dir(opts.SocketPath), 0o700); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	_ = os.Remove(opts.SocketPath)
	ln, err := net.Listen("unix", opts.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", opts.SocketPath, err)
	}
	return &Server{
		opts:     opts,
		listener: ln,
	}, nil
}

// SocketPath returns the bound AF_UNIX socket path.
func (s *Server) SocketPath() string {
	return s.opts.SocketPath
}

// Serve accepts connections until ctx is cancelled or Close is called.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				s.wg.Wait()
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

// Close closes the AF_UNIX listener, waits for active clients, and removes the socket file.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.listener
	s.mu.Unlock()

	var err error
	if ln != nil {
		err = ln.Close()
	}
	s.wg.Wait()
	_ = os.Remove(s.opts.SocketPath)
	return err
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	if err := verifySameUserPeer(conn); err != nil {
		return
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	enc := json.NewEncoder(conn)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(Response{OK: false, Error: fmt.Sprintf("invalid request json: %v", err)})
			continue
		}
		resp := s.dispatch(ctx, req)
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

func (s *Server) dispatch(ctx context.Context, req Request) Response {
	switch req.Method {
	case "ping":
		return Response{
			ID: req.ID,
			OK: true,
			Result: map[string]any{
				"pong":    true,
				"version": version.Version,
			},
		}
	case "status":
		leaseCount := 0
		if s.opts.Leases != nil {
			leaseCount = len(s.opts.Leases.List())
		}
		return Response{
			ID: req.ID,
			OK: true,
			Result: map[string]any{
				"version":      version.Version,
				"state_dir":    s.opts.StateDir,
				"profile":      s.opts.Profile,
				"socket_path":  s.opts.SocketPath,
				"active_lease": leaseCount,
			},
		}
	case "devices":
		if s.opts.ADB == nil {
			return Response{ID: req.ID, OK: false, Error: "adb client unavailable"}
		}
		qCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		devs, err := s.opts.ADB.Devices(qCtx)
		if err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		svcs, _ := s.opts.ADB.MDNSServices(qCtx)
		return Response{
			ID: req.ID,
			OK: true,
			Result: map[string]any{
				"devices": devs,
				"mdns":    svcs,
			},
		}
	case "leases.list":
		if s.opts.Leases == nil {
			return Response{ID: req.ID, OK: true, Result: []lease.Lease{}}
		}
		return Response{ID: req.ID, OK: true, Result: s.opts.Leases.List()}
	case "leases.recover":
		n := RecoverLeases(ctx, s.opts.ADB, s.opts.Leases)
		return Response{
			ID:     req.ID,
			OK:     true,
			Result: map[string]any{"recovered": n},
		}
	default:
		return Response{
			ID:    req.ID,
			OK:    false,
			Error: fmt.Sprintf("unknown method %q", req.Method),
		}
	}
}

// RecoverLeases tears down stale port forwards and DeX loopbacks recorded in store (`INV-03`).
func RecoverLeases(ctx context.Context, client *adb.Client, store *lease.Store) int {
	if store == nil {
		return 0
	}
	stale := store.List()
	if len(stale) == 0 {
		return 0
	}
	byHash := make(map[string]adb.Device)
	if client != nil {
		qCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		if devs, err := client.Devices(qCtx); err == nil {
			for _, d := range devs {
				if d.State == "device" {
					byHash[d.DeviceHash] = d
				}
			}
		}
		cancel()
	}
	recovered := 0
	for _, l := range stale {
		if dev, ok := byHash[l.DeviceHash]; ok && client != nil {
			rCtx, rCancel := context.WithTimeout(ctx, 5*time.Second)
			if l.ForwardPort > 0 {
				_ = client.RemoveForward(rCtx, dev.Serial, l.ForwardPort)
			}
			if l.Kind == lease.KindNativeDeX {
				_ = dex.Recover(rCtx, client, dev.Serial)
			}
			rCancel()
		}
		if err := store.Release(l.SessionID); err == nil {
			recovered++
		}
	}
	return recovered
}

// Call connects to socketPath, sends one NDJSON Request, and decodes the Response.
func Call(ctx context.Context, socketPath string, req Request) (Response, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}
