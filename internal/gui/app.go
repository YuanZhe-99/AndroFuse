// Package gui implements the MyGo native UI shell for AndroFuse across
// Windows desktop (x64/ARM64) and Android Companion (M3).
//
// Views are pure functions of App state; background diagnostics reach the UI
// via Window.Update under App.mu (.agents/skills/mygo-maintenance/SKILL.md).
package gui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/avbridge"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/dex"
	"github.com/YuanZhe-99/AndroFuse/internal/mcp"
	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
	"github.com/YuanZhe-99/AndroFuse/internal/vm"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

type navPage string

const (
	pageDevices     navPage = "devices"
	pageApps        navPage = "apps"
	pageCompanion   navPage = "companion"
	pageDiagnostics navPage = "diagnostics"
	pageSettings    navPage = "settings"
)

// Prefs stores persistent GUI preferences in stateDir/gui.json.
type Prefs struct {
	Theme    string     `json:"theme,omitempty"`
	Language string     `json:"language,omitempty"`
	Mesh     *MeshPrefs `json:"mesh,omitempty"`
	// StreamsInWindow and AppsInWindow open DeX, mirroring and camera sessions, and app windows,
	// in their own windows instead of main-window panes (Settings › Windows and split).
	StreamsInWindow bool `json:"streams_in_window,omitempty"`
	AppsInWindow    bool `json:"apps_in_window,omitempty"`
}

// App holds the state of the AndroFuse UI shell.
type App struct {
	mu       sync.Mutex
	profile  string
	stateDir string
	prefs    Prefs
	window   *mygo.Window
	tray     *Tray

	page       string
	probing    bool
	diag       HostDiagnostics
	diagReady  bool
	themeIndex int
	langIndex  int
	quitting   bool

	rt               guiRuntime
	scanningDevices  bool
	devices          []DiscoveredDevice
	mdnsServices     []adb.MDNSService
	deviceNotice     string
	manualAddr       string
	pairAddr         string
	pairCode         string
	appFilter        string
	showSystemApps   bool
	appIcons         map[string]*ui.Bitmap
	dexRoutes        map[string]dex.RoutePreference
	vmStatus         vm.Status
	compAddr         string
	compToken        string
	compConnected    bool
	compStatus       companion.DeviceStatus
	compNotice       string
	mcpStatus        mcp.Status
	mcpAllowLaunch   bool
	mcpNotice        string
	cameraFacingIdx  int
	cameraBridgeURL  string
	vcamCap          avbridge.VirtualCameraCapability
	meshNetName      string
	meshSecret       string
	meshPeer         string
	meshAccept       bool // accept direct connections on a random high port
	compJoinMesh     bool // first-pairing checkbox: also hand the running mesh to the phone
	meshStatus       mesh.Status
	meshNotice       string
	micUplinkActive  bool
	micUplinkNotice  string
	sessions         map[string]*SessionWindow
	remembered       []RememberedDevice // devices.json
	split            *paneSplit         // two docked sessions shown together (pageSplit)
	connectOpen      bool               // the Connect device dialog is showing
	connectTab       int                // 0 wireless phone, 1 Companion
	settingsTab      string             // Settings category
	appsDevice       string             // HardwareHash whose apps the Apps page lists
	installCompanion bool               // Connect dialog: also install the Companion (unticked, INV-09)
	customAPK        string             // a Companion installer the user chose (ChooseCompanionAPK)
	bundledAPK       bool               // a same-version Companion APK ships with the app (checked once)
}

// DefaultStateDir returns the per-user state directory for AndroFuse.
func DefaultStateDir(profile string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := "AndroFuse"
	if profile != "" {
		dir += "-" + profile
	}
	return filepath.Join(base, dir), nil
}

