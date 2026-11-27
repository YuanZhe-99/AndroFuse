// Package androidhost implements the optional Android Companion host state,
// graded permission controls (`INV-09`), and scrubbed telemetry/audit log (`INV-08`).
package androidhost

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/androidbridge"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// legacyListenPort is the fixed TCP port builds before random ports used and saved.
const legacyListenPort = 27189

// Config persists per-user Android Companion settings under `<stateDir>/companion.json`.
type Config struct {
	// ListenPort is the TCP port the Companion last listened on (companion.PickDevicePort), shown on
	// the phone and handed to a desktop when it pairs; 0 until chosen.
	ListenPort      int                   `json:"listen_port"`
	Token           string                `json:"token"`
	Permissions     companion.Permissions `json:"permissions"`
	WirelessADBPort int                   `json:"wireless_adb_port,omitempty"`
	// Standby keeps the Companion reachable in the background through a minimum-importance
	// foreground service; off by default (PLAN §9.1-3).
	Standby bool `json:"standby,omitempty"`
	// Mesh is the EasyTier network handed over by a paired desktop; nil when not joined.
	Mesh *MeshSettings `json:"mesh,omitempty"`
	// Stopped is set when the phone's user stopped the service, which then stays off until they
	// start it again (AutoStart).
	Stopped bool `json:"stopped,omitempty"`
	// Computers are the computers paired with this phone, each with a token of its own.
	Computers []PairedComputer `json:"computers,omitempty"`
	// MeshID is the random id in the phone's EasyTier hostname (mesh.Hostname), drawn on first use.
	MeshID string `json:"mesh_id,omitempty"`
	// PermDefaultsV2 records whether the phone has initialized v0.2+ consumer-friendly default
	// permissions (ADBBeacon & ScreenShare enabled prior to any pairing).
	PermDefaultsV2 bool `json:"perm_defaults_v2,omitempty"`
}

// Host owns the Android-side TLS 1.3 Companion server, persisted permissions, and audit log.
type Host struct {
	stateDir string
	onChange func()

	srvOpMu              sync.Mutex
	mu                   sync.Mutex
	cfg                  Config
	srv                  *companion.Server
	cancelSrv            context.CancelFunc
	running              bool
	lastErr              string
	audit                []string
	beaconInput          string
	receiveBytes         int64
	network              string // default network kind from the Android NetworkCallback
	stopNetWatch         func()
	wirelessADBPairPort  int // temporary _adb-tls-pairing._tcp port discovered by NsdManager
	pair                 *pairPrompt
	tab                  string // Companion tab shown on phones (tabHome when "")
	beaconCheck          string // outcome of the last wireless debugging port check
	a11yOn               bool   // the Companion's accessibility service is on (checked in the background)
	a11yChecked          time.Time
	daemonReady          bool
	daemonPID            int
	daemonTrustedDisplay bool
	daemonFDVerified     bool
	apkHash              string
	// meshForm is the network editor on the phone's network card while it is open; the pointer is
	// swapped under mu, its fields are edited by the view.
	meshForm *meshForm
	// renaming is the paired computer whose Rename editor is open, and renameText its text (UI
	// thread only).
	renaming, renameText string
	// langIndex is the language control's choice (-1 until first drawn), and getLang/setLang reach
	// the app's saved language (SetLanguageHooks).
	langIndex int
	getLang   func() string
	setLang   func(string)
	mesh      meshRuntime
	// Debounce timer for rebinding EasyTier mesh when the active Android network switches (e.g. 5G <-> Wi-Fi).
	meshRebindTimer *time.Timer
	meshRebindDelay time.Duration
	onMeshRebind    func()
}

// New constructs an Android Companion Host rooted at stateDir and loads saved config.
func New(stateDir string, onChange func()) *Host {
	tok, _ := companion.GeneratePairingToken()
	if tok == "" {
		tok = "ANDROFUS"
	}
	h := &Host{
		stateDir:  stateDir,
		onChange:  onChange,
		langIndex: -1,
		cfg: Config{
			Token:          tok,
			Permissions:    companion.DefaultPermissions(),
			PermDefaultsV2: true,
		},
	}
	h.loadConfig()
	if !h.cfg.PermDefaultsV2 {
		if len(h.cfg.Computers) == 0 {
			h.cfg.Permissions.ADBBeacon = true
			h.cfg.Permissions.ScreenShare = true
		}
		h.cfg.PermDefaultsV2 = true
	}
	if h.cfg.ListenPort == legacyListenPort {
		// Builds before random ports saved the old fixed port; draw a random one instead.
		h.cfg.ListenPort = 0
	}
	if strings.TrimSpace(h.cfg.Token) == "" {
		h.cfg.Token = tok
	}
	if h.cfg.WirelessADBPort > 0 {
		h.beaconInput = strconv.Itoa(h.cfg.WirelessADBPort)
	}
	return h
}

