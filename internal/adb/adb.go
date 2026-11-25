// Package adb provides argument-vector ADB orchestration, mDNS/USB transport
// detection, scrubbed device telemetry (INV-08), and read-only capability probing.
package adb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Transport identifies how ADB reaches a device.
type Transport string

const (
	TransportUSB      Transport = "usb"
	TransportTLSWiFi  Transport = "tls_wifi"
	TransportTCP      Transport = "tcp"
	TransportEmulator Transport = "emulator"
)

// Device summarizes one attached ADB target without storing unsanitized
// secrets in display logs.
type Device struct {
	Serial      string    `json:"-"`
	MaskedID    string    `json:"masked_id"`
	DeviceHash  string    `json:"device_hash"`
	State       string    `json:"state"`
	Product     string    `json:"product,omitempty"`
	Model       string    `json:"model,omitempty"`
	DeviceCode  string    `json:"device,omitempty"`
	TransportID string    `json:"transport_id,omitempty"`
	Transport   Transport `json:"transport"`
}

// DeviceProfile describes real Android/One UI capabilities queried from the
// target via `--user 0` safe shell inspection.
type DeviceProfile struct {
	MaskedID         string `json:"masked_id"`
	DeviceHash       string `json:"device_hash"`
	Manufacturer     string `json:"manufacturer"`
	Model            string `json:"model"`
	AndroidRelease   string `json:"android_release"`
	SDKInt           int    `json:"sdk_int"`
	OneUIVersion     string `json:"oneui_version,omitempty"`
	SemVersion       string `json:"sem_version,omitempty"`
	SoC              string `json:"soc,omitempty"`
	HasUHID          bool   `json:"has_uhid"`
	HasDeskLauncher  bool   `json:"has_desk_launcher"`
	WifiDisplayState int    `json:"wifi_display_state"`
	ActiveDisplays   []int  `json:"active_displays"`
	AVCEncoderMax    int    `json:"avc_encoder_max,omitempty"`
	HEVCEncoderMax   int    `json:"hevc_encoder_max,omitempty"`
}

// Client wraps a concrete adb binary path.
type Client struct {
	Path string
}

// FindClient locates adb (PATH first, then %LOCALAPPDATA%\Android\Sdk\platform-tools\adb.exe).
func FindClient() (*Client, error) {
	if p, err := exec.LookPath("adb"); err == nil {
		return &Client{Path: p}, nil
	}
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			p := filepath.Join(local, "Android", "Sdk", "platform-tools", "adb.exe")
			if _, err := os.Stat(p); err == nil {
				return &Client{Path: p}, nil
			}
		}
	}
	return nil, errors.New("adb executable not found")
}

// MaskSerial scrubs a hardware serial or LAN endpoint for logs and diagnostics (INV-08).
func MaskSerial(serial string) string {
	s := strings.TrimSpace(serial)
	if s == "" {
		return "<unknown>"
	}
	if strings.Contains(s, ":") {
		return MaskEndpoint(s)
	}
	if len(s) <= 6 {
		return "****"
	}
	return s[:3] + "****" + s[len(s)-3:]
}

// MaskEndpoint scrubs a host:port endpoint so raw LAN or VPN IPs never leak into
// logs or UI telemetry (INV-08).
func MaskEndpoint(addr string) string {
	s := strings.TrimSpace(addr)
	if s == "" {
		return "<unknown>"
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return "****"
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return fmt.Sprintf("%d.*.*.*:%s", v4[0], port)
		}
		return fmt.Sprintf("[****]:%s", port)
	}
	if len(host) > 4 {
		return host[:2] + "****" + ":" + port
	}
	return "****:" + port
}

// MDNSService describes one discovered Android Wireless Debugging mDNS entry
// (`_adb-tls-connect._tcp` or `_adb-tls-pairing._tcp`).
type MDNSService struct {
	Instance    string `json:"instance"`
	ServiceType string `json:"service_type"`
	Addr        string `json:"-"`
	MaskedAddr  string `json:"masked_addr"`
	CanConnect  bool   `json:"can_connect"`
	CanPair     bool   `json:"can_pair"`
}

