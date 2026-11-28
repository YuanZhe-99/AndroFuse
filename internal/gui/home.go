package gui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/dex"
	"github.com/YuanZhe-99/AndroFuse/internal/mesh"
	"github.com/YuanZhe-99/AndroFuse/internal/vm"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// homePane is the consumer Home pane: hero header with network & Add a phone controls,
// conditional ADB/USB-auth banners, remembered/online phone cards, and automatically
// discovered Wi-Fi/network phones ready to pair.
func (a *App) homePane(c *ui.Context, k tokens) {
	t := c.Theme()
	ui.Column(c.Key("home-pane")).Grow(1).FillWidth().MinHeight(0).Radius(paneRadius).Clip().
		Background(t.Background).Border(1, t.Border).Children(func() {
		ui.Row(c.Key("home-head")).Height(paneHeaderH).Shrink(0).FillWidth().Padding(0, 0, 0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconHome).Size(18, 18).TextColor(k.text2)
			ui.Text(c, L.NavHome()).Bold().FontSize(sizeBody).DragWindow()
			ui.Box(c.Key("home-drag")).Grow(1).FillHeight().DragWindow()
			a.windowButtons(c)
		})
		ui.Box(c.Key("home-line")).Height(1).FillWidth().Background(t.Border)
		ui.Scroll(c.Key("home-scroll")).Grow(1).FillWidth().Children(func() {
			ui.Column(c.Key("home-body")).FillWidth().MaxWidth(homeMaxW).Padding(24, 28, 32, 28).Gap(14).AlignSelf(ui.Center).Children(func() {
				a.homeHero(c, k)
				a.adbBanner(c, k)
				a.phonesSection(c, k)
			})
		})
	})
}

// homeHero renders Home's clean top header: "My phones", phone count pill, network status, All/Online
// filter, refresh, and the primary "+ Add a phone" action.
func (a *App) homeHero(c *ui.Context, k tokens) {
	a.mu.Lock()
	devs := append([]DiscoveredDevice(nil), a.devices...)
	onlineOnly := a.prefs.HomeOnlineOnly
	scanning := a.scanningDevices
	a.mu.Unlock()
	offline := a.offlineRemembered(devs)
	count := len(devs) + len(offline)
	dot, netWords := a.networkSummary(k)

	ui.Row(c.Key("home-hero")).FillWidth().Gap(12).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Column(c.Key("home-hero-l")).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, L.HomeMyPhones()).Bold().FontSize(sizeTitle)
				pill(c, "home-count", strconv.Itoa(count), k.sunk, k.text2)
			})
			ui.Text(c, L.HomeMyPhonesSub()).FontSize(sizeSmall).TextColor(k.text3)
		})
		ui.Row(c.Key("home-hero-r")).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			t := c.Theme()
			nb := ui.ButtonBase(c.Key("home-net-btn")).Height(32).Padding(0, 10).Radius(radiusMD).Background(t.Surface).Border(1, t.Border).Label(netWords)
			if nb.Hovered() {
				nb.Background(t.SurfaceHover)
			}
			nb.Children(func() {
				ui.Row(c).FillHeight().Gap(6).AlignItems(ui.Center).Children(func() {
					statusDot(c, dot)
					ui.Text(c, netWords).FontSize(sizeSmall).SingleLine()
				})
			})
			if nb.Clicked() {
				a.openSettings(setNetwork)
			}

			idx := 0
			if onlineOnly {
				idx = 1
			}
			if ui.Segmented(c.Key("phones-filter"), &idx, L.HomeAll(), L.HomeOnline()).Changed() {
				a.mu.Lock()
				a.prefs.HomeOnlineOnly = idx == 1
				a.savePrefs()
				a.mu.Unlock()
			}
			rb := iconBtn(c, "phones-refresh", iconRefresh, L.DevicesRefresh(), false)
			if scanning {
				rb.Disabled(true)
			}
			if rb.Clicked() {
				a.RefreshDevices()
			}
			if ui.PrimaryButton(c.Key("home-add-btn"), "+ "+L.NavAddPhone()).Clicked() {
				a.OpenAddWizard(0)
			}
		})
	})
}

// adbBanner shows a quiet progress or retry card on Home ONLY when managed phone connection tools
// (ADB) are missing, currently downloading, or failed to download (`INV-10`). When ready, renders nothing.
func (a *App) adbBanner(c *ui.Context, k tokens) {
	a.mu.Lock()
	missing, installing, pct, errMsg := a.adbMissing, a.adbInstalling, a.adbInstallPct, a.adbInstallErr
	a.mu.Unlock()
	if !missing && !installing && errMsg == "" {
		return
	}
	t := c.Theme()
	ui.Row(c.Key("adb-banner")).FillWidth().Radius(cardRadius).Background(t.Surface).Border(1, t.Border).Clip().Children(func() {
		edge := k.brand
		if !installing {
			edge = k.warn
		}
		ui.Box(c).Width(noticeEdge).FillHeight().Background(edge)
		ui.Column(c).Grow(1).MinWidth(0).Padding(12, 16).Gap(8).Children(func() {
			switch {
			case installing:
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(16, 16)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, L.ADBDownloadingTitle(pct)).Bold().FontSize(sizeBody)
						ui.Text(c, L.ADBDownloadingSub()).FontSize(sizeSmall).TextColor(k.text2)
					})
				})
				ui.Progress(c.Key("adb-dl-prog"), float64(pct)/100.0).FillWidth()
			case errMsg != "":
				ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconWarn).Size(18, 18).TextColor(k.warnText)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, errMsg).Bold().FontSize(sizeBody)
						ui.Text(c, L.ADBDownloadFailedSub()).FontSize(sizeSmall).TextColor(k.text2)
					})
					if ui.PrimaryButton(c.Key("adb-retry-btn"), L.ADBActionRetry()).Clicked() {
						a.InstallADB()
					}
				})
			default:
				ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconDownload).Size(18, 18).TextColor(k.warnText)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, L.CheckToolsMissing()).Bold().FontSize(sizeBody)
						ui.Text(c, L.ADBDownloadingSub()).FontSize(sizeSmall).TextColor(k.text2)
					})
					if ui.PrimaryButton(c.Key("adb-dl-btn"), L.ADBActionDownload()).Clicked() {
						a.InstallADB()
					}
				})
			}
		})
	})
}

