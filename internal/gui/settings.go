package gui

import (
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
	"github.com/YuanZhe-99/AndroFuse/internal/vm"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// Settings pages.
const (
	setGeneral  = "general"
	setPhones   = "phones"
	setCamera   = "camera"
	setNetwork  = "network"
	setVM       = "vm"
	setSecurity = "security"
	setAbout    = "about"
)

// openSettings opens the Settings sheet at page ("" keeps the last one).
func (a *App) openSettings(page string) {
	a.update(func() {
		a.settingsOpen = true
		if page != "" {
			a.settingsPage = page
		}
		if a.settingsPage == "" {
			a.settingsPage = setGeneral
		}
		a.searchOpen, a.picker.Open = false, false
	})
	a.Focus()
}

// settingsPageName names a Settings page.
func settingsPageName(p string) string {
	switch p {
	case setPhones:
		return L.SetPhones()
	case setCamera:
		return L.SetCamera()
	case setNetwork:
		return L.SettingsNetwork()
	case setVM:
		return L.VMTitle()
	case setSecurity:
		return L.SetSecurity()
	case setAbout:
		return L.SetAbout()
	default:
		return L.SettingsGeneral()
	}
}

// settingEntry is one setting search finds.
type settingEntry struct{ key, page, title, words string }

// settingsIndex lists every setting by page, for Settings' and the palette's search.
func settingsIndex() []settingEntry {
	return []settingEntry{
		{"lang", setGeneral, L.SettingsLanguage(), "language"},
		{"look", setGeneral, L.SettingsAppearance(), "theme dark light"},
		{"login", setGeneral, L.SetStartAtLogin(), "startup login"},
		{"close", setGeneral, L.SetOnClose(), "tray quit"},
		{"open", setGeneral, L.SetStreamsOpen(), "pane window"},
		{"remembered", setPhones, L.SetRemembered(), "forget rename"},
		{"size", setPhones, L.SetMaxSize(), "resolution 1080p 720p"},
		{"fps", setPhones, L.SetMaxFPS(), "frame rate"},
		{"codec", setPhones, L.SetCodec(), "h.264 h.265 hevc"},
		{"sound", setPhones, L.SetPlaySound(), "audio"},
		{"connmode", setPhones, L.SetConnMode(), "connection adb companion priority"},
		{"autoswitch", setPhones, L.SetAutoSwitch(), "auto switch upgrade fallback 5g wifi"},
		{"handoff", setPhones, L.SetHandoff(), "usb wi-fi"},
		{"offer", setPhones, L.SetOfferCompanion(), "companion"},
		{"cam", setCamera, L.SetDefaultCamera(), "front back"},
		{"vcam", setCamera, L.SetCameraForApps(), "webcam virtual"},
		{"mic", setCamera, L.SetMicToPhone(), "microphone"},
		{"nets", setNetwork, L.SetNetworks(), "easytier mesh"},
		{"accept", setNetwork, L.SetAcceptDirect(), "listen port"},
		{"p2p", setNetwork, L.SetTryDirect(), "p2p"},
		{"vmrow", setVM, L.VMTitle(), "emulator virtual machine"},
		{"assistant", setSecurity, L.SetAssistant(), "mcp ai"},
		{"updates", setAbout, L.SetUpdates(), "update"},
		{"logs", setAbout, L.SetSaveLogs(), "logs diagnostics"},
	}
}

// settingsSheet is Settings: a sheet over the whole window (Kyokai's), its sections on the left
// under a search, the page on the right; Escape, × or a click outside closes it.
func (a *App) settingsSheet(c *ui.Context, k tokens) {
	a.mu.Lock()
	open, page, query := a.settingsOpen, a.settingsPage, strings.TrimSpace(a.settingsQuery)
	a.mu.Unlock()
	if !open {
		return
	}
	t := c.Theme()
	w, h := c.Size()
	mx := min(sheetMarginX, max(16, (w-760)/2))
	ui.DialogBase(c.Key("settings"), &a.settingsOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.28))
		panel.Width(w-2*mx).Height(h-sheetMarginT-sheetMarginB).Radius(cardRadius).Background(t.Background).
			Border(1, t.Border).Shadow(0, 16, 40, 0, paneShadow).Clip()
		ui.Row(c.Key("set-head")).FillWidth().Height(52).Shrink(0).Padding(0, 8, 0, 20).AlignItems(ui.Center).Children(func() {
			ui.Text(c, L.NavSettings()).Bold().FontSize(sizeHeading).Grow(1)
			if iconBtn(c, "set-close", winClose, L.PaneClose(), false).Clicked() {
				a.update(func() { a.settingsOpen = false })
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(t.Border)
		ui.Row(c.Key("set-body")).Grow(1).FillWidth().MinHeight(0).Children(func() {
			ui.Scroll(c.Key("set-nav")).Width(sheetNavW).Shrink(0).FillHeight().Padding(12, 10).Children(func() {
				ui.Column(c).FillWidth().Gap(2).Children(func() {
					ui.TextInput(c.Key("set-search"), &a.settingsQuery).Placeholder(L.SetSearch()).FillWidth()
					if query != "" {
						a.settingsHits(c, k, query)
						return
					}
					group := func(key, label string) {
						ui.Text(c.Key(key), label).FontSize(sizeSmall).TextColor(k.text3).Padding(12, 10, 4, 10)
					}
					item := func(id string, icon *ui.SVG) {
						on := page == id
						b := ui.ButtonBase(c.Key("set-nav-"+id)).FillWidth().Height(36).Padding(0, 10).Radius(radiusMD).Label(settingsPageName(id))
						fg := t.Text
						switch {
						case on:
							b.Background(k.brandSoft)
							fg = k.brandSoftText
						case b.Hovered():
							b.Background(t.SurfaceHover)
						}
						b.Children(func() {
							ui.Row(c).FillHeight().FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
								ui.Icon(c, icon).Size(18, 18).TextColor(fg)
								ui.Text(c, settingsPageName(id)).FontSize(sizeBody).TextColor(fg).SingleLine()
							})
						})
						if b.Clicked() {
							a.update(func() { a.settingsPage = id })
						}
					}
					group("sg-pref", L.SetGroupPrefs())
					item(setGeneral, iconSettings)
					group("sg-phones", L.HomePhones())
					item(setPhones, iconDevices)
					item(setCamera, iconCamera)
					group("sg-pc", L.HomeThisComputer())
					item(setNetwork, iconMesh)
					item(setVM, iconVM)
					item(setSecurity, iconShield)
					group("sg-app", L.AppName())
					item(setAbout, iconInfo)
				})
			})
			ui.Box(c).Width(1).FillHeight().Background(t.Border)
			ui.Scroll(c.Key("set-page")).Grow(1).MinWidth(0).FillHeight().Padding(24, 32, 40, 32).Children(func() {
				ui.Column(c.Key("set-page-" + page)).FillWidth().MaxWidth(pageMaxW).Gap(0).Children(func() {
					switch page {
					case setPhones:
						a.phonesSettings(c, k)
					case setCamera:
						a.cameraSettings(c, k)
					case setNetwork:
						a.networkSettings(c, k)
					case setVM:
						a.vmSettings(c, k)
					case setSecurity:
						a.securitySettings(c, k)
					case setAbout:
						a.aboutSettings(c, k)
					default:
						a.generalSettings(c, k)
					}
				})
			})
		})
	})
}

