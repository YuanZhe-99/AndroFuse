// Package dex orchestrates dual-channel Samsung DeX (ADB SemWifiDisplayConfig
// loopback via androfuse-dex-helper.jar, plus Miracast fallback probes) and
// enforces honest DeX-vs-virtual-display classification (INV-06).
package dex

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/lease"
	"github.com/YuanZhe-99/AndroFuse/internal/scrcpy"

	"github.com/YuanZhe-99/AndroFuse/internal/sysproc"
)

const (
	// RemoteHelperPath is the copy of androfuse-dex-helper.jar the persistent UID 2000 daemon runs
	// from and pins by SHA-256 at start (Daemon.java DEX_HELPER_PATH); only StartDaemon writes it.
	RemoteHelperPath = "/data/local/tmp/androfuse-dex-helper.jar"
	// RemoteToolPath is the separate copy one-shot ADB helpers (probe, apps, serve, attach,
	// recover, appguard) push and run, so they never replace the file under a running daemon.
	RemoteToolPath = "/data/local/tmp/androfuse-dex-tool.jar"
)

// Backend identifies the active DeX or virtual-desktop transport mechanism (INV-05, INV-06).
type Backend string

const (
	BackendADBLoopback     Backend = "adb_native_dex_loopback"
	BackendMiracastWFD     Backend = "miracast_wireless_dex"
	BackendVirtualFallback Backend = "virtual_display_fallback"
)

// ProbeReport captures reflected Samsung DeX capabilities reported by `Main probe`.
type ProbeReport struct {
	Manufacturer             string            `json:"manufacturer"`
	Model                    string            `json:"model"`
	SDK                      int               `json:"sdk"`
	OneUI                    string            `json:"oneui"`
	Sem                      string            `json:"sem"`
	HasSemBuilder            bool              `json:"has_sem_builder"`
	HasSetAp                 bool              `json:"has_set_ap"`
	HasSetMode               bool              `json:"has_set_mode"`
	HasConnectWithConfig     bool              `json:"has_connect_with_config"`
	HasDisconnectWifiDisplay bool              `json:"has_disconnect_wifi_display"`
	ProbeError               string            `json:"probe_error"`
	Displays                 []DisplaySnapshot `json:"displays"`
}

// DisplaySnapshot mirrors a DisplayInfo snapshot from Android DisplayManagerGlobal.
type DisplaySnapshot struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	DPI    int    `json:"dpi"`
	Type   int    `json:"type"`
	Flags  int    `json:"flags"`
}

// TruthVerdict records whether a display provides genuine Samsung DeX vs fallback (INV-06).
type TruthVerdict struct {
	Backend      Backend `json:"backend"`
	IsGenuineDeX bool    `json:"is_genuine_dex"`
	Label        string  `json:"label"`
	Reason       string  `json:"reason"`
}

// RoutePreference records the user's explicit DeX vs fallback routing choice (`INV-06`).
type RoutePreference string

const (
	RouteAuto            RoutePreference = "auto"
	RouteNativeDeX       RoutePreference = "native_dex"
	RouteVirtualFallback RoutePreference = "virtual_fallback"
)

// RoutePlan describes the selected DeX/desktop transport backend, capability verdict,
// and user-facing reason for the current device (`INV-06`).
type RoutePlan struct {
	Preference      RoutePreference `json:"preference"`
	Backend         Backend         `json:"backend"`
	IsGenuineDeX    bool            `json:"is_genuine_dex"`
	CanUseNativeDeX bool            `json:"can_use_native_dex"`
	OneUIDisplay    string          `json:"oneui_display,omitempty"`
	Label           string          `json:"label"`
	Reason          string          `json:"reason"`
}

// ReadyInfo captures live display and control-socket metadata emitted by `Main serve`.
type ReadyInfo struct {
	DisplayID int
	Width     int
	Height    int
	DPI       int
	Type      int
	Flags     int
	RemotePID int
	CtrlPort  int
}

// LoopbackSession supervises a running `androfuse-dex-helper.jar serve` process on the device.
type LoopbackSession struct {
	sessionID string
	serial    string
	token     string
	graceSec  int
	adb       *adb.Client
	leases    *lease.Store
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	ready     ReadyInfo

	mu      sync.Mutex
	closed  bool
	doneCh  chan struct{}
	lineCh  chan string
	stopped bool
}

