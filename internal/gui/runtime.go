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
	"runtime"
	"slices"

	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
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
	// RememberedName is the remembered phone's own name when it could not be probed this time.
	RememberedName string
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
	meshCfgs        map[string]mesh.Config        // the running networks by instance, with secrets and forwards, for handing one to a paired phone
	tunnels         map[string]context.CancelFunc // Wireless debugging tunnels through each phone's Companion, by HardwareHash
	tunnelTried     map[string]time.Time          // last automatic tunnel attempt per phone
	tunnelAddrs     map[string]string             // each tunnel's loopback adb address, by HardwareHash
	portScanTried   map[string]time.Time          // last Wireless debugging port-range scan per phone
	maintTried      map[string]time.Time          // last automatic APK/daemon self-heal attempt per phone
	fetchingApps    map[string]bool               // active Companion apps.list RPCs by HardwareHash
	localAPKPath    string
	localAPKHash    string
	synced          map[string]bool // phones whose Companion was checked since they came online (syncCompanions)
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
		a.rt.androidHost.SetLanguageHooks(a.LanguagePref, func(lang string) {
			a.mu.Lock()
			a.prefs.Language = lang
			a.savePrefs()
			a.mu.Unlock()
			mygo.App.SetLocale(lang)
		})
		if runtime.GOOS == "android" {
			go a.rt.androidHost.AutoStart()
		}
	}
	h := a.rt.androidHost
	a.rt.coreMu.Unlock()
	return h
}

// HandleURL handles `androfuse://` deep links. On Android, `androfuse://pair` (opened over adb by
// a desktop installing the Companion) starts the Companion service so that desktop can request
// pairing; the link carries no secret, and pairing still needs the phone user's consent.
func (a *App) HandleURL(u string) {
	if runtime.GOOS != "android" || !strings.HasPrefix(u, "androfuse://pair") {
		return
	}
	go func() { _ = a.ensureAndroidHost().StartServer() }()
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
			a.update(func() { a.compNotice = a.failed(L.FailCompanion(), err) })
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
			ap := adb.AppEntry{Package: res.Package, Label: res.Package, Resizeable: true}
			if cached, ok := a.findCachedApp(target.HardwareHash, res.Package); ok {
				ap = cached
			}
			a.LaunchAppWindow(*target, ap, OpenDefault)
		}
	}()
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
			a.update(func() { a.compNotice = a.failed(L.FailCompanion(), err) })
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
			a.update(func() { a.deviceNotice = a.failed(L.FailVM(), err) })
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
			a.update(func() { a.deviceNotice = a.failed(L.FailVM(), err) })
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
			a.update(func() { a.deviceNotice = a.failed(L.FailVM(), err) })
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
			a.update(func() { a.deviceNotice = a.failed(L.FailVM(), err) })
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

// InstallADB downloads official Google platform-tools into adb.DefaultPlatformToolsDir() when adb
// is missing on the machine, reporting progress to Home and Settings › About and updates.
// Side effects: HTTPS GET to dl.google.com, refreshes diagnostics and devices on completion.
func (a *App) InstallADB() {
	a.mu.Lock()
	if a.adbInstalling {
		a.mu.Unlock()
		return
	}
	a.adbInstalling = true
	a.adbInstallPct = 0
	a.adbInstallErr = ""
	a.mu.Unlock()
	a.update(func() {})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		cl, err := adb.InstallPlatformTools(ctx, adb.DefaultPlatformToolsDir(), func(pct int) {
			a.update(func() { a.adbInstallPct = pct })
		})
		if err != nil {
			a.update(func() {
				a.adbInstalling = false
				a.adbMissing = true
				a.adbInstallErr = a.failed(L.ADBDownloadFailedTitle(), err)
			})
			return
		}
		a.rt.coreMu.Lock()
		a.rt.adbClient = cl
		a.rt.coreMu.Unlock()
		a.update(func() {
			a.adbInstalling = false
			a.adbInstallPct = 100
			a.adbMissing = false
			a.adbInstallErr = ""
			a.deviceNotice = ""
		})
		a.RefreshDiagnostics()
		a.RefreshDevices()
	}()
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

// connectWirelessFor connects the TLS Wireless Debugging endpoint that mDNS advertises for
// hardwareHash, so a session started over USB can hand off even when Wi-Fi ADB was never
// connected (`INV-02`).
// Returns: true when an endpoint for this phone accepted the connection.
// Notes: only the `adb-<ro.serialno>-…` instance of this phone is used, never other devices on
// the LAN, and only TLS endpoints (`INV-07`).
func (a *App) connectWirelessFor(ctx context.Context, client *adb.Client, hardwareHash string) bool {
	if client == nil || hardwareHash == "" {
		return false
	}
	svcs, err := client.MDNSServices(ctx)
	if err != nil {
		return false
	}
	for _, s := range svcs {
		if s.CanConnect && s.HardwareHash == hardwareHash {
			if _, err := client.ConnectTLS(ctx, s.Addr, false); err == nil {
				return true
			}
		}
	}
	return false
}