// ApplyTheme applies "system", "light", or "dark" to the MyGo application theme.
func ApplyTheme(theme string) {
	switch theme {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}

// New constructs an App state container and loads saved preferences.
func New(profile, stateDir string) *App {
	a := &App{
		profile:    profile,
		stateDir:   stateDir,
		prefs:      Prefs{Theme: "system"},
		page:       string(pageDevices),
		appIcons:   make(map[string]*ui.Bitmap),
		dexRoutes:  make(map[string]dex.RoutePreference),
		vcamCap:    avbridge.ProbeVirtualCamera(),
		meshStatus: mesh.Status{},
		sessions:   make(map[string]*SessionWindow),
	}
	a.loadPrefs()
	a.loadDevices()
	a.bundledAPK = companionAPK() != ""
	if p := a.prefs.Mesh; p != nil {
		a.meshNetName, a.meshPeer, a.meshAccept = p.NetworkName, p.Peer, p.Accept
	}
	a.compJoinMesh = true
	a.syncControlIndices()
	return a
}

// ThemePref returns the saved appearance preference.
func (a *App) ThemePref() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prefs.Theme
}

// LanguagePref returns the saved BCP 47 language preference, or "" for system.
func (a *App) LanguagePref() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prefs.Language
}

// SetTray stores the active desktop tray handle.
func (a *App) SetTray(t *Tray) {
	a.mu.Lock()
	a.tray = t
	a.mu.Unlock()
}

// Start kicks off the initial asynchronous host diagnostics check and device scan.
func (a *App) Start() {
	a.RefreshDiagnostics()
	a.RefreshDevices()
	a.RefreshVMStatus()
	go a.resumeMesh()
}

// Focus creates (when closed to tray) or raises the main application window.
func (a *App) Focus() {
	a.mu.Lock()
	w := a.window
	a.mu.Unlock()
	if w == nil {
		w = a.openMainWindow()
		a.mu.Lock()
		a.window = w
		a.mu.Unlock()
	}
	w.Show()
	w.Focus()
}

// RefreshChrome updates window metadata when the active locale changes.
func (a *App) RefreshChrome() {
	a.mu.Lock()
	w := a.window
	a.mu.Unlock()
	if w != nil {
		w.SetTitle(L.AppName())
	}
}

// Quit tears down active streaming sessions and exits cleanly (INV-03).
func (a *App) Quit() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	a.CloseAllSessions()
	mygo.App.Quit()
}

// RefreshDiagnostics probes host hardware/driver capabilities off the UI thread.
func (a *App) RefreshDiagnostics() {
	a.mu.Lock()
	if a.probing {
		a.mu.Unlock()
		return
	}
	a.probing = true
	stateDir, profile := a.stateDir, a.profile
	a.mu.Unlock()

	go func() {
		res := RunDiagnostics(stateDir, profile)
		a.update(func() {
			a.diag = res
			a.diagReady = true
			a.probing = false
		})
	}()
}

func (a *App) openMainWindow() *mygo.Window {
	opts := mygo.WindowOptions{
		Title:     L.AppName(),
		Width:     mainDefW,
		Height:    mainDefH,
		MinWidth:  mainMinW,
		MinHeight: mainMinH,
		StateKey:  "main",
		Content:   ui.View(a.view),
	}
	if runtime.GOOS == "windows" {
		opts.TitleBarStyle = mygo.TitleBarHidden
		opts.TitleBarHeight = int(2*paneMargin + paneHeaderH)
		opts.HideWindowButtons = true
		opts.Vibrancy = mygo.VibrancyMica
	}
	w := mygo.NewWindow(opts)
	w.OnClose(func(ev *mygo.CloseEvent) {
		a.mu.Lock()
		quitting := a.quitting
		hasTray := a.tray != nil
		a.mu.Unlock()
		if !quitting && hasTray && runtime.GOOS != "android" {
			ev.PreventDefault()
			w.Hide()
			return
		}
		a.mu.Lock()
		if a.window == w {
			a.window = nil
		}
		a.mu.Unlock()
	})
	return w
}

func (a *App) update(fn func()) {
	a.mu.Lock()
	fn()
	w := a.window
	a.mu.Unlock()
	if w != nil {
		w.Update(func() {})
	}
}