// settingsHits replaces the sections while the search holds words: each matching setting with its
// page; choosing one opens that page.
func (a *App) settingsHits(c *ui.Context, k tokens, query string) {
	words := strings.Fields(strings.ToLower(query))
	t := c.Theme()
	for _, e := range settingsIndex() {
		all := strings.ToLower(e.title + " " + e.words)
		ok := true
		for _, w := range words {
			ok = ok && strings.Contains(all, w)
		}
		if !ok {
			continue
		}
		e := e
		b := ui.ButtonBase(c.Key("hit-"+e.key)).FillWidth().Padding(8, 10).Radius(radiusMD).Label(e.title)
		if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
		b.Children(func() {
			ui.Column(c).Gap(1).Children(func() {
				ui.Text(c, e.title).FontSize(sizeBody).SingleLine()
				ui.Text(c, settingsPageName(e.page)).FontSize(sizeSmall).TextColor(k.text3).SingleLine()
			})
		})
		if b.Clicked() {
			a.update(func() { a.settingsPage, a.settingsQuery = e.page, "" })
		}
	}
}

// pageHead is a Settings page's title and one sentence on what it is for.
func pageHead(c *ui.Context, k tokens, title, lead string) {
	ui.Text(c.Key("page-title"), title).Bold().FontSize(sizeTitle)
	ui.Text(c.Key("page-lead"), lead).FontSize(sizeBody).TextColor(k.text2).Padding(4, 0, 12, 0)
}

// section is a muted title (extra on its right) over a rounded list of rows.
func section(c *ui.Context, k tokens, key, title string, extra func(), rows func()) {
	t := c.Theme()
	ui.Row(c.Key(key+"-t")).FillWidth().MinHeight(28).Padding(20, 0, 8, 0).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).Bold().FontSize(sizeSmall).TextColor(k.text2).Grow(1)
		if extra != nil {
			extra()
		}
	})
	ui.Column(c.Key(key)).FillWidth().Radius(radiusLG).Background(t.Surface).Border(1, t.Border).Clip().Children(rows)
}

// srow is a settings row: an icon tile, a title and one sentence, the control at its end; a line
// above every row but the first of its list.
func srow(c *ui.Context, k tokens, key string, first bool, icon *ui.SVG, title, sub string, control func()) {
	t := c.Theme()
	if !first {
		ui.Box(c.Key(key + "-line")).FillWidth().Height(1).Background(t.Border)
	}
	ui.Row(c.Key(key)).FillWidth().MinHeight(rowMinH).Padding(12, 14).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(tileSize, tileSize).Radius(radiusMD).Background(k.sunk).Border(1, t.Border).Center().Shrink(0).Children(func() {
			ui.Icon(c, icon).Size(16, 16).TextColor(k.text2)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, title).Bold().FontSize(sizeBody)
			if sub != "" {
				ui.Text(c, sub).FontSize(sizeSmall).TextColor(k.text2)
			}
		})
		if control != nil {
			ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(control)
		}
	})
}

// subRow is a full-width block under the row it belongs to (a form, a list).
func subRow(c *ui.Context, k tokens, key string, fn func()) {
	t := c.Theme()
	ui.Box(c.Key(key + "-line")).FillWidth().Height(1).Background(t.Border)
	ui.Column(c.Key(key)).FillWidth().Padding(14, 14, 16, subRowIndent).Gap(10).Background(k.sunk).Children(fn)
}

// prefSwitch is a switch bound to a preference; set saves it.
func (a *App) prefSwitch(c *ui.Context, key string, on bool, label string, set func(p *Prefs, on bool)) {
	v := on
	if ui.Switch(c.Key(key), &v).Label(label).Changed() {
		a.mu.Lock()
		set(&a.prefs, v)
		a.savePrefs()
		a.mu.Unlock()
	}
}

// prefChoice is a segmented choice bound to a preference.
func (a *App) prefChoice(c *ui.Context, key string, idx int, set func(p *Prefs, i int), labels ...string) {
	v := idx
	if ui.Segmented(c.Key(key), &v, labels...).Changed() {
		a.mu.Lock()
		set(&a.prefs, v)
		a.savePrefs()
		a.mu.Unlock()
	}
}