// RefreshDevices scans wired USB, TLS Wi-Fi ADB, Emulator, and mDNS Wireless Debugging services,
// deduplicating multi-transport entries for the same physical device (`INV-02`).
func (a *App) RefreshDevices() {
	if a.isInstallingUpdate() {
		return
	}
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
			if errors.Is(err, adb.ErrNotFound) {
				shouldInstall := false
				a.update(func() {
					a.scanningDevices = false
					a.adbMissing = true
					a.deviceNotice = ""
					if !a.adbInstalling && a.adbInstallErr == "" {
						shouldInstall = true
					}
				})
				if shouldInstall {
					a.InstallADB()
				}
				return
			}
			a.update(func() {
				a.scanningDevices = false
				a.deviceNotice = a.failed(L.FailDevices(), err)
			})
			return
		}
		a.update(func() {
			a.adbMissing = false
			a.adbInstallErr = ""
		})
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()

		devs, dErr := client.Devices(ctx)
		svcs, _ := client.MDNSServices(ctx)
		if dErr == nil {
			rctx, rcancel := context.WithTimeout(ctx, 6*time.Second)
			reconn := a.reconnectRemembered(rctx, client, a.onlineHashes(devs), svcs)
			rcancel()
			if reconn {
				devs, dErr = client.Devices(ctx)
			}
		}
		devs = dropDeadWireless(ctx, client, devs)

		a.rt.coreMu.Lock()
		if !a.rt.recoverRan && dErr == nil {
			a.rt.recoverRan = true
			a.recoverStaleLeases(ctx, client, store, devs)
		}
		a.rt.coreMu.Unlock()

		profiles := make(map[string]adb.DeviceProfile, len(devs))
		probeErrs := make(map[string]string, len(devs))
		dead := map[string]bool{}
		for _, d := range devs {
			if d.State != "device" {
				continue
			}
			prof, pErr := client.ProbeDevice(ctx, d.Serial)
			if pErr != nil && ctx.Err() == nil {
				prof, pErr = client.ProbeDevice(ctx, d.Serial) // a slow link across networks: once more
			}
			if pErr != nil && strings.Contains(d.Serial, ":") && d.Transport != adb.TransportEmulator {
				// A wireless connection that does not answer twice is one adb has not noticed is
				// gone (the phone left Wi-Fi): close it rather than list it as a phone.
				_ = client.Disconnect(ctx, d.Serial)
				dead[d.Serial] = true
				continue
			}
			if pErr != nil {
				probeErrs[d.Serial] = pErr.Error()
			} else {
				profiles[d.Serial] = prof
			}
		}

		devs = slices.DeleteFunc(devs, func(d adb.Device) bool { return dead[d.Serial] })
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
			if !item.Probed {
				if hash, name := a.rememberedAt(d.Serial); hash != "" {
					item.HardwareHash, item.RememberedName = hash, name
				}
			}
			if d.State == "device" && item.Probed {
				if strings.EqualFold(item.Profile.Manufacturer, "samsung") && d.Transport != adb.TransportEmulator {
					pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
					rep, rErr := dex.PushAndProbe(pctx, client, d.Serial)
					pcancel()
					if rErr == nil {
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
				iconDir := a.deviceIconDir(iconKey)
				apps, aErr := dex.ListAppsWithIcons(ctx, client, d.Serial, iconDir)
				if aErr != nil {
					apps, aErr = client.ListLaunchableApps(ctx, d.Serial)
				}
				if aErr == nil {
					item.Apps = apps
					a.saveDeviceApps(iconKey, apps)
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

		a.rememberDevices(inspected, time.Now())
		notice := ""
		if dErr != nil {
			notice = dErr.Error()
		}
		a.update(func() {
			a.scanningDevices = false
			a.devices = inspected
			// A phone that is online again drops the last failure its card showed.
			for _, d := range inspected {
				if w, ok := a.cardWork[d.HardwareHash]; ok && d.Target.State == "device" && w.Busy == "" {
					delete(a.cardWork, d.HardwareHash)
				}
			}
			a.mdnsServices = svcs
			a.deviceNotice = notice
			if a.appIcons == nil {
				a.appIcons = make(map[string]*ui.Bitmap)
			}
			for path, bmp := range decodedIcons {
				a.appIcons[path] = bmp
			}
		})
		a.openPending(inspected)
		a.syncCompanions(inspected)
		a.refreshTray()
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
func (a *App) LaunchDeX(d DiscoveredDevice, forceFallback bool, open OpenIn) {
	sp := a.streamPrefs()
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
	wirelessADB := d.Target.Transport != adb.TransportUSB && d.Target.Transport != adb.TransportEmulator
	if wirelessADB && a.prefersCompanion() {
		if r, ok := a.RememberedFor(devKey); ok && r.Companion != nil {
			a.LaunchDaemonDesktop(devKey, open)
			return
		}
	}

	a.update(func() { a.deviceNotice = "" })
	go func() {
		a.rt.coreMu.Lock()
		lib, lErr := a.ensureCoreLocked()
		a.rt.coreMu.Unlock()
		if lErr != nil {
			a.update(func() { a.deviceNotice = a.failed(L.FailEngine(), lErr) })
			return
		}
		client, store, aErr := a.ensureADBAndLeases()
		if aErr != nil {
			a.update(func() { a.deviceNotice = a.failed(L.FailEngine(), aErr) })
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		tLabel := transportLabel(d.Target.Transport)
		wirelessADB := d.Target.Transport != adb.TransportUSB && d.Target.Transport != adb.TransportEmulator
		if useNative {
			dexSess, err := dex.StartNativeLoopback(ctx, client, store, d.Target.Serial, 1920, 1080, 160, 30)
			if err != nil {
				if wirelessADB && a.autoSwitchOn() && a.fallbackToCompanion(devKey, d.Target.Serial, true, open) {
					return
				}
				a.update(func() { a.deviceNotice = a.failed(L.FailDeX(), err) })
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
				Open:         open,
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
				MaxFPS:        sp.fps,
				CodecID:       sp.codec,
				VideoBitRate:  12_000_000,
				EnableControl: true,
				Audio:         sp.audio,
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
				if wirelessADB && a.autoSwitchOn() && a.fallbackToCompanion(devKey, d.Target.Serial, true, open) {
					return
				}
				a.update(func() { a.deviceNotice = a.failed(L.FailDeX(), err) })
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
			Open:         open,
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
			MaxFPS:        sp.fps,
			CodecID:       sp.codec,
			VideoBitRate:  8_000_000,
			EnableControl: true,
			Audio:         sp.audio,
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
			if wirelessADB && a.autoSwitchOn() && a.fallbackToCompanion(devKey, d.Target.Serial, true, open) {
				return
			}
			a.update(func() { a.deviceNotice = a.failed(L.FailDeX(), err) })
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, sessionDefW, sessionDefH) })
	}()
}

// LaunchMirror opens an interactive Phone Mirror window for Display 0.
func (a *App) LaunchMirror(d DiscoveredDevice, open OpenIn) {
	sp := a.streamPrefs()
	devKey := d.HardwareHash
	if devKey == "" {
		devKey = d.Target.DeviceHash
	}
	if used, limit, atLimit, _ := EncoderHeadroom(d.Profile, a.activeStreamCountFor(devKey)); atLimit {
		a.update(func() { a.deviceNotice = L.EncoderLimitReached(used, limit) })
		return
	}
	wirelessADB := d.Target.Transport != adb.TransportUSB && d.Target.Transport != adb.TransportEmulator
	if wirelessADB && a.prefersCompanion() {
		if r, ok := a.RememberedFor(devKey); ok && r.Companion != nil {
			a.LaunchShare(devKey, open)
			return
		}
	}
	a.update(func() { a.deviceNotice = "" })
	go func() {
		a.rt.coreMu.Lock()
		lib, lErr := a.ensureCoreLocked()
		a.rt.coreMu.Unlock()
		if lErr != nil {
			a.update(func() { a.deviceNotice = a.failed(L.FailEngine(), lErr) })
			return
		}
		client, store, aErr := a.ensureADBAndLeases()
		if aErr != nil {
			a.update(func() { a.deviceNotice = a.failed(L.FailEngine(), aErr) })
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		tLabel := transportLabel(d.Target.Transport)
		wirelessADB := d.Target.Transport != adb.TransportUSB && d.Target.Transport != adb.TransportEmulator
		sid := newSessionID("mirror")
		sw := newSessionWindow(a, SessionSpec{
			ID:           sid,
			DeviceKey:    devKey,
			Serial:       d.Target.Serial,
			Title:        fmt.Sprintf("%s · %s (%s)", d.Profile.Model, L.ModeMirrorTitle(), tLabel),
			Mode:         SessionModeMirror,
			Open:         open,
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
			MaxSize:       sp.size,
			MaxFPS:        sp.fps,
			CodecID:       sp.codec,
			VideoBitRate:  8_000_000,
			EnableControl: true,
			Audio:         sp.audio,
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
			if wirelessADB && a.autoSwitchOn() && a.fallbackToCompanion(devKey, d.Target.Serial, false, open) {
				return
			}
			a.update(func() { a.deviceNotice = a.failed(L.FailScreen(), err) })
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, mirrorDefW, mirrorDefH) })
	}()
}

// LaunchAppWindow opens an isolated chromeless flex-display native window running app on the target device.
func (a *App) LaunchAppWindow(d DiscoveredDevice, app adb.AppEntry, open OpenIn) {
	sp := a.streamPrefs()
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
	wirelessADB := d.Target.Transport != adb.TransportUSB && d.Target.Transport != adb.TransportEmulator
	if wirelessADB && a.prefersCompanion() {
		if r, ok := a.RememberedFor(devKey); ok && r.Companion != nil {
			a.LaunchDaemonAppWindow(devKey, app, open)
			return
		}
	}
	if app.Label == "" || app.Label == pkg || app.IconPath == "" {
		if cached, ok := a.findCachedApp(devKey, pkg); ok {
			if app.Label == "" || app.Label == pkg {
				app.Label = cached.Label
			}
			if app.IconPath == "" {
				app.IconPath = cached.IconPath
			}
		}
	}
	label := strings.TrimSpace(app.Label)
	if label == "" {
		label = pkg
	}
	icon := a.appIconFor(app.IconPath)
	if icon == nil {
		icon = a.appIconByPackage(devKey, pkg)
	}
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
			a.update(func() { a.deviceNotice = a.failed(L.FailEngine(), lErr) })
			return
		}
		client, store, aErr := a.ensureADBAndLeases()
		if aErr != nil {
			a.update(func() { a.deviceNotice = a.failed(L.FailEngine(), aErr) })
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		tLabel := transportLabel(d.Target.Transport)
		wirelessADB := d.Target.Transport != adb.TransportUSB && d.Target.Transport != adb.TransportEmulator
		sid := newSessionID("app")
		sw := newSessionWindow(a, SessionSpec{
			ID:           sid,
			DeviceKey:    devKey,
			Serial:       d.Target.Serial,
			Title:        label,
			Mode:         SessionModeApp,
			Open:         open,
			Transport:    tLabel,
			BackendLabel: label,
			TargetSpec:   fmt.Sprintf("%d×%d flex · 60 fps", appWinInitW, appWinInitH),
			AllowResize:  true,
			ScreenOffCap: true,
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
			MaxFPS:                sp.fps,
			CodecID:               sp.codec,
			VideoBitRate:          8_000_000,
			EnableControl:         true,
			Audio:                 sp.audio,
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
			if wirelessADB && a.autoSwitchOn() && a.fallbackAppToCompanion(devKey, d.Target.Serial, app, open) {
				return
			}
			a.update(func() { a.deviceNotice = a.failed(L.FailApp(), err) })
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, appWinPortraitW, appWinPortraitH) })
		sw.startADBAppGuardAsync(d.Target.Serial, vSess, pkg)
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
			a.update(func() { a.deviceNotice = a.failed(L.FailEngine(), lErr) })
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
			a.update(func() { a.deviceNotice = a.failed(L.FailDeX(), err) })
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
	a.undockLocked(id)
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

// CloseAllSessions shuts everything down for quitting or an update: every session (releasing its
// leases, INV-03), the core with its networks, the Companion link, the assistant and the VM.
func (a *App) CloseAllSessions() {
	a.CloseSessions()
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
			return a.hostDiagnostics(), nil
		},
		LaunchApp: func(_ context.Context, deviceID, packageName string) (any, error) {
			target, ok := a.findDeviceForMCP(deviceID)
			if !ok {
				return nil, fmt.Errorf("device not found")
			}
			a.LaunchAppWindow(target, adb.AppEntry{Package: packageName, Label: packageName}, OpenDefault)
			return map[string]any{"requested": packageName}, nil
		},
		Approve: a.requestApproval,
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

// EnableMCP starts (or restarts with the current choices) the opt-in loopback server that lets
// the local assistant act: the tools in Settings › Security and privacy run without asking, the
// others ask with a card above the panes each time.
// Side effects: Opens a 127.0.0.1 HTTP listener with a bounded 30-minute token.
func (a *App) EnableMCP() {
	a.rt.coreMu.Lock()
	srv := a.ensureMCPServerLocked()
	a.rt.coreMu.Unlock()

	a.mu.Lock()
	allow := assistantAllowlist(a.prefs.AssistantAllow)
	a.mu.Unlock()
	st, err := srv.Enable(allow)
	a.mu.Lock()
	if err != nil {
		a.mcpNotice = a.failed(L.FailAssistant(), err)
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
		a.mcpNotice = a.failed(L.FailAssistant(), err)
	} else {
		a.mcpNotice = ""
	}
	a.mcpStatus = srv.Status()
	a.mu.Unlock()
	a.runOnMain(func() {})
}

// LaunchPhoneCamera opens a scrcpy camera+mic stream paired with a loopback MJPEG bridge (`M5`).
// Side effects: Starts avbridge.MJPEGServer, opens a SessionWindow, and clears state on close.
func (a *App) LaunchPhoneCamera(d DiscoveredDevice, facing string, open OpenIn) {
	a.launchCamera(d, facing, open, true)
}

// launchCamera starts the camera session; muted says whether its sound starts off on this computer.
func (a *App) launchCamera(d DiscoveredDevice, facing string, open OpenIn, muted bool) {
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
			a.update(func() { a.deviceNotice = a.failed(L.FailEngine(), lErr) })
			return
		}
		_, _, aErr := a.ensureADBAndLeases()
		if aErr != nil {
			a.update(func() { a.deviceNotice = a.failed(L.FailEngine(), aErr) })
			return
		}

		mjpegSrv := avbridge.NewMJPEGServer()
		mjpegURL, mErr := mjpegSrv.Start()
		if mErr != nil {
			a.update(func() { a.deviceNotice = a.failed(L.FailEngine(), mErr) })
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()

		tLabel := transportLabel(d.Target.Transport)
		sid := newSessionID("cam")
		sw := newSessionWindow(a, SessionSpec{
			ID:           sid,
			DeviceKey:    devKey,
			Serial:       d.Target.Serial,
			Title:        a.phoneName(d) + " · " + cameraWord(facing),
			Mode:         SessionModeCamera,
			CameraFacing: facing,
			Open:         open,
			Transport:    tLabel,
			BackendLabel: "scrcpy camera + loopback MJPEG",
			TargetSpec:   "1280×720 @ 30 fps",
			OnClose: func() {
				_ = mjpegSrv.Close()
				a.update(func() {
					if a.cameraBridgeURL == mjpegURL {
						a.cameraBridgeURL = ""
					}
				})
			},
		})
		// The phone's microphone is loud on the computer's speakers, so camera sound starts off.
		sw.audioMuted = muted
		ap, _ := lib.OpenAudioPlayer(48000, 2)
		sw.attachAudio(ap)
		sw.camBridge = mjpegSrv

		vSess, err := a.startCameraStream(ctx, sw, facing)
		if err != nil {
			_ = mjpegSrv.Close()
			if ap != nil {
				ap.Close()
			}
			a.update(func() { a.deviceNotice = a.failed(L.FailCamera(), err) })
			return
		}
		a.update(func() { a.cameraBridgeURL = mjpegURL })
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, sessionDefW, sessionDefH) })
	}()
}