func (a *App) loadPrefs() {
	if a.stateDir == "" {
		return
	}
	raw, err := os.ReadFile(filepath.Join(a.stateDir, "gui.json"))
	if err != nil {
		return
	}
	var p Prefs
	if err := json.Unmarshal(raw, &p); err == nil {
		a.prefs = p
	}
}

func (a *App) savePrefs() {
	if a.stateDir == "" {
		return
	}
	_ = os.MkdirAll(a.stateDir, 0o755)
	raw, err := json.MarshalIndent(a.prefs, "", "  ")
	if err != nil {
		return
	}
	tmp := filepath.Join(a.stateDir, "gui.json.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err == nil {
		_ = os.Rename(tmp, filepath.Join(a.stateDir, "gui.json"))
	}
}

func (a *App) syncControlIndices() {
	switch a.prefs.Theme {
	case "light":
		a.themeIndex = 1
	case "dark":
		a.themeIndex = 2
	default:
		a.themeIndex = 0
	}
	switch a.prefs.Language {
	case "en":
		a.langIndex = 1
	case "zh-Hans":
		a.langIndex = 2
	default:
		a.langIndex = 0
	}
}
func (a *App) vmCard(c *ui.Context, k tokens, st vm.Status) {
	if st.NativeABI == "" {
		st.NativeABI, st.SystemImagePkg, _ = vm.NativeABIForArch(runtime.GOARCH)
	}
	if st.State == "" {
		st.State = vm.StateNotInstalled
	}

	stateLabel := L.VMStateNotInstalled()
	stateColor := k.muted
	switch st.State {
	case vm.StateDownloading:
		stateLabel = L.VMStateDownloading(st.ProgressPct)
		stateColor = k.brand
	case vm.StateStarting:
		stateLabel = L.VMStateStarting()
		stateColor = k.brand
	case vm.StateRunning:
		stateLabel = L.VMStateRunning(st.RunningSerial)
		stateColor = k.good
	case vm.StateStopped:
		stateLabel = L.VMStateStopped()
		stateColor = k.good
	case vm.StateError:
		stateLabel = L.VMStateError()
		stateColor = k.bad
	}

	hasManagedDefault := false
	for _, avd := range st.AvailableAVDs {
		if avd == vm.DefaultAVDName {
			hasManagedDefault = true
			break
		}
	}

	card(c.Key("vm-card")).Children(func() {
		ui.Row(c.Key("vm-card-head")).FillWidth().AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
			ui.Icon(c, iconDevices).Size(18, 18).TextColor(k.brand)
			ui.Text(c, L.VMCardTitle()).Bold().FontSize(sizeHeading).Grow(1)
			if st.NativeABI != "" {
				ui.Row(c.Key("vm-abi-pill")).Padding(badgePadV, badgePadH).Radius(pillRadius).Background(k.brandSoft).Children(func() {
					ui.Text(c, L.VMNativeArchBadge(st.NativeABI)).FontSize(sizeSmall).TextColor(k.brand).Bold().SingleLine()
				})
			}
			if st.StorageBytes > 0 {
				mb := max(int(st.StorageBytes>>20), 1)
				ui.Row(c.Key("vm-size-pill")).Padding(badgePadV, badgePadH).Radius(pillRadius).Background(k.brandSoft).Children(func() {
					ui.Text(c, L.VMStorageBadge(mb)).FontSize(sizeSmall).TextColor(k.muted).Bold().SingleLine()
				})
			}
			ui.Row(c.Key("vm-state-pill")).Padding(badgePadV, badgePadH).Radius(pillRadius).Background(k.brandSoft).Children(func() {
				ui.Text(c, stateLabel).FontSize(sizeSmall).TextColor(stateColor).Bold().SingleLine()
			})
		})

		ui.Text(c, L.VMCardDesc()).FontSize(sizeSmall).TextColor(k.muted)

		if len(st.AvailableAVDs) > 0 {
			ui.Text(c, strings.Join(st.AvailableAVDs, " · ")).FontSize(sizeSmall).TextColor(k.muted)
		}
		if st.ProgressMsg != "" {
			ui.Text(c, st.ProgressMsg).FontSize(sizeSmall).TextColor(k.brand)
		}
		if st.LastError != "" {
			ui.Text(c, st.LastError).FontSize(sizeSmall).TextColor(k.warn)
		}

		busy := st.State == vm.StateDownloading || st.State == vm.StateStarting
		ui.Row(c.Key("vm-actions")).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			if st.State == vm.StateRunning {
				if ui.Button(c.Key("btn-vm-stop"), L.VMActionStop()).Clicked() {
					a.StopVM()
				}
				return
			}
			if len(st.AvailableAVDs) > 0 {
				startBtn := ui.Button(c.Key("btn-vm-start"), L.VMActionStart())
				if busy {
					startBtn.Disabled(true)
				}
				if startBtn.Clicked() {
					a.StartVM(st.AVDName)
				}
			}
			if !hasManagedDefault {
				dlBtn := ui.Button(c.Key("btn-vm-download"), L.VMActionDownload())
				if busy {
					dlBtn.Disabled(true)
				}
				if dlBtn.Clicked() {
					a.ProvisionVM()
				}
			}
			if st.StorageBytes > 0 && !busy {
				if ui.Button(c.Key("btn-vm-delete"), L.VMActionDelete()).Clicked() {
					a.DeleteManagedVM()
				}
			}
		})
	})
}

