package gui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// addWizardState holds the state of the guided "+ Add a phone" modal dialog.
type addWizardState struct {
	Open         bool
	Tab          int // 0 USB cable, 1 Same Wi-Fi, 2 Cross-network
	WifiPairCode string
	ManualOpen   bool
	Busy         bool
	Notice       string
	cancel       context.CancelFunc
}

// activateState holds the state of the guided one-time "Activate Enhanced mode" modal dialog.
type activateState struct {
	Open     bool
	Hash     string
	PairCode string
	Busy     bool
	Notice   string
	Done     bool
	ThenOpen int
	cancel   context.CancelFunc
}

// OpenAddWizard opens the guided "+ Add a phone" modal at tab (0 USB, 1 Same Wi-Fi, 2 Cross-network).
// Side effects: triggers a device/mDNS scan so nearby phones appear immediately.
func (a *App) OpenAddWizard(tab int) {
	a.update(func() {
		a.addWizard.Open = true
		if tab >= 0 && tab <= 2 {
			a.addWizard.Tab = tab
		}
		a.addWizard.Notice = ""
		a.searchOpen = false
		a.picker.Open = false
	})
	a.RefreshDevices()
}

// CloseAddWizard closes the "+ Add a phone" modal and cancels any in-flight Wi-Fi pairing.
func (a *App) CloseAddWizard() {
	a.mu.Lock()
	cancel := a.addWizard.cancel
	a.addWizard.Open = false
	a.addWizard.Busy = false
	a.addWizard.Notice = ""
	a.addWizard.cancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.update(func() {})
}

// unpairedPairMDNSServices returns `_adb-tls-pairing._tcp` mDNS entries that are currently open
// on the local Wi-Fi network and not already connected as an online ADB device.
func (a *App) unpairedPairMDNSServices() []adb.MDNSService {
	a.mu.Lock()
	defer a.mu.Unlock()
	online := make(map[string]bool, len(a.devices))
	for _, d := range a.devices {
		if d.Target.State == "device" {
			online[d.HardwareHash] = true
		}
	}
	var out []adb.MDNSService
	for _, s := range a.mdnsServices {
		if !s.CanPair || s.Addr == "" {
			continue
		}
		if s.HardwareHash != "" && online[s.HardwareHash] {
			continue
		}
		out = append(out, s)
	}
	return out
}

func sameIPHost(aAddr, bAddr string) bool {
	ah := aAddr
	if h, _, err := net.SplitHostPort(aAddr); err == nil {
		ah = h
	}
	bh := bAddr
	if h, _, err := net.SplitHostPort(bAddr); err == nil {
		bh = h
	}
	return ah != "" && strings.EqualFold(ah, bh)
}

