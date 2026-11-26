# AndroFuse UI Redesign (V3 HTML Drafts)

Standalone HTML drafts for reviewing the next AndroFuse interface before the native MyGo views,
the ARB catalogs and the docs change. Open [`index.html`](index.html) in a browser (`file://`
works). [`common.css`](common.css) holds the tokens and components, [`common.js`](common.js) the
icon set, device avatars and small state demos, and [`logos.js`](logos.js) the vendor marks.
Every page has a light/dark toggle; [`Settings.html`](Settings.html) switches pages from its left
column (or `Settings.html#security`, `#about`, …).

V3 replaces V2.1. It follows **Kyokai**'s structure and logic (`Kyokai/doc/en-us/gui.md`): a
sidebar on the window's material with floating panes, the + picker, Settings as a sheet over the
window, rows made of an icon tile, a title, one sentence and a control, and device cards with live
status. Everything drawn here was a **proposal**; what is built is listed under "What is new behind these
screens", and nothing may be described as supported before it is verified on real hardware.

## Screens

| Screen | File | Size |
|---|---|---|
| Overview and feedback map | [`index.html`](index.html) | responsive |
| Home: this computer, connect by address, phone cards; 820×600 dark with Rename and the card menu | [`Home.html`](Home.html) | 1280×800, 820×600 |
| + picker, address-only connect states, pairing, first USB connection | [`Connect.html`](Connect.html) | 1280×800, cards |
| Split panes (DeX + app window), telemetry popover, decision card; Companion screen sharing on mobile data (dark) | [`Panes.html`](Panes.html) | 1280×800 |
| Settings sheet: Network, General, Phones and picture, Camera and microphone, Android on this PC, Security and privacy, About and updates | [`Settings.html`](Settings.html) | 1280×800 |
| Tray menu with submenus, notification reply | [`Tray.html`](Tray.html) | 1280×720 |
| Search (Ctrl K) | [`Search.html`](Search.html) | 1280×800 |
| Companion on the phone: Home (mobile data, Wi-Fi dark), Permissions, Security, pairing and screen-sharing prompts, Fold unfolded | [`Phone.html`](Phone.html) | 390×820, 860×760 |

## Shell (from Kyokai)

- **Main window.** The sidebar sits on the window's material (Mica); panes are boxes floating 8 px
  from the window's edges and from each other, with a 4 px corner where they meet the window's.
  Home and every session are panes. Windows' own buttons are hidden and drawn at the end of the
  top-right pane's header, after a separator.
- **Sidebar.** Top: hide the sidebar, and + (the picker). Home with AndroFuse's mark. "Open" with
  the count: a pane alone is a row (the phone's avatar or the kind's icon, the phone's name, the
  kind), a split group is a card titled with the phone's name over its panes' rows; then "Separate
  windows". Bottom: the network state as a dot and words (opens Settings › Network), search,
  appearance (system, light, dark) and Settings. Below 960 px it folds to a 64 px rail.
- **One header per pane (44 px).** Left: icon or avatar, the phone's name and a second line
  (kind · connection). Middle: Back, Home, Recents, then Screenshot, Send clipboard, Fullscreen, and
  ⋯ for the rest (record, sound, screen off, rotate). Then the telemetry chip (measured fps with a
  status dot) and the pane buttons: open in a separate window, split right, split down, close. A
  pane narrower than 560 px keeps only its name, ⋯ and close. **No second title bar above the pane,
  and no telemetry bar over the picture** (both caused the overlaps in the current build).
- **Telemetry popover** (INV-05): the measured fps large, the target muted beside it ("target 60,
  not guaranteed"), then the size the phone actually sends, decoder and time per frame, bitrate,
  connection and backend ("Samsung DeX (the phone's own desktop)" vs "large-screen desktop (not
  Samsung DeX)", INV-06).
- **Cards above the panes** are only for decisions: an external assistant's request (Deny / Allow
  once), a pairing to confirm. Status news goes to a toast or a Windows notification.
