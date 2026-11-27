package gui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/scrcpy"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// Pages that show sessions: "pane:<id>" is one docked session, pageSplit two side by side.
const (
	panePrefix = "pane:"
	pageSplit  = "split"
)

// windowPrefix marks a sidebar entry that raises a session in its own window.
const windowPrefix = "win:"

// paneSplit is the two docked sessions shown together and how the room is shared between them.
type paneSplit struct {
	A, B     string  // session IDs: left (or top) and right (or bottom)
	Vertical bool    // B below A instead of beside it
	Percent  float32 // room A takes, 0–100
}

// mainWindow returns the main window, or nil while it is closed to the tray.
func (a *App) mainWindow() *mygo.Window {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.window
}

// opensInWindow reports whether sessions of mode open in their own window by default
// (Settings › General › Where things open).
func (a *App) opensInWindow(mode SessionMode) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if mode == SessionModeApp {
		return a.prefs.AppsInWindow
	}
	return a.prefs.StreamsInWindow
}

// dockSession shows the docked session id in the main window: alone, or beside the pane showing
// now when beside is set and one is.
// Side effects: switches the main window's page and raises it.
func (a *App) dockSession(id string, beside bool) {
	a.update(func() { a.dockLocked(id, beside) })
	a.Focus()
}

// dockLocked switches the page to the docked session id; the caller holds a.mu.
func (a *App) dockLocked(id string, beside bool) {
	cur, isPane := strings.CutPrefix(a.page, panePrefix)
	switch {
	case beside && isPane && cur != id:
		a.split = &paneSplit{A: cur, B: id, Percent: 50}
		a.page = pageSplit
	case a.split != nil && (a.split.A == id || a.split.B == id):
		a.page = pageSplit
	default:
		a.page = panePrefix + id
	}
}

// undockSession takes id out of the main window's panes, as it pops out or ends: a split falls
// back to the other pane, a page showing only id falls back to Home.
func (a *App) undockSession(id string) {
	a.update(func() { a.undockLocked(id) })
}

func (a *App) undockLocked(id string) {
	if s := a.split; s != nil && (s.A == id || s.B == id) {
		other := s.A
		if other == id {
			other = s.B
		}
		a.split = nil
		if a.page == pageSplit {
			a.page = panePrefix + other
		}
	}
	if a.page == panePrefix+id {
		a.page = pageHome
	}
}

// splitWith shows the docked sessions first and second side by side, or second below first.
func (a *App) splitWith(first, second string, vertical bool) {
	a.update(func() {
		a.split = &paneSplit{A: first, B: second, Vertical: vertical, Percent: 50}
		a.page = pageSplit
	})
}

// unsplit leaves only keep showing from a split.
func (a *App) unsplit(keep string) {
	a.update(func() {
		a.split = nil
		a.page = panePrefix + keep
	})
}

// sessionsSorted returns the open sessions in a stable order (their IDs carry their start time).
func (a *App) sessionsSorted() []*SessionWindow {
	a.mu.Lock()
	list := make([]*SessionWindow, 0, len(a.sessions))
	for _, s := range a.sessions {
		list = append(list, s)
	}
	a.mu.Unlock()
	slices.SortFunc(list, func(x, y *SessionWindow) int { return strings.Compare(x.spec.ID, y.spec.ID) })
	return list
}