func hostFromAddr(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// PairWifiMDNS pairs with an auto-discovered `_adb-tls-pairing._tcp` phone on the local Wi-Fi
// using the 6-digit pairing code shown on the phone, then connects to its `_adb-tls-connect._tcp`
// port without asking the user to type any IP address or port.
// Side effects: closes the Add Phone modal and opens the connected phone screen once ready.
func (a *App) PairWifiMDNS(svc adb.MDNSService, rawCode string) {
	code := strings.ReplaceAll(strings.TrimSpace(rawCode), " ", "")
	if !validPairCode(code) {
		a.update(func() { a.addWizard.Notice = L.PairCodeError() })
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	a.mu.Lock()
	if a.addWizard.cancel != nil {
		a.addWizard.cancel()
	}
	a.addWizard.cancel = cancel
	a.addWizard.Busy = true
	a.addWizard.Notice = ""
	a.mu.Unlock()
	a.update(func() {})

	go func() {
		defer cancel()
		client, _, err := a.ensureADBAndLeases()
		if err != nil {
			a.update(func() {
				a.addWizard.Busy = false
				if errors.Is(err, adb.ErrNotFound) {
					a.addWizard.Notice = L.CheckToolsMissing()
					go a.InstallADB()
				} else {
					a.addWizard.Notice = a.failed(L.FailPair(), err)
				}
			})
			return
		}
		pctx, pcancel := context.WithTimeout(ctx, 15*time.Second)
		_, err = client.PairTLS(pctx, svc.Addr, code)
		pcancel()
		if err != nil {
			if ctx.Err() == nil {
				a.update(func() {
					a.addWizard.Busy = false
					a.addWizard.Notice = a.failed(L.FailPair(), err)
				})
			}
			return
		}
		// Locate the phone's `_adb-tls-connect._tcp` port via mDNS, polling briefly in case it
		// takes a second to advertise after pairing succeeds.
		var connectAddr string
		for i := 0; i < 6 && ctx.Err() == nil; i++ {
			svcs, _ := client.MDNSServices(ctx)
			for _, cs := range svcs {
				if !cs.CanConnect || cs.Addr == "" {
					continue
				}
				if (svc.HardwareHash != "" && cs.HardwareHash == svc.HardwareHash) || sameIPHost(cs.Addr, svc.Addr) {
					connectAddr = cs.Addr
					break
				}
			}
			if connectAddr != "" {
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		if connectAddr != "" {
			cctx, ccancel := context.WithTimeout(ctx, 12*time.Second)
			_, cerr := client.ConnectTLS(cctx, connectAddr, false)
			ccancel()
			if cerr == nil {
				a.update(func() {
					a.addWizard.Open = false
					a.addWizard.Busy = false
					a.addWizard.WifiPairCode = ""
					a.addWizard.Notice = ""
					a.addWizard.cancel = nil
				})
				a.afterConnect(connectAddr, openAsScreen)
				return
			}
		}
		serial, _, cerr := a.findAndConnect(ctx, hostFromAddr(svc.Addr), 0)
		if serial != "" {
			a.update(func() {
				a.addWizard.Open = false
				a.addWizard.Busy = false
				a.addWizard.WifiPairCode = ""
				a.addWizard.Notice = ""
				a.addWizard.cancel = nil
			})
			a.afterConnect(serial, openAsScreen)
			return
		}
		a.update(func() {
			a.addWizard.Busy = false
			a.addWizard.Notice = a.connectFailure(cerr)
		})
	}()
}

// OpenActivateModal opens the live-polling guided activation modal for Enhanced mode on hash,
// remembering thenOpen (`openAsDeX`, `openAsApp`, `openAsScreen`) to continue once ready.
// Side effects: starts background 2s polling of Companion status and USB/Wi-Fi ADB availability.
func (a *App) OpenActivateModal(hash string, thenOpen int) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	a.update(func() {
		if a.activate.cancel != nil {
			a.activate.cancel()
		}
		a.activate = activateState{
			Open:     true,
			Hash:     hash,
			ThenOpen: thenOpen,
			cancel:   cancel,
		}
	})
	go a.startActivatePoll(ctx, hash)
}

// CloseActivateModal closes the Enhanced mode activation modal and stops its background poll.
func (a *App) CloseActivateModal() {
	a.mu.Lock()
	cancel := a.activate.cancel
	a.activate.Open = false
	a.activate.Busy = false
	a.activate.Notice = ""
	a.activate.cancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.update(func() {})
}

// startActivatePoll polls Companion status and ADB availability every 2 seconds while the
// Activation modal is open, automatically bootstrapping the daemon as soon as USB or trusted
// Wi-Fi Debugging becomes reachable (`INV-07`).
func (a *App) startActivatePoll(ctx context.Context, hash string) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	poll := func() bool {
		a.RefreshDevices()
		r, ok := a.RememberedFor(hash)
		if !ok {
			return false
		}
		var st companion.DeviceStatus
		if r.Companion != nil {
			if tok, err := openSecret(r.Companion.SealedToken); err == nil {
				dctx, dcancel := context.WithTimeout(ctx, 4*time.Second)
				c, _, cerr := a.findCompanion(dctx, hash, *r.Companion, string(tok))
				dcancel()
				if cerr == nil && c != nil {
					sctx, scancel := context.WithTimeout(ctx, 3*time.Second)
					if gst, serr := c.GetStatus(sctx); serr == nil {
						st = gst
						a.update(func() {
							if a.peerStatus == nil {
								a.peerStatus = make(map[string]companion.DeviceStatus)
							}
							a.peerStatus[hash] = gst
							if a.compHash == hash {
								a.compStatus = gst
							}
						})
					}
					scancel()
					_ = c.Close()
				}
			}
		}
		if st.DaemonReady && st.DaemonTrustedDisplay {
			a.update(func() {
				a.activate.Done = true
				a.activate.Busy = false
				a.activate.Notice = ""
			})
			return true
		}
		onlineADB := a.hasOnlineADB(hash)
		if onlineADB || (st.NetworkType == "wifi" && st.WirelessADBPort > 0 && !a.isADBAuthNeeded(hash)) {
			bctx, bcancel := context.WithTimeout(ctx, 18*time.Second)
			cand, found := a.discoverAndConnectADB(bctx, hash, false)
			if found {
				if client, _, err := a.ensureADBAndLeases(); err == nil {
					apkPath, localHash := a.localCompanionAPK()
					needsAPK := apkPath != "" && localHash != "" && st.AppBuildHash != "" && !strings.EqualFold(st.AppBuildHash, localHash)
					if a.bootstrapDaemonOverSerial(bctx, client, r, cand.Serial, apkPath, needsAPK, true) {
						bcancel()
						a.setADBAuthNeeded(hash, false)
						a.RefreshDevices()
						a.update(func() {
							a.activate.Done = true
							a.activate.Busy = false
							a.activate.Notice = ""
						})
						return true
					}
				}
			}
			bcancel()
		}
		return false
	}
	if poll() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if poll() {
				return
			}
		}
	}
}