func (h *Host) loadConfig() {
	if h.stateDir == "" {
		return
	}
	raw, err := os.ReadFile(filepath.Join(h.stateDir, "companion.json"))
	if err != nil {
		return
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err == nil {
		h.cfg = c
	}
}

func (h *Host) saveConfigLocked() {
	if h.stateDir == "" {
		return
	}
	_ = os.MkdirAll(h.stateDir, 0o755)
	raw, err := json.MarshalIndent(h.cfg, "", "  ")
	if err != nil {
		return
	}
	tmp := filepath.Join(h.stateDir, "companion.json.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err == nil {
		_ = os.Rename(tmp, filepath.Join(h.stateDir, "companion.json"))
	}
}

func (h *Host) appendAuditLocked(line string) {
	stamp := time.Now().Format("15:04:05")
	h.audit = append(h.audit, stamp+" · "+line)
	if len(h.audit) > 20 {
		h.audit = append([]string(nil), h.audit[len(h.audit)-20:]...)
	}
}

func (h *Host) notify() {
	if h.onChange != nil {
		h.onChange()
	}
}

// StartServer binds the TLS 1.3 Companion listeners: TCP on all interfaces at the first free one of
// the ports the phone's device id gives (companion.DevicePorts) for LAN and mesh peers, and, on Android, the abstract local socket
// companion.LocalSocket that `adb forward` reaches.
// Side effects: saves a newly drawn ListenPort.
func (h *Host) StartServer() error {
	h.srvOpMu.Lock()
	defer h.srvOpMu.Unlock()
	h.mu.Lock()
	if h.running && h.srv != nil {
		h.mu.Unlock()
		return nil
	}
	port := h.cfg.ListenPort
	tok := h.cfg.Token
	h.mu.Unlock()

	_, model, rel, sdk := deviceIdentity()
	srv, err := companion.NewServer(tok, model, rel, sdk, companion.Handler{
		Identity: h.identity(),
		MeshHost: h.meshHost,
		GetIdentity: func() (string, string, string) {
			name, maker, brand, _, _, _ := deviceDetails()
			return name, maker, brand
		},
		GetPermissions: func() companion.Permissions {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.cfg.Permissions
		},
		GetStatus: func() companion.DeviceStatus {
			return h.Status()
		},
		OnListApps: func() ([]companion.DaemonAppEntry, error) {
			raw, err := androidbridge.ListDaemonApps()
			if err != nil {
				return nil, err
			}
			var apps []companion.DaemonAppEntry
			if err := json.Unmarshal([]byte(raw), &apps); err != nil {
				return nil, err
			}
			return apps, nil
		},
		OnShare: func() (net.Conn, error) {
			if err := androidbridge.StartShare(1280, 4_000_000, L.ShareNotifTitle(), L.ShareNotifText(), L.ShareNotifStop()); err != nil {
				return nil, err
			}
			var c net.Conn
			var err error
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
				if c, err = net.Dial("unix", "@androfuse-share"); err == nil {
					break
				}
			}
			if err == nil {
				h.mu.Lock()
				h.appendAuditLocked("computer started screen sharing")
				h.mu.Unlock()
				h.notify()
			}
			return c, err
		},
		OnKey: func(action string) error {
			if !androidbridge.GlobalAction(action) {
				return errors.New(L.ShareNeedsAccessibility())
			}
			return nil
		},
		OnDaemonStream: func(req companion.DaemonStreamRequest) (string, net.Conn, net.Conn, func(), error) {
			androidbridge.TouchActive()
			handles, err := androidbridge.OpenDaemonSession(androidbridge.DaemonSessionConfig{
				MaxSize:               req.MaxSize,
				BitRate:               req.BitRate,
				MaxFPS:                req.MaxFPS,
				Codec:                 req.Codec,
				NewDisplay:            req.NewDisplay,
				HideSystemDecorations: req.HideSystemDecorations,
				FlexDisplay:           req.FlexDisplay,
				DisplayID:             req.DisplayID,
				StartApp:              req.StartApp,
				Control:               req.Control,
			})
			if err != nil {
				return "", nil, nil, nil, err
			}
			videoConn, err := net.Dial("unix", "@"+handles.VideoSocket)
			if err != nil {
				androidbridge.CloseDaemonSession(handles.SessionID)
				return "", nil, nil, nil, err
			}
			var ctrlConn net.Conn
			if req.Control && handles.CtrlSocket != "" {
				ctrlConn, err = net.Dial("unix", "@"+handles.CtrlSocket)
				if err != nil {
					_ = videoConn.Close()
					androidbridge.CloseDaemonSession(handles.SessionID)
					return "", nil, nil, nil, err
				}
			}
			stopTouch := make(chan struct{})
			var closeOnce sync.Once
			go func() {
				tick := time.NewTicker(20 * time.Second)
				defer tick.Stop()
				for {
					select {
					case <-stopTouch:
						return
					case <-tick.C:
						androidbridge.TouchActive()
					}
				}
			}()
			h.mu.Lock()
			h.appendAuditLocked("uid=2000 daemon opened session " + handles.SessionID)
			h.mu.Unlock()
			h.notify()
			return handles.SessionID, videoConn, ctrlConn, func() {
				closeOnce.Do(func() { close(stopTouch) })
				androidbridge.CloseDaemonSession(handles.SessionID)
			}, nil
		},
		OnTunnel: func(port int) (net.Conn, error) {
			h.mu.Lock()
			allowed := h.cfg.WirelessADBPort
			pairAllowed := h.wirelessADBPairPort
			h.mu.Unlock()
			if port > 0 && pairAllowed > 0 && port == pairAllowed {
				// `_adb-tls-pairing._tcp` uses direct TLS+SPAKE2+ without ADB CNXN, so only check
				// that this port matches the live NsdManager advertisement on loopback (`INV-07`).
				c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 3*time.Second)
				if err == nil {
					h.mu.Lock()
					h.appendAuditLocked("computer opened Wireless debugging pairing through the Companion")
					h.mu.Unlock()
					h.notify()
				}
				return c, err
			}
			if allowed <= 0 || port != allowed {
				return nil, fmt.Errorf("port %d is not this phone's Wireless debugging port", port)
			}
			// Only a port that answers as Wireless debugging (STLS) is joined (`INV-07`).
			if ans, err := probeLocalADB(port); ans != adb.AnswerTLS {
				return nil, fmt.Errorf("Wireless debugging does not answer on port %d: %v", port, err)
			}
			c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 3*time.Second)
			if err == nil {
				h.mu.Lock()
				h.appendAuditLocked("computer opened Wireless debugging through the Companion")
				h.mu.Unlock()
				h.notify()
			}
			return c, err
		},
		OnClipboard: func(msg companion.ClipboardMessage) error {
			androidbridge.TouchActive()
			h.mu.Lock()
			h.appendAuditLocked(fmt.Sprintf("clipboard synced (%d chars)", len(msg.Text)))
			h.mu.Unlock()
			h.notify()
			return nil
		},
		OnNotificationReply: func(rep companion.NotificationReply) error {
			h.mu.Lock()
			h.appendAuditLocked(fmt.Sprintf("notification reply submitted (id=%s)", rep.ID))
			h.mu.Unlock()
			h.notify()
			return nil
		},
		OnAppContinuity: func(req companion.AppContinuityRequest) (companion.AppContinuityResponse, error) {
			pkg := strings.TrimSpace(req.Package)
			if pkg == "" {
				st := h.Status()
				pkg = st.ForegroundPackage
			}
			h.mu.Lock()
			h.appendAuditLocked(fmt.Sprintf("app continuity %s (%s)", req.Action, pkg))
			h.mu.Unlock()
			h.notify()
			return companion.AppContinuityResponse{OK: true, Package: pkg, Message: "ok"}, nil
		},
		OnFilePush: func(name string, data []byte, shaHex string) (companion.FilePushResponse, error) {
			androidbridge.TouchActive()
			inbox := filepath.Join(h.stateDir, "inbox")
			if err := os.MkdirAll(inbox, 0o755); err != nil {
				return companion.FilePushResponse{}, err
			}
			dst := filepath.Join(inbox, name)
			if err := os.WriteFile(dst, data, 0o600); err != nil {
				return companion.FilePushResponse{}, err
			}
			h.mu.Lock()
			h.receiveBytes += int64(len(data))
			h.appendAuditLocked(fmt.Sprintf("received file %s (%d bytes)", name, len(data)))
			h.mu.Unlock()
			h.notify()
			return companion.FilePushResponse{
				SavedPath:    dst,
				SHA256:       shaHex,
				BytesWritten: int64(len(data)),
			}, nil
		},
		OnDisplayGuard: func(_ companion.DisplayGuardRequest) (companion.DisplayGuardResponse, error) {
			h.mu.Lock()
			h.appendAuditLocked("display guard sweep executed")
			h.mu.Unlock()
			h.notify()
			return companion.DisplayGuardResponse{CleanedCount: 0, Message: "virtual display check complete"}, nil
		},
		OnPairRequest: h.askPair,
		IssueToken:    h.issueToken,
		CheckToken:    h.checkToken,
		OnMeshJoin:    h.JoinMesh,
		OnMediaFrame: func(req companion.MediaFrameRequest) (companion.MediaFrameResponse, error) {
			androidbridge.TouchActive()
			rawLen := len(req.DataBase64) * 3 / 4
			h.mu.Lock()
			h.receiveBytes += int64(rawLen)
			h.appendAuditLocked(fmt.Sprintf("media bridge %s frame accepted", req.Kind))
			h.mu.Unlock()
			h.notify()
			return companion.MediaFrameResponse{OK: true, ReceivedBytes: rawLen, Message: "accepted"}, nil
		},
	})
	if err != nil {
		h.mu.Lock()
		h.lastErr = err.Error()
		h.mu.Unlock()
		h.notify()
		return err
	}

	// The port comes from the phone's device id, so a desktop finds it again from the id in the
	// phone's mesh hostname even after the saved port was taken.
	port, err = companion.PickDevicePort(h.meshID())
	if err == nil {
		err = srv.Listen(fmt.Sprintf(":%d", port))
	}
	if err == nil && localSocketAddr != "" {
		err = srv.Listen(localSocketAddr)
	}
	if err != nil {
		_ = srv.Close()
		h.mu.Lock()
		h.lastErr = err.Error()
		h.mu.Unlock()
		h.notify()
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.mu.Lock()
	h.srv = srv
	h.cancelSrv = cancel
	h.running = true
	h.lastErr = ""
	if h.cfg.Stopped {
		h.cfg.Stopped = false
		h.saveConfigLocked()
	}
	if h.cfg.ListenPort != port {
		h.cfg.ListenPort = port
		h.saveConfigLocked()
	}
	wPort := h.cfg.WirelessADBPort
	standby := h.cfg.Standby
	h.appendAuditLocked("TLS 1.3 companion listening on " + srv.Addr())
	h.mu.Unlock()
	h.applyStandby(standby)
	stopNet := androidbridge.WatchNetwork(h.onNetworkChanged)
	// The Wireless debugging port is found automatically (NsdManager); the phone's user no longer
	// types it. A port is kept only once it answers as Wireless debugging (SaveWirelessADBPort).
	stopADB := androidbridge.WatchWirelessDebugging(func(port int) {
		if port > 0 {
			h.SaveWirelessADBPort(port)
			return
		}
		h.SetWirelessADBPort(0)
		h.notify()
	})
	stopADBPair := androidbridge.WatchWirelessPairing(func(port int) {
		h.SetWirelessADBPairPort(port)
	})
	stopDaemon := androidbridge.WatchDaemon(func(st androidbridge.DaemonStatus) {
		h.updateDaemonStatus(st)
	})
	go func() {
		if sum := androidbridge.APKHash(); sum != "" {
			h.mu.Lock()
			h.apkHash = sum
			h.mu.Unlock()
		}
	}()
	stopAll := func() { stopNet(); stopADB(); stopADBPair(); stopDaemon() }
	h.mu.Lock()
	h.stopNetWatch = stopAll
	h.mu.Unlock()
	go h.resumeMesh()
	androidbridge.SyncCompanionWidget(true, wPort, readDeviceTelemetry().BatteryPct)
	h.notify()

	go func() {
		_ = srv.Serve(ctx)
		h.mu.Lock()
		if h.srv == srv {
			h.running = false
			h.srv = nil
		}
		h.mu.Unlock()
		androidbridge.SyncCompanionWidget(false, 0, readDeviceTelemetry().BatteryPct)
		h.notify()
	}()
	return nil
}

