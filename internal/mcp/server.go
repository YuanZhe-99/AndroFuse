// Package mcp implements AndroFuse's opt-in, default-disabled local MCP control server.
//
// Invariants enforced here:
//   - INV-07 / INV-08: Disabled by default; explicit user activation binds strictly to
//     127.0.0.1 with an ephemeral high-entropy bearer token, bounded TTL, per-tool
//     allowlist, and an emergency stop that revokes access immediately.
//   - Secret/device scrubbing: Audit entries never record bearer tokens or raw device IDs.
package mcp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/version"
)

const (
	defaultTokenTTL = 30 * time.Minute
	maxAuditEntries = 64
	maxRequestBytes = 1 << 20
)

// Allowlist controls which read/control tools the local MCP server exposes while enabled.
type Allowlist struct {
	ListDevices    bool `json:"listDevices"`
	RunDiagnostics bool `json:"runDiagnostics"`
	LaunchApp      bool `json:"launchApp"`
}

// Handlers binds concrete desktop operations invoked by allowlisted MCP tool calls.
type Handlers struct {
	ListDevices    func(ctx context.Context) (any, error)
	RunDiagnostics func(ctx context.Context) (any, error)
	LaunchApp      func(ctx context.Context, deviceID, packageName string) (any, error)
	// Approve asks the user about one call of a tool the Allowlist does not let run on its own;
	// it blocks until they answer or ctx ends. Nil refuses such calls.
	Approve func(ctx context.Context, tool, detail string) bool
}

// AuditEntry captures one scrubbed MCP lifecycle or request decision without secrets.
type AuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Method    string    `json:"method"`
	Tool      string    `json:"tool,omitempty"`
	Allowed   bool      `json:"allowed"`
	Outcome   string    `json:"outcome"`
}

// Status reports a thread-safe snapshot of the local MCP server state and recent audit trail.
type Status struct {
	Enabled   bool         `json:"enabled"`
	Addr      string       `json:"addr,omitempty"`
	Token     string       `json:"token,omitempty"`
	ExpiresAt time.Time    `json:"expiresAt,omitempty"`
	Allow     Allowlist    `json:"allow"`
	Audit     []AuditEntry `json:"audit,omitempty"`
}

// Server hosts an audited loopback-only HTTP JSON-RPC 2.0 MCP endpoint that starts disabled.
type Server struct {
	mu       sync.Mutex
	enabled  bool
	addr     string
	token    string
	expires  time.Time
	ttl      time.Duration
	allow    Allowlist
	handlers Handlers
	ln       net.Listener
	httpSrv  *http.Server
	audit    []AuditEntry
}

// NewServer constructs a disabled MCP server using h for allowlisted operations.
// Inputs: h provides optional tool handlers; ttl <= 0 defaults to 30 minutes.
// Returns: A disabled Server ready for explicit user Enable.
func NewServer(h Handlers, ttl time.Duration) *Server {
	if ttl <= 0 {
		ttl = defaultTokenTTL
	}
	return &Server{
		ttl:      ttl,
		handlers: h,
	}
}

// Enable binds the server to 127.0.0.1 on an ephemeral port with a fresh bearer token.
// Inputs: allow chooses which tools may execute during this grant window.
// Returns: The active Status containing the one-time local token, or an error.
// Side effects: Opens a loopback TCP listener and starts an HTTP server goroutine.
func (s *Server) Enable(allow Allowlist) (Status, error) {
	token, err := generateBearerToken()
	if err != nil {
		return Status{}, fmt.Errorf("mcp: generate token: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Status{}, fmt.Errorf("mcp: listen loopback: %w", err)
	}

	httpSrv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
	}

	s.mu.Lock()
	oldSrv := s.httpSrv
	oldLn := s.ln
	s.enabled = true
	s.addr = ln.Addr().String()
	s.token = token
	s.expires = time.Now().Add(s.ttl)
	s.allow = allow
	s.ln = ln
	s.httpSrv = httpSrv
	s.appendAuditLocked("server.enable", "", true, "enabled")
	st := s.snapshotLocked()
	s.mu.Unlock()

	if oldSrv != nil {
		_ = oldSrv.Close()
	} else if oldLn != nil {
		_ = oldLn.Close()
	}

	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.mu.Lock()
			if s.httpSrv == httpSrv {
				s.enabled = false
				s.token = ""
				s.expires = time.Time{}
				s.appendAuditLocked("server.error", "", false, "stopped")
			}
			s.mu.Unlock()
		}
	}()

	return st, nil
}

