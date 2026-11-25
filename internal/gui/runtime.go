package gui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/androidhost"
	"github.com/YuanZhe-99/AndroFuse/internal/avbridge"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/dex"
	"github.com/YuanZhe-99/AndroFuse/internal/lease"
	"github.com/YuanZhe-99/AndroFuse/internal/mcp"
	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
	"github.com/YuanZhe-99/AndroFuse/internal/scrcpy"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
	"github.com/YuanZhe-99/AndroFuse/internal/vm"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// DiscoveredDevice holds inspected metadata, collapsed transports (INV-02),
// DeX routing capabilities (INV-06), and user-0 launchable apps for one physical device.
type DiscoveredDevice struct {
	HardwareHash string
	Target       adb.Device
	Transports   []adb.Device
	Profile      adb.DeviceProfile
	DexProbe     dex.ProbeReport
	TruthDeX     dex.TruthVerdict
	Route        dex.RoutePlan
	Apps         []adb.AppEntry
	Probed       bool
	ProbeErr     string
}

// EncoderHeadroom computes hardware encoder stream usage vs probed limit (`INV-01`).
//
// Notes: Defaults to 4 concurrent streams when a device XML omits `concurrent-instances`.
func EncoderHeadroom(prof adb.DeviceProfile, activeStreams int) (used, limit int, atLimit, nearLimit bool) {
	limit = prof.AVCEncoderMax
	if limit <= 0 {
		limit = 4
	}
	if activeStreams < 0 {
		activeStreams = 0
	}
	used = activeStreams
	atLimit = used >= limit
	nearLimit = !atLimit && used >= limit-1
	return used, limit, atLimit, nearLimit
}

// ActiveSessionSummary describes an open SessionWindow for the dashboard strip.
type ActiveSessionSummary struct {
	ID           string
	Title        string
	Transport    string
	BackendLabel string
	Resolution   string
	MeasuredFPS  float64
}

type guiRuntime struct {
	coreMu          sync.Mutex
	coreLib         *core.Lib
	adbClient       *adb.Client
	leaseStore      *lease.Store
	vmMgr           *vm.Manager
	androidHost     *androidhost.Host
	compClient      *companion.Client
	mcpServer       *mcp.Server
	meshMgr         *mesh.Manager
	micUplink       *avbridge.UplinkBridge
	micUplinkCancel context.CancelFunc
	recoverRan      bool
}

func (a *App) ensureAndroidHost() *androidhost.Host {
	a.rt.coreMu.Lock()
	if a.rt.androidHost == nil {
		a.rt.androidHost = androidhost.New(a.stateDir, func() {
			a.update(func() {})
		})
	}
	h := a.rt.androidHost
	a.rt.coreMu.Unlock()
	return h
}

// ConnectCompanion establishes an authenticated TLS 1.3 connection to an optional Android Companion APK.
func (a *App) ConnectCompanion(addr, token string) {
	addr = strings.TrimSpace(addr)
	token = strings.TrimSpace(token)
	a.update(func() { a.compNotice = "" })
	if addr == "" || token == "" {
		a.update(func() { a.compNotice = L.CompanionCardDesc() })
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client, err := companion.Dial(ctx, addr, token, "")
		if err != nil {
			a.update(func() { a.compNotice = err.Error() })
			return
		}
		st, _ := client.GetStatus(ctx)

		a.rt.coreMu.Lock()
		old := a.rt.compClient
		a.rt.compClient = client
		a.rt.coreMu.Unlock()
		if old != nil {
			_ = old.Close()
		}
		a.update(func() {
			a.compConnected = true
			a.compStatus = st
			a.compNotice = ""
		})
	}()
}

// DisconnectCompanion closes any active TLS 1.3 connection to the Android Companion APK.
func (a *App) DisconnectCompanion() {
	go func() {
		a.rt.coreMu.Lock()
		c := a.rt.compClient
		a.rt.compClient = nil
		a.rt.coreMu.Unlock()
		if c != nil {
			_ = c.Close()
		}
		a.update(func() {
			a.compConnected = false
			a.compStatus = companion.DeviceStatus{}
			a.compNotice = ""
		})
	}()
}

// ContinueCompanionApp asks Companion for the active foreground app and launches it in an Independent App Window.
func (a *App) ContinueCompanionApp() {
	a.rt.coreMu.Lock()
	c := a.rt.compClient
	a.rt.coreMu.Unlock()
	if c == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		res, err := c.RequestContinuity(ctx, "handoff_to_pc", "")
		cancel()
		if err != nil {
			a.update(func() { a.compNotice = err.Error() })
			return
		}
		if res.Package == "" {
			return
		}
		a.mu.Lock()
		var target *DiscoveredDevice
		if len(a.devices) > 0 && a.devices[0].Target.State == "device" {
			cp := a.devices[0]
			target = &cp
		}
		a.mu.Unlock()
		if target != nil {
			a.LaunchAppWindow(*target, adb.AppEntry{Package: res.Package, Label: res.Package, Resizeable: true})
		}
	}()
}

// ConnectCompanionBeaconADB connects to the TLS wireless ADB port advertised by Companion over VPN/LAN.
func (a *App) ConnectCompanionBeaconADB() {
	a.mu.Lock()
	addr := strings.TrimSpace(a.compAddr)
	port := a.compStatus.WirelessADBPort
	a.mu.Unlock()
	if addr == "" || port <= 0 {
		return
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return
	}
	a.ConnectWirelessTLS(net.JoinHostPort(host, strconv.Itoa(port)))
}