func (a *App) generalSettings(c *ui.Context, k tokens) {
	a.mu.Lock()
	p := a.prefs
	a.mu.Unlock()
	pageHead(c, k, L.SettingsGeneral(), L.SetGeneralLead())
	section(c, k, "gen-look", L.SetLookTitle(), nil, func() {
		srow(c, k, "gen-lang", true, iconGlobe, L.SettingsLanguage(), L.SettingsLanguageSub(), func() {
			if ui.Segmented(c.Key("lang-seg"), &a.langIndex, L.LangSystem(), L.LangZhHans(), L.LangEnglish()).Changed() {
				langs := []string{"", "zh-Hans", "en"}
				a.mu.Lock()
				a.prefs.Language = langs[a.langIndex]
				a.savePrefs()
				a.mu.Unlock()
				mygo.App.SetLocale(a.prefs.Language)
			}
		})
		srow(c, k, "gen-theme", false, iconAuto, L.SettingsAppearance(), L.SettingsAppearanceSub(), func() {
			if ui.Segmented(c.Key("theme-seg"), &a.themeIndex, L.ThemeSystem(), L.ThemeLight(), L.ThemeDark()).Changed() {
				a.setTheme([]string{"system", "light", "dark"}[a.themeIndex])
			}
		})
	})
	section(c, k, "gen-start", L.SetStartTitle(), nil, func() {
		srow(c, k, "gen-login", true, iconPower, L.SetStartAtLogin(), L.SetStartAtLoginSub(), func() {
			on := a.openAtLogin
			if ui.Switch(c.Key("login-sw"), &on).Label(L.SetStartAtLogin()).Changed() {
				a.mu.Lock()
				a.openAtLogin = on
				a.mu.Unlock()
				go func() { _ = mygo.App.SetOpenAtLogin(on) }()
			}
		})
		tray := p.TrayOnLogin == nil || *p.TrayOnLogin
		srow(c, k, "gen-tray", false, iconTray, L.SetTrayOnLogin(), L.SetTrayOnLoginSub(), func() {
			a.prefSwitch(c, "tray-sw", tray, L.SetTrayOnLogin(), func(p *Prefs, on bool) { p.TrayOnLogin = &on })
		})
		close := 0
		if p.CloseQuits {
			close = 1
		}
		srow(c, k, "gen-close", false, winClose, L.SetOnClose(), L.SetOnCloseSub(), func() {
			a.prefChoice(c, "close-seg", close, func(p *Prefs, i int) { p.CloseQuits = i == 1 }, L.SetCloseTray(), L.SetCloseQuit())
		})
	})
	section(c, k, "gen-open", L.SetOpenTitle(), nil, func() {
		s, ap := 0, 1
		if p.StreamsInWindow {
			s = 1
		}
		if !p.AppsInWindow {
			ap = 0
		}
		srow(c, k, "gen-streams", true, iconDex, L.SetStreamsOpen(), L.SetStreamsOpenSub(), func() {
			a.prefChoice(c, "streams-seg", s, func(p *Prefs, i int) { p.StreamsInWindow = i == 1 }, L.OpenPaneShort(), L.OpenWindowShort())
		})
		srow(c, k, "gen-apps", false, iconAppWin, L.SettingsAppsOpen(), L.SettingsAppsOpenSub(), func() {
			a.prefChoice(c, "apps-seg", ap, func(p *Prefs, i int) { p.AppsInWindow = i == 1 }, L.OpenPaneShort(), L.OpenWindowShort())
		})
	})
}

func (a *App) phonesSettings(c *ui.Context, k tokens) {
	a.mu.Lock()
	p := a.prefs
	a.mu.Unlock()
	pageHead(c, k, L.SetPhones(), L.SetPhonesLead())
	list := a.rememberedList()
	section(c, k, "ph-rem", L.SetRemembered(), func() {
		ui.Text(c, fmt.Sprint(len(list))).FontSize(sizeSmall).TextColor(k.text3)
	}, func() {
		if len(list) == 0 {
			srow(c, k, "ph-none", true, iconDevices, L.SetRememberedNone(), "", nil)
		}
		for i, r := range list {
			r := r
			srow(c, k, "ph-"+r.HardwareHash, i == 0, iconDevices, r.displayName(), phoneDetail(r.displayName(), r.Name, r.Android), func() {
				auto := r.AutoReconnect
				ui.Text(c, L.RememberedAuto()).FontSize(sizeSmall).TextColor(k.text2)
				if ui.Switch(c.Key("ph-auto-"+r.HardwareHash), &auto).Label(L.RememberedAuto()).Changed() {
					a.SetAutoReconnect(r.HardwareHash, auto)
				}
				iconBtn(c, "ph-more-"+r.HardwareHash, iconMore, L.PaneMore(), false).PopoverMenuButton(func(m *ui.Menu) {
					if m.Item(L.ActionRename()).Chosen() {
						a.update(func() { a.settingsOpen, a.page = false, pageHome })
						a.startRename(r.HardwareHash)
					}
					if m.Item(L.RememberedForgetPhone()).Danger().Chosen() {
						if gone, ok := a.ForgetDevice(r.HardwareHash); ok {
							c.ToastAction(L.RememberedForgotten(gone.displayName()), L.ActionUndo(), func() { a.RestoreDevice(gone) })
						}
					}
				})
			})
		}
	})
	size := map[int]int{0: 0, 1920: 1, 1280: 2}[p.MaxSize]
	fps := 0
	if p.MaxFPS == 30 {
		fps = 1
	}
	codec := map[string]int{"": 0, "h264": 1, "h265": 2}[p.Codec]
	section(c, k, "ph-pic", L.SetPicture(), nil, func() {
		srow(c, k, "ph-size", true, iconDex, L.SetMaxSize(), L.SetMaxSizeSub(), func() {
			a.prefChoice(c, "size-seg", size, func(p *Prefs, i int) { p.MaxSize = []int{0, 1920, 1280}[i] }, L.SetFollowPhone(), "1080p", "720p")
		})
		srow(c, k, "ph-fps", false, iconPulse, L.SetMaxFPS(), L.SetMaxFPSSub(), func() {
			a.prefChoice(c, "fps-seg", fps, func(p *Prefs, i int) { p.MaxFPS = []int{0, 30}[i] }, "60", "30")
		})
		srow(c, k, "ph-codec", false, iconCPU, L.SetCodec(), L.SetCodecSub(), func() {
			a.prefChoice(c, "codec-seg", codec, func(p *Prefs, i int) { p.Codec = []string{"", "h264", "h265"}[i] }, L.SetAuto(), "H.264", "H.265")
		})
		srow(c, k, "ph-sound", false, iconVolumeOn, L.SetPlaySound(), L.SetPlaySoundSub(), func() {
			a.prefSwitch(c, "sound-sw", !p.MuteSound, L.SetPlaySound(), func(p *Prefs, on bool) { p.MuteSound = !on })
		})
	})
	section(c, k, "ph-conn", L.SetConnection(), nil, func() {
		connIdx := 0
		switch p.ConnectionMode {
		case ConnBothCompanion:
			connIdx = 1
		case ConnCompanionOnly:
			connIdx = 2
		}
		srow(c, k, "ph-conn-mode", true, iconLink, L.SetConnMode(), L.SetConnModeSub(), nil)
		subRow(c, k, "ph-conn-mode-pick", func() {
			a.prefChoice(c, "conn-mode-seg", connIdx, func(p *Prefs, i int) {
				p.ConnectionMode = []string{ConnBothADB, ConnBothCompanion, ConnCompanionOnly}[i]
			}, L.ConnModeBothADB(), L.ConnModeBothComp(), L.ConnModeCompOnly())
		})
		srow(c, k, "ph-auto-switch", false, iconRefresh, L.SetAutoSwitch(), L.SetAutoSwitchSub(), func() {
			v := p.ConnectionMode != ConnCompanionOnly && !p.NoAutoSwitch
			if ui.Switch(c.Key("auto-switch-sw"), &v).Label(L.SetAutoSwitch()).Disabled(p.ConnectionMode == ConnCompanionOnly).Changed() {
				a.mu.Lock()
				a.prefs.NoAutoSwitch = !v
				a.savePrefs()
				a.mu.Unlock()
			}
		})
		srow(c, k, "ph-handoff", false, iconUsb, L.SetHandoff(), L.SetHandoffSub(), func() {
			a.prefSwitch(c, "handoff-sw", !p.NoUSBHandoff, L.SetHandoff(), func(p *Prefs, on bool) { p.NoUSBHandoff = !on })
		})
		srow(c, k, "ph-offer", false, iconDevices, L.SetOfferCompanion(), L.SetOfferCompanionSub(), func() {
			a.prefSwitch(c, "offer-sw", !p.NoCompanionOffer, L.SetOfferCompanion(), func(p *Prefs, on bool) { p.NoCompanionOffer = !on })
		})
	})
}

