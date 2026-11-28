package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// pickerState is the + picker (Kyokai's): what to open, a search by name or address, the phones,
// and where it opens.
type pickerState struct {
	Open   bool
	Kind   int // openAs*
	Query  string
	Beside bool
	Device string // the phone whose apps show, for the app kind
}

// openPicker opens the + picker on kind; device, when set, goes straight to that phone's apps.
func (a *App) openPicker(kind int, device string) {
	a.update(func() {
		a.picker = pickerState{Open: true, Kind: kind, Device: device, Beside: a.picker.Beside}
		a.searchOpen = false
	})
	a.Focus()
}

// openPickerBeside opens the + picker to open something beside the pane in focus.
func (a *App) openPickerBeside() {
	a.update(func() {
		a.picker = pickerState{Open: true, Kind: a.picker.Kind, Beside: true}
	})
}

// pickerOpen returns where a choice of the picker opens.
func (a *App) pickerOpenIn() OpenIn {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.picker.Beside {
		return OpenSplit
	}
	return OpenDefault
}

// closePicker closes the + picker.
func (a *App) closePicker() {
	a.update(func() { a.picker.Open = false })
}

// kindOffered reports whether d can open as kind, and why not.
func kindOffered(d DiscoveredDevice, kind int) (bool, string) {
	switch kind {
	case openAsDeX:
		if !genuineDeX(d) {
			return false, L.PickerNoDeX()
		}
	case openAsApp:
		if len(d.Apps) == 0 {
			return false, L.PickerNoApps()
		}
	case openAsCamera:
		if isEmulator(d) {
			return false, L.PickerNoCamera()
		}
	}
	return true, ""
}

