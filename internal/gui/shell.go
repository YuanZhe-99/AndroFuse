package gui

import (
	"runtime"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/androidhost"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// pageHome is the main window's Home pane; sessions show as "pane:<id>" or pageSplit.
const pageHome = "home"

// view builds the main window (design/README.md, "Shell"): on Android the Companion; on desktops
// the sidebar on the window's material beside the panes — cards that need a decision above them,
// then Home or the open sessions — with Settings, search and the + picker over them.
func (a *App) view(c *ui.Context) {
	if runtime.GOOS == "android" {
		androidhost.RenderCompanionView(c, a.ensureAndroidHost())
		return
	}
	k := tok(c)
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	} else {
		c.Root().Background(k.canvas)
	}
	c.OnWindowShortcut(ui.Cmd, ui.KeyK, func() { a.toggleSearch() })
	c.OnWindowShortcut(ui.Cmd, ui.KeyComma, func() { a.openSettings("") })

	w, _ := c.Size()
	a.mu.Lock()
	rail := a.prefs.SidebarRail || w < railBreak
	hidden := w < mobileBreak
	a.mu.Unlock()

	ui.Row(c.Key("shell")).Fill().AlignItems(ui.Stretch).Children(func() {
		if !hidden {
			a.sidebar(c, k, rail)
		}
		left := float32(0)
		if hidden {
			left = paneMargin
		}
		ui.Column(c.Key("panes")).Grow(1).MinWidth(0).MinHeight(0).Gap(paneMargin).
			Padding(paneMargin, paneMargin, paneMargin, left).Children(func() {
			a.noticeCards(c, k)
			if !a.paneArea(c) {
				a.homePane(c, k)
			}
		})
	})
	a.settingsSheet(c, k)
	a.searchPalette(c, k)
	a.addWizardModal(c, k)
	a.activateModal(c, k)
}

// sidebar is Kyokai's sidebar: hide, + (the picker), Home, the open panes grouped as they show,
// separate windows, and at its foot the networks' state, search, appearance and Settings.
func (a *App) sidebar(c *ui.Context, k tokens, rail bool) {
	width := sidebarW
	if rail {
		width = railW
	}
	a.mu.Lock()
	page := a.page
	a.mu.Unlock()
	align := ui.Stretch
	if rail {
		align = ui.Center
	}
	ui.Column(c.Key("sidebar")).Width(width).Shrink(0).FillHeight().Padding(paneMargin).Gap(2).AlignItems(align).Children(func() {
		ui.Row(c.Key("side-top")).Height(paneHeaderH).Shrink(0).AlignItems(ui.Center).Gap(4).DragWindow().Children(func() {
			if iconBtn(c, "side-toggle", iconSidebar, L.SidebarToggle(), false).Clicked() {
				a.mu.Lock()
				a.prefs.SidebarRail = !a.prefs.SidebarRail
				a.savePrefs()
				a.mu.Unlock()
			}
			if !rail {
				ui.Box(c.Key("side-drag")).Grow(1).FillHeight().DragWindow()
				a.plusButton(c, "side-plus")
			}
		})
		if rail {
			a.plusButton(c, "side-plus")
		}
		a.navRow(c, k, "nav-home", rail, page == pageHome, func() { appMark(c, 20) }, L.NavHome(), "", func() { a.choosePage(pageHome) })
		a.mu.Lock()
		wizOpen := a.addWizard.Open
		a.mu.Unlock()
		a.navRow(c, k, "nav-add", rail, wizOpen, func() { ui.Icon(c, iconPlus).Size(navIconSize, navIconSize).TextColor(k.brandSoftText) }, L.NavAddPhone(), "", func() { a.OpenAddWizard(0) })

		ui.Scroll(c.Key("side-scroll")).Grow(1).FillWidth().Children(func() {
			ui.Column(c.Key("side-open")).FillWidth().Gap(2).AlignItems(align).Children(func() {
				a.openEntries(c, k, rail, page)
			})
		})
		a.sidebarFoot(c, k, rail)
	})
}

// plusButton opens the + picker below it.
func (a *App) plusButton(c *ui.Context, key string) {
	a.mu.Lock()
	open := a.picker.Open
	a.mu.Unlock()
	b := iconBtn(c, key, iconPlus, L.PickerOpen(), open)
	if b.Clicked() {
		a.openPicker(a.picker.Kind, "")
	}
	a.pickerPopover(c, b)
}