// FindHelperJar locates androfuse-dex-helper.jar: ANDROFUSE_DEX_HELPER, the installed package's
// `bin/`, next to the binary, or the repo's `build/`.
func FindHelperJar() (string, error) {
	if env := strings.TrimSpace(os.Getenv("ANDROFUSE_DEX_HELPER")); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		// Installed packages carry it in the signed bin/ bundle; dev builds next to the binary.
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "bin", "androfuse-dex-helper.jar"), filepath.Join(dir, "androfuse-dex-helper.jar"))
	}
	if wd, err := os.Getwd(); err == nil {
		cur := wd
		for range 6 {
			candidates = append(candidates, filepath.Join(cur, "build", "androfuse-dex-helper.jar"))
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("androfuse-dex-helper.jar not found (run `go run ./tools/builddex`)")
}

// EvaluateTruth determines honestly whether an active display represents full
// Samsung DeX or a virtual-display fallback (INV-06).
//
// Notes: One UI 9+ (SDK 37+) integrates DeX into SystemUI/Launcher without
// `com.sec.android.app.desktoplauncher`, so we validate Samsung OEM identity,
// SemWifiDisplay activation, and display attributes rather than hard-requiring
// the legacy package name.
func EvaluateTruth(prof adb.DeviceProfile, probe ProbeReport, activeDisplayName string, backend Backend) TruthVerdict {
	if !strings.EqualFold(prof.Manufacturer, "samsung") {
		return TruthVerdict{
			Backend:      BackendVirtualFallback,
			IsGenuineDeX: false,
			Label:        "Virtual Desktop Fallback (Non-Samsung)",
			Reason:       fmt.Sprintf("manufacturer %q does not support Samsung DeX", prof.Manufacturer),
		}
	}
	if backend == BackendVirtualFallback {
		return TruthVerdict{
			Backend:      BackendVirtualFallback,
			IsGenuineDeX: false,
			Label:        "Virtual Display Compatibility Mode (Not Full DeX)",
			Reason:       "plain scrcpy --new-display does not activate Samsung DeX taskbar or Recents",
		}
	}
	if backend == BackendADBLoopback {
		if !probe.HasSemBuilder || !probe.HasConnectWithConfig {
			return TruthVerdict{
				Backend:      BackendVirtualFallback,
				IsGenuineDeX: false,
				Label:        "Virtual Display Fallback",
				Reason:       "SemWifiDisplayConfig API unavailable on firmware",
			}
		}
		if strings.EqualFold(activeDisplayName, "AndroFuseDeX") || prof.WifiDisplayState > 0 {
			modeNote := "One UI Desktop Windowing"
			if prof.HasDeskLauncher {
				modeNote = "Samsung DeX Launcher"
			}
			return TruthVerdict{
				Backend:      BackendADBLoopback,
				IsGenuineDeX: true,
				Label:        "Samsung Native DeX (ADB Loopback)",
				Reason:       fmt.Sprintf("SemWifiDisplayConfig loopback active (%s, One UI %s)", modeNote, prof.OneUIVersion),
			}
		}
	}
	if backend == BackendMiracastWFD {
		return TruthVerdict{
			Backend:      BackendMiracastWFD,
			IsGenuineDeX: true,
			Label:        "Samsung Wireless DeX (Miracast WFD)",
			Reason:       "Wi-Fi Direct Miracast sink negotiation active",
		}
	}
	return TruthVerdict{
		Backend:      BackendVirtualFallback,
		IsGenuineDeX: false,
		Label:        "Unverified Display Mode",
		Reason:       "DeX display verification incomplete",
	}
}

// FormatOneUIVersion formats Samsung's integer `ro.build.version.oneui` value
// (e.g. "90000" -> "9.0", "80500" -> "8.5", "60101" -> "6.1.1").
func FormatOneUIVersion(raw string) string {
	s := strings.TrimSpace(raw)
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return s
	}
	major := n / 10000
	minor := (n / 100) % 100
	patch := n % 100
	if patch > 0 {
		return fmt.Sprintf("%d.%d.%d", major, minor, patch)
	}
	return fmt.Sprintf("%d.%d", major, minor)
}