// session returns the open session id.
func (a *App) session(id string) *SessionWindow {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

// kindWord names what a session shows: Samsung DeX, the large-screen desktop (not DeX), the phone
// screen, an app window, the camera, or the Companion's screen sharing (`INV-06`).
func (sw *SessionWindow) kindWord() string {
	switch sw.spec.Mode {
	case SessionModeDeX, SessionModeMiracastDeX:
		return L.KindDeX()
	case SessionModeDeXFallback:
		return L.KindDeXFallbackShort()
	case SessionModeApp:
		return L.KindApp()
	case SessionModeCamera:
		return L.KindCamera()
	case SessionModeShare:
		return L.KindShare()
	default:
		return L.KindScreen()
	}
}

// paneArea fills the main window with the session page: one docked session, or a split of two.
// Returns: false when the page names no open session, so the caller shows Home instead.
func (a *App) paneArea(c *ui.Context) bool {
	a.mu.Lock()
	page, split := a.page, a.split
	a.mu.Unlock()
	if page == pageSplit && split != nil {
		sa, sb := a.session(split.A), a.session(split.B)
		if sa == nil || sb == nil {
			return false
		}
		build := ui.SplitPercent
		if split.Vertical {
			build = ui.SplitVerticalPercent
		}
		// The window's buttons go on the top-right pane: B beside A, A above B.
		build(c.Key("pane-split"), &split.Percent,
			func() { a.pane(c, sa, sb, split.Vertical) },
			func() { a.pane(c, sb, sa, !split.Vertical) }).SplitReset(50).Fill().Gap(paneMargin)
		return true
	}
	id, ok := strings.CutPrefix(page, panePrefix)
	if !ok {
		return false
	}
	sw := a.session(id)
	if sw == nil {
		return false
	}
	a.pane(c, sw, nil, true)
	return true
}

// pane draws one docked session under its single header; partner is the other half of a split,
// topRight whether the window's buttons go at the end of its header.
func (a *App) pane(c *ui.Context, sw *SessionWindow, partner *SessionWindow, topRight bool) {
	t := c.Theme()
	ui.Column(c.Key("pane-"+sw.spec.ID)).Fill().MinWidth(0).MinHeight(0).Radius(paneRadius).Clip().
		Background(t.Background).Border(1, t.Border).Bind(&sw.paneHandle).Children(func() {
		a.sessionHeader(c, sw, partner, true, topRight)
		sw.statusStrip(c)
		sw.shareStrip(c)
		ui.Column(c.Key("pane-body")).Grow(1).FillWidth().MinHeight(0).Background(ui.Hex(stageBgHex)).Children(func() {
			sw.content(c, true)
		})
	})
}

// sessionHeader is a session's one 44-DIP header, in a pane or its own window: the phone's avatar
// or the app, its name and what it shows over the connection; in the middle the phone's keys and
// tools; the measured frame rate (details in a popover, `INV-05`); then pop out or dock back,
// split right and down, close; and the window's buttons when they belong here. Narrow headers fold
// the tools and pane buttons into ⋯.
func (a *App) sessionHeader(c *ui.Context, sw *SessionWindow, partner *SessionWindow, docked, winButtons bool) {
	t := c.Theme()
	k := tok(c)
	width := float32(0)
	if docked {
		width = sw.paneHandle.Bounds(c).W
	} else {
		width, _ = c.Size()
	}
	narrow := width > 0 && width < paneFoldW
	roomForTools := width == 0 || width >= 760

	var others []*SessionWindow
	for _, o := range a.sessionsSorted() {
		if o != sw && o.Docked() {
			others = append(others, o)
		}
	}
	box := sw.paneHandle.Bounds(c)
	canRight := docked && partner == nil && (box.W == 0 || box.W >= 2*splitMinW)
	canDown := docked && partner == nil && (box.H == 0 || box.H >= 2*splitMinH)

	title, sub := a.sessionPhone(sw), sw.kindWord()+" · "+sw.transportWord()
	if sw.spec.Mode == SessionModeCamera {
		sub = cameraWord(sw.spec.CameraFacing) + " · " + sw.transportWord()
	}
	if sw.spec.Mode == SessionModeApp && sw.spec.Title != "" {
		title, sub = sw.spec.Title, a.sessionPhone(sw)+" · "+L.KindApp()
	}

	ui.Row(c.Key("sess-head")).Height(paneHeaderH).Shrink(0).FillWidth().Padding(0, 4, 0, 12).Gap(6).
		AlignItems(ui.Center).Background(t.Background).Children(func() {
		a.sessionIcon(c, sw, avatarSmall)
		ui.Column(c.Key("sess-title")).Shrink(1).MinWidth(96).MaxWidth(260).Gap(1).DragWindow().Children(func() {
			ui.Text(c, title).Bold().FontSize(sizeBody).SingleLine()
			ui.Text(c, sub).FontSize(sizeSmall).TextColor(k.text3).SingleLine()
		})
		ui.Box(c.Key("sess-drag")).Grow(1).MinWidth(8).FillHeight().DragWindow()
		if !narrow {
			sw.toolbar(c, k, roomForTools)
			ui.Box(c).Width(1).Height(20).Background(t.Border).Margin(0, 4)
			sw.telemetryChip(c, k)
			ui.Box(c.Key("sess-drag2")).Grow(1).MinWidth(8).FillHeight().DragWindow()
		}
		if narrow {
			iconBtn(c, "sess-more", iconMore, L.PaneMore(), false).PopoverMenuButton(func(m *ui.Menu) {
				sw.toolMenu(m, c, true)
				m.Separator()
				if docked {
					if m.Item(L.PanePopOut()).Chosen() {
						sw.PopOut()
					}
					for _, o := range others {
						if m.Item(L.PaneSplitWith(a.paneLabel(o))).Disabled(!canRight).Chosen() {
							a.splitWith(sw.spec.ID, o.spec.ID, false)
						}
					}
					if partner != nil && m.Item(L.PaneUnsplit()).Chosen() {
						a.unsplit(sw.spec.ID)
					}
				} else if m.Item(L.PaneDock()).Chosen() {
					sw.Dock()
				}
			})
		} else if docked {
			if iconBtn(c, "sess-pop", iconPopOut, L.PanePopOut(), false).Clicked() {
				sw.PopOut()
			}
			if partner != nil {
				if iconBtn(c, "sess-unsplit", iconMaximize, L.PaneUnsplit(), false).Clicked() {
					a.unsplit(sw.spec.ID)
				}
			} else {
				splitMenu := func(vertical bool) func(m *ui.Menu) {
					return func(m *ui.Menu) {
						for _, o := range others {
							if m.Item(a.paneLabel(o)).Chosen() {
								a.splitWith(sw.spec.ID, o.spec.ID, vertical)
							}
						}
						m.Separator()
						if m.Item(L.PickerOpenDevice()).Chosen() {
							a.openPickerBeside()
						}
					}
				}
				r := iconBtn(c, "sess-split-r", iconSplitRight, L.PaneSplitRight(), false)
				if !canRight {
					r.Disabled(true).Tooltip(L.PaneNoRoom())
				}
				r.PopoverMenuButton(splitMenu(false))
				d := iconBtn(c, "sess-split-d", iconSplitDown, L.PaneSplitDown(), false)
				if !canDown {
					d.Disabled(true).Tooltip(L.PaneNoRoom())
				}
				d.PopoverMenuButton(splitMenu(true))
			}
		} else if iconBtn(c, "sess-dock", iconDock, L.PaneDock(), false).Clicked() {
			sw.Dock()
		}
		if iconBtn(c, "sess-close", winClose, L.PaneClose(), false).Clicked() {
			sw.Close()
		}
		if winButtons {
			ui.Box(c).Width(1).Height(20).Background(t.Border).Margin(0, 4)
			if docked {
				a.windowButtons(c)
			} else {
				sw.mu.Lock()
				win := sw.win
				sw.mu.Unlock()
				drawWindowButtons(c, win, sw.Close)
			}
		}
	})
	ui.Box(c.Key("sess-head-line")).Height(1).FillWidth().Background(t.Border)
}

// paneLabel names a session in menus: its phone and what it shows.
func (a *App) paneLabel(s *SessionWindow) string {
	if s.spec.Mode == SessionModeApp && s.spec.Title != "" {
		return a.sessionPhone(s) + " · " + s.spec.Title
	}
	return a.sessionPhone(s) + " · " + s.kindWord()
}

// transportWord is the session's connection in words.
func (sw *SessionWindow) transportWord() string {
	tr := sw.spec.Transport
	switch {
	case strings.Contains(tr, "USB"):
		return "USB"
	case strings.Contains(tr, "Emulator"):
		return L.RouteThisComputer()
	case sw.spec.Mode == SessionModeShare:
		return L.RouteCrossNetwork()
	case strings.Contains(tr, "127.0.0.1"), strings.Contains(tr, "Companion"):
		return L.RouteCompanion()
	default:
		return "Wi-Fi"
	}
}

// toolbar is the phone's keys and the session tools in its header: Back, Home and Recents, then
// screenshot, send clipboard and full screen, and ⋯ for the rest; without room only ⋯.
func (sw *SessionWindow) toolbar(c *ui.Context, k tokens, room bool) {
	sw.mu.Lock()
	s := sw.scrcpySess
	sw.mu.Unlock()
	t := c.Theme()
	focus := func() { sw.surface.Focus(c) }
	ui.Row(c.Key("sess-tools")).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		if room && s != nil && sw.spec.Mode == SessionModeCamera {
			sw.mu.Lock()
			muted := sw.audioMuted
			sw.mu.Unlock()
			if iconBtn(c, "tool-flip", iconCameraSwitch, L.ActionSwitchCamera(), false).Clicked() {
				sw.switchCamera()
			}
			sound, soundIcon := L.ActionPlaySound(), iconVolumeOff
			if !muted {
				sound, soundIcon = L.ActionMute(), iconVolumeOn
			}
			if iconBtn(c, "tool-sound", soundIcon, sound, false).Clicked() {
				sw.toggleSound()
			}
			ui.Box(c).Width(1).Height(20).Background(t.Border).Margin(0, 4)
			if iconBtn(c, "tool-shot", iconScreenshot, L.ActionScreenshot(), false).Clicked() {
				sw.Screenshot()
			}
			if iconBtn(c, "tool-full", iconFullscreen, L.ActionFullscreen(), false).Clicked() {
				sw.toggleFullScreen()
			}
		} else if room && s != nil {
			if iconBtn(c, "tool-back", iconBack, L.ActionBack(), false).Clicked() {
				go sw.pressKey("back")
				focus()
			}
			if iconBtn(c, "tool-home", iconRecord, L.ActionHome(), false).Clicked() {
				go sw.pressKey("home")
				focus()
			}
			if iconBtn(c, "tool-recents", iconMaximize, L.ActionRecents(), false).Clicked() {
				go sw.pressKey("recents")
				focus()
			}
			ui.Box(c).Width(1).Height(20).Background(t.Border).Margin(0, 4)
			if iconBtn(c, "tool-shot", iconScreenshot, L.ActionScreenshot(), false).Clicked() {
				sw.Screenshot()
				focus()
			}
			if sw.spec.Mode != SessionModeShare && iconBtn(c, "tool-clip", iconClipboard, L.ActionSendClipboard(), false).Clicked() {
				sw.SyncClipboardToDevice(c.ReadClipboard())
				focus()
			}
			if iconBtn(c, "tool-full", iconFullscreen, L.ActionFullscreen(), false).Clicked() {
				sw.toggleFullScreen()
				focus()
			}
		}
		iconBtn(c, "tool-more", iconMore, L.PaneMore(), false).PopoverMenuButton(func(m *ui.Menu) {
			sw.toolMenu(m, c, !room)
		})
	})
}

