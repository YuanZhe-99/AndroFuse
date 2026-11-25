// Package vm manages the optional, on-demand downloadable Android virtual machine
// (`%LOCALAPPDATA%\AndroFuse\vm`), enforcing native architecture parity (`INV-10`),
// headless `-no-window` execution, and clean lifecycle teardown (`INV-03`).
package vm

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"

	"github.com/YuanZhe-99/AndroFuse/internal/sysproc"
)

const (
	// DefaultAVDName is the managed virtual device profile created by Provision.
	DefaultAVDName = "AndroFuse_VM"
)

// State describes the current lifecycle phase of the built-in Android VM.
type State string

const (
	StateNotInstalled State = "not_installed"
	StateDownloading  State = "downloading"
	StateStopped      State = "stopped"
	StateStarting     State = "starting"
	StateRunning      State = "running"
	StateError        State = "error"
)

// Status summarizes built-in VM availability, native ABI, disk footprint, and runtime state.
type Status struct {
	State          State    `json:"state"`
	NativeABI      string   `json:"native_abi"`
	SystemImagePkg string   `json:"system_image_pkg"`
	VMRoot         string   `json:"vm_root"`
	AVDName        string   `json:"avd_name"`
	AvailableAVDs  []string `json:"available_avds,omitempty"`
	RunningSerial  string   `json:"running_serial,omitempty"`
	UsingHostSDK   bool     `json:"using_host_sdk"`
	StorageBytes   int64    `json:"storage_bytes"`
	ProgressPct    int      `json:"progress_pct,omitempty"`
	ProgressMsg    string   `json:"progress_msg,omitempty"`
	LastError      string   `json:"last_error,omitempty"`
}

// NativeABIForArch returns the native Android system image ABI and SDK package matching goarch (`INV-10`).
//
// Notes: Refuses unsupported architectures so Windows ARM64 never falls back to x64 emulation.
func NativeABIForArch(goarch string) (abi string, sysImgPkg string, err error) {
	switch goarch {
	case "arm64":
		return "arm64-v8a", "system-images;android-36;google_apis;arm64-v8a", nil
	case "amd64":
		return "x86_64", "system-images;android-36;google_apis;x86_64", nil
	default:
		return "", "", fmt.Errorf("unsupported host architecture %q for built-in Android VM (INV-10)", goarch)
	}
}

// DefaultVMRoot returns `%LOCALAPPDATA%\AndroFuse\vm` (or user cache fallback on non-Windows).
func DefaultVMRoot() string {
	if runtime.GOOS == "windows" {
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "AndroFuse", "vm")
		}
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "AndroFuse", "vm")
}

// Manager supervises provisioning, headless startup, boot readiness checks, and clean shutdown
// of the optional built-in Android VM.
type Manager struct {
	vmRoot   string
	adb      *adb.Client
	onChange func(Status)

	mu            sync.Mutex
	state         State
	progressPct   int
	progressMsg   string
	lastErr       string
	activeAVD     string
	runningSerial string
	proc          *exec.Cmd
	cancelProv    context.CancelFunc
}

// NewManager constructs a VM Manager rooted at vmRoot using adbClient (optional if adb is in PATH).
func NewManager(vmRoot string, adbClient *adb.Client, onChange func(Status)) *Manager {
	if strings.TrimSpace(vmRoot) == "" {
		vmRoot = DefaultVMRoot()
	}
	return &Manager{
		vmRoot:   vmRoot,
		adb:      adbClient,
		onChange: onChange,
		state:    StateNotInstalled,
	}
}