// SelectRoute chooses Native DeX loopback vs Virtual Display Compatibility Mode for a device
// according to hardware/firmware support and user preference while keeping labeling honest (`INV-06`).
func SelectRoute(prof adb.DeviceProfile, probe ProbeReport, transport adb.Transport, pref RoutePreference) RoutePlan {
	if pref == "" {
		pref = RouteAuto
	}
	oneui := FormatOneUIVersion(prof.OneUIVersion)
	canNative := strings.EqualFold(prof.Manufacturer, "samsung") &&
		transport != adb.TransportEmulator &&
		probe.HasSemBuilder &&
		probe.HasConnectWithConfig

	if pref == RouteVirtualFallback {
		v := EvaluateTruth(prof, probe, "", BackendVirtualFallback)
		return RoutePlan{
			Preference:      pref,
			Backend:         v.Backend,
			IsGenuineDeX:    false,
			CanUseNativeDeX: canNative,
			OneUIDisplay:    oneui,
			Label:           v.Label,
			Reason:          "Manual Virtual Display Compatibility Mode selected",
		}
	}
	if canNative {
		v := EvaluateTruth(prof, probe, "AndroFuseDeX", BackendADBLoopback)
		return RoutePlan{
			Preference:      pref,
			Backend:         v.Backend,
			IsGenuineDeX:    v.IsGenuineDeX,
			CanUseNativeDeX: true,
			OneUIDisplay:    oneui,
			Label:           v.Label,
			Reason:          v.Reason,
		}
	}
	reason := "Samsung SemWifiDisplayConfig API unavailable; using Virtual Display Fallback"
	if !strings.EqualFold(prof.Manufacturer, "samsung") {
		reason = fmt.Sprintf("Non-Samsung device (%s); using Virtual Display Fallback", prof.Manufacturer)
	} else if transport == adb.TransportEmulator {
		reason = "Android Emulator uses Virtual Display Fallback instead of Samsung DeX loopback"
	}
	v := EvaluateTruth(prof, probe, "", BackendVirtualFallback)
	return RoutePlan{
		Preference:      pref,
		Backend:         BackendVirtualFallback,
		IsGenuineDeX:    false,
		CanUseNativeDeX: false,
		OneUIDisplay:    oneui,
		Label:           v.Label,
		Reason:          reason,
	}
}

// PushAndProbe pushes androfuse-dex-helper.jar and runs `Main probe` on serial.
func PushAndProbe(ctx context.Context, client *adb.Client, serial string) (ProbeReport, error) {
	jar, err := FindHelperJar()
	if err != nil {
		return ProbeReport{}, err
	}
	if err := client.PushFile(ctx, serial, jar, RemoteToolPath); err != nil {
		return ProbeReport{}, fmt.Errorf("push dex helper: %w", err)
	}
	_, _ = client.Shell(ctx, serial, "chmod 600 "+RemoteToolPath+" 2>/dev/null || true")
	cmd := fmt.Sprintf("CLASSPATH=%s app_process / com.androfuse.dexhelper.Main probe", RemoteToolPath)
	out, err := client.Shell(ctx, serial, cmd)
	if err != nil {
		return ProbeReport{}, fmt.Errorf("dex helper probe: %w (%s)", err, out)
	}
	return ParseProbeOutput(out)
}

// ParseProbeOutput extracts the ProbeReport from an `AFDEX1|PROBE|{json}` line.
func ParseProbeOutput(raw string) (ProbeReport, error) {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if payload, ok := strings.CutPrefix(line, "AFDEX1|PROBE|"); ok {
			var rep ProbeReport
			if err := json.Unmarshal([]byte(payload), &rep); err != nil {
				return ProbeReport{}, fmt.Errorf("parse probe JSON: %w", err)
			}
			return rep, nil
		}
	}
	return ProbeReport{}, fmt.Errorf("missing AFDEX1|PROBE line in output %q", raw)
}

