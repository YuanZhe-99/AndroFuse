// Package companion defines the encrypted TLS 1.3 NDJSON protocol shared between
// the Windows desktop AndroFuse client and the optional Android Companion APK (`INV-07`, `INV-09`, `INV-11`).
package companion

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	// ProtocolVersion is the NDJSON Companion wire schema version.
	ProtocolVersion = 1
	// LocalSocket is the abstract local socket the Android Companion also listens on, so the
	// desktop reaches it with `adb forward tcp:0 localabstract:<LocalSocket>` whatever random TCP
	// port (PickPort) it uses for LAN and mesh connections.
	LocalSocket = "androfuse-companion"
	// MaxFilePushBytes caps a single inlined FilePushRequest payload at 16 MiB.
	MaxFilePushBytes = 16 << 20
	// MaxMediaFrameBytes caps a single PC->Android camera JPEG or mic PCM frame at 2 MiB.
	MaxMediaFrameBytes = 2 << 20
	// PairWait is how long the phone waits for its user to answer an adb pairing request.
	PairWait = 60 * time.Second
)

// Permissions controls graded, opt-in Companion capabilities enforced server-side (`INV-09`).
type Permissions struct {
	Telemetry     bool `json:"telemetry"`
	Clipboard     bool `json:"clipboard"`
	Notifications bool `json:"notifications"`
	FileTransfer  bool `json:"file_transfer"`
	AppContinuity bool `json:"app_continuity"`
	DisplayGuard  bool `json:"display_guard"`
	ADBBeacon     bool `json:"adb_beacon"`
	MediaBridge   bool `json:"media_bridge"`
	// ScreenShare lets a paired computer see this screen when Wireless debugging cannot run
	// (mobile data); Android still asks for consent each time.
	ScreenShare bool `json:"screen_share"`
}

// DefaultPermissions returns safe defaults: telemetry, TLS ADB tunnel, and MediaProjection screen
// viewing are enabled so first-time setup works smoothly while sensitive clipboard, notification,
// file, continuity, and mic/camera permissions remain opt-in (`INV-07`, `INV-09`).
func DefaultPermissions() Permissions {
	return Permissions{
		Telemetry:   true,
		ADBBeacon:   true,
		ScreenShare: true,
	}
}

// Envelope is one newline-delimited JSON RPC frame over the TLS 1.3 Companion socket.
type Envelope struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// AuthRequest authenticates a desktop client against the Companion server using the pairing token.
type AuthRequest struct {
	Token      string `json:"token"`
	ClientName string `json:"client_name,omitempty"`
	// ComputerID is the computer's own stable id (its mesh hostname's id), by which the phone
	// keeps one pairing per computer.
	ComputerID      string `json:"computer_id,omitempty"`
	ProtocolVersion int    `json:"protocol_version"`
}

// AuthResponse summarizes device identity, certificate fingerprint, and active permissions.
type AuthResponse struct {
	OK              bool        `json:"ok"`
	DeviceModel     string      `json:"device_model"`
	DeviceName      string      `json:"device_name,omitempty"`
	Manufacturer    string      `json:"manufacturer,omitempty"`
	Brand           string      `json:"brand,omitempty"`
	AndroidRelease  string      `json:"android_release"`
	SDKInt          int         `json:"sdk_int"`
	CertFingerprint string      `json:"cert_fingerprint"`
	Permissions     Permissions `json:"permissions"`
	// MeshHost is the EasyTier hostname the phone joins networks under (mesh.Hostname), so the
	// desktop finds it in a network even after it renamed itself.
	MeshHost string `json:"mesh_host,omitempty"`
	Error    string `json:"error,omitempty"`
}

// DeviceStatus reports battery, thermal, foreground app, and live ADB/Enhanced-mode availability.
type DeviceStatus struct {
	BatteryPct        int     `json:"battery_pct"`
	Charging          bool    `json:"charging"`
	TempC             float64 `json:"temp_c"`
	ThermalWarning    bool    `json:"thermal_warning"`
	NetworkType       string  `json:"network_type"`
	WifiIPv4          string  `json:"wifi_ipv4,omitempty"`
	ForegroundPackage string  `json:"foreground_package,omitempty"`
	WirelessADBPort   int     `json:"wireless_adb_port,omitempty"`
	// WirelessADBPairPort is this phone's live `_adb-tls-pairing._tcp` port while Android's
	// Wireless debugging pairing dialog is open, 0 otherwise.
	WirelessADBPairPort int `json:"wireless_adb_pair_port,omitempty"`
	// USBDebugOn and WirelessDebugOn mirror Android global developer settings so the desktop
	// knows whether ADB is enabled before prompting the user.
	USBDebugOn         bool `json:"usb_debug_on,omitempty"`
	WirelessDebugOn    bool `json:"wireless_debug_on,omitempty"`
	ADBTunnelAllowed   bool `json:"adb_tunnel_allowed,omitempty"`
	ScreenShareAllowed bool `json:"screen_share_allowed,omitempty"`
	// MeshNetwork is the EasyTier network name the phone joined ("" for none) and MeshRunning
	// whether it runs, so a desktop sees when the phone is not in its network (never the secret).
	MeshNetwork string `json:"mesh_network,omitempty"`
	MeshRunning bool   `json:"mesh_running,omitempty"`
	// Daemon fields report the persistent UID 2000 daemon (`PLAN` §9.2) attached to the Companion.
	DaemonReady          bool   `json:"daemon_ready,omitempty"`
	DaemonPID            int    `json:"daemon_pid,omitempty"`
	DaemonTrustedDisplay bool   `json:"daemon_trusted_display,omitempty"`
	DaemonFDVerified     bool   `json:"daemon_fd_verified,omitempty"`
	AppVersion           string `json:"app_version,omitempty"`
	AppBuildHash         string `json:"app_build_hash,omitempty"`
	CompanionPort        int    `json:"companion_port,omitempty"`
	StandbyRunning       bool   `json:"standby_running,omitempty"`
	AccessibilityOn      bool   `json:"accessibility_on,omitempty"`
	Timestamp            int64  `json:"timestamp"`
}