// navRow is one sidebar entry: an icon (or avatar), a title and a muted word after it.
func (a *App) navRow(c *ui.Context, k tokens, key string, rail, selected bool, icon func(), title, sub string, onClick func()) {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Radius(radiusMD).Label(title).Tooltip(title)
	if rail {
		b.Size(touchTargetMin+4, touchTargetMin).Center()
	} else {
		b.FillWidth().Height(36).Padding(0, 10)
	}
	switch {
	case selected:
		b.Background(t.Surface).Border(1, t.Border)
	case b.Hovered():
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		row := ui.Row(c).FillHeight().Gap(10).AlignItems(ui.Center)
		if !rail {
			row.FillWidth()
		}
		row.Children(func() {
			icon()
			if !rail {
				ui.Text(c, title).FontSize(sizeBody).SingleLine().Grow(1).MinWidth(0)
				if sub != "" {
					ui.Text(c, sub).FontSize(sizeSmall).TextColor(k.text3).SingleLine().Shrink(0)
				}
			}
		})
	})
	if b.Clicked() {
		onClick()
	}
}

// appMark draws AndroFuse's mark at size.
func appMark(c *ui.Context, size float32) {
	if appIconBitmap != nil {
		ui.Image(c, appIconBitmap).Size(size, size).Radius(size / 4)
	}
}

// openEntries lists the open sessions: a split as a card titled with its phones over its two rows,
// a pane alone as a row, then the sessions in windows of their own.
func (a *App) openEntries(c *ui.Context, k tokens, rail bool, page string) {
	a.mu.Lock()
	split := a.split
	a.mu.Unlock()
	var docked, popped []*SessionWindow
	for _, s := range a.sessionsSorted() {
		if s.Docked() {
			docked = append(docked, s)
		} else {
			popped = append(popped, s)
		}
	}
	if len(docked) > 0 && !rail {
		ui.Row(c.Key("side-open-label")).FillWidth().Padding(12, 10, 4, 10).Children(func() {
			ui.Text(c, L.NavOpen()).FontSize(sizeSmall).TextColor(k.text3).Grow(1)
			ui.Text(c, strconv.Itoa(len(docked))).FontSize(sizeSmall).TextColor(k.text3).FontFeatures("tnum")
		})
	}
	inSplit := map[string]bool{}
	if split != nil {
		sa, sb := a.session(split.A), a.session(split.B)
		if sa != nil && sb != nil {
			inSplit[sa.spec.ID], inSplit[sb.spec.ID] = true, true
			group := func() {
				for _, s := range []*SessionWindow{sa, sb} {
					a.sessionRow(c, k, s, rail, page == pageSplit, pageSplit)
				}
			}
			if rail {
				group()
			} else {
				t := c.Theme()
				ui.Column(c.Key("side-group")).FillWidth().Padding(4).Radius(radiusLG).Background(t.SurfaceHover.Alpha(0.6)).Gap(2).Children(func() {
					ui.Text(c, a.groupTitle(sa, sb)).FontSize(sizeSmall).TextColor(k.text3).SingleLine().Padding(2, 8)
					group()
				})
			}
		}
	}
	for _, s := range docked {
		if !inSplit[s.spec.ID] {
			a.sessionRow(c, k, s, rail, page == panePrefix+s.spec.ID, panePrefix+s.spec.ID)
		}
	}
	if len(popped) > 0 {
		if !rail {
			ui.Text(c.Key("side-win-label"), L.NavWindows()).FontSize(sizeSmall).TextColor(k.text3).Padding(12, 10, 4, 10)
		}
		for _, s := range popped {
			a.sessionRow(c, k, s, rail, false, windowPrefix+s.spec.ID)
		}
	}
}

// groupTitle names a split group by its phones ("My Fold + Pixel 8 Pro", or one name).
func (a *App) groupTitle(x, y *SessionWindow) string {
	nx, ny := a.sessionPhone(x), a.sessionPhone(y)
	if nx == ny {
		return nx
	}
	return nx + " + " + ny
}

// sessionRow is a session's sidebar entry: the phone's avatar or the app, its name, and the kind.
func (a *App) sessionRow(c *ui.Context, k tokens, s *SessionWindow, rail, selected bool, target string) {
	title, kind := a.sessionPhone(s), s.kindWord()
	if s.spec.Mode == SessionModeApp && s.spec.Title != "" {
		title, kind = s.spec.Title, a.sessionPhone(s)
	}
	a.navRow(c, k, "side-"+s.spec.ID, rail, selected, func() { a.sessionIcon(c, s, avatarSmall) }, title, kind, func() { a.choosePage(target) })
}

// sessionIcon is a session's small icon: the app's icon, else the phone's avatar.
func (a *App) sessionIcon(c *ui.Context, s *SessionWindow, size float32) {
	if s.spec.Mode == SessionModeApp && s.spec.AppIcon != nil {
		ui.Image(c, s.spec.AppIcon).Size(size, size).Radius(size / 4)
		return
	}
	logo, glyph, name := a.phoneMark(s.spec.DeviceKey)
	phoneAvatar(c, size, logo, glyph, name)
}