func (a *App) companionCard(c *ui.Context, k tokens, connected bool, st companion.DeviceStatus, notice string) {
	card(c.Key("companion-card")).Children(func() {
		ui.Row(c.Key("companion-head")).FillWidth().AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
			ui.Icon(c, iconWifi).Size(18, 18).TextColor(k.brand)
			ui.Text(c, L.CompanionCardTitle()).Bold().FontSize(sizeHeading).Grow(1)
			if connected {
				fg := st.ForegroundPackage
				if fg == "" {
					fg = "--"
				}
				summary := L.CompanionConnectedSummary(st.BatteryPct, fmt.Sprintf("%.1f", st.TempC), fg)
				ui.Row(c.Key("comp-status-pill")).Padding(badgePadV, badgePadH).Radius(pillRadius).Background(k.brandSoft).Children(func() {
					ui.Text(c, summary).FontSize(sizeSmall).TextColor(k.good).Bold().SingleLine()
				})
			}
		})
		ui.Text(c, L.CompanionCardDesc()).FontSize(sizeSmall).TextColor(k.muted)

		if notice != "" {
			ui.Text(c, notice).FontSize(sizeSmall).TextColor(k.warn)
		}

		ui.Row(c.Key("comp-connect-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("comp-addr"), &a.compAddr).Placeholder(L.CompanionAddrHint()).Grow(2)
			ui.TextInput(c.Key("comp-token"), &a.compToken).Placeholder(L.CompanionTokenHint()).Grow(1)
			if connected {
				if ui.Button(c.Key("btn-comp-disc"), L.CompanionDisconnectBtn()).Clicked() {
					a.DisconnectCompanion()
				}
			} else {
				if ui.Button(c.Key("btn-comp-conn"), L.CompanionConnectBtn()).Clicked() {
					a.ConnectCompanion(a.compAddr, a.compToken)
				}
			}
		})

		if !connected {
			a.mu.Lock()
			devs := append([]DiscoveredDevice(nil), a.devices...)
			meshName := a.rt.meshCfg.NetworkName
			a.mu.Unlock()
			if meshName != "" {
				ui.Checkbox(c.Key("comp-join-mesh"), &a.compJoinMesh, L.CompanionJoinMesh(meshName))
			}
			ui.Row(c.Key("comp-pair-row")).Gap(8).Wrap().Children(func() {
				for _, d := range devs {
					if d.Target.State != "device" {
						continue
					}
					model := d.Profile.Model
					if model == "" {
						model = d.Target.Model
					}
					if ui.Button(c.Key("comp-pair-"+d.HardwareHash), L.CompanionInstallPair(model)).Clicked() {
						a.PairCompanionOverADB(d.Target.Serial, model, a.compJoinMesh && meshName != "")
					}
				}
				if !a.bundledAPK && ui.Button(c.Key("comp-choose-apk"), L.CompanionChooseApk()).Clicked() {
					a.ChooseCompanionAPK()
				}
			})
		}

		if connected {
			ui.Row(c.Key("comp-actions-row")).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				if st.ForegroundPackage != "" {
					if ui.Button(c.Key("btn-comp-cont"), L.CompanionContinueApp()).Clicked() {
						a.ContinueCompanionApp()
					}
				}
				if st.WirelessADBPort > 0 {
					if ui.Button(c.Key("btn-comp-beacon"), L.CompanionConnectBeaconADB()).Clicked() {
						a.ConnectCompanionBeaconADB()
					}
				}
				if ui.Button(c.Key("btn-comp-guard"), L.CompanionRunDisplayGuard()).Clicked() {
					a.RunCompanionDisplayGuard()
				}
				a.mu.Lock()
				micActive := a.micUplinkActive
				micNotice := a.micUplinkNotice
				a.mu.Unlock()
				if !micActive {
					if ui.Button(c.Key("btn-comp-mic"), L.CompanionStartMicBtn()).Clicked() {
						a.StartCompanionMicUplink()
					}
				} else {
					if ui.Button(c.Key("btn-comp-mic-stop"), L.CompanionStopMicBtn()).Clicked() {
						a.StopCompanionMicUplink()
					}
				}
				if micNotice != "" {
					ui.Text(c, micNotice).FontSize(sizeSmall).TextColor(k.warn)
				}
			})
		}
	})
}