// pickerPopover draws the + picker under anchor while it is open.
func (a *App) pickerPopover(c *ui.Context, anchor ui.Element) {
	k := tok(c)
	t := c.Theme()
	ui.PopoverBase(c.Key("picker"), anchor, &a.picker.Open, func(panel ui.Element) {
		panel.Width(pickerW).Padding(8).Gap(6).Radius(radiusLG).Background(t.Surface).Border(1, t.Border).Shadow(0, 12, 32, 0, paneShadow)
		a.mu.Lock()
		p := a.picker
		devs := append([]DiscoveredDevice(nil), a.devices...)
		a.mu.Unlock()
		ui.Segmented(c.Key("picker-kind"), &a.picker.Kind, L.KindDeX(), L.KindScreen(), L.KindApp(), L.KindCamera()).FillWidth()

		if p.Kind == openAsApp && p.Device != "" {
			for _, d := range devs {
				if d.HardwareHash == p.Device {
					a.appPickerFor(c, k, d.HardwareHash, a.phoneName(d), d.Apps, func(ap adb.AppEntry) {
						a.LaunchAppWindow(d, ap, a.pickerOpenIn())
					})
					return
				}
			}
			if r, ok := a.RememberedFor(p.Device); ok && r.Companion != nil {
				apps := a.appsForDevice(r.HardwareHash)
				if len(apps) > 0 {
					if a.needsCompanionAppFetch(r.HardwareHash) {
						go a.fetchApps(r.HardwareHash, false)
					}
					a.appPickerFor(c, k, r.HardwareHash, r.displayName(), apps, func(ap adb.AppEntry) {
						a.LaunchDaemonAppWindow(r.HardwareHash, ap, a.pickerOpenIn())
					})
					return
				}
				go a.fetchApps(r.HardwareHash, false)
				a.appPickerLoading(c, k, r.HardwareHash, r.displayName())
				return
			}
		}

		q := ui.TextInput(c.Key("picker-q"), &a.picker.Query).Placeholder(L.PickerSearch()).FillWidth().AutoFocus()
		query := strings.TrimSpace(p.Query)
		var first func()
		row := func(key string, icon func(), title, sub string, enabled bool, onPick func()) {
			if enabled && first == nil {
				first = onPick
			}
			b := ui.ButtonBase(c.Key(key)).FillWidth().Padding(8).Radius(radiusMD).Label(title).Disabled(!enabled)
			if b.Hovered() && enabled {
				b.Background(k.brandSoft)
			}
			b.Children(func() {
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Opacity(map[bool]float32{true: 1, false: 0.55}[enabled]).Children(func() {
					icon()
					ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
						ui.Text(c, title).FontSize(sizeBody).SingleLine()
						if sub != "" {
							ui.Text(c, sub).FontSize(sizeSmall).TextColor(k.text3).SingleLine()
						}
					})
				})
			})
			if b.Clicked() && enabled {
				onPick()
			}
		}
		label := func(key, s string) {
			ui.Text(c.Key(key), s).FontSize(sizeSmall).TextColor(k.text3).Padding(6, 8, 2, 8)
		}
		match := func(s ...string) bool {
			if query == "" {
				return true
			}
			for _, x := range s {
				if strings.Contains(strings.ToLower(x), strings.ToLower(query)) {
					return true
				}
			}
			return false
		}

		ui.Scroll(c.Key("picker-list")).FillWidth().MaxHeight(380).Children(func() {
			ui.Column(c).FillWidth().Gap(1).Children(func() {
				label("picker-phones", L.HomePhones())
				offline := a.offlineRemembered(devs)
				if len(devs) == 0 && len(offline) == 0 && query == "" {
					row("pk-add-empty", func() { iconTile(c, k, iconPlus) }, L.NavAddPhone(), L.HomeMyPhonesSub(), true, func() {
						a.closePicker()
						a.OpenAddWizard(0)
					})
				}
				for _, d := range devs {
					name := a.phoneName(d)
					if d.Target.State != "device" || !match(name, deviceModel(d), d.Target.Serial) {
						continue
					}
					ok, why := kindOffered(d, p.Kind)
					route, _ := a.routeLine(d)
					sub := route
					if !ok {
						sub = why
					}
					row("pk-"+d.HardwareHash, func() {
						glyph := (*ui.SVG)(nil)
						if isEmulator(d) {
							glyph = iconVM
						}
						phoneAvatar(c, 32, deviceLogo(d), glyph, name)
					}, name, sub, ok, func() {
						if p.Kind == openAsApp {
							a.update(func() { a.picker.Device = d.HardwareHash; a.picker.Query = "" })
							return
						}
						a.closePicker()
						a.openKind(d, p.Kind, a.pickerOpenIn())
					})
				}
				for _, r := range a.offlineRemembered(devs) {
					if !match(r.displayName(), r.Name) {
						continue
					}
					r := r
					if p.Kind == openAsApp && r.Companion != nil {
						apps := a.appsForDevice(r.HardwareHash)
						st, hasSt := a.peerStatusFor(r.HardwareHash)
						if len(apps) > 0 || (hasSt && st.DaemonReady && st.DaemonTrustedDisplay) {
							row("pk-off-"+r.HardwareHash, func() {
								phoneAvatar(c, 32, phoneLogo(r.Maker, r.Brand, r.Model, r.Name), nil, r.displayName())
							}, r.displayName(), L.KindApp(), true, func() {
								if len(apps) == 0 || a.needsCompanionAppFetch(r.HardwareHash) {
									go a.fetchApps(r.HardwareHash, false)
								}
								a.update(func() { a.picker.Device = r.HardwareHash; a.picker.Query = "" })
							})
							continue
						}
					}
					row("pk-off-"+r.HardwareHash, func() {
						phoneAvatar(c, 32, phoneLogo(r.Maker, r.Brand, r.Model, r.Name), nil, r.displayName())
					}, r.displayName(), L.LastConnected(lastSeenWords(r.LastSeen)), true, func() {
						a.closePicker()
						a.connectRemembered(r, p.Kind)
					})
				}
				if query != "" && looksLikeAddress(query) {
					row("pk-addr", func() { iconTile(c, k, iconGlobe) }, L.PickerConnectAddress(query), L.PickerAddressHint(), true, func() {
						a.closePicker()
						a.update(func() { a.connect.Input, a.connect.OpenAs = query, p.Kind })
						a.ConnectByAddress()
					})
				}
				others := a.othersInNetworks()
				if len(others) > 0 && p.Kind != openAsApp {
					label("picker-net", L.PickerInNetworks())
					for _, o := range others {
						if !match(o.Name, o.IPv4) {
							continue
						}
						o := o
						row("pk-net-"+o.Peer, func() { phoneAvatar(c, 32, phoneLogo(o.Name), nil, o.Name) },
							o.Name, o.Network+" · "+routeWords(o.IPv4, o.Direct, o.Latency), true, func() {
								a.closePicker()
								a.update(func() { a.connect.Input, a.connect.OpenAs = o.IPv4, p.Kind })
								a.ConnectByAddress()
							})
					}
				}
			})
		})
		if q.Submitted() && first != nil {
			first()
		}
		a.pickerFoot(c, k)
	})
}