// sidebarFoot holds the networks' state (opens Settings › Network), search, appearance and Settings.
func (a *App) sidebarFoot(c *ui.Context, k tokens, rail bool) {
	dot, words := a.networkSummary(k)
	footAlign := ui.Stretch
	if rail {
		footAlign = ui.Center
	}
	ui.Column(c.Key("side-foot")).FillWidth().Gap(2).AlignItems(footAlign).Children(func() {
		if !rail {
			t := c.Theme()
			b := ui.ButtonBase(c.Key("side-net")).FillWidth().Height(32).Padding(0, 10).Radius(radiusMD).Label(L.NetSidebar(words)).Tooltip(L.SettingsNetwork())
			if b.Hovered() {
				b.Background(t.SurfaceHover)
			}
			b.Children(func() {
				ui.Row(c).FillHeight().FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(8, 8).Radius(4).Background(dot).Shrink(0)
					ui.Text(c, L.NetSidebar(words)).FontSize(sizeSmall).TextColor(k.text2).SingleLine().MinWidth(0)
				})
			})
			if b.Clicked() {
				a.openSettings(setNetwork)
			}
		}
		dir := ui.Row
		if rail {
			dir = ui.Column
		}
		dir(c.Key("side-icons")).Gap(2).AlignItems(ui.Center).Children(func() {
			if iconBtn(c, "side-search", iconSearch, L.SearchOpen(), false).Clicked() {
				a.toggleSearch()
			}
			a.mu.Lock()
			theme := a.prefs.Theme
			a.mu.Unlock()
			icon, tip := iconAuto, L.ThemeSystem()
			switch theme {
			case "light":
				icon, tip = iconSun, L.ThemeLight()
			case "dark":
				icon, tip = iconMoon, L.ThemeDark()
			}
			if iconBtn(c, "side-theme", icon, tip, false).Clicked() {
				next := map[string]string{"system": "light", "light": "dark", "dark": "system", "": "light"}[theme]
				a.setTheme(next)
			}
			if iconBtn(c, "side-settings", iconSettings, L.NavSettings(), false).Clicked() {
				a.openSettings("")
			}
			if rail {
				ui.Box(c.Key("side-net-dot")).Size(8, 8).Radius(4).Background(dot).Margin(10, 0).Tooltip(words)
			}
		})
	})
}

// networkSummary is the networks' state as a dot color and a few words.
func (a *App) networkSummary(k tokens) (ui.Color, string) {
	nets := a.networks()
	a.mu.Lock()
	views := make(map[string]netView, len(a.netViews))
	for id, v := range a.netViews {
		views[id] = v
	}
	a.mu.Unlock()
	joined, up, failed := 0, 0, 0
	for _, n := range nets {
		if !n.Join {
			continue
		}
		joined++
		v := views[n.ID]
		switch {
		case v.Status.Running:
			up++
		case v.Err != "" || v.Status.LastError != "":
			failed++
		}
	}
	switch {
	case joined == 0:
		return k.text3, L.NetSummaryOff()
	case up == joined:
		return k.good, L.NetSummaryConnected()
	case failed > 0 && up == 0:
		return k.bad, L.NetSummaryFailed()
	default:
		return k.warn, L.NetSummaryConnecting()
	}
}

// setTheme saves and applies the appearance ("system", "light" or "dark").
func (a *App) setTheme(theme string) {
	a.mu.Lock()
	a.prefs.Theme = theme
	a.savePrefs()
	a.syncControlIndices()
	a.mu.Unlock()
	ApplyTheme(theme)
}

// choosePage follows a sidebar choice: a session in its own window comes forward, anything else
// becomes the main window's page.
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

// iconBtn is a 32-DIP icon button with a tooltip; on draws it in the accent's tint.
func iconBtn(c *ui.Context, key string, icon *ui.SVG, tip string, on bool) ui.Element {
	t := c.Theme()
	k := tok(c)
	b := ui.ButtonBase(c.Key(key)).Size(iconButton, iconButton).Shrink(0).Radius(radiusSM).Center().Label(tip).Tooltip(tip)
	fg := k.text2
	switch {
	case on:
		b.Background(k.brandSoft)
		fg = k.brandSoftText
	case b.Hovered():
		b.Background(t.SurfaceHover)
		fg = t.Text
	}
	b.Children(func() { ui.Icon(c, icon).Size(18, 18).TextColor(fg) })
	return b
}

// windowButtons draws minimize, maximize and close for the frameless main window, at the end of
// the top-right pane's header (Kyokai: the system's would sit half outside the pane).
func (a *App) windowButtons(c *ui.Context) {
	if runtime.GOOS != "windows" {
		return
	}
	drawWindowButtons(c, a.mainWindow(), nil)
}