// usbAuthBanner prompts the user cleanly on Home whenever a plugged USB phone is awaiting
// the phone-side "Allow USB debugging" confirmation.
func (a *App) usbAuthBanner(c *ui.Context, k tokens, devs []DiscoveredDevice) {
	for _, d := range devs {
		hasUnauth := false
		for _, tr := range d.Transports {
			if tr.Transport == adb.TransportUSB && tr.State == "unauthorized" {
				hasUnauth = true
				break
			}
		}
		if !hasUnauth {
			continue
		}
		noticeCard(c, k, "usb-auth-"+d.HardwareHash, iconShield, true, L.USBAuthBannerTitle(), L.USBAuthBannerSub(a.phoneName(d)), func() {
			if ui.Button(c.Key("usb-auth-ref-"+d.HardwareHash), L.DevicesRefresh()).Clicked() {
				a.RefreshDevices()
			}
		})
		break
	}
}

// vmStateWords is the managed VM's state in words.
// vmLive corrects the VM's state with what adb sees now: an emulator adb lists but that is still
// booting is starting, an online one is running (also one started outside AndroFuse), so this
// card agrees with the emulator's phone card.
func (a *App) vmLive(st vm.Status) vm.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range a.devices {
		if d.Target.Transport != adb.TransportEmulator {
			continue
		}
		switch {
		case d.Target.State == "device":
			st.State, st.RunningSerial = vm.StateRunning, d.Target.Serial
		case st.State != vm.StateRunning:
			st.State = vm.StateStarting
		}
	}
	return st
}

func vmStateWords(st vm.Status) string {
	switch st.State {
	case vm.StateDownloading:
		return L.VMStateDownloading(st.ProgressPct)
	case vm.StateStarting:
		return L.VMStateStarting()
	case vm.StateRunning:
		return L.VMStateRunningNow()
	case vm.StateStopped:
		return L.VMStateStopped()
	case vm.StateError:
		return L.VMStateError()
	default:
		return L.VMStateNotInstalled()
	}
}

// vmAction is the one button that moves the managed VM on: Download, Start or Stop.
func (a *App) vmAction(c *ui.Context, key string, st vm.Status) {
	busy := st.State == vm.StateDownloading || st.State == vm.StateStarting
	switch {
	case st.State == vm.StateRunning:
		if ui.Button(c.Key(key), L.VMActionStop()).Clicked() {
			a.StopVM()
		}
	case len(st.AvailableAVDs) > 0:
		if ui.Button(c.Key(key), L.VMActionStart()).Disabled(busy).Clicked() {
			a.StartVM(st.AVDName)
		}
	default:
		if ui.Button(c.Key(key), L.VMActionDownload()).Disabled(busy).Clicked() {
			a.ProvisionVM()
		}
	}
}

// connectAdvancedBody renders the manual wireless address/port and pairing fields inside the
// "+ Add a phone" wizard's collapsible Advanced section.
func (a *App) connectAdvancedBody(c *ui.Context, k tokens) {
	a.mu.Lock()
	f := a.connect
	a.mu.Unlock()
	ui.Column(c.Key("home-connect")).FillWidth().Gap(gap).Children(func() {
		ui.Row(c.Key("conn-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c.Key("conn-input"), &a.connect.Input).Placeholder(L.ConnectPlaceholder()).Grow(1).MinWidth(0)
			if in.Submitted() {
				a.ConnectByAddress()
			}
			if f.Phase == connectLooking {
				if ui.Button(c.Key("conn-cancel"), L.ActionCancel()).Clicked() {
					a.CancelConnect()
				}
			} else if ui.PrimaryButton(c.Key("conn-go"), L.ConnectGo()).Disabled(strings.TrimSpace(f.Input) == "").Clicked() {
				a.ConnectByAddress()
			}
		})
		ui.Row(c.Key("conn-as")).FillWidth().Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, L.ConnectOpenAs()).FontSize(sizeSmall).TextColor(k.text3)
			ui.Segmented(c.Key("conn-as-seg"), &a.connect.OpenAs, L.KindDeX(), L.KindScreen(), L.KindApp(), L.KindCamera())
		})
		switch f.Phase {
		case connectLooking:
			ui.Row(c.Key("conn-looking")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(16, 16)
				ui.Text(c, L.ConnectLooking(f.Host)).FontSize(sizeBody).Grow(1).SingleLine()
			})
			if f.Progress > 0 {
				ui.Progress(c.Key("conn-progress"), f.Progress).FillWidth()
			}
		case connectNeedPair:
			a.pairForm(c, k, f)
		case connectNoAnswer:
			ui.Text(c.Key("conn-none"), L.ConnectNoAnswer(f.Host)).Bold().FontSize(sizeBody)
			ui.Text(c, L.ConnectNoAnswerHint()).FontSize(sizeSmall).TextColor(k.text2)
			ui.Row(c.Key("conn-manual")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, f.Host+" :").FontSize(sizeBody).TextColor(k.text3).Shrink(0)
				ui.TextInput(c.Key("conn-port"), &a.connect.ManualPort).Placeholder(L.ConnectPortPlaceholder()).Width(120)
				_, ok := validPort(f.ManualPort)
				if ui.Button(c.Key("conn-port-go"), L.ConnectGo()).Disabled(!ok).Clicked() {
					a.ConnectManualPort()
				}
				if ui.Button(c.Key("conn-retry"), L.ConnectRetry()).Clicked() {
					a.ConnectByAddress()
				}
			})
			if strings.TrimSpace(f.ManualPort) != "" {
				if _, ok := validPort(f.ManualPort); !ok {
					ui.Text(c, L.PortRangeError()).FontSize(sizeSmall).TextColor(k.badText)
				}
			}
		}
		if f.Notice != "" {
			ui.Text(c.Key("conn-notice"), f.Notice).FontSize(sizeSmall).TextColor(k.warnText)
		}
	})
}

// pairForm asks for the code and pairing port the phone shows under Wireless debugging › Pair
// device with pairing code.
func (a *App) pairForm(c *ui.Context, k tokens, f connectFlow) {
	ui.Text(c.Key("pair-title"), L.PairNotYet()).Bold().FontSize(sizeBody)
	ui.Text(c, L.PairSteps()).FontSize(sizeSmall).TextColor(k.text2)
	ui.Row(c.Key("pair-fields")).FillWidth().Gap(10).AlignItems(ui.End).Children(func() {
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, L.PairCodeLabel()).FontSize(sizeSmall).TextColor(k.text2)
			ui.TextInput(c.Key("pair-code"), &a.connect.PairCode).Placeholder("123456").FillWidth()
		})
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, L.PairPortLabel()).FontSize(sizeSmall).TextColor(k.text2)
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, f.Host+" :").FontSize(sizeBody).TextColor(k.text3).Shrink(0)
				ui.TextInput(c.Key("pair-port"), &a.connect.PairPort).Grow(1).MinWidth(0)
			})
		})
		code := strings.ReplaceAll(strings.TrimSpace(f.PairCode), " ", "")
		_, portOK := validPort(f.PairPort)
		if ui.PrimaryButton(c.Key("pair-go"), L.PairAndConnect()).Disabled(!validPairCode(code) || !portOK).Clicked() {
			a.PairAndConnect()
		}
	})
	if code := strings.ReplaceAll(strings.TrimSpace(f.PairCode), " ", ""); code != "" && !validPairCode(code) {
		ui.Text(c, L.PairCodeError()).FontSize(sizeSmall).TextColor(k.badText)
	}
}

