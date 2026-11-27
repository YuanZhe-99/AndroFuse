package androidhost

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/androidbridge"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
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

// pairPrompt asks the phone's user to confirm a computer's pairing request by tapping the
// 6-digit SAS code matching what the computer shows (`INV-09`).
func (h *Host) pairPrompt(c *ui.Context) {
	req, choices, ok := h.PendingPairChoices()
	if !ok {
		return
	}
	t := c.Theme()
	card(c.Key("comp-pair-card")).Border(2, t.Accent).Children(func() {
		ui.Text(c, L.PairPromptTitle()).Bold().FontSize(16)
		ui.Text(c, L.PairPromptChoose(req.ClientName)).FontSize(13).TextColor(t.TextMuted)
		ui.Row(c.Key("comp-pair-choices")).FillWidth().Gap(8).Children(func() {
			for i, ch := range choices {
				ch := ch
				b := ui.ButtonBase(c.Key(fmt.Sprintf("comp-pair-choice-%d", i))).Grow(1).Basis(0).Height(44).
					Radius(10).Background(t.Background).Border(1, t.Border).Center().Label(spaced(ch))
				b.Children(func() {
					ui.Text(c, spaced(ch)).Bold().FontSize(18).FontFeatures("tnum")
				})
				if b.Clicked() {
					go h.AnswerPairChoice(ch)
				}
			}
		})
		ui.Row(c.Key("comp-pair-actions")).Gap(8).Children(func() {
			if ui.Button(c.Key("comp-pair-decline"), L.PairDecline()).Clicked() {
				go h.AnswerPair(false)
			}
		})
	})
}

// homeTab is the Companion's main page in sections: the app's name, Connection (the service and
// Wireless debugging), Paired computers, Network, Settings (standby, language, notifications,
// and, unless they show beside it on an unfolded phone, permissions and security) and More.
func (h *Host) homeTab(c *ui.Context, wide bool) {
	t := c.Theme()
	h.mu.Lock()
	running, cfg, lastErr := h.running, h.cfg, h.lastErr
	h.mu.Unlock()

	ui.Column(c.Key("comp-head")).FillWidth().Gap(4).Padding(8, 4, 4, 4).Children(func() {
		ui.Text(c, "AndroFuse").Bold().FontSize(28)
		ui.Text(c, L.PhoneTagline()).FontSize(14).TextColor(t.TextMuted)
	})

	sectionTitle(c, L.PhoneSectionConnection())
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
					go h.StopByUser()
				}
			} else if ui.PrimaryButton(c.Key("comp-btn-start"), L.CompanionActionStart()).Clicked() {
				go func() { _ = h.StartServer() }()
			}
		})
		if lastErr != "" {
			ui.Text(c, lastErr).FontSize(12).TextColor(t.Danger)
		}
	})
	h.daemonCard(c)
	h.adbCard(c)

	sectionTitle(c, L.PhoneComputersTitle())
	h.computersCard(c)

	sectionTitle(c, L.MeshPhoneTitle())
	h.meshCard(c)

	sectionTitle(c, L.PhoneSectionSettings())
	h.settingsCard(c)
	if !wide {
		on := 0
		for _, v := range []bool{cfg.Permissions.Telemetry, cfg.Permissions.Clipboard, cfg.Permissions.Notifications, cfg.Permissions.FileTransfer,
			cfg.Permissions.AppContinuity, cfg.Permissions.DisplayGuard, cfg.Permissions.ADBBeacon, cfg.Permissions.MediaBridge} {
			if v {
				on++
			}
		}
		h.linkRow(c, "home-perms", iconShield, L.PhonePermsSummary(), L.PhonePermsOn(on), tabPermissions)
		h.linkRow(c, "home-security", iconLock, L.PhoneSecuritySummary(), L.PhoneSecuritySub(), tabSecurity)
	}

	sectionTitle(c, L.PhoneSectionMore())
	h.aboutCard(c)
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