// pickerFoot chooses where the choice opens: on its own, or beside the pane in focus, and offers
// a 1-click "+ Add a phone" button.
func (a *App) pickerFoot(c *ui.Context, k tokens) {
	t := c.Theme()
	ui.Box(c).FillWidth().Height(1).Background(t.Border)
	ui.Row(c.Key("picker-foot")).FillWidth().Gap(8).Padding(6, 4, 2, 4).AlignItems(ui.Center).Children(func() {
		ui.Text(c, L.PickerOpensIn()).FontSize(sizeSmall).TextColor(k.text3)
		idx := 0
		if a.picker.Beside {
			idx = 1
		}
		if ui.Segmented(c.Key("picker-where"), &idx, L.PickerOwn(), L.PickerBeside()).Changed() {
			a.mu.Lock()
			a.picker.Beside = idx == 1
			a.mu.Unlock()
		}
		ui.Box(c).Grow(1)
		if ui.Button(c.Key("picker-add-phone"), "+ "+L.NavAddPhone()).Clicked() {
			a.closePicker()
			a.OpenAddWizard(0)
		}
	})
}

// openKind opens d as kind (an openAs* constant) at open.
func (a *App) openKind(d DiscoveredDevice, kind int, open OpenIn) {
	switch kind {
	case openAsDeX:
		a.LaunchDeX(d, false, open)
	case openAsApp:
		a.openPicker(openAsApp, d.HardwareHash)
	case openAsCamera:
		a.LaunchPhoneCamera(d, a.cameraFacing(), open)
	default:
		a.LaunchMirror(d, open)
	}
}

// appPickerFor is the picker's app grid for one phone: a search, user apps first, a tile per app.
func (a *App) appPickerFor(c *ui.Context, k tokens, hash, phoneTitle string, apps []adb.AppEntry, onLaunch func(adb.AppEntry)) {
	t := c.Theme()
	a.appPickerHead(c, k, hash, phoneTitle)
	ui.TextInput(c.Key("apps-q"), &a.picker.Query).Placeholder(L.AppFilterHint()).FillWidth().AutoFocus()
	a.mu.Lock()
	query := strings.ToLower(strings.TrimSpace(a.picker.Query))
	a.mu.Unlock()
	var shown []adb.AppEntry
	for _, ap := range apps {
		if query == "" && ap.System {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(ap.Label), query) && !strings.Contains(strings.ToLower(ap.Package), query) {
			continue
		}
		shown = append(shown, ap)
	}
	if len(shown) == 0 && query == "" {
		shown = apps
	}
	ui.Scroll(c.Key("apps-grid")).FillWidth().MaxHeight(360).Children(func() {
		ui.Row(c).FillWidth().Gap(4).Wrap().Children(func() {
			for _, ap := range shown {
				ap := ap
				tile := ui.ButtonBase(c.Key("app-"+ap.Package)).Width(appTileW).Padding(appTilePadV, appTilePadH).Radius(radiusMD).Center().Label(ap.Label).Tooltip(ap.Label)
				if tile.Hovered() {
					tile.Background(t.SurfaceHover)
				}
				tile.Children(func() {
					ui.Column(c).Gap(6).AlignItems(ui.Center).FillWidth().Children(func() {
						if bmp := a.appIconFor(ap.IconPath); bmp != nil {
							ui.Image(c, bmp).Size(appTileIconSz, appTileIconSz).Radius(10)
						} else {
							ui.Box(c).Size(appTileIconSz, appTileIconSz).Radius(10).Background(k.brandSoft).Center().Children(func() {
								ui.Text(c, appInitial(ap.Label)).Bold().FontSize(sizeHeading).TextColor(k.brandSoftText)
							})
						}
						ui.Text(c, ap.Label).FontSize(sizeSmall).SingleLine()
					})
				})
				if tile.Clicked() {
					a.closePicker()
					onLaunch(ap)
				}
			}
		})
	})
	a.pickerFoot(c, k)
}