// toolMenu adds the session tools to m; all adds the ones the header shows when it has room too.
func (sw *SessionWindow) toolMenu(m *ui.Menu, c *ui.Context, all bool) {
	sw.mu.Lock()
	s := sw.scrcpySess
	recording, muted, screenOff, touch := sw.recording, sw.audioMuted, sw.screenOff, sw.touchMode
	sw.mu.Unlock()
	if s == nil {
		return
	}
	share := sw.spec.Mode == SessionModeShare
	if sw.spec.Mode == SessionModeCamera {
		sw.cameraMenu(m, all, muted, recording)
		return
	}
	if all {
		if m.Item(L.ActionBack()).Chosen() {
			go sw.pressKey("back")
		}
		if m.Item(L.ActionHome()).Chosen() {
			go sw.pressKey("home")
		}
		if m.Item(L.ActionRecents()).Chosen() {
			go sw.pressKey("recents")
		}
		if m.Item(L.ActionScreenshot()).Chosen() {
			sw.Screenshot()
		}
		if !share && m.Item(L.ActionSendClipboard()).Chosen() {
			sw.SyncClipboardToDevice(c.ReadClipboard())
		}
		if m.Item(L.ActionFullscreen()).Chosen() {
			sw.toggleFullScreen()
		}
		m.Separator()
	}
	if share {
		return
	}
	recLabel := L.ActionRecordStart()
	if recording {
		recLabel = L.ActionRecordStop()
	}
	if m.Item(recLabel).Chosen() {
		sw.ToggleRecording()
	}
	if m.Item(L.ActionPlaySound()).Checked(!muted).Chosen() {
		sw.toggleSound()
	}
	if sw.spec.ScreenOffCap {
		if m.Item(L.ActionPhoneScreenOff()).Checked(screenOff).Chosen() {
			next := !screenOff
			sw.mu.Lock()
			sw.screenOff = next
			sw.mu.Unlock()
			go func() { _ = s.SetDisplayPower(!next) }()
		}
	}
	if !sw.spec.IsDeX && m.Item(L.ActionRotate()).Chosen() {
		go func() { _ = s.RotateDevice() }()
	}
	if m.Item(L.InputModeTouch()).Checked(touch).Chosen() {
		sw.mu.Lock()
		sw.touchMode = !sw.touchMode
		sw.mu.Unlock()
	}
}