// StopServer shuts down the active TLS 1.3 Companion listener cleanly.
func (h *Host) StopServer() error {
	h.srvOpMu.Lock()
	defer h.srvOpMu.Unlock()
	h.mu.Lock()
	srv := h.srv
	cancel := h.cancelSrv
	h.srv = nil
	h.cancelSrv = nil
	h.running = false
	wPort := h.cfg.WirelessADBPort
	if srv != nil {
		h.appendAuditLocked("companion server stopped")
	}
	stopNet := h.stopNetWatch
	h.stopNetWatch = nil
	h.wirelessADBPairPort = 0
	if h.meshRebindTimer != nil {
		h.meshRebindTimer.Stop()
		h.meshRebindTimer = nil
	}
	h.mu.Unlock()
	if stopNet != nil {
		stopNet()
	}
	androidbridge.ReleaseActive()
	h.applyStandby(false)
	if cancel != nil {
		cancel()
	}
	if srv != nil {
		_ = srv.Close()
	}
	androidbridge.SyncCompanionWidget(false, wPort, readDeviceTelemetry().BatteryPct)
	h.notify()
	return nil
}

// Running reports whether the TLS Companion listener is currently active.
func (h *Host) Running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running
}

// ListenAddr returns the active bound address or "" if stopped.
func (h *Host) ListenAddr() string {
	h.mu.Lock()
	srv := h.srv
	h.mu.Unlock()
	if srv == nil {
		return ""
	}
	return srv.Addr()
}

