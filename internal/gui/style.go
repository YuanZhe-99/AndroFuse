package gui

import (
	"github.com/egoist/mygo/ui"
)

// Layout and typography tokens (no scattered magic numbers in views).
const (
	sizeSmall   float32 = 12
	sizeBody    float32 = 14
	sizeHeading float32 = 16
	sizeTitle   float32 = 22

	sidebarW    float32 = 232
	railW       float32 = 64  // icon rail the sidebar folds into below railBreak
	railBreak   float32 = 960 // window width below which the sidebar is an icon rail
	mainDefW    int     = 1180
	mainDefH    int     = 760
	mainMinW    int     = 820 // V2.1 minimum main window
	mainMinH    int     = 560
	paneFoldW   float32 = 560 // narrower panes fold their header tools into ⋯
	splitMinW   float32 = 360 // each half of a split stays at least 360×280
	splitMinH   float32 = 280
	tileSize    float32 = 32 // icon container of a list row
	navBadgeMax         = 99 // badge counters cap at "99+"
	paneMargin  float32 = 8
	paneRadius  float32 = 4 // a pane corner that meets the window's (Kyokai paneTightRadius); Windows rounds the window at 8
	paneHeaderH float32 = 44
	cardRadius  float32 = 12
	cardPad     float32 = 16
	gap         float32 = 12
	navIconSize float32 = 16
	winButtonW  float32 = 40
	winButtonH  float32 = 30
	winGlyph    float32 = 10
	mobileBreak float32 = 640

	hudHeight        float32 = 42
	stageBgHex       string  = "#0B0F12"
	hudBgDark        string  = "#161E24"
	pillRadius       float32 = 999
	badgePadV        float32 = 3
	badgePadH        float32 = 8
	sessionDefW      int     = 1180
	sessionDefH      int     = 720
	mirrorDefW       int     = 460
	mirrorDefH       int     = 840
	appWinDefW       int     = 960
	appWinDefH       int     = 680
	appWinPortraitW  int     = 420
	appWinPortraitH  int     = 780
	appWinInitW      int     = 640
	appWinInitH      int     = 1120
	appWinInitDPI    int     = 220
	appWinPixelScale float32 = 1.5
	appTileW         float32 = 96
	appTileIconSz    float32 = 44
	appTilePadV      float32 = 10
	appTilePadH      float32 = 6
	appTileRadius    float32 = 10
	appTitleBarH     float32 = 38
	appTitleIconSz   float32 = 18
	appMinResizePx   int     = 320
	sessionMinW      int     = 360
	sessionMinH      int     = 320
	resizeSnapPx     int     = 16
	resizeMinDiff    float32 = 24
)

// V2.1 design tokens (design/README.md § Token hand-off, topinspo vocabulary audit).
const (
	// Radius scale; nested radii are outer − padding (concentric radius).
	radiusXS float32 = 4  // keycaps, skeleton bars
	radiusSM float32 = 6  // icon buttons, menu rows
	radiusMD float32 = 8  // buttons, inputs, nav rows
	radiusLG float32 = 12 // panes, cards, lists, menus
	radiusXL float32 = 14 // dialogs, standalone windows

	// Motion (milliseconds); curve is ease-out cubic-bezier(0.32, 0.72, 0, 1).
	motionPressMs = 120
	motionFastMs  = 160
	motionPanelMs = 240
	motionMenuMs  = 280

	pressScale     float32 = 0.97 // press state
	touchTargetMin float32 = 40   // minimum hit area for icon buttons
)

var (
	paneShadow     = ui.RGBA(16, 24, 40, 0.08)
	closeHover     = ui.RGB(196, 43, 28)
	closeHoverText = ui.RGB(255, 255, 255)
)