// ActivatePairAndBootstrap pairs desktop ADB to the phone using the 6-digit Wireless debugging
// pairing code (either directly via LAN mDNS/Wi-Fi IP or tunneled through Companion when
// WirelessADBPairPort is reported), then starts the Enhanced mode daemon once (`dex.StartDaemon`).
func (a *App) ActivatePairAndBootstrap() {
	a.mu.Lock()
	hash := a.activate.Hash
	code := strings.ReplaceAll(strings.TrimSpace(a.activate.PairCode), " ", "")
	svcs := append([]adb.MDNSService(nil), a.mdnsServices...)
	a.mu.Unlock()
	if !validPairCode(code) {
		a.update(func() { a.activate.Notice = L.PairCodeError() })
		return
	}
	r, ok := a.RememberedFor(hash)
	if !ok {
		return
	}
	a.update(func() {
		a.activate.Busy = true
		a.activate.Notice = ""
	})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		client, _, err := a.ensureADBAndLeases()
		if err != nil {
			a.update(func() {
				a.activate.Busy = false
				if errors.Is(err, adb.ErrNotFound) {
					a.activate.Notice = L.CheckToolsMissing()
					go a.InstallADB()
				} else {
					a.activate.Notice = a.failed(L.FailPair(), err)
				}
			})
			return
		}
		st, _ := a.peerStatusFor(hash)
		var pairAddr string
		for _, s := range svcs {
			if s.CanPair && s.Addr != "" && ((s.HardwareHash != "" && s.HardwareHash == hash) || (st.WifiIPv4 != "" && sameIPHost(s.Addr, st.WifiIPv4))) {
				pairAddr = s.Addr
				break
			}
		}
		var pairErr error
		paired := false
		if pairAddr != "" {
			pctx, pcancel := context.WithTimeout(ctx, 12*time.Second)
			_, pairErr = client.PairTLS(pctx, pairAddr, code)
			pcancel()
			paired = pairErr == nil
		}
		if !paired && st.WirelessADBPairPort > 0 && st.WifiIPv4 != "" && st.NetworkType == "wifi" && !a.isMeshAddress(st.WifiIPv4) {
			directPair := net.JoinHostPort(st.WifiIPv4, strconv.Itoa(st.WirelessADBPairPort))
			pctx, pcancel := context.WithTimeout(ctx, 8*time.Second)
			_, pairErr = client.PairTLS(pctx, directPair, code)
			pcancel()
			paired = pairErr == nil
		}
		// Fall back to tunneling the pairing port through Companion if direct LAN is blocked/unavailable.
		if !paired && st.WirelessADBPairPort > 0 && r.Companion != nil {
			if tok, serr := openSecret(r.Companion.SealedToken); serr == nil {
				c, compAddr, cerr := a.findCompanion(ctx, hash, *r.Companion, string(tok))
				if cerr == nil && c != nil {
					_ = c.Close()
					tctx, tcancel := context.WithCancel(ctx)
					localPair, terr := companion.ServeTunnels(tctx, compAddr, string(tok), r.Companion.Fingerprint, st.WirelessADBPairPort)
					if terr == nil {
						pctx, pcancel := context.WithTimeout(ctx, 12*time.Second)
						_, pairErr = client.PairTLS(pctx, localPair, code)
						pcancel()
						paired = pairErr == nil
					} else {
						pairErr = terr
					}
					tcancel()
				}
			}
		}
		if !paired {
			if pairErr == nil {
				pairErr = errors.New(L.WizardWifiNone())
			}
			a.update(func() {
				a.activate.Busy = false
				a.activate.Notice = a.failed(L.FailPair(), pairErr)
			})
			return
		}
		a.setADBAuthNeeded(hash, false)
		cand, found := a.discoverAndConnectADB(ctx, hash, true)
		if !found {
			a.update(func() {
				a.activate.Busy = false
				a.activate.Notice = L.ConnectNoAnswerHint()
			})
			return
		}
		apkPath, localHash := a.localCompanionAPK()
		needsAPK := apkPath != "" && localHash != "" && st.AppBuildHash != "" && !strings.EqualFold(st.AppBuildHash, localHash)
		if !a.bootstrapDaemonOverSerial(ctx, client, r, cand.Serial, apkPath, needsAPK, true) {
			a.update(func() {
				a.activate.Busy = false
				a.activate.Notice = L.FailDeX()
			})
			return
		}
		a.RefreshDevices()
		a.update(func() {
			a.activate.Done = true
			a.activate.Busy = false
			a.activate.Notice = ""
		})
	}()
}