// RunCompanionDisplayGuard invokes orphan display cleanup on the connected Companion APK (`INV-03`).
func (a *App) RunCompanionDisplayGuard() {
	a.rt.coreMu.Lock()
	c := a.rt.compClient
	a.rt.coreMu.Unlock()
	if c == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		res, err := c.RunDisplayGuard(ctx)
		cancel()
		if err != nil {
			a.update(func() { a.compNotice = err.Error() })
			return
		}
		a.update(func() { a.compNotice = res.Message })
	}()
}

func (a *App) ensureVMManager() *vm.Manager {
	a.rt.coreMu.Lock()
	if a.rt.vmMgr != nil {
		m := a.rt.vmMgr
		a.rt.coreMu.Unlock()
		return m
	}
	client := a.rt.adbClient
	if client == nil {
		if c, err := adb.FindClient(); err == nil {
			a.rt.adbClient = c
			client = c
		}
	}
	m := vm.NewManager(vm.DefaultVMRoot(), client, func(st vm.Status) {
		a.update(func() {
			a.vmStatus = st
		})
	})
	a.rt.vmMgr = m
	a.rt.coreMu.Unlock()
	return m
}

// RefreshVMStatus inspects installed and running built-in Android VMs off the UI thread.
func (a *App) RefreshVMStatus() {
	go func() {
		mgr := a.ensureVMManager()
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		st := mgr.Status(ctx)
		cancel()
		a.update(func() {
			a.vmStatus = st
		})
	}()
}

