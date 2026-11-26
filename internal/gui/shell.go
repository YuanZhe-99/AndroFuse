package gui

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/androidhost"
	"github.com/YuanZhe-99/AndroFuse/internal/dex"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// Settings categories (V2.1: six icon-led categories with a breadcrumb).
const (
	setGeneral  = "general"
	setWindows  = "windows"
	setNetwork  = "network"
	setLocal    = "local"
	setSecurity = "security"
	setAbout    = "about"
)

// view builds the main window: on Android the Companion; on desktops the sidebar (an icon rail
// below railBreak) beside the main pane, whose page is a list page or docked sessions.
func (a *App) view(c *ui.Context) {
	if runtime.GOOS == "android" {
		androidhost.RenderCompanionView(c, a.ensureAndroidHost())
		return
	}
	t := c.Theme()
	k := tok(c)
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	} else {
		c.Root().Background(k.canvas)
	}
	w, _ := c.Size()

	if w < mobileBreak {
		ui.Column(c.Key("mobile-shell")).Fill().Background(t.Background).Children(func() {
			ui.Scroll(c.Key("mobile-content")).Grow(1).FillWidth().Padding(16).Children(func() {
				a.pageBody(c, k)
			})
			a.bottomNav(c, k)
		})
		a.connectDialog(c, k)
		return
	}

	ui.Row(c.Key("desktop-shell")).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebar(c, k, w < railBreak)
		ui.Column(c.Key("main-pane-wrap")).Grow(1).MinWidth(0).MinHeight(0).Padding(paneMargin, paneMargin, paneMargin, 0).Children(func() {
			ui.Column(c.Key("main-pane")).Grow(1).FillWidth().MinHeight(0).Radius(paneRadius).Clip().
				Background(t.Background).Border(1, t.Border).Shadow(0, 6, 18, 0, paneShadow).Children(func() {
				a.mainHeader(c)
				ui.Column(c.Key("main-body")).Grow(1).FillWidth().MinHeight(0).Children(func() {
					if !a.paneArea(c) {
						ui.Scroll(c.Key("pane-scroll")).Grow(1).FillWidth().Padding(20).Children(func() {
							a.pageBody(c, k)
						})
					}
				})
			})
		})
	})
	a.connectDialog(c, k)
}

// navCounts returns how many devices are online and how many apps the Apps page can list.
func (a *App) navCounts() (devices, apps int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range a.devices {
		if d.Target.State == "device" {
			devices++
		}
		apps += len(d.Apps)
	}
	return devices, apps
}

// badgeText caps a counter at "99+"; zero hides it.
func badgeText(n int) string {
	if n > navBadgeMax {
		return strconv.Itoa(navBadgeMax) + "+"
	}
	return strconv.Itoa(n)
}

// sidebar is the navigation column: brand, the one tinted Connect action, Devices / Apps /
// Companion, the open sessions, Settings and the readiness footer that opens Diagnostics.
func (a *App) sidebar(c *ui.Context, k tokens, rail bool) {
	width := sidebarW
	if rail {
		width = railW
	}
	a.mu.Lock()
	selected := a.page
	a.mu.Unlock()
	devCount, appCount := a.navCounts()

	ui.Column(c.Key("sidebar")).Width(width).Shrink(0).FillHeight().Padding(14, 10).Gap(10).Children(func() {
		ui.Row(c.Key("brand-row")).Gap(10).AlignItems(ui.Center).Padding(4, 4).DragWindow().Children(func() {
			if appIconBitmap != nil {
				ui.Image(c, appIconBitmap).Size(28, 28).Radius(7)
			}
			if !rail {
				ui.Column(c.Key("brand-text")).Gap(1).Children(func() {
					ui.Text(c, L.AppName()).Bold().FontSize(sizeHeading)
					ui.Text(c, "v"+version.Version).FontSize(sizeSmall).TextColor(k.text3)
				})
			}
		})

		conn := ui.ButtonBase(c.Key("btn-connect")).FillWidth().Height(36).Radius(radiusMD).Background(k.brandSoft).
			Center().Label(L.ConnectDevice()).Tooltip(L.ConnectDevice())
		conn.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconPlus).Size(navIconSize, navIconSize).TextColor(k.brandSoftText)
				if !rail {
					ui.Text(c, L.ConnectDevice()).Bold().FontSize(sizeBody).TextColor(k.brandSoftText)
				}
			})
		})
		if conn.Clicked() {
			a.mu.Lock()
			a.connectOpen = true
			a.mu.Unlock()
		}

		if rail {
			a.railNav(c, k, selected)
		} else {
			sb := ui.Sidebar(c.Key("nav-sidebar"), &selected, func() {
				ui.SidebarSection(c.Key("nav-main"), "", nil, func() {
					ui.SidebarItem(c, string(pageDevices), iconDevices, L.NavDevices()).Children(func() {
						if devCount > 0 {
							ui.Badge(c, badgeText(devCount))
						}
					})
					ui.SidebarItem(c, string(pageApps), iconApps, L.NavApps()).Children(func() {
						if appCount > 0 {
							ui.Badge(c, badgeText(appCount))
						}
					})
					ui.SidebarItem(c, string(pageCompanion), iconLink, L.NavCompanion())
				})
				a.openSidebarItems(c)
				ui.SidebarSection(c.Key("nav-app"), "", nil, func() {
					ui.SidebarItem(c, string(pageSettings), iconSettings, L.NavSettings())
				})
			}).Grow(1).FillWidth().Background(ui.Transparent)
			if sb.Changed() {
				a.choosePage(selected)
			}
		}
		a.readinessFooter(c, k, rail)
	})
}