// addWizardModal renders the guided "+ Add a phone" 3-tab modal dialog.
func (a *App) addWizardModal(c *ui.Context, k tokens) {
	a.mu.Lock()
	wiz := a.addWizard
	a.mu.Unlock()
	if !wiz.Open {
		return
	}
	t := c.Theme()
	ui.DialogBase(c.Key("add-wizard"), &a.addWizard.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.28)).Center()
		panel.Width(580).MaxHeight(680).Radius(cardRadius).Background(t.Surface).Border(1, t.Border).Shadow(0, 18, 48, 0, paneShadow).Clip()

		// Header
		ui.Row(c.Key("wiz-head")).FillWidth().Padding(18, 22, 14, 22).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, L.WizardTitle()).Bold().FontSize(sizeTitle)
				ui.Text(c, L.WizardSubtitle()).FontSize(sizeSmall).TextColor(k.text3)
			})
			if iconBtn(c, "wiz-close", winClose, L.PaneClose(), false).Clicked() {
				a.CloseAddWizard()
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(t.Border)

		ui.Scroll(c.Key("wiz-scroll")).Grow(1).FillWidth().Padding(18, 22).Children(func() {
			ui.Column(c.Key("wiz-body")).FillWidth().Gap(14).Children(func() {
				ui.Segmented(c.Key("wiz-tabs"), &a.addWizard.Tab, L.WizardTabUSB(), L.WizardTabWifi(), L.WizardTabMesh())

				switch wiz.Tab {
				case 0:
					a.wizardUSBTab(c, k)
				case 1:
					a.wizardWifiTab(c, k, wiz)
				default:
					a.wizardMeshTab(c, k)
				}

				if wiz.Notice != "" {
					ui.Text(c.Key("wiz-notice"), wiz.Notice).FontSize(sizeSmall).TextColor(k.warnText)
				}

				ui.Box(c).FillWidth().Height(1).Background(t.Border)
				ui.Collapsible(c.Key("wiz-adv"), L.WizardAdvancedTitle(), &a.addWizard.ManualOpen, func() {
					a.connectAdvancedBody(c, k)
				})
			})
		})
	})
}

func (a *App) wizardStepRow(c *ui.Context, k tokens, key string, num int, title, sub string) {
	t := c.Theme()
	ui.Row(c.Key(key)).FillWidth().Padding(12, 14).Gap(12).Radius(radiusMD).Background(t.Background).Border(1, t.Border).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Size(24, 24).Radius(12).Background(k.brandSoft).Center().Shrink(0).Children(func() {
			ui.Text(c, fmt.Sprintf("%d", num)).Bold().FontSize(sizeSmall).TextColor(k.brandSoftText).FontFeatures("tnum")
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).Bold().FontSize(sizeBody)
			ui.Text(c, sub).FontSize(sizeSmall).TextColor(k.text2)
		})
	})
}

