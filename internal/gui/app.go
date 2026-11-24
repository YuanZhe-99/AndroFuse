// Package gui implements the MyGo native UI shell for AndroFuse across
// Windows desktop (x64/ARM64) and Android Companion (M3).
//
// Views are pure functions of App state; background diagnostics reach the UI
// via Window.Update under App.mu (.agents/skills/mygo-maintenance/SKILL.md).
package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

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
		profile:  profile,
		stateDir: stateDir,
		prefs:    Prefs{Theme: "system"},
		page:     string(pageDevices),
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

// Start kicks off the initial asynchronous host diagnostics check.
func (a *App) Start() {
	a.RefreshDiagnostics()
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

// Quit exits the application cleanly.
func (a *App) Quit() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
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
	ui.Column(c.Key("page-devices")).Gap(16).FillWidth().Children(func() {
		ui.Text(c, L.DevicesSubtitle()).FontSize(sizeBody).TextColor(k.muted)
		card(c.Key("devices-empty")).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconDevices).Size(20, 20).TextColor(k.brand)
				ui.Text(c, L.DevicesEmptyTitle()).Bold().FontSize(sizeHeading)
			})
			ui.Text(c, L.DevicesEmptyBody()).FontSize(sizeBody).TextColor(k.muted)
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c.Key("go-diag"), L.NavDiagnostics()).Clicked() {
					a.mu.Lock()
					a.page = string(pageDiagnostics)
					a.mu.Unlock()
				}
				if ui.Button(c.Key("go-modes"), L.NavModes()).Clicked() {
					a.mu.Lock()
					a.page = string(pageModes)
					a.mu.Unlock()
				}
			})
		})
	})
}

func (a *App) modesPage(c *ui.Context, k tokens) {
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