// Recover runs `Main recover` on serial to clear any lingering DeX loopback session.
func Recover(ctx context.Context, client *adb.Client, serial string) error {
	cmd := fmt.Sprintf("CLASSPATH=%s app_process / com.androfuse.dexhelper.Main recover", RemoteToolPath)
	out, err := client.Shell(ctx, serial, cmd)
	if err != nil {
		return fmt.Errorf("dex recover: %w (%s)", err, out)
	}
	if strings.Contains(out, "RESIDUAL") {
		return fmt.Errorf("residual DeX display remained after recover: %s", out)
	}
	return nil
}

// detachedHelperCommand returns the `adb shell` command line that runs helper args
// (`serve …`) in its own session with SIGHUP ignored.
// Notes: adbd kills the shell process group when its transport drops (USB unplug, Wi-Fi loss),
// which used to kill `serve` before its grace window (`INV-02`) could start; detached, the helper
// only sees stdin EOF, keeps the DeX display alive and waits for `attach` on the new transport.
// Verified on Fold 8 / toybox 0.8.13. Falls back to a plain launch where `setsid -w` is missing.
func detachedHelperCommand(jar, args string) string {
	launch := "app_process / com.androfuse.dexhelper.Main " + args
	return fmt.Sprintf("export CLASSPATH=%s; if setsid -w true 2>/dev/null; then exec setsid -w nohup %s; else exec %s; fi",
		jar, launch, launch)
}

// StartNativeLoopback launches `androfuse-dex-helper.jar serve` to activate genuine
// Samsung DeX on loopback (`127.0.0.1:7236`) and waits for READY display metadata.
func StartNativeLoopback(
	ctx context.Context,
	client *adb.Client,
	store *lease.Store,
	serial string,
	width, height, dpi, graceSec int,
) (*LoopbackSession, error) {
	if client == nil {
		return nil, errors.New("adb client is required")
	}
	// Ensure One UI display pipeline is awake before attaching DeX loopback.
	_ = client.WakeScreen(ctx, serial)
	jar, err := FindHelperJar()
	if err != nil {
		return nil, err
	}
	if err := client.PushFile(ctx, serial, jar, RemoteToolPath); err != nil {
		return nil, fmt.Errorf("push dex helper: %w", err)
	}
	_, _ = client.Shell(ctx, serial, "chmod 600 "+RemoteToolPath+" 2>/dev/null || true")
	var tokBytes [6]byte
	if _, err := rand.Read(tokBytes[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tokBytes[:])
	sessionID := "dex-" + token

	if store != nil {
		if err := store.Acquire(lease.Lease{
			SessionID:  sessionID,
			DeviceHash: adb.DeviceHash(serial),
			Kind:       lease.KindNativeDeX,
			Token:      token,
		}); err != nil {
			return nil, err
		}
	}

	shellCmd := detachedHelperCommand(RemoteToolPath, fmt.Sprintf("serve %s %d %d %d %d", token, width, height, dpi, graceSec))
	cmd := exec.Command(client.Path, "-s", serial, "shell", shellCmd)
	sysproc.HideConsole(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		if store != nil {
			_ = store.Release(sessionID)
		}
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		if store != nil {
			_ = store.Release(sessionID)
		}
		return nil, err
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		if store != nil {
			_ = store.Release(sessionID)
		}
		return nil, fmt.Errorf("start dex helper: %w", err)
	}

	sess := &LoopbackSession{
		sessionID: sessionID,
		serial:    serial,
		token:     token,
		graceSec:  graceSec,
		adb:       client,
		leases:    store,
		cmd:       cmd,
		stdin:     stdin,
		doneCh:    make(chan struct{}),
		lineCh:    make(chan string, 32),
	}

	go func() {
		defer close(sess.doneCh)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			sess.lineCh <- strings.TrimSpace(sc.Text())
		}
		close(sess.lineCh)
	}()

	readyDeadline := time.NewTimer(18 * time.Second)
	defer readyDeadline.Stop()
	for {
		select {
		case <-ctx.Done():
			sess.Stop()
			return nil, ctx.Err()
		case <-readyDeadline.C:
			sess.Stop()
			return nil, fmt.Errorf("timed out waiting for DeX loopback READY (stderr=%s)", strings.TrimSpace(stderrBuf.String()))
		case line, ok := <-sess.lineCh:
			if !ok {
				sess.Stop()
				return nil, fmt.Errorf("dex helper exited before READY (stderr=%s)", strings.TrimSpace(stderrBuf.String()))
			}
			if payload, ok := strings.CutPrefix(line, "AFDEX1|ERR|"); ok {
				sess.Stop()
				return nil, fmt.Errorf("dex helper error: %s", payload)
			}
			prefix := "AFDEX1|" + token + "|READY|"
			if payload, ok := strings.CutPrefix(line, prefix); ok {
				info, parseErr := ParseReadyPayload(payload)
				if parseErr != nil {
					sess.Stop()
					return nil, parseErr
				}
				sess.ready = info
				if store != nil {
					_ = store.Acquire(lease.Lease{
						SessionID:  sessionID,
						DeviceHash: adb.DeviceHash(serial),
						Kind:       lease.KindNativeDeX,
						Token:      token,
						DisplayID:  info.DisplayID,
					})
				}
				return sess, nil
			}
		}
	}
}