// ProvisionVM downloads and provisions a native-ABI Android VM into `%LOCALAPPDATA%\AndroFuse\vm`.
func (a *App) ProvisionVM() {
	go func() {
		mgr := a.ensureVMManager()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := mgr.Provision(ctx, vm.DefaultAVDName); err != nil {
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.RefreshVMStatus()
	}()
}

// StartVM boots the built-in Android VM headlessly (`-no-window`) and refreshes Devices when online.
func (a *App) StartVM(avdName string) {
	a.update(func() { a.deviceNotice = "" })
	go func() {
		mgr := a.ensureVMManager()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := mgr.Start(ctx, avdName); err != nil {
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.RefreshDevices()
	}()
}

// StopVM gracefully shuts down the running built-in Android VM (`adb emu kill`).
func (a *App) StopVM() {
	go func() {
		mgr := a.ensureVMManager()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := mgr.Stop(ctx); err != nil {
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.RefreshDevices()
	}()
}

// DeleteManagedVM removes the managed built-in VM directory after stopping any active instance.
func (a *App) DeleteManagedVM() {
	go func() {
		mgr := a.ensureVMManager()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := mgr.DeleteManaged(ctx); err != nil {
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.RefreshDevices()
	}()
}

func (a *App) ensureCoreLocked() (*core.Lib, error) {
	if a.rt.coreLib != nil {
		return a.rt.coreLib, nil
	}
	libPath := locateCoreLibrary()
	if libPath == "" {
		return nil, fmt.Errorf("%s", L.DiagCoreMissing())
	}
	lib, err := core.Open(libPath)
	if err != nil {
		return nil, err
	}
	if _, err := lib.Start(core.Config{
		Version:  version.Version,
		StateDir: a.stateDir,
		Profile:  a.profile,
	}); err != nil {
		return nil, err
	}
	a.rt.coreLib = lib
	return lib, nil
}

func (a *App) ensureADBAndLeases() (*adb.Client, *lease.Store, error) {
	a.rt.coreMu.Lock()
	defer a.rt.coreMu.Unlock()

	if a.rt.adbClient == nil {
		c, err := adb.FindClient()
		if err != nil {
			return nil, nil, err
		}
		a.rt.adbClient = c
	}
	if a.rt.leaseStore == nil && a.stateDir != "" {
		st, err := lease.Open(filepath.Join(a.stateDir, "leases.json"))
		if err == nil {
			a.rt.leaseStore = st
		}
	}
	return a.rt.adbClient, a.rt.leaseStore, nil
}

func (a *App) recoverStaleLeases(ctx context.Context, client *adb.Client, store *lease.Store, devs []adb.Device) {
	if client == nil || store == nil {
		return
	}
	stale := store.List()
	if len(stale) == 0 {
		return
	}
	byHash := make(map[string]adb.Device, len(devs))
	for _, d := range devs {
		if d.State == "device" {
			byHash[d.DeviceHash] = d
		}
	}
	for _, l := range stale {
		dev, ok := byHash[l.DeviceHash]
		if ok {
			if l.ForwardPort > 0 {
				_ = client.RemoveForward(ctx, dev.Serial, l.ForwardPort)
			}
			if l.Kind == lease.KindNativeDeX {
				_ = dex.Recover(ctx, client, dev.Serial)
			}
		}
		_ = store.Release(l.SessionID)
	}
}

func (a *App) dexRoutePref(hwHash string) dex.RoutePreference {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dexRoutes != nil {
		if pref, ok := a.dexRoutes[hwHash]; ok && pref != "" {
			return pref
		}
	}
	return dex.RouteAuto
}

func (a *App) setDexRoutePref(hwHash string, pref dex.RoutePreference) {
	if hwHash == "" {
		return
	}
	a.mu.Lock()
	if a.dexRoutes == nil {
		a.dexRoutes = make(map[string]dex.RoutePreference)
	}
	a.dexRoutes[hwHash] = pref
	for i := range a.devices {
		if a.devices[i].HardwareHash == hwHash {
			d := &a.devices[i]
			d.Route = dex.SelectRoute(d.Profile, d.DexProbe, d.Target.Transport, pref)
		}
	}
	w := a.window
	a.mu.Unlock()
	if w != nil {
		w.Update(func() {})
	}
}

func (a *App) activeStreamCountFor(hwHash string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if hwHash == "" {
		return len(a.sessions)
	}
	n := 0
	for _, sw := range a.sessions {
		sw.mu.Lock()
		match := sw.spec.DeviceKey == hwHash
		sw.mu.Unlock()
		if match {
			n++
		}
	}
	return n
}

// findLiveTransportFor finds an online ADB transport matching hardwareHash,
// preferring an alternate transport whose Serial differs from excludeSerial (`INV-02`).
func (a *App) findLiveTransportFor(ctx context.Context, client *adb.Client, hardwareHash, excludeSerial string) (adb.Device, bool) {
	if client == nil || hardwareHash == "" {
		return adb.Device{}, false
	}
	devs, err := client.Devices(ctx)
	if err != nil {
		return adb.Device{}, false
	}

	known := make(map[string]string)
	a.mu.Lock()
	for _, dev := range a.devices {
		for _, tr := range dev.Transports {
			known[tr.Serial] = dev.HardwareHash
		}
		if dev.Target.Serial != "" {
			known[dev.Target.Serial] = dev.HardwareHash
		}
	}
	a.mu.Unlock()

	var altCands []adb.Device
	var sameCands []adb.Device
	for _, d := range devs {
		if d.State != "device" {
			continue
		}
		match := d.DeviceHash == hardwareHash
		if !match {
			if hw, ok := known[d.Serial]; ok {
				match = hw == hardwareHash
			} else {
				pCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
				prof, pErr := client.ProbeDevice(pCtx, d.Serial)
				cancel()
				if pErr == nil && (prof.HardwareHash == hardwareHash || prof.DeviceHash == hardwareHash) {
					match = true
				}
			}
		}
		if !match {
			continue
		}
		if d.Serial != excludeSerial {
			altCands = append(altCands, d)
		} else {
			sameCands = append(sameCands, d)
		}
	}
	if len(altCands) > 0 {
		best := altCands[0]
		for _, c := range altCands[1:] {
			if adb.TransportPriority(c.Transport) < adb.TransportPriority(best.Transport) {
				best = c
			}
		}
		return best, true
	}
	if len(sameCands) > 0 {
		return sameCands[0], true
	}
	return adb.Device{}, false
}

// RefreshDevices scans wired USB, TLS Wi-Fi ADB, Emulator, and mDNS Wireless Debugging services,
// deduplicating multi-transport entries for the same physical device (`INV-02`).
func (a *App) RefreshDevices() {
	a.RefreshVMStatus()
	a.mu.Lock()
	if a.scanningDevices {
		a.mu.Unlock()
		return
	}
	a.scanningDevices = true
	a.mu.Unlock()
	a.update(func() {})

	go func() {
		client, store, err := a.ensureADBAndLeases()
		if err != nil {
			a.update(func() {
				a.scanningDevices = false
				a.deviceNotice = err.Error()
			})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()

		devs, dErr := client.Devices(ctx)
		svcs, _ := client.MDNSServices(ctx)

		a.rt.coreMu.Lock()
		if !a.rt.recoverRan && dErr == nil {
			a.rt.recoverRan = true
			a.recoverStaleLeases(ctx, client, store, devs)
		}
		a.rt.coreMu.Unlock()

		profiles := make(map[string]adb.DeviceProfile, len(devs))
		probeErrs := make(map[string]string, len(devs))
		for _, d := range devs {
			if d.State != "device" {
				continue
			}
			prof, pErr := client.ProbeDevice(ctx, d.Serial)
			if pErr != nil {
				probeErrs[d.Serial] = pErr.Error()
			} else {
				profiles[d.Serial] = prof
			}
		}

		unified := adb.MergeDevices(devs, profiles)
		inspected := make([]DiscoveredDevice, 0, len(unified))
		decodedIcons := make(map[string]*ui.Bitmap)
		for _, u := range unified {
			d := u.Primary
			item := DiscoveredDevice{
				HardwareHash: u.HardwareHash,
				Target:       d,
				Transports:   u.Transports,
				Profile:      u.Profile,
				Probed:       u.Probed,
				ProbeErr:     probeErrs[d.Serial],
			}
			if d.State == "device" && item.Probed {
				if strings.EqualFold(item.Profile.Manufacturer, "samsung") && d.Transport != adb.TransportEmulator {
					if rep, rErr := dex.PushAndProbe(ctx, client, d.Serial); rErr == nil {
						item.DexProbe = rep
						item.TruthDeX = dex.EvaluateTruth(item.Profile, rep, "AndroFuseDeX", dex.BackendADBLoopback)
					} else {
						item.TruthDeX = dex.EvaluateTruth(item.Profile, dex.ProbeReport{}, "", dex.BackendVirtualFallback)
					}
				} else {
					item.TruthDeX = dex.EvaluateTruth(item.Profile, dex.ProbeReport{}, "", dex.BackendVirtualFallback)
				}
				item.Route = dex.SelectRoute(item.Profile, item.DexProbe, d.Transport, a.dexRoutePref(u.HardwareHash))

				iconKey := u.HardwareHash
				if iconKey == "" {
					iconKey = d.DeviceHash
				}
				iconDir := filepath.Join(a.stateDir, "icon-cache", iconKey)
				if a.stateDir == "" {
					iconDir = filepath.Join(os.TempDir(), "androfuse-icon-cache", iconKey)
				}
				apps, aErr := dex.ListAppsWithIcons(ctx, client, d.Serial, iconDir)
				if aErr != nil {
					apps, aErr = client.ListLaunchableApps(ctx, d.Serial)
				}
				if aErr == nil {
					item.Apps = apps
					for _, ap := range apps {
						if ap.IconPath == "" || decodedIcons[ap.IconPath] != nil {
							continue
						}
						if raw, rErr := os.ReadFile(ap.IconPath); rErr == nil {
							if bmp, bErr := ui.DecodeBitmap(raw); bErr == nil {
								decodedIcons[ap.IconPath] = bmp
							}
						}
					}
				}
			}
			inspected = append(inspected, item)
		}

		notice := ""
		if dErr != nil {
			notice = dErr.Error()
		}
		a.update(func() {
			a.scanningDevices = false
			a.devices = inspected
			a.mdnsServices = svcs
			a.deviceNotice = notice
			if a.appIcons == nil {
				a.appIcons = make(map[string]*ui.Bitmap)
			}
			for path, bmp := range decodedIcons {
				a.appIcons[path] = bmp
			}
		})
	}()
}

// ConnectWirelessTLS connects to a discovered mDNS or manual TLS Wireless ADB host:port (`INV-07`).
func (a *App) ConnectWirelessTLS(addr string) {
	a.update(func() { a.deviceNotice = "" })
	go func() {
		client, _, err := a.ensureADBAndLeases()
		if err != nil {
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := client.ConnectTLS(ctx, addr, false); err != nil {
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.RefreshDevices()
	}()
}

// PairWirelessTLS pairs with a 6-digit Android Wireless Debugging code (`INV-07`).
func (a *App) PairWirelessTLS(addr, code string) {
	a.update(func() { a.deviceNotice = "" })
	go func() {
		client, _, err := a.ensureADBAndLeases()
		if err != nil {
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if _, err := client.PairTLS(ctx, addr, code); err != nil {
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.update(func() {
			a.pairCode = ""
			a.deviceNotice = L.PairSuccessHint()
		})
		a.RefreshDevices()
	}()
}

// DisconnectWireless detaches an active wireless ADB target.
func (a *App) DisconnectWireless(serial string) {
	go func() {
		client, _, err := a.ensureADBAndLeases()
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_ = client.Disconnect(ctx, serial)
		a.RefreshDevices()
	}()
}

// LaunchDeX opens an interactive native Samsung DeX window (or virtual desktop fallback if requested/non-Samsung).
func (a *App) LaunchDeX(d DiscoveredDevice, forceFallback bool) {
	devKey := d.HardwareHash
	if devKey == "" {
		devKey = d.Target.DeviceHash
	}
	if used, limit, atLimit, _ := EncoderHeadroom(d.Profile, a.activeStreamCountFor(devKey)); atLimit {
		a.update(func() { a.deviceNotice = L.EncoderLimitReached(used, limit) })
		return
	}
	pref := a.dexRoutePref(devKey)
	route := dex.SelectRoute(d.Profile, d.DexProbe, d.Target.Transport, pref)
	useNative := !forceFallback && route.Backend == dex.BackendADBLoopback && route.IsGenuineDeX

	a.update(func() { a.deviceNotice = "" })
	go func() {
		a.rt.coreMu.Lock()
		lib, lErr := a.ensureCoreLocked()
		a.rt.coreMu.Unlock()
		if lErr != nil {
			a.update(func() { a.deviceNotice = lErr.Error() })
			return
		}
		client, store, aErr := a.ensureADBAndLeases()
		if aErr != nil {
			a.update(func() { a.deviceNotice = aErr.Error() })
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		tLabel := transportLabel(d.Target.Transport)
		if useNative {
			dexSess, err := dex.StartNativeLoopback(ctx, client, store, d.Target.Serial, 1920, 1080, 160, 30)
			if err != nil {
				a.update(func() { a.deviceNotice = err.Error() })
				return
			}
			ready := dexSess.Ready()
			sid := newSessionID("dex")
			sw := newSessionWindow(a, SessionSpec{
				ID:           sid,
				DeviceKey:    devKey,
				Serial:       d.Target.Serial,
				Title:        fmt.Sprintf("%s · Samsung DeX (%s)", d.Profile.Model, tLabel),
				Mode:         SessionModeDeX,
				Transport:    tLabel,
				BackendLabel: route.Label,
				TargetSpec:   "1920×1080 @ 60 fps",
				IsDeX:        true,
				AllowResize:  true,
			})
			ap, _ := lib.OpenAudioPlayer(48000, 2)
			sw.attachAudio(ap)
			var vSess *scrcpy.Session
			vSess, err = scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
				SessionID:     sid + "-vid",
				Serial:        d.Target.Serial,
				DisplayID:     ready.DisplayID,
				MaxFPS:        60,
				VideoBitRate:  12_000_000,
				EnableControl: true,
				Audio:         true,
				InitialWidth:  uint32(ready.Width),
				InitialHeight: uint32(ready.Height),
				OnFrame: func(f core.NV12Frame) {
					if vSess != nil {
						st := vSess.Stats()
						sw.PushNV12(f, st.MeasuredFPS, st.AvgDecodeUs)
					} else {
						sw.PushNV12(f, 0, int64(f.DecodeUs))
					}
				},
				OnClipboard: sw.OnDeviceClipboard,
				OnAudioPCM:  sw.PushAudioPCM,
			})
			if err != nil {
				if ap != nil {
					ap.Close()
				}
				dexSess.Stop()
				a.update(func() { a.deviceNotice = err.Error() })
				return
			}
			a.addSession(sw)
			a.runOnMain(func() { sw.attachScrcpy(vSess, dexSess, sessionDefW, sessionDefH) })
			return
		}

		// Honest Virtual Desktop Compatibility Mode (non-Samsung, Emulator, or explicit fallback, INV-06).
		sid := newSessionID("vdesk")
		fallbackVerdict := dex.EvaluateTruth(d.Profile, d.DexProbe, "", dex.BackendVirtualFallback)
		sw := newSessionWindow(a, SessionSpec{
			ID:           sid,
			DeviceKey:    devKey,
			Serial:       d.Target.Serial,
			Title:        fmt.Sprintf("%s · %s (%s)", d.Profile.Model, fallbackVerdict.Label, tLabel),
			Mode:         SessionModeDeXFallback,
			Transport:    tLabel,
			BackendLabel: fallbackVerdict.Label,
			TargetSpec:   "1600×900 @ 60 fps",
			IsDeX:        true,
			AllowResize:  false,
		})
		ap, _ := lib.OpenAudioPlayer(48000, 2)
		sw.attachAudio(ap)
		var vSess *scrcpy.Session
		vSess, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
			SessionID:     sid,
			Serial:        d.Target.Serial,
			NewDisplay:    "1600x900/160",
			FlexDisplay:   true,
			MaxFPS:        60,
			VideoBitRate:  8_000_000,
			EnableControl: true,
			Audio:         true,
			InitialWidth:  1600,
			InitialHeight: 900,
			OnFrame: func(f core.NV12Frame) {
				if vSess != nil {
					st := vSess.Stats()
					sw.PushNV12(f, st.MeasuredFPS, st.AvgDecodeUs)
				} else {
					sw.PushNV12(f, 0, int64(f.DecodeUs))
				}
			},
			OnClipboard: sw.OnDeviceClipboard,
			OnAudioPCM:  sw.PushAudioPCM,
		})
		if err != nil {
			if ap != nil {
				ap.Close()
			}
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, sessionDefW, sessionDefH) })
	}()
}

// LaunchMirror opens an interactive Phone Mirror window for Display 0.
func (a *App) LaunchMirror(d DiscoveredDevice) {
	devKey := d.HardwareHash
	if devKey == "" {
		devKey = d.Target.DeviceHash
	}
	if used, limit, atLimit, _ := EncoderHeadroom(d.Profile, a.activeStreamCountFor(devKey)); atLimit {
		a.update(func() { a.deviceNotice = L.EncoderLimitReached(used, limit) })
		return
	}
	a.update(func() { a.deviceNotice = "" })
	go func() {
		a.rt.coreMu.Lock()
		lib, lErr := a.ensureCoreLocked()
		a.rt.coreMu.Unlock()
		if lErr != nil {
			a.update(func() { a.deviceNotice = lErr.Error() })
			return
		}
		client, store, aErr := a.ensureADBAndLeases()
		if aErr != nil {
			a.update(func() { a.deviceNotice = aErr.Error() })
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		tLabel := transportLabel(d.Target.Transport)
		sid := newSessionID("mirror")
		sw := newSessionWindow(a, SessionSpec{
			ID:           sid,
			DeviceKey:    devKey,
			Serial:       d.Target.Serial,
			Title:        fmt.Sprintf("%s · %s (%s)", d.Profile.Model, L.ModeMirrorTitle(), tLabel),
			Mode:         SessionModeMirror,
			Transport:    tLabel,
			BackendLabel: L.ModeMirrorTitle(),
			TargetSpec:   "Display 0 · 60 fps",
			ScreenOffCap: true,
		})
		ap, _ := lib.OpenAudioPlayer(48000, 2)
		sw.attachAudio(ap)
		var vSess *scrcpy.Session
		vSess, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
			SessionID:     sid,
			Serial:        d.Target.Serial,
			DisplayID:     0,
			MaxSize:       1280,
			MaxFPS:        60,
			VideoBitRate:  8_000_000,
			EnableControl: true,
			Audio:         true,
			InitialWidth:  1080,
			InitialHeight: 1920,
			OnFrame: func(f core.NV12Frame) {
				if vSess != nil {
					st := vSess.Stats()
					sw.PushNV12(f, st.MeasuredFPS, st.AvgDecodeUs)
				} else {
					sw.PushNV12(f, 0, int64(f.DecodeUs))
				}
			},
			OnClipboard: sw.OnDeviceClipboard,
			OnAudioPCM:  sw.PushAudioPCM,
		})
		if err != nil {
			if ap != nil {
				ap.Close()
			}
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, mirrorDefW, mirrorDefH) })
	}()
}

// LaunchAppWindow opens an isolated chromeless flex-display native window running app on the target device.
func (a *App) LaunchAppWindow(d DiscoveredDevice, app adb.AppEntry) {
	pkg := strings.TrimSpace(app.Package)
	if pkg == "" {
		return
	}
	devKey := d.HardwareHash
	if devKey == "" {
		devKey = d.Target.DeviceHash
	}
	if used, limit, atLimit, _ := EncoderHeadroom(d.Profile, a.activeStreamCountFor(devKey)); atLimit {
		a.update(func() { a.deviceNotice = L.EncoderLimitReached(used, limit) })
		return
	}
	label := strings.TrimSpace(app.Label)
	if label == "" {
		label = pkg
	}
	icon := a.appIconFor(app.IconPath)
	startArg := pkg
	if !strings.HasPrefix(startArg, "+") {
		startArg = "+" + startArg
	}
	a.update(func() { a.deviceNotice = "" })
	go func() {
		a.rt.coreMu.Lock()
		lib, lErr := a.ensureCoreLocked()
		a.rt.coreMu.Unlock()
		if lErr != nil {
			a.update(func() { a.deviceNotice = lErr.Error() })
			return
		}
		client, store, aErr := a.ensureADBAndLeases()
		if aErr != nil {
			a.update(func() { a.deviceNotice = aErr.Error() })
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		tLabel := transportLabel(d.Target.Transport)
		sid := newSessionID("app")
		sw := newSessionWindow(a, SessionSpec{
			ID:           sid,
			DeviceKey:    devKey,
			Serial:       d.Target.Serial,
			Title:        label,
			Mode:         SessionModeApp,
			Transport:    tLabel,
			BackendLabel: label,
			TargetSpec:   fmt.Sprintf("%d×%d flex · 60 fps", appWinInitW, appWinInitH),
			AllowResize:  true,
			AppIcon:      icon,
			AppPackage:   pkg,
		})
		ap, _ := lib.OpenAudioPlayer(48000, 2)
		sw.attachAudio(ap)
		var vSess *scrcpy.Session
		vSess, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
			SessionID:             sid,
			Serial:                d.Target.Serial,
			NewDisplay:            fmt.Sprintf("%dx%d/%d", appWinInitW, appWinInitH, appWinInitDPI),
			FlexDisplay:           true,
			HideSystemDecorations: true,
			StartAppPackage:       startArg,
			MaxFPS:                60,
			VideoBitRate:          8_000_000,
			EnableControl:         true,
			Audio:                 true,
			InitialWidth:          uint32(appWinInitW),
			InitialHeight:         uint32(appWinInitH),
			OnFrame: func(f core.NV12Frame) {
				if vSess != nil {
					st := vSess.Stats()
					sw.PushNV12(f, st.MeasuredFPS, st.AvgDecodeUs)
				} else {
					sw.PushNV12(f, 0, int64(f.DecodeUs))
				}
			},
			OnClipboard: sw.OnDeviceClipboard,
			OnAudioPCM:  sw.PushAudioPCM,
		})
		if err != nil {
			if ap != nil {
				ap.Close()
			}
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, appWinPortraitW, appWinPortraitH) })
	}()
}

