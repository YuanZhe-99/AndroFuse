package gui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/dex"
	"github.com/YuanZhe-99/AndroFuse/internal/lease"
	"github.com/YuanZhe-99/AndroFuse/internal/scrcpy"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// DiscoveredDevice holds inspected metadata, DeX capabilities, and selectable user-0 apps
// for one online ADB transport (USB wired, TLS Wi-Fi wireless, or Android Emulator).
type DiscoveredDevice struct {
	Target      adb.Device
	Profile     adb.DeviceProfile
	DexProbe    dex.ProbeReport
	TruthDeX    dex.TruthVerdict
	Apps        []adb.AppEntry
	SelectedApp string
	Probed      bool
	ProbeErr    string
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
	coreMu     sync.Mutex
	coreLib    *core.Lib
	adbClient  *adb.Client
	leaseStore *lease.Store
	recoverRan bool
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

// RefreshDevices scans wired USB, TLS Wi-Fi ADB, Emulator, and mDNS Wireless Debugging services.
func (a *App) RefreshDevices() {
	a.mu.Lock()
	if a.scanningDevices {
		a.mu.Unlock()
		return
	}
	a.scanningDevices = true
	prevApps := make(map[string]string, len(a.devices))
	for _, d := range a.devices {
		if d.SelectedApp != "" {
			prevApps[d.Target.Serial] = d.SelectedApp
		}
	}
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
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		devs, dErr := client.Devices(ctx)
		svcs, _ := client.MDNSServices(ctx)

		a.rt.coreMu.Lock()
		if !a.rt.recoverRan && dErr == nil {
			a.rt.recoverRan = true
			a.recoverStaleLeases(ctx, client, store, devs)
		}
		a.rt.coreMu.Unlock()

		var inspected []DiscoveredDevice
		for _, d := range devs {
			item := DiscoveredDevice{
				Target:      d,
				SelectedApp: prevApps[d.Serial],
			}
			if d.State == "device" {
				prof, pErr := client.ProbeDevice(ctx, d.Serial)
				if pErr != nil {
					item.ProbeErr = pErr.Error()
				} else {
					item.Profile = prof
					item.Probed = true
					if strings.EqualFold(prof.Manufacturer, "samsung") && d.Transport != adb.TransportEmulator {
						if rep, rErr := dex.PushAndProbe(ctx, client, d.Serial); rErr == nil {
							item.DexProbe = rep
							item.TruthDeX = dex.EvaluateTruth(prof, rep, "AndroFuseDeX", dex.BackendADBLoopback)
						} else {
							item.TruthDeX = dex.EvaluateTruth(prof, dex.ProbeReport{}, "", dex.BackendVirtualFallback)
						}
					} else {
						item.TruthDeX = dex.EvaluateTruth(prof, dex.ProbeReport{}, "", dex.BackendVirtualFallback)
					}
					if apps, aErr := client.ListLaunchableApps(ctx, d.Serial); aErr == nil {
						item.Apps = apps
						if item.SelectedApp == "" && len(apps) > 0 {
							item.SelectedApp = pickDefaultApp(apps)
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
		})
	}()
}

func pickDefaultApp(apps []adb.AppEntry) string {
	preferred := []string{
		"com.sec.android.app.popupcalculator",
		"com.google.android.calculator",
		"com.android.calculator2",
		"com.android.settings",
		"com.sec.android.app.clockpackage",
	}
	for _, want := range preferred {
		for _, a := range apps {
			if a.Package == want {
				return a.Package
			}
		}
	}
	return apps[0].Package
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
		if !forceFallback && d.TruthDeX.IsGenuineDeX && d.Target.Transport != adb.TransportEmulator {
			dexSess, err := dex.StartNativeLoopback(ctx, client, store, d.Target.Serial, 1920, 1080, 160, 15)
			if err != nil {
				a.update(func() { a.deviceNotice = err.Error() })
				return
			}
			ready := dexSess.Ready()
			sid := newSessionID("dex")
			sw := newSessionWindow(a, SessionSpec{
				ID:           sid,
				Title:        fmt.Sprintf("%s · Samsung DeX (%s)", d.Profile.Model, tLabel),
				Mode:         SessionModeDeX,
				Transport:    tLabel,
				BackendLabel: d.TruthDeX.Label,
				TargetSpec:   "1920×1080 @ 60 fps",
				IsDeX:        true,
				AllowResize:  true,
			})
			var vSess *scrcpy.Session
			vSess, err = scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
				SessionID:     sid + "-vid",
				Serial:        d.Target.Serial,
				DisplayID:     ready.DisplayID,
				MaxFPS:        60,
				VideoBitRate:  12_000_000,
				EnableControl: true,
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
			})
			if err != nil {
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
			Title:        fmt.Sprintf("%s · %s (%s)", d.Profile.Model, fallbackVerdict.Label, tLabel),
			Mode:         SessionModeDeXFallback,
			Transport:    tLabel,
			BackendLabel: fallbackVerdict.Label,
			TargetSpec:   "1600×900 @ 60 fps",
			IsDeX:        true,
			AllowResize:  false,
		})
		var vSess *scrcpy.Session
		vSess, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
			SessionID:     sid,
			Serial:        d.Target.Serial,
			NewDisplay:    "1600x900/160",
			FlexDisplay:   true,
			MaxFPS:        60,
			VideoBitRate:  8_000_000,
			EnableControl: true,
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
		})
		if err != nil {
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, sessionDefW, sessionDefH) })
	}()
}