// startCameraStream starts the phone camera facing for camera session sw: frames go to sw and
// its MJPEG bridge, sound to sw.
// Notes: right after another camera stream (Switch camera) the phone may still hold the camera,
// and its scrcpy-server then stops at once, so a start that fails is tried twice more.
func (a *App) startCameraStream(ctx context.Context, sw *SessionWindow, facing string) (*scrcpy.Session, error) {
	a.rt.coreMu.Lock()
	lib, err := a.ensureCoreLocked()
	a.rt.coreMu.Unlock()
	if err != nil {
		return nil, err
	}
	client, store, err := a.ensureADBAndLeases()
	if err != nil {
		return nil, err
	}
	var vSess *scrcpy.Session
	cfg := scrcpy.LaunchConfig{
		SessionID:     sw.spec.ID,
		Serial:        sw.spec.Serial,
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
			if sw.camBridge != nil {
				sw.camBridge.PushNV12(f.Width, f.Height, f.YStride, f.UVStride, f.Y, f.UV)
			}
			if vSess != nil {
				st := vSess.Stats()
				sw.PushNV12(f, st.MeasuredFPS, st.AvgDecodeUs)
			} else {
				sw.PushNV12(f, 0, int64(f.DecodeUs))
			}
		},
		OnAudioPCM: sw.PushAudioPCM,
	}
	for attempt := 1; ; attempt++ {
		vSess, err = scrcpy.StartADB(ctx, client, lib, store, cfg)
		if err == nil || attempt == 3 || ctx.Err() != nil {
			return vSess, err
		}
		time.Sleep(1500 * time.Millisecond)
	}
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
			a.update(func() { a.micUplinkNotice = a.failed(L.FailMic(), lErr) })
			return
		}
		if cl == nil {
			a.update(func() { a.micUplinkNotice = "companion not connected" })
			return
		}
		cap, err := lib.OpenMicCapture(48000, 1)
		if err != nil {
			a.update(func() { a.micUplinkNotice = a.failed(L.FailMic(), err) })
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
				a.micUplinkNotice = a.failed(L.FailMic(), streamErr)
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

// SendClipboardToPhone sends text to the connected Companion's clipboard (Home's ⋯ and the tray).
func (a *App) SendClipboardToPhone(text string) {
	a.rt.coreMu.Lock()
	c := a.rt.compClient
	a.rt.coreMu.Unlock()
	if c == nil || text == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := c.SendClipboard(ctx, uint64(time.Now().UnixNano()), text); err != nil {
			a.toast(err.Error())
			return
		}
		a.toast(L.ClipboardSyncedToDevice())
	}()
}