// cameraMenu is the tool menu of a camera session: no phone keys, clipboard or rotation, which a
// camera stream has none of. all adds the ones the header shows when it has room too.
func (sw *SessionWindow) cameraMenu(m *ui.Menu, all, muted, recording bool) {
	if all {
		if m.Item(L.ActionSwitchCamera()).Chosen() {
			sw.switchCamera()
		}
		if m.Item(L.ActionScreenshot()).Chosen() {
			sw.Screenshot()
		}
		if m.Item(L.ActionFullscreen()).Chosen() {
			sw.toggleFullScreen()
		}
		m.Separator()
	}
	recLabel := L.ActionRecordStart()
	if recording {
		recLabel = L.ActionRecordStop()
	}
	if m.Item(recLabel).Chosen() {
		sw.ToggleRecording()
	}
	if m.Item(L.ActionPlaySound()).Checked(!muted).Chosen() {
		sw.toggleSound()
	}
}

// toggleSound mutes or unmutes the session's sound on this computer.
func (sw *SessionWindow) toggleSound() {
	sw.mu.Lock()
	sw.audioMuted = !sw.audioMuted
	sw.mu.Unlock()
	sw.redraw()
}

// switchCamera shows the phone's other camera in this same pane or window: the last picture
// stays, dimmed under "Switching to the front camera…", while the stream restarts (scrcpy picks
// the camera when it starts). Sound, recording state and placement are kept.
func (sw *SessionWindow) switchCamera() {
	sw.mu.Lock()
	if sw.switching || sw.closing || sw.scrcpySess == nil {
		sw.mu.Unlock()
		return
	}
	sw.switching = true
	old := sw.scrcpySess
	next := "front"
	if sw.spec.CameraFacing == "front" {
		next = "back"
	}
	sw.statusNote = L.CameraSwitching(cameraWord(next))
	sw.mu.Unlock()
	sw.redraw()
	go func() {
		old.Close() // lets go of the camera before the other one opens
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		s, err := sw.app.startCameraStream(ctx, sw, next)
		sw.mu.Lock()
		sw.switching = false
		switch {
		case sw.closing:
			if s != nil {
				go s.Close()
			}
		case err != nil:
			sw.statusNote = sw.app.failed(L.FailCamera(), err)
		default:
			sw.scrcpySess = s
			sw.spec.CameraFacing = next
			sw.spec.Title = sw.app.sessionPhone(sw) + " · " + cameraWord(next)
			sw.statusNote = ""
		}
		sw.mu.Unlock()
		if err == nil && s != nil {
			go sw.watchScrcpyEnd(s)
		}
		sw.redraw()
	}()
}