// ParseReadyPayload parses `<displayId>|<w>|<h>|<dpi>|<type>|<flags>|<pid>[|<ctrlPort>]`.
func ParseReadyPayload(payload string) (ReadyInfo, error) {
	parts := strings.Split(strings.TrimSpace(payload), "|")
	if len(parts) < 7 {
		return ReadyInfo{}, fmt.Errorf("malformed READY payload %q", payload)
	}
	nums := make([]int, 7)
	for i := range 7 {
		v, err := strconv.Atoi(parts[i])
		if err != nil {
			return ReadyInfo{}, fmt.Errorf("invalid field %d in READY payload %q: %w", i, payload, err)
		}
		nums[i] = v
	}
	var ctrlPort int
	if len(parts) >= 8 && strings.TrimSpace(parts[7]) != "" {
		cp, err := strconv.Atoi(strings.TrimSpace(parts[7]))
		if err != nil {
			return ReadyInfo{}, fmt.Errorf("invalid ctrlPort in READY payload %q: %w", payload, err)
		}
		ctrlPort = cp
	}
	return ReadyInfo{
		DisplayID: nums[0],
		Width:     nums[1],
		Height:    nums[2],
		DPI:       nums[3],
		Type:      nums[4],
		Flags:     nums[5],
		RemotePID: nums[6],
		CtrlPort:  ctrlPort,
	}, nil
}

// Ready returns the active Samsung DeX display metadata.
func (s *LoopbackSession) Ready() ReadyInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// Serial returns the ADB transport serial currently supervising this DeX loopback.
func (s *LoopbackSession) Serial() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serial
}

// GraceSec returns the configured helper reconnect grace window in seconds.
func (s *LoopbackSession) GraceSec() int {
	return s.graceSec
}

// Done returns a channel closed when the current ADB helper control pipe exits.
func (s *LoopbackSession) Done() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.doneCh
}

