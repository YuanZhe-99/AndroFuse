// Package dex orchestrates dual-channel Samsung DeX (ADB SemWifiDisplayConfig
// loopback via androfuse-dex-helper.jar, plus Miracast fallback probes) and
// enforces honest DeX-vs-virtual-display classification (INV-06).
package dex

import (
	"bufio"
	"context"
	"crypto/rand"
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
)

// RemoteHelperPath is the non-world-writable device path for androfuse-dex-helper.jar.
const RemoteHelperPath = "/data/local/tmp/androfuse-dex-helper.jar"

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

// ReadyInfo captures live display metadata emitted by `Main serve`.
type ReadyInfo struct {
	DisplayID int
	Width     int
	Height    int
	DPI       int
	Type      int
	Flags     int
	RemotePID int
}

// LoopbackSession supervises a running `androfuse-dex-helper.jar serve` process on the device.
type LoopbackSession struct {
	sessionID string
	serial    string
	token     string
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

// FindHelperJar locates build/androfuse-dex-helper.jar near the binary or repo root.
func FindHelperJar() (string, error) {
	if env := strings.TrimSpace(os.Getenv("ANDROFUSE_DEX_HELPER")); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "androfuse-dex-helper.jar"))
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

// PushAndProbe pushes androfuse-dex-helper.jar and runs `Main probe` on serial.
func PushAndProbe(ctx context.Context, client *adb.Client, serial string) (ProbeReport, error) {
	jar, err := FindHelperJar()
	if err != nil {
		return ProbeReport{}, err
	}
	if err := client.PushFile(ctx, serial, jar, RemoteHelperPath); err != nil {
		return ProbeReport{}, fmt.Errorf("push dex helper: %w", err)
	}
	cmd := fmt.Sprintf("CLASSPATH=%s app_process / com.androfuse.dexhelper.Main probe", RemoteHelperPath)
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
	cmd := fmt.Sprintf("CLASSPATH=%s app_process / com.androfuse.dexhelper.Main recover", RemoteHelperPath)
	out, err := client.Shell(ctx, serial, cmd)
	if err != nil {
		return fmt.Errorf("dex recover: %w (%s)", err, out)
	}
	if strings.Contains(out, "RESIDUAL") {
		return fmt.Errorf("residual DeX display remained after recover: %s", out)
	}
	return nil
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
	if err := client.PushFile(ctx, serial, jar, RemoteHelperPath); err != nil {
		return nil, fmt.Errorf("push dex helper: %w", err)
	}
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

	shellCmd := fmt.Sprintf("CLASSPATH=%s app_process / com.androfuse.dexhelper.Main serve %s %d %d %d %d",
		RemoteHelperPath, token, width, height, dpi, graceSec)
	cmd := exec.Command(client.Path, "-s", serial, "shell", shellCmd)
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

// ParseReadyPayload parses `<displayId>|<w>|<h>|<dpi>|<type>|<flags>|<pid>`.
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
	return ReadyInfo{
		DisplayID: nums[0],
		Width:     nums[1],
		Height:    nums[2],
		DPI:       nums[3],
		Type:      nums[4],
		Flags:     nums[5],
		RemotePID: nums[6],
	}, nil
}

// Ready returns the active Samsung DeX display metadata.
func (s *LoopbackSession) Ready() ReadyInfo {
	return s.ready
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