// choosePage follows a sidebar choice: a popped-out session raises its window, anything else
// becomes the page.
func (a *App) choosePage(id string) {
	if wid, ok := strings.CutPrefix(id, windowPrefix); ok {
		if sw := a.session(wid); sw != nil {
			sw.FocusWindow()
		}
		return
	}
	a.mu.Lock()
	a.page = id
	a.mu.Unlock()
}

// railNav is the sidebar as 40-DIP icon buttons with tooltips, for windows below railBreak.
func (a *App) railNav(c *ui.Context, k tokens, selected string) {
	type item struct {
		id    string
		icon  *ui.SVG
		label string
	}
	items := []item{
		{string(pageDevices), iconDevices, L.NavDevices()},
		{string(pageApps), iconApps, L.NavApps()},
		{string(pageCompanion), iconLink, L.NavCompanion()},
	}
	for _, s := range a.sessionsSorted() {
		id := panePrefix + s.spec.ID
		if !s.Docked() {
			id = windowPrefix + s.spec.ID
		}
		items = append(items, item{id, modeIcon(s.spec.Mode), s.shortTitle()})
	}
	ui.Column(c.Key("rail")).Grow(1).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
		for _, it := range items {
			b := ui.ButtonBase(c.Key("rail-"+it.id)).Size(touchTargetMin, touchTargetMin).Radius(radiusMD).Center().
				Label(it.label).Tooltip(it.label)
			fg := k.text2
			if it.id == selected {
				b.Background(k.brandSoft)
				fg = k.brandSoftText
			}
			b.Children(func() { ui.Icon(c, it.icon).Size(navIconSize+2, navIconSize+2).TextColor(fg) })
			if b.Clicked() {
				a.choosePage(it.id)
			}
		}
		ui.Box(c.Key("rail-gap")).Grow(1)
		s := ui.ButtonBase(c.Key("rail-settings")).Size(touchTargetMin, touchTargetMin).Radius(radiusMD).Center().
			Label(L.NavSettings()).Tooltip(L.NavSettings())
		s.Children(func() { ui.Icon(c, iconSettings).Size(navIconSize+2, navIconSize+2).TextColor(k.text2) })
		if s.Clicked() {
			a.choosePage(string(pageSettings))
		}
	})
}

// readinessFooter summarizes the host check in one line and opens Diagnostics, where the
// technical details live.
func (a *App) readinessFooter(c *ui.Context, k tokens, rail bool) {
	t := c.Theme()
	a.mu.Lock()
	ready, d := a.diagReady, a.diag
	a.mu.Unlock()
	ok := ready && d.CoreLoaded && d.ADBFound
	title, sub, dot := L.ReadyChecking(), "", k.text3
	switch {
	case ok:
		title, dot = L.ReadyOK(), k.good
		if d.SelfCheck.H264HWDecoder {
			sub = L.ReadyHWDecode()
		}
	case ready:
		title, sub, dot = L.ReadyAttention(), L.ReadySeeDiagnostics(), k.warn
	}
	b := ui.ButtonBase(c.Key("ready-footer")).FillWidth().Padding(8, 10).Radius(radiusLG).Background(t.Surface).
		Border(1, t.Border).Label(title).Tooltip(L.NavDiagnostics())
	b.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(8, 8).Radius(4).Background(dot).Shrink(0)
			if !rail {
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, title).FontSize(sizeSmall).Bold().SingleLine()
					if sub != "" {
						ui.Text(c, sub).FontSize(sizeSmall).TextColor(k.text3).SingleLine()
					}
				})
			}
		})
	})
	if b.Clicked() {
		a.choosePage(string(pageDiagnostics))
	}
}