func (a *App) appIconFor(iconPath string) *ui.Bitmap {
	if iconPath == "" {
		return nil
	}
	a.mu.Lock()
	bmp := a.appIcons[iconPath]
	a.mu.Unlock()
	return bmp
}

func (a *App) appLauncherSection(c *ui.Context, k tokens, devKey string, d DiscoveredDevice) {
	t := c.Theme()
	a.mu.Lock()
	showSys := a.showSystemApps
	a.mu.Unlock()

	userCount := 0
	for _, ap := range d.Apps {
		if !ap.System {
			userCount++
		}
	}
	includeSystem := showSys || userCount == 0

	filter := strings.ToLower(strings.TrimSpace(a.appFilter))
	var visible []adb.AppEntry
	for _, ap := range d.Apps {
		if !includeSystem && ap.System && filter == "" {
			continue
		}
		if filter != "" &&
			!strings.Contains(strings.ToLower(ap.Label), filter) &&
			!strings.Contains(strings.ToLower(ap.Package), filter) {
			continue
		}
		visible = append(visible, ap)
	}

	ui.Column(c.Key(devKey + "-launcher")).FillWidth().Gap(10).Children(func() {
		ui.Row(c.Key(devKey + "-appbar")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconApps).Size(16, 16).TextColor(k.brand)
			ui.Text(c, L.AppLauncherTitle()).Bold().FontSize(sizeBody)
			ui.Row(c).Padding(badgePadV, badgePadH).Radius(pillRadius).Background(k.brandSoft).Children(func() {
				ui.Text(c, L.AppCountBadge(len(visible))).FontSize(sizeSmall).TextColor(k.brand).Bold().SingleLine()
			})
			ui.TextInput(c.Key(devKey+"-filter"), &a.appFilter).Placeholder(L.AppFilterHint()).Grow(1)
			if userCount > 0 {
				sysLbl := L.AppShowSystem()
				if showSys {
					sysLbl = L.AppHideSystem()
				}
				if ui.Button(c.Key(devKey+"-togglesys"), sysLbl).Clicked() {
					a.mu.Lock()
					a.showSystemApps = !a.showSystemApps
					a.mu.Unlock()
				}
			}
		})

		const maxVisibleTiles = 48
		shown := visible
		if len(shown) > maxVisibleTiles {
			shown = shown[:maxVisibleTiles]
		}
		ui.Row(c.Key(devKey + "-appgrid")).FillWidth().Gap(8).Wrap().Children(func() {
			for _, ap := range shown {
				appItem := ap
				tip := appItem.Label + " (" + appItem.Package + ")"
				if !appItem.Resizeable {
					tip += " · " + L.AppUnresizeableNote()
				}
				tile := ui.ButtonBase(c.Key(devKey+"-tile-"+appItem.Package)).
					Width(appTileW).
					Padding(appTilePadV, appTilePadH).
					Radius(appTileRadius).
					Center().
					Label(appItem.Label).
					Tooltip(tip)
				if tile.Hovered() {
					tile.Background(t.SurfaceHover)
				} else {
					tile.Background(t.Background)
				}
				tile.Border(1, t.Border)
				tile.Children(func() {
					ui.Column(c).Gap(6).AlignItems(ui.Center).FillWidth().Children(func() {
						if bmp := a.appIconFor(appItem.IconPath); bmp != nil {
							ui.Image(c, bmp).Size(appTileIconSz, appTileIconSz).Radius(10)
						} else {
							ui.Box(c).Size(appTileIconSz, appTileIconSz).Radius(10).Background(k.brandSoft).Center().Children(func() {
								ui.Text(c, appInitial(appItem.Label)).Bold().FontSize(sizeHeading).TextColor(k.brand)
							})
						}
						ui.Text(c, appItem.Label).FontSize(sizeSmall).SingleLine()
					})
				})
				if tile.Clicked() {
					a.LaunchAppWindow(d, appItem, OpenDefault)
				}
			}
		})
	})
}

