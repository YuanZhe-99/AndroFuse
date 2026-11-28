package gui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/vm"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// Tray owns the system tray icon and menu on desktop platforms.
type Tray struct {
	app  *App
	tray *mygo.Tray
	dark bool
}

// NewTray creates the desktop status tray icon on the main thread.
// Returns nil on platforms without tray support (e.g., Android).
func (a *App) NewTray() *Tray {
	t, err := mygo.NewTray(mygo.TrayOptions{
		Icon:    trayPNG(false),
		ToolTip: "AndroFuse",
	})
	if err != nil {
		return nil
	}
	tr := &Tray{app: a, tray: t, dark: t.Dark()}
	_ = t.SetIcon(trayPNG(tr.dark), false)
	t.OnAppearanceChange(func() {
		tr.dark = t.Dark()
		_ = t.SetIcon(trayPNG(tr.dark), false)
	})
	tr.Refresh()
	return tr
}

// Refresh rebuilds the tray menu (design/README.md, "Tray"): the state in one line, the local
// assistant's stop while it runs, one submenu per phone with what it can do without the main
// window, Connect a phone…, the open pictures, the networks as switches, Android on this PC, and
// Show, Settings and Quit.
func (tr *Tray) Refresh() {
	if tr == nil || tr.tray == nil {
		return
	}
	tr.tray.SetMenu(mygo.NewMenu(tr.app.trayItems()))
}

// refreshTray rebuilds the tray menu after a change it shows.
func (a *App) refreshTray() {
	a.mu.Lock()
	t := a.tray
	a.mu.Unlock()
	if t != nil {
		mygo.RunOnMain(t.Refresh)
	}
}

// trayItems builds the tray menu's items.
func (a *App) trayItems() []*mygo.MenuItem {
	click := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { go fn() }
	}
	sep := func() *mygo.MenuItem { return &mygo.MenuItem{Type: mygo.MenuItemSeparator} }
	a.mu.Lock()
	devs := append([]DiscoveredDevice(nil), a.devices...)
	mcpOn := a.mcpStatus.Enabled
	vmSt := a.vmStatus
	found := a.update_.Found
	a.mu.Unlock()

	online := 0
	for _, d := range devs {
		if d.Target.State == "device" {
			online++
		}
	}
	_, netWords := a.networkSummary(tokens{})
	items := []*mygo.MenuItem{{Label: L.AppName(), Subtitle: L.TrayState(L.NetSidebar(netWords), online), Disabled: true}}
	if mcpOn {
		items = append(items, &mygo.MenuItem{Label: L.TrayStopAssistant(), Click: click(a.EmergencyStopMCP)})
	}
	items = append(items, sep())

	for _, d := range devs {
		if d.Target.State != "device" {
			continue
		}
		route, _ := a.routeLine(d)
		items = append(items, &mygo.MenuItem{Label: a.phoneName(d), Badge: route, Submenu: a.phoneTrayItems(d)})
	}
	for _, r := range a.offlineRemembered(devs) {
		sub := []*mygo.MenuItem{{Label: L.ConnectGo(), Click: click(func() { a.connectRemembered(r, openAsScreen) })}}
		if r.Companion != nil {
			sub = append(sub, &mygo.MenuItem{Label: L.ShareScreen(), Click: click(func() { a.LaunchCompanionMirror(r.HardwareHash, OpenDefault) })})
		}
		items = append(items, &mygo.MenuItem{Label: r.displayName(), Badge: L.StateOffline(), Submenu: sub})
	}
	items = append(items, &mygo.MenuItem{Label: L.TrayConnectPhone(), Click: func(*mygo.MenuItem, *mygo.Window) { a.openPicker(openAsScreen, "") }}, sep())

	sessions := a.sessionsSorted()
	var open []*mygo.MenuItem
	for _, s := range sessions {
		open = append(open, &mygo.MenuItem{Label: a.paneLabel(s), Click: func(*mygo.MenuItem, *mygo.Window) { s.FocusWindow() }})
	}
	if len(open) > 0 {
		open = append(open, sep(), &mygo.MenuItem{Label: L.TrayCloseAll(), Click: click(a.CloseAllSessions)})
		items = append(items, &mygo.MenuItem{Label: L.TrayOpen(), Badge: fmt.Sprint(len(sessions)), Submenu: open})
	}

	var nets []*mygo.MenuItem
	for _, n := range a.networks() {
		a.mu.Lock()
		v := a.netViews[n.ID]
		a.mu.Unlock()
		state := L.NetNotJoined()
		switch {
		case n.Join && v.Status.Running:
			state = L.StateConnected()
		case n.Join && (v.Err != "" || v.Status.LastError != ""):
			state = L.NetSummaryFailed()
		case n.Join:
			state = L.NetSummaryConnecting()
		}
		nets = append(nets, &mygo.MenuItem{Label: n.shownName(), Type: mygo.MenuItemCheckbox, Checked: n.Join, Badge: state,
			Click: func(it *mygo.MenuItem, _ *mygo.Window) {
				if it.Checked {
					go a.JoinNetwork(n.ID)
				} else {
					go a.LeaveNetwork(n.ID)
				}
			}})
	}
	if len(nets) > 0 {
		nets = append(nets, sep())
	}
	nets = append(nets, &mygo.MenuItem{Label: L.TrayNetworkSettings(), Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings(setNetwork) }})
	items = append(items, &mygo.MenuItem{Label: L.SettingsNetwork(), Submenu: nets})

	var vmItems []*mygo.MenuItem
	switch {
	case vmSt.State == vm.StateRunning:
		vmItems = append(vmItems, &mygo.MenuItem{Label: L.VMActionStop(), Click: click(a.StopVM)})
	case len(vmSt.AvailableAVDs) > 0:
		vmItems = append(vmItems, &mygo.MenuItem{Label: L.VMActionStart(), Click: click(func() { a.StartVM(vmSt.AVDName) })})
	default:
		vmItems = append(vmItems, &mygo.MenuItem{Label: L.VMActionDownload(), Click: click(a.ProvisionVM)})
	}
	items = append(items, &mygo.MenuItem{Label: L.VMTitle(), Badge: vmStateWords(vmSt), Submenu: vmItems}, sep())

	if found != nil {
		items = append(items, &mygo.MenuItem{Label: L.UpdateFound(found.Version), Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings(setAbout) }})
	}
	items = append(items,
		&mygo.MenuItem{Label: L.TrayShow(), Click: func(*mygo.MenuItem, *mygo.Window) { a.Focus() }},
		&mygo.MenuItem{Label: L.NavSettings(), Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings("") }},
		&mygo.MenuItem{Label: L.TrayQuit(), Click: func(*mygo.MenuItem, *mygo.Window) { a.Quit() }},
	)
	return items
}