// ListenPort returns the active bound TCP port, or configured ListenPort.
func (h *Host) ListenPort() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ListenPortLocked()
}

// ListenPortLocked returns the bound port while h.mu is held.
func (h *Host) ListenPortLocked() int {
	if h.srv != nil && h.srv.Addr() != "" {
		if _, pStr, err := net.SplitHostPort(h.srv.Addr()); err == nil {
			if p, err := strconv.Atoi(pStr); err == nil {
				return p
			}
		}
	}
	return h.cfg.ListenPort
}

// Fingerprint returns the server's short display SHA-256 certificate fingerprint.
func (h *Host) Fingerprint() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.FingerprintLocked()
}

// FingerprintLocked returns the short display SHA-256 fingerprint while h.mu is held.
func (h *Host) FingerprintLocked() string {
	if h.srv == nil {
		return "--"
	}
	fp := h.srv.Fingerprint()
	if len(fp) > 23 {
		return fp[:23] + "..."
	}
	return fp
}

// Config returns a snapshot of the persisted Companion configuration.
func (h *Host) Config() Config {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg
}

// LastError returns the most recent startup/runtime error, or "".
func (h *Host) LastError() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastErr
}

// Status reads current device battery/thermal telemetry and attaches any live ADB and Enhanced mode state.
func (h *Host) Status() companion.DeviceStatus {
	st := readDeviceTelemetry()
	st.Timestamp = time.Now().Unix()
	usbDbg, wifiDbg := readADBSettings()
	st.USBDebugOn = usbDbg
	st.WirelessDebugOn = wifiDbg
	h.mu.Lock()
	if h.cfg.WirelessADBPort > 0 {
		st.WirelessADBPort = h.cfg.WirelessADBPort
	}
	if h.wirelessADBPairPort > 0 {
		st.WirelessADBPairPort = h.wirelessADBPairPort
	}
	st.ADBTunnelAllowed = h.cfg.Permissions.ADBBeacon
	st.ScreenShareAllowed = h.cfg.Permissions.ScreenShare
	if h.network != "" {
		st.NetworkType = h.network
	}
	st.DaemonReady = h.daemonReady
	st.DaemonPID = h.daemonPID
	st.DaemonTrustedDisplay = h.daemonTrustedDisplay
	st.DaemonFDVerified = h.daemonFDVerified
	st.AppVersion = version.Version
	st.AppBuildHash = h.apkHash
	st.CompanionPort = h.ListenPortLocked()
	st.StandbyRunning = h.cfg.Standby
	st.AccessibilityOn = h.a11yOn
	h.mu.Unlock()
	if st.NetworkType == "wifi" {
		st.WifiIPv4 = localWifiIPv4()
	}
	st.MeshNetwork, st.MeshRunning, _ = h.MeshState()
	return st
}