// phonesSection renders Home's phone cards grid, optional Rename editor, empty state, and
// automatic "Found nearby or in your networks" section for unpaired Wi-Fi and mesh phones.
func (a *App) phonesSection(c *ui.Context, k tokens) {
	a.mu.Lock()
	devs := append([]DiscoveredDevice(nil), a.devices...)
	hideDiscovered, onlineOnly := a.prefs.HideOthersInNetworks, a.prefs.HomeOnlineOnly
	renaming := a.renaming
	notice := a.deviceNotice
	a.mu.Unlock()
	offline := a.offlineRemembered(devs)
	count := len(devs) + len(offline)

	a.usbAuthBanner(c, k, devs)

	if notice != "" {
		ui.Text(c.Key("phones-notice"), notice).FontSize(sizeSmall).TextColor(k.warnText)
	}
	if renaming != "" {
		a.renameEditor(c, k, renaming)
	}
	if count == 0 {
		t := c.Theme()
		ui.Column(c.Key("phones-empty")).FillWidth().Padding(28, 20).Gap(10).Radius(cardRadius).Background(t.Surface).Border(1, t.Border).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconDevices).Size(32, 32).TextColor(k.text3)
			ui.Text(c, L.HomeEmptyTitle()).Bold().FontSize(sizeHeading)
			ui.Text(c, L.HomeEmptySub()).FontSize(sizeSmall).TextColor(k.text2)
			if ui.PrimaryButton(c.Key("empty-add-btn"), "+ "+L.NavAddPhone()).Clicked() {
				a.OpenAddWizard(0)
			}
		})
	}
	// Two columns across Home, four when wide enough, one when narrow.
	w, _ := c.Size()
	inner := min(w-sidebarW, homeMaxW) - 56
	cols := 2
	switch {
	case inner < 2*devCardMinW+homeGap:
		cols = 1
	case w >= 1000 && inner >= 4*devCardMinW+3*homeGap:
		cols = 4
	}
	renderGrid := func(prefix string, cards []func()) {
		for row := 0; row*cols < len(cards); row++ {
			ui.Row(c.Key(prefix + strconv.Itoa(row))).FillWidth().Gap(homeGap).AlignItems(ui.Stretch).Children(func() {
				for i := row * cols; i < row*cols+cols; i++ {
					if i < len(cards) {
						cards[i]()
					} else {
						ui.Box(c).Grow(1).Basis(0)
					}
				}
			})
		}
	}

	cards := make([]func(), 0, count)
	for _, d := range devs {
		d := d
		cards = append(cards, func() { a.phoneCard(c, k, d) })
	}
	if !onlineOnly {
		for _, r := range offline {
			r := r
			cards = append(cards, func() { a.offlineCard(c, k, r) })
		}
	}
	if len(cards) > 0 {
		renderGrid("phones-row-", cards)
	}

	if !hideDiscovered {
		wifiPairs := a.unpairedPairMDNSServices()
		meshPeers := a.othersInNetworks()
		if len(wifiPairs)+len(meshPeers) > 0 {
			ui.Row(c.Key("found-head")).FillWidth().Padding(10, 0, 0, 0).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, L.HomeFoundNew()).Bold().FontSize(sizeBody).TextColor(k.text2)
				pill(c, "found-count", strconv.Itoa(len(wifiPairs)+len(meshPeers)), k.sunk, k.text2)
			})
			discovered := make([]func(), 0, len(wifiPairs)+len(meshPeers))
			for idx, svc := range wifiPairs {
				idx, svc := idx, svc
				discovered = append(discovered, func() { a.mdnsWifiCard(c, k, idx, svc) })
			}
			for _, p := range meshPeers {
				p := p
				discovered = append(discovered, func() { a.networkPhoneCard(c, k, p) })
			}
			renderGrid("found-row-", discovered)
		}
	}
}

// phoneShell is a phone card's frame.
func phoneShell(c *ui.Context, key string, dashed bool) ui.Element {
	t := c.Theme()
	k := tok(c)
	e := ui.Column(c.Key(key)).Grow(1).Basis(0).MinWidth(0).Padding(14).Gap(12).Radius(cardRadius)
	if dashed {
		return e.Border(1, k.text3.Alpha(0.5))
	}
	return e.Background(t.Surface).Border(1, t.Border)
}

// phoneHeader is a card's avatar, name, detail line, state pill and ⋯.
func phoneHeader(c *ui.Context, k tokens, key string, avatar func(), name, detail string, state func(), more func()) {
	ui.Row(c.Key(key)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		avatar()
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, name).Bold().FontSize(sizeHeading).SingleLine()
			if detail != "" {
				ui.Text(c, detail).FontSize(sizeSmall).TextColor(k.text3).SingleLine()
			}
		})
		if state != nil {
			state()
		}
		if more != nil {
			more()
		}
	})
}

// genuineDeX reports whether d offers real Samsung DeX (`INV-06`).
func genuineDeX(d DiscoveredDevice) bool {
	r := d.Route
	return r.Backend == dex.BackendADBLoopback && r.IsGenuineDeX && !isEmulator(d)
}

// routeLine is how an online device is connected, in words ("USB · switches to Wi-Fi when
// unplugged", "Wi-Fi", "Through the Companion").
func (a *App) routeLine(d DiscoveredDevice) (string, bool) {
	usb, wifi, tunnel := false, false, false
	for _, tr := range d.Transports {
		if tr.State != "device" {
			continue
		}
		switch tr.Transport {
		case adb.TransportUSB:
			usb = true
		case adb.TransportTLSWiFi, adb.TransportTCP:
			if strings.HasPrefix(tr.Serial, "127.0.0.1:") {
				tunnel = true
			} else {
				wifi = true
			}
		}
	}
	switch {
	case isEmulator(d):
		return L.RouteThisComputer(), false
	case usb && (wifi || a.hasWirelessMemory(d.HardwareHash)):
		return L.RouteUSBWithWiFi(), false
	case usb:
		return L.RouteUSB(), false
	case tunnel:
		return L.RouteCompanion(), true
	case wifi:
		return L.RouteWiFi(), false
	}
	return adbStateWords(d.Target.State), true
}

// adbStateWords says what adb's state for a phone means for the person, never the raw word.
func adbStateWords(state string) string {
	switch state {
	case "unauthorized":
		return L.StateAllowDebugging()
	case "authorizing", "connecting":
		return L.NetSummaryConnecting()
	case "offline":
		return L.StateNotResponding()
	}
	return L.StateUnavailable()
}