// Reattach connects to the still-running helper's loopback control socket (`127.0.0.1:<CtrlPort>`)
// over newSerial after USB disconnect / TLS Wi-Fi hot handoff within the grace window (`INV-02`).
//
// Inputs: bounded context and the new online ADB serial for the same physical device.
// Returns: current ReadyInfo synced from `REATTACHED|<id>|<w>|<h>|<dpi>`.
func (s *LoopbackSession) Reattach(ctx context.Context, newSerial string) (ReadyInfo, error) {
	newSerial = strings.TrimSpace(newSerial)
	if newSerial == "" {
		return ReadyInfo{}, errors.New("reattach requires a non-empty ADB serial")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ReadyInfo{}, errors.New("dex session closed")
	}
	ctrlPort := s.ready.CtrlPort
	token := s.token
	prevType := s.ready.Type
	prevFlags := s.ready.Flags
	prevPID := s.ready.RemotePID
	adbClient := s.adb
	stores := s.leases
	sessionID := s.sessionID
	oldStdin := s.stdin
	oldCmd := s.cmd
	s.mu.Unlock()

	if ctrlPort <= 0 {
		return ReadyInfo{}, errors.New("dex helper did not advertise a control port")
	}
	if adbClient == nil {
		return ReadyInfo{}, errors.New("adb client is required")
	}

	shellCmd := fmt.Sprintf("CLASSPATH=%s app_process / com.androfuse.dexhelper.Main attach %s %d",
		RemoteToolPath, token, ctrlPort)
	cmd := exec.Command(adbClient.Path, "-s", newSerial, "shell", shellCmd)
	sysproc.HideConsole(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ReadyInfo{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return ReadyInfo{}, err
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return ReadyInfo{}, fmt.Errorf("start dex attach: %w", err)
	}

	doneCh := make(chan struct{})
	lineCh := make(chan string, 32)
	go func() {
		defer close(doneCh)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lineCh <- strings.TrimSpace(sc.Text())
		}
		close(lineCh)
	}()

	reattachPrefix := "AFDEX1|" + token + "|REATTACHED|"
	waitCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	var updated ReadyInfo
	for {
		select {
		case <-waitCtx.Done():
			_ = stdin.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
			return ReadyInfo{}, fmt.Errorf("timed out waiting for DeX REATTACHED (%w, stderr=%s)", waitCtx.Err(), strings.TrimSpace(stderrBuf.String()))
		case line, ok := <-lineCh:
			if !ok {
				_ = cmd.Wait()
				return ReadyInfo{}, fmt.Errorf("dex attach exited before REATTACHED (stderr=%s)", strings.TrimSpace(stderrBuf.String()))
			}
			if payload, isErr := strings.CutPrefix(line, "AFDEX1|ERR|"); isErr {
				_ = stdin.Close()
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				_ = cmd.Wait()
				return ReadyInfo{}, fmt.Errorf("dex attach error: %s", payload)
			}
			if payload, matched := strings.CutPrefix(line, reattachPrefix); matched {
				parts := strings.Split(strings.TrimSpace(payload), "|")
				if len(parts) < 4 {
					_ = stdin.Close()
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					return ReadyInfo{}, fmt.Errorf("malformed REATTACHED payload %q", payload)
				}
				id, err0 := strconv.Atoi(parts[0])
				w, err1 := strconv.Atoi(parts[1])
				h, err2 := strconv.Atoi(parts[2])
				d, err3 := strconv.Atoi(parts[3])
				if err0 != nil || err1 != nil || err2 != nil || err3 != nil {
					_ = stdin.Close()
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					return ReadyInfo{}, fmt.Errorf("invalid REATTACHED payload %q", payload)
				}
				updated = ReadyInfo{
					DisplayID: id,
					Width:     w,
					Height:    h,
					DPI:       d,
					Type:      prevType,
					Flags:     prevFlags,
					RemotePID: prevPID,
					CtrlPort:  ctrlPort,
				}
				goto attached
			}
		}
	}

attached:
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		return ReadyInfo{}, errors.New("dex session closed during reattach")
	}
	s.serial = newSerial
	s.cmd = cmd
	s.stdin = stdin
	s.lineCh = lineCh
	s.doneCh = doneCh
	s.ready = updated
	s.mu.Unlock()

	if oldStdin != nil {
		_ = oldStdin.Close()
	}
	if oldCmd != nil && oldCmd.Process != nil {
		_ = oldCmd.Process.Kill()
		_ = oldCmd.Wait()
	}
	if stores != nil {
		_ = stores.Acquire(lease.Lease{
			SessionID:  sessionID,
			DeviceHash: adb.DeviceHash(newSerial),
			Kind:       lease.KindNativeDeX,
			Token:      token,
			DisplayID:  updated.DisplayID,
		})
	}
	return updated, nil
}

// Ping verifies live presence of the active DeX display.
func (s *LoopbackSession) Ping(timeout time.Duration) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("dex session closed")
	}
	_, err := io.WriteString(s.stdin, "PING\n")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	pongPrefix := "AFDEX1|" + s.token + "|PONG|"
	for {
		select {
		case <-deadline.C:
			return errors.New("timed out waiting for DeX PONG")
		case line, ok := <-s.lineCh:
			if !ok {
				return errors.New("dex helper closed")
			}
			if strings.HasPrefix(line, pongPrefix) {
				return nil
			}
		}
	}
}