func (a *App) cameraSettings(c *ui.Context, k tokens) {
	a.mu.Lock()
	p, vcam, mic := a.prefs, a.vcamCap, a.micUplinkActive
	connected := a.compConnected
	a.mu.Unlock()
	pageHead(c, k, L.SetCamera(), L.SetCameraLead())
	section(c, k, "cam-phone", L.SetPhoneCamera(), nil, func() {
		facing := 0
		if p.FrontCamera {
			facing = 1
		}
		srow(c, k, "cam-facing", true, iconCamera, L.SetDefaultCamera(), L.SetDefaultCameraSub(), func() {
			a.prefChoice(c, "facing-seg", facing, func(p *Prefs, i int) { p.FrontCamera = i == 1 }, L.AVCameraBack(), L.AVCameraFront())
		})
		sub := L.SetCameraForAppsSub()
		if !vcam.MFVirtualCameraAPI {
			sub = L.SetCameraForAppsNoWin11()
		}
		srow(c, k, "cam-apps", false, iconDownload, L.SetCameraForApps(), sub, func() {
			pill(c, "cam-apps-state", L.SetNotInstalled(), k.sunk, k.text2)
		})
	})
	section(c, k, "cam-mic", L.SetComputerMic(), nil, func() {
		srow(c, k, "cam-mic-row", true, iconMic, L.SetMicToPhone(), L.SetMicToPhoneSub(), func() {
			on := mic
			sw := ui.Switch(c.Key("mic-sw"), &on).Label(L.SetMicToPhone()).Disabled(!connected)
			if sw.Changed() {
				if on {
					a.StartCompanionMicUplink()
				} else {
					a.StopCompanionMicUplink()
				}
			}
		})
	})
}

func (a *App) vmSettings(c *ui.Context, k tokens) {
	a.mu.Lock()
	st := a.vmStatus
	a.mu.Unlock()
	if st.NativeABI == "" {
		st.NativeABI, st.SystemImagePkg, _ = vm.NativeABIForArch(runtime.GOARCH)
	}
	pageHead(c, k, L.VMTitle(), L.VMLead())
	section(c, k, "vm-main", L.VMSection(), nil, func() {
		sub := vmStateWords(st)
		if st.StorageBytes > 0 {
			sub += " · " + L.VMSize(fmt.Sprintf("%.1f", float64(st.StorageBytes)/(1<<30)))
		}
		srow(c, k, "vm-state", true, iconVM, L.VMAndroid(), sub+" · "+L.VMNative(), func() { a.vmAction(c, "vm-act", st) })
		if st.State == vm.StateDownloading {
			subRow(c, k, "vm-progress", func() {
				ui.Progress(c.Key("vm-prog"), float64(st.ProgressPct)/100).FillWidth()
			})
		}
		if st.LastError != "" {
			subRow(c, k, "vm-err", func() {
				ui.Text(c, st.LastError).FontSize(sizeSmall).TextColor(k.warnText)
			})
		}
		if st.StorageBytes > 0 && st.State != vm.StateDownloading && st.State != vm.StateStarting {
			srow(c, k, "vm-del", false, iconTrash, L.VMDelete(), L.VMDeleteSub(), func() {
				if ui.Button(c.Key("vm-del-btn"), L.VMDeleteBtn()).Clicked() {
					a.DeleteManagedVM()
				}
			})
		}
	})
}