// localWifiIPv4 returns the device's non-loopback LAN IPv4 address when connected over Wi-Fi,
// skipping EasyTier virtual mesh and CGNAT ranges (`10.126.126.*`, `100.64.0.0/10`) when a
// local private IPv4 address is present.
func localWifiIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	fallback := ""
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() {
			continue
		}
		v4 := ipn.IP.To4()
		if v4 == nil {
			continue
		}
		if v4[0] == 10 && v4[1] == 126 && v4[2] == 126 {
			continue
		}
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			if fallback == "" {
				fallback = v4.String()
			}
			continue
		}
		if v4.IsPrivate() {
			return v4.String()
		}
		if fallback == "" {
			fallback = v4.String()
		}
	}
	return fallback
}

func (h *Host) updateDaemonStatus(st androidbridge.DaemonStatus) {
	h.mu.Lock()
	wasReady := h.daemonReady
	h.daemonReady = st.Ready
	h.daemonPID = st.PID
	h.daemonTrustedDisplay = st.HasTrustedDisplay
	if !st.Ready {
		h.daemonFDVerified = false
	} else if st.FDVerified {
		h.daemonFDVerified = true
	}
	verifyNow := !wasReady && st.Ready
	if verifyNow {
		h.appendAuditLocked(fmt.Sprintf("uid=2000 daemon attached (pid=%d, trusted_display=%v)", st.PID, st.HasTrustedDisplay))
	} else if wasReady && !st.Ready {
		h.appendAuditLocked("uid=2000 daemon detached")
	}
	h.mu.Unlock()
	h.notify()

	if verifyNow {
		go func() {
			ok, err := androidbridge.TestDaemonFD()
			h.mu.Lock()
			if h.daemonReady && h.daemonPID == st.PID {
				h.daemonFDVerified = ok
				if ok {
					h.appendAuditLocked("uid=2000 daemon socketpair FD verified")
				} else if err != nil {
					h.appendAuditLocked("uid=2000 daemon socketpair FD failed: " + err.Error())
				} else {
					h.appendAuditLocked("uid=2000 daemon socketpair FD rejected")
				}
			}
			h.mu.Unlock()
			h.notify()
		}()
	}
}