// LaunchMiracastDeX starts the Windows Wi-Fi Direct / Miracast DeX receiver on port 7236.
func (a *App) LaunchMiracastDeX() {
	a.update(func() { a.deviceNotice = "" })
	go func() {
		a.rt.coreMu.Lock()
		lib, lErr := a.ensureCoreLocked()
		a.rt.coreMu.Unlock()
		if lErr != nil {
			a.update(func() { a.deviceNotice = lErr.Error() })
			return
		}
		sid := newSessionID("miracast")
		sw := newSessionWindow(a, SessionSpec{
			ID:           sid,
			Title:        L.MiracastWindowTitle(),
			Mode:         SessionModeMiracastDeX,
			Transport:    "Miracast WFD",
			BackendLabel: "Samsung Wireless DeX (Miracast WFD)",
			TargetSpec:   "1920×1080 @ 30/60 fps",
			IsDeX:        true,
			IsMiracast:   true,
		})
		var mSess *dex.MiracastSession
		var err error
		mSess, err = dex.StartMiracast(lib, 7236, func(f core.NV12Frame) {
			if mSess != nil {
				st := mSess.Stats()
				sw.PushNV12(f, st.MeasuredFPS, st.AvgDecodeUs)
			} else {
				sw.PushNV12(f, 0, int64(f.DecodeUs))
			}
		}, func(state uint32) {
			switch state {
			case core.MiracastNegotiating:
				sw.setStatusNote(L.MiracastNegotiatingNote())
			case core.MiracastListening:
				sw.setStatusNote(L.MiracastWaitingNote())
			}
		})
		if err != nil {
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachMiracast(mSess) })
	}()
}

