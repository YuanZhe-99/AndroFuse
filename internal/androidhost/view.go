package androidhost

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// Companion tabs (V2.1 design: Home / Permissions / Security).
const (
	tabHome        = "home"
	tabPermissions = "permissions"
	tabSecurity    = "security"
)

// Layout of the Companion view, in dp.
const (
	phoneTwoColumnMin = 680 // Fold 8 unfolded and tablets use two columns
	phoneCardRadius   = 12
	phoneCardPad      = 14
	phoneGap          = 12
	phoneTileSize     = 38 // icon tile of a permission row
	phoneTabBarH      = 60
)

// RenderCompanionView renders the Android Companion: a pairing prompt above three tabs, Home,
// Permissions and Security, in one column on phones and two on unfolded foldables (`INV-09`).
func RenderCompanionView(c *ui.Context, h *Host) {
	if h == nil {
		return
	}
	t := c.Theme()
	w, _ := c.Size()
	wide := w >= phoneTwoColumnMin

	h.mu.Lock()
	tab := h.tab
	h.mu.Unlock()
	if tab == "" {
		tab = tabHome
	}

	ui.Column(c.Key("companion-shell")).Fill().Background(t.Background).Children(func() {
		ui.Scroll(c.Key("companion-scroll")).Grow(1).FillWidth().Padding(16).Children(func() {
			ui.Column(c.Key("companion-body")).FillWidth().Gap(phoneGap).Children(func() {
				h.pairPrompt(c)
				switch {
				case wide:
					// Unfolded: Home beside Permissions, Security below.
					ui.Row(c.Key("companion-cols")).FillWidth().Gap(phoneGap).AlignItems(ui.Start).Children(func() {
						ui.Column(c.Key("col-a")).Grow(1).Basis(0).Gap(phoneGap).Children(func() { h.homeTab(c, true) })
						ui.Column(c.Key("col-b")).Grow(1).Basis(0).Gap(phoneGap).Children(func() {
							h.permissionsTab(c)
							h.securityTab(c)
						})
					})
				case tab == tabPermissions:
					h.permissionsTab(c)
				case tab == tabSecurity:
					h.securityTab(c)
				default:
					h.homeTab(c, false)
				}
			})
		})
		if !wide {
			h.tabBar(c, tab)
		}
	})
}

// card is the surface every Companion section sits on.
func card(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Column(c).FillWidth().Padding(phoneCardPad).Gap(10).Radius(phoneCardRadius).
		Background(t.Surface).Border(1, t.Border)
}

// sectionTitle is a muted label above a group of rows.
func sectionTitle(c *ui.Context, s string) {
	ui.Text(c, s).FontSize(12).Bold().TextColor(c.Theme().TextMuted)
}

// pairPrompt asks the phone's user to allow a computer's adb pairing request, showing its code.
func (h *Host) pairPrompt(c *ui.Context) {
	req, ok := h.PendingPair()
	if !ok {
		return
	}
	t := c.Theme()
	card(c.Key("comp-pair-card")).Border(2, t.Accent).Children(func() {
		ui.Text(c, L.PairPromptTitle()).Bold().FontSize(16)
		ui.Text(c, L.PairPromptBody(req.ClientName, req.Code)).FontSize(13).TextColor(t.TextMuted)
		ui.Text(c.Key("comp-pair-code"), spaced(req.Code)).Bold().FontSize(28).FontFeatures("tnum")
		ui.Row(c.Key("comp-pair-actions")).Gap(8).Children(func() {
			if ui.PrimaryButton(c.Key("comp-pair-allow"), L.PairAllow()).Clicked() {
				go h.AnswerPair(true)
			}
			if ui.Button(c.Key("comp-pair-decline"), L.PairDecline()).Clicked() {
				go h.AnswerPair(false)
			}
		})
	})
}