// hasWirelessMemory reports whether hash has a remembered Wireless debugging address.
func (a *App) hasWirelessMemory(hash string) bool {
	r, ok := a.RememberedFor(hash)
	return ok && r.Endpoint != ""
}

// phoneCard is one online phone: header, route, live figures from the Companion, and its actions —
// the most likely kind as the primary split button, the other kinds and Send file as icons, the
// rest in ⋯.
func (a *App) phoneCard(c *ui.Context, k tokens, d DiscoveredDevice) {
	hash := d.HardwareHash
	if hash == "" {
		hash = d.Target.DeviceHash
	}
	key := "phone-" + hash
	name := a.phoneName(d)
	online := d.Target.State == "device"
	phoneShell(c, key, false).Children(func() {
		phoneHeader(c, k, key+"-h", func() {
			glyph := (*ui.SVG)(nil)
			if isEmulator(d) {
				glyph = iconVM
			}
			phoneAvatar(c, avatarSize, deviceLogo(d), glyph, name)
		}, name, phoneDetail(name, deviceModel(d), d.Profile.AndroidRelease), func() {
			switch {
			case online:
				pill(c, key+"-state", L.StateConnected(), k.good.Alpha(0.14), k.goodText)
			case isEmulator(d):
				pill(c, key+"-state", L.StateStarting(), k.warn.Alpha(0.16), k.warnText)
			default:
				pill(c, key+"-state", L.StateUnavailable(), k.warn.Alpha(0.16), k.warnText)
			}
		}, func() { a.phoneMenu(c, key+"-more", d) })

		route, warn := a.routeLine(d)
		ui.Row(c.Key(key + "-route")).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			if isEmulator(d) && !online {
				// Booting: adb lists it, Android is not up yet.
				ui.Spinner(c).Size(12, 12)
				ui.Text(c, L.VMBootingAndroid()).FontSize(sizeSmall).TextColor(k.text2).SingleLine().MinWidth(0)
				return
			}
			col := k.good
			if warn {
				col = k.warn
			}
			statusDot(c, col)
			ui.Text(c, route).FontSize(sizeSmall).TextColor(k.text2).SingleLine().MinWidth(0)
		})
		busy := a.cardWorkLine(c, k, key, hash)
		if !online {
			return
		}
		a.phoneFigures(c, k, key, hash)
		if r, ok := a.RememberedFor(hash); ok && !isEmulator(d) {
			if r.Companion == nil && !a.firstConnectShowingFor(hash) {
				pairAssistBanner(c, k, key+"-pair-enh", iconSpark, L.CardSuggestCompanion(), L.ActionPairEnhancedShort(), busy, func() {
					a.PairCompanionOverADB(d.Target.Serial, a.phoneName(d), a.pairNetworkName() != "")
				})
			} else if r.Companion != nil && a.allowsWirelessADB() && !a.hasWirelessADBPaired(hash, &d) {
				pairAssistBanner(c, k, key+"-pair-adb", iconWifi, L.CardSuggestWirelessADB(), L.ActionPairWirelessADBShort(), busy, func() {
					a.OpenPairADBModal(hash)
				})
			}
		}
		a.phoneActions(c, k, key, d)
	})
}

func (a *App) firstConnectShowingFor(hash string) bool {
	if hash == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.firstConnect != nil && a.firstConnect.Hash == hash
}

// hasWirelessADBPaired reports whether hash currently has an active wireless ADB transport or a
// saved non-stale wireless endpoint.
func (a *App) hasWirelessADBPaired(hash string, d *DiscoveredDevice) bool {
	if hash == "" || a.isADBAuthNeeded(hash) {
		return false
	}
	if r, ok := a.RememberedFor(hash); ok && r.Endpoint != "" {
		return true
	}
	if d != nil {
		for _, tr := range d.Transports {
			if tr.State == "device" && (tr.Transport == adb.TransportTLSWiFi || tr.Transport == adb.TransportTCP) {
				return true
			}
		}
	}
	return false
}

// pairAssistBanner renders a subtle inline reminder inside a phone card when only one of Enhanced
// mode or Wireless debugging (ADB) is paired yet.
func pairAssistBanner(c *ui.Context, k tokens, key string, icon *ui.SVG, text, btnLabel string, disabled bool, onClick func()) {
	ui.Row(c.Key(key)).FillWidth().Padding(8, 10).Gap(8).Radius(radiusMD).Background(k.brandSoft).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon).Size(15, 15).TextColor(k.brandSoftText).Shrink(0)
		ui.Text(c, text).FontSize(sizeSmall).TextColor(k.text2).Grow(1).MinWidth(0)
		if ui.Button(c.Key(key+"-btn"), btnLabel).Disabled(disabled).Clicked() {
			onClick()
		}
	})
}

// phoneFigures shows the Companion's live status for hash when it is the connected Companion:
// battery, temperature, network and the app in front.
func (a *App) phoneFigures(c *ui.Context, k tokens, key, hash string) {
	st, ok := a.peerStatusFor(hash)
	if !ok {
		return
	}
	// fig is one figure; frac < 0 draws no bar (unknown values, and words such as the network).
	fig := func(fkey, label, value string, frac float32, col ui.Color) {
		ui.Column(c.Key(fkey)).Grow(1).Basis(0).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, label).FontSize(sizeSmall).TextColor(k.text3).SingleLine()
			ui.Text(c, value).Bold().FontSize(sizeBody).FontFeatures("tnum").SingleLine()
			if frac >= 0 {
				ui.Box(c).FillWidth().Height(3).Radius(2).Background(k.hairline).Children(func() {
					ui.Box(c).WidthPercent(min(100, frac*100)).FillHeight().Radius(2).Background(col)
				})
			}
		})
	}
	ui.Row(c.Key(key + "-figs")).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
		batt, battWord, battFrac := k.good, "—", float32(-1)
		if st.BatteryPct >= 0 {
			battWord, battFrac = fmt.Sprintf("%d%%", st.BatteryPct), float32(st.BatteryPct)/100
			if st.Charging {
				battWord += " · " + L.FigCharging()
			} else if st.BatteryPct <= 15 {
				batt = k.warn
			}
		}
		fig(key+"-batt", L.FigBattery(), battWord, battFrac, batt)
		tempWord, tempFrac, tempCol := "—", float32(-1), ui.Hex("#E8833A")
		if st.TempC > 0 {
			tempWord, tempFrac = fmt.Sprintf("%.0f°C", st.TempC), float32(st.TempC)/60
			if st.ThermalWarning {
				tempCol = k.warn
			}
		}
		fig(key+"-temp", L.FigTemperature(), tempWord, tempFrac, tempCol)
		fig(key+"-net", L.FigNetwork(), networkWord(st.NetworkType), -1, k.good)
	})
}