func (a *App) securitySettings(c *ui.Context, k tokens) {
	a.mu.Lock()
	st, notice, allow := a.mcpStatus, a.mcpNotice, slices.Clone(a.prefs.AssistantAllow)
	a.mu.Unlock()
	pageHead(c, k, L.SetSecurity(), L.SetSecurityLead())
	section(c, k, "sec-ai", L.SetAssistant(), nil, func() {
		srow(c, k, "sec-ai-on", true, iconSpark, L.SetAssistantOn(), L.SetAssistantOnSub(), func() {
			on := st.Enabled
			if ui.Switch(c.Key("ai-sw"), &on).Label(L.SetAssistantOn()).Changed() {
				if on {
					go a.EnableMCP()
				} else {
					go a.EmergencyStopMCP()
				}
			}
		})
		subRow(c, k, "sec-ai-tools", func() {
			ui.Text(c, L.SetAssistantTools()).FontSize(sizeSmall).TextColor(k.text2)
			ui.Row(c.Key("ai-tiles")).FillWidth().Gap(8).Children(func() {
				for _, tool := range []string{toolStatus, toolChecks, toolLaunch} {
					tool := tool
					on := slices.Contains(allow, tool)
					if ui.Checkbox(c.Key("ai-"+tool), &on, assistantToolWords(tool)).Changed() {
						a.mu.Lock()
						if on {
							a.prefs.AssistantAllow = append(a.prefs.AssistantAllow, tool)
						} else {
							a.prefs.AssistantAllow = slices.DeleteFunc(a.prefs.AssistantAllow, func(s string) bool { return s == tool })
						}
						a.savePrefs()
						running := a.mcpStatus.Enabled
						a.mu.Unlock()
						if running {
							go a.EnableMCP()
						}
					}
				}
			})
			ui.Text(c, L.SetAssistantAskHint()).FontSize(sizeSmall).TextColor(k.text3)
			if st.Enabled && st.Addr != "" {
				ui.Text(c, L.SetAssistantAddress(st.Addr)).FontSize(sizeSmall).TextColor(k.text3).FontFeatures("tnum")
			}
			if notice != "" {
				ui.Text(c, notice).FontSize(sizeSmall).TextColor(k.warnText)
			}
		})
		srow(c, k, "sec-ai-log", false, iconLog, L.SetAssistantRecent(), "", func() {
			if st.Enabled && ui.Button(c.Key("ai-stop"), L.SetStopNow()).Clicked() {
				go a.EmergencyStopMCP()
			}
		})
		if len(st.Audit) > 0 {
			subRow(c, k, "sec-ai-audit", func() {
				start := max(0, len(st.Audit)-5)
				for i, e := range st.Audit[start:] {
					ui.Row(c.Key(fmt.Sprintf("audit-%d", i))).Gap(10).Children(func() {
						ui.Text(c, e.Timestamp.Local().Format("15:04")).FontSize(sizeSmall).TextColor(k.text3).FontFeatures("tnum")
						ui.Text(c, auditWords(e.Tool, e.Outcome)).FontSize(sizeSmall)
					})
				}
			})
		}
	})
	section(c, k, "sec-logs", L.SetLogs(), nil, func() {
		srow(c, k, "sec-scrub", true, iconShield, L.SetScrub(), L.SetAlwaysOn(), nil)
	})
}

// auditWords is one local assistant audit entry in words.
func auditWords(tool, outcome string) string {
	what := assistantToolWords(tool)
	if tool == "" {
		what = L.ToolSession()
	}
	switch outcome {
	case "ok", "user_allowed_once":
		return L.AuditAllowed(what)
	case "user_denied", "not_allowlisted":
		return L.AuditDenied(what)
	}
	return what + " · " + outcome
}

