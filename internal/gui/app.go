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
	"github.com/YuanZhe-99/AndroFuse/internal/dex"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

type navPage string

const (
	pageDevices     navPage = "devices"
	pageModes       navPage = "modes"
	pageDiagnostics navPage = "diagnostics"
	pageSettings    navPage = "settings"
	pageAbout       navPage = "about"
)

// Prefs stores persistent GUI preferences in stateDir/gui.json.
type Prefs struct {
	Theme    string `json:"theme,omitempty"`
	Language string `json:"language,omitempty"`
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

	rt              guiRuntime
	scanningDevices bool
	devices         []DiscoveredDevice
	mdnsServices    []adb.MDNSService
	deviceNotice    string
	manualAddr      string
	pairAddr        string
	pairCode        string
	appFilter       string
	showSystemApps  bool
	appIcons        map[string]*ui.Bitmap
	dexRoutes       map[string]dex.RoutePreference
	sessions        map[string]*SessionWindow
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
		profile:   profile,
		stateDir:  stateDir,
		prefs:     Prefs{Theme: "system"},
		page:      string(pageDevices),
		appIcons:  make(map[string]*ui.Bitmap),
		dexRoutes: make(map[string]dex.RoutePreference),
		sessions:  make(map[string]*SessionWindow),
	}
	a.loadPrefs()
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
		Width:     1080,
		Height:    700,
		MinWidth:  780,
		MinHeight: 540,
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

func (a *App) view(c *ui.Context) {
	t := c.Theme()
	k := tok(c)
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	} else {
		c.Root().Background(k.canvas)
	}

	w, _ := c.Size()
	narrow := w < mobileBreak || runtime.GOOS == "android"

	if narrow {
		ui.Column(c.Key("mobile-shell")).Fill().Background(t.Background).Children(func() {
			ui.Scroll(c.Key("mobile-content")).Grow(1).FillWidth().Padding(16).Children(func() {
				a.pageBody(c, k)
			})
			a.bottomNav(c, k)
		})
		return
	}

	ui.Row(c.Key("desktop-shell")).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebar(c, k)
		ui.Column(c.Key("main-pane-wrap")).Grow(1).MinWidth(0).MinHeight(0).Padding(paneMargin, paneMargin, paneMargin, 0).Children(func() {
			ui.Column(c.Key("main-pane")).Grow(1).FillWidth().MinHeight(0).Radius(paneRadius).Clip().
				Background(t.Background).Border(1, t.Border).Shadow(0, 6, 18, 0, paneShadow).Children(func() {
				a.paneHeader(c)
				ui.Scroll(c.Key("pane-scroll")).Grow(1).FillWidth().Padding(20).Children(func() {
					a.pageBody(c, k)
				})
			})
		})
	})
}

func (a *App) sidebar(c *ui.Context, k tokens) {
	t := c.Theme()
	a.mu.Lock()
	selected := a.page
	a.mu.Unlock()

	ui.Column(c.Key("sidebar")).Width(sidebarW).Shrink(0).FillHeight().Padding(14, 12, 14, 12).Gap(12).Children(func() {
		ui.Row(c.Key("brand-row")).Gap(10).AlignItems(ui.Center).Padding(4, 6).DragWindow().Children(func() {
			if appIconBitmap != nil {
				ui.Image(c, appIconBitmap).Size(28, 28).Radius(7)
			}
			ui.Column(c.Key("brand-text")).Gap(1).Children(func() {
				ui.Text(c, L.AppName()).Bold().FontSize(sizeHeading)
				ui.Text(c, "v"+version.Version).FontSize(sizeSmall).TextColor(k.muted)
			})
		})

		sb := ui.Sidebar(c.Key("nav-sidebar"), &selected, func() {
			ui.SidebarSection(c.Key("nav-main"), "", nil, func() {
				ui.SidebarItem(c, string(pageDevices), iconDevices, L.NavDevices())
				ui.SidebarItem(c, string(pageModes), iconModes, L.NavModes())
				ui.SidebarItem(c, string(pageDiagnostics), iconDiagnostics, L.NavDiagnostics())
			})
			ui.SidebarSection(c.Key("nav-app"), "", nil, func() {
				ui.SidebarItem(c, string(pageSettings), iconSettings, L.NavSettings())
				ui.SidebarItem(c, string(pageAbout), iconInfo, L.NavAbout())
			})
		}).Grow(1).FillWidth()
		if sb.Changed() {
			a.mu.Lock()
			a.page = selected
			a.mu.Unlock()
		}

		ui.Row(c.Key("sidebar-foot")).Padding(8, 10).Radius(10).Background(t.Surface).Border(1, t.Border).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconShield).Size(14, 14).TextColor(k.brand)
			ui.Text(c, L.DevicesBadgePlanned()).FontSize(sizeSmall).TextColor(k.muted).SingleLine()
		})
	})
}