// AppEntry describes a user-launchable activity installed on Android user 0.
type AppEntry struct {
	Package    string `json:"package"`
	Activity   string `json:"activity"`
	Label      string `json:"label"`
	IconPath   string `json:"icon_path,omitempty"`
	System     bool   `json:"system,omitempty"`
	Resizeable bool   `json:"resizeable"`
}

// DeviceHash returns a stable short SHA-256 identifier suitable for lease files and filenames.
func DeviceHash(serial string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(serial)))
	return hex.EncodeToString(sum[:8])
}

// Run executes `adb <args...>` without invoking a shell and returns trimmed stdout.
func (c *Client) Run(ctx context.Context, args ...string) (string, error) {
	if c == nil || c.Path == "" {
		return "", errors.New("adb client uninitialized")
	}
	cmd := exec.CommandContext(ctx, c.Path, args...)
	hideConsoleWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errTxt := strings.TrimSpace(stderr.String())
		if errTxt == "" {
			errTxt = err.Error()
		}
		return strings.TrimSpace(stdout.String()), fmt.Errorf("adb %s: %s", strings.Join(args, " "), errTxt)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Devices returns all targets reported by `adb devices -l`.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	out, err := c.Run(ctx, "devices", "-l")
	if err != nil {
		return nil, err
	}
	return ParseDevicesOutput(out), nil
}

// ParseDevicesOutput parses stdout from `adb devices -l`.
func ParseDevicesOutput(raw string) []Device {
	var out []Device
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		serial := fields[0]
		d := Device{
			Serial:     serial,
			MaskedID:   MaskSerial(serial),
			DeviceHash: DeviceHash(serial),
			State:      fields[1],
			Transport:  classifyTransport(serial),
		}
		for _, kv := range fields[2:] {
			k, v, ok := strings.Cut(kv, ":")
			if !ok {
				continue
			}
			switch k {
			case "product":
				d.Product = v
			case "model":
				d.Model = v
			case "device":
				d.DeviceCode = v
			case "transport_id":
				d.TransportID = v
			}
		}
		out = append(out, d)
	}
	return out
}

func classifyTransport(serial string) Transport {
	switch {
	case strings.HasPrefix(serial, "emulator-"):
		return TransportEmulator
	case strings.Contains(serial, "_adb-tls-connect"):
		return TransportTLSWiFi
	case strings.Contains(serial, ":"):
		if _, port, err := net.SplitHostPort(serial); err == nil && port != "5555" {
			// Android 11+ Wireless Debugging advertises random ephemeral ports (>1024),
			// whereas insecure legacy `adb tcpip` defaults to :5555 (INV-07).
			return TransportTLSWiFi
		}
		return TransportTCP
	default:
		return TransportUSB
	}
}

// MDNSServices queries `adb mdns services` and returns discovered TLS wireless
// connect and pairing services without exposing raw IPs in MaskedAddr (INV-08).
func (c *Client) MDNSServices(ctx context.Context) ([]MDNSService, error) {
	out, err := c.Run(ctx, "mdns", "services")
	if err != nil {
		return nil, err
	}
	return ParseMDNSServices(out), nil
}

// ParseMDNSServices parses stdout from `adb mdns services`.
func ParseMDNSServices(raw string) []MDNSService {
	var out []MDNSService
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of ") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		svcType := fields[1]
		addr := fields[len(fields)-1]
		canConnect := strings.Contains(svcType, "_adb-tls-connect")
		canPair := strings.Contains(svcType, "_adb-tls-pairing")
		if !canConnect && !canPair {
			continue
		}
		out = append(out, MDNSService{
			Instance:    MaskSerial(fields[0]),
			ServiceType: svcType,
			Addr:        addr,
			MaskedAddr:  MaskEndpoint(addr),
			CanConnect:  canConnect,
			CanPair:     canPair,
		})
	}
	return out
}