func (a *App) aboutSettings(c *ui.Context, k tokens) {
	a.mu.Lock()
	up, d, ready := a.update_, a.diag, a.diagReady
	p := a.prefs
	probing := a.probing
	a.mu.Unlock()
	pageHead(c, k, L.SetAbout(), L.SetAboutLead())
	section(c, k, "ab-app", L.AppName(), nil, func() {
		srow(c, k, "ab-ver", true, iconInfo, "AndroFuse "+version.Version, L.SetLicense(), func() {
			if ui.Button(c.Key("ab-src"), L.SetSourceCode()).Clicked() {
				_ = mygo.Shell.OpenExternal("https://github.com/YuanZhe-99/AndroFuse")
			}
		})
	})
	section(c, k, "ab-up", L.SetUpdates(), nil, func() {
		title, sub := L.UpdateUpToDate(), ""
		if !up.LastCheck.IsZero() {
			sub = L.UpdateLastCheck(up.LastCheck.Local().Format("01-02 15:04"))
		}
		switch {
		case !updatesEnabled():
			title, sub = L.UpdateUnavailable(), L.UpdateUnavailableSub()
		case up.Installing:
			title = L.UpdateInstalling(up.Found.Version)
		case up.Found != nil:
			title = L.UpdateFound(up.Found.Version)
		case up.Checking:
			title = L.UpdateChecking()
		}
		srow(c, k, "ab-up-state", true, iconUpdate, title, sub, func() {
			switch {
			case !updatesEnabled():
			case up.Found != nil && !up.Installing:
				if ui.PrimaryButton(c.Key("up-install"), L.UpdateInstall()).Clicked() {
					a.InstallUpdate()
				}
			default:
				if ui.Button(c.Key("up-check"), L.UpdateCheck()).Disabled(up.Checking || up.Installing).Clicked() {
					a.CheckForUpdates()
				}
			}
		})
		if up.Installing {
			subRow(c, k, "ab-up-prog", func() { ui.Progress(c.Key("up-prog"), up.Progress).FillWidth() })
		}
		if up.Err != "" {
			subRow(c, k, "ab-up-err", func() { ui.Text(c, up.Err).FontSize(sizeSmall).TextColor(k.warnText) })
		}
		if updatesEnabled() {
			srow(c, k, "ab-daily", false, iconRefresh, L.UpdateDaily(), "", func() {
				a.prefSwitch(c, "daily-sw", !p.NoDailyCheck, L.UpdateDaily(), func(p *Prefs, on bool) { p.NoDailyCheck = !on })
			})
			mode := map[string]int{"": 0, autoIdle: 1, autoNight: 2}[p.AutoInstall]
			srow(c, k, "ab-auto", false, iconDownload, L.UpdateAuto(), L.UpdateAutoSub(), func() {
				a.prefChoice(c, "auto-seg", mode, func(p *Prefs, i int) { p.AutoInstall = []string{"", autoIdle, autoNight}[i] }, L.UpdateNever(), L.UpdateIdle(), L.UpdateNight())
			})
		}
	})
	section(c, k, "ab-pc", L.HomeThisComputer(), func() {
		if ui.Button(c.Key("ab-recheck"), L.DiagRefresh()).Disabled(probing).Clicked() {
			a.RefreshDiagnostics()
		}
	}, func() {
		check := func(key string, first bool, icon *ui.SVG, title, sub string, ok bool) {
			srow(c, k, key, first, icon, title, sub, func() {
				if !ready {
					pill(c, key+"-p", L.ReadyChecking(), k.sunk, k.text2)
				} else if ok {
					pill(c, key+"-p", L.CheckOK(), k.good.Alpha(0.14), k.goodText)
				} else {
					pill(c, key+"-p", L.CheckAttention(), k.warn.Alpha(0.16), k.warnText)
				}
			})
		}
		arch := L.CheckNative(d.SelfCheck.NativeArch)
		if d.SelfCheck.IsEmulated {
			arch = L.CheckEmulated(d.SelfCheck.NativeArch)
		}
		check("ab-cpu", true, iconCPU, L.CheckProcessor(), arch, !d.SelfCheck.IsEmulated)
		dec := L.HomeDecodeSoftware()
		if d.SelfCheck.H264HWDecoder && d.SelfCheck.HEVCHWDecoder {
			dec = L.HomeDecodeHardwareBoth()
		} else if d.SelfCheck.H264HWDecoder {
			dec = L.HomeDecodeHardwareH264()
		}
		check("ab-dec", false, iconDex, L.HomePicture(), dec, d.SelfCheck.H264HWDecoder)
		a.mu.Lock()
		adbMissing, adbInstalling, adbPct, adbErr := a.adbMissing, a.adbInstalling, a.adbInstallPct, a.adbInstallErr
		a.mu.Unlock()
		tools := L.CheckToolsMissing()
		switch {
		case adbInstalling:
			tools = L.ADBDownloadingTitle(adbPct)
		case d.ADBFound && d.CoreLoaded:
			tools = L.CheckToolsOK()
		case adbErr != "":
			tools = adbErr
		}
		srow(c, k, "ab-tools", false, iconUsb, L.CheckTools(), tools, func() {
			switch {
			case adbInstalling:
				ui.Spinner(c).Size(16, 16)
			case d.ADBFound && d.CoreLoaded && !adbMissing:
				pill(c, "ab-tools-p", L.CheckOK(), k.good.Alpha(0.14), k.goodText)
			case adbErr != "":
				if ui.Button(c.Key("ab-tools-retry"), L.ADBActionRetry()).Clicked() {
					a.InstallADB()
				}
			default:
				if ui.Button(c.Key("ab-tools-dl"), L.ADBActionDownload()).Clicked() {
					a.InstallADB()
				}
			}
		})
		srow(c, k, "ab-miracast", false, iconWifi, L.CheckMiracast(), L.CheckMiracastSub(), func() {
			pill(c, "ab-miracast-p", L.CheckOff(), k.sunk, k.text2)
		})
	})
	ui.Row(c.Key("ab-buttons")).Gap(8).Padding(12, 0, 0, 0).Children(func() {
		if ui.Button(c.Key("ab-folder"), L.SetOpenDataFolder()).Clicked() {
			_ = mygo.Shell.OpenPath(a.stateDir)
		}
		if ui.Button(c.Key("ab-logs"), L.SetSaveLogs()).Clicked() {
			a.SaveLogs()
		}
	})
	ui.Collapsible(c.Key("ab-tech"), L.SetTechDetails(), &a.techOpen, func() {
		lines := []string{
			fmt.Sprintf("core ABI v%d", d.CoreABI),
			"adb " + d.ADBVersion,
			fmt.Sprintf("Media Foundation: H.264 HW %v, HEVC HW %v", d.SelfCheck.H264HWDecoder, d.SelfCheck.HEVCHWDecoder),
			"EasyTier 2.7.1 (embedded, no TUN)",
			fmt.Sprintf("%s %s build %d", d.SelfCheck.OS, d.SelfCheck.NativeArch, d.SelfCheck.OSBuild),
		}
		for i, l := range lines {
			ui.Text(c.Key(fmt.Sprintf("tech-%d", i)), l).FontSize(sizeSmall).TextColor(k.text2).Font("monospace")
		}
		for i, n := range d.SelfCheck.Notes {
			ui.Text(c.Key(fmt.Sprintf("note-%d", i)), "• "+n).FontSize(sizeSmall).TextColor(k.text3)
		}
	})
}