// SendFileToPhone asks for files and pushes them to the connected Companion, which saves them in
// Downloads.
// Side effects: opens the system file dialog off the UI thread.
func (a *App) SendFileToPhone() {
	a.rt.coreMu.Lock()
	c := a.rt.compClient
	a.rt.coreMu.Unlock()
	if c == nil {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.mainWindow(), Title: L.ActionSendFile(), Multiple: true})
		if err != nil || len(paths) == 0 {
			return
		}
		for _, p := range paths {
			data, err := os.ReadFile(p)
			if err != nil {
				a.toast(err.Error())
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err = c.PushFile(ctx, filepath.Base(p), data)
			cancel()
			if err != nil {
				a.toast(err.Error())
				return
			}
		}
		a.toast(L.FilesSent(len(paths)))
	}()
}

// toast shows a short message at the foot of the main window.
func (a *App) toast(msg string) {
	a.update(func() { a.toastMsg = msg })
}

// Local assistant tools, as Settings › Security and privacy names them.
const (
	toolStatus = "list_devices"
	toolChecks = "run_diagnostics"
	toolLaunch = "launch_app"
)

// assistantAllowlist turns the tools chosen to run without asking into the server's allowlist.
func assistantAllowlist(tools []string) mcp.Allowlist {
	return mcp.Allowlist{
		ListDevices:    slices.Contains(tools, toolStatus),
		RunDiagnostics: slices.Contains(tools, toolChecks),
		LaunchApp:      slices.Contains(tools, toolLaunch),
	}
}