// appPickerHead is the top of the picker's app grid: Back, the phone's name, and Reload apps,
// which fetches the list, names and icons again (fetchApps); under it, why the last fetch failed.
func (a *App) appPickerHead(c *ui.Context, k tokens, hash, phoneTitle string) {
	st := a.appsFetchFor(hash)
	ui.Row(c.Key("apps-head")).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
		if iconBtn(c, "apps-back", iconBack, L.ActionBack(), false).Clicked() {
			a.update(func() { a.picker.Device = "" })
		}
		ui.Text(c, phoneTitle).Bold().FontSize(sizeBody).Grow(1).SingleLine()
		if st.Busy {
			ui.Box(c.Key("apps-reloading")).Size(iconButton, iconButton).Center().Children(func() {
				ui.Spinner(c).Size(16, 16)
			})
		} else if iconBtn(c, "apps-reload", iconRefresh, L.ActionReloadApps(), false).Clicked() {
			go a.fetchApps(hash, true)
		}
	})
	if !st.Busy && st.Err != "" {
		ui.Text(c.Key("apps-err"), st.Err).FontSize(sizeSmall).TextColor(k.badText).FillWidth()
	}
}

func (a *App) appPickerLoading(c *ui.Context, k tokens, hash, phoneTitle string) {
	a.appPickerHead(c, k, hash, phoneTitle)
	if st := a.appsFetchFor(hash); !st.Busy && st.Err != "" {
		a.pickerFoot(c, k)
		return
	}
	ui.Row(c.Key("apps-loading")).FillWidth().Padding(24, 12).Gap(10).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Spinner(c).Size(16, 16)
		ui.Text(c, L.PickerLoadingApps()).FontSize(sizeSmall).TextColor(k.text2)
	})
	a.pickerFoot(c, k)
}

// toggleSearch opens or closes search (Ctrl K, or the magnifier).
func (a *App) toggleSearch() {
	a.update(func() {
		a.searchOpen = !a.searchOpen
		a.searchQuery = ""
		a.picker.Open = false
	})
}

// searchResult is one row of search.
type searchResult struct {
	key, group, title, sub string
	icon                   func(c *ui.Context)
	run                    func()
}

