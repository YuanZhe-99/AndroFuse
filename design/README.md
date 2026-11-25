# AndroFuse UI Redesign Specification (V2.1 HTML Drafts)

This folder contains standalone HTML UI drafts for reviewing **AndroFuse** across **Windows Desktop** and **Android Companion (Phone & Foldable)** before updating native MyGo views, locales, and docs. Open [`index.html`](index.html) offline via `file://`. Shared tokens live in [`common.css`](common.css); [`common.js`](common.js) only demonstrates interaction states (switches, sticky header, loading button, undo toast, inline validation, `Ctrl K`).

V2.1 is a refinement pass against the [topinspo.design design vocabulary](https://topinspo.design/vocabulary); see [Design Vocabulary Audit](#design-vocabulary-audit).

## Core Design Principles

1. **Pane-First Workspace + Optional Standalone Pop-Out (主窗口内嵌窗格优先，可选独立窗口)** — modeling [`Kyokai`](../../Kyokai/doc/en-us/gui.md) & [`Windows-ARM64.apk`](../../Windows-ARM64.apk/design/README.md):
   - Samsung DeX, Phone Mirroring, Independent App Windows, and Phone Camera **open inside the main window as panes by default**, grouped in the sidebar under **`已打开`** (supporting horizontal/vertical split groups like `DeX + 手机镜像`).
   - Primary launch buttons use a **split button (`▶ 启动 DeX | ▾`)** whose menu offers: `在主窗口打开 (默认)`, `在当前窗格右侧分屏`, and `在独立窗口打开`.
   - Every pane header (`44px`) exposes `[↗ 独立窗口]`, `[◫ 右分屏]`, `[⬓ 下分屏]` (folding into `⋯` below `560px`).
   - Popped-out windows live under **`独立窗口`** in the sidebar and offer a **`◧` (放回主窗口)** button in their header to dock back without disconnecting.
2. **Concise User-Facing Copy + Crisp Iconography (去冗余文字，全界面图标引导)**:
   - Remove multi-line engineering paragraphs from primary screens; keep row subtitles `<= 16` chars.
   - Every sidebar nav item, device row, split-menu action, pane header control, floating phone `devbar`, `24×24` settings row tile, and `38×38` mobile permission row carries a crisp vector SVG icon.
   - Technical diagnostics (`ARM64`, `ABI v5`, `H.264/HEVC`, `ADB`, Miracast notice) live strictly inside [`DesktopDiagnostics.html`](DesktopDiagnostics.html).
3. **Unified Device Memory & Optional First-Pair Companion Install**:
   - Currently `gui.json` persists only `theme`/`language` and `companion.json` is separate; `devices.json` (keyed by `HardwareHash`) will unify remembered wireless devices (`AutoReconnect`, DeX route preference, default open mode, optional pinned Companion binding, and "忘记此设备").
   - First-time wireless pairing offers an **unchecked-by-default (`INV-09`)** checkbox (`连接后顺便安装「AndroFuse 伴侣」（可选）`) plus a local APK picker fallback and one-click install from the Companion page for already-connected devices.

## Screen Index

| Platform | Screen | File | Viewport |
|---|---|---|---|
| Gallery | Offline artboard overview | [`index.html`](index.html) | Responsive |
| Desktop | Home · Devices (Light, split CTA, remembered devices) | [`DesktopDevices.html`](DesktopDevices.html) | 1280×800 |
| Desktop | Split-button Open Menu (`在主窗口 / 分屏 / 独立窗口`) | [`DesktopOpenMenu.html`](DesktopOpenMenu.html) | 1280×800 |
| Desktop | Embedded DeX Pane in Main Window (Dark) | [`DesktopPaneDeX.html`](DesktopPaneDeX.html) | 1280×800 |
| Desktop | Split Panes (`DeX + 手机镜像`) & `<560px` `⋯` Overflow | [`DesktopPaneSplit.html`](DesktopPaneSplit.html) | 1280×800 |
| Desktop | Standalone Pop-Out Window (`◧ 放回主窗口`) | [`DesktopStandalone.html`](DesktopStandalone.html) | 1280×800 |
| Desktop | Minimum Width Stress Test (`64px` Icon Rail) | [`DesktopDevicesNarrow.html`](DesktopDevicesNarrow.html) | 820×600 |
| Desktop | Devices Empty Onboarding | [`DesktopDevicesEmpty.html`](DesktopDevicesEmpty.html) | 1280×800 |
| Desktop | Independent App Launcher Grid | [`DesktopApps.html`](DesktopApps.html) | 1280×800 |
| Desktop | Phone Collaboration & One-Click Companion Install | [`DesktopCompanion.html`](DesktopCompanion.html) | 1280×800 |
| Desktop | Connect Device Modal (+ Opt-In APK Install) | [`DesktopConnectDialog.html`](DesktopConnectDialog.html) | 1280×800 |
| Desktop | Settings Pane (6 icon-led categories, breadcrumb, progressive disclosure) | [`DesktopSettings.html`](DesktopSettings.html) | 1280×800 |
| Desktop | Host Diagnostics (Bento, measured session stats, tech details disclosed) | [`DesktopDiagnostics.html`](DesktopDiagnostics.html) | 1280×800 |
| Desktop | Command Palette (`Ctrl K`) | [`DesktopCommandPalette.html`](DesktopCommandPalette.html) | 1280×800 |
| Desktop | Interaction State Sheet (vocabulary terms on real surfaces) | [`DesktopStates.html`](DesktopStates.html) | 1280×900 |
| Mobile | Android Companion Home | [`MobileHome.html`](MobileHome.html) | 390×800 |
| Mobile | Graded Permissions (`日常协同 / 画面与安全`) | [`MobilePermissions.html`](MobilePermissions.html) | 390×800 |
| Mobile | Cross-Network Port, Fingerprint & Recent History | [`MobileSecurity.html`](MobileSecurity.html) | 390×800 |
| Mobile | Fold 8 Unfolded (2-col) + `340dp` Cover Stress | [`MobileFoldAndNarrow.html`](MobileFoldAndNarrow.html) | Showcase |

## Width Constraints & Breakpoints

| Surface | Default | Minimum | Responsive Rules |
|---|---|---|---|
| Desktop Main Window | `1280×800` | `820×560` | `>= 960px`: `232px` sidebar; `820–959px`: auto-folds to `64px` icon rail. |
| Embedded Split Pane | Flex | `360×280` | Splits disabled when half `< 360×280`; `< 560px` wide folds header tools into `⋯`. |
| Standalone Window | `960×680` / `420×740` | `340×340` | Icon + ellipsized title (`>= 130px`) + Back + `◧` Dock + `⋯` + Win buttons. |
| Android Companion | `390×800` | `320dp` wide | Zero fixed-pixel label widths; `>= 680dp` (Fold 8 unfolded) uses 2-column grid. |

## Design Vocabulary Audit

V2.1 checks every draft against the 68 terms in [topinspo.design/vocabulary](https://topinspo.design/vocabulary), grouped the way the site groups them. Rules marked `[Term]` in [`common.css`](common.css) trace back to this table. **Proposal** means the draft shows behaviour the app does not implement yet; it must not ship or be described as supported until it is built and verified.

### Cramped — spacing

| Term | Rule in these drafts | Where |
|---|---|---|
| Padding / Gap | Rows `12×14`, cards `16`, pane `18×24`; flex `gap` instead of margins | all |
| Line Height | UI rows `1.45`; prose (notes, empty states) `--lh-prose: 1.6` | `.note`, `.prose` |
| Proximity | Label sits `6px` above its input; field groups `14px` apart | `.field`, `.field-grid` |
| Whitespace | Empty state is one icon, one line, one primary action | `DesktopDevicesEmpty` |
| Divider vs. Space | One faint hairline between sibling rows; no extra boxes | `.list`, `.srow`, `.step` |

### No focus — hierarchy and type

| Term | Rule in these drafts | Where |
|---|---|---|
| Hierarchy | Three text tiers: `--tx` 16.5:1, `--tx2` 7.4:1, `--tx3` 4.9:1 (timestamps, counts, hints) | tokens |
| Font Weight | Row titles 600 at 13–14px instead of larger sizes | `.lr`, `.srow` |
| Tracking | Page titles `-0.01em` (CJK is never tracked tighter); pairing digits `+0.14em` | `.ptop h1`, `.code-digits` |
| Tabular Numbers | fps, resolution, battery, temperature, counters, times, ports | chips, `.stat`, `.mono`, row subtitles |
| Text Balance | Headings `text-wrap: balance`; notes `pretty` | `h1–h3`, `.note` |
| Line Clamp | App names clamp to 2 lines so tiles stay even | `.app-tile .name` |
| Baseline Alignment | Measured numbers and units share one baseline; the target is muted beside them (`INV-05`) | `DesktopDiagnostics`, `DesktopStates` |
| Shortcut Hint | Right-aligned monospace keycaps (`Enter`, `Shift Enter`, `Ctrl \`, `Ctrl Shift S/R`, `Ctrl K`) — **proposal** | menus, palette, sidebar |
| Middle Truncation | VM image path keeps its ABI suffix; camera stream URL keeps `.mjpeg` | Settings › 本地环境, `DesktopCompanion` |

### Muddy — colour and surfaces

| Term | Rule in these drafts | Where |
|---|---|---|
| Accent | One solid green action per area: sidebar `连接设备` is now tinted; duplicate `+ 连接设备` page buttons removed; Companion keeps only `在电脑继续打开`; phone `停止服务` is neutral; phone section labels are muted, not green | all |
| Contrast | Secondary ≥ 7:1, tertiary ≥ 4.5:1 on pane and soft surfaces, light and dark | tokens |
| Muted Text | `--tx3` for times, counts, hints, keycaps | tokens |
| Semantic Color / Tint & Shade | Status chips and notes: tinted fill + same-hue hairline + deep same-hue text | `.chip.*`, `.note.*` |
| Disabled State | Flat matte `--dis-bg` / `--dis-tx`, no shadow, no opacity dimming | `.btn:disabled`, `.mi.dis`, `.sw:disabled` |
| Border Radius | Scale `4 / 6 / 8 / 12 / 14` (`--r-xs … --r-xl`) replaces ten ad-hoc radii | tokens |
| Concentric Radius | Inner = outer − padding: menu 12→8, segmented 8→5, group 10→6, bezel 26→19, phone 34→24 | `.menu`, `.seg`, `.grp`, `.bezel`, `.mob` |
| Nested Cards | Lists inside cards lose their own border and background | `.card .list` (Companion, Connect dialog) |
| Hairline Border | `rgba` hairlines (`--border`, `--divider`); inputs keep a firmer `--ctl` edge | tokens |
| Optical Alignment | Play triangle nudged `1px` right | `svg.play` |
| Icon Container | Tiles fixed at `24 / 32 / 40 / 48` with an inset hairline; inline sizes removed | `.tile-ico.*`, `.mob-ico` |
| Badge Counter | Height-locked neutral pill, tabular, hidden at zero, `99+` cap | `.cnt` |
| Layered Shadow | Pane, popover and sheet shadows are 3–5 faint layers plus a ring | `--shadow`, `--shadow-pop`, `--sheet-shadow` |
| Scrim | `26%` behind dialogs and the palette; background no longer blurred | `.scrim` |
| Edge Fade | Sidebar `已打开` list scrolls with a bottom fade instead of pushing 设置 off a 560px-tall window | `.side-scroll` |
| Sticky Header | Opaque page header gains hairline + shadow only once content scrolls beneath | `.ptop.sticky` (Apps) |
| Floating Bar | Phone devbar uses the elevated floating style; no bulk-action bar because there is no multi-select flow yet | `.devbar` |

### Stiff — motion

| Term | Rule in these drafts | Where |
|---|---|---|
| Easing | `--ease-out: cubic-bezier(0.32, 0.72, 0, 1)`; panels 240ms, menus 280ms, press 120ms | tokens |
| Directional Motion | Settings categories push in from the right | `.spage-inner` |
| Spring | Switch knob settles with a small overshoot (`--ease-spring`) | `.sw` |
| Stagger | App tiles enter 30ms apart | `.app-grid` |
| Press State | Buttons, icon buttons, tiles and tabs scale to `0.97` | `.btn`, `.ib`, `.app-tile`, `.mob-tab` |
| Shared Element | Native views should grow the pane into the pop-out window (`↗`) and back (`◧`) instead of hard-cutting; not drawable in static HTML | spec only |
| Origin-Aware Motion | Menus scale from their trigger corner (`top right` by default) | `.menu`, `.tip`, `.palette` |
| Hover Reveal | `忘记此设备` appears on hover or keyboard focus only; always visible on touch | `.reveal` |
| — | `prefers-reduced-motion` collapses all motion | `common.css` |

### UI patterns

| Term | Rule in these drafts | Where |
|---|---|---|
| Loading Button | Width locked, label replaced by a centred spinner; siblings never move | `[data-loading]` (pair, reconnect, re-check, install) |
| Button Hierarchy | Solid `.pri` → outline `.btn` → `.soft` → `.text` | all |
| Touch Target | Desktop icon buttons have a `40×40` hit area around a `32px` box; phone buttons ≥ 36–40dp; switches padded to 48dp | `.ib::before`, `.sw::before`, `.mob .btn` |
| Empty State | Icon + one line + primary action; the three ways to connect are clickable | `DesktopDevicesEmpty` |
| Inline Validation | Error under the field as soon as it is clear (6-digit code, port 1024–65535) | Connect dialog, phone Security, States |
| Skeleton | App-grid placeholders while icons and labels load | `DesktopStates` |
| Popover vs. Tooltip | Tooltips only label (`放回主窗口`); the copyable stream URL moved into a disclosure with a Copy button | `DesktopStandalone`, `DesktopCompanion` |
| Modal vs. Drawer | Settings moved from a full-window modal sheet to a main-window pane; Connect stays a modal | `DesktopSettings` |
| Tabs vs. Segmented | Connect dialog `无线手机 / 手机伴侣` switches forms → underline tabs; filters and value pickers stay segmented | `.tabs`, `.seg` |
| Switch vs. Checkbox | Settings and remembered-device switches apply instantly; the opt-in APK install stays a checkbox that waits for submit (`INV-09`) | Connect dialog |
| Command Palette | `Ctrl K` palette over device, app, pane and setting actions — **proposal** | `DesktopCommandPalette` |
| Bento Grid | Diagnostics: the live session tile is large, the checks are small | `DesktopDiagnostics` |
| Progressive Disclosure | Mesh fields appear only after the switch is on; relay under 高级选项; raw diagnostics under 技术详情 | Settings › 网络, Diagnostics |
| Combobox | The palette is a combobox; the Apps device selector should become one above 3 devices | palette |
| Input Affix | `tcp:// … :11010` relay field; phone port field prefixed with its IP | Settings › 网络, phone Security |
| Split Button | `启动 DeX \| ▾` (unchanged) | Devices |
| Breadcrumb | `设置 › 网络` in the Settings pane header | `DesktopSettings` |

### AI UI

AndroFuse has no chat surface, so Prompt Composer, Streaming Text, Thinking Disclosure, Citation Pill, Slash Command, Diff View, Suggested Prompts and Context Chips do not apply. Three terms map onto the opt-in local MCP control (`INV-07`, `INV-08`) and device management:

| Term | Rule in these drafts | Where |
|---|---|---|
| Approval Gate | Before an external assistant changes phone state, a card states the action, result and source with `拒绝 / 允许一次` — **proposal**: `internal/mcp` currently has a tool whitelist and emergency stop only | `DesktopStates` |
| Tool Call Steps | The redacted MCP audit log is shown as one row per action with a status glyph | Settings › 安全与高级, `DesktopStates` |
| Undo Toast | `忘记此设备` acts immediately with a 5-second Undo instead of a confirm dialog | Devices, `DesktopStates` |

### Honesty fixes in this pass

- The DeX pane sample no longer advertises "30 秒拔线热切换": USB ↔ Wi-Fi hot handoff failed on the Fold 8 on 2026-11-25 (`PLAN.md` §9.1, item 4).
- Stream subtitles say `实测 59.8 fps` (measured), and Diagnostics shows the target as a muted "目标 60 · 非保证" (`INV-05`).
- `改用通用大屏模式打开` now says "非三星 DeX", so the fallback backend is never mistaken for real DeX (`INV-06`).

### Token hand-off to `internal/gui/style.go`

When the native views are rebuilt, the CSS tokens map one-to-one onto named Go constants (no scattered literals): radius scale `--r-xs … --r-xl`, text tiers `--tx / --tx2 / --tx3`, `--border / --divider` hairlines, `--dis-bg / --dis-tx`, the three shadow stacks, and the motion constants `--ease-out` and `--dur-press / fast / panel / menu`. `style.go` currently has `paneRadius 14`, `cardRadius 12` and `appTileRadius 10`; V2.1 sets panes and cards to 12 and dialogs to 14.