// networkWord names a Companion network type.
func networkWord(t string) string {
	switch t {
	case "wifi":
		return "Wi-Fi"
	case "cellular":
		return L.NetCellular()
	case "ethernet":
		return L.NetEthernet()
	case "vpn":
		return "VPN"
	case "", "none":
		return L.NetOffline()
	}
	return t
}

// phoneActions are a card's action buttons.
func (a *App) phoneActions(c *ui.Context, k tokens, key string, d DiscoveredDevice) {
	dexOK := genuineDeX(d)
	ui.Row(c.Key(key + "-acts")).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
		main, launch := L.KindScreen(), func(open OpenIn) { a.LaunchMirror(d, open) }
		if dexOK {
			main, launch = L.KindDeX(), func(open OpenIn) { a.LaunchDeX(d, false, open) }
		}
		sb := ui.PrimarySplitButton(c.Key(key+"-main"), main)
		sb.Arrow.PopoverMenuButton(func(m *ui.Menu) {
			if m.Item(L.OpenInPane()).Chosen() {
				launch(OpenPane)
			}
			if m.Item(L.OpenBeside()).Chosen() {
				launch(OpenSplit)
			}
			if m.Item(L.OpenInWindow()).Chosen() {
				launch(OpenWindow)
			}
		}, ui.PopoverMenuButtonOptions{At: ui.AnchorBottomStart, Self: ui.AnchorTopStart})
		if sb.Button.Clicked() {
			launch(OpenDefault)
		}
		if dexOK && iconBtn(c, key+"-screen", iconMirror, L.KindScreen(), false).Clicked() {
			a.LaunchMirror(d, OpenDefault)
		}
		if len(d.Apps) > 0 && iconBtn(c, key+"-apps", iconApps, L.KindApp(), false).Clicked() {
			a.openPicker(openAsApp, d.HardwareHash)
		}
		if !isEmulator(d) && iconBtn(c, key+"-cam", iconCamera, L.KindCamera(), false).Clicked() {
			a.LaunchPhoneCamera(d, a.cameraFacing(), OpenDefault)
		}
		if a.compHashConnected(d.HardwareHash) && iconBtn(c, key+"-send", iconSend, L.ActionSendFile(), false).Clicked() {
			a.SendFileToPhone()
		}
	})
}

// phoneMenu is a card's ⋯ menu.
func (a *App) phoneMenu(c *ui.Context, key string, d DiscoveredDevice) {
	hash := d.HardwareHash
	_, remembered := a.RememberedFor(hash)
	moreBtn := iconBtn(c, key, iconMore, L.PaneMore(), false)
	moreBtn.PopoverMenuButton(func(m *ui.Menu) {
		if m.Item(L.KindDeXFallback()).Chosen() {
			a.LaunchDeX(d, true, OpenDefault)
		}
		if len(d.Apps) > 0 && m.Item(L.KindAppMenu()).Chosen() {
			a.openPicker(openAsApp, hash)
		}
		m.Separator()
		if a.compHashConnected(hash) {
			if m.Item(L.ActionSendClipboard()).Chosen() {
				a.SendClipboardToPhone(c.ReadClipboard())
			}
			if m.Item(L.ActionSendFileMenu()).Chosen() {
				a.SendFileToPhone()
			}
		}
		if !isEmulator(d) {
			a.mu.Lock()
			inst := a.compInstalled[hash]
			a.mu.Unlock()
			r, _ := a.RememberedFor(hash)
			if inst.Installed && !inst.Current && m.Item(L.ActionUpdateCompanion()).Chosen() {
				a.UpdateCompanion(d)
			}
			enhLabel := L.ActionMenuPairEnhanced()
			if r.Companion != nil {
				enhLabel = L.ActionMenuRepairEnhanced()
			}
			if m.Item(enhLabel).Chosen() {
				a.PairCompanionOverADB(d.Target.Serial, a.phoneName(d), a.pairNetworkName() != "")
			}
			adbLabel := L.ActionMenuPairADB()
			if a.hasWirelessADBPaired(hash, &d) {
				adbLabel = L.ActionMenuRepairADB()
			}
			if m.Item(adbLabel).Chosen() {
				if remembered {
					a.OpenPairADBModal(hash)
				} else {
					a.OpenAddWizard(1)
				}
			}
		}
		m.Separator()
		if remembered && m.Item(L.ActionRename()).Chosen() {
			a.startRename(hash)
		}
		if r, ok := a.RememberedFor(hash); ok {
			if m.Item(L.RememberedAuto()).Checked(r.AutoReconnect).Chosen() {
				a.SetAutoReconnect(hash, !r.AutoReconnect)
			}
		}
		if route := d.Route; route.CanUseNativeDeX && !isEmulator(d) {
			pref := a.dexRoutePref(hash)
			m.Submenu(L.DeXRouteLabel(), func(m *ui.Menu) {
				for _, r := range []struct {
					p     dex.RoutePreference
					label string
				}{{dex.RouteAuto, L.DeXRouteAuto()}, {dex.RouteNativeDeX, L.DeXRouteNative()}, {dex.RouteVirtualFallback, L.DeXRouteFallback()}} {
					if m.Item(r.label).Radio(pref == r.p).Chosen() {
						a.setDexRoutePref(hash, r.p)
					}
				}
			})
		}
		for _, tr := range d.Transports {
			if tr.State == "device" && (tr.Transport == adb.TransportTLSWiFi || tr.Transport == adb.TransportTCP) {
				if m.Item(L.ActionDisconnectWireless()).Chosen() {
					a.DisconnectWireless(tr.Serial)
				}
				break
			}
		}
		if remembered {
			m.Separator()
			if m.Item(L.RememberedForgetPhone()).Chosen() {
				if gone, ok := a.ForgetDevice(hash); ok {
					c.ToastAction(L.RememberedForgotten(gone.displayName()), L.ActionUndo(), func() { a.RestoreDevice(gone) })
				}
			}
		}
	})
}

// isSamsungRemembered reports whether a remembered phone is a Samsung / Galaxy device.
func isSamsungRemembered(r RememberedDevice) bool {
	for _, s := range []string{r.Maker, r.Brand, r.Model, r.Name} {
		low := strings.ToLower(s)
		if strings.Contains(low, "samsung") || strings.Contains(low, "galaxy") {
			return true
		}
	}
	return false
}