// searchResults gathers what matches query, by group, at most six each: open pictures, phones,
// apps (only once something is typed), actions and settings (only once something is typed).
func (a *App) searchResults(c *ui.Context, query string) []searchResult {
	words := strings.Fields(strings.ToLower(query))
	matches := func(s ...string) bool {
		all := strings.ToLower(strings.Join(s, " "))
		for _, w := range words {
			if !strings.Contains(all, w) {
				return false
			}
		}
		return true
	}
	var out []searchResult
	add := func(r searchResult, group map[string]int) {
		if group[r.group] < 6 {
			group[r.group]++
			out = append(out, r)
		}
	}
	n := map[string]int{}
	for _, s := range a.sessionsSorted() {
		s := s
		title := a.paneLabel(s)
		if matches(title) {
			add(searchResult{key: "s-" + s.spec.ID, group: L.NavOpen(), title: title, icon: func(c *ui.Context) { a.sessionIcon(c, s, 18) }, run: s.FocusWindow}, n)
		}
	}
	a.mu.Lock()
	devs := append([]DiscoveredDevice(nil), a.devices...)
	a.mu.Unlock()
	for _, d := range devs {
		d := d
		name := a.phoneName(d)
		if d.Target.State != "device" || !matches(name, deviceModel(d)) {
			continue
		}
		kind, label := openAsScreen, L.KindScreen()
		if genuineDeX(d) {
			kind, label = openAsDeX, L.KindDeX()
		}
		add(searchResult{key: "d-" + d.HardwareHash, group: L.HomePhones(), title: name, sub: label,
			icon: func(c *ui.Context) { phoneAvatar(c, 18, deviceLogo(d), nil, name) },
			run:  func() { a.openKind(d, kind, OpenDefault) }}, n)
	}
	if len(words) > 0 {
		for _, d := range devs {
			d := d
			for _, ap := range d.Apps {
				ap := ap
				if !matches(ap.Label, ap.Package) {
					continue
				}
				add(searchResult{key: "a-" + d.HardwareHash + ap.Package, group: L.SearchApps(), title: ap.Label, sub: L.SearchOpenOn(a.phoneName(d)),
					icon: func(c *ui.Context) {
						if bmp := a.appIconFor(ap.IconPath); bmp != nil {
							ui.Image(c, bmp).Size(18, 18).Radius(4)
						} else {
							ui.Icon(c, iconAppWin).Size(18, 18)
						}
					},
					run: func() { a.LaunchAppWindow(d, ap, OpenDefault) }}, n)
			}
		}
		for _, r := range a.offlineRemembered(devs) {
			r := r
			if r.Companion == nil {
				continue
			}
			for _, ap := range a.appsForDevice(r.HardwareHash) {
				ap := ap
				if !matches(ap.Label, ap.Package) {
					continue
				}
				add(searchResult{key: "a-off-" + r.HardwareHash + ap.Package, group: L.SearchApps(), title: ap.Label, sub: L.SearchOpenOn(r.displayName()),
					icon: func(c *ui.Context) {
						if bmp := a.appIconFor(ap.IconPath); bmp != nil {
							ui.Image(c, bmp).Size(18, 18).Radius(4)
						} else {
							ui.Icon(c, iconAppWin).Size(18, 18)
						}
					},
					run: func() { a.LaunchDaemonAppWindow(r.HardwareHash, ap, OpenDefault) }}, n)
			}
		}
	}
	actions := []searchResult{
		{key: "x-add", title: L.NavAddPhone(), icon: func(c *ui.Context) { ui.Icon(c, iconPlus).Size(18, 18) }, run: func() { a.OpenAddWizard(0) }},
		{key: "x-connect", title: L.ConnectPhone(), icon: func(c *ui.Context) { ui.Icon(c, iconLink).Size(18, 18) }, run: func() { a.openPicker(openAsScreen, "") }},
		{key: "x-settings", title: L.NavSettings(), icon: func(c *ui.Context) { ui.Icon(c, iconSettings).Size(18, 18) }, run: func() { a.openSettings("") }},
		{key: "x-sidebar", title: L.SidebarToggle(), icon: func(c *ui.Context) { ui.Icon(c, iconSidebar).Size(18, 18) }, run: func() {
			a.mu.Lock()
			a.prefs.SidebarRail = !a.prefs.SidebarRail
			a.savePrefs()
			a.mu.Unlock()
		}},
		{key: "x-light", title: L.ThemeLight(), icon: func(c *ui.Context) { ui.Icon(c, iconSun).Size(18, 18) }, run: func() { a.setTheme("light") }},
		{key: "x-dark", title: L.ThemeDark(), icon: func(c *ui.Context) { ui.Icon(c, iconMoon).Size(18, 18) }, run: func() { a.setTheme("dark") }},
		{key: "x-system", title: L.ThemeSystem(), icon: func(c *ui.Context) { ui.Icon(c, iconAuto).Size(18, 18) }, run: func() { a.setTheme("system") }},
	}
	if a.compConnected {
		actions = append(actions, searchResult{key: "x-clip", title: L.ActionSendClipboard(), icon: func(c *ui.Context) { ui.Icon(c, iconClipboard).Size(18, 18) },
			run: func() { a.SendClipboardToPhone(c.ReadClipboard()) }})
	}
	for _, r := range actions {
		r.group = L.SearchActions()
		if matches(r.title) {
			add(r, n)
		}
	}
	if len(words) > 0 {
		for _, e := range settingsIndex() {
			e := e
			if matches(e.title, e.words) {
				add(searchResult{key: "set-" + e.key, group: L.NavSettings(), title: e.title, sub: settingsPageName(e.page),
					icon: func(c *ui.Context) { ui.Icon(c, iconSettings).Size(18, 18) },
					run:  func() { a.openSettings(e.page) }}, n)
			}
		}
	}
	return out
}