// homeTab shows the service switch, the pairing code and address, the mesh and standby, and,
// unless the other tabs already show beside it (wide), links to them.
func (h *Host) homeTab(c *ui.Context, wide bool) {
	t := c.Theme()
	h.mu.Lock()
	running, port, cfg, lastErr := h.running, h.ListenPortLocked(), h.cfg, h.lastErr
	h.mu.Unlock()

	card(c.Key("comp-service-card")).Children(func() {
		ui.Row(c.Key("comp-service-row")).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			dot := t.TextMuted
			title, sub := L.PhoneServiceOff(), L.PhoneServiceOffSub()
			if running {
				dot, title, sub = t.Accent, L.PhoneServiceOn(), L.PhoneServiceOnSub()
			}
			ui.Box(c.Key("comp-service-dot")).Size(10, 10).Radius(5).Background(dot)
			ui.Column(c.Key("comp-service-text")).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, title).Bold().FontSize(15)
				ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted)
			})
			if running {
				if ui.Button(c.Key("comp-btn-stop"), L.CompanionActionStop()).Clicked() {
					go func() { _ = h.StopServer() }()
				}
			} else if ui.PrimaryButton(c.Key("comp-btn-start"), L.CompanionActionStart()).Clicked() {
				go func() { _ = h.StartServer() }()
			}
		})
		if lastErr != "" {
			ui.Text(c, lastErr).FontSize(12).TextColor(t.Danger)
		}
	})

	card(c.Key("comp-code-card")).Children(func() {
		sectionTitle(c, L.CompanionTokenLabel())
		ui.Row(c.Key("comp-code-row")).FillWidth().Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, cfg.Token).Bold().FontSize(22).FontFeatures("tnum").Grow(1)
			if ui.Button(c.Key("comp-code-copy"), L.ActionCopy()).Clicked() {
				c.WriteClipboard(cfg.Token)
				c.Toast(L.Copied())
			}
			if ui.Button(c.Key("comp-btn-rotate"), L.PhoneNewCode()).Clicked() {
				go func() { _, _ = h.RotateToken() }()
			}
		})
		if running {
			ui.Text(c, net.JoinHostPort(lanIPv4(), strconv.Itoa(port))).FontSize(12).TextColor(t.TextMuted).FontFeatures("tnum")
		}
	})

	on := 0
	for _, v := range []bool{cfg.Permissions.Telemetry, cfg.Permissions.Clipboard, cfg.Permissions.Notifications, cfg.Permissions.FileTransfer,
		cfg.Permissions.AppContinuity, cfg.Permissions.DisplayGuard, cfg.Permissions.ADBBeacon, cfg.Permissions.MediaBridge} {
		if v {
			on++
		}
	}
	if !wide {
		h.linkRow(c, "home-perms", iconShield, L.PhonePermsSummary(), L.PhonePermsOn(on), tabPermissions)
		h.linkRow(c, "home-security", iconLock, L.PhoneSecuritySummary(), L.PhoneSecuritySub(), tabSecurity)
	}
	h.meshCard(c)
	h.standbyCard(c)
}

// linkRow is a Home card that opens another tab (on unfolded foldables every tab already shows).
func (h *Host) linkRow(c *ui.Context, key string, icon *ui.SVG, title, sub, tab string) {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).FillWidth().Padding(phoneCardPad).Radius(phoneCardRadius).
		Background(t.Surface).Border(1, t.Border).Label(title)
	b.Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			tile(c, icon)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, title).Bold().FontSize(14)
				ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted)
			})
			ui.Icon(c, iconChevron).Size(14, 14).TextColor(t.TextMuted)
		})
	})
	if b.Clicked() {
		h.mu.Lock()
		h.tab = tab
		h.mu.Unlock()
	}
}

// meshCard shows the EasyTier network a paired computer handed over, its low-power switch and
// Leave.
func (h *Host) meshCard(c *ui.Context) {
	t := c.Theme()
	name, running, errMsg := h.MeshState()
	card(c.Key("comp-mesh-card")).Children(func() {
		ui.Row(c.Key("comp-mesh-head")).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			tile(c, iconGlobe)
			ui.Column(c.Key("comp-mesh-text")).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, L.MeshPhoneTitle()).Bold().FontSize(14)
				switch {
				case name == "":
					ui.Text(c, L.MeshPhoneNone()).FontSize(12).TextColor(t.TextMuted)
				case running:
					ui.Text(c, L.MeshPhoneJoined(name)).FontSize(12).TextColor(t.TextMuted)
				default:
					ui.Text(c, L.MeshPhoneConnecting()).FontSize(12).TextColor(t.TextMuted)
				}
			})
			if name != "" && ui.Button(c.Key("comp-mesh-leave"), L.MeshPhoneLeave()).Clicked() {
				go h.LeaveMesh()
			}
		})
		if name != "" {
			saver := !h.MeshFullSpeed()
			switchRow(c, "mesh-saver", L.MeshSaverTitle(), L.MeshSaverDesc(), &saver, func() { h.SetMeshFullSpeed(!saver) })
		}
		if errMsg != "" {
			ui.Text(c, errMsg).FontSize(12).TextColor(t.Danger)
		}
	})
}