func appInitial(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "?"
	}
	r, _ := utf8.DecodeRuneInString(label)
	if r == utf8.RuneError {
		return "?"
	}
	return strings.ToUpper(string(r))
}
func (a *App) avBridgeCard(c *ui.Context, k tokens) {
	a.mu.Lock()
	facingIdx := a.cameraFacingIdx
	bridgeURL := a.cameraBridgeURL
	vcam := a.vcamCap
	a.mu.Unlock()

	card(c.Key("mode-av-bridge")).Children(func() {
		ui.Text(c, L.AVBridgeCardTitle()).Bold().FontSize(sizeHeading)
		ui.Text(c, L.AVBridgeCardDesc()).FontSize(sizeSmall).TextColor(k.muted)

		ui.Row(c.Key("av-facing-row")).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, L.AVCameraFacingLabel()).FontSize(sizeBody).Grow(1)
			seg := ui.Segmented(c.Key("av-cam-facing"), &facingIdx, L.AVCameraBack(), L.AVCameraFront())
			if seg.Changed() {
				a.mu.Lock()
				a.cameraFacingIdx = facingIdx
				a.mu.Unlock()
			}
		})

		if vcam.MFVirtualCameraAPI {
			ui.Text(c, L.AVVirtualCamAPIReady()).FontSize(sizeSmall).TextColor(k.good)
		} else {
			ui.Text(c, L.AVVirtualCamAPIMissing()).FontSize(sizeSmall).TextColor(k.muted)
		}
		if bridgeURL != "" {
			ui.Text(c, L.AVBridgeLocalURL(bridgeURL)).FontSize(sizeSmall).TextColor(k.brand)
		}
		ui.Text(c, L.AVUplinkNote()).FontSize(sizeSmall).TextColor(k.muted)
	})
}