// meshCard shows the phone's EasyTier network (handed over by a paired computer or typed here),
// Edit, its low-power switch and Leave.
func (h *Host) meshCard(c *ui.Context) {
	t := c.Theme()
	name, running, errMsg := h.MeshState()
	card(c.Key("comp-mesh-card")).Children(func() {
		ui.Row(c.Key("comp-mesh-head")).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			tile(c, iconGlobe)
			ui.Column(c.Key("comp-mesh-text")).Grow(1).MinWidth(0).Gap(2).Children(func() {
				title := L.MeshPhoneNotJoined()
				if name != "" {
					title = name
				}
				ui.Text(c, title).Bold().FontSize(14).SingleLine()
				switch {
				case name == "":
					ui.Text(c, L.MeshPhoneNone()).FontSize(12).TextColor(t.TextMuted)
				case running:
					ui.Text(c, L.MeshPhoneJoined(name)).FontSize(12).TextColor(t.TextMuted)
				default:
					ui.Text(c, L.MeshPhoneConnecting()).FontSize(12).TextColor(t.TextMuted)
				}
			})
			h.mu.Lock()
			editing := h.meshForm != nil
			h.mu.Unlock()
			if !editing {
				label := L.MeshPhoneJoinOwn()
				if name != "" {
					label = L.ActionEdit()
				}
				if ui.Button(c.Key("comp-mesh-edit"), label).Clicked() {
					h.editMesh()
				}
			}
			if name != "" && ui.Button(c.Key("comp-mesh-leave"), L.MeshPhoneLeave()).Clicked() {
				go h.LeaveMesh()
			}
		})
		h.meshEditor(c)
		if name != "" {
			saver := !h.MeshFullSpeed()
			switchRow(c, "mesh-saver", L.MeshSaverTitle(), L.MeshSaverDesc(), &saver, func() { h.SetMeshFullSpeed(!saver) })
		}
		if errMsg != "" {
			ui.Text(c, errMsg).FontSize(12).TextColor(t.Danger)
		}
	})
}

// settingsCard holds background standby (PLAN §9.1-3), the app's language and a way to Android's
// notification settings for AndroFuse.
func (h *Host) settingsCard(c *ui.Context) {
	t := c.Theme()
	h.mu.Lock()
	standby := h.cfg.Standby
	h.mu.Unlock()
	card(c.Key("comp-settings-card")).Children(func() {
		switchRow(c, "standby", L.StandbyTitle(), L.StandbyDesc(), &standby, func() { go h.SetStandby(standby) })
		ui.Box(c).FillWidth().Height(1).Background(t.Border)
		ui.Column(c.Key("comp-lang")).FillWidth().Gap(8).Children(func() {
			ui.Text(c, L.SettingsLanguage()).Bold().FontSize(14)
			if h.langIndex < 0 {
				h.langIndex = langIndexOf(h.language())
			}
			if ui.Segmented(c.Key("comp-lang-seg"), &h.langIndex, L.LangSystem(), L.LangZhHans(), L.LangEnglish()).FillWidth().Changed() {
				h.setLanguage(phoneLangs[h.langIndex])
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(t.Border)
		b := ui.ButtonBase(c.Key("comp-notif")).FillWidth().Label(L.PhoneNotifTitle())
		b.Children(func() {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, L.PhoneNotifTitle()).Bold().FontSize(14)
					ui.Text(c, L.PhoneNotifSub()).FontSize(12).TextColor(t.TextMuted)
				})
				ui.Icon(c, iconChevron).Size(14, 14).TextColor(t.TextMuted)
			})
		})
		if b.Clicked() {
			go androidbridge.OpenNotificationSettings()
		}
	})
}

// phoneLangs are the language choices in Settings, in the segmented control's order: the
// system's, Simplified Chinese, English.
var phoneLangs = []string{"", "zh-Hans", "en"}

func langIndexOf(lang string) int {
	for i, l := range phoneLangs {
		if l == lang {
			return i
		}
	}
	return 0
}

// language and setLanguage reach the app's saved language (SetLanguageHooks); without hooks the
// choice is not kept.
func (h *Host) language() string {
	if h.getLang == nil {
		return ""
	}
	return h.getLang()
}

func (h *Host) setLanguage(lang string) {
	if h.setLang != nil {
		h.setLang(lang)
	}
}

// SetLanguageHooks connects the phone's language choice to the app's preferences: get returns the
// saved one ("" for the system's) and set saves and applies a new one.
func (h *Host) SetLanguageHooks(get func() string, set func(string)) {
	h.getLang, h.setLang = get, set
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
			{"screen_share", L.PermScreenShare(), L.PermScreenShareSub(), iconScreen, p.ScreenShare},
			{"media_bridge", L.PermMediaBridge(), L.PermMediaBridgeSub(), iconMic, p.MediaBridge},
			{"display_guard", L.PermDisplayGuard(), L.PermDisplayGuardSub(), iconScreen, p.DisplayGuard},
			{"adb_beacon", L.PermADBBeacon(), L.PermADBBeaconSub(), iconGlobe, p.ADBBeacon},
		}},
	}
	for gi, g := range groups {
		sectionTitle(c, g.title)
		card(c.Key(fmt.Sprintf("perm-group-%d", gi))).Children(func() {
			for _, pm := range g.perms {
				on := pm.on
				ui.Row(c.Key("perm-row-" + pm.key)).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
					tile(c, pm.icon)
					switchRow(c, "perm-"+pm.key, pm.title, pm.sub, &on, func() { h.SetPermission(pm.key, on) })
				})
			}
			if gi == 1 {
				h.accessibilityRow(c)
			}
		})
	}
}