- **Settings** is a sheet over the whole window (64 px from the sides, 48 from the top, 32 from the
  bottom, the window dimmed), closed by Esc, × or a click outside. Left: search, then Preferences
  (General), Phones (Phones and picture, Camera and microphone), This computer (Network, Android on
  this PC, Security and privacy), AndroFuse (About and updates). A page is a title, one sentence,
  then sections of rows; long explanations sit behind ⓘ.
- **Search (Ctrl K)** and the magnifier: groups Open, Phones, Apps, Actions, Settings, six results
  each; every word typed must match the title or the line under it; ↑ ↓, Enter, Esc. While a
  picture has the keyboard, Ctrl K goes to the phone.

## Home

- **This computer**: readiness as a pill; picture (hardware decoding), the network (name and phones
  online, with Network settings), and Android on this PC (Start).
- **Connect a phone**: one field for an IP address, a domain or a phone's name, and Connect; "Open
  as" Samsung DeX, Phone screen, App window or Camera. USB phones appear by themselves.
- **Phones**: one card per phone (never one per session), at least 300 px wide, one to three per
  row. Header: avatar, name, model · Android version, state pill, ⋯. Route line (opens the route
  menu): "USB · switches to Wi-Fi when unplugged", "Cross-network · relayed · 48 ms" in the warning
  colour, "Last connected 25 Nov 02:23 · Wi-Fi". Four live figures from the Companion (battery,
  temperature, free storage, network), each with a thin bar. Actions: the primary split button is
  the phone's most used kind (Samsung DeX only on Samsung phones that support it), then icons for
  the other kinds and Send file. Everything else is in ⋯: large-screen desktop (not Samsung DeX),
  App window…, Send clipboard, Send file…, Rename…, Routes…, Reconnect automatically (check),
  Companion…, Forget this phone (with Undo).
- **In your networks** (switch beside the title): phones in a joined network that this computer
  never connected to, as dashed cards with the network, the way (direct or relayed) and latency,
  and Connect.

### Names and avatars (Kyokai `deviceAvatar`)