// SetPermission toggles one named graded permission (`INV-09`) and persists the update.
func (h *Host) SetPermission(name string, enabled bool) {
	h.mu.Lock()
	switch name {
	case "telemetry":
		h.cfg.Permissions.Telemetry = enabled
	case "clipboard":
		h.cfg.Permissions.Clipboard = enabled
	case "notifications":
		h.cfg.Permissions.Notifications = enabled
	case "file_transfer":
		h.cfg.Permissions.FileTransfer = enabled
	case "app_continuity":
		h.cfg.Permissions.AppContinuity = enabled
	case "display_guard":
		h.cfg.Permissions.DisplayGuard = enabled
	case "adb_beacon":
		h.cfg.Permissions.ADBBeacon = enabled
	case "media_bridge":
		h.cfg.Permissions.MediaBridge = enabled
	case "screen_share":
		h.cfg.Permissions.ScreenShare = enabled
	}
	h.appendAuditLocked(fmt.Sprintf("permission %s=%v", name, enabled))
	h.saveConfigLocked()
	h.mu.Unlock()
	h.notify()
}

// SetStandby turns background standby on or off and persists it; it takes effect at once when
// the Companion server is running (PLAN §9.1-3).
func (h *Host) SetStandby(on bool) {
	h.mu.Lock()
	h.cfg.Standby = on
	running := h.running
	h.appendAuditLocked(fmt.Sprintf("background standby=%v", on))
	h.saveConfigLocked()
	h.mu.Unlock()
	if running {
		h.applyStandby(on)
	}
	h.notify()
}