// switchingNow reports whether Switch camera is restarting sw's stream.
func (sw *SessionWindow) switchingNow() bool {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.switching
}

// cameraWord names a phone camera: "Front Camera" or "Rear Camera".
func cameraWord(facing string) string {
	if facing == "front" {
		return L.AVCameraFront()
	}
	return L.AVCameraBack()
}

// toggleFullScreen shows the session alone on the whole screen: its own window full screen, or,
// docked, the main window full screen.
func (sw *SessionWindow) toggleFullScreen() {
	win := sw.hostWindow()
	if win != nil {
		win.SetFullScreen(!win.IsFullScreen())
	}
}

// telemetryChip is the measured frame rate with a status dot; it opens the details popover: the
// measured rate large with the target muted beside it, the size the phone actually sends, the
// decoder and its time per frame, the connection and the backend (`INV-05`, `INV-06`).
func (sw *SessionWindow) telemetryChip(c *ui.Context, k tokens) {
	sw.mu.Lock()
	fps, w, h := sw.measuredFPS, sw.negotiatedW, sw.negotiatedH
	hw, codec, decUs := sw.hwAccelerated, sw.codecLabel, sw.avgDecodeUs
	target, backend := sw.spec.TargetSpec, sw.spec.BackendLabel
	sw.mu.Unlock()
	dot := k.good
	switch {
	case w <= 0:
		dot = k.text3
	case fps < 20:
		dot = k.warn
	}
	t := c.Theme()
	chip := ui.ButtonBase(c.Key("telem-chip")).Height(26).Padding(0, 9).Radius(pillRadius).Background(k.sunk).Label(L.TelemetryDetails()).Tooltip(L.TelemetryDetails())
	chip.Children(func() {
		ui.Row(c).FillHeight().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(dot)
			ui.Text(c, fmt.Sprintf("%.1f fps", fps)).FontSize(sizeSmall).TextColor(k.text2).FontFeatures("tnum").SingleLine()
		})
	})
	if chip.Clicked() {
		sw.mu.Lock()
		sw.telemOpen = !sw.telemOpen
		sw.mu.Unlock()
	}
	ui.PopoverBase(c.Key("telem-pop"), chip, &sw.telemOpen, func(panel ui.Element) {
		panel.Width(telemetryPopW).Padding(14).Gap(10).Radius(radiusLG).Background(t.Surface).Border(1, t.Border).Shadow(0, 8, 24, 0, paneShadow)
		ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
			ui.Text(c, fmt.Sprintf("%.1f", fps)).Bold().FontSize(28).FontFeatures("tnum")
			ui.Text(c, L.TelemetryMeasured()).FontSize(sizeBody)
			if target != "" {
				ui.Text(c, L.TelemetryTarget(target)).FontSize(sizeSmall).TextColor(k.text3)
			}
		})
		kv := func(key, label, value string) {
			ui.Row(c.Key(key)).FillWidth().Gap(14).Children(func() {
				ui.Text(c, label).FontSize(sizeSmall).TextColor(k.text3).Width(56).Shrink(0)
				ui.Text(c, value).FontSize(sizeSmall).Grow(1).MinWidth(0).FontFeatures("tnum")
			})
		}
		size := "—"
		if w > 0 && h > 0 {
			size = L.TelemetrySize(fmt.Sprintf("%d×%d", w, h))
		}
		dec := L.DecodeSoftware()
		if hw {
			dec = L.DecodeHardware()
		}
		if codec == "" {
			codec = "H.264"
		}
		kv("telem-size", L.TelemetryPicture(), size)
		kv("telem-dec", L.TelemetryDecode(), fmt.Sprintf("%s · %s · %.1f ms", codec, dec, float64(decUs)/1000))
		kv("telem-conn", L.TelemetryConnection(), sw.transportWord())
		kv("telem-mode", L.TelemetryMode(), backend)
	})
}