// bottomNav is the navigation of very narrow windows.
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
		{pageApps, iconApps, L.NavApps()},
		{pageCompanion, iconLink, L.NavCompanion()},
		{pageSettings, iconSettings, L.NavSettings()},
	}
	ui.Row(c.Key("mobile-bottom-nav")).FillWidth().Padding(8, 12).Gap(6).
		Background(t.Surface).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
		for _, it := range items {
			active := cur == string(it.id)
			b := ui.ButtonBase(c.Key("mnav-"+string(it.id))).Grow(1).Radius(10).Padding(8, 6).Center().Label(it.label)
			fg := k.text2
			if active {
				b.Background(k.brandSoft)
				fg = k.brandSoftText
			}
			b.Children(func() {
				ui.Column(c).Gap(3).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, it.icon).Size(16, 16).TextColor(fg)
					ui.Text(c, it.label).FontSize(sizeSmall).TextColor(fg).SingleLine()
				})
			})
			if b.Clicked() {
				a.choosePage(string(it.id))
			}
		}
	})
}

// mainHeader is the main pane's title bar: the page title (a breadcrumb in Settings), the drag
// area and, on Windows, the window buttons.
func (a *App) mainHeader(c *ui.Context) {
	t := c.Theme()
	k := tok(c)
	title, crumb := a.currentTitle()
	ui.Row(c.Key("main-head")).Height(paneHeaderH).Shrink(0).FillWidth().Padding(0, 8, 0, 18).Gap(6).
		AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).Bold().FontSize(sizeHeading).DragWindow()
		if crumb != "" {
			ui.Icon(c, iconChevron).Size(12, 12).TextColor(k.text3)
			ui.Text(c, crumb).FontSize(sizeBody).TextColor(k.text2).DragWindow()
		}
		ui.Box(c.Key("main-drag")).Grow(1).FillHeight().DragWindow()
		a.windowButtons(c)
	})
	ui.Box(c.Key("main-divider")).Height(1).FillWidth().Background(t.Border)
}

// windowButtons draws minimize, maximize and close for the frameless Windows main window.
func (a *App) windowButtons(c *ui.Context) {
	if runtime.GOOS != "windows" {
		return
	}
	t := c.Theme()
	win := a.mainWindow()
	btn := func(name string, glyph *ui.SVG) ui.Element {
		b := ui.ButtonBase(c.Key("win-"+name)).Size(winButtonW, winButtonH).Radius(radiusSM).Center().Shrink(0).Label(name)
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

// currentTitle returns the main pane's title and, in Settings, its category.
func (a *App) currentTitle() (string, string) {
	a.mu.Lock()
	p, split, cat := a.page, a.split, a.settingsTab
	a.mu.Unlock()
	if p == pageSplit && split != nil {
		if sa, sb := a.session(split.A), a.session(split.B); sa != nil && sb != nil {
			return sa.shortTitle() + " + " + sb.shortTitle(), ""
		}
	}
	if id, ok := strings.CutPrefix(p, panePrefix); ok {
		if sw := a.session(id); sw != nil {
			return sw.shortTitle(), ""
		}
	}
	switch navPage(p) {
	case pageApps:
		return L.NavApps(), ""
	case pageCompanion:
		return L.NavCompanion(), ""
	case pageDiagnostics:
		return L.DiagTitle(), ""
	case pageSettings:
		return L.NavSettings(), settingsLabel(cat)
	default:
		return L.NavDevices(), ""
	}
}

// pageBody builds the list page the sidebar chose.
func (a *App) pageBody(c *ui.Context, k tokens) {
	a.mu.Lock()
	p := navPage(a.page)
	a.mu.Unlock()
	switch p {
	case pageApps:
		a.appsPage(c, k)
	case pageCompanion:
		a.companionPage(c, k)
	case pageDiagnostics:
		a.diagnosticsPage(c, k)
	case pageSettings:
		a.settingsPage(c, k)
	default:
		a.devicesPage(c, k)
	}
}

// deviceName is what the interface calls a device.
func deviceName(d DiscoveredDevice) string {
	for _, s := range []string{d.Profile.Model, d.Target.Model, d.Target.MaskedID} {
		if s != "" {
			return s
		}
	}
	return "Android"
}

// devicesPage lists the online devices with their launch actions, the local Android
// environment and the remembered phones that are offline.
func (a *App) devicesPage(c *ui.Context, k tokens) {
	a.mu.Lock()
	scanning := a.scanningDevices
	devs := append([]DiscoveredDevice(nil), a.devices...)
	notice := a.deviceNotice
	vmSt := a.vmStatus
	a.mu.Unlock()
	offline := a.offlineRemembered(devs)

	ui.Column(c.Key("page-devices")).Gap(14).FillWidth().Children(func() {
		ui.Row(c.Key("devices-topbar")).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, L.DevicesCount(len(devs))).FontSize(sizeBody).TextColor(k.text2).Grow(1)
			rb := ui.Button(c.Key("btn-scan-devices"), "").Label(L.DevicesRefresh()).Tooltip(L.DevicesRefresh()).Disabled(scanning).Children(func() {
				if scanning {
					ui.Spinner(c).Size(14, 14)
				} else {
					ui.Icon(c, iconRefresh).Size(14, 14)
				}
			})
			if rb.Clicked() {
				a.RefreshDevices()
			}
		})

		if notice != "" {
			ui.Text(c, notice).FontSize(sizeSmall).TextColor(k.warn)
		}

		if len(devs) == 0 && len(offline) == 0 {
			a.devicesEmpty(c, k)
		}
		for _, d := range devs {
			a.deviceCard(c, k, d)
		}
		if len(devs) > 0 {
			ui.Text(c, L.OpenHint()).FontSize(sizeSmall).TextColor(k.text3)
		}

		a.vmCard(c, k, vmSt)

		if len(offline) > 0 {
			ui.Row(c.Key("remembered-head")).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, L.RememberedTitle()).Bold().FontSize(sizeBody)
				ui.Text(c, L.RememberedOffline(len(offline))).FontSize(sizeSmall).TextColor(k.text3)
			})
			card(c.Key("remembered-card")).Gap(0).Padding(4, cardPad).Children(func() {
				for i, r := range offline {
					if i > 0 {
						ui.Box(c.Key("rem-line-" + r.HardwareHash)).Height(1).FillWidth().Background(k.hairline)
					}
					a.rememberedRow(c, k, r)
				}
			})
		}
	})
}