func (a *App) addSession(sw *SessionWindow) {
	a.mu.Lock()
	a.sessions[sw.spec.ID] = sw
	w := a.window
	a.mu.Unlock()
	if w != nil {
		w.Update(func() {})
	}
}

func (a *App) removeSession(id string) {
	a.mu.Lock()
	delete(a.sessions, id)
	w := a.window
	a.mu.Unlock()
	if w != nil {
		w.Update(func() {})
	}
}

func (a *App) runOnMain(fn func()) {
	a.mu.Lock()
	w := a.window
	a.mu.Unlock()
	if w != nil {
		w.Update(fn)
	}
}

// CloseAllSessions tears down every active session window and releases leases (INV-03).
func (a *App) CloseAllSessions() {
	a.mu.Lock()
	list := make([]*SessionWindow, 0, len(a.sessions))
	for _, s := range a.sessions {
		list = append(list, s)
	}
	a.mu.Unlock()
	for _, s := range list {
		s.Close()
	}
	a.rt.coreMu.Lock()
	if a.rt.coreLib != nil {
		a.rt.coreLib.Stop()
		a.rt.coreLib = nil
	}
	vmMgr := a.rt.vmMgr
	compClient := a.rt.compClient
	a.rt.compClient = nil
	aHost := a.rt.androidHost
	mcpSrv := a.rt.mcpServer
	meshMgr := a.rt.meshMgr
	micCancel := a.rt.micUplinkCancel
	a.rt.micUplinkCancel = nil
	if a.rt.micUplink != nil {
		a.rt.micUplink.Stop()
		a.rt.micUplink = nil
	}
	a.rt.coreMu.Unlock()
	if micCancel != nil {
		micCancel()
	}
	if meshMgr != nil {
		_ = meshMgr.Stop()
		a.mu.Lock()
		a.meshStatus = meshMgr.Status()
		a.mu.Unlock()
	}
	if mcpSrv != nil {
		_ = mcpSrv.EmergencyStop()
		a.mu.Lock()
		a.mcpStatus = mcpSrv.Status()
		a.mu.Unlock()
	}
	if compClient != nil {
		_ = compClient.Close()
	}
	if aHost != nil {
		_ = aHost.StopServer()
	}
	if vmMgr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		_ = vmMgr.Stop(ctx)
		cancel()
	}
}

