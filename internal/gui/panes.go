package gui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// Pages that show sessions: "pane:<id>" is one docked session, pageSplit two side by side.
const (
	panePrefix = "pane:"
	pageSplit  = "split"
)

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
// (Settings › Windows and split).
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
// back to the other pane, a page showing only id falls back to Devices.
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
		a.page = string(pageDevices)
	}
}

// splitWith shows the docked sessions a and b side by side, or b below a when vertical.
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

// modeIcon is the icon of a session's work mode.
func modeIcon(m SessionMode) *ui.SVG {
	switch m {
	case SessionModeDeX, SessionModeMiracastDeX, SessionModeDeXFallback:
		return iconDex
	case SessionModeApp:
		return iconApps
	case SessionModeCamera:
		return iconCamera
	default:
		return iconDevices
	}
}

// shortTitle is what the sidebar and pane headers call a session: its mode, or its app.
func (sw *SessionWindow) shortTitle() string {
	switch sw.spec.Mode {
	case SessionModeApp:
		if sw.spec.AppPackage != "" && sw.spec.Title != "" {
			return sw.spec.Title
		}
		return L.PaneAppTitle()
	case SessionModeCamera:
		return L.PaneCameraTitle()
	case SessionModeMirror:
		return L.PaneMirrorTitle()
	default:
		return sw.spec.BackendLabel
	}
}

// telemetryLine is the measured state of a session: transport, negotiated size and measured
// frame rate, never the target (`INV-05`).
func (sw *SessionWindow) telemetryLine() string {
	sw.mu.Lock()
	w, h, fps, tr := sw.negotiatedW, sw.negotiatedH, sw.measuredFPS, sw.spec.Transport
	sw.mu.Unlock()
	if w <= 0 || h <= 0 {
		return tr
	}
	return L.PaneTelemetry(tr, fmt.Sprintf("%d×%d", w, h), fmt.Sprintf("%.1f", fps))
}

// paneArea fills the main window with the session page: one docked session, or a split of two.
// Returns: false when the page names no open session, so the caller shows Devices instead.
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
		build(c.Key("pane-split"), &split.Percent,
			func() { a.pane(c, sa, sb) },
			func() { a.pane(c, sb, sa) }).SplitReset(50).Fill()
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
	a.pane(c, sw, nil)
	return true
}

// pane draws one docked session under its header; partner is the other half of a split.
func (a *App) pane(c *ui.Context, sw *SessionWindow, partner *SessionWindow) {
	ui.Column(c.Key("pane-" + sw.spec.ID)).Fill().MinWidth(0).MinHeight(0).Bind(&sw.paneHandle).Children(func() {
		a.paneHeader(c, sw, partner)
		ui.Column(c.Key("pane-body")).Grow(1).FillWidth().MinHeight(0).Background(ui.Hex(stageBgHex)).Children(func() {
			sw.content(c, true)
		})
	})
}