// Status inspects current managed/host SDK AVDs, live running `emulator-*` ADB targets, and storage usage.
func (m *Manager) Status(ctx context.Context) Status {
	abi, sysPkg, abiErr := NativeABIForArch(runtime.GOARCH)
	emuPath, sdkRoot, avdHome, usingHost := m.resolveEmulatorAndSDK()
	var avds []string
	if emuPath != "" {
		avds = listAVDs(emuPath, sdkRoot, avdHome)
	}
	storage := dirSizeBytes(m.vmRoot)

	var onlineEmu string
	client := m.ensureADBClient()
	if client != nil {
		qCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if devs, err := client.Devices(qCtx); err == nil {
			for _, d := range devs {
				if d.State == "device" && d.Transport == adb.TransportEmulator {
					onlineEmu = d.Serial
					break
				}
			}
		}
		cancel()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.proc != nil && m.proc.ProcessState != nil && m.proc.ProcessState.Exited() {
		m.proc = nil
		if m.state == StateRunning || m.state == StateStarting {
			m.runningSerial = ""
			m.state = StateStopped
		}
	}

	st := m.state
	runSerial := m.runningSerial
	if onlineEmu != "" {
		st = StateRunning
		runSerial = onlineEmu
		m.runningSerial = onlineEmu
		if m.state != StateDownloading {
			m.state = StateRunning
		}
	} else if st != StateDownloading && st != StateStarting && st != StateError {
		if len(avds) > 0 {
			st = StateStopped
		} else {
			st = StateNotInstalled
		}
		m.state = st
		runSerial = ""
		m.runningSerial = ""
	}

	avdName := m.activeAVD
	if avdName == "" {
		for _, a := range avds {
			if a == DefaultAVDName {
				avdName = a
				break
			}
		}
		if avdName == "" && len(avds) > 0 {
			avdName = avds[0]
		}
	}
	lastErr := m.lastErr
	if abiErr != nil && lastErr == "" {
		lastErr = abiErr.Error()
	}

	return Status{
		State:          st,
		NativeABI:      abi,
		SystemImagePkg: sysPkg,
		VMRoot:         m.vmRoot,
		AVDName:        avdName,
		AvailableAVDs:  avds,
		RunningSerial:  runSerial,
		UsingHostSDK:   usingHost,
		StorageBytes:   storage,
		ProgressPct:    m.progressPct,
		ProgressMsg:    m.progressMsg,
		LastError:      lastErr,
	}
}

// Start launches avdName (or DefaultAVDName / first available AVD) headlessly with `-no-window`,
// waits for `sys.boot_completed=1`, and returns the active running Status (`INV-03`, `INV-10`).
func (m *Manager) Start(ctx context.Context, avdName string) (Status, error) {
	if _, _, err := NativeABIForArch(runtime.GOARCH); err != nil {
		return m.failState(err), err
	}
	cur := m.Status(ctx)
	if cur.State == StateRunning && cur.RunningSerial != "" {
		return cur, nil
	}
	if cur.State == StateDownloading || cur.State == StateStarting {
		return cur, fmt.Errorf("VM is currently %s", cur.State)
	}

	emuPath, sdkRoot, avdHome, _ := m.resolveEmulatorAndSDK()
	if emuPath == "" {
		err := errors.New("Android Emulator binary not found; download Built-in VM first")
		return m.failState(err), err
	}
	avds := listAVDs(emuPath, sdkRoot, avdHome)
	if len(avds) == 0 {
		err := errors.New("no Android VM profile found; download Built-in VM first")
		return m.failState(err), err
	}
	target := strings.TrimSpace(avdName)
	if target == "" {
		target = cur.AVDName
	}
	if target == "" {
		target = avds[0]
	}

	m.mu.Lock()
	m.state = StateStarting
	m.activeAVD = target
	m.progressPct = 15
	m.progressMsg = "Starting headless Android VM (" + target + ")..."
	m.lastErr = ""
	m.mu.Unlock()
	m.emitStatus(ctx)

	args := []string{
		"-avd", target,
		"-no-window",
		"-no-audio",
		"-no-boot-anim",
		"-gpu", "auto",
		"-netdelay", "none",
		"-netspeed", "full",
	}
	cmd := exec.Command(emuPath, args...)
	sysproc.HideConsole(cmd)
	cmd.Env = buildSDKEvn(sdkRoot, avdHome)

	if err := cmd.Start(); err != nil {
		return m.failState(fmt.Errorf("launch emulator: %w", err)), err
	}

	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()

	m.mu.Lock()
	m.proc = cmd
	m.mu.Unlock()

	client := m.ensureADBClient()
	if client == nil {
		_ = m.Stop(context.Background())
		err := errors.New("adb executable not found after VM launch")
		return m.failState(err), err
	}

	bootCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	pct := 25
	for {
		select {
		case wErr := <-exitCh:
			m.mu.Lock()
			if m.proc == cmd {
				m.proc = nil
			}
			m.mu.Unlock()
			err := fmt.Errorf("headless VM exited before boot completed (%v)", wErr)
			return m.failState(err), err
		case <-bootCtx.Done():
			_ = m.Stop(context.Background())
			err := fmt.Errorf("timed out waiting for Android VM boot: %w", bootCtx.Err())
			return m.failState(err), err
		case <-ticker.C:
			qCtx, qCancel := context.WithTimeout(bootCtx, 3*time.Second)
			devs, _ := client.Devices(qCtx)
			var emuSerial string
			for _, d := range devs {
				if d.State == "device" && d.Transport == adb.TransportEmulator {
					emuSerial = d.Serial
					break
				}
			}
			if emuSerial != "" {
				booted, _ := client.Shell(qCtx, emuSerial, "getprop sys.boot_completed")
				qCancel()
				if strings.TrimSpace(booted) == "1" {
					m.mu.Lock()
					m.state = StateRunning
					m.runningSerial = emuSerial
					m.progressPct = 100
					m.progressMsg = "VM online (" + emuSerial + ")"
					m.lastErr = ""
					m.mu.Unlock()
					st := m.Status(context.Background())
					m.emitStatus(context.Background())
					return st, nil
				}
				if pct < 85 {
					pct += 10
				}
				m.mu.Lock()
				m.progressPct = pct
				m.progressMsg = "Booting Android runtime (" + emuSerial + ")..."
				m.mu.Unlock()
				m.emitStatus(context.Background())
			} else {
				qCancel()
				if pct < 60 {
					pct += 5
				}
				m.mu.Lock()
				m.progressPct = pct
				m.progressMsg = "Waiting for ADB transport (" + target + ")..."
				m.mu.Unlock()
				m.emitStatus(context.Background())
			}
		}
	}
}

// Stop gracefully shuts down any running emulator instance (`adb emu kill`),
// kills the child process if needed, and resets runtime state (`INV-03`).
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	if m.cancelProv != nil {
		m.cancelProv()
		m.cancelProv = nil
	}
	proc := m.proc
	trackedSerial := m.runningSerial
	m.mu.Unlock()

	client := m.ensureADBClient()
	if client != nil {
		serials := make([]string, 0, 2)
		if trackedSerial != "" {
			serials = append(serials, trackedSerial)
		}
		qCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		if devs, err := client.Devices(qCtx); err == nil {
			for _, d := range devs {
				if d.Transport == adb.TransportEmulator && d.Serial != trackedSerial {
					serials = append(serials, d.Serial)
				}
			}
		}
		for _, s := range serials {
			_, _ = client.Run(qCtx, "-s", s, "emu", "kill")
		}
		cancel()
	}

	if proc != nil && proc.Process != nil {
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			if proc.ProcessState != nil && proc.ProcessState.Exited() {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
		if proc.ProcessState == nil || !proc.ProcessState.Exited() {
			_ = proc.Process.Kill()
		}
	}

	m.mu.Lock()
	m.proc = nil
	m.runningSerial = ""
	m.progressPct = 0
	m.progressMsg = ""
	m.state = StateStopped
	m.mu.Unlock()
	m.emitStatus(context.Background())
	return nil
}

// DeleteManaged stops any active VM and removes `%LOCALAPPDATA%\AndroFuse\vm`.
func (m *Manager) DeleteManaged(ctx context.Context) error {
	_ = m.Stop(ctx)
	if strings.TrimSpace(m.vmRoot) != "" {
		if err := os.RemoveAll(m.vmRoot); err != nil {
			return fmt.Errorf("remove managed vm root: %w", err)
		}
	}
	m.mu.Lock()
	m.state = StateNotInstalled
	m.progressPct = 0
	m.progressMsg = ""
	m.lastErr = ""
	m.activeAVD = ""
	m.runningSerial = ""
	m.mu.Unlock()
	m.emitStatus(context.Background())
	return nil
}

func (m *Manager) failState(err error) Status {
	m.mu.Lock()
	m.state = StateError
	m.lastErr = err.Error()
	m.progressMsg = ""
	m.mu.Unlock()
	st := m.Status(context.Background())
	m.emitStatus(context.Background())
	return st
}

func (m *Manager) emitStatus(ctx context.Context) {
	if m.onChange != nil {
		m.onChange(m.Status(ctx))
	}
}

func (m *Manager) ensureADBClient() *adb.Client {
	if m.adb != nil {
		return m.adb
	}
	managedADB := filepath.Join(m.vmRoot, "sdk", "platform-tools", adbBinaryName())
	if _, err := os.Stat(managedADB); err == nil {
		return &adb.Client{Path: managedADB}
	}
	c, err := adb.FindClient()
	if err == nil {
		m.adb = c
		return c
	}
	return nil
}

func (m *Manager) resolveEmulatorAndSDK() (emuPath, sdkRoot, avdHome string, usingHostSDK bool) {
	exeName := emulatorBinaryName()
	managedSDK := filepath.Join(m.vmRoot, "sdk")
	managedEmu := filepath.Join(managedSDK, "emulator", exeName)
	managedAVD := filepath.Join(m.vmRoot, "avd")
	if _, err := os.Stat(managedEmu); err == nil {
		return managedEmu, managedSDK, managedAVD, false
	}
	for _, hostSDK := range hostSDKCandidates() {
		cand := filepath.Join(hostSDK, "emulator", exeName)
		if _, err := os.Stat(cand); err == nil {
			return cand, hostSDK, defaultHostAVDHome(), true
		}
	}
	return "", managedSDK, managedAVD, false
}

func hostSDKCandidates() []string {
	var out []string
	if v := strings.TrimSpace(os.Getenv("ANDROID_HOME")); v != "" {
		out = append(out, v)
	}
	if v := strings.TrimSpace(os.Getenv("ANDROID_SDK_ROOT")); v != "" {
		out = append(out, v)
	}
	if runtime.GOOS == "windows" {
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			out = append(out, filepath.Join(local, "Android", "Sdk"))
		}
	}
	return out
}