func (a *App) wizardUSBTab(c *ui.Context, k tokens) {
	ui.Text(c, L.WizardTabUSBSub()).FontSize(sizeSmall).TextColor(k.text3)
	a.wizardStepRow(c, k, "wiz-usb-1", 1, L.WizardUSBStep1Title(), L.WizardUSBStep1Sub())
	a.wizardStepRow(c, k, "wiz-usb-2", 2, L.WizardUSBStep2Title(), L.WizardUSBStep2Sub())
	a.wizardStepRow(c, k, "wiz-usb-3", 3, L.WizardUSBStep3Title(), L.WizardUSBStep3Sub())

	a.mu.Lock()
	devs := append([]DiscoveredDevice(nil), a.devices...)
	a.mu.Unlock()
	usbReady := false
	for _, d := range devs {
		if d.Target.Transport == adb.TransportUSB && d.Target.State == "device" {
			usbReady = true
			break
		}
	}
	ui.Row(c.Key("wiz-usb-stat")).FillWidth().Padding(8, 2, 0, 2).Gap(8).AlignItems(ui.Center).Children(func() {
		if usbReady {
			ui.Icon(c, iconCheck).Size(16, 16).TextColor(k.goodText)
			ui.Text(c, L.CheckOK()).Bold().FontSize(sizeSmall).TextColor(k.goodText)
		} else {
			ui.Spinner(c).Size(16, 16)
			ui.Text(c, L.WizardUSBWaiting()).FontSize(sizeSmall).TextColor(k.text2).Grow(1)
			if ui.Button(c.Key("wiz-usb-ref"), L.DevicesRefresh()).Clicked() {
				a.RefreshDevices()
			}
		}
	})
}

func (a *App) wizardWifiTab(c *ui.Context, k tokens, wiz addWizardState) {
	t := c.Theme()
	ui.Text(c, L.WizardTabWifiSub()).FontSize(sizeSmall).TextColor(k.text3)
	a.wizardStepRow(c, k, "wiz-wifi-1", 1, L.WizardWifiStep1Title(), L.WizardWifiStep1Sub())
	svcs := a.unpairedPairMDNSServices()
	if len(svcs) == 0 {
		ui.Row(c.Key("wiz-wifi-none")).FillWidth().Padding(14).Gap(10).Radius(radiusMD).Background(t.Background).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(16, 16)
			ui.Text(c, L.WizardWifiNone()).FontSize(sizeSmall).TextColor(k.text2).Grow(1)
			if ui.Button(c.Key("wiz-wifi-scan"), L.DevicesRefresh()).Clicked() {
				a.RefreshDevices()
			}
		})
		return
	}
	for i, s := range svcs {
		s := s
		key := fmt.Sprintf("wiz-mdns-%d", i)
		ui.Column(c.Key(key)).FillWidth().Padding(14).Gap(10).Radius(radiusMD).Background(t.Background).Border(1, k.brand).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconWifi).Size(18, 18).TextColor(k.brand)
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, L.HomeDiscoveredSameWifi()).Bold().FontSize(sizeBody)
					ui.Text(c, L.WizardWifiPairFound()).FontSize(sizeSmall).TextColor(k.text2)
				})
			})
			ui.Row(c.Key(key + "-in")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				in := ui.TextInput(c.Key(key+"-code"), &a.addWizard.WifiPairCode).Placeholder(L.PairCodeLabel()).Grow(1).MinWidth(0)
				code := strings.ReplaceAll(strings.TrimSpace(wiz.WifiPairCode), " ", "")
				if in.Submitted() && validPairCode(code) && !wiz.Busy {
					a.PairWifiMDNS(s, wiz.WifiPairCode)
				}
				if ui.PrimaryButton(c.Key(key+"-go"), L.PairAndConnect()).Disabled(wiz.Busy || !validPairCode(code)).Clicked() {
					a.PairWifiMDNS(s, wiz.WifiPairCode)
				}
			})
		})
	}
}