// requestApproval shows a card above the panes asking about one call of the local assistant's
// tool and waits for the answer (Deny when ctx ends first), INV-07.
func (a *App) requestApproval(ctx context.Context, tool, detail string) bool {
	id := newSessionID("ap")
	ch := make(chan bool, 1)
	title := L.ApprovalTitle(assistantToolWords(tool))
	a.update(func() { a.approvals = append(a.approvals, approval{ID: id, Title: title, Detail: detail, answer: ch}) })
	if n := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: L.ApprovalNotify()}); n != nil {
		_ = n.Show()
	}
	defer a.update(func() {
		a.approvals = slices.DeleteFunc(a.approvals, func(x approval) bool { return x.ID == id })
	})
	wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	select {
	case ok := <-ch:
		return ok
	case <-wait.Done():
		return false
	}
}

// answerApproval answers the card id.
func (a *App) answerApproval(id string, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ap := range a.approvals {
		if ap.ID == id {
			select {
			case ap.answer <- ok:
			default:
			}
		}
	}
}

// assistantToolWords names a local assistant tool for people.
func assistantToolWords(tool string) string {
	switch tool {
	case toolStatus:
		return L.ToolStatus()
	case toolChecks:
		return L.ToolChecks()
	case toolLaunch:
		return L.ToolLaunch()
	}
	return tool
}

// CloseSessions closes every open picture (DeX, phone screens, app windows, cameras), releasing
// their leases (INV-03), and leaves the rest running.
func (a *App) CloseSessions() {
	a.mu.Lock()
	list := make([]*SessionWindow, 0, len(a.sessions))
	for _, s := range a.sessions {
		list = append(list, s)
	}
	a.mu.Unlock()
	for _, s := range list {
		s.Close()
	}
}

// ScreenshotPhone saves the phone's screen to Pictures\AndroFuse without opening a picture
// (the tray's Screenshot).
func (a *App) ScreenshotPhone(d DiscoveredDevice) {
	client, _, err := a.ensureADBAndLeases()
	if err != nil {
		a.notify(err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	png, err := client.ExecOut(ctx, d.Target.Serial, "screencap", "-p")
	if err != nil || len(png) == 0 {
		a.notify(L.ScreenshotFailed())
		return
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Pictures", "AndroFuse")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.png", strings.ReplaceAll(a.phoneName(d), " ", "_"), time.Now().Format("20060102-150405")))
	if err := os.WriteFile(path, png, 0o644); err != nil {
		a.notify(err.Error())
		return
	}
	a.notify(L.ScreenshotSaved(filepath.Base(path)))
}

// PhoneScreenOff puts the phone's screen to sleep (the tray's Turn phone screen off).
func (a *App) PhoneScreenOff(d DiscoveredDevice) {
	client, _, err := a.ensureADBAndLeases()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, _ = client.Shell(ctx, d.Target.Serial, "input keyevent KEYCODE_SLEEP")
}

// notify shows a Windows notification from AndroFuse (for actions taken from the tray, when the
// main window may be closed).
func (a *App) notify(msg string) {
	_ = mygo.NewNotification(mygo.NotificationOptions{Title: L.AppName(), Body: msg}).Show()
}

// streamPrefs is the picture the user asked for (Settings › Phones and picture): a size limit (0
// follows the phone), a frame-rate limit, the video format and whether the phone's sound plays.
// They are targets; the header shows what is measured (`INV-05`).
type streamPrefs struct {
	size  int
	fps   float64
	codec uint32
	audio bool
}

func (a *App) streamPrefs() streamPrefs {
	a.mu.Lock()
	defer a.mu.Unlock()
	sp := streamPrefs{size: a.prefs.MaxSize, fps: 60, codec: scrcpy.CodecH264, audio: !a.prefs.MuteSound}
	if a.prefs.MaxFPS > 0 {
		sp.fps = float64(a.prefs.MaxFPS)
	}
	if a.prefs.Codec == "h265" {
		sp.codec = scrcpy.CodecH265
	}
	return sp
}

// handoffOn reports whether a DeX session moves to Wi-Fi when USB is unplugged (Settings ›
// Phones and picture).
func (a *App) handoffOn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.prefs.NoUSBHandoff
}

// watchDevices notices phones plugged in, unplugged or reconnected: every 2 s it lists adb's
// targets (cheap), asks each wireless one to answer and closes it within ~3 s after two fast
// misses (adb itself notices a phone that left Wi-Fi only minutes later), and scans fully when
// that list changed; every 30 s it scans when a remembered phone with automatic reconnect is
// offline, which tries to reconnect it.
func (a *App) watchDevices(ctx context.Context) {
	last := ""
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		n++
		if a.isInstallingUpdate() {
			continue
		}
		client, _, err := a.ensureADBAndLeases()
		if err != nil {
			continue
		}
		lctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		devs, err := client.Devices(lctx)
		cancel()
		if err != nil {
			continue
		}
		// adb keeps listing a wireless connection as "device" for minutes after the phone left
		// Wi-Fi (for mobile data): probe each wireless one and close immediately on two quick misses.
		dropped := false
		for _, d := range devs {
			if d.State != "device" || !strings.Contains(d.Serial, ":") || d.Transport == adb.TransportEmulator {
				continue
			}
			ok := false
			for range 2 {
				pctx, pcancel := context.WithTimeout(ctx, 1500*time.Millisecond)
				_, pErr := client.Shell(pctx, d.Serial, "echo")
				pcancel()
				if pErr == nil {
					ok = true
					break
				}
			}
			if !ok {
				dctx, dcancel := context.WithTimeout(ctx, 2*time.Second)
				_ = client.Disconnect(dctx, d.Serial)
				dcancel()
				dropped = true
			}
		}
		var parts []string
		for _, d := range devs {
			parts = append(parts, d.Serial+"="+d.State)
		}
		slices.Sort(parts)
		now := strings.Join(parts, ",")
		changed := now != last
		last = now
		if changed || dropped || (n%15 == 0 && a.wantsReconnect(devs)) {
			a.RefreshDevices()
		}
	}
}