func (a *App) diagnosticsPage(c *ui.Context, k tokens) {
	a.mu.Lock()
	probing, ready, d := a.probing, a.diagReady, a.diag
	a.mu.Unlock()

	yesNo := func(ok bool) string {
		if ok {
			return L.DiagStatusYes()
		}
		return L.DiagStatusNo()
	}

	ui.Column(c.Key("page-diag")).Gap(14).FillWidth().Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Text(c, L.DiagSubtitle()).FontSize(sizeBody).TextColor(k.muted).Grow(1)
			btnLabel := L.DiagRefresh()
			if probing {
				btnLabel = L.DiagRefreshing()
			}
			b := ui.Button(c.Key("diag-refresh"), btnLabel)
			if probing {
				b.Disabled(true)
			}
			if b.Clicked() {
				a.RefreshDiagnostics()
			}
		})

		card(c.Key("diag-card")).Children(func() {
			archVal := L.DiagArchNative(d.SelfCheck.NativeArch, d.SelfCheck.OS, int(d.SelfCheck.OSBuild))
			if d.SelfCheck.IsEmulated {
				archVal = L.DiagArchEmulated(d.SelfCheck.ProcessArch, d.SelfCheck.NativeArch)
			}
			diagRow(c, k, L.DiagArchLabel(), archVal, !d.SelfCheck.IsEmulated)

			coreVal := L.DiagCoreMissing()
			if d.CoreLoaded {
				coreVal = L.DiagCoreLoaded(int(d.CoreABI))
			} else if d.CoreError != "" {
				coreVal = d.CoreError
			}
			diagRow(c, k, L.DiagCoreLabel(), coreVal, d.CoreLoaded)

			videoVal := L.DiagVideoSummary(yesNo(d.SelfCheck.H264HWDecoder), yesNo(d.SelfCheck.HEVCHWDecoder))
			diagRow(c, k, L.DiagVideoLabel(), videoVal, d.SelfCheck.MediaFoundationPresent && (d.SelfCheck.H264HWDecoder || d.SelfCheck.H264AnyDecoder))

			wfdVal := L.DiagWfdSummary(yesNo(d.SelfCheck.WiFiDirectAPI), yesNo(d.SelfCheck.MiracastIEProvider))
			diagRow(c, k, L.DiagWfdLabel(), wfdVal, d.SelfCheck.WiFiDirectAPI)

			adbVal := L.DiagAdbMissing()
			if d.ADBFound {
				adbVal = L.DiagAdbFound(d.ADBVersion)
			}
			diagRow(c, k, L.DiagAdbLabel(), adbVal, d.ADBFound)
		})

		if ready && len(d.SelfCheck.Notes) > 0 {
			card(c.Key("diag-notes")).Children(func() {
				ui.Text(c, L.DiagNotesTitle()).Bold().FontSize(sizeBody)
				for i, n := range d.SelfCheck.Notes {
					ui.Text(c.Key(i), "• "+n).FontSize(sizeSmall).TextColor(k.muted)
				}
			})
		}
	})
}

func diagRow(c *ui.Context, k tokens, label, value string, ok bool) {
	dotColor := k.warn
	if ok {
		dotColor = k.good
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Padding(4, 0).Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(dotColor).Shrink(0)
		ui.Text(c, label).Bold().FontSize(sizeBody).Width(240).Shrink(0)
		ui.Text(c, value).FontSize(sizeBody).TextColor(k.muted).Grow(1)
	})
}
func (a *App) mcpSettingsCard(c *ui.Context, k tokens) {
	a.mu.Lock()
	st := a.mcpStatus
	allowLaunch := a.mcpAllowLaunch
	notice := a.mcpNotice
	a.mu.Unlock()

	card(c.Key("settings-mcp")).Children(func() {
		ui.Text(c, L.MCPCardTitle()).Bold().FontSize(sizeBody)
		ui.Text(c, L.MCPCardDesc()).FontSize(sizeSmall).TextColor(k.muted)

		if st.Enabled {
			ui.Text(c, L.MCPStatusRunning(st.Addr)).FontSize(sizeBody).TextColor(k.good)
			if st.Token != "" {
				ui.Text(c, L.MCPTokenLabel(st.Token)).FontSize(sizeSmall).TextColor(k.muted)
			}
		} else {
			ui.Text(c, L.MCPStatusDisabled()).FontSize(sizeBody).TextColor(k.muted)
		}

		ui.Row(c.Key("mcp-allow-row")).AlignItems(ui.Center).Gap(8).Children(func() {
			sw := ui.Switch(c.Key("mcp-allow-launch"), &allowLaunch)
			if sw.Changed() {
				a.mu.Lock()
				a.mcpAllowLaunch = allowLaunch
				enabled := a.mcpStatus.Enabled
				a.mu.Unlock()
				if enabled {
					go a.EnableMCP(allowLaunch)
				}
			}
			ui.Text(c, L.MCPAllowLaunchLabel()).FontSize(sizeBody)
		})

		if notice != "" {
			ui.Text(c, notice).FontSize(sizeSmall).TextColor(k.warn)
		}

		ui.Row(c.Key("mcp-actions-row")).Gap(8).Children(func() {
			if !st.Enabled {
				if ui.Button(c.Key("mcp-enable-btn"), L.MCPEnableBtn()).Clicked() {
					go a.EnableMCP(allowLaunch)
				}
			} else {
				if ui.Button(c.Key("mcp-stop-btn"), L.MCPEmergencyStopBtn()).Clicked() {
					go a.EmergencyStopMCP()
				}
			}
		})

		if len(st.Audit) > 0 {
			ui.Text(c, L.MCPAuditTitle()).Bold().FontSize(sizeSmall).TextColor(k.muted)
			start := max(0, len(st.Audit)-4)
			for i, entry := range st.Audit[start:] {
				rowText := fmt.Sprintf("%s · %s %s -> %s",
					entry.Timestamp.Format("15:04:05"), entry.Method, entry.Tool, entry.Outcome)
				ui.Text(c.Key(fmt.Sprintf("mcp-audit-%d", i)), rowText).FontSize(sizeSmall).TextColor(k.muted)
			}
		}
	})
}