func (a *App) bottomNav(c *ui.Context, k tokens) {
	t := c.Theme()
	a.mu.Lock()
	cur := a.page
	a.mu.Unlock()

	items := []struct {
		id    navPage
		icon  *ui.SVG
		label string
	}{
		{pageDevices, iconDevices, L.NavDevices()},
		{pageModes, iconModes, L.NavModes()},
		{pageDiagnostics, iconDiagnostics, L.NavDiagnostics()},
		{pageSettings, iconSettings, L.NavSettings()},
	}
	ui.Row(c.Key("mobile-bottom-nav")).FillWidth().Padding(8, 12).Gap(6).
		Background(t.Surface).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
		for _, it := range items {
			active := cur == string(it.id)
			b := ui.ButtonBase(c.Key("mnav-"+string(it.id))).Grow(1).Radius(10).Padding(8, 6).Center().Label(it.label)
			fg := k.muted
			if active {
				b.Background(k.brandSoft)
				fg = k.brand
			}
			b.Children(func() {
				ui.Column(c).Gap(3).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, it.icon).Size(16, 16).TextColor(fg)
					ui.Text(c, it.label).FontSize(sizeSmall).TextColor(fg).SingleLine()
				})
			})
			if b.Clicked() {
				a.mu.Lock()
				a.page = string(it.id)
				a.mu.Unlock()
			}
		}
	})
}

func (a *App) paneHeader(c *ui.Context) {
	t := c.Theme()
	title := a.currentTitle()
	ui.Row(c.Key("pane-head")).Height(paneHeaderH).Shrink(0).FillWidth().Padding(0, 8, 0, 18).
		AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).Bold().FontSize(sizeHeading).DragWindow()
		ui.Box(c.Key("pane-drag")).Grow(1).FillHeight().DragWindow()
		a.windowButtons(c)
	})
	ui.Box(c.Key("pane-divider")).Height(1).FillWidth().Background(t.Border)
}

func (a *App) windowButtons(c *ui.Context) {
	if runtime.GOOS != "windows" {
		return
	}
	t := c.Theme()
	a.mu.Lock()
	win := a.window
	a.mu.Unlock()

	btn := func(name string, glyph *ui.SVG) ui.Element {
		b := ui.ButtonBase(c.Key("win-"+name)).Size(winButtonW, winButtonH).Radius(6).Center().Shrink(0).Label(name)
		fg := t.TextMuted
		if b.Hovered() {
			if name == "close" {
				b.Background(closeHover)
				fg = closeHoverText
			} else {
				b.Background(t.SurfaceHover)
				fg = t.Text
			}
		}
		b.Children(func() { ui.Icon(c, glyph).Size(winGlyph, winGlyph).TextColor(fg) })
		return b
	}

	if btn("min", winMinimize).Clicked() && win != nil {
		win.Minimize()
	}
	maximized := win != nil && win.IsMaximized()
	maxGlyph := winMaximize
	if maximized {
		maxGlyph = winRestore
	}
	if btn("max", maxGlyph).MaximizeButton().Clicked() && win != nil {
		if maximized {
			win.Unmaximize()
		} else {
			win.Maximize()
		}
	}
	if btn("close", winClose).Clicked() && win != nil {
		win.Close()
	}
}

func (a *App) currentTitle() string {
	a.mu.Lock()
	p := a.page
	a.mu.Unlock()
	switch navPage(p) {
	case pageModes:
		return L.ModesTitle()
	case pageDiagnostics:
		return L.DiagTitle()
	case pageSettings:
		return L.SettingsTitle()
	case pageAbout:
		return L.AboutTitle()
	default:
		return L.DevicesTitle()
	}
}