// fallbackToCompanion drops a dead wireless ADB target and immediately hands off to the paired
// Companion (daemon desktop/DeX or mirror/share) instead of failing the launch.
func (a *App) fallbackToCompanion(devKey, serial string, desktop bool, open OpenIn) bool {
	if devKey == "" {
		return false
	}
	r, ok := a.RememberedFor(devKey)
	if !ok || r.Companion == nil {
		return false
	}
	a.setCardWork(devKey, cardWork{Busy: L.CardSwitchingCompanion(r.displayName())})
	a.disconnectDeadWirelessAsync(serial)
	if desktop {
		a.LaunchDaemonDesktop(devKey, open)
	} else {
		a.LaunchShare(devKey, open)
	}
	return true
}

// fallbackAppToCompanion drops a dead wireless ADB target and opens the Independent App Window
// through the paired Companion daemon instead of failing the launch.
func (a *App) fallbackAppToCompanion(devKey, serial string, app adb.AppEntry, open OpenIn) bool {
	if devKey == "" {
		return false
	}
	r, ok := a.RememberedFor(devKey)
	if !ok || r.Companion == nil {
		return false
	}
	a.setCardWork(devKey, cardWork{Busy: L.CardSwitchingCompanion(r.displayName())})
	a.disconnectDeadWirelessAsync(serial)
	a.LaunchDaemonAppWindow(devKey, app, open)
	return true
}

func (a *App) disconnectDeadWirelessAsync(serial string) {
	if serial == "" || !strings.Contains(serial, ":") {
		return
	}
	go func() {
		if client, _, err := a.ensureADBAndLeases(); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = client.Disconnect(ctx, serial)
			cancel()
			a.RefreshDevices()
		}
	}()
}

// companionStreamProfile picks bitrate, FPS cap, size cap, and video codec adapted to the active
// route (ADB / direct Wi-Fi LAN vs P2P mesh vs relayed WAN / 5G) and decoder capabilities (`INV-05`).
func (a *App) companionStreamProfile(meshHost, addr string, desktop bool) (bitRate, maxFPS, maxSize int, codecStr string, codecID uint32, relayed bool) {
	sp := a.streamPrefs()
	a.mu.Lock()
	hevcHW := a.diag.SelfCheck.HEVCHWDecoder
	a.mu.Unlock()
	overMesh := a.isMeshRoute(addr)
	direct, latencyMs, inMesh := a.meshPeerRoute(meshHost)

	maxFPS = int(sp.fps)
	if maxFPS <= 0 {
		maxFPS = 60
	}
	maxSize = sp.size
	switch {
	case !overMesh:
		if desktop {
			bitRate = 8_000_000
		} else {
			bitRate = 6_000_000
		}
	case inMesh && direct && latencyMs < 35:
		if desktop {
			bitRate = 6_000_000
		} else {
			bitRate = 5_000_000
		}
	case inMesh && direct:
		if desktop {
			bitRate = 2_500_000
		} else {
			bitRate = 2_000_000
		}
		if maxFPS > 30 {
			maxFPS = 30
		}
		if !desktop && maxSize == 0 {
			maxSize = 1080
		}
	default:
		relayed = true
		if desktop {
			bitRate = 1_500_000
		} else {
			bitRate = 1_000_000
		}
		if maxFPS > 30 {
			maxFPS = 30
		}
		if !desktop && maxSize == 0 {
			maxSize = 960
		}
	}
	codecStr, codecID = "h264", scrcpy.CodecH264
	if sp.codec == scrcpy.CodecH265 || (overMesh && hevcHW && (relayed || latencyMs >= 35)) {
		codecStr, codecID = "h265", scrcpy.CodecH265
	}
	return bitRate, maxFPS, maxSize, codecStr, codecID, relayed
}

// wantsReconnect reports whether a remembered phone with automatic reconnect is not among devs.
func (a *App) wantsReconnect(devs []adb.Device) bool {
	if !a.allowsWirelessADB() {
		return false
	}
	online := a.onlineHashes(devs)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range a.remembered {
		if r.AutoReconnect && !online[r.HardwareHash] {
			return true
		}
	}
	return false
}