// Resize updates the active Samsung DeX virtual display width, height, and DPI via `RESIZE`.
//
// Returns: Updated ReadyInfo carrying settled DisplayID, Width, Height, and DPI.
func (s *LoopbackSession) Resize(width, height, dpi int, timeout time.Duration) (ReadyInfo, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ReadyInfo{}, errors.New("dex session closed")
	}
	_, err := fmt.Fprintf(s.stdin, "RESIZE %d %d %d\n", width, height, dpi)
	s.mu.Unlock()
	if err != nil {
		return ReadyInfo{}, err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	resizedPrefix := "AFDEX1|" + s.token + "|RESIZED|"
	for {
		select {
		case <-deadline.C:
			return ReadyInfo{}, errors.New("timed out waiting for DeX RESIZED")
		case line, ok := <-s.lineCh:
			if !ok {
				return ReadyInfo{}, errors.New("dex helper closed")
			}
			if payload, ok := strings.CutPrefix(line, resizedPrefix); ok {
				parts := strings.Split(strings.TrimSpace(payload), "|")
				if len(parts) < 4 {
					return ReadyInfo{}, fmt.Errorf("malformed RESIZED payload %q", payload)
				}
				id, err0 := strconv.Atoi(parts[0])
				w, err1 := strconv.Atoi(parts[1])
				h, err2 := strconv.Atoi(parts[2])
				d, err3 := strconv.Atoi(parts[3])
				if err0 != nil || err1 != nil || err2 != nil || err3 != nil {
					return ReadyInfo{}, fmt.Errorf("invalid RESIZED payload %q", payload)
				}
				info := ReadyInfo{
					DisplayID: id,
					Width:     w,
					Height:    h,
					DPI:       d,
					Type:      s.ready.Type,
					Flags:     s.ready.Flags,
					RemotePID: s.ready.RemotePID,
				}
				s.mu.Lock()
				s.ready = info
				s.mu.Unlock()
				return info, nil
			}
		}
	}
}

// Stop sends STOP, waits for clean display removal, runs recover if needed, and
// releases the persistent session lease (INV-03).
func (s *LoopbackSession) Stop() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	stdin := s.stdin
	cmd := s.cmd
	s.mu.Unlock()

	if stdin != nil {
		_, _ = io.WriteString(stdin, "STOP\n")
		_ = stdin.Close()
	}

	stopPrefix := "AFDEX1|" + s.token + "|STOPPED|"
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		for line := range s.lineCh {
			if strings.HasPrefix(line, stopPrefix) {
				s.stopped = true
				return
			}
		}
	}()

	select {
	case <-waitDone:
	case <-time.After(6 * time.Second):
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	<-s.doneCh
	if cmd != nil {
		_ = cmd.Wait()
	}

	if !s.stopped && s.adb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		_ = Recover(ctx, s.adb, s.serial)
		cancel()
	}
	if s.leases != nil {
		_ = s.leases.Release(s.sessionID)
	}
}