// paneHeader is a docked session's 44-DIP header: icon, title and measured telemetry, then pop
// out, split right, split down and close, which fold into ⋯ when the pane is narrow.
func (a *App) paneHeader(c *ui.Context, sw *SessionWindow, partner *SessionWindow) {
	t := c.Theme()
	k := tok(c)
	box := sw.paneHandle.Bounds(c)
	narrow := box.W > 0 && box.W < paneFoldW

	var others []*SessionWindow
	for _, o := range a.sessionsSorted() {
		if o != sw && o.Docked() {
			others = append(others, o)
		}
	}
	canRight := len(others) > 0 && partner == nil && (box.W == 0 || box.W >= 2*splitMinW)
	canDown := len(others) > 0 && partner == nil && (box.H == 0 || box.H >= 2*splitMinH)

	splitMenu := func(vertical bool) func(m *ui.Menu) {
		return func(m *ui.Menu) {
			for _, o := range others {
				if m.Item(L.PaneSplitWith(o.shortTitle())).Chosen() {
					a.splitWith(sw.spec.ID, o.spec.ID, vertical)
				}
			}
		}
	}

	ui.Row(c.Key("pane-head")).Height(paneHeaderH).Shrink(0).FillWidth().Padding(0, 6, 0, 12).Gap(8).
		AlignItems(ui.Center).Background(t.Surface).Children(func() {
		ui.Icon(c, modeIcon(sw.spec.Mode)).Size(navIconSize, navIconSize).TextColor(k.brand)
		ui.Column(c.Key("pane-title")).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, sw.shortTitle()).Bold().FontSize(sizeBody).SingleLine()
			ui.Text(c, sw.telemetryLine()).FontSize(sizeSmall).TextColor(k.text3).FontFeatures("tnum").SingleLine()
		})
		if narrow {
			ui.Button(c.Key("pane-more"), "").Label(L.PaneMore()).Children(func() {
				ui.Icon(c, iconMore).Size(14, 14)
			}).PopoverMenuButton(func(m *ui.Menu) {
				if m.Item(L.PanePopOut()).Chosen() {
					sw.PopOut()
				}
				for _, o := range others {
					if m.Item(L.PaneSplitWith(o.shortTitle())).Disabled(!canRight).Chosen() {
						a.splitWith(sw.spec.ID, o.spec.ID, false)
					}
				}
				if partner != nil && m.Item(L.PaneUnsplit()).Chosen() {
					a.unsplit(sw.spec.ID)
				}
				if m.Item(L.ActionDisconnect()).Chosen() {
					sw.Close()
				}
			})
			return
		}
		if ui.Button(c.Key("pane-pop"), "").Tooltip(L.PanePopOut()).Children(func() {
			ui.Icon(c, iconPopOut).Size(14, 14)
		}).Clicked() {
			sw.PopOut()
		}
		if partner != nil {
			if ui.Button(c.Key("pane-unsplit"), "").Tooltip(L.PaneUnsplit()).Children(func() {
				ui.Icon(c, iconMaximize).Size(14, 14)
			}).Clicked() {
				a.unsplit(sw.spec.ID)
			}
		} else {
			ui.Button(c.Key("pane-split-right"), "").Tooltip(L.PaneSplitRight()).Disabled(!canRight).Children(func() {
				ui.Icon(c, iconSplitRight).Size(14, 14)
			}).PopoverMenuButton(splitMenu(false))
			ui.Button(c.Key("pane-split-down"), "").Tooltip(L.PaneSplitDown()).Disabled(!canDown).Children(func() {
				ui.Icon(c, iconSplitDown).Size(14, 14)
			}).PopoverMenuButton(splitMenu(true))
		}
		if ui.Button(c.Key("pane-close"), "").Tooltip(L.ActionDisconnect()).Children(func() {
			ui.Icon(c, winClose).Size(winGlyph, winGlyph)
		}).Clicked() {
			sw.Close()
		}
	})
	ui.Box(c.Key("pane-head-line")).Height(1).FillWidth().Background(t.Border)
}

// openSidebarItems adds the sidebar's "Open" section (docked sessions, and a split as one entry)
// and "Windows" section (popped-out sessions) to a ui.Sidebar.
func (a *App) openSidebarItems(c *ui.Context) {
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
	if len(docked) > 0 {
		ui.SidebarSection(c.Key("nav-open"), L.NavOpen(), nil, func() {
			if split != nil {
				sa, sb := a.session(split.A), a.session(split.B)
				if sa != nil && sb != nil {
					ui.SidebarItem(c, pageSplit, iconSplitRight, sa.shortTitle()+" + "+sb.shortTitle())
				}
			}
			for _, s := range docked {
				ui.SidebarItem(c, panePrefix+s.spec.ID, modeIcon(s.spec.Mode), s.shortTitle())
			}
		})
	}
	if len(popped) > 0 {
		ui.SidebarSection(c.Key("nav-windows"), L.NavWindows(), nil, func() {
			for _, s := range popped {
				ui.SidebarItem(c, windowPrefix+s.spec.ID, modeIcon(s.spec.Mode), s.shortTitle())
			}
		})
	}
}

// windowPrefix marks a sidebar item that raises a popped-out session's window.
const windowPrefix = "win:"