// networkSettings is Kyokai's Network page: how the networks are doing, the networks with their
// Join switches, peers and the Add / Edit form, the network new phones join, direct connections and
// the advanced choices.
func (a *App) networkSettings(c *ui.Context, k tokens) {
	nets := a.networks()
	a.mu.Lock()
	views := make(map[string]netView, len(a.netViews))
	for id, v := range a.netViews {
		views[id] = v
	}
	form, formErr := a.netForm, a.netFormErr
	mp := MeshPrefs{}
	if a.prefs.Mesh != nil {
		mp = *a.prefs.Mesh
	}
	a.mu.Unlock()
	pageHead(c, k, L.SettingsNetwork(), L.NetLead())
	dot, words := a.networkSummary(k)
	up := 0
	for _, n := range nets {
		if views[n.ID].Status.Running {
			up++
		}
	}
	ui.Column(c.Key("net-sum")).FillWidth().Radius(radiusLG).Background(c.Theme().Surface).Border(1, c.Theme().Border).Children(func() {
		srow(c, k, "net-sum-row", true, iconMesh, words, L.NetSummaryCount(len(nets), up), func() { statusDot(c, dot) })
	})

	section(c, k, "net-list", L.SetNetworks(), func() {
		if ui.Button(c.Key("net-add"), L.NetAdd()).Disabled(len(nets) >= maxNetworks).Clicked() {
			a.update(func() { a.netForm, a.netFormErr = &netForm{}, "" })
		}
	}, func() {
		if form != nil && form.ID == "" {
			a.networkForm(c, k, formErr, true)
		}
		if len(nets) == 0 && form == nil {
			srow(c, k, "net-none", true, iconGlobe, L.NetNone(), L.NetNoneSub(), nil)
		}
		for i, n := range nets {
			a.networkRow(c, k, n, views[n.ID], i == 0 && (form == nil || form.ID != ""))
			if form != nil && form.ID == n.ID {
				a.networkForm(c, k, formErr, false)
			}
		}
	})

	section(c, k, "net-new", L.NetNewPhones(), nil, func() {
		pairName := L.NetNoNetwork()
		for _, n := range nets {
			if n.ID == mp.PairNetwork {
				pairName = n.shownName()
			}
		}
		srow(c, k, "net-pair", true, iconLink, L.NetPairJoins(), L.NetPairJoinsSub(), func() {
			ui.PopoverMenuButton(c.Key("net-pair-menu"), pairName, func(m *ui.Menu) {
				if m.Item(L.NetNoNetwork()).Radio(mp.PairNetwork == "").Chosen() {
					a.SetNetworkOptions(func(p *MeshPrefs) { p.PairNetwork = "" })
				}
				for _, n := range nets {
					if m.Item(n.shownName()).Radio(mp.PairNetwork == n.ID).Chosen() {
						a.SetNetworkOptions(func(p *MeshPrefs) { p.PairNetwork = n.ID })
					}
				}
			})
		})
	})
	section(c, k, "net-in", L.NetOtherWays(), nil, func() {
		srow(c, k, "net-accept", true, iconDex, L.SetAcceptDirect(), L.SetAcceptDirectSub(), func() {
			on := mp.Accept
			if ui.Switch(c.Key("accept-sw"), &on).Label(L.SetAcceptDirect()).Changed() {
				a.SetNetworkOptions(func(p *MeshPrefs) { p.Accept = on })
			}
		})
	})
	section(c, k, "net-adv", L.NetAdvanced(), nil, func() {
		srow(c, k, "net-p2p", true, iconPulse, L.SetTryDirect(), L.SetTryDirectSub(), func() {
			on := !mp.NoDirect
			if ui.Switch(c.Key("p2p-sw"), &on).Label(L.SetTryDirect()).Changed() {
				a.SetNetworkOptions(func(p *MeshPrefs) { p.NoDirect = !on })
			}
		})
		srow(c, k, "net-lat", false, iconGlobe, L.SetLatencyFirst(), L.SetLatencyFirstSub(), func() {
			on := mp.LatencyFirst
			if ui.Switch(c.Key("lat-sw"), &on).Label(L.SetLatencyFirst()).Changed() {
				a.SetNetworkOptions(func(p *MeshPrefs) { p.LatencyFirst = on })
			}
		})
		srow(c, k, "net-engine", false, iconCPU, L.NetEngine(), L.NetEngineSub(), func() {
			pill(c, "net-engine-p", L.NetBuiltIn(), k.sunk, k.text2)
		})
	})
}

// networkRow is one network: its name, network name and live state, a state pill, the Join switch
// and ⋯ (Edit, Remove); under a connected one, the phones it reaches.
func (a *App) networkRow(c *ui.Context, k tokens, n MeshNetwork, v netView, first bool) {
	key := "net-" + n.ID
	sub := L.NetName(n.NetworkName)
	errText := v.Err
	if errText == "" && n.Join {
		errText = v.Status.LastError
	}
	switch {
	case n.Join && v.Status.Running:
		parts := []string{sub}
		if v.Info.IPv4 != "" {
			parts = append(parts, L.NetThisComputer(v.Info.IPv4))
		}
		if v.Info.NAT != "" {
			parts = append(parts, natWords(v.Info.NAT))
		}
		if n.ListenPort > 0 {
			a.mu.Lock()
			accept := a.prefs.Mesh != nil && a.prefs.Mesh.Accept
			a.mu.Unlock()
			if accept {
				parts = append(parts, L.NetListening(n.ListenPort))
			}
		}
		sub = strings.Join(parts, " · ")
	case n.Join && errText != "":
		sub = L.NetCouldNot(errText)
	case n.Join:
		sub += " · " + L.NetSummaryConnecting()
	default:
		sub += " · " + L.NetNotJoined()
	}
	srow(c, k, key, first, iconGlobe, n.shownName(), sub, func() {
		switch {
		case n.Join && v.Status.Running:
			pill(c, key+"-p", L.StateConnected(), k.good.Alpha(0.14), k.goodText)
		case n.Join && errText != "":
			pill(c, key+"-p", L.NetSummaryFailed(), k.bad.Alpha(0.14), k.badText)
		}
		join := n.Join
		if ui.Switch(c.Key(key+"-join"), &join).Label(L.NetJoin()).Changed() {
			if join {
				go a.JoinNetwork(n.ID)
			} else {
				go a.LeaveNetwork(n.ID)
			}
		}
		iconBtn(c, key+"-more", iconMore, L.PaneMore(), false).PopoverMenuButton(func(m *ui.Menu) {
			if m.Item(L.ActionEdit()).Chosen() {
				a.update(func() {
					a.netForm = &netForm{ID: n.ID, Label: n.Label, NetworkName: n.NetworkName, Servers: strings.Join(n.Servers, "\n")}
					a.netFormErr = ""
				})
			}
			if m.Item(L.NetRemove()).Danger().Chosen() {
				a.confirmRemoveNetwork(n)
			}
		})
	})
	var devices []mesh.Peer // AndroFuse devices only, not relay servers (mesh.HostPrefix)
	for _, p := range v.Info.Peers {
		if _, _, _, ok := mesh.ParseHostname(p.Hostname); ok {
			devices = append(devices, p)
		}
	}
	if n.Join && v.Status.Running && len(devices) > 0 {
		subRow(c, k, key+"-peers", func() {
			for _, p := range devices {
				p := p
				kind, _, _, _ := mesh.ParseHostname(p.Hostname)
				name := a.meshPeerName(p.Hostname)
				ui.Row(c.Key(key + "-peer-" + p.Hostname)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					phoneAvatar(c, avatarSmall, phoneLogo(name, p.Hostname), nil, name)
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						ui.Text(c, name).FontSize(sizeBody).SingleLine()
						ui.Text(c, routeWords(p.IPv4, p.Direct, p.Latency)).FontSize(sizeSmall).TextColor(k.text3).FontFeatures("tnum").SingleLine()
					})
					if kind == mesh.KindPhone && p.IPv4 != "" {
						if ui.Button(c.Key(key+"-peer-go-"+p.Hostname), L.ConnectGo()).Clicked() {
							a.update(func() { a.settingsOpen, a.page, a.connect.Input = false, pageHome, p.IPv4 })
							a.ConnectByAddress()
						}
					}
				})
			}
		})
	}
}