// phoneTrayItems is one phone's tray submenu: its kinds, a few of its apps, the clipboard and
// files, a screenshot, the phone's screen, and disconnecting.
func (a *App) phoneTrayItems(d DiscoveredDevice) []*mygo.MenuItem {
	click := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { fn() }
	}
	var out []*mygo.MenuItem
	if genuineDeX(d) {
		out = append(out, &mygo.MenuItem{Label: L.KindDeX(), Click: click(func() { a.LaunchDeX(d, false, OpenDefault) })})
	}
	out = append(out, &mygo.MenuItem{Label: L.KindScreen(), Click: click(func() { a.LaunchMirror(d, OpenDefault) })})
	if len(d.Apps) > 0 {
		var apps []*mygo.MenuItem
		for _, ap := range someApps(d.Apps, 6) {
			apps = append(apps, &mygo.MenuItem{Label: ap.Label, Click: click(func() { a.LaunchAppWindow(d, ap, OpenDefault) })})
		}
		apps = append(apps, &mygo.MenuItem{Type: mygo.MenuItemSeparator}, &mygo.MenuItem{Label: L.TrayAllApps(), Click: click(func() { a.openPicker(openAsApp, d.HardwareHash) })})
		out = append(out, &mygo.MenuItem{Label: L.KindApp(), Submenu: apps})
	}
	if !isEmulator(d) {
		out = append(out, &mygo.MenuItem{Label: L.KindCamera(), Click: click(func() { a.LaunchPhoneCamera(d, a.cameraFacing(), OpenDefault) })})
	}
	out = append(out, &mygo.MenuItem{Type: mygo.MenuItemSeparator})
	if a.compHashConnected(d.HardwareHash) {
		out = append(out,
			&mygo.MenuItem{Label: L.ActionSendClipboard(), Click: click(func() {
				if b, err := mygo.Clipboard.ReadFormat(transfer.Text); err == nil {
					a.SendClipboardToPhone(string(b))
				}
			})},
			&mygo.MenuItem{Label: L.ActionSendFileMenu(), Click: click(a.SendFileToPhone)})
	}
	out = append(out,
		&mygo.MenuItem{Label: L.ActionScreenshot(), Badge: L.TraySavedToPictures(), Click: click(func() { go a.ScreenshotPhone(d) })},
		&mygo.MenuItem{Label: L.TrayScreenOff(), Click: click(func() { go a.PhoneScreenOff(d) })},
	)
	for _, tr := range d.Transports {
		if tr.State == "device" && (tr.Transport == adb.TransportTLSWiFi || tr.Transport == adb.TransportTCP) {
			out = append(out, &mygo.MenuItem{Type: mygo.MenuItemSeparator}, &mygo.MenuItem{Label: L.ActionDisconnectWireless(), Click: click(func() { a.DisconnectWireless(tr.Serial) })})
			break
		}
	}
	return out
}

// someApps picks up to n of the phone's own apps for the tray (in the launcher's order; system
// apps only when it has nothing else).
func someApps(apps []adb.AppEntry, n int) []adb.AppEntry {
	var out []adb.AppEntry
	for _, ap := range apps {
		if !ap.System {
			out = append(out, ap)
		}
		if len(out) == n {
			return out
		}
	}
	if len(out) == 0 && len(apps) > 0 {
		return apps[:min(n, len(apps))]
	}
	return out
}

func trayPNG(darkBar bool) []byte {
	srcBytes := trayDarkPNG
	if darkBar {
		srcBytes = trayLightPNG
	}
	src, err := png.Decode(bytes.NewReader(srcBytes))
	if err != nil {
		return srcBytes
	}
	b := src.Bounds()
	dst := image.NewNRGBA(b)
	tint := color.NRGBA{0x1D, 0x2C, 0x33, 0xff}
	if darkBar {
		tint = color.NRGBA{0xB8, 0xF1, 0xD2, 0xff}
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := src.At(x, y).RGBA()
			dst.SetNRGBA(x, y, color.NRGBA{tint.R, tint.G, tint.B, uint8(a >> 8)})
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, dst)
	return out.Bytes()
}