func (a *App) ensureMCPServerLocked() *mcp.Server {
	if a.rt.mcpServer != nil {
		return a.rt.mcpServer
	}
	a.rt.mcpServer = mcp.NewServer(mcp.Handlers{
		ListDevices: func(context.Context) (any, error) {
			a.mu.Lock()
			devs := make([]map[string]any, 0, len(a.devices))
			for _, d := range a.devices {
				devs = append(devs, map[string]any{
					"hardwareHash": d.HardwareHash,
					"maskedId":     d.Target.MaskedID,
					"model":        d.Target.Model,
					"transport":    d.Target.Transport,
					"genuineDeX":   d.TruthDeX.IsGenuineDeX,
				})
			}
			a.mu.Unlock()
			return devs, nil
		},
		RunDiagnostics: func(context.Context) (any, error) {
			return RunDiagnostics(a.stateDir, ""), nil
		},
		LaunchApp: func(_ context.Context, deviceID, packageName string) (any, error) {
			target, ok := a.findDeviceForMCP(deviceID)
			if !ok {
				return nil, fmt.Errorf("device not found")
			}
			a.LaunchAppWindow(target, adb.AppEntry{Package: packageName, Label: packageName})
			return map[string]any{"requested": packageName}, nil
		},
	}, 30*time.Minute)
	return a.rt.mcpServer
}