// LaunchShare shows a remembered phone's screen through its Companion when Wireless debugging
// cannot run (mobile data): the phone asks its user (Android's capture consent), then streams
// H.264 in scrcpy framing over the Companion's TLS link, which the same decoder shows. Back, Home
// and Recents go through the Companion's accessibility service; taps are not available.
// Side effects: may add a mesh forward to reach the Companion; opens a session.
func (a *App) LaunchShare(hash string, open OpenIn) {
	r, ok := a.RememberedFor(hash)
	if !ok || r.Companion == nil {
		a.notify(L.CompanionNotPaired())
		return
	}
	b := *r.Companion
	name := r.displayName()
	fail := func(err error) { a.setCardWork(hash, cardWork{Note: a.failed(L.CardShareFailed(name), err)}) }
	a.setCardWork(hash, cardWork{Busy: L.CardReaching(name)})
	go func() {
		tok, err := openSecret(b.SealedToken)
		if err != nil {
			fail(err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		keys, addr, err := a.findCompanion(ctx, hash, b, string(tok))
		if err != nil {
			fail(err)
			return
		}
		if st, stErr := keys.GetStatus(ctx); stErr == nil && st.DaemonReady {
			bitRate, maxFPS, maxSize, codecStr, codecID, _ := a.companionStreamProfile(b.MeshHost, addr, false)
			vidConn, ctrlConn, dErr := companion.DialDaemonStream(ctx, addr, string(tok), b.Fingerprint, companion.DaemonStreamRequest{
				MaxSize: maxSize,
				BitRate: bitRate,
				MaxFPS:  maxFPS,
				Codec:   codecStr,
				Control: true,
			})
			if dErr == nil {
				_ = keys.Close()
				a.setCardWork(hash, cardWork{})
				a.rt.coreMu.Lock()
				lib, lErr := a.ensureCoreLocked()
				a.rt.coreMu.Unlock()
				if lErr != nil {
					_ = vidConn.Close()
					if ctrlConn != nil {
						_ = ctrlConn.Close()
					}
					a.notify(a.failed(L.FailEngine(), lErr))
					return
				}
				sid := newSessionID("mirror")
				sw := newSessionWindow(a, SessionSpec{
					ID:           sid,
					DeviceKey:    hash,
					Title:        fmt.Sprintf("%s · %s (Companion)", r.displayName(), L.ModeMirrorTitle()),
					Mode:         SessionModeMirror,
					Open:         open,
					Transport:    "Companion",
					BackendLabel: L.ModeMirrorTitle(),
					TargetSpec:   fmt.Sprintf("Display 0 · %d fps", maxFPS),
					ScreenOffCap: true,
				})
				var vSess *scrcpy.Session
				vSess, err = scrcpy.StartFromConns(lib, nil, nil, 0, 0, nil, vidConn, ctrlConn, true, scrcpy.LaunchConfig{
					SessionID:     sid,
					EnableControl: true,
					MaxSize:       maxSize,
					MaxFPS:        float64(maxFPS),
					CodecID:       codecID,
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
				})
				if err != nil {
					_ = vidConn.Close()
					if ctrlConn != nil {
						_ = ctrlConn.Close()
					}
					a.notify(a.failed(L.FailScreen(), err))
					return
				}
				a.addSession(sw)
				a.runOnMain(func() { sw.attachScrcpy(vSess, nil, mirrorDefW, mirrorDefH) })
				return
			}
			_ = a.failed(L.FailScreen(), dErr)
		}
		a.setCardWork(hash, cardWork{Busy: L.ShareAsking(name)})
		stream, err := companion.DialShare(ctx, addr, string(tok), b.Fingerprint)
		if err != nil {
			_ = keys.Close()
			fail(err)
			return
		}
		a.setCardWork(hash, cardWork{})
		a.rt.coreMu.Lock()
		lib, lErr := a.ensureCoreLocked()
		a.rt.coreMu.Unlock()
		if lErr != nil {
			_ = stream.Close()
			a.notify(a.failed(L.FailEngine(), lErr))
			return
		}
		sid := newSessionID("share")
		sw := newSessionWindow(a, SessionSpec{
			ID:           sid,
			DeviceKey:    hash,
			Title:        r.displayName(),
			Mode:         SessionModeShare,
			Open:         open,
			Transport:    "Companion",
			BackendLabel: L.KindShare(),
			TargetSpec:   "1280 · 30 fps",
			OnClose: func() {
				if keys != nil {
					_ = keys.Close()
				}
			},
		})
		if keys != nil {
			sw.shareKey = func(action string) {
				kctx, kcancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer kcancel()
				if err := keys.Key(kctx, action); err != nil {
					sw.flashNote(err.Error())
				}
			}
		}
		var vSess *scrcpy.Session
		vSess, err = scrcpy.StartFromConns(lib, nil, nil, 0, 0, nil, stream, nil, false, scrcpy.LaunchConfig{
			SessionID:     sid,
			InitialWidth:  720,
			InitialHeight: 1280,
			OnFrame: func(f core.NV12Frame) {
				if vSess != nil {
					st := vSess.Stats()
					sw.PushNV12(f, st.MeasuredFPS, st.AvgDecodeUs)
				} else {
					sw.PushNV12(f, 0, int64(f.DecodeUs))
				}
			},
		})
		if err != nil {
			_ = stream.Close()
			a.notify(a.failed(L.FailScreen(), err))
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, mirrorDefW, mirrorDefH) })
	}()
}

// LaunchDaemonDesktop opens a 1920x1080 virtual desktop / Samsung DeX display through the
// attached UID 2000 daemon over Companion TLS 1.3 without requiring Wireless debugging (`PLAN` §9.2).
// Side effects: connects over Companion, opens a resizable SessionWindow backed by scrcpy-server.
func (a *App) LaunchDaemonDesktop(hash string, open OpenIn) {
	r, ok := a.RememberedFor(hash)
	if !ok || r.Companion == nil {
		a.notify(L.CompanionNotPaired())
		return
	}
	b := *r.Companion
	name := r.displayName()
	fail := func(err error) { a.setCardWork(hash, cardWork{Note: a.failed(L.FailDeX(), err)}) }
	a.setCardWork(hash, cardWork{Busy: L.CardReaching(name)})
	go func() {
		tok, err := openSecret(b.SealedToken)
		if err != nil {
			fail(err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		keys, addr, err := a.findCompanion(ctx, hash, b, string(tok))
		if err != nil {
			fail(err)
			return
		}
		st, stErr := keys.GetStatus(ctx)
		_ = keys.Close()
		if stErr != nil {
			fail(stErr)
			return
		}
		if !st.DaemonReady || !st.DaemonTrustedDisplay {
			fail(fmt.Errorf("daemon desktop unavailable (ready=%v trusted=%v)", st.DaemonReady, st.DaemonTrustedDisplay))
			return
		}

		bitRate, maxFPS, _, codecStr, codecID, relayed := a.companionStreamProfile(b.MeshHost, addr, true)
		isSamsung := isSamsungRemembered(r)
		dexSpec, fallbackDisp := "dex:1920x1080/160", "1920x1080/200"
		initW, initH := uint32(1920), uint32(1080)
		if relayed {
			dexSpec, fallbackDisp = "dex:1280x720/140", "1280x720/160"
			initW, initH = 1280, 720
		}
		var (
			vidConn, ctrlConn net.Conn
			newDisplay        = fallbackDisp
			flexDisplay       = true
			mode              = SessionModeDeXFallback
			label             = L.KindDeXFallback()
		)
		if isSamsung {
			vidConn, ctrlConn, err = companion.DialDaemonStream(ctx, addr, string(tok), b.Fingerprint, companion.DaemonStreamRequest{
				BitRate:    bitRate,
				MaxFPS:     maxFPS,
				Codec:      codecStr,
				NewDisplay: dexSpec,
				Control:    true,
			})
			if err == nil {
				newDisplay = ""
				flexDisplay = false
				mode = SessionModeDeX
				label = L.KindDeX()
			}
		}
		if vidConn == nil {
			vidConn, ctrlConn, err = companion.DialDaemonStream(ctx, addr, string(tok), b.Fingerprint, companion.DaemonStreamRequest{
				BitRate:     bitRate,
				MaxFPS:      maxFPS,
				Codec:       codecStr,
				NewDisplay:  fallbackDisp,
				FlexDisplay: true,
				Control:     true,
			})
		}
		if err != nil {
			fail(err)
			return
		}
		a.setCardWork(hash, cardWork{})
		a.rt.coreMu.Lock()
		lib, lErr := a.ensureCoreLocked()
		a.rt.coreMu.Unlock()
		if lErr != nil {
			_ = vidConn.Close()
			if ctrlConn != nil {
				_ = ctrlConn.Close()
			}
			a.notify(a.failed(L.FailEngine(), lErr))
			return
		}

		sid := newSessionID("ddex")
		sw := newSessionWindow(a, SessionSpec{
			ID:           sid,
			DeviceKey:    hash,
			Title:        fmt.Sprintf("%s · %s (Companion)", name, label),
			Mode:         mode,
			Open:         open,
			Transport:    "Companion",
			BackendLabel: label,
			TargetSpec:   fmt.Sprintf("%d×%d · %d fps", initW, initH, maxFPS),
			IsDeX:        true,
			AllowResize:  true,
		})
		var vSess *scrcpy.Session
		vSess, err = scrcpy.StartFromConns(lib, nil, nil, 0, 0, nil, vidConn, ctrlConn, true, scrcpy.LaunchConfig{
			SessionID:     sid,
			NewDisplay:    newDisplay,
			FlexDisplay:   flexDisplay,
			EnableControl: true,
			MaxFPS:        float64(maxFPS),
			CodecID:       codecID,
			VideoBitRate:  bitRate,
			InitialWidth:  initW,
			InitialHeight: initH,
			OnFrame: func(f core.NV12Frame) {
				if vSess != nil {
					st := vSess.Stats()
					sw.PushNV12(f, st.MeasuredFPS, st.AvgDecodeUs)
				} else {
					sw.PushNV12(f, 0, int64(f.DecodeUs))
				}
			},
			OnClipboard: sw.OnDeviceClipboard,
		})
		if err != nil {
			_ = vidConn.Close()
			if ctrlConn != nil {
				_ = ctrlConn.Close()
			}
			fail(err)
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, sessionDefW, sessionDefH) })
	}()
}