func (a *App) wizardMeshTab(c *ui.Context, k tokens) {
	t := c.Theme()
	ui.Text(c, L.WizardTabMeshSub()).FontSize(sizeSmall).TextColor(k.text3)
	a.wizardStepRow(c, k, "wiz-mesh-1", 1, L.WizardMeshStep1Title(), L.WizardMeshStep1Sub())
	peers := a.othersInNetworks()
	hasNets := len(a.networks()) > 0
	if len(peers) == 0 {
		ui.Column(c.Key("wiz-mesh-empty")).FillWidth().Padding(14).Gap(10).Radius(radiusMD).Background(t.Background).Border(1, t.Border).Children(func() {
			msg := L.WizardMeshNone()
			if !hasNets {
				msg = L.WizardMeshNeedNet()
			}
			ui.Text(c, msg).FontSize(sizeSmall).TextColor(k.text2)
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c.Key("wiz-mesh-net"), L.WizardOpenNetSettings()).Clicked() {
					a.CloseAddWizard()
					a.openSettings(setNetwork)
				}
			})
		})
		return
	}
	for _, p := range peers {
		p := p
		_, devID, _, _ := mesh.ParseHostname(p.Peer)
		key := "wiz-mesh-p-" + p.Peer
		ui.Column(c.Key(key)).FillWidth().Padding(12, 14).Gap(8).Radius(radiusMD).Background(t.Background).Border(1, t.Border).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				phoneAvatar(c, avatarSmall, phoneLogo(p.Name), nil, p.Name)
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, p.Name).Bold().FontSize(sizeBody).SingleLine()
					ui.Text(c, L.InNetworkNever(p.Network)+" · "+wayWords(p.Direct, p.Latency)).FontSize(sizeSmall).TextColor(k.text3).SingleLine()
				})
				if ui.PrimaryButton(c.Key(key+"-pair"), L.NetworkPairConnect()).Clicked() {
					a.PairMeshPhone(p)
				}
			})
			a.cardWorkLine(c, k, key, "net:"+devID)
		})
	}
}