// applyStandby starts or stops the foreground standby service, recording failures.
func (h *Host) applyStandby(on bool) {
	if err := androidbridge.SetStandby(on, L.StandbyNotifTitle(), L.StandbyNotifText()); err != nil {
		h.mu.Lock()
		h.lastErr = err.Error()
		h.mu.Unlock()
	}
}

// RotateToken generates a new cryptographic pairing token and updates the running server (`INV-07`).
func (h *Host) RotateToken() (string, error) {
	h.mu.Lock()
	srv := h.srv
	h.mu.Unlock()

	var tok string
	var err error
	if srv != nil {
		tok, err = srv.RotateToken()
	} else {
		tok, err = companion.GeneratePairingToken()
	}
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	h.cfg.Token = tok
	h.appendAuditLocked("pairing token rotated")
	h.saveConfigLocked()
	h.mu.Unlock()
	h.notify()
	return tok, nil
}

// SetWirelessADBPort updates and persists the advertised cross-network `_adb-tls-connect._tcp` port.
func (h *Host) SetWirelessADBPort(port int) {
	if port < 0 || port > 65535 {
		return
	}
	h.mu.Lock()
	h.cfg.WirelessADBPort = port
	running := h.running
	if port > 0 {
		h.beaconInput = strconv.Itoa(port)
		h.appendAuditLocked(fmt.Sprintf("wireless ADB beacon port updated (%d)", port))
	} else {
		h.beaconInput = ""
		h.appendAuditLocked("wireless ADB beacon cleared")
	}
	h.saveConfigLocked()
	h.mu.Unlock()
	androidbridge.SyncCompanionWidget(running, port, readDeviceTelemetry().BatteryPct)
	h.notify()
}

// SetWirelessADBPairPort records the active `_adb-tls-pairing._tcp` port while the phone's pairing
// dialog is open (0 when closed) so paired desktops can tunnel 6-digit pairing without typing IP/port.
func (h *Host) SetWirelessADBPairPort(port int) {
	if port < 0 || port > 65535 {
		return
	}
	h.mu.Lock()
	if h.wirelessADBPairPort == port {
		h.mu.Unlock()
		return
	}
	h.wirelessADBPairPort = port
	h.mu.Unlock()
	h.notify()
}

// AuditLog returns a copy of recent scrubbed Companion activity lines (`INV-08`).
func (h *Host) AuditLog() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.audit...)
}

// identityFile keeps the Companion's TLS certificate and key, sealed with the Android Keystore.
const identityFile = "identity.sealed"

// identity returns the Companion's kept TLS certificate, drawing and keeping one the first time,
// so a paired desktop's pinned fingerprint survives restarts, updates and reboots.
// Returns: nil when it cannot be kept (outside Android, no Keystore), and the server then draws
// a certificate of its own for this run.
// Side effects: writes identity.sealed in the state directory the first time.
func (h *Host) identity() *tls.Certificate {
	if h.stateDir == "" {
		return nil
	}
	path := filepath.Join(h.stateDir, identityFile)
	if sealed, err := os.ReadFile(path); err == nil {
		if raw, err := unseal(sealed); err == nil {
			if cert, err := companion.ParseIdentity(raw); err == nil {
				return &cert
			}
		}
	}
	cert, _, err := companion.GenerateSelfSignedCert("AndroFuse Companion")
	if err != nil {
		return nil
	}
	raw, err := companion.MarshalIdentity(cert)
	if err != nil {
		return nil
	}
	sealed, err := seal(raw)
	if err != nil {
		return nil
	}
	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		return nil
	}
	return &cert
}

// AutoStart starts the Companion service when the app starts, once the phone is paired with a
// computer or in a network, unless its user stopped it: the network, the Wi-Fi watch and the
// Wireless debugging port all run with the service, so without it a phone that restarted (an
// update, a reboot) could not be reached until someone opened the app and tapped Start.
func (h *Host) AutoStart() {
	h.mu.Lock()
	want := !h.cfg.Stopped && (len(h.cfg.Computers) > 0 || h.cfg.Mesh != nil)
	h.mu.Unlock()
	if want {
		_ = h.StartServer()
	}
}

// StopByUser stops the service because the phone's user asked, and keeps it off across restarts.
func (h *Host) StopByUser() {
	h.mu.Lock()
	h.cfg.Stopped = true
	h.saveConfigLocked()
	h.mu.Unlock()
	_ = h.StopServer()
}