// securityTab holds the certificate fingerprint and the recent activity.
func (h *Host) securityTab(c *ui.Context) {
	t := c.Theme()
	h.mu.Lock()
	fp := h.FingerprintLocked()
	audit := append([]string(nil), h.audit...)
	h.mu.Unlock()

	sectionTitle(c, L.CompanionFingerprintLabel())
	card(c.Key("comp-fp-card")).Children(func() {
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
		sectionTitle(c, L.CompanionAuditTitle())
		card(c.Key("comp-audit-card")).Children(func() {
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

// daemonCard shows whether the persistent direct wireless control daemon is active (`PLAN` §9.2).
func (h *Host) daemonCard(c *ui.Context) {
	t := c.Theme()
	h.mu.Lock()
	ready := h.daemonReady
	h.mu.Unlock()
	dot := t.TextMuted
	title, sub := L.PhoneDaemonOff(), L.PhoneDaemonOffSub()
	if ready {
		dot, title, sub = t.Accent, L.PhoneDaemonReady(), L.PhoneDaemonReadySub()
	}
	card(c.Key("comp-daemon-card")).Children(func() {
		ui.Row(c.Key("comp-daemon-row")).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			tile(c, iconScreen)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Box(c.Key("comp-daemon-dot")).Size(8, 8).Radius(4).Background(dot)
					ui.Text(c, title).Bold().FontSize(14)
				})
				ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted)
			})
		})
	})
}

// adbCard shows this phone's Wireless debugging port, which the Companion finds on its own
// (NsdManager) and gives the paired computer, or why there is none.
func (h *Host) adbCard(c *ui.Context) {
	t := c.Theme()
	h.mu.Lock()
	port, network, check := h.cfg.WirelessADBPort, h.network, h.beaconCheck
	h.mu.Unlock()
	title, sub := L.PhoneADBOff(), L.PhoneADBOffSub()
	switch {
	case port > 0:
		title, sub = L.PhoneADBPort(port), L.PhoneADBPortSub()
	case network != "" && network != "wifi" && network != "ethernet":
		title, sub = L.PhoneADBNoWifi(), L.PhoneADBNoWifiSub()
	}
	card(c.Key("comp-adb-card")).Children(func() {
		sectionTitle(c, L.PhoneADBTitle())
		ui.Row(c.Key("comp-adb-row")).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			tile(c, iconGlobe)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, title).Bold().FontSize(14).FontFeatures("tnum")
				ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted)
			})
		})
		if check != "" && port > 0 {
			ui.Text(c, check).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// accessibilityRow shows whether the Companion's accessibility service is on (Back, Home and
// Recents while sharing the screen) and opens Android's settings to turn it on.
func (h *Host) accessibilityRow(c *ui.Context) {
	t := c.Theme()
	h.mu.Lock()
	on, stale := h.a11yOn, time.Since(h.a11yChecked) > 3*time.Second
	if stale {
		h.a11yChecked = time.Now()
	}
	h.mu.Unlock()
	if stale {
		go func() {
			now := androidbridge.AccessibilityOn()
			h.mu.Lock()
			changed := now != h.a11yOn
			h.a11yOn = now
			h.mu.Unlock()
			if changed {
				h.notify()
			}
		}()
	}
	ui.Row(c.Key("perm-row-a11y")).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		tile(c, iconHand)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, L.PermA11y()).Bold().FontSize(14)
			sub := L.PermA11ySubOff()
			if on {
				sub = L.PermA11ySubOn()
			}
			ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted)
		})
		if !on && ui.Button(c.Key("perm-a11y-open"), L.PermA11yOpen()).Clicked() {
			go androidbridge.OpenAccessibilitySettings()
		}
	})
}