// ConnectTLS connects to a TLS Wireless Debugging `<host>:<port>` endpoint (`INV-07`).
//
// Notes: Refuses default insecure `adb tcpip` port 5555 unless allowPlainTCP is true.
func (c *Client) ConnectTLS(ctx context.Context, addr string, allowPlainTCP bool) (string, error) {
	addr = strings.TrimSpace(addr)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid host:port %q", addr)
	}
	if !allowPlainTCP && port == "5555" {
		return "", errors.New("port 5555 belongs to plaintext adb tcpip and is disabled by default (INV-07)")
	}
	out, err := c.Run(ctx, "connect", addr)
	if err != nil {
		return out, err
	}
	lower := strings.ToLower(out)
	if strings.Contains(lower, "failed") || strings.Contains(lower, "cannot connect") || strings.Contains(lower, "refused") {
		return out, fmt.Errorf("adb connect %s: %s", MaskEndpoint(addr), out)
	}
	return out, nil
}

// PairTLS pairs with an Android 11+ Wireless Debugging endpoint using a 6-digit code.
func (c *Client) PairTLS(ctx context.Context, addr, code string) (string, error) {
	addr = strings.TrimSpace(addr)
	code = strings.TrimSpace(code)
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return "", fmt.Errorf("invalid pairing host:port %q", addr)
	}
	if code == "" {
		return "", errors.New("pairing code is required")
	}
	out, err := c.Run(ctx, "pair", addr, code)
	if err != nil {
		return out, err
	}
	if !strings.Contains(strings.ToLower(out), "successfully paired") {
		return out, fmt.Errorf("adb pair %s: %s", MaskEndpoint(addr), out)
	}
	return out, nil
}

// Disconnect detaches a connected wireless ADB endpoint.
func (c *Client) Disconnect(ctx context.Context, addr string) error {
	_, err := c.Run(ctx, "disconnect", strings.TrimSpace(addr))
	return err
}

// WakeScreen ensures the Android screen is awake before activating Samsung DeX loopback,
// avoiding One UI 9 cold-screen attach aborts.
func (c *Client) WakeScreen(ctx context.Context, serial string) error {
	out, err := c.Shell(ctx, serial, "dumpsys power | grep -E 'mWakefulness=|Display Power:' | head -n 4")
	if err != nil {
		return err
	}
	if strings.Contains(out, "Awake") && strings.Contains(out, "state=ON") {
		return nil
	}
	if _, err := c.Shell(ctx, serial, "input keyevent 224"); err != nil {
		return err
	}
	time.Sleep(350 * time.Millisecond)
	return nil
}

// ListLaunchableApps enumerates launcher activities installed for Android user 0,
// sorted deterministically by friendly label for the Independent App Window picker.
func (c *Client) ListLaunchableApps(ctx context.Context, serial string) ([]AppEntry, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := c.Shell(queryCtx, serial, "cmd package query-activities --brief -a android.intent.action.MAIN -c android.intent.category.LAUNCHER --user 0 2>/dev/null")
	if err != nil || strings.TrimSpace(out) == "" {
		// Fallback to third-party packages if query-activities is unavailable.
		out, err = c.Shell(queryCtx, serial, "pm list packages -3 --user 0")
		if err != nil {
			return nil, err
		}
	}
	return ParseLaunchableApps(out), nil
}

// ParseLaunchableApps parses `cmd package query-activities --brief` or `pm list packages` output.
func ParseLaunchableApps(raw string) []AppEntry {
	seen := map[string]bool{}
	var apps []AppEntry
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Activity #") || strings.HasPrefix(line, "priority=") {
			continue
		}
		var pkg, act string
		if rest, ok := strings.CutPrefix(line, "package:"); ok {
			pkg = strings.TrimSpace(rest)
		} else if p, a, ok := strings.Cut(line, "/"); ok && !strings.ContainsAny(p, " \t") {
			pkg = strings.TrimSpace(p)
			act = strings.TrimSpace(a)
		} else {
			continue
		}
		if pkg == "" || IsNoisyAppPackage(pkg) || seen[pkg] {
			continue
		}
		seen[pkg] = true
		apps = append(apps, AppEntry{
			Package:    pkg,
			Activity:   act,
			Label:      friendlyAppLabel(pkg),
			Resizeable: true,
		})
	}
	sort.Slice(apps, func(i, j int) bool {
		li := strings.ToLower(apps[i].Label)
		lj := strings.ToLower(apps[j].Label)
		if li != lj {
			return li < lj
		}
		return apps[i].Package < apps[j].Package
	})
	return apps
}