// devicesEmpty is the first-run state: one icon, one line, one primary action.
func (a *App) devicesEmpty(c *ui.Context, k tokens) {
	ui.Column(c.Key("devices-empty")).FillWidth().Padding(36, 16).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconDevices).Size(36, 36).TextColor(k.brand)
		ui.Text(c, L.DevicesEmptyTitle()).Bold().FontSize(sizeHeading)
		ui.Text(c, L.DevicesEmptyLine()).FontSize(sizeBody).TextColor(k.text2)
		if ui.PrimaryButton(c.Key("empty-connect"), L.ConnectDevice()).Clicked() {
			a.mu.Lock()
			a.connectOpen = true
			a.mu.Unlock()
		}
	})
}

// deviceCard shows one online device: name, transports and route, then the split button that
// opens its main mode (in a pane, beside the current one, or in a window) and its other modes.
func (a *App) deviceCard(c *ui.Context, k tokens, d DiscoveredDevice) {
	hwKey := d.HardwareHash
	if hwKey == "" {
		hwKey = d.Target.DeviceHash
	}
	devKey := "dev-" + hwKey
	transports := d.Transports
	if len(transports) == 0 {
		transports = []adb.Device{d.Target}
	}
	used, limit, atLimit, nearLimit := EncoderHeadroom(d.Profile, a.activeStreamCountFor(hwKey))
	route := d.Route
	if route.Label == "" {
		route = dex.SelectRoute(d.Profile, d.DexProbe, d.Target.Transport, a.dexRoutePref(hwKey))
	}
	var wirelessSerial string
	var labels []string
	for _, tr := range transports {
		labels = append(labels, transportLabel(tr.Transport))
		if tr.State == "device" && (tr.Transport == adb.TransportTLSWiFi || tr.Transport == adb.TransportTCP) {
			wirelessSerial = tr.Serial
		}
	}
	icon := iconUsb
	if d.Target.Transport == adb.TransportTLSWiFi || d.Target.Transport == adb.TransportTCP {
		icon = iconWifi
	}

	card(c.Key(devKey)).Children(func() {
		ui.Row(c.Key(devKey + "-head")).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			iconTile(c, k, icon)
			ui.Column(c.Key(devKey + "-name")).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, deviceName(d)).Bold().FontSize(sizeHeading).SingleLine()
				ui.Text(c, strings.Join(labels, " · ")).FontSize(sizeSmall).TextColor(k.text2).SingleLine()
			})
			if len(transports) > 1 {
				chip(c, devKey+"-handoff", L.TransportHandoffReady(), k.good)
			}
			if d.Probed && (nearLimit || atLimit) {
				chip(c, devKey+"-enc", L.EncoderStreamsBadge(used, limit), k.warn)
			}
		})

		if d.Target.State != "device" {
			ui.Text(c, d.Target.State).FontSize(sizeSmall).TextColor(k.warn)
			return
		}
		if d.ProbeErr != "" && !d.Probed {
			ui.Text(c, d.ProbeErr).FontSize(sizeSmall).TextColor(k.warn)
		}
		if d.Probed {
			// The route label names the real backend: Samsung DeX, or a large-screen mode that is
			// not DeX (`INV-06`).
			ui.Text(c, route.Label).FontSize(sizeSmall).TextColor(k.text3)
		}

		genuineDeX := route.Backend == dex.BackendADBLoopback && route.IsGenuineDeX && d.Target.Transport != adb.TransportEmulator
		ui.Row(c.Key(devKey + "-modes")).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			main := L.ActionLaunchMirror()
			launch := func(open OpenIn) { a.LaunchMirror(d, open) }
			if genuineDeX {
				main = L.ActionLaunchDeX()
				launch = func(open OpenIn) { a.LaunchDeX(d, false, open) }
			}
			sb := ui.PrimarySplitButton(c.Key(devKey+"-main"), main)
			sb.Arrow.PopoverMenuButton(func(m *ui.Menu) {
				if m.Item(L.OpenInPane()).Chosen() {
					launch(OpenPane)
				}
				if m.Item(L.OpenBeside()).Chosen() {
					launch(OpenSplit)
				}
				if m.Item(L.OpenInWindow()).Chosen() {
					launch(OpenWindow)
				}
			}, ui.PopoverMenuButtonOptions{At: ui.AnchorBottomStart, Self: ui.AnchorTopStart})
			if sb.Button.Clicked() {
				launch(OpenDefault)
			}
			if genuineDeX && ui.Button(c.Key(devKey+"-mirror"), L.ActionLaunchMirror()).Clicked() {
				a.LaunchMirror(d, OpenDefault)
			}
			if len(d.Apps) > 0 && ui.Button(c.Key(devKey+"-apps"), L.NavApps()).Clicked() {
				a.mu.Lock()
				a.appsDevice = hwKey
				a.page = string(pageApps)
				a.mu.Unlock()
			}
			ui.Button(c.Key(devKey+"-more"), "").Label(L.PaneMore()).Children(func() {
				ui.Icon(c, iconMore).Size(14, 14)
			}).PopoverMenuButton(func(m *ui.Menu) {
				if d.Target.Transport != adb.TransportEmulator && m.Item(L.ActionLaunchCamera()).Chosen() {
					a.mu.Lock()
					back := a.cameraFacingIdx == 0
					a.mu.Unlock()
					facing := "front"
					if back {
						facing = "back"
					}
					a.LaunchPhoneCamera(d, facing, OpenDefault)
				}
				if m.Item(L.ActionLaunchFallback()).Chosen() {
					a.LaunchDeX(d, true, OpenDefault)
				}
				if route.CanUseNativeDeX && d.Target.Transport != adb.TransportEmulator {
					pref := a.dexRoutePref(hwKey)
					m.Submenu(L.DeXRouteLabel(), func(m *ui.Menu) {
						for _, r := range []struct {
							p     dex.RoutePreference
							label string
						}{{dex.RouteAuto, L.DeXRouteAuto()}, {dex.RouteNativeDeX, L.DeXRouteNative()}, {dex.RouteVirtualFallback, L.DeXRouteFallback()}} {
							if m.Item(r.label).Radio(pref == r.p).Chosen() {
								a.setDexRoutePref(hwKey, r.p)
							}
						}
					})
				}
				if wirelessSerial != "" && m.Item(L.ActionDisconnect()).Chosen() {
					a.DisconnectWireless(wirelessSerial)
				}
			})
		})
	})
}

