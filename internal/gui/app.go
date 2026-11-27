// Package gui implements the MyGo native UI shell for AndroFuse across Windows desktop (x64/ARM64)
// and the Android Companion.
//
// Views are pure functions of App state; background work reaches the UI via Window.Update under
// App.mu (.agents/skills/mygo-maintenance/SKILL.md). The shell follows Kyokai's (design/README.md).
package gui

import (
	"context"
	"encoding/json"
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
	"github.com/YuanZhe-99/AndroFuse/internal/vm"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// Prefs stores persistent GUI preferences in stateDir/gui.json.
type Prefs struct {
	Theme    string     `json:"theme,omitempty"`
	Language string     `json:"language,omitempty"`
	Mesh     *MeshPrefs `json:"mesh,omitempty"`
	// StreamsInWindow and AppsInWindow open DeX, phone screens and cameras, and app windows, in
	// their own windows instead of main-window panes (Settings › General).
	StreamsInWindow bool `json:"streams_in_window,omitempty"`
	AppsInWindow    bool `json:"apps_in_window,omitempty"`
	// SidebarRail folds the sidebar to a rail of icons.
	SidebarRail bool `json:"sidebar_rail,omitempty"`
	// OthersInNetworks lists, on Home, the phones of the joined networks never connected to.
	OthersInNetworks bool `json:"others_in_networks,omitempty"`
	HomeOnlineOnly   bool `json:"home_online_only,omitempty"`
	// TrayOnLogin starts in the tray when Windows opens AndroFuse at sign-in; CloseQuits quits
	// instead of staying in the tray when the main window closes.
	TrayOnLogin  *bool `json:"tray_on_login,omitempty"`
	CloseQuits   bool  `json:"close_quits,omitempty"`
	TrayToldOnce bool  `json:"tray_told_once,omitempty"`
	// Picture: MaxSize 0 follows the phone, else the longest side; MaxFPS 0 is 60; Codec "" is auto.
	MaxSize   int    `json:"max_size,omitempty"`
	MaxFPS    int    `json:"max_fps,omitempty"`
	Codec     string `json:"codec,omitempty"`
	MuteSound bool   `json:"mute_sound,omitempty"`
	// NoUSBHandoff turns off switching to Wi-Fi when USB is unplugged; NoCompanionOffer stops asking
	// to install the Companion on a first USB connection.
	NoUSBHandoff     bool `json:"no_usb_handoff,omitempty"`
	NoCompanionOffer bool `json:"no_companion_offer,omitempty"`
	// ConnectionMode picks how phones connect wirelessly: "both_adb" (default), "both_companion",
	// or "companion_only". NoAutoSwitch turns off automatic fast fallback to Companion and
	// automatic upgrade back to ADB when Wi-Fi returns.
	ConnectionMode string `json:"connection_mode,omitempty"`
	NoAutoSwitch   bool   `json:"no_auto_switch,omitempty"`
	FrontCamera    bool   `json:"front_camera,omitempty"`
	// Updates: NoDailyCheck stops the daily check; AutoInstall is "", "idle" or "night".
	NoDailyCheck bool   `json:"no_daily_check,omitempty"`
	AutoInstall  string `json:"auto_install,omitempty"`
	// AssistantAllow lists the local assistant's tools that run without asking each time.
	AssistantAllow []string `json:"assistant_allow,omitempty"`
}

// firstConnect is the card for a phone connected over USB for the first time.
type firstConnect struct {
	Hash, Serial, Name string
	Install, Join      bool
}

// approval is a local assistant's request waiting for the user (a card above the panes).
type approval struct {
	ID, Title, Detail string
	answer            chan bool
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
	toastMsg   string
	cardWork   map[string]cardWork // per phone card: what runs, or why it failed (setCardWork)
	// compInstalled is what the last check found of each phone's Companion (syncCompanion).
	compInstalled map[string]compInstall

	rt              guiRuntime
	scanningDevices bool
	devices         []DiscoveredDevice
	mdnsServices    []adb.MDNSService
	deviceNotice    string
	appIcons        map[string]*ui.Bitmap
	cachedApps      map[string][]adb.AppEntry
	dexRoutes       map[string]dex.RoutePreference
	vmStatus        vm.Status

	compAddr        string
	compToken       string
	compConnected   bool
	compHash        string // HardwareHash of the phone whose Companion is connected
	compStatus      companion.DeviceStatus
	peerStatus      map[string]companion.DeviceStatus
	compNotice      string
	pairingCode     string // the code to check on the phone while a pairing waits
	pairingModel    string
	firstConnect    *firstConnect
	mcpStatus       mcp.Status
	mcpAllowLaunch  bool
	mcpNotice       string
	approvals       []approval
	cameraBridgeURL string
	vcamCap         avbridge.VirtualCameraCapability
	micUplinkActive bool
	micUplinkNotice string
	customAPK       string // a Companion installer the user chose (ChooseCompanionAPK)
	errs            errLog // recent failures in full, for Save logs

	sessions    map[string]*SessionWindow
	remembered  []RememberedDevice // devices.json
	split       *paneSplit         // two docked sessions shown together (pageSplit)
	connect     connectFlow        // Home's "Connect a phone"
	pendingOpen *pendingOpen
	netViews    map[string]netView // joined networks' state, by network ID
	netForm     *netForm           // Settings › Network's Add / Edit form while open
	netFormErr  string
	renaming    string // HardwareHash whose Rename editor is open
	renameText  string

	settingsOpen  bool
	settingsPage  string
	settingsQuery string
	searchOpen    bool
	searchQuery   string
	picker        pickerState
	update_       updateState
	techOpen      bool // About and updates › Technical details
	openAtLogin   bool // Windows starts AndroFuse at sign-in (read once at Start; the call waits on the main thread)
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
		page:       pageHome,
		appIcons:   make(map[string]*ui.Bitmap),
		cachedApps: make(map[string][]adb.AppEntry),
		peerStatus: make(map[string]companion.DeviceStatus),
		dexRoutes:  make(map[string]dex.RoutePreference),
		vcamCap:    avbridge.ProbeVirtualCamera(),
		sessions:   make(map[string]*SessionWindow),
		netViews:   make(map[string]netView),
	}
	a.loadPrefs()
	a.loadDevices()
	a.migrateMeshLocked()
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

// StartInTray reports whether AndroFuse was opened at Windows sign-in and should show only its
// tray icon (Settings › General › Start and close).
func (a *App) StartInTray() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return mygo.App.WasOpenedAtLogin() && (a.prefs.TrayOnLogin == nil || *a.prefs.TrayOnLogin)
}