// searchPalette is Kyokai's search: a field 72 DIPs from the window's top over the window lightly
// dimmed, then the results by group; Enter opens the first, Escape or a click outside closes it.
func (a *App) searchPalette(c *ui.Context, k tokens) {
	a.mu.Lock()
	open, query := a.searchOpen, a.searchQuery
	a.mu.Unlock()
	if !open {
		return
	}
	t := c.Theme()
	ui.DialogBase(c.Key("search"), &a.searchOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.12)).AlignItems(ui.Center).Justify(ui.Start).Padding(paletteTop, 0, 0, 0)
		panel.Width(paletteW).Radius(cardRadius).Background(t.Surface).Border(1, t.Border).Shadow(0, 16, 40, 0, paneShadow).Clip()
		results := a.searchResults(c, query)
		ui.Row(c.Key("search-head")).FillWidth().Height(52).Padding(0, 16).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconSearch).Size(18, 18).TextColor(k.text2)
			in := ui.TextInput(c.Key("search-q"), &a.searchQuery).Placeholder(L.SearchPlaceholder()).Grow(1).AutoFocus()
			if in.Submitted() && len(results) > 0 {
				a.toggleSearch()
				results[0].run()
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(t.Border)
		ui.Scroll(c.Key("search-list")).FillWidth().MaxHeight(480).Padding(6).Children(func() {
			ui.Column(c).FillWidth().Gap(1).Children(func() {
				group := ""
				for _, r := range results {
					if r.group != group {
						group = r.group
						ui.Text(c.Key("sg-"+group), group).FontSize(sizeSmall).TextColor(k.text3).Padding(8, 10, 2, 10)
					}
					r := r
					b := ui.ButtonBase(c.Key(r.key)).FillWidth().Height(40).Padding(0, 10).Radius(radiusSM).Label(r.title)
					if b.Hovered() {
						b.Background(k.brandSoft)
					}
					b.Children(func() {
						ui.Row(c).FillHeight().FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
							r.icon(c)
							ui.Text(c, r.title).FontSize(sizeBody).SingleLine().MinWidth(0)
							if r.sub != "" {
								ui.Text(c, r.sub).FontSize(sizeSmall).TextColor(k.text3).SingleLine().MinWidth(0)
							}
						})
					})
					if b.Clicked() {
						a.toggleSearch()
						r.run()
					}
				}
				if len(results) == 0 {
					ui.Text(c.Key("search-none"), L.SearchNothing()).FontSize(sizeBody).TextColor(k.text3).Padding(16)
				}
			})
		})
	})
}

// iconTile is a row's fixed-size icon container.
func iconTile(c *ui.Context, k tokens, icon *ui.SVG) {
	ui.Box(c).Size(tileSize, tileSize).Shrink(0).Radius(radiusMD).Background(k.brandSoft).Center().Children(func() {
		ui.Icon(c, icon).Size(navIconSize, navIconSize).TextColor(k.brandSoftText)
	})
}