// IsNoisyAppPackage reports whether pkg is a WebAPK hash wrapper, test stub,
// or internal shell component that should be excluded from the App Launcher Grid.
func IsNoisyAppPackage(pkg string) bool {
	if pkg == "" || pkg == "com.android.shell" ||
		strings.HasPrefix(pkg, "org.chromium.webapk.") ||
		strings.HasPrefix(pkg, "com.google.android.apps.chrome.webapps.") ||
		strings.HasSuffix(pkg, ".test") {
		return true
	}
	for _, seg := range strings.Split(pkg, ".") {
		base, ver, hasVer := strings.Cut(seg, "_v")
		if hasVer && ver != "" {
			allDigits := true
			for i := 0; i < len(ver); i++ {
				if ver[i] < '0' || ver[i] > '9' {
					allDigits = false
					break
				}
			}
			if !allDigits {
				base = seg
			}
		} else {
			base = seg
		}
		if len(base) >= 12 && isHexASCII(base) {
			return true
		}
	}
	return false
}

func isHexASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return len(s) > 0
}

func friendlyAppLabel(pkg string) string {
	parts := strings.Split(pkg, ".")
	last := parts[len(parts)-1]
	if last == "android" || last == "app" || last == "mobile" {
		if len(parts) >= 2 {
			last = parts[len(parts)-2]
		}
	}
	last = strings.TrimSuffix(last, "package")
	if last == "" {
		return pkg
	}
	return strings.ToUpper(last[:1]) + last[1:]
}

// Shell executes `adb -s <serial> shell <cmd>` cleanly without wrapping host quotes.
func (c *Client) Shell(ctx context.Context, serial, shellCmd string) (string, error) {
	return c.Run(ctx, "-s", serial, "shell", shellCmd)
}

// ProbeDevice inspects device manufacturer, OS/One UI version, /dev/uhid,
// active display IDs, and hardware video encoder concurrency limits.
func (c *Client) ProbeDevice(ctx context.Context, serial string) (DeviceProfile, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	script := strings.Join([]string{
		`echo "MFR=$(getprop ro.product.manufacturer)"`,
		`echo "MOD=$(getprop ro.product.model)"`,
		`echo "REL=$(getprop ro.build.version.release)"`,
		`echo "SDK=$(getprop ro.build.version.sdk)"`,
		`echo "ONEUI=$(getprop ro.build.version.oneui)"`,
		`echo "SEM=$(getprop ro.build.version.sem)"`,
		`echo "SOC=$(getprop ro.soc.model)"`,
		`if [ -c /dev/uhid ]; then echo "UHID=1"; else echo "UHID=0"; fi`,
		`if pm list packages --user 0 com.sec.android.app.desktoplauncher 2>/dev/null | grep -q desktoplauncher; then echo "DESKPKG=1"; else echo "DESKPKG=0"; fi`,
		`dumpsys display | grep -E "mDisplayId=|mCurrentStatus=WifiDisplayStatus" | head -n 40`,
		`echo "---CODECS---"`,
		`awk '/c2\.qti\.avc\.encoder|c2\.exynos\.avc\.encoder|OMX\..*avc\.encoder/{in_avc=1} /c2\.qti\.hevc\.encoder|c2\.exynos\.hevc\.encoder/{in_hevc=1} /concurrent-instances/{if(in_avc){print "AVC="$0; in_avc=0} if(in_hevc){print "HEVC="$0; in_hevc=0}} /<\/MediaCodec>/{in_avc=0; in_hevc=0}' /vendor/etc/media_codecs*.xml 2>/dev/null | head -n 10`,
	}, "; ")

	raw, err := c.Shell(probeCtx, serial, script)
	if err != nil {
		return DeviceProfile{}, err
	}
	return ParseDeviceProbe(serial, raw), nil
}