// offlineCard is a remembered phone that is not connected: when it was last seen, Connect, and ⋯.
func (a *App) offlineCard(c *ui.Context, k tokens, r RememberedDevice) {
	key := "off-" + r.HardwareHash
	name := r.displayName()
	way, inNet := a.networkWay(r)
	isSamsung := isSamsungRemembered(r)
	cachedApps := a.appsForDevice(r.HardwareHash)
	st, hasSt := a.peerStatusFor(r.HardwareHash)
	daemonAppsOK := hasSt && st.DaemonReady && st.DaemonTrustedDisplay
	hasApps := r.Companion != nil && (len(cachedApps) > 0 || daemonAppsOK)
	phoneShell(c, key, false).Children(func() {
		phoneHeader(c, k, key+"-h", func() {
			phoneAvatar(c, avatarSize, phoneLogo(r.Maker, r.Brand, r.Model, r.Name), nil, name)
		}, name, phoneDetail(name, r.Name, r.Android), func() {
			if inNet {
				pill(c, key+"-state", L.StateInNetwork(), k.good.Alpha(0.14), k.goodText)
			} else {
				pill(c, key+"-state", L.StateOffline(), k.sunk, k.text2)
			}
		}, func() {
			iconBtn(c, key+"-more", iconMore, L.PaneMore(), false).PopoverMenuButton(func(m *ui.Menu) {
				if r.Companion != nil {
					dexLabel := L.KindDeXFallback()
					if isSamsung {
						dexLabel = L.KindDeX()
					}
					if m.Item(dexLabel).Chosen() {
						if daemonAppsOK {
							a.LaunchDaemonDesktop(r.HardwareHash, OpenDefault)
						} else {
							a.EnsureEnhancedAndRun(r.HardwareHash, openAsDeX)
						}
					}
					if m.Item(L.ActionViewScreen()).Chosen() {
						a.LaunchCompanionMirror(r.HardwareHash, OpenDefault)
					}
					if m.Item(L.KindAppMenu()).Chosen() {
						if daemonAppsOK {
							if len(cachedApps) == 0 {
								go a.fetchApps(r.HardwareHash, false)
							}
							a.openPicker(openAsApp, r.HardwareHash)
						} else {
							a.EnsureEnhancedAndRun(r.HardwareHash, openAsApp)
						}
					}
					m.Separator()
				}
				if r.Companion == nil {
					if m.Item(L.ActionMenuPairEnhanced()).Chosen() {
						if p, ok := a.unpairedMeshPhoneByQuery(r.Name); ok {
							a.PairMeshPhone(p)
						} else if len(a.networks()) > 0 {
							a.OpenAddWizard(2)
						} else {
							a.OpenAddWizard(0)
						}
					}
				} else {
					if !daemonAppsOK && m.Item(L.ActionActivateEnhanced()).Chosen() {
						a.OpenActivateModal(r.HardwareHash, 0)
					}
					if m.Item(L.ActionMenuRepairEnhanced()).Chosen() {
						a.RepairMeshCompanion(r)
					}
				}
				adbLabel := L.ActionMenuPairADB()
				if a.hasWirelessADBPaired(r.HardwareHash, nil) {
					adbLabel = L.ActionMenuRepairADB()
				}
				if m.Item(adbLabel).Chosen() {
					if r.Companion != nil {
						a.OpenPairADBModal(r.HardwareHash)
					} else {
						a.OpenAddWizard(1)
					}
				}
				m.Separator()
				if m.Item(L.ActionRename()).Chosen() {
					a.startRename(r.HardwareHash)
				}
				if m.Item(L.RememberedAuto()).Checked(r.AutoReconnect).Chosen() {
					a.SetAutoReconnect(r.HardwareHash, !r.AutoReconnect)
				}
				m.Separator()
				if m.Item(L.RememberedForgetPhone()).Chosen() {
					if gone, ok := a.ForgetDevice(r.HardwareHash); ok {
						c.ToastAction(L.RememberedForgotten(gone.displayName()), L.ActionUndo(), func() { a.RestoreDevice(gone) })
					}
				}
			})
		})
		ui.Row(c.Key(key + "-route")).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			if inNet {
				statusDot(c, k.good)
				ui.Text(c, way).FontSize(sizeSmall).TextColor(k.text2).SingleLine().MinWidth(0)
				return
			}
			ui.Box(c).Size(8, 8).Radius(4).Border(1.5, k.text3).Shrink(0)
			ui.Text(c, L.LastConnected(lastSeenWords(r.LastSeen))).FontSize(sizeSmall).TextColor(k.text2).SingleLine().MinWidth(0)
		})
		if inNet {
			a.phoneFigures(c, k, key, r.HardwareHash)
			if r.Companion != nil {
				switch {
				case daemonAppsOK:
					ui.Text(c, L.CardEnhancedReady()).FontSize(sizeSmall).TextColor(k.goodText)
				case hasSt && st.NetworkType == "wifi" && (st.WirelessDebugOn || st.WirelessADBPort > 0):
					ui.Text(c, L.CardEnhancedWifiHint()).FontSize(sizeSmall).TextColor(k.text2)
				default:
					ui.Text(c, L.CardEnhancedNeedsSetup()).FontSize(sizeSmall).TextColor(k.text3)
				}
			}
		} else if r.AutoReconnect {
			ui.Text(c, L.OfflineAutoHint()).FontSize(sizeSmall).TextColor(k.text3)
		}
		busy := a.cardWorkLine(c, k, key, r.HardwareHash)
		if r.Companion != nil && inNet && a.allowsWirelessADB() && !a.hasWirelessADBPaired(r.HardwareHash, nil) {
			pairAssistBanner(c, k, key+"-pair-adb", iconWifi, L.CardSuggestWirelessADB(), L.ActionPairWirelessADBShort(), busy, func() {
				a.OpenPairADBModal(r.HardwareHash)
			})
		} else if r.Companion == nil {
			pairAssistBanner(c, k, key+"-pair-enh", iconSpark, L.CardOfflineSuggestCompanion(), L.ActionPairEnhancedShort(), busy, func() {
				if p, ok := a.unpairedMeshPhoneByQuery(r.Name); ok {
					a.PairMeshPhone(p)
				} else {
					a.OpenAddWizard(0)
				}
			})
		}
		ui.Box(c).Grow(1)
		ui.Row(c.Key(key + "-acts")).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
			// In a network without adb, expose actions tailored to Enhanced mode readiness:
			// active daemon gives DeX/Desktop + Apps + View screen; before activation, View screen
			// is primary and DeX / Apps trigger guided one-time ADB bootstrap (`EnsureEnhancedAndRun`).
			if inNet && r.Companion != nil {
				dexLabel := L.KindDeXFallback()
				if isSamsung {
					dexLabel = L.KindDeX()
				}
				if daemonAppsOK {
					if isSamsung {
						sb := ui.PrimarySplitButton(c.Key(key+"-ddex-main"), L.KindDeX())
						sb.Arrow.PopoverMenuButton(func(m *ui.Menu) {
							if m.Item(L.OpenInPane()).Chosen() {
								a.LaunchDaemonDesktop(r.HardwareHash, OpenPane)
							}
							if m.Item(L.OpenBeside()).Chosen() {
								a.LaunchDaemonDesktop(r.HardwareHash, OpenSplit)
							}
							if m.Item(L.OpenInWindow()).Chosen() {
								a.LaunchDaemonDesktop(r.HardwareHash, OpenWindow)
							}
						}, ui.PopoverMenuButtonOptions{At: ui.AnchorBottomStart, Self: ui.AnchorTopStart})
						if sb.Button.Disabled(busy).Clicked() {
							a.LaunchDaemonDesktop(r.HardwareHash, OpenDefault)
						}
						if iconBtn(c, key+"-share-icon", iconMirror, L.ActionViewScreen(), false).Disabled(busy).Clicked() {
							a.LaunchCompanionMirror(r.HardwareHash, OpenDefault)
						}
					} else {
						if ui.PrimaryButton(c.Key(key+"-share-main"), L.ActionViewScreen()).Disabled(busy).Clicked() {
							a.LaunchCompanionMirror(r.HardwareHash, OpenDefault)
						}
						if iconBtn(c, key+"-ddex", iconDex, L.KindDeXFallback(), false).Disabled(busy).Clicked() {
							a.LaunchDaemonDesktop(r.HardwareHash, OpenDefault)
						}
					}
					if hasApps && iconBtn(c, key+"-apps", iconApps, L.KindApp(), false).Disabled(busy).Clicked() {
						if len(cachedApps) == 0 {
							go a.fetchApps(r.HardwareHash, false)
						}
						a.openPicker(openAsApp, r.HardwareHash)
					}
				} else {
					if ui.PrimaryButton(c.Key(key+"-share-main"), L.ActionViewScreen()).Disabled(busy).Clicked() {
						a.LaunchCompanionMirror(r.HardwareHash, OpenDefault)
					}
					if ui.Button(c.Key(key+"-ddex"), dexLabel).Disabled(busy).Clicked() {
						a.EnsureEnhancedAndRun(r.HardwareHash, openAsDeX)
					}
					if iconBtn(c, key+"-apps", iconApps, L.KindApp(), false).Disabled(busy).Clicked() {
						a.EnsureEnhancedAndRun(r.HardwareHash, openAsApp)
					}
				}
			}
			if !inNet {
				if ui.Button(c.Key(key+"-connect"), L.ConnectGo()).Disabled(busy).Clicked() {
					a.mu.Lock()
					openAs := a.connect.OpenAs
					a.mu.Unlock()
					a.connectRemembered(r, openAs)
				}
				if r.Companion != nil && ui.Button(c.Key(key+"-share"), L.ActionViewScreen()).Disabled(busy).Clicked() {
					a.LaunchCompanionMirror(r.HardwareHash, OpenDefault)
				}
			}
		})
	})
}