// SetTray stores the active desktop tray handle.
func (a *App) SetTray(t *Tray) {
	a.mu.Lock()
	a.tray = t
	a.mu.Unlock()
}

// Start kicks off the host check, the device scan, the joined networks and the update check.
func (a *App) Start() {
	go func() {
		on := mygo.App.OpenAtLogin()
		a.update(func() { a.openAtLogin = on })
	}()
	host, _ := os.Hostname()
	companion.SetClient(a.deviceID(), host)
	a.RefreshDiagnostics()
	a.RefreshDevices()
	a.RefreshVMStatus()
	go a.resumeNetworks()
	go a.pollNetworks(context.Background())
	go a.pollCompanionHealth(context.Background())
	go a.updateLoop(context.Background())
	go a.watchDevices(context.Background())
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

// RefreshChrome updates window metadata and the tray when the active locale changes.
func (a *App) RefreshChrome() {
	a.mu.Lock()
	w := a.window
	a.mu.Unlock()
	if w != nil {
		w.SetTitle(L.AppName())
	}
	a.refreshTray()
}

// Quit tears down active streaming sessions and tunnels and exits cleanly (INV-03).
func (a *App) Quit() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	a.CloseAllSessions()
	a.stopTunnels()
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
	a.mu.Unlock()

	go func() {
		res := a.hostDiagnostics()
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
		opts.TitleBarHeight = int(paneMargin + paneHeaderH)
		opts.HideWindowButtons = true
		opts.Vibrancy = mygo.VibrancyMica
	}
	w := mygo.NewWindow(opts)
	w.OnClose(func(ev *mygo.CloseEvent) {
		a.mu.Lock()
		quitting := a.quitting
		hasTray := a.tray != nil
		closeQuits := a.prefs.CloseQuits
		told := a.prefs.TrayToldOnce
		a.mu.Unlock()
		if !quitting && closeQuits {
			go a.Quit()
			ev.PreventDefault()
			return
		}
		if !quitting && hasTray && runtime.GOOS != "android" {
			ev.PreventDefault()
			w.Hide()
			if !told {
				a.mu.Lock()
				a.prefs.TrayToldOnce = true
				a.savePrefs()
				a.mu.Unlock()
				_ = mygo.NewNotification(mygo.NotificationOptions{Title: L.AppName(), Body: L.TrayStillRunning()}).Show()
			}
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
	case "zh-Hans":
		a.langIndex = 1
	case "en":
		a.langIndex = 2
	default:
		a.langIndex = 0
	}
}

// Connection strategy constants stored in Prefs.ConnectionMode.
const (
	ConnBothADB       = "both_adb"
	ConnBothCompanion = "both_companion"
	ConnCompanionOnly = "companion_only"
)

func (a *App) connectionMode() string {
	a.mu.Lock()
	m := a.prefs.ConnectionMode
	a.mu.Unlock()
	switch m {
	case ConnBothCompanion, ConnCompanionOnly:
		return m
	default:
		return ConnBothADB
	}
}

func (a *App) autoSwitchOn() bool {
	a.mu.Lock()
	m := a.prefs.ConnectionMode
	noAuto := a.prefs.NoAutoSwitch
	a.mu.Unlock()
	return m != ConnCompanionOnly && !noAuto
}

func (a *App) prefersCompanion() bool {
	m := a.connectionMode()
	return m == ConnCompanionOnly || m == ConnBothCompanion
}

func (a *App) allowsWirelessADB() bool {
	return a.connectionMode() != ConnCompanionOnly
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