func (a *App) pageBody(c *ui.Context, k tokens) {
	a.mu.Lock()
	p := navPage(a.page)
	a.mu.Unlock()
	switch p {
	case pageModes:
		a.modesPage(c, k)
	case pageDiagnostics:
		a.diagnosticsPage(c, k)
	case pageSettings:
		a.settingsPage(c, k)
	case pageAbout:
		a.aboutPage(c, k)
	default:
		a.devicesPage(c, k)
	}
}

func (a *App) devicesPage(c *ui.Context, k tokens) {
	a.mu.Lock()
	scanning := a.scanningDevices
	devs := append([]DiscoveredDevice(nil), a.devices...)
	mdns := append([]adb.MDNSService(nil), a.mdnsServices...)
	notice := a.deviceNotice
	sessList := make([]*SessionWindow, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessList = append(sessList, s)
	}
	a.mu.Unlock()

	ui.Column(c.Key("page-devices")).Gap(14).FillWidth().Children(func() {
		ui.Row(c.Key("devices-topbar")).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, L.DevicesSubtitle()).FontSize(sizeBody).TextColor(k.muted).Grow(1)
			scanLbl := L.DevicesRefresh()
			if scanning {
				scanLbl = L.DevicesRefreshing()
			}
			rb := ui.Button(c.Key("btn-scan-devices"), scanLbl)
			if scanning {
				rb.Disabled(true)
			}
			if rb.Clicked() {
				a.RefreshDevices()
			}
		})

		if notice != "" {
			card(c.Key("devices-notice")).Children(func() {
				ui.Text(c, notice).FontSize(sizeSmall).TextColor(k.warn)
			})
		}

		if len(sessList) > 0 {
			card(c.Key("active-sessions-card")).Children(func() {
				ui.Text(c, L.ActiveSessionsTitle()).Bold().FontSize(sizeHeading)
				for _, sw := range sessList {
					sw.mu.Lock()
					nw, nh, fps := sw.negotiatedW, sw.negotiatedH, sw.measuredFPS
					sessTransport := sw.spec.Transport
					sw.mu.Unlock()
					res := "--"
					if nw > 0 && nh > 0 {
						res = fmt.Sprintf("%d×%d · %.1f fps", nw, nh, fps)
					}
					ui.Row(c.Key("sess-row-" + sw.spec.ID)).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Row(c).Padding(badgePadV, badgePadH).Radius(pillRadius).Background(k.brandSoft).Children(func() {
							ui.Text(c, sessTransport).FontSize(sizeSmall).TextColor(k.brand).Bold().SingleLine()
						})
						ui.Text(c, sw.spec.Title).FontSize(sizeBody).Bold().Grow(1).SingleLine()
						ui.Text(c, res).FontSize(sizeSmall).TextColor(k.muted).SingleLine()
						if ui.Button(c.Key("focus-"+sw.spec.ID), L.ActionFocusWindow()).Clicked() {
							sw.FocusWindow()
						}
						if ui.Button(c.Key("close-"+sw.spec.ID), L.ActionDisconnect()).Clicked() {
							sw.Close()
						}
					})
				}
			})
		}

		card(c.Key("wireless-card")).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconWifi).Size(18, 18).TextColor(k.brand)
				ui.Text(c, L.WirelessCardTitle()).Bold().FontSize(sizeHeading)
			})
			ui.Text(c, L.WirelessCardDesc()).FontSize(sizeSmall).TextColor(k.muted)

			if len(mdns) > 0 {
				ui.Row(c.Key("mdns-chips")).Gap(8).Wrap().Children(func() {
					for i, s := range mdns {
						if s.CanConnect {
							if ui.Button(c.Key(fmt.Sprintf("mdns-conn-%d", i)), L.WirelessMdnsConnect(s.MaskedAddr)).Clicked() {
								a.ConnectWirelessTLS(s.Addr)
							}
						} else if s.CanPair {
							if ui.Button(c.Key(fmt.Sprintf("mdns-pair-%d", i)), L.WirelessMdnsPairFound(s.MaskedAddr)).Clicked() {
								a.mu.Lock()
								a.pairAddr = s.Addr
								a.mu.Unlock()
							}
						}
					}
				})
			}

			ui.Row(c.Key("manual-tls-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("manual-addr"), &a.manualAddr).Placeholder(L.WirelessManualAddrHint()).Grow(1)
				if ui.Button(c.Key("btn-manual-tls"), L.WirelessConnectBtn()).Clicked() {
					a.ConnectWirelessTLS(a.manualAddr)
				}
			})
			ui.Row(c.Key("pair-tls-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("pair-addr"), &a.pairAddr).Placeholder(L.WirelessPairAddrHint()).Grow(2)
				ui.TextInput(c.Key("pair-code"), &a.pairCode).Placeholder(L.WirelessPairCodeHint()).Grow(1)
				if ui.Button(c.Key("btn-pair-tls"), L.WirelessPairBtn()).Clicked() {
					a.PairWirelessTLS(a.pairAddr, a.pairCode)
				}
			})
		})

		if len(devs) == 0 {
			card(c.Key("devices-empty")).Children(func() {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconDevices).Size(20, 20).TextColor(k.brand)
					ui.Text(c, L.DevicesEmptyTitle()).Bold().FontSize(sizeHeading)
				})
				ui.Text(c, L.DevicesEmptyBody()).FontSize(sizeBody).TextColor(k.muted)
			})
			return
		}

		for _, dev := range devs {
			d := dev
			hwKey := d.HardwareHash
			if hwKey == "" {
				hwKey = d.Target.DeviceHash
			}
			devKey := "dev-" + hwKey
			transports := d.Transports
			if len(transports) == 0 {
				transports = []adb.Device{d.Target}
			}
			usedStreams, limitStreams, atLimit, nearLimit := EncoderHeadroom(d.Profile, a.activeStreamCountFor(hwKey))
			route := d.Route
			if route.Label == "" {
				route = dex.SelectRoute(d.Profile, d.DexProbe, d.Target.Transport, a.dexRoutePref(hwKey))
			}
			var wirelessSerial string
			for _, tr := range transports {
				if tr.State == "device" && (tr.Transport == adb.TransportTLSWiFi || tr.Transport == adb.TransportTCP) {
					wirelessSerial = tr.Serial
					break
				}
			}

			card(c.Key(devKey)).Children(func() {
				title := d.Target.Model
				if title == "" {
					title = d.Profile.Model
				}
				if title == "" {
					title = d.Target.MaskedID
				}
				tIcon := iconUsb
				if d.Target.Transport == adb.TransportTLSWiFi || d.Target.Transport == adb.TransportTCP {
					tIcon = iconWifi
				}
				ui.Row(c.Key(devKey + "-head")).FillWidth().AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
					ui.Icon(c, tIcon).Size(18, 18).TextColor(k.brand)
					ui.Text(c, title).Bold().FontSize(sizeHeading)
					ui.Text(c, d.Target.MaskedID).FontSize(sizeSmall).TextColor(k.muted).Grow(1)
					for _, tr := range transports {
						ui.Row(c.Key(devKey+"-tr-"+string(tr.Transport)+"-"+tr.DeviceHash)).
							Padding(badgePadV, badgePadH).Radius(pillRadius).Background(k.brandSoft).Children(func() {
							ui.Text(c, transportLabel(tr.Transport)).FontSize(sizeSmall).TextColor(k.brand).Bold().SingleLine()
						})
					}
					if len(transports) > 1 {
						ui.Row(c.Key(devKey+"-handoff")).Padding(badgePadV, badgePadH).Radius(pillRadius).Background(k.brandSoft).Children(func() {
							ui.Text(c, L.TransportHandoffReady()).FontSize(sizeSmall).TextColor(k.good).Bold().SingleLine()
						})
					}
					if d.Probed {
						streamColor := k.muted
						if nearLimit || atLimit {
							streamColor = k.warn
						}
						ui.Row(c.Key(devKey+"-enc")).Padding(badgePadV, badgePadH).Radius(pillRadius).Background(k.brandSoft).Children(func() {
							ui.Text(c, L.EncoderStreamsBadge(usedStreams, limitStreams)).FontSize(sizeSmall).TextColor(streamColor).Bold().SingleLine()
						})
					}
				})

				if d.Target.State != "device" {
					ui.Text(c, d.Target.State).FontSize(sizeSmall).TextColor(k.warn)
					return
				}
				if d.Probed {
					meta := fmt.Sprintf("%s · Android %s (SDK %d)", d.Profile.Manufacturer, d.Profile.AndroidRelease, d.Profile.SDKInt)
					if uiVer := dex.FormatOneUIVersion(d.Profile.OneUIVersion); uiVer != "" {
						meta += " · One UI " + uiVer
					}
					meta += " · " + route.Label
					ui.Text(c, meta).FontSize(sizeSmall).TextColor(k.muted)
					if route.Reason != "" {
						ui.Text(c, route.Reason).FontSize(sizeSmall).TextColor(k.muted)
					}
				} else if d.ProbeErr != "" {
					ui.Text(c, d.ProbeErr).FontSize(sizeSmall).TextColor(k.warn)
				}

				if d.Probed && route.CanUseNativeDeX && d.Target.Transport != adb.TransportEmulator {
					pref := a.dexRoutePref(hwKey)
					routeIdx := 0
					switch pref {
					case dex.RouteNativeDeX:
						routeIdx = 1
					case dex.RouteVirtualFallback:
						routeIdx = 2
					}
					ui.Row(c.Key(devKey + "-route-row")).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, L.DeXRouteLabel()).FontSize(sizeSmall).TextColor(k.muted)
						rseg := ui.Segmented(c.Key(devKey+"-route-seg"), &routeIdx, L.DeXRouteAuto(), L.DeXRouteNative(), L.DeXRouteFallback())
						if rseg.Changed() {
							prefs := []dex.RoutePreference{dex.RouteAuto, dex.RouteNativeDeX, dex.RouteVirtualFallback}
							a.setDexRoutePref(hwKey, prefs[routeIdx])
						}
					})
				}

				ui.Row(c.Key(devKey + "-modes")).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
					if route.Backend == dex.BackendADBLoopback && route.IsGenuineDeX && d.Target.Transport != adb.TransportEmulator {
						if ui.Button(c.Key(devKey+"-dex"), L.ActionLaunchDeX()).Children(func() {
							ui.Icon(c, iconDex).Size(14, 14)
						}).Clicked() {
							a.LaunchDeX(d, false)
						}
					}
					if ui.Button(c.Key(devKey+"-fallback"), L.ActionLaunchFallback()).Clicked() {
						a.LaunchDeX(d, true)
					}
					if ui.Button(c.Key(devKey+"-mirror"), L.ActionLaunchMirror()).Clicked() {
						a.LaunchMirror(d)
					}
					if wirelessSerial != "" {
						if ui.Button(c.Key(devKey+"-disc"), L.ActionDisconnect()).Clicked() {
							a.DisconnectWireless(wirelessSerial)
						}
					}
				})

				if len(d.Apps) > 0 {
					a.appLauncherSection(c, k, devKey, d)
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
					a.LaunchAppWindow(d, appItem)
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

func (a *App) modesPage(c *ui.Context, k tokens) {
	a.mu.Lock()
	devs := append([]DiscoveredDevice(nil), a.devices...)
	a.mu.Unlock()

	modes := []struct {
		key, title, badge, desc string
	}{
		{"dex", L.ModeDexTitle(), L.ModeDexBadge(), L.ModeDexDesc()},
		{"mirror", L.ModeMirrorTitle(), L.ModeMirrorBadge(), L.ModeMirrorDesc()},
		{"apps", L.ModeAppsTitle(), L.ModeAppsBadge(), L.ModeAppsDesc()},
	}
	ui.Column(c.Key("page-modes")).Gap(14).FillWidth().Children(func() {
		ui.Text(c, L.ModesSubtitle()).FontSize(sizeBody).TextColor(k.muted)
		for _, m := range modes {
			card(c.Key("mode-" + m.key)).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, m.title).Bold().FontSize(sizeHeading).Grow(1)
					ui.Row(c).Padding(4, 10).Radius(999).Background(k.brandSoft).Children(func() {
						ui.Text(c, m.badge).FontSize(sizeSmall).TextColor(k.brand).Bold()
					})
				})
				ui.Text(c, m.desc).FontSize(sizeBody).TextColor(k.muted)
			})
		}
		card(c.Key("mode-live-coexist")).Children(func() {
			ui.Text(c, L.ModesLiveStatusTitle()).Bold().FontSize(sizeHeading)
			if len(devs) == 0 {
				ui.Text(c, L.ModesLiveStatusEmpty()).FontSize(sizeBody).TextColor(k.muted)
			} else {
				for _, d := range devs {
					hwKey := d.HardwareHash
					if hwKey == "" {
						hwKey = d.Target.DeviceHash
					}
					used, limit, atLimit, nearLimit := EncoderHeadroom(d.Profile, a.activeStreamCountFor(hwKey))
					name := d.Profile.Model
					if name == "" {
						name = d.Target.Model
					}
					if name == "" {
						name = d.Target.MaskedID
					}
					route := d.Route
					if route.Label == "" {
						route = dex.SelectRoute(d.Profile, d.DexProbe, d.Target.Transport, a.dexRoutePref(hwKey))
					}
					badgeColor := k.brand
					if nearLimit || atLimit {
						badgeColor = k.warn
					}
					ui.Row(c.Key("mode-dev-" + hwKey)).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, name).Bold().FontSize(sizeBody)
						ui.Text(c, route.Label).FontSize(sizeSmall).TextColor(k.muted).Grow(1).SingleLine()
						ui.Row(c).Padding(badgePadV, badgePadH).Radius(pillRadius).Background(k.brandSoft).Children(func() {
							ui.Text(c, L.EncoderStreamsBadge(used, limit)).FontSize(sizeSmall).TextColor(badgeColor).Bold().SingleLine()
						})
					})
				}
			}
		})
		card(c.Key("mode-coexist")).Children(func() {
			ui.Text(c, L.ModeCoexistenceNote()).Bold().FontSize(sizeBody)
			ui.Text(c, L.ModeCoexistenceDesc()).FontSize(sizeBody).TextColor(k.muted)
		})
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