// standbyCard holds the background standby switch (PLAN §9.1-3).
func (h *Host) standbyCard(c *ui.Context) {
	h.mu.Lock()
	standby := h.cfg.Standby
	h.mu.Unlock()
	card(c.Key("comp-standby-card")).Children(func() {
		switchRow(c, "standby", L.StandbyTitle(), L.StandbyDesc(), &standby, func() { go h.SetStandby(standby) })
	})
}

// permissionsTab groups the graded opt-in permissions: everyday collaboration, then screen and
// safety (`INV-09`).
func (h *Host) permissionsTab(c *ui.Context) {
	h.mu.Lock()
	p := h.cfg.Permissions
	h.mu.Unlock()
	type perm struct {
		key, title, sub string
		icon            *ui.SVG
		on              bool
	}
	groups := []struct {
		title string
		perms []perm
	}{
		{L.PhonePermsEveryday(), []perm{
			{"telemetry", L.PermTelemetry(), L.PermTelemetrySub(), iconBattery, p.Telemetry},
			{"clipboard", L.PermClipboard(), L.PermClipboardSub(), iconClipboard, p.Clipboard},
			{"notifications", L.PermNotifications(), L.PermNotificationsSub(), iconBell, p.Notifications},
			{"file_transfer", L.PermFileTransfer(), L.PermFileTransferSub(), iconFolder, p.FileTransfer},
			{"app_continuity", L.PermAppContinuity(), L.PermAppContinuitySub(), iconHandoff, p.AppContinuity},
		}},
		{L.PhonePermsScreen(), []perm{
			{"media_bridge", L.PermMediaBridge(), L.PermMediaBridgeSub(), iconMic, p.MediaBridge},
			{"display_guard", L.PermDisplayGuard(), L.PermDisplayGuardSub(), iconScreen, p.DisplayGuard},
			{"adb_beacon", L.PermADBBeacon(), L.PermADBBeaconSub(), iconGlobe, p.ADBBeacon},
		}},
	}
	for gi, g := range groups {
		card(c.Key(fmt.Sprintf("perm-group-%d", gi))).Children(func() {
			sectionTitle(c, g.title)
			for _, pm := range g.perms {
				on := pm.on
				ui.Row(c.Key("perm-row-" + pm.key)).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
					tile(c, pm.icon)
					switchRow(c, "perm-"+pm.key, pm.title, pm.sub, &on, func() { h.SetPermission(pm.key, on) })
				})
			}
		})
	}
}