// StartDaemon pushes and launches the detached persistent UID 2000 daemon (`server/dexhelper`
// `daemon`, PLAN §9.2), which pushes its scoped `IBinder` to the Companion app and idles with
// zero active wakeups until stopped or replaced.
//
// Inputs: companionPkg overrides the default Companion package (`com.yuanzhe.androfuse`).
// Side effects: uploads `androfuse-dex-helper.jar` to RemoteHelperPath and scrcpy-server to
// scrcpy.RemoteServerPath, then replaces any running daemon with a detached `app_process`.
func StartDaemon(ctx context.Context, client *adb.Client, serial, companionPkg string) error {
	if client == nil {
		return errors.New("adb client is required")
	}
	if strings.TrimSpace(companionPkg) == "" {
		companionPkg = "com.yuanzhe.androfuse"
	}
	jar, err := FindHelperJar()
	if err != nil {
		return err
	}
	if err := client.PushFile(ctx, serial, jar, RemoteHelperPath); err != nil {
		return fmt.Errorf("push dex helper: %w", err)
	}
	// The daemon runs every mirror, DeX and app window through this copy; without it each session
	// would crash on the phone, so a missing local scrcpy-server is an error, not a skip.
	srvPath, err := scrcpy.FindCachedServer()
	if err != nil {
		return err
	}
	if err := client.PushFile(ctx, serial, srvPath, scrcpy.RemoteServerPath); err != nil {
		return fmt.Errorf("push scrcpy-server: %w", err)
	}
	_, _ = client.Shell(ctx, serial, "chmod 600 "+RemoteHelperPath+" "+scrcpy.RemoteServerPath+" 2>/dev/null || true")
	// Terminate any prior uid=2000 daemon so the new JAR/binary takes over the lock cleanly.
	_, _ = client.Shell(ctx, serial, "pkill -f 'com.androfuse.dexhelper.Main daemon' >/dev/null 2>&1 || true; sleep 0.2")
	inner := detachedHelperCommand(RemoteHelperPath, fmt.Sprintf("daemon --pkg %s", companionPkg))
	bg := fmt.Sprintf("(%s) </dev/null >/dev/null 2>&1 &", inner)
	_, err = client.Shell(ctx, serial, bg)
	if err != nil {
		return fmt.Errorf("launch uid=2000 daemon: %w", err)
	}
	return nil
}

// DaemonOutdated reports why the phone's running UID 2000 daemon must be restarted with this
// desktop's copies (StartDaemon), or "" when it is current or not running.
// Returns: "helper" or "scrcpy-server" when that copy on the phone is missing or differs from the
// local one, or "replaced" when either file changed after the daemon started.
// Notes: the daemon pins both SHA-256 digests at start, so any of these makes every session it
// runs fail (a missing scrcpy-server crashes app_process with ClassNotFoundException).
func DaemonOutdated(ctx context.Context, client *adb.Client, serial string) (string, error) {
	helper, err := FindHelperJar()
	if err != nil {
		return "", err
	}
	srv, err := scrcpy.FindCachedServer()
	if err != nil {
		return "", err
	}
	want := map[string]string{}
	for name, p := range map[string]string{"helper": helper, "scrcpy-server": srv} {
		raw, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(raw)
		want[name] = hex.EncodeToString(sum[:])
	}
	// `[d]aemon` keeps pgrep from matching this very shell's command line.
	script := fmt.Sprintf(`p=$(pgrep -f 'com.androfuse.dexhelper.Main [d]aemon' | head -n 1); `+
		`if [ -n "$p" ]; then awk -v now="$(date +%%s)" -v st="$(cut -d' ' -f22 /proc/$p/stat)" -v hz="$(getconf CLK_TCK 2>/dev/null || echo 100)" '{printf "START=%%d\n", now-($1-st/hz)}' /proc/uptime; fi; `+
		`for f in %s %s; do echo "FILE=$(stat -c %%Y $f 2>/dev/null) $(sha256sum $f 2>/dev/null | cut -d' ' -f1)"; done`,
		RemoteHelperPath, scrcpy.RemoteServerPath)
	out, err := client.Shell(ctx, serial, script)
	if err != nil {
		return "", err
	}
	return daemonOutdatedFrom(out, want["helper"], want["scrcpy-server"]), nil
}

// daemonOutdatedFrom evaluates DaemonOutdated's shell output against the local digests.
func daemonOutdatedFrom(out, helperSHA, serverSHA string) string {
	var start int64
	var files []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, "START="); ok {
			start, _ = strconv.ParseInt(v, 10, 64)
		} else if v, ok := strings.CutPrefix(l, "FILE="); ok {
			files = append(files, v)
		}
	}
	if start == 0 || len(files) != 2 {
		return "" // no daemon running: the ready check starts one.
	}
	for i, name := range []string{"helper", "scrcpy-server"} {
		mtime, sum, _ := strings.Cut(files[i], " ")
		if sum == "" || !strings.EqualFold(sum, []string{helperSHA, serverSHA}[i]) {
			return name
		}
		// A couple of seconds of slack: StartDaemon pushes right before it launches.
		if t, err := strconv.ParseInt(mtime, 10, 64); err == nil && t > start+2 {
			return "replaced"
		}
	}
	return ""
}