// noticeCards are the cards above the panes for what waits on the user: a pairing to confirm on
// the phone, a local assistant's request.
func (a *App) noticeCards(c *ui.Context, k tokens) {
	a.mu.Lock()
	pairCode, pairModel := a.pairingCode, a.pairingModel
	approvals := append([]approval(nil), a.approvals...)
	a.mu.Unlock()
	a.firstConnectCard(c, k)
	if pairCode != "" {
		noticeCard(c, k, "notice-pair", iconShield, false, L.PairOnPhoneTitle(pairModel), L.PairOnPhoneSub(), func() {
			ui.Text(c, formatPairCode(pairCode)).Bold().FontSize(sizeTitle).FontFeatures("tnum")
			if ui.Button(c.Key("notice-pair-cancel"), L.ActionCancel()).Clicked() {
				a.CancelPairing()
			}
		})
	}
	for _, ap := range approvals {
		ap := ap
		noticeCard(c, k, "notice-ap-"+ap.ID, iconSpark, false, ap.Title, ap.Detail, func() {
			if ui.Button(c.Key("ap-deny-"+ap.ID), L.ApprovalDeny()).Clicked() {
				a.answerApproval(ap.ID, false)
			}
			if ui.PrimaryButton(c.Key("ap-allow-"+ap.ID), L.ApprovalAllowOnce()).Clicked() {
				a.answerApproval(ap.ID, true)
			}
		})
	}
}

// noticeCard is one card above the panes: an accent edge (or the warning color), an icon tile, a
// title and one sentence, and its controls.
func noticeCard(c *ui.Context, k tokens, key string, icon *ui.SVG, warn bool, title, sub string, controls func()) {
	t := c.Theme()
	edge := k.brand
	if warn {
		edge = k.warn
	}
	ui.Row(c.Key(key)).FillWidth().Shrink(0).Radius(paneRadius).Background(t.Background).Border(1, t.Border).Clip().Children(func() {
		ui.Box(c).Width(noticeEdge).FillHeight().Background(edge)
		ui.Row(c).Grow(1).MinWidth(0).Padding(12, 14).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(tileSize, tileSize).Radius(radiusMD).Background(k.brandSoft).Center().Shrink(0).Children(func() {
				ui.Icon(c, icon).Size(16, 16).TextColor(k.brandSoftText)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, title).Bold().FontSize(sizeBody).SingleLine()
				if sub != "" {
					ui.Text(c, sub).FontSize(sizeSmall).TextColor(k.text2).SingleLine()
				}
			})
			controls()
		})
	})
}

// badgeText caps a counter at "99+"; zero hides it.
func badgeText(n int) string {
	if n > navBadgeMax {
		return strconv.Itoa(navBadgeMax) + "+"
	}
	return strconv.Itoa(n)
}

// validPairCode reports whether s is a 6-digit Wireless Debugging pairing code.
func validPairCode(s string) bool {
	if len(s) != 6 {
		return false
	}
	_, err := strconv.Atoi(s)
	return err == nil
}

// pill is a small rounded label: tinted fill and same-hue words.
func pill(c *ui.Context, key, text string, bg, fg ui.Color) {
	ui.Row(c.Key(key)).Height(24).Padding(0, 9).Radius(pillRadius).Background(bg).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, text).FontSize(sizeSmall).TextColor(fg).SingleLine()
	})
}

// statusDot is an 8-DIP status dot.
func statusDot(c *ui.Context, color ui.Color) {
	ui.Box(c).Size(8, 8).Radius(4).Background(color).Shrink(0)
}

// firstConnectCard offers, for a phone connected over USB for the first time, to install the
// Companion and join the network new phones join; nothing happens without Continue (`INV-11`).
func (a *App) firstConnectCard(c *ui.Context, k tokens) {
	a.mu.Lock()
	fc := a.firstConnect
	a.mu.Unlock()
	if fc == nil {
		return
	}
	netName := a.pairNetworkName()
	noticeCard(c, k, "notice-first", iconUsb, false, L.FirstConnectTitle(fc.Name), L.FirstConnectSub(), func() {
		install, join := fc.Install, fc.Join
		if ui.Checkbox(c.Key("fc-install"), &install, L.FirstConnectInstall()).Changed() {
			a.mu.Lock()
			a.firstConnect.Install = install
			a.mu.Unlock()
		}
		if netName != "" && ui.Checkbox(c.Key("fc-join"), &join, L.FirstConnectJoin(netName)).Changed() {
			a.mu.Lock()
			a.firstConnect.Join = join
			a.mu.Unlock()
		}
		if ui.Button(c.Key("fc-later"), L.FirstConnectLater()).Clicked() {
			a.update(func() { a.firstConnect = nil })
		}
		if ui.PrimaryButton(c.Key("fc-go"), L.FirstConnectContinue()).Clicked() {
			a.update(func() { a.firstConnect = nil })
			if fc.Install {
				a.PairCompanionOverADB(fc.Serial, fc.Name, fc.Join && netName != "")
			}
		}
	})
}