// rememberedRow is an offline remembered phone: when it was last seen, automatic reconnect,
// Companion reconnect over the mesh when paired, and Forget with Undo.
func (a *App) rememberedRow(c *ui.Context, k tokens, r RememberedDevice) {
	key := "rem-" + r.HardwareHash
	ui.Row(c.Key(key)).FillWidth().Padding(10, 0).Gap(12).AlignItems(ui.Center).Children(func() {
		iconTile(c, k, iconDevices)
		ui.Column(c.Key(key + "-text")).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, r.Name).Bold().FontSize(sizeBody).SingleLine()
			ui.Text(c, L.RememberedLastSeen(r.LastSeen.Local().Format("01-02 15:04"))).FontSize(sizeSmall).TextColor(k.text3).
				FontFeatures("tnum").SingleLine()
		})
		if r.Companion != nil {
			if ui.Button(c.Key(key+"-comp"), L.RememberedCompanion()).Tooltip(L.RememberedCompanionTip()).Clicked() {
				a.ReconnectCompanion(r.HardwareHash)
				a.choosePage(string(pageCompanion))
			}
		}
		auto := r.AutoReconnect
		ui.Text(c, L.RememberedAuto()).FontSize(sizeSmall).TextColor(k.text2)
		if ui.Switch(c.Key(key+"-auto"), &auto).Label(L.RememberedAuto()).Changed() {
			a.SetAutoReconnect(r.HardwareHash, auto)
		}
		if ui.Button(c.Key(key+"-forget"), L.RememberedForget()).Clicked() {
			if gone, ok := a.ForgetDevice(r.HardwareHash); ok {
				c.ToastAction(L.RememberedForgotten(gone.Name), L.ActionUndo(), func() { a.RestoreDevice(gone) })
			}
		}
	})
}