// activateModal renders the guided one-time "Activate Enhanced mode" dialog with live status chips.
func (a *App) activateModal(c *ui.Context, k tokens) {
	a.mu.Lock()
	act := a.activate
	svcs := append([]adb.MDNSService(nil), a.mdnsServices...)
	a.mu.Unlock()
	if !act.Open {
		return
	}
	r, ok := a.RememberedFor(act.Hash)
	if !ok {
		return
	}
	st, hasSt := a.peerStatusFor(act.Hash)
	t := c.Theme()
	name := r.displayName()

	ui.DialogBase(c.Key("activate-modal"), &a.activate.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.28)).Center()
		panel.Width(560).MaxHeight(680).Radius(cardRadius).Background(t.Surface).Border(1, t.Border).Shadow(0, 18, 48, 0, paneShadow).Clip()

		ui.Row(c.Key("act-head")).FillWidth().Padding(18, 22, 14, 22).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, L.ActivateTitle(name)).Bold().FontSize(sizeTitle)
				ui.Text(c, L.ActivateSub()).FontSize(sizeSmall).TextColor(k.text2)
			})
			if iconBtn(c, "act-close", winClose, L.PaneClose(), false).Clicked() {
				a.CloseActivateModal()
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(t.Border)

		ui.Scroll(c.Key("act-scroll")).Grow(1).FillWidth().Padding(18, 22).Children(func() {
			ui.Column(c.Key("act-body")).FillWidth().Gap(14).Children(func() {
				if act.Done {
					ui.Column(c.Key("act-done")).FillWidth().Padding(16).Gap(10).Radius(radiusMD).Background(k.good.Alpha(0.12)).Border(1, k.good).Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, iconCheck).Size(18, 18).TextColor(k.goodText)
							ui.Text(c, L.ActivateDoneTitle()).Bold().FontSize(sizeHeading).TextColor(k.goodText)
						})
						ui.Text(c, L.ActivateDoneSub()).FontSize(sizeSmall).TextColor(k.text2)
						ui.Row(c).Padding(4, 0, 0, 0).Gap(8).Children(func() {
							if ui.PrimaryButton(c.Key("act-cont"), L.ActivateOpenNow()).Clicked() {
								thenOpen := act.ThenOpen
								hash := act.Hash
								a.CloseActivateModal()
								a.runAfterEnhanced(hash, thenOpen)
							}
						})
					})
					return
				}

				// Live phone state chips
				ui.Row(c.Key("act-chips")).FillWidth().Gap(8).Wrap().Children(func() {
					if hasSt && st.NetworkType == "wifi" {
						pill(c, "act-net-chip", L.ChipWifiOn(), k.good.Alpha(0.14), k.goodText)
					} else {
						pill(c, "act-net-chip", L.ChipCellularOn(), k.warn.Alpha(0.16), k.warnText)
					}
					if hasSt && (st.WirelessDebugOn || st.WirelessADBPort > 0) {
						pill(c, "act-adb-chip", L.ChipWirelessADBOn(), k.good.Alpha(0.14), k.goodText)
					} else if hasSt {
						pill(c, "act-adb-chip", L.ChipWirelessADBOff(), k.sunk, k.text2)
					}
					hasPairWin := hasSt && st.WirelessADBPairPort > 0
					if !hasPairWin {
						for _, s := range svcs {
							if s.CanPair && ((s.HardwareHash != "" && s.HardwareHash == act.Hash) || (hasSt && sameIPHost(s.Addr, st.WifiIPv4))) {
								hasPairWin = true
								break
							}
						}
					}
					if hasPairWin {
						pill(c, "act-pair-chip", L.ChipPairWindowFound(), k.brandSoft, k.brandSoftText)
					}
				})

				onWifi := !hasSt || st.NetworkType == "wifi"
				wirelessOn := hasSt && (st.WirelessDebugOn || st.WirelessADBPort > 0 || st.WirelessADBPairPort > 0)

				switch {
				case !onWifi:
					ui.Column(c.Key("act-cell")).FillWidth().Padding(14).Gap(6).Radius(radiusMD).Background(t.Background).Border(1, t.Border).Children(func() {
						ui.Text(c, L.ActivateCellularTitle()).Bold().FontSize(sizeBody)
						ui.Text(c, L.ActivateCellularBody()).FontSize(sizeSmall).TextColor(k.text2)
					})
					a.wizardStepRow(c, k, "act-cell-usb", 1, L.WizardUSBStep1Title(), L.ActivateUSBSub())
					a.wizardStepRow(c, k, "act-cell-wifi", 2, L.ActivateWifiOffOptionA(), L.ActivateWifiOffOptionASub())
				case wirelessOn:
					a.wizardStepRow(c, k, "act-w-1", 1, L.ActivateWifiPairStep1(), L.ActivateWifiPairStep1Sub())
					ui.Column(c.Key("act-w-pair")).FillWidth().Padding(14).Gap(8).Radius(radiusMD).Background(t.Background).Border(1, t.Border).Children(func() {
						ui.Text(c, L.ActivateWifiPairStep2()).Bold().FontSize(sizeBody)
						ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
							in := ui.TextInput(c.Key("act-code"), &a.activate.PairCode).Placeholder("123456").Grow(1).MinWidth(0)
							code := strings.ReplaceAll(strings.TrimSpace(act.PairCode), " ", "")
							if in.Submitted() && validPairCode(code) && !act.Busy {
								a.ActivatePairAndBootstrap()
							}
							if ui.PrimaryButton(c.Key("act-go"), L.ActivateWifiPairBtn()).Disabled(act.Busy || !validPairCode(code)).Clicked() {
								a.ActivatePairAndBootstrap()
							}
						})
					})
					a.wizardStepRow(c, k, "act-w-usb", 2, L.ActivateOptionUSBBrief(), L.ActivateUSBSub())
				default:
					a.wizardStepRow(c, k, "act-off-1", 1, L.ActivateWifiOffOptionA(), L.ActivateWifiOffOptionASub())
					a.wizardStepRow(c, k, "act-off-2", 2, L.ActivateOptionUSBBrief(), L.ActivateUSBSub())
				}

				if act.Notice != "" {
					ui.Text(c.Key("act-notice"), act.Notice).FontSize(sizeSmall).TextColor(k.warnText)
				}
				ui.Row(c.Key("act-foot")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, L.ChipWaitingPairWindow()).FontSize(sizeSmall).TextColor(k.text3).Grow(1)
					if ui.Button(c.Key("act-later"), L.ActionLater()).Clicked() {
						a.CloseActivateModal()
					}
				})
			})
		})
	})
}