// EmergencyStop immediately revokes the active token and closes the loopback listener.
// Side effects: Terminates HTTP serving, clears the allowlist/token, and logs audit history.
func (s *Server) EmergencyStop() error {
	s.mu.Lock()
	wasEnabled := s.enabled || s.httpSrv != nil || s.ln != nil
	httpSrv := s.httpSrv
	ln := s.ln
	s.enabled = false
	s.addr = ""
	s.token = ""
	s.expires = time.Time{}
	s.allow = Allowlist{}
	s.httpSrv = nil
	s.ln = nil
	if wasEnabled {
		s.appendAuditLocked("server.emergency_stop", "", true, "revoked")
	}
	s.mu.Unlock()

	var closeErr error
	if httpSrv != nil {
		closeErr = httpSrv.Close()
	} else if ln != nil {
		closeErr = ln.Close()
	}
	return closeErr
}

// Status returns the current server state and recent scrubbed audit log entries.
func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enabled && !s.expires.IsZero() && time.Now().After(s.expires) {
		httpSrv := s.httpSrv
		s.enabled = false
		s.token = ""
		s.addr = ""
		s.expires = time.Time{}
		s.allow = Allowlist{}
		s.httpSrv = nil
		s.ln = nil
		s.appendAuditLocked("server.ttl_expired", "", false, "expired")
		st := s.snapshotLocked()
		if httpSrv != nil {
			go func() { _ = httpSrv.Close() }()
		}
		return st
	}
	return s.snapshotLocked()
}

// ServeHTTP authenticates bearer tokens, checks TTL, and dispatches JSON-RPC 2.0 MCP calls.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	enabled := s.enabled
	expectedToken := s.token
	expires := s.expires
	allow := s.allow
	s.mu.Unlock()

	if !enabled || expectedToken == "" {
		s.recordAudit("http.request", "", false, "disabled")
		http.Error(w, "mcp disabled", http.StatusServiceUnavailable)
		return
	}
	if !expires.IsZero() && time.Now().After(expires) {
		_ = s.EmergencyStop()
		s.recordAudit("http.request", "", false, "token_expired")
		http.Error(w, "token expired", http.StatusUnauthorized)
		return
	}

	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(authz, prefix) {
		s.recordAudit("auth", "", false, "missing_bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	provided := strings.TrimSpace(strings.TrimPrefix(authz, prefix))
	if len(provided) != len(expectedToken) ||
		subtle.ConstantTimeCompare([]byte(provided), []byte(expectedToken)) != 1 {
		s.recordAudit("auth", "", false, "invalid_token")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, -32700, "invalid JSON payload")
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		writeRPCError(w, req.ID, -32600, "invalid JSON-RPC 2.0 request")
		return
	}

	switch req.Method {
	case "initialize":
		s.recordAudit("initialize", "", true, "ok")
		writeRPCResult(w, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    "androfuse-mcp",
				"version": version.Version,
			},
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusNoContent)
	case "tools/list":
		s.recordAudit("tools/list", "", true, "ok")
		writeRPCResult(w, req.ID, map[string]any{
			"tools": allowedToolDescriptors(s.listed(allow)),
		})
	case "tools/call":
		s.handleToolCall(w, r.Context(), req, allow)
	default:
		s.recordAudit(req.Method, "", false, "unknown_method")
		writeRPCError(w, req.ID, -32601, "method not found")
	}
}