// meshEditor is the network form under the network card while it is open: name, servers and
// secret, as on the computer's Settings › Network.
func (h *Host) meshEditor(c *ui.Context) {
	h.mu.Lock()
	f := h.meshForm
	h.mu.Unlock()
	if f == nil {
		return
	}
	t := c.Theme()
	ui.Column(c.Key("comp-mesh-form")).FillWidth().Gap(10).Padding(8, 0, 0, 0).Children(func() {
		ui.Text(c, L.NetFormName()).FontSize(12).TextColor(t.TextMuted)
		ui.TextInput(c.Key("comp-mesh-name"), &f.Name).FillWidth()
		ui.Text(c, L.NetFormServers()).FontSize(12).TextColor(t.TextMuted)
		ui.TextArea(c.Key("comp-mesh-servers"), &f.Servers).FillWidth().Lines(2, 5).Font("monospace")
		ui.Text(c, L.NetFormServersHint()).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, L.NetFormSecret()).FontSize(12).TextColor(t.TextMuted)
		ui.TextInput(c.Key("comp-mesh-secret"), &f.Secret).FillWidth().Password()
		if f.Err != "" {
			ui.Text(c, f.Err).FontSize(12).TextColor(t.Danger)
		}
		ui.Row(c.Key("comp-mesh-btns")).FillWidth().Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c.Key("comp-mesh-cancel"), L.ActionCancel()).Clicked() {
				h.mu.Lock()
				h.meshForm = nil
				h.mu.Unlock()
			}
			ok := strings.TrimSpace(f.Name) != "" && f.Secret != ""
			if ui.PrimaryButton(c.Key("comp-mesh-save"), L.ActionSave()).Disabled(!ok).Clicked() {
				snapshot := *f
				go h.saveMeshForm(snapshot)
			}
		})
	})
}

// computersCard lists the computers paired with this phone, most recently connected first, each
// with when it last connected, Rename (a name kept on this phone) and Forget.
func (h *Host) computersCard(c *ui.Context) {
	t := c.Theme()
	list := h.Computers()
	card(c.Key("comp-pcs-card")).Children(func() {
		if len(list) == 0 {
			ui.Text(c, L.PhoneComputersNone()).FontSize(12).TextColor(t.TextMuted)
			return
		}
		for _, pc := range list {
			key := "comp-pc-" + pc.ID
			if h.renaming == pc.ID {
				ui.Row(c.Key(key + "-edit")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c.Key(key+"-name"), &h.renameText).Grow(1).MinWidth(0)
					if ui.Button(c.Key(key+"-cancel"), L.ActionCancel()).Clicked() {
						h.renaming = ""
					}
					if ui.PrimaryButton(c.Key(key+"-save"), L.ActionSave()).Clicked() {
						id, text := pc.ID, h.renameText
						h.renaming = ""
						go h.RenameComputer(id, text)
					}
				})
				continue
			}
			ui.Row(c.Key(key)).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				tile(c, iconLaptop)
				ui.Column(c.Key(key + "-text")).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, pc.shownName()).Bold().FontSize(14).SingleLine()
					when := "—"
					if !pc.LastSeen.IsZero() {
						when = pc.LastSeen.Local().Format("2006-01-02 15:04")
					}
					ui.Text(c, L.PhoneComputerLastSeen(when)).FontSize(12).TextColor(t.TextMuted).SingleLine()
				})
				more := ui.ButtonBase(c.Key(key+"-more")).Size(40, 40).Shrink(0).Radius(20).Center().Label(L.PhoneComputerMore())
				more.Children(func() { ui.Icon(c, iconMore).Size(20, 20).TextColor(t.TextMuted) })
				more.PopoverMenuButton(func(m *ui.Menu) {
					if m.Item(L.ActionRename()).Chosen() {
						h.renaming, h.renameText = pc.ID, pc.shownName()
					}
					if m.Item(L.PhoneComputerForget()).Danger().Chosen() {
						go h.ForgetComputer(pc.ID)
					}
				})
			})
		}
	})
}

// aboutCard shows the Companion's version and its certificate fingerprint, which a paired
// computer pins.
func (h *Host) aboutCard(c *ui.Context) {
	t := c.Theme()
	card(c.Key("comp-about-card")).Children(func() {
		ui.Row(c.Key("comp-about-row")).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			tile(c, iconInfo)
			ui.Column(c.Key("comp-about-text")).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, L.PhoneAboutTitle()).Bold().FontSize(14)
				ui.Text(c, L.PhoneVersion(version.Version)).FontSize(12).TextColor(t.TextMuted)
			})
		})
	})
}