// appsPage lists the launchable apps of one online device; a device selector shows when there
// are several.
func (a *App) appsPage(c *ui.Context, k tokens) {
	a.mu.Lock()
	var devs []DiscoveredDevice
	for _, d := range a.devices {
		if d.Target.State == "device" && len(d.Apps) > 0 {
			devs = append(devs, d)
		}
	}
	sel := a.appsDevice
	a.mu.Unlock()

	ui.Column(c.Key("page-apps")).Gap(14).FillWidth().Children(func() {
		if len(devs) == 0 {
			ui.Column(c.Key("apps-empty")).FillWidth().Padding(36, 16).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconApps).Size(32, 32).TextColor(k.brand)
				ui.Text(c, L.AppsEmpty()).FontSize(sizeBody).TextColor(k.text2)
			})
			return
		}
		idx := 0
		names := make([]string, len(devs))
		for i, d := range devs {
			names[i] = deviceName(d)
			if d.HardwareHash == sel {
				idx = i
			}
		}
		if len(devs) > 1 {
			if ui.Segmented(c.Key("apps-device"), &idx, names...).Changed() {
				a.mu.Lock()
				a.appsDevice = devs[idx].HardwareHash
				a.mu.Unlock()
			}
		}
		d := devs[idx]
		ui.Text(c, L.AppsHint()).FontSize(sizeSmall).TextColor(k.text3)
		a.appLauncherSection(c, k, "apps-"+d.HardwareHash, d)
	})
}

// companionPage holds the phone collaboration: the paired Companion, camera and microphone.
func (a *App) companionPage(c *ui.Context, k tokens) {
	a.mu.Lock()
	connected, st, notice := a.compConnected, a.compStatus, a.compNotice
	a.mu.Unlock()
	ui.Column(c.Key("page-companion")).Gap(14).FillWidth().Children(func() {
		a.companionCard(c, k, connected, st, notice)
		a.avBridgeCard(c, k)
	})
}

// settingsLabel names a Settings category.
func settingsLabel(cat string) string {
	switch cat {
	case setWindows:
		return L.SettingsWindows()
	case setNetwork:
		return L.SettingsNetwork()
	case setLocal:
		return L.SettingsLocal()
	case setSecurity:
		return L.SettingsSecurity()
	case setAbout:
		return L.NavAbout()
	default:
		return L.SettingsGeneral()
	}
}

// settingsPage lists the six categories beside the chosen one's settings, or above them in a
// narrow pane.
func (a *App) settingsPage(c *ui.Context, k tokens) {
	t := c.Theme()
	a.mu.Lock()
	cat := a.settingsTab
	a.mu.Unlock()
	if cat == "" {
		cat = setGeneral
	}
	cats := []struct {
		id   string
		icon *ui.SVG
	}{
		{setGeneral, iconSettings}, {setWindows, iconSplitRight}, {setNetwork, iconGlobe},
		{setLocal, iconDevices}, {setSecurity, iconShield}, {setAbout, iconInfo},
	}
	w, _ := c.Size()
	narrow := w < railBreak

	body := func() {
		ui.Column(c.Key("settings-" + cat)).Grow(1).MinWidth(0).Gap(14).Children(func() {
			switch cat {
			case setWindows:
				a.windowsSettings(c, k)
			case setNetwork:
				a.meshSettingsCard(c, k)
			case setLocal:
				a.mu.Lock()
				st := a.vmStatus
				a.mu.Unlock()
				a.vmCard(c, k, st)
			case setSecurity:
				a.mcpSettingsCard(c, k)
				ui.Text(c, L.SettingsSecurityBody()).FontSize(sizeSmall).TextColor(k.text3)
			case setAbout:
				a.aboutSection(c, k)
			default:
				a.generalSettings(c, k)
			}
		})
	}
	choose := func(id string) {
		a.mu.Lock()
		a.settingsTab = id
		a.mu.Unlock()
	}

	if narrow {
		ui.Column(c.Key("page-settings")).FillWidth().Gap(14).Children(func() {
			idx := 0
			labels := make([]string, len(cats))
			for i, s := range cats {
				labels[i] = settingsLabel(s.id)
				if s.id == cat {
					idx = i
				}
			}
			if ui.Segmented(c.Key("settings-seg"), &idx, labels...).Changed() {
				choose(cats[idx].id)
			}
			body()
		})
		return
	}
	ui.Row(c.Key("page-settings")).FillWidth().Gap(20).AlignItems(ui.Start).Children(func() {
		ui.Column(c.Key("settings-cats")).Width(200).Shrink(0).Gap(2).Children(func() {
			for _, s := range cats {
				b := ui.ButtonBase(c.Key("set-"+s.id)).FillWidth().Height(36).Padding(0, 10).Radius(radiusMD).Label(settingsLabel(s.id))
				fg := k.text2
				if s.id == cat {
					b.Background(t.SurfaceHover)
					fg = t.Text
				}
				b.Children(func() {
					ui.Row(c).FillWidth().FillHeight().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, s.icon).Size(navIconSize, navIconSize).TextColor(fg)
						ui.Text(c, settingsLabel(s.id)).FontSize(sizeBody).TextColor(fg).SingleLine()
					})
				})
				if b.Clicked() {
					choose(s.id)
				}
			}
		})
		body()
	})
}