// drawWindowButtons draws minimize, maximize and close for a frameless window; onClose replaces
// closing the window when set.
func drawWindowButtons(c *ui.Context, win *mygo.Window, onClose func()) {
	t := c.Theme()
	btn := func(name string, glyph *ui.SVG) ui.Element {
		b := ui.ButtonBase(c.Key("win-"+name)).Size(winButtonW, paneHeaderH).Center().Shrink(0).Label(name)
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
	ui.Row(c.Key("win-buttons")).Shrink(0).Children(func() {
		if btn("min", winMinimize).Clicked() && win != nil {
			win.Minimize()
		}
		maximized := win != nil && win.IsMaximized()
		glyph := winMaximize
		if maximized {
			glyph = winRestore
		}
		if btn("max", glyph).MaximizeButton().Clicked() && win != nil {
			if maximized {
				win.Unmaximize()
			} else {
				win.Maximize()
			}
		}
		if btn("close", winClose).Clicked() {
			if onClose != nil {
				onClose()
			} else if win != nil {
				win.Close()
			}
		}
	})
}

// pressKey presses Back, Home or Recents ("back", "home", "recents") on the session's phone:
// through scrcpy's control socket, or through the Companion for its screen sharing.
func (sw *SessionWindow) pressKey(action string) {
	sw.mu.Lock()
	s, share := sw.scrcpySess, sw.shareKey
	sw.mu.Unlock()
	if sw.spec.Mode == SessionModeShare {
		if share != nil {
			share(action)
		}
		return
	}
	if s == nil {
		return
	}
	code := map[string]uint32{"back": scrcpy.KeycodeBack, "home": scrcpy.KeycodeHome, "recents": scrcpy.KeycodeAppSwitch}[action]
	_ = s.TapKeycode(code)
}