// DaemonAppEntry describes one launchable application reported by Companion Enhanced mode.
type DaemonAppEntry struct {
	Package    string `json:"package"`
	Label      string `json:"label"`
	System     bool   `json:"system,omitempty"`
	Resizeable bool   `json:"resizeable"`
	IconB64    string `json:"icon_b64,omitempty"`
}

// AppListRequest optionally names packages whose icons the desktop already has cached on disk,
// so Companion Enhanced mode streams only missing icons (`apps.list`).
type AppListRequest struct {
	KnownIcons []string `json:"known_icons,omitempty"`
}

// AppListResponse returns the launchable app list queried over Companion (`apps.list`).
type AppListResponse struct {
	Apps []DaemonAppEntry `json:"apps"`
}

// TunnelRequest asks Companion to bridge Wireless debugging; defined alongside DaemonStreamRequest (`PLAN` §9.2).
// DaemonStreamRequest opens a full scrcpy-server video+control stream through the attached UID 2000 daemon (`PLAN` §9.2 Phase 2).
type DaemonStreamRequest struct {
	MaxSize               int    `json:"max_size,omitempty"`
	BitRate               int    `json:"bit_rate,omitempty"`
	MaxFPS                int    `json:"max_fps,omitempty"`
	Codec                 string `json:"codec,omitempty"` // "h264" | "h265"
	NewDisplay            string `json:"new_display,omitempty"`
	HideSystemDecorations bool   `json:"hide_system_decorations,omitempty"`
	FlexDisplay           bool   `json:"flex_display,omitempty"`
	DisplayID             int    `json:"display_id,omitempty"`
	StartApp              string `json:"start_app,omitempty"`
	Control               bool   `json:"control,omitempty"`
}

// DaemonStreamResponse returns the session ID whose control channel can be attached via `daemon.ctrl`.
type DaemonStreamResponse struct {
	SessionID string `json:"session_id"`
}

// DaemonCtrlRequest attaches the secondary control TLS stream to a pending daemon session.
type DaemonCtrlRequest struct {
	SessionID string `json:"session_id"`
}

// NotificationEvent represents one synced Android notification.
type NotificationEvent struct {
	ID        string `json:"id"`
	Package   string `json:"package"`
	AppLabel  string `json:"app_label"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
	CanReply  bool   `json:"can_reply"`
}

// NotificationReply delivers an inline notification reply from desktop back to Android.
type NotificationReply struct {
	ID        string `json:"id"`
	ReplyText string `json:"reply_text"`
}

// ClipboardMessage carries bidirectional clipboard text.
type ClipboardMessage struct {
	Seq    uint64 `json:"seq"`
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
}

// AppContinuityRequest asks Companion to report/hand off foreground apps between phone and PC.
type AppContinuityRequest struct {
	Action  string `json:"action"` // "handoff_to_pc" | "return_to_phone"
	Package string `json:"package,omitempty"`
}

// AppContinuityResponse acknowledges a continuity action and returns the resolved package.
type AppContinuityResponse struct {
	OK      bool   `json:"ok"`
	Package string `json:"package,omitempty"`
	Message string `json:"message,omitempty"`
}

// FilePushRequest transfers one file payload alongside its SHA-256 integrity digest.
type FilePushRequest struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	DataBase64 string `json:"data_base64"`
}

// FilePushResponse confirms sanitized file persistence and checksum verification on Android.
type FilePushResponse struct {
	SavedPath    string `json:"saved_path"`
	SHA256       string `json:"sha256"`
	BytesWritten int64  `json:"bytes_written"`
}

// DisplayGuardRequest invokes virtual display cleanup (`INV-03`).
type DisplayGuardRequest struct {
	Action string `json:"action"` // "cleanup_orphans"
}

// DisplayGuardResponse reports how many stale virtual/overlay display entries were cleared.
type DisplayGuardResponse struct {
	CleanedCount int    `json:"cleaned_count"`
	Message      string `json:"message,omitempty"`
}

// MediaFrameRequest carries a bounded PC-to-Android microphone PCM or webcam JPEG frame (`INV-09`).
type MediaFrameRequest struct {
	Kind       string `json:"kind"` // "mic_pcm" | "camera_jpeg"
	SampleRate int    `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DataBase64 string `json:"data_base64"`
	Timestamp  int64  `json:"timestamp"`
}