// settingRow is one labeled setting: a title, a muted line and its control on the right.
func settingRow(c *ui.Context, k tokens, key, title, sub string, control func()) {
	ui.Row(c.Key(key)).FillWidth().Gap(16).AlignItems(ui.Center).Padding(4, 0).Children(func() {
		ui.Column(c.Key(key + "-text")).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).Bold().FontSize(sizeBody)
			if sub != "" {
				ui.Text(c, sub).FontSize(sizeSmall).TextColor(k.text3)
			}
		})
		control()
	})
}

// generalSettings holds appearance and language, which apply at once.
func (a *App) generalSettings(c *ui.Context, k tokens) {
	card(c.Key("settings-general-card")).Children(func() {
		settingRow(c, k, "set-theme", L.SettingsAppearance(), L.SettingsAppearanceSub(), func() {
			if ui.Segmented(c.Key("theme-seg"), &a.themeIndex, L.ThemeSystem(), L.ThemeLight(), L.ThemeDark()).Changed() {
				themes := []string{"system", "light", "dark"}
				a.mu.Lock()
				a.prefs.Theme = themes[a.themeIndex]
				a.savePrefs()
				a.mu.Unlock()
				ApplyTheme(a.prefs.Theme)
			}
		})
		settingRow(c, k, "set-lang", L.SettingsLanguage(), L.SettingsLanguageSub(), func() {
			if ui.Segmented(c.Key("lang-seg"), &a.langIndex, L.LangSystem(), L.LangZhHans(), L.LangEnglish()).Changed() {
				langs := []string{"", "zh-Hans", "en"}
				a.mu.Lock()
				a.prefs.Language = langs[a.langIndex]
				a.savePrefs()
				a.mu.Unlock()
				mygo.App.SetLocale(a.prefs.Language)
			}
		})
	})
}

// windowsSettings chooses where sessions open by default: a main-window pane or their own window.
func (a *App) windowsSettings(c *ui.Context, k tokens) {
	a.mu.Lock()
	streams, apps := 0, 0
	if a.prefs.StreamsInWindow {
		streams = 1
	}
	if a.prefs.AppsInWindow {
		apps = 1
	}
	a.mu.Unlock()
	card(c.Key("settings-windows-card")).Children(func() {
		settingRow(c, k, "set-streams-open", L.SettingsStreamsOpen(), L.SettingsStreamsOpenSub(), func() {
			if ui.Segmented(c.Key("streams-open-seg"), &streams, L.OpenPaneShort(), L.OpenWindowShort()).Changed() {
				a.mu.Lock()
				a.prefs.StreamsInWindow = streams == 1
				a.savePrefs()
				a.mu.Unlock()
			}
		})
		settingRow(c, k, "set-apps-open", L.SettingsAppsOpen(), L.SettingsAppsOpenSub(), func() {
			if ui.Segmented(c.Key("apps-open-seg"), &apps, L.OpenPaneShort(), L.OpenWindowShort()).Changed() {
				a.mu.Lock()
				a.prefs.AppsInWindow = apps == 1
				a.savePrefs()
				a.mu.Unlock()
			}
		})
	})
}

// aboutSection is the version, license, acknowledgments and trademark notice.
func (a *App) aboutSection(c *ui.Context, k tokens) {
	card(c.Key("about-card")).Children(func() {
		ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
			if appIconBitmap != nil {
				ui.Image(c, appIconBitmap).Size(56, 56).Radius(radiusLG)
			}
			ui.Column(c).Gap(4).Grow(1).Children(func() {
				ui.Text(c, L.AppName()).Bold().FontSize(sizeTitle)
				ui.Text(c, L.AboutVersion(version.Version)).FontSize(sizeBody).TextColor(k.brand)
				ui.Text(c, L.AppTagline()).FontSize(sizeBody).TextColor(k.text2)
			})
		})
		ui.Text(c, L.AboutLicense()).FontSize(sizeBody)
		ui.Text(c, L.AboutTrademarks()).FontSize(sizeSmall).TextColor(k.text3)
	})
}