- A phone shows **the name given on this computer**, else its marketing name (`ro.product.marketname`
  or the name set in Android's settings), never a model code (`SM-F971U1`) or an emulator's AVD
  name (`Pixel_9a_API_36`). An emulator shows "Android emulator · Android 16"; the managed VM shows
  "Android on this PC".
- **Rename…** opens an editor card above the phones: the avatar, the field (Enter saves, Esc
  cancels, empty restores the phone's own name), "Only shown on this computer". The name is stored
  in `devices.json` and used in the sidebar, pane headers, the tray, the Network page and
  notifications.
- **Avatar**: the vendor's monochrome mark when the maker, model or name (in that order, whole
  words) names a known maker — Samsung/Galaxy, Google/Pixel, Xiaomi/Redmi/POCO, Huawei,
  Sony/Xperia, LG, Lenovo/Motorola, ASUS/ROG — else the name's first letter; an emulator or VM uses
  the VM glyph. Marks come from Kyokai's `internal/gui/assets/logos` (Simple Icons, CC0 1.0); their
  trademark notice goes into `THIRD_PARTY_NOTICES.md` and `doc/en-us/third-party.md` when they
  ship. Wordmark logos (Samsung) are drawn at 26 px inside the 36 px circle so they stay legible.

## Connecting with only an address

The user types an IP address or domain; a port is optional and never required. AndroFuse looks for
the phone in this order and shows "Looking for 100.64.0.12…" with a progress bar and Cancel:

1. **Remembered phone** at that address: use its last wireless-debugging and Companion ports.
2. **Ask the Companion**: the Companion answers on its saved TCP port (a remembered phone) and
   reports the current wireless-debugging port, which it discovers itself with `NsdManager`
   (`_adb-tls-connect._tcp`) and pushes to the paired computer whenever it changes.
3. **Search the usual ranges** on that address (wireless debugging's dynamic ports and the
   Companion's 20000–48999), about twenty seconds, cancellable; a Companion is recognised by its
   pinned certificate, adb by its handshake.
4. **Only then** ask for the port shown on the phone's Wireless debugging page, in a field whose
   prefix is the address ("100.64.0.12 :"), validated as 1024–65535.

Not paired yet: the steps on the phone (Settings › Developer options › Wireless debugging › Pair
with pairing code) and two fields, **pairing code** and **pairing port**, prefixed with the address.
Plain `adb tcpip` stays refused unless Settings › Security allows it (INV-07). The words
"host:port" never appear.

**First USB connection**: one card with two checkboxes, both on: "Install AndroFuse Companion"
(notifications, clipboard, files, and staying connected after USB is unplugged) and "Join network
Home" (connect when not on the same Wi-Fi); Not now / Continue. Then the 6-digit code shown on both
screens ("Allow pairing on the phone").

## Cross-network picture

- **Over the mesh with Wi-Fi on the phone**: the computer adds a loopback port forward to the
  phone's wireless-debugging port through EasyTier (as it already does for the Companion) and
  connects adb to it, so mirroring, DeX and app windows work as on a LAN. The Companion's
  `NsdManager` beacon keeps the port current.
- **On mobile data** (wireless debugging cannot run without Wi-Fi): the Companion shares the screen
  with `MediaProjection` + `MediaCodec` over the mesh, and — only if the user turns it on in
  Android's Accessibility settings — takes taps and text through an `AccessibilityService`
  (INV-09). The pane says so: "Companion screen sharing · cross-network · relayed", with a strip
  explaining what works (see, tap, type) and what waits for Wi-Fi (sound, Samsung DeX, app
  windows). Android asks for consent each time; the Companion first explains why.

## Network (Kyokai's Network page)

- **Summary**: connected / connecting / couldn't connect / off, how many joined networks are
  connected, phones online.
- **Networks** (up to 8, Add network beside the title, ⓘ "this computer has the same name in
  each"). A row: the name shown here, then the network name, this computer's virtual address, NAT
  in plain words ("easy to connect directly"), the port it accepts direct connections on; or
  "Couldn't connect" with the reason in the danger colour; then a state pill, a Join switch
  (applied at once) and ⋯ (Edit, Remove after a confirmation). Under a connected network: its
  phones with avatar, name, virtual address, direct or relayed, latency, and Connect (or
  "Connected"); phones never connected marked as such.
- **Add / Edit form** under the row: display name (optional), network name, servers (one URL per
  line, with a note that public nodes carry encrypted traffic through a third party), secret
  (write-only; empty keeps the stored one; a weak one is flagged), Cancel / Save.
- **New phones**: the network a newly paired phone joins (menu), and the phones' Save battery
  default.
- **Other ways in**: accept direct connections (shows the port).
- **Advanced**: try direct connections (ⓘ: lower latency, easier to discover), prefer the lowest
  latency route, and the engine ("Built-in EasyTier 2.7.1").

Today `internal/mesh` runs one instance; several networks means one EasyTier instance per joined
network (or one instance with several network identities if EasyTier allows it — to verify).

## Tray

Menu (MyGo `MenuItem` supports submenus, checkboxes, separators and disabled items):

- Header (disabled): AndroFuse, "2 phones connected · cross-network connected".
- **Stop the local assistant** (only while one is working; Kyokai's emergency stop).
- One submenu per remembered phone, named as on Home with its state ("USB", "cross-network",
  "offline"): Samsung DeX (Samsung only), Phone screen, Open app › recent apps and All apps…,
  Camera, Send clipboard, Send file…, Screenshot (saved to Pictures), Turn phone screen off,
  Disconnect (offline phones: Connect).
- Connect a phone…
- Open pictures › each pane (focus it), Close all.
- Cross-network › each network as a checkbox with its state, Network settings…
- Android on this PC › Start or Stop, Open app…
- Show AndroFuse, Settings, Quit AndroFuse.

The tray icon follows the taskbar's light or dark theme. Phone notifications become Windows
notifications with an inline reply and "Open on PC".

## Companion (phone)

- **Home**: the connected computer (Disconnect) or "Waiting for your computer"; the network (name,
  address, direct or relayed, Save battery, Leave); **wireless debugging found automatically**
  ("Port 41235 · found and sent to YUANZHE-PC", or "Not available on mobile data"), replacing the
  manual port field; background standby.
- **Permissions**: Everyday (battery and temperature, clipboard, notifications, receive files,
  handoff) and Picture and control (share the screen on mobile data, allow control on mobile data
  via Accessibility — off, with "Turn on", receive the computer's microphone — off, restore the
  display after a disconnect).
- **Security**: paired computers (Remove), this phone's fingerprint in groups, recent activity.
- Unfolded (≥ 680 dp): two columns, Home on the left, Permissions on the right.

## Settings pages in short

- **General**: language; appearance; start at sign-in; tray only when started at sign-in; closing
  the window (stay in the tray / quit); where DeX and Phone screen open, where app windows open;
  show the tour again.
- **Phones and picture**: remembered phones (Reconnect automatically, ⋯); maximum size (follow the
  phone / 1080p / 720p), maximum frame rate (60 / 30, "a target, not guaranteed"), video format
  (Auto / H.264 / H.265), play the phone's sound; switch to Wi-Fi when USB is unplugged; whether to
  offer the Companion on a first USB connection.
- **Camera and microphone**: default camera; **use the phone camera in other apps** (installs the
  AndroFuse Camera component, which needs administrator rights; Windows 11 only — the M5 item 6
  conclusion); a local preview address (off; this computer only, new key each start); send the
  computer's microphone to the phone.
- **Android on this PC**: Android 16, downloaded, size, "runs natively, no translation"; Start;
  start with AndroFuse; open folder; Delete….
- **Security and privacy**: allow unencrypted wireless connections (off; still asks every time);
  this computer's pairing fingerprint; **local assistant**: allow it to control phones (30 minutes,
  stop from the tray), what it may do without asking (tiles: view status, screenshot, open apps,
  tap and type), anything unticked asks with a card above the panes; recent actions; Stop now;
  logs hide phone serials, addresses and secrets (always on).
- **About and updates**: version, source code, licenses; **updates**: check now, check daily,
  install automatically (never / when idle / daily at 3:00), signed packages; **this computer**:
  processor (native ARM64 or x64), hardware decoding, phone tools, wireless display (Miracast)
  turned off and why; open data folder, save logs…; technical details (ABI, versions) in a
  disclosure — the only place such terms appear.

## Wording rules

- Written for the person using the app. Never shown outside "Technical details": milestone or
  invariant codes (M1, M5, INV-xx, Spike), ABI, scrcpy, MJPEG, TLS, ADB, tcpip, loopback, `host:port`,
  package names, socket names, AVD names, model codes.
- English uses **sentence case** for everything (titles, buttons, menu items): "Connect a phone",
  "Phone screen", "Send clipboard", "Save logs…". Proper names keep their case: Wi-Fi, USB,
  Samsung DeX, Android, EasyTier, Windows, H.264.
- Chinese uses full-width punctuation and 「」 for names; no "（可选）" next to a checkbox — a
  checkbox is already optional; no "顺便".
- Numbers use tabular digits; a target is never shown as a measurement.

### Copy deck (zh-Hans → en)

| zh-Hans | en |
|---|---|
| 主页 | Home |
| 这台电脑 / 可以使用 | This computer / Ready |
| 连接手机 | Connect a phone |
| 打开为 | Open as |
| 三星 DeX / 手机屏幕 / 应用窗口 / 摄像头 | Samsung DeX / Phone screen / App window / Camera |
| 大屏桌面（非三星 DeX） | Large-screen desktop (not Samsung DeX) |
| 手机 · 网络中的其他手机 | Phones · Others in my networks |
| USB · 断开后自动切到 Wi-Fi | USB · switches to Wi-Fi when unplugged |
| 跨网络 · 中继 · 48 ms | Cross-network · relayed · 48 ms |
| 上次连接 11 月 25 日 02:23 | Last connected 25 Nov, 02:23 |
| 重命名… / 只在这台电脑上显示 | Rename… / Only shown on this computer |
| 忘记这台手机 | Forget this phone |
| 自动重连 | Reconnect automatically |
| 正在查找 100.64.0.12… | Looking for 100.64.0.12… |
| 找到 Pixel 8 Pro | Found Pixel 8 Pro |
| 配对码 / 配对端口 | Pairing code / Pairing port |
| 配对并连接 | Pair and connect |
| 100.64.0.12 没有应答 | 100.64.0.12 didn't answer |
| 安装 AndroFuse 伴侣 | Install AndroFuse Companion |
| 加入网络「家里」 | Join the network "Home" |
| 在手机上允许配对 | Allow pairing on the phone |
| 59.8 fps 实测 · 目标 60，不保证 | 59.8 fps measured · target 60, not guaranteed |
| 伴侣共享屏幕 | Companion screen sharing |
| 允许一次 / 拒绝 | Allow once / Deny |
| 网络 / 添加网络 / 网络名称 / 网络密钥 | Network / Add network / Network name / Network secret |
| 服务器（每行一个） | Servers (one per line) |
| 接受直接连接 | Accept direct connections |
| 尝试直连 | Try direct connections |
| 电脑上的 Android | Android on this PC |
| Android 模拟器 | Android emulator |
| 本机助手 / 停止本机助手 | Local assistant / Stop the local assistant |
| 关于与更新 / 检查更新 / 保存日志… | About and updates / Check for updates / Save logs… |
| 技术详情 | Technical details |
| 显示 AndroFuse / 退出 AndroFuse | Show AndroFuse / Quit AndroFuse |
| 已打开的画面 / 全部关闭 | Open pictures / Close all |
| 发送剪贴板 / 发送文件… / 截图 | Send clipboard / Send file… / Screenshot |

### Current strings to retire (`locales/en.json`)

`ModeMirrorBadge`/`ModeAppsBadge` ("Active M1"), `AboutVersion` ("M0 Architecture Baseline"),
`AVBridgeCardTitle` ("… (M5)"), `AVBridgeCardDesc`, `AVVirtualCamAPIReady`/`Missing`,
`AVBridgeLocalURL`, `AVUplinkNote`, `WirelessManualAddrHint` ("TLS or VPN host:port"),
`WirelessPairAddrHint`, `CompanionAddrHint` ("Companion host:port"), `WirelessConnectBtn`
("Connect TLS"), `WirelessPairBtn`, `CompanionConnectBeaconADB`, `CompanionRunDisplayGuard`,
`CompanionCardDesc`, `CompanionSubtitle`, `CompanionBeaconTitle`/`Hint`, `MCPCardTitle`/`Desc`,
`MCPAllowLaunchLabel`, `MCPTokenLabel`, `DiagCoreLabel`/`Missing`, `DiagWfdSummary`,
`SettingsSecurityBody`, `DevicesSubtitle`, `ModesSubtitle`, `ModeDexDesc`,
`TransportHandoffReady`, `ConnectInstallCompanion` ("Also install … (optional)"),
`MeshStatusStopped` ("default safe state"), `MeshPeerHint`, and every Title Case label (for
example "Connect Companion", "Start Mesh", "Remove Managed VM").

## What is new behind these screens

Status 2026-11-26: items 1–8 and 10 are built (see doc/en-us/architecture.md and protocols.md); item 6 shows the screen and runs Back, Home and Recents but cannot tap or type (MyGo plugins cannot ship the accessibility XML that allows gestures); item 9 is not built and Settings says so. Notification replies from Windows notifications are not built (MyGo notifications take no reply). What each item needs:

1. Address-only connect (port discovery order above) and `host:port` removal.
2. Phone names: marketing name, rename (`devices.json` alias), vendor avatars.
3. Kyokai shell: one header per pane, window buttons in the top-right header, cards above panes,
   Settings sheet with search, + picker, Ctrl K.
4. Multiple mesh networks with per-network state, phones in each network, Others in my networks.
5. ADB over the mesh (loopback forward to the phone's wireless-debugging port) and the
   Companion's `NsdManager` port beacon.
6. Companion screen sharing on mobile data (`MediaProjection`, optional `AccessibilityService`).
7. Tray menu with per-phone submenus; notifications with reply.
8. Auto-update (signed packages; checking, installing when idle).
9. AndroFuse Camera component for other apps (separate native Media Source DLL per architecture,
   registered by the installer with administrator consent; Windows 11 only).
10. Local assistant: per-action approval cards and allowed-action tiles (today `internal/mcp` has
    a whitelist and an emergency stop only).