func (a *App) findDeviceForMCP(id string) (DiscoveredDevice, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range a.devices {
		if d.HardwareHash == id || d.Target.MaskedID == id || d.Target.Serial == id {
			return d, true
		}
	}
	return DiscoveredDevice{}, false
}

// EnableMCP starts or updates the opt-in loopback MCP server with explicit allowlist settings.
// Side effects: Opens 127.0.0.1 HTTP listener with bounded 30-minute token and updates UI state.
func (a *App) EnableMCP(allowLaunch bool) {
	a.rt.coreMu.Lock()
	srv := a.ensureMCPServerLocked()
	a.rt.coreMu.Unlock()

	st, err := srv.Enable(mcp.Allowlist{
		ListDevices:    true,
		RunDiagnostics: true,
		LaunchApp:      allowLaunch,
	})
	a.mu.Lock()
	a.mcpAllowLaunch = allowLaunch
	if err != nil {
		a.mcpNotice = err.Error()
		a.mcpStatus = srv.Status()
	} else {
		a.mcpNotice = ""
		a.mcpStatus = st
	}
	a.mu.Unlock()
	a.runOnMain(func() {})
}

// EmergencyStopMCP revokes the active local MCP token and closes the loopback port immediately.
// Side effects: Stops local MCP serving and refreshes the Settings UI.
func (a *App) EmergencyStopMCP() {
	a.rt.coreMu.Lock()
	srv := a.rt.mcpServer
	a.rt.coreMu.Unlock()
	if srv == nil {
		return
	}
	err := srv.EmergencyStop()
	a.mu.Lock()
	if err != nil {
		a.mcpNotice = err.Error()
	} else {
		a.mcpNotice = ""
	}
	a.mcpStatus = srv.Status()
	a.mu.Unlock()
	a.runOnMain(func() {})
}

// LaunchPhoneCamera opens a scrcpy camera+mic stream paired with a loopback MJPEG bridge (`M5`).
// Side effects: Starts avbridge.MJPEGServer, opens a SessionWindow, and clears state on close.
func (a *App) LaunchPhoneCamera(d DiscoveredDevice, facing string) {
	devKey := d.HardwareHash
	if devKey == "" {
		devKey = d.Target.DeviceHash
	}
	if used, limit, atLimit, _ := EncoderHeadroom(d.Profile, a.activeStreamCountFor(devKey)); atLimit {
		a.update(func() { a.deviceNotice = L.EncoderLimitReached(used, limit) })
		return
	}
	if facing != "front" && facing != "back" {
		facing = "back"
	}
	a.update(func() { a.deviceNotice = "" })
	go func() {
		a.rt.coreMu.Lock()
		lib, lErr := a.ensureCoreLocked()
		a.rt.coreMu.Unlock()
		if lErr != nil {
			a.update(func() { a.deviceNotice = lErr.Error() })
			return
		}
		client, store, aErr := a.ensureADBAndLeases()
		if aErr != nil {
			a.update(func() { a.deviceNotice = aErr.Error() })
			return
		}

		mjpegSrv := avbridge.NewMJPEGServer()
		mjpegURL, mErr := mjpegSrv.Start()
		if mErr != nil {
			a.update(func() { a.deviceNotice = mErr.Error() })
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		tLabel := transportLabel(d.Target.Transport)
		sid := newSessionID("cam")
		sw := newSessionWindow(a, SessionSpec{
			ID:           sid,
			DeviceKey:    devKey,
			Serial:       d.Target.Serial,
			Title:        fmt.Sprintf("%s · %s (%s)", d.Profile.Model, L.ActionLaunchCamera(), facing),
			Mode:         SessionModeCamera,
			Transport:    tLabel,
			BackendLabel: "scrcpy camera + loopback MJPEG",
			TargetSpec:   "1280×720 @ 30 fps (" + facing + ")",
			OnClose: func() {
				_ = mjpegSrv.Close()
				a.update(func() {
					if a.cameraBridgeURL == mjpegURL {
						a.cameraBridgeURL = ""
					}
				})
			},
		})
		ap, _ := lib.OpenAudioPlayer(48000, 2)
		sw.attachAudio(ap)

		var vSess *scrcpy.Session
		vSess, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
			SessionID:     sid,
			Serial:        d.Target.Serial,
			VideoSource:   "camera",
			CameraFacing:  facing,
			CameraSize:    "1280x720",
			CameraFPS:     30,
			VideoBitRate:  6_000_000,
			Audio:         true,
			AudioSource:   "mic",
			InitialWidth:  1280,
			InitialHeight: 720,
			OnFrame: func(f core.NV12Frame) {
				mjpegSrv.PushNV12(f.Width, f.Height, f.YStride, f.UVStride, f.Y, f.UV)
				if vSess != nil {
					st := vSess.Stats()
					sw.PushNV12(f, st.MeasuredFPS, st.AvgDecodeUs)
				} else {
					sw.PushNV12(f, 0, int64(f.DecodeUs))
				}
			},
			OnAudioPCM: sw.PushAudioPCM,
		})
		if err != nil {
			_ = mjpegSrv.Close()
			if ap != nil {
				ap.Close()
			}
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.update(func() { a.cameraBridgeURL = mjpegURL })
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, sessionDefW, sessionDefH) })
	}()
}

