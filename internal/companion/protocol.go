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

// DefaultPermissions returns conservative defaults (telemetry enabled; sensitive hooks opt-in).
func DefaultPermissions() Permissions {
	return Permissions{
		Telemetry: true,
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
	Token           string `json:"token"`
	ClientName      string `json:"client_name,omitempty"`
	ProtocolVersion int    `json:"protocol_version"`
}

// AuthResponse summarizes device identity, certificate fingerprint, and active permissions.
type AuthResponse struct {
	OK              bool        `json:"ok"`
	DeviceModel     string      `json:"device_model"`
	AndroidRelease  string      `json:"android_release"`
	SDKInt          int         `json:"sdk_int"`
	CertFingerprint string      `json:"cert_fingerprint"`
	Permissions     Permissions `json:"permissions"`
	// MeshHost is the EasyTier hostname the phone joins networks under (mesh.Hostname), so the
	// desktop finds it in a network even after it renamed itself.
	MeshHost string `json:"mesh_host,omitempty"`
	Error    string `json:"error,omitempty"`
}

// DeviceStatus reports battery, thermal, foreground app, and cross-network ADB beacon state.
type DeviceStatus struct {
	BatteryPct        int     `json:"battery_pct"`
	Charging          bool    `json:"charging"`
	TempC             float64 `json:"temp_c"`
	ThermalWarning    bool    `json:"thermal_warning"`
	NetworkType       string  `json:"network_type"`
	ForegroundPackage string  `json:"foreground_package,omitempty"`
	WirelessADBPort   int     `json:"wireless_adb_port,omitempty"`
	Timestamp         int64   `json:"timestamp"`
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

// PairRequest asks the phone's user to pair with a desktop reached over adb (`adb forward`).
// Notes: accepted only as the first frame of a loopback connection, so LAN peers cannot raise
// prompts; the phone shows ClientName and Code and the user compares the code with the one the
// desktop shows (`INV-09`).
type PairRequest struct {
	ClientName string `json:"client_name"`
	Code       string `json:"code"`
}

// PairResponse carries the pairing token after the phone's user allowed the request.
type PairResponse struct {
	Token           string `json:"token"`
	CertFingerprint string `json:"cert_fingerprint"`
	// Port is the Companion's random TCP port and Model the name it joins the mesh under, so the
	// desktop can reach it again over LAN or the mesh without adb; MeshHost is its EasyTier
	// hostname (mesh.Hostname).
	Port     int    `json:"port,omitempty"`
	Model    string `json:"model,omitempty"`
	MeshHost string `json:"mesh_host,omitempty"`
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

// GeneratePairingToken generates a cryptographically random 8-character pairing token (`INV-07`).
func GeneratePairingToken() (string, error) {
	var raw [8]byte
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