// LaunchMirror opens an interactive Phone Mirror window for Display 0.
func (a *App) LaunchMirror(d DiscoveredDevice) {
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
			Title:        fmt.Sprintf("%s · %s (%s)", d.Profile.Model, L.ModeMirrorTitle(), tLabel),
			Mode:         SessionModeMirror,
			Transport:    tLabel,
			BackendLabel: L.ModeMirrorTitle(),
			TargetSpec:   "Display 0 · 60 fps",
			ScreenOffCap: true,
		})
		var vSess *scrcpy.Session
		vSess, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
			SessionID:     sid,
			Serial:        d.Target.Serial,
			DisplayID:     0,
			MaxSize:       1280,
			MaxFPS:        60,
			VideoBitRate:  8_000_000,
			EnableControl: true,
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
		})
		if err != nil {
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, mirrorDefW, mirrorDefH) })
	}()
}

// LaunchAppWindow opens an isolated flex-display native window running pkg on the target device.
func (a *App) LaunchAppWindow(d DiscoveredDevice, pkg, label string) {
	pkg = strings.TrimSpace(pkg)
	if pkg == "" {
		return
	}
	if label == "" {
		label = pkg
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
			Title:        fmt.Sprintf("%s · %s (%s)", label, d.Profile.Model, tLabel),
			Mode:         SessionModeApp,
			Transport:    tLabel,
			BackendLabel: label,
			TargetSpec:   "1280×800 flex · 60 fps",
			AllowResize:  true,
		})
		var vSess *scrcpy.Session
		vSess, err := scrcpy.StartADB(ctx, client, lib, store, scrcpy.LaunchConfig{
			SessionID:       sid,
			Serial:          d.Target.Serial,
			NewDisplay:      "1280x800/160",
			FlexDisplay:     true,
			StartAppPackage: pkg,
			MaxFPS:          60,
			VideoBitRate:    8_000_000,
			EnableControl:   true,
			InitialWidth:    1280,
			InitialHeight:   800,
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
			a.update(func() { a.deviceNotice = err.Error() })
			return
		}
		a.addSession(sw)
		a.runOnMain(func() { sw.attachScrcpy(vSess, nil, appWinDefW, appWinDefH) })
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
	a.rt.coreMu.Unlock()
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