// StartMesh starts the optional default-disabled userspace EasyTier (`--no-tun`) supervisor (`INV-07`).
func (a *App) StartMesh(netName, secret, peer string) {
	a.rt.coreMu.Lock()
	if a.rt.meshMgr == nil {
		a.rt.meshMgr = mesh.NewManager()
	}
	mgr := a.rt.meshMgr
	a.rt.coreMu.Unlock()

	cfg := mesh.Config{
		Enabled:       true,
		NetworkName:   strings.TrimSpace(netName),
		NetworkSecret: strings.TrimSpace(secret),
		DeviceName:    "androfuse-desktop",
	}
	if p := strings.TrimSpace(peer); p != "" {
		cfg.Peers = []string{p}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := mgr.Start(ctx, cfg)
	cancel()

	a.mu.Lock()
	a.meshStatus = mgr.Status()
	if err != nil {
		a.meshNotice = err.Error()
	} else {
		a.meshNotice = ""
	}
	a.mu.Unlock()
	a.runOnMain(func() {})
}

// StopMesh stops the userspace EasyTier (`--no-tun`) process cleanly.
func (a *App) StopMesh() {
	a.rt.coreMu.Lock()
	mgr := a.rt.meshMgr
	a.rt.coreMu.Unlock()
	if mgr == nil {
		return
	}
	err := mgr.Stop()
	a.mu.Lock()
	a.meshStatus = mgr.Status()
	if err != nil {
		a.meshNotice = err.Error()
	} else {
		a.meshNotice = ""
	}
	a.mu.Unlock()
	a.runOnMain(func() {})
}

// StartCompanionMicUplink opens Windows WASAPI mic capture and streams 20ms s16le PCM to Android Companion (`INV-09`).
func (a *App) StartCompanionMicUplink() {
	a.StopCompanionMicUplink()
	go func() {
		a.rt.coreMu.Lock()
		lib, lErr := a.ensureCoreLocked()
		cl := a.rt.compClient
		a.rt.coreMu.Unlock()
		if lErr != nil {
			a.update(func() { a.micUplinkNotice = lErr.Error() })
			return
		}
		if cl == nil {
			a.update(func() { a.micUplinkNotice = "companion not connected" })
			return
		}
		cap, err := lib.OpenMicCapture(48000, 1)
		if err != nil {
			a.update(func() { a.micUplinkNotice = err.Error() })
			return
		}
		bridge := avbridge.NewUplinkBridge(cl)
		bridge.Start()
		ctx, cancel := context.WithCancel(context.Background())

		a.rt.coreMu.Lock()
		a.rt.micUplink = bridge
		a.rt.micUplinkCancel = cancel
		a.rt.coreMu.Unlock()

		a.update(func() {
			a.micUplinkActive = true
			a.micUplinkNotice = ""
		})
		streamErr := bridge.StreamMic(ctx, 48000, 1, cap)
		cap.Close()
		bridge.Stop()

		a.rt.coreMu.Lock()
		if a.rt.micUplink == bridge {
			a.rt.micUplink = nil
			a.rt.micUplinkCancel = nil
		}
		a.rt.coreMu.Unlock()

		a.update(func() {
			a.micUplinkActive = false
			if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
				a.micUplinkNotice = streamErr.Error()
			}
		})
	}()
}

// StopCompanionMicUplink stops the active PC microphone -> Android Companion uplink stream.
func (a *App) StopCompanionMicUplink() {
	a.rt.coreMu.Lock()
	cancel := a.rt.micUplinkCancel
	bridge := a.rt.micUplink
	a.rt.micUplinkCancel = nil
	a.rt.micUplink = nil
	a.rt.coreMu.Unlock()
	if bridge != nil {
		bridge.Stop()
	}
	if cancel != nil {
		cancel()
	}
	a.update(func() { a.micUplinkActive = false })
}

func transportLabel(t adb.Transport) string {
	switch t {
	case adb.TransportUSB:
		return "USB ADB"
	case adb.TransportTLSWiFi:
		return "TLS Wi-Fi"
	case adb.TransportEmulator:
		return "Emulator"
	case adb.TransportTCP:
		return "TCP"
	default:
		return string(t)
	}
}

func newSessionID(prefix string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}