// MediaFrameResponse acknowledges one accepted PC-to-Android media frame.
type MediaFrameResponse struct {
	OK            bool   `json:"ok"`
	ReceivedBytes int    `json:"received_bytes"`
	Message       string `json:"message,omitempty"`
}

// PairRequest asks the phone's user to pair with a desktop reached over adb (`adb forward`) or
// an EasyTier mesh loopback forward.
// Notes: accepted only as the first frame of a local socket or loopback connection, so LAN peers
// cannot raise prompts; when UseSAS is true, Code is bound to the TLS 1.3 ExportKeyingMaterial
// SAS (`INV-07`, `INV-09`).
type PairRequest struct {
	ClientName string `json:"client_name"`
	Code       string `json:"code"`
	ComputerID string `json:"computer_id,omitempty"`
	UseSAS     bool   `json:"use_sas,omitempty"`
}

// PairResponse carries the pairing token, identity metadata, and optional initial status after the
// phone's user allowed the request.
type PairResponse struct {
	Token           string `json:"token"`
	CertFingerprint string `json:"cert_fingerprint"`
	// Port is the Companion's random TCP port and Model the name it joins the mesh under, so the
	// desktop can reach it again over LAN or the mesh without adb; MeshHost is its EasyTier
	// hostname (mesh.Hostname).
	Port           int           `json:"port,omitempty"`
	Model          string        `json:"model,omitempty"`
	DeviceName     string        `json:"device_name,omitempty"`
	Manufacturer   string        `json:"manufacturer,omitempty"`
	Brand          string        `json:"brand,omitempty"`
	AndroidRelease string        `json:"android_release,omitempty"`
	SDKInt         int           `json:"sdk_int,omitempty"`
	MeshHost       string        `json:"mesh_host,omitempty"`
	Status         *DeviceStatus `json:"status,omitempty"`
}

// MeshJoinRequest hands the desktop's EasyTier network to the phone so it joins the same mesh.
// Notes: the desktop sends it only after its user chose to (first-pairing checkbox, `INV-07`);
// the secret travels inside the authenticated TLS 1.3 channel and is never logged (`INV-08`).
type MeshJoinRequest struct {
	NetworkName   string   `json:"network_name"`
	NetworkSecret string   `json:"network_secret"`
	Peers         []string `json:"peers,omitempty"`
}

// MeshJoinResponse reports whether the phone's embedded mesh is now running.
type MeshJoinResponse struct {
	Running bool   `json:"running"`
	Message string `json:"message,omitempty"`
}

// SanitizeFileName enforces a single clean basename without traversal (`..`, separators, or NUL).
func SanitizeFileName(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') {
		return "", errors.New("invalid empty or NUL file name")
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, "/\\") {
		return "", errors.New("path traversal or directory separator rejected")
	}
	base := path.Base(strings.TrimSpace(name))
	base = filepath.Base(base)
	if base == "" || base == "." || base == ".." {
		return "", errors.New("invalid file name")
	}
	return base, nil
}

const tokenAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePairingToken generates a cryptographically random 32-character (160-bit) pairing token (`INV-07`).
func GeneratePairingToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, v := range raw {
		b.WriteByte(tokenAlphabet[int(v)%len(tokenAlphabet)])
	}
	return b.String(), nil
}

// SanitizeClientName normalizes a pairing requester's display name for safe notification and UI
// rendering, stripping control characters, collapsing whitespace, and bounding length to 64 runes.
func SanitizeClientName(s string) string {
	var clean strings.Builder
	clean.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			clean.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			continue
		default:
			clean.WriteRune(r)
		}
	}
	collapsed := strings.Join(strings.Fields(clean.String()), " ")
	if collapsed == "" {
		return "Computer"
	}
	runes := []rune(collapsed)
	if len(runes) > 64 {
		return string(runes[:64])
	}
	return collapsed
}

// SanitizeComputerID validates and bounds a client's persistent ComputerID identifier.
func SanitizeComputerID(s string) string {
	s = strings.TrimSpace(s)
	if len(s) == 0 || len(s) > 64 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' || c == ':') {
			return ""
		}
	}
	return s
}

// ValidPairCode reports whether code is exactly 6 ASCII decimal digits.
func ValidPairCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for i := 0; i < 6; i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}