func (a *App) settingsPage(c *ui.Context, k tokens) {
	ui.Column(c.Key("page-settings")).Gap(14).FillWidth().Children(func() {
		ui.Text(c, L.SettingsSubtitle()).FontSize(sizeBody).TextColor(k.muted)
		card(c.Key("settings-appearance")).Children(func() {
			ui.Text(c, L.SettingsAppearance()).Bold().FontSize(sizeBody)
			seg := ui.Segmented(c.Key("theme-seg"), &a.themeIndex, L.ThemeSystem(), L.ThemeLight(), L.ThemeDark()).FillEqually()
			if seg.Changed() {
				themes := []string{"system", "light", "dark"}
				a.mu.Lock()
				a.prefs.Theme = themes[a.themeIndex]
				a.savePrefs()
				a.mu.Unlock()
				ApplyTheme(a.prefs.Theme)
			}

			ui.Text(c, L.SettingsLanguage()).Bold().FontSize(sizeBody)
			lseg := ui.Segmented(c.Key("lang-seg"), &a.langIndex, L.LangSystem(), L.LangEnglish(), L.LangZhHans()).FillEqually()
			if lseg.Changed() {
				langs := []string{"", "en", "zh-Hans"}
				a.mu.Lock()
				a.prefs.Language = langs[a.langIndex]
				a.savePrefs()
				a.mu.Unlock()
				mygo.App.SetLocale(a.prefs.Language)
			}
		})

		card(c.Key("settings-security")).Children(func() {
			ui.Text(c, L.SettingsSecurityTitle()).Bold().FontSize(sizeBody)
			ui.Text(c, L.SettingsSecurityBody()).FontSize(sizeBody).TextColor(k.muted)
		})
	})
}

func (a *App) aboutPage(c *ui.Context, k tokens) {
	ui.Column(c.Key("page-about")).Gap(14).FillWidth().Children(func() {
		card(c.Key("about-card")).Children(func() {
			ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
				if appIconBitmap != nil {
					ui.Image(c, appIconBitmap).Size(56, 56).Radius(12)
				}
				ui.Column(c).Gap(4).Grow(1).Children(func() {
					ui.Text(c, L.AppName()).Bold().FontSize(sizeTitle)
					ui.Text(c, L.AboutVersion(version.Version)).FontSize(sizeBody).TextColor(k.brand)
					ui.Text(c, L.AppTagline()).FontSize(sizeBody).TextColor(k.muted)
				})
			})
			ui.Text(c, L.AboutLicense()).FontSize(sizeBody)
			ui.Text(c, L.AboutTrademarks()).FontSize(sizeSmall).TextColor(k.muted)
		})
	})
}