func defaultHostAVDHome() string {
	if v := strings.TrimSpace(os.Getenv("ANDROID_AVD_HOME")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".android", "avd")
}

func buildSDKEvn(sdkRoot, avdHome string) []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env)+3)
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANDROID_HOME=") ||
			strings.HasPrefix(kv, "ANDROID_SDK_ROOT=") ||
			strings.HasPrefix(kv, "ANDROID_AVD_HOME=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	if sdkRoot != "" {
		filtered = append(filtered, "ANDROID_HOME="+sdkRoot, "ANDROID_SDK_ROOT="+sdkRoot)
	}
	if avdHome != "" {
		filtered = append(filtered, "ANDROID_AVD_HOME="+avdHome)
	}
	return filtered
}

func listAVDs(emuPath, sdkRoot, avdHome string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, emuPath, "-list-avds")
	sysproc.HideConsole(cmd)
	cmd.Env = buildSDKEvn(sdkRoot, avdHome)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var avds []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			avds = append(avds, line)
		}
	}
	return avds
}

func dirSizeBytes(root string) int64 {
	if strings.TrimSpace(root) == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, iErr := d.Info(); iErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func emulatorBinaryName() string {
	if runtime.GOOS == "windows" {
		return "emulator.exe"
	}
	return "emulator"
}

func adbBinaryName() string {
	if runtime.GOOS == "windows" {
		return "adb.exe"
	}
	return "adb"
}