// securityTab holds the cross-network wireless debugging port, the certificate fingerprint and
// the recent activity.
func (h *Host) securityTab(c *ui.Context) {
	t := c.Theme()
	h.mu.Lock()
	fp := h.FingerprintLocked()
	audit := append([]string(nil), h.audit...)
	h.mu.Unlock()

	card(c.Key("comp-beacon-card")).Children(func() {
		ui.Text(c, L.CompanionBeaconTitle()).Bold().FontSize(14)
		ui.Text(c, L.PhoneBeaconSub()).FontSize(12).TextColor(t.TextMuted)
		ui.Row(c.Key("comp-beacon-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, lanIPv4()+":").FontSize(13).TextColor(t.TextMuted).FontFeatures("tnum")
			ui.TextInput(c.Key("comp-beacon-input"), &h.beaconInput).Placeholder(L.CompanionBeaconHint()).Grow(1).MinWidth(0)
			p, err := strconv.Atoi(strings.TrimSpace(h.beaconInput))
			valid := err == nil && p >= 1024 && p <= 65535
			if ui.Button(c.Key("comp-beacon-save"), L.CompanionBeaconSave()).Disabled(!valid).Clicked() {
				h.SaveWirelessADBPort(p)
			}
		})
		h.mu.Lock()
		check := h.beaconCheck
		h.mu.Unlock()
		if check != "" {
			ui.Text(c, check).FontSize(12).TextColor(t.TextMuted)
		}
		if s := strings.TrimSpace(h.beaconInput); s != "" {
			if p, err := strconv.Atoi(s); err != nil || p < 1024 || p > 65535 {
				ui.Text(c, L.PortRangeError()).FontSize(12).TextColor(t.Danger)
			}
		}
	})

	card(c.Key("comp-fp-card")).Children(func() {
		ui.Text(c, L.CompanionFingerprintLabel()).Bold().FontSize(14)
		ui.Text(c, fp).FontSize(12).FontFeatures("tnum")
		ui.Text(c, L.PhoneFingerprintSub()).FontSize(12).TextColor(t.TextMuted)
		ui.Row(c.Key("comp-fp-actions")).Children(func() {
			if ui.Button(c.Key("comp-fp-copy"), L.ActionCopy()).Clicked() {
				c.WriteClipboard(fp)
				c.Toast(L.Copied())
			}
		})
	})

	if len(audit) > 0 {
		card(c.Key("comp-audit-card")).Children(func() {
			ui.Text(c, L.CompanionAuditTitle()).Bold().FontSize(14)
			for i := len(audit) - 1; i >= 0; i-- {
				ui.Text(c.Key(fmt.Sprintf("audit-%d", i)), audit[i]).FontSize(12).TextColor(t.TextMuted).FontFeatures("tnum")
			}
		})
	}
}

// tabBar is the bottom navigation between the three tabs on phones and folded foldables.
func (h *Host) tabBar(c *ui.Context, cur string) {
	t := c.Theme()
	items := []struct {
		id    string
		icon  *ui.SVG
		label string
	}{
		{tabHome, iconHomeTab, L.PhoneTabHome()},
		{tabPermissions, iconShield, L.PhoneTabPermissions()},
		{tabSecurity, iconLock, L.PhoneTabSecurity()},
	}
	ui.Row(c.Key("companion-tabs")).FillWidth().Height(phoneTabBarH).Shrink(0).Padding(6, 12).Gap(6).
		Background(t.Surface).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
		for _, it := range items {
			fg := t.TextMuted
			b := ui.ButtonBase(c.Key("tab-" + it.id)).Grow(1).FillHeight().Radius(10).Center().Label(it.label)
			if cur == it.id {
				fg = t.Accent
			}
			b.Children(func() {
				ui.Column(c).Gap(3).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, it.icon).Size(18, 18).TextColor(fg)
					ui.Text(c, it.label).FontSize(11).TextColor(fg).SingleLine()
				})
			})
			if b.Clicked() {
				h.mu.Lock()
				h.tab = it.id
				h.mu.Unlock()
			}
		}
	})
}

// switchRow is a title, a muted line and a switch that runs onChange after the user flips it.
func switchRow(c *ui.Context, key, title, sub string, v *bool, onChange func()) {
	t := c.Theme()
	ui.Row(c.Key(key + "-row")).Grow(1).MinWidth(0).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Column(c.Key(key + "-text")).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(14)
			if sub != "" {
				ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted)
			}
		})
		if ui.Switch(c.Key(key+"-sw"), v).Label(title).Changed() {
			onChange()
		}
	})
}

// tile is the fixed-size icon container of a row.
func tile(c *ui.Context, icon *ui.SVG) {
	t := c.Theme()
	ui.Box(c).Size(phoneTileSize, phoneTileSize).Shrink(0).Radius(10).Background(t.SurfaceHover).Center().Children(func() {
		ui.Icon(c, icon).Size(18, 18).TextColor(t.Accent)
	})
}

// spaced groups a 6-digit code as "123 456".
func spaced(code string) string {
	if len(code) == 6 {
		return code[:3] + " " + code[3:]
	}
	return code
}

// lanIPv4 returns the phone's outbound IPv4 address, found without sending anything (a UDP
// "connection" only picks a route), or "" offline.
func lanIPv4() string {
	conn, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		return ""
	}
	defer conn.Close()
	if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}