func (s *Server) handleToolCall(w http.ResponseWriter, ctx context.Context, req rpcRequest, allow Allowlist) {
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &call); err != nil {
			s.recordAudit("tools/call", "", false, "bad_params")
			writeRPCError(w, req.ID, -32602, "invalid tool call parameters")
			return
		}
	}

	switch call.Name {
	case "list_devices":
		if s.handlers.ListDevices == nil || !s.permit(ctx, allow.ListDevices, call.Name, "") {
			s.recordAudit("tools/call", call.Name, false, "not_allowlisted")
			writeRPCError(w, req.ID, -32003, "tool disabled by allowlist")
			return
		}
		res, err := s.handlers.ListDevices(ctx)
		if err != nil {
			s.recordAudit("tools/call", call.Name, true, "handler_error")
			writeRPCError(w, req.ID, -32000, err.Error())
			return
		}
		s.recordAudit("tools/call", call.Name, true, "ok")
		writeToolContent(w, req.ID, res)
	case "run_diagnostics":
		if s.handlers.RunDiagnostics == nil || !s.permit(ctx, allow.RunDiagnostics, call.Name, "") {
			s.recordAudit("tools/call", call.Name, false, "not_allowlisted")
			writeRPCError(w, req.ID, -32003, "tool disabled by allowlist")
			return
		}
		res, err := s.handlers.RunDiagnostics(ctx)
		if err != nil {
			s.recordAudit("tools/call", call.Name, true, "handler_error")
			writeRPCError(w, req.ID, -32000, err.Error())
			return
		}
		s.recordAudit("tools/call", call.Name, true, "ok")
		writeToolContent(w, req.ID, res)
	case "launch_app":
		deviceID, _ := call.Arguments["device_id"].(string)
		packageName, _ := call.Arguments["package_name"].(string)
		deviceID = strings.TrimSpace(deviceID)
		packageName = strings.TrimSpace(packageName)
		if deviceID == "" || packageName == "" {
			s.recordAudit("tools/call", call.Name, false, "invalid_args")
			writeRPCError(w, req.ID, -32602, "device_id and package_name are required")
			return
		}
		if s.handlers.LaunchApp == nil || !s.permit(ctx, allow.LaunchApp, call.Name, packageName) {
			s.recordAudit("tools/call", call.Name, false, "not_allowlisted")
			writeRPCError(w, req.ID, -32003, "tool disabled by allowlist")
			return
		}
		res, err := s.handlers.LaunchApp(ctx, deviceID, packageName)
		if err != nil {
			s.recordAudit("tools/call", call.Name, true, "handler_error")
			writeRPCError(w, req.ID, -32000, err.Error())
			return
		}
		s.recordAudit("tools/call", call.Name, true, "ok")
		writeToolContent(w, req.ID, res)
	default:
		s.recordAudit("tools/call", call.Name, false, "unknown_tool")
		writeRPCError(w, req.ID, -32601, "unknown tool")
	}
}

func (s *Server) snapshotLocked() Status {
	auditCopy := make([]AuditEntry, len(s.audit))
	copy(auditCopy, s.audit)
	st := Status{
		Enabled: s.enabled,
		Allow:   s.allow,
		Audit:   auditCopy,
	}
	if s.enabled {
		st.Addr = s.addr
		st.Token = s.token
		st.ExpiresAt = s.expires
	}
	return st
}

func (s *Server) recordAudit(method, tool string, allowed bool, outcome string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendAuditLocked(method, tool, allowed, outcome)
}

func (s *Server) appendAuditLocked(method, tool string, allowed bool, outcome string) {
	entry := AuditEntry{
		Timestamp: time.Now().UTC(),
		Method:    method,
		Tool:      tool,
		Allowed:   allowed,
		Outcome:   outcome,
	}
	s.audit = append(s.audit, entry)
	if len(s.audit) > maxAuditEntries {
		s.audit = append([]AuditEntry(nil), s.audit[len(s.audit)-maxAuditEntries:]...)
	}
}

func generateBearerToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func allowedToolDescriptors(allow Allowlist) []map[string]any {
	var tools []map[string]any
	if allow.ListDevices {
		tools = append(tools, map[string]any{
			"name":        "list_devices",
			"description": "List currently discovered Android/DeX devices and routes.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		})
	}
	if allow.RunDiagnostics {
		tools = append(tools, map[string]any{
			"name":        "run_diagnostics",
			"description": "Run non-destructive AndroFuse host and ADB diagnostics.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		})
	}
	if allow.LaunchApp {
		tools = append(tools, map[string]any{
			"name":        "launch_app",
			"description": "Launch an Android app window on the selected device when explicitly allowlisted.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"device_id":    map[string]any{"type": "string"},
					"package_name": map[string]any{"type": "string"},
				},
				"required": []string{"device_id", "package_name"},
			},
		})
	}
	return tools
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      idValue(id),
		"result":  result,
	})
}

func writeToolContent(w http.ResponseWriter, id json.RawMessage, payload any) {
	encoded, _ := json.Marshal(payload)
	writeRPCResult(w, id, map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": string(encoded),
			},
		},
	})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      idValue(id),
		"error": map[string]any{
			"code":    code,
			"message": msg,
		},
	})
}

func idValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// permit reports whether one call of tool may run: always when allowed (the Allowlist), else only
// when the user allows this call (Handlers.Approve), which is audited either way.
func (s *Server) permit(ctx context.Context, allowed bool, tool, detail string) bool {
	if allowed {
		return true
	}
	if s.handlers.Approve == nil {
		return false
	}
	ok := s.handlers.Approve(ctx, tool, detail)
	outcome := "user_denied"
	if ok {
		outcome = "user_allowed_once"
	}
	s.recordAudit("approval", tool, ok, outcome)
	return ok
}

// listed is the tools tools/list shows: all of them while calls can be approved one by one,
// else only the allowlisted ones.
func (s *Server) listed(allow Allowlist) Allowlist {
	if s.handlers.Approve != nil {
		return Allowlist{ListDevices: true, RunDiagnostics: true, LaunchApp: true}
	}
	return allow
}