// connectDialog is the Connect device modal: a wireless phone (mDNS, address or pairing code)
// or the Companion, with the optional, unticked Companion install (`INV-09`).
func (a *App) connectDialog(c *ui.Context, k tokens) {
	a.mu.Lock()
	open := a.connectOpen
	a.mu.Unlock()
	if !open {
		return
	}
	ui.Modal(c.Key("connect-dialog"), &a.connectOpen, func() {
		ui.Column(c.Key("connect-body")).Width(520).Gap(14).Padding(6).Children(func() {
			ui.Text(c, L.ConnectDevice()).Bold().FontSize(sizeHeading)
			ui.Tabs(c.Key("connect-tabs"), &a.connectTab, L.ConnectTabWireless(), L.ConnectTabCompanion())
			if a.connectTab == 1 {
				a.mu.Lock()
				connected, st, notice := a.compConnected, a.compStatus, a.compNotice
				a.mu.Unlock()
				a.companionCard(c, k, connected, st, notice)
				return
			}
			a.wirelessForm(c, k)
		})
	})
}

// wirelessForm connects a TLS Wireless Debugging phone: found by mDNS, by address, or paired
// with its 6-digit code first (`INV-07`).
func (a *App) wirelessForm(c *ui.Context, k tokens) {
	a.mu.Lock()
	mdns := append([]adb.MDNSService(nil), a.mdnsServices...)
	notice := a.deviceNotice
	install := a.installCompanion
	a.mu.Unlock()

	ui.Text(c, L.WirelessCardDesc()).FontSize(sizeSmall).TextColor(k.text2)
	if len(mdns) > 0 {
		ui.Row(c.Key("mdns-chips")).Gap(8).Wrap().Children(func() {
			for i, s := range mdns {
				if s.CanConnect {
					if ui.Button(c.Key(fmt.Sprintf("mdns-conn-%d", i)), L.WirelessMdnsConnect(s.MaskedAddr)).Clicked() {
						a.ConnectWirelessTLS(s.Addr, install)
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
		ui.TextInput(c.Key("manual-addr"), &a.manualAddr).Placeholder(L.WirelessManualAddrHint()).Grow(1).MinWidth(0)
		if ui.Button(c.Key("btn-manual-tls"), L.WirelessConnectBtn()).Disabled(strings.TrimSpace(a.manualAddr) == "").Clicked() {
			a.ConnectWirelessTLS(a.manualAddr, install)
		}
	})
	ui.Row(c.Key("pair-tls-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key("pair-addr"), &a.pairAddr).Placeholder(L.WirelessPairAddrHint()).Grow(2).MinWidth(0)
		ui.TextInput(c.Key("pair-code"), &a.pairCode).Placeholder(L.WirelessPairCodeHint()).Grow(1).MinWidth(0)
		code := strings.TrimSpace(a.pairCode)
		if ui.Button(c.Key("btn-pair-tls"), L.WirelessPairBtn()).Disabled(!validPairCode(code) || strings.TrimSpace(a.pairAddr) == "").Clicked() {
			a.PairWirelessTLS(a.pairAddr, code)
		}
	})
	if code := strings.TrimSpace(a.pairCode); code != "" && !validPairCode(code) {
		ui.Text(c, L.PairCodeError()).FontSize(sizeSmall).TextColor(k.bad)
	}
	if ui.Checkbox(c.Key("connect-install"), &install, L.ConnectInstallCompanion()).Changed() {
		a.mu.Lock()
		a.installCompanion = install
		a.mu.Unlock()
	}
	if notice != "" {
		ui.Text(c, notice).FontSize(sizeSmall).TextColor(k.warn)
	}
}

// validPairCode reports whether s is a 6-digit Wireless Debugging pairing code.
func validPairCode(s string) bool {
	if len(s) != 6 {
		return false
	}
	_, err := strconv.Atoi(s)
	return err == nil
}

// chip is a tinted status pill: tinted fill and deep same-hue text.
func chip(c *ui.Context, key, text string, fg ui.Color) {
	k := tok(c)
	ui.Row(c.Key(key)).Padding(badgePadV, badgePadH).Radius(pillRadius).Background(k.brandSoft).Shrink(0).Children(func() {
		ui.Text(c, text).FontSize(sizeSmall).TextColor(fg).Bold().SingleLine()
	})
}

// iconTile is a row's fixed-size icon container.
func iconTile(c *ui.Context, k tokens, icon *ui.SVG) {
	ui.Box(c).Size(tileSize, tileSize).Shrink(0).Radius(radiusMD).Background(k.brandSoft).Center().Children(func() {
		ui.Icon(c, icon).Size(navIconSize, navIconSize).TextColor(k.brandSoftText)
	})
}