func (a *App) meshSettingsCard(c *ui.Context, k tokens) {
	a.mu.Lock()
	st := a.meshStatus
	notice := a.meshNotice
	a.mu.Unlock()

	card(c.Key("settings-mesh")).Children(func() {
		ui.Text(c, L.MeshCardTitle()).Bold().FontSize(sizeBody)
		ui.Text(c, L.MeshCardDesc()).FontSize(sizeSmall).TextColor(k.muted)

		if st.Running {
			ui.Text(c, L.MeshStatusRunning(meshDeviceName)).FontSize(sizeBody).TextColor(k.good)
		} else {
			ui.Text(c, L.MeshStatusStopped()).FontSize(sizeBody).TextColor(k.muted)
		}
		if st.LastError != "" {
			ui.Text(c, st.LastError).FontSize(sizeSmall).TextColor(k.warn)
		}

		ui.Row(c.Key("mesh-fields-row")).FillWidth().Gap(8).Children(func() {
			ui.TextInput(c.Key("mesh-net"), &a.meshNetName).Placeholder(L.MeshNetNameHint()).Grow(1)
			ui.TextInput(c.Key("mesh-sec"), &a.meshSecret).Placeholder(L.MeshSecretHint()).Grow(1)
			ui.TextInput(c.Key("mesh-peer"), &a.meshPeer).Placeholder(L.MeshPeerHint()).Grow(1)
		})
		ui.Checkbox(c.Key("mesh-accept"), &a.meshAccept, L.MeshAcceptLabel())
		if st.Running {
			a.mu.Lock()
			port := 0
			if p := a.prefs.Mesh; p != nil && p.Accept {
				port = p.ListenPort
			}
			a.mu.Unlock()
			if port > 0 {
				ui.Text(c, L.MeshListenPort(port)).FontSize(sizeSmall).TextColor(k.muted)
			}
		}

		if notice != "" {
			ui.Text(c, notice).FontSize(sizeSmall).TextColor(k.warn)
		}

		ui.Row(c.Key("mesh-actions-row")).Gap(8).Children(func() {
			if !st.Running {
				if ui.Button(c.Key("mesh-start-btn"), L.MeshStartBtn()).Clicked() {
					a.mu.Lock()
					netName := a.meshNetName
					secret := a.meshSecret
					peer := a.meshPeer
					accept := a.meshAccept
					a.mu.Unlock()
					go a.StartMesh(netName, secret, peer, accept)
				}
			} else {
				if ui.Button(c.Key("mesh-stop-btn"), L.MeshStopBtn()).Clicked() {
					go a.StopMesh()
				}
			}
		})
	})
}