// lastSeenWords is a last-seen time as the interface writes it.
func lastSeenWords(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("01-02 15:04")
}

// networkPhone is a phone in a joined network that this computer never connected to.
type networkPhone struct {
	NetworkID string // local network ID (`MeshNetwork.ID`)
	Network   string // the network's shown name
	Peer      string // EasyTier hostname, the name it joined under (mesh.Hostname)
	Name      string // the name the phone gives itself in that hostname
	IPv4      string
	Direct    bool
	Latency   float64
}

// othersInNetworks lists the AndroFuse phones in the running networks that are no remembered
// phone, each once. Other nodes, such as public relay servers, and computers are left out (like
// Kyokai, only hostnames with mesh.HostPrefix count).
func (a *App) othersInNetworks() []networkPhone {
	var remembered []string
	for _, r := range a.rememberedList() {
		if r.Companion != nil && r.Companion.MeshHost != "" {
			remembered = append(remembered, r.Companion.MeshHost)
		}
	}
	known := map[string]bool{}
	nets := map[string]string{}
	for _, n := range a.networks() {
		nets[n.ID] = n.shownName()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []networkPhone
	for id, v := range a.netViews {
		for _, p := range v.Info.Peers {
			kind, devID, name, ok := mesh.ParseHostname(p.Hostname)
			if !ok || kind != mesh.KindPhone || known[devID] || slices.ContainsFunc(remembered, func(h string) bool { return mesh.SameDevice(h, p.Hostname) }) {
				continue
			}
			known[devID] = true
			if name == "" {
				name = devID
			}
			out = append(out, networkPhone{NetworkID: id, Network: nets[id], Peer: p.Hostname, Name: name, IPv4: p.IPv4, Direct: p.Direct, Latency: p.Latency})
		}
	}
	return out
}

// routeWords is a peer's mesh address and route in words, "10.126.126.12 · Direct · 3 ms"; the address
// is left out while it has none.
func routeWords(ip string, direct bool, ms float64) string {
	if ip == "" {
		return wayWords(direct, ms)
	}
	return ip + " · " + wayWords(direct, ms)
}

// wayWords is a network route in words: "Direct · 12 ms" or "Relayed · 48 ms".
func wayWords(direct bool, ms float64) string {
	w := L.WayRelayed()
	if direct {
		w = L.WayDirect()
	}
	if ms > 0 {
		return fmt.Sprintf("%s · %.0f ms", w, ms)
	}
	return w
}

// mdnsWifiCard shows an unpaired phone broadcasting Wireless debugging pairing on the same Wi-Fi.
func (a *App) mdnsWifiCard(c *ui.Context, k tokens, idx int, svc adb.MDNSService) {
	key := fmt.Sprintf("mdns-wifi-%d", idx)
	phoneShell(c, key, true).Children(func() {
		phoneHeader(c, k, key+"-h", func() {
			ui.Box(c).Size(avatarSize, avatarSize).Radius(radiusMD).Background(k.brandSoft).Center().Children(func() {
				ui.Icon(c, iconWifi).Size(18, 18).TextColor(k.brandSoftText)
			})
		}, L.HomeDiscoveredSameWifi(), L.HomeDiscoveredWifiHint(), func() {
			pill(c, key+"-st", L.ActionPairNow(), k.brandSoft, k.brandSoftText)
		}, nil)
		ui.Box(c).Grow(1)
		ui.Row(c.Key(key + "-acts")).Children(func() {
			if ui.PrimaryButton(c.Key(key+"-pair"), L.ActionPairNow()).Clicked() {
				a.OpenAddWizard(1)
			}
		})
	})
}

// networkPhoneCard is a dashed card for an unpaired phone discovered in a joined network.
// Clicking Pair & connect opens cross-network Companion pairing without exposing raw mesh IPs (`INV-08`).
func (a *App) networkPhoneCard(c *ui.Context, k tokens, p networkPhone) {
	_, devID, _, _ := mesh.ParseHostname(p.Peer)
	key := "netp-" + p.Peer
	phoneShell(c, key, true).Children(func() {
		phoneHeader(c, k, key+"-h", func() {
			phoneAvatar(c, avatarSize, phoneLogo(p.Name), nil, p.Name)
		}, p.Name, L.InNetworkNever(p.Network), nil, nil)
		ui.Row(c.Key(key + "-route")).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			col := k.warn
			if p.Direct {
				col = k.good
			}
			statusDot(c, col)
			ui.Text(c, wayWords(p.Direct, p.Latency)).FontSize(sizeSmall).TextColor(k.text2).SingleLine()
		})
		ui.Text(c, L.NetworkPairCardHint()).FontSize(sizeSmall).TextColor(k.text3)
		busy := false
		if devID != "" {
			busy = a.cardWorkLine(c, k, key, "net:"+devID)
		}
		ui.Box(c).Grow(1)
		ui.Row(c.Key(key + "-acts")).Children(func() {
			if ui.PrimaryButton(c.Key(key+"-pair"), L.NetworkPairConnect()).Disabled(busy).Clicked() {
				a.PairMeshPhone(p)
			}
		})
	})
}