// LaunchDaemonAppWindow opens an isolated chromeless flex-display Independent App Window through
// the paired UID 2000 daemon over Companion TLS 1.3 (`INV-01`, `PLAN` §9.2).
// Side effects: connects over Companion, opens a resizable SessionWindow running `app`.
func (a *App) LaunchDaemonAppWindow(hash string, app adb.AppEntry, open OpenIn) {
	pkg := strings.TrimSpace(app.Package)
	if pkg == "" {
		return
	}
	r, ok := a.RememberedFor(hash)
	if !ok || r.Companion == nil {
		a.notify(L.CompanionNotPaired())
		return
	}
	if app.Label == "" || app.Label == pkg || app.IconPath == "" {
		if cached, ok := a.findCachedApp(hash, pkg); ok {
			if app.Label == "" || app.Label == pkg {
				app.Label = cached.Label
			}
			if app.IconPath == "" {
				app.IconPath = cached.IconPath
			}
		}
	}
	label := strings.TrimSpace(app.Label)
	if label == "" {
		label = pkg
	}
	icon := a.appIconFor(app.IconPath)
	if icon == nil {
		icon = a.appIconByPackage(hash, pkg)
	}
	startArg := pkg
	if !strings.HasPrefix(startArg, "+") {
		startArg = "+" + startArg
	}
	b := *r.Companion
	name := r.displayName()
	fail := func(err error) { a.setCardWork(hash, cardWork{Note: a.failed(L.FailApp(), err)}) }
	a.setCardWork(hash, cardWork{Busy: L.CardReaching(name)})
	go func() {
		tok, err := openSecret(b.SealedToken)
		if err != nil {
			fail(err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		keys, addr, err := a.findCompanion(ctx, hash, b, string(tok))
		if err != nil {
			fail(err)
			return
		}
		st, stErr := keys.GetStatus(ctx)
		_ = keys.Close()
		if stErr != nil {
			fail(stErr)
			return
		}
		if !st.DaemonReady || !st.DaemonTrustedDisplay {
			fail(fmt.Errorf("daemon app window unavailable (ready=%v trusted=%v)", st.DaemonReady, st.DaemonTrustedDisplay))
			return
		}

		bitRate, maxFPS, _, codecStr, codecID, _ := a.companionStreamProfile(b.MeshHost, addr, false)
		newDisp := fmt.Sprintf("%dx%d/%d", appWinInitW, appWinInitH, appWinInitDPI)
		vidConn, ctrlConn, err := companion.DialDaemonStream(ctx, addr, string(tok), b.Fingerprint, companion.DaemonStreamRequest{
			BitRate:               bitRate,
			MaxFPS:                maxFPS,
			Codec:                 codecStr,
			NewDisplay:            newDisp,
			HideSystemDecorations: true,
			FlexDisplay:           true,
			StartApp:              startArg,
			Control:               true,
		})
		if err != nil {
			fail(err)
			return
		}
		a.setCardWork(hash, cardWork{})
		a.rt.coreMu.Lock()
		lib, lErr := a.ensureCoreLocked()
		a.rt.coreMu.Unlock()
		if lErr != nil {
			_ = vidConn.Close()
			if ctrlConn != nil {
				_ = ctrlConn.Close()
			}
			a.notify(a.failed(L.FailEngine(), lErr))
			return
		}

		sid := newSessionID("dapp")
		sw := newSessionWindow(a, SessionSpec{
			ID:           sid,
			DeviceKey:    hash,
			Title:        label,
			Mode:         SessionModeApp,
			Open:         open,
			Transport:    "Companion",
			BackendLabel: label,
			TargetSpec:   fmt.Sprintf("%d×%d flex · %d fps", appWinInitW, appWinInitH, maxFPS),
			AllowResize:  true,
			ScreenOffCap: true,
			AppIcon:      icon,
			AppPackage:   pkg,
		})
		var vSess *scrcpy.Session
		vSess, err = scrcpy.StartFromConns(lib, nil, nil, 0, 0, nil, vidConn, ctrlConn, true, scrcpy.LaunchConfig{
			SessionID:             sid,
			NewDisplay:            newDisp,
			FlexDisplay:           true,
			HideSystemDecorations: true,
			StartAppPackage:       startArg,
			EnableControl:         true,
			MaxFPS:                float64(maxFPS),
			CodecID:               codecID,
			VideoBitRate:          bitRate,
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
		})
		if err != nil {
			_ = vidConn.Close()
			if ctrlConn != nil {
				_ = ctrlConn.Close()
			}
			fail(err)
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, appWinPortraitW, appWinPortraitH) })
	}()
}