// tokens holds semantic colors for the active theme (design/common.css, V3). brand is the system
// accent (Kyokai: one accent per area); text2/text3 are the secondary and tertiary text tiers
// (≥ 7:1 and ≥ 4.5:1 on the pane); status colors as words are a shade deeper than as dots.
type tokens struct {
	brand, onBrand  ui.Color
	brandSoft       ui.Color // accent tint surface
	brandSoftText   ui.Color // accent as words
	good, warn, bad ui.Color // dots, edges, tints
	goodText        ui.Color // status colors as words
	warnText        ui.Color
	badText         ui.Color
	onBad           ui.Color
	muted, hairline ui.Color
	text2, text3    ui.Color
	disabledBg      ui.Color
	disabledText    ui.Color
	surface, canvas ui.Color
	sunk            ui.Color // fields, row hover, sub-rows
}

func tok(c *ui.Context) tokens {
	t := c.Theme()
	acc := t.Accent
	if t.Dark {
		return tokens{
			brand:         acc,
			onBrand:       t.AccentText,
			brandSoft:     acc.Alpha(0.16),
			brandSoftText: acc.Mix(ui.Hex("#ffffff"), 0.35),
			text2:         ui.Hex("#C9CDD4"),
			text3:         ui.Hex("#A0A6B0"),
			disabledBg:    ui.Hex("#2F3238"),
			disabledText:  ui.Hex("#7A808A"),
			good:          ui.Hex("#4CBB4C"),
			warn:          ui.Hex("#F0B429"),
			bad:           ui.Hex("#F2554A"),
			goodText:      ui.Hex("#7FD67F"),
			warnText:      ui.Hex("#FFC95C"),
			badText:       ui.Hex("#FF9A90"),
			onBad:         ui.Hex("#ffffff"),
			muted:         t.TextMuted,
			hairline:      t.Border,
			surface:       t.Surface,
			canvas:        ui.Hex("#1D1F23"),
			sunk:          ui.Hex("#2F3238"),
		}
	}
	return tokens{
		brand:         acc,
		onBrand:       t.AccentText,
		brandSoft:     acc.Alpha(0.12),
		brandSoftText: acc.Mix(ui.Hex("#000000"), 0.3),
		text2:         ui.Hex("#4A505C"),
		text3:         ui.Hex("#646B78"),
		disabledBg:    ui.Hex("#EEF1F4"),
		disabledText:  ui.Hex("#8A919C"),
		good:          ui.Hex("#2AA52A"),
		warn:          ui.Hex("#E8A200"),
		bad:           ui.Hex("#D93A2F"),
		goodText:      ui.Hex("#0F7B0F"),
		warnText:      ui.Hex("#8A5100"),
		badText:       ui.Hex("#B3261E"),
		onBad:         ui.Hex("#ffffff"),
		muted:         t.TextMuted,
		hairline:      t.Border,
		surface:       t.Surface,
		canvas:        ui.Hex("#EEF0F3"),
		sunk:          ui.Hex("#F5F6F8"),
	}
}

func card(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Column(c).
		Background(t.Surface).
		Radius(cardRadius).
		Padding(cardPad).
		Gap(gap).
		FillWidth().
		Border(1, t.Border)
}

// V3 shell tokens (design/README.md: Kyokai's structure).
const (
	homeMaxW      float32 = 1120 // Home's content never grows wider
	pageMaxW      float32 = 720  // a Settings page's content
	devCardMinW   float32 = 300  // a phone card on Home
	homeGap       float32 = 16   // between Home's cards, the top two and the phone grid alike
	avatarSize    float32 = 36
	avatarSmall   float32 = 22
	avatarLarge   float32 = 44
	sheetMarginX  float32 = 64 // the Settings sheet's distance from the window's sides
	sheetMarginT  float32 = 48
	sheetMarginB  float32 = 32
	sheetNavW     float32 = 232
	paletteW      float32 = 620
	paletteTop    float32 = 72
	pickerW       float32 = 420
	rowMinH       float32 = 60
	subRowIndent  float32 = 58
	iconButton    float32 = 32
	telemetryPopW float32 = 340
	noticeEdge    float32 = 3
)