// startRename opens the Rename editor for the remembered phone hash.
func (a *App) startRename(hash string) {
	r, ok := a.RememberedFor(hash)
	if !ok {
		return
	}
	a.update(func() {
		a.renaming = hash
		a.renameText = r.displayName()
	})
}

// renameEditor is Kyokai's Rename card above the phones: the avatar, the field (Enter saves,
// Escape leaves it; empty goes back to the phone's own name), Cancel and Save.
func (a *App) renameEditor(c *ui.Context, k tokens, hash string) {
	r, ok := a.RememberedFor(hash)
	if !ok {
		return
	}
	t := c.Theme()
	done := func(save bool) {
		a.mu.Lock()
		text := a.renameText
		a.renaming = ""
		a.mu.Unlock()
		if save {
			a.SetAlias(hash, text)
			a.refreshTray()
		}
	}
	ui.Row(c.Key("rename-card")).FillWidth().Padding(14).Gap(12).Radius(cardRadius).AlignItems(ui.Center).
		Background(t.Surface).Border(1, k.brand).OnShortcut(0, ui.KeyEscape, func() { done(false) }).Children(func() {
		phoneAvatar(c, avatarLarge, phoneLogo(r.Maker, r.Brand, r.Model, r.Name), nil, r.displayName())
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, L.RenameLabel()).FontSize(sizeSmall).TextColor(k.text2)
			in := ui.TextInput(c.Key("rename-input"), &a.renameText).FillWidth().AutoFocus()
			if in.Submitted() {
				done(true)
			}
			ui.Text(c, L.RenameHint(r.Name)).FontSize(sizeSmall).TextColor(k.text3)
		})
		if ui.Button(c.Key("rename-cancel"), L.ActionCancel()).Clicked() {
			done(false)
		}
		if ui.PrimaryButton(c.Key("rename-save"), L.ActionSave()).Clicked() {
			done(true)
		}
	})
}

// phoneMark returns the avatar parts of the device hash: its maker's mark, the VM glyph for an
// emulator, and its name.
func (a *App) phoneMark(hash string) (*ui.SVG, *ui.SVG, string) {
	a.mu.Lock()
	devs := append([]DiscoveredDevice(nil), a.devices...)
	a.mu.Unlock()
	for _, d := range devs {
		if d.HardwareHash == hash || d.Target.DeviceHash == hash {
			if isEmulator(d) {
				return nil, iconVM, a.phoneName(d)
			}
			return deviceLogo(d), nil, a.phoneName(d)
		}
	}
	if r, ok := a.RememberedFor(hash); ok {
		return phoneLogo(r.Maker, r.Brand, r.Model, r.Name), nil, r.displayName()
	}
	return nil, iconDevices, "Android"
}

// sessionPhone is the name of the phone a session shows.
func (a *App) sessionPhone(s *SessionWindow) string {
	_, _, name := a.phoneMark(s.spec.DeviceKey)
	return name
}

// compHashConnected reports whether the connected Companion belongs to the phone hash.
func (a *App) compHashConnected(hash string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.compConnected && a.compHash == hash && hash != ""
}

// cameraFacing is the default phone camera (Settings › Camera and microphone).
func (a *App) cameraFacing() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.prefs.FrontCamera {
		return "front"
	}
	return "back"
}

var _ = companion.LocalSocket

// cardWork is what a phone card shows about the last thing started from it: Busy while it runs
// (the card's buttons wait), then Note when it failed, in plain words.
type cardWork struct {
	Busy, Note string
}

// setCardWork sets what hash's card shows; an empty cardWork clears it.
func (a *App) setCardWork(hash string, w cardWork) {
	a.update(func() {
		if a.cardWork == nil {
			a.cardWork = map[string]cardWork{}
		}
		if w == (cardWork{}) {
			delete(a.cardWork, hash)
			return
		}
		a.cardWork[hash] = w
	})
}

// cardWorkLine draws hash's card progress (a spinner and what it is doing) or its last failure.
// Returns: true while something runs, so the card's buttons wait.
func (a *App) cardWorkLine(c *ui.Context, k tokens, key, hash string) bool {
	a.mu.Lock()
	w := a.cardWork[hash]
	a.mu.Unlock()
	switch {
	case w.Busy != "":
		ui.Row(c.Key(key + "-work")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(14, 14)
			ui.Text(c, w.Busy).FontSize(sizeSmall).TextColor(k.text2).MinWidth(0)
		})
		return true
	case w.Note != "":
		ui.Text(c.Key(key+"-note"), w.Note).FontSize(sizeSmall).TextColor(k.warnText)
	}
	return false
}

// networkWay reports whether remembered phone r is in a running network now (paired, and its
// mesh hostname among that network's peers) and how it is reached there, in words.
func (a *App) networkWay(r RememberedDevice) (string, bool) {
	if r.Companion == nil || r.Companion.MeshHost == "" {
		return "", false
	}
	names := map[string]string{}
	for _, n := range a.networks() {
		names[n.ID] = n.shownName()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, v := range a.netViews {
		if !v.Status.Running {
			continue
		}
		for _, p := range v.Info.Peers {
			if mesh.SameDevice(p.Hostname, r.Companion.MeshHost) {
				return L.RouteInNetwork(names[id]) + " · " + wayWords(p.Direct, p.Latency), true
			}
		}
	}
	return "", false
}
