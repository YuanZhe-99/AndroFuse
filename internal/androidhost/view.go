package androidhost

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// RenderCompanionView renders the Android Companion control surface using pure MyGo UI (`INV-09`).
func RenderCompanionView(c *ui.Context, h *Host) {
	if h == nil {
		return
	}
	t := c.Theme()

	h.mu.Lock()
	running := h.running
	port := h.ListenPortLocked()
	cfg := h.cfg
	lastErr := h.lastErr
	audit := append([]string(nil), h.audit...)
	fp := h.FingerprintLocked()
	h.mu.Unlock()

	card := func(ctx *ui.Context) ui.Element {
		return ui.Column(ctx).FillWidth().Padding(14).Gap(10).Radius(12).
			Background(t.Surface).Border(1, t.Border)
	}

	ui.Column(c.Key("android-companion-root")).FillWidth().Gap(14).Children(func() {
		card(c.Key("comp-header-card")).Children(func() {
			ui.Text(c, L.CompanionTitle()).Bold().FontSize(18)
			ui.Text(c, L.CompanionSubtitle()).FontSize(13).TextColor(t.TextMuted)

			statusLbl := L.CompanionStatusStopped()
			statusCol := t.TextMuted
			if running {
				statusLbl = L.CompanionStatusRunning(port)
				statusCol = t.Accent
			}
			ui.Row(c.Key("comp-status-row")).FillWidth().Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				ui.Text(c, statusLbl).Bold().FontSize(13).TextColor(statusCol).Grow(1)
				if running {
					if ui.Button(c.Key("comp-btn-stop"), L.CompanionActionStop()).Clicked() {
						go func() { _ = h.StopServer() }()
					}
				} else {
					if ui.Button(c.Key("comp-btn-start"), L.CompanionActionStart()).Clicked() {
						go func() { _ = h.StartServer() }()
					}
				}
				if ui.Button(c.Key("comp-btn-rotate"), L.CompanionActionRotate()).Clicked() {
					go func() { _, _ = h.RotateToken() }()
				}
			})

			ui.Row(c.Key("comp-token-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, L.CompanionTokenLabel()).FontSize(12).TextColor(t.TextMuted).Width(130)
				ui.Text(c, cfg.Token).Bold().FontSize(13).Grow(1)
			})
			ui.Row(c.Key("comp-fp-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, L.CompanionFingerprintLabel()).FontSize(12).TextColor(t.TextMuted).Width(130)
				ui.Text(c, fp).FontSize(11).TextColor(t.TextMuted).Grow(1).SingleLine()
			})
			if lastErr != "" {
				ui.Text(c, lastErr).FontSize(12).TextColor(t.TextMuted)
			}
		})

		card(c.Key("comp-perms-card")).Children(func() {
			ui.Text(c, L.CompanionPermissionsTitle()).Bold().FontSize(15)
			perms := []struct {
				key   string
				label string
				val   bool
			}{
				{"telemetry", L.PermTelemetry(), cfg.Permissions.Telemetry},
				{"clipboard", L.PermClipboard(), cfg.Permissions.Clipboard},
				{"notifications", L.PermNotifications(), cfg.Permissions.Notifications},
				{"file_transfer", L.PermFileTransfer(), cfg.Permissions.FileTransfer},
				{"app_continuity", L.PermAppContinuity(), cfg.Permissions.AppContinuity},
				{"display_guard", L.PermDisplayGuard(), cfg.Permissions.DisplayGuard},
				{"adb_beacon", L.PermADBBeacon(), cfg.Permissions.ADBBeacon},
				{"media_bridge", L.PermMediaBridge(), cfg.Permissions.MediaBridge},
			}
			for _, p := range perms {
				cur := p.val
				ui.Row(c.Key("perm-row-" + p.key)).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
					ui.Text(c, p.label).FontSize(13).Grow(1)
					sw := ui.Switch(c.Key("perm-sw-"+p.key), &cur)
					if sw.Changed() {
						h.SetPermission(p.key, cur)
					}
				})
			}
		})

		card(c.Key("comp-standby-card")).Children(func() {
			standby := cfg.Standby
			ui.Row(c.Key("standby-row")).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
				ui.Column(c.Key("standby-text")).Grow(1).Gap(2).Children(func() {
					ui.Text(c, L.StandbyTitle()).Bold().FontSize(15)
					ui.Text(c, L.StandbyDesc()).FontSize(12).TextColor(t.TextMuted)
				})
				if ui.Switch(c.Key("standby-sw"), &standby).Changed() {
					go h.SetStandby(standby)
				}
			})
		})

		card(c.Key("comp-beacon-card")).Children(func() {
			ui.Text(c, L.CompanionBeaconTitle()).Bold().FontSize(15)
			ui.Row(c.Key("comp-beacon-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("comp-beacon-input"), &h.beaconInput).Placeholder(L.CompanionBeaconHint()).Grow(1)
				if ui.Button(c.Key("comp-beacon-save"), L.CompanionBeaconSave()).Clicked() {
					p, err := strconv.Atoi(strings.TrimSpace(h.beaconInput))
					if err == nil {
						h.SetWirelessADBPort(p)
					}
				}
			})
		})

		if len(audit) > 0 {
			card(c.Key("comp-audit-card")).Children(func() {
				ui.Text(c, L.CompanionAuditTitle()).Bold().FontSize(15)
				for i, line := range audit {
					ui.Text(c.Key(fmt.Sprintf("audit-%d", i)), line).FontSize(12).TextColor(t.TextMuted)
				}
			})
		}
	})
}