// meshPeerName is what the interface calls an AndroFuse device in a network: a remembered phone's
// name when it is one, else the name it gives itself in its hostname.
func (a *App) meshPeerName(host string) string {
	for _, r := range a.rememberedList() {
		if r.Companion != nil && mesh.SameDevice(r.Companion.MeshHost, host) {
			return r.displayName()
		}
	}
	if _, id, name, ok := mesh.ParseHostname(host); ok {
		if name == "" {
			return id
		}
		return name
	}
	return host
}

// natWords is EasyTier's NAT type in plain words.
func natWords(nat string) string {
	switch strings.ToLower(nat) {
	case "openinternet", "nopat", "fullcone":
		return L.NatEasy()
	case "restricted", "portrestricted":
		return L.NatMedium()
	case "symmetric", "symudpfirewall", "symmetriceasyinc", "symmetriceasydec":
		return L.NatHard()
	}
	return L.NatUnknown()
}

// networkForm is the Add network / Edit form: the name shown here, the network name, servers one
// per line, and a write-only secret (empty keeps the stored one; a weak one is flagged).
func (a *App) networkForm(c *ui.Context, k tokens, errText string, adding bool) {
	if a.netForm == nil {
		return
	}
	subRow(c, k, "net-form", func() {
		field := func(key, label string, value *string, hint string, password bool) {
			ui.Column(c.Key(key)).FillWidth().Gap(6).Children(func() {
				ui.Text(c, label).FontSize(sizeSmall).TextColor(k.text2)
				in := ui.TextInput(c.Key(key+"-in"), value).FillWidth()
				if password {
					in.Password()
				}
				if hint != "" {
					ui.Text(c, hint).FontSize(sizeSmall).TextColor(k.text3)
				}
			})
		}
		ui.Row(c.Key("nf-top")).FillWidth().Gap(12).Children(func() {
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
				field("nf-label", L.NetFormLabel(), &a.netForm.Label, "", false)
			})
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
				field("nf-name", L.NetFormName(), &a.netForm.NetworkName, "", false)
			})
		})
		ui.Column(c.Key("nf-servers")).FillWidth().Gap(6).Children(func() {
			ui.Text(c, L.NetFormServers()).FontSize(sizeSmall).TextColor(k.text2)
			ui.TextArea(c.Key("nf-servers-in"), &a.netForm.Servers).FillWidth().Lines(2, 6).Font("monospace")
			ui.Text(c, L.NetFormServersHint()).FontSize(sizeSmall).TextColor(k.text3)
		})
		hint := L.NetFormSecretHint()
		if adding {
			hint = ""
		}
		field("nf-secret", L.NetFormSecret(), &a.netForm.Secret, hint, true)
		if weakSecret(a.netForm.Secret) {
			ui.Text(c, L.NetFormWeak()).FontSize(sizeSmall).TextColor(k.warnText)
		}
		if errText != "" {
			ui.Text(c, errText).FontSize(sizeSmall).TextColor(k.badText)
		}
		ui.Row(c.Key("nf-btns")).FillWidth().Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c.Key("nf-cancel"), L.ActionCancel()).Clicked() {
				a.update(func() { a.netForm, a.netFormErr = nil, "" })
			}
			ok := strings.TrimSpace(a.netForm.NetworkName) != "" && (!adding || a.netForm.Secret != "")
			if ui.PrimaryButton(c.Key("nf-save"), L.ActionSave()).Disabled(!ok).Clicked() {
				f := *a.netForm
				go func() {
					if err := a.SaveNetwork(f); err != nil {
						a.update(func() { a.netFormErr = err.Error() })
						return
					}
					a.update(func() { a.netForm, a.netFormErr = nil, "" })
				}()
			}
		})
	})
}

// confirmRemoveNetwork asks before forgetting a network's servers, name and secret.
func (a *App) confirmRemoveNetwork(n MeshNetwork) {
	go func() {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent:             a.mainWindow(),
			Title:              L.NetRemoveTitle(n.shownName()),
			Message:            L.NetRemoveTitle(n.shownName()),
			Detail:             L.NetRemoveBody(),
			Buttons:            []string{L.NetRemove(), L.ActionCancel()},
			CancelButton:       1,
			DestructiveButtons: []int{0},
		})
		if err == nil && res.Button == 0 {
			a.RemoveNetwork(n.ID)
		}
	}()
}

// SaveLogs writes AndroFuse's recent diagnostics into one text file the user chooses.
func (a *App) SaveLogs() {
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.mainWindow(), Title: L.SetSaveLogs(), DefaultPath: "androfuse-logs.txt"})
		if err != nil || path == "" {
			return
		}
		if err := writeLogs(path, a); err != nil {
			a.toast(err.Error())
			return
		}
		a.toast(L.SetLogsSaved())
	}()
}