// ParseDeviceProbe parses the structured output emitted by ProbeDevice.
func ParseDeviceProbe(serial, raw string) DeviceProfile {
	prof := DeviceProfile{
		MaskedID:         MaskSerial(serial),
		DeviceHash:       DeviceHash(serial),
		WifiDisplayState: -1,
	}
	seenDisp := map[int]bool{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "MFR="):
			prof.Manufacturer = strings.TrimPrefix(line, "MFR=")
		case strings.HasPrefix(line, "MOD="):
			prof.Model = strings.TrimPrefix(line, "MOD=")
		case strings.HasPrefix(line, "REL="):
			prof.AndroidRelease = strings.TrimPrefix(line, "REL=")
		case strings.HasPrefix(line, "SDK="):
			prof.SDKInt, _ = strconv.Atoi(strings.TrimPrefix(line, "SDK="))
		case strings.HasPrefix(line, "ONEUI="):
			prof.OneUIVersion = strings.TrimPrefix(line, "ONEUI=")
		case strings.HasPrefix(line, "SEM="):
			prof.SemVersion = strings.TrimPrefix(line, "SEM=")
		case strings.HasPrefix(line, "SOC="):
			prof.SoC = strings.TrimPrefix(line, "SOC=")
		case line == "UHID=1":
			prof.HasUHID = true
		case line == "DESKPKG=1":
			prof.HasDeskLauncher = true
		case strings.HasPrefix(line, "mDisplayId="):
			idStr := strings.TrimPrefix(line, "mDisplayId=")
			if id, err := strconv.Atoi(strings.TrimSpace(idStr)); err == nil && !seenDisp[id] {
				seenDisp[id] = true
				prof.ActiveDisplays = append(prof.ActiveDisplays, id)
			}
		case strings.Contains(line, "activeDisplayState="):
			if _, after, ok := strings.Cut(line, "activeDisplayState="); ok {
				num, _, _ := strings.Cut(after, ",")
				prof.WifiDisplayState, _ = strconv.Atoi(strings.TrimSpace(num))
			}
		case strings.HasPrefix(line, "AVC="):
			if prof.AVCEncoderMax == 0 {
				prof.AVCEncoderMax = parseCodecMax(line)
			}
		case strings.HasPrefix(line, "HEVC="):
			if prof.HEVCEncoderMax == 0 {
				prof.HEVCEncoderMax = parseCodecMax(line)
			}
		}
	}
	return prof
}

func parseCodecMax(line string) int {
	if _, after, ok := strings.Cut(line, `max="`); ok {
		val, _, _ := strings.Cut(after, `"`)
		n, _ := strconv.Atoi(val)
		return n
	}
	return 0
}

// ForwardTCP sets up `adb -s <serial> forward tcp:<localPort> localabstract:<socketName>`.
func (c *Client) ForwardTCP(ctx context.Context, serial string, localPort int, socketName string) (int, error) {
	spec := fmt.Sprintf("tcp:%d", localPort)
	target := "localabstract:" + socketName
	out, err := c.Run(ctx, "-s", serial, "forward", spec, target)
	if err != nil {
		return 0, err
	}
	if localPort == 0 {
		p, pErr := strconv.Atoi(strings.TrimSpace(out))
		if pErr != nil {
			return 0, fmt.Errorf("parse allocated adb forward port %q: %w", out, pErr)
		}
		return p, nil
	}
	return localPort, nil
}

// RemoveForward deletes a local TCP forward previously created by ForwardTCP.
func (c *Client) RemoveForward(ctx context.Context, serial string, localPort int) error {
	if localPort <= 0 {
		return nil
	}
	_, err := c.Run(ctx, "-s", serial, "forward", "--remove", fmt.Sprintf("tcp:%d", localPort))
	return err
}

// PushFile uploads localPath to remotePath on the device.
func (c *Client) PushFile(ctx context.Context, serial, localPath, remotePath string) error {
	_, err := c.Run(ctx, "-s", serial, "push", localPath, remotePath)
	return err
}
