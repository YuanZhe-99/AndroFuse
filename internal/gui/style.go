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
	paneMargin  float32 = 8
	paneRadius  float32 = 12 // V2.1: panes and cards share --r-lg; dialogs use radiusXL
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

// tokens holds semantic colors for the active theme, matching design/common.css (V2.1).
// brand is the single accent reserved for one primary action per area; text2/text3 are the
// secondary and tertiary text tiers (≥ 7:1 and ≥ 4.5:1 on surface).
type tokens struct {
	brand, onBrand  ui.Color
	brandSoft       ui.Color // tint surface
	brandSoftText   ui.Color // deep shade text on brandSoft
	good, warn, bad ui.Color
	onBad           ui.Color
	muted, hairline ui.Color
	text2, text3    ui.Color
	disabledBg      ui.Color
	disabledText    ui.Color
	surface, canvas ui.Color
}

func tok(c *ui.Context) tokens {
	t := c.Theme()
	if t.Dark {
		return tokens{
			brand:         ui.Hex("#5CD6A0"),
			onBrand:       ui.Hex("#0A1F17"),
			brandSoft:     ui.Hex("#16382B"),
			brandSoftText: ui.Hex("#A6F0CE"),
			text2:         ui.Hex("#B0B6C0"),
			text3:         ui.Hex("#8E95A0"),
			disabledBg:    ui.Hex("#2A2E35"),
			disabledText:  ui.Hex("#6B717C"),
			good:          t.Success,
			warn:          t.Warning,
			bad:           t.Danger,
			onBad:         ui.Hex("#ffffff"),
			muted:         t.TextMuted,
			hairline:      t.Border,
			surface:       t.Surface,
			canvas:        ui.Hex("#131D22"),
		}
	}
	return tokens{
		brand:         ui.Hex("#146C4B"),
		onBrand:       ui.Hex("#ffffff"),
		brandSoft:     ui.Hex("#E3F5ED"),
		brandSoftText: ui.Hex("#0D4E35"),
		text2:         ui.Hex("#4F5764"),
		text3:         ui.Hex("#6A7280"),
		disabledBg:    ui.Hex("#EEF1F4"),
		disabledText:  ui.Hex("#9AA1AB"),
		good:          t.Success,
		warn:          t.Warning,
		bad:           t.Danger,
		onBad:         ui.Hex("#ffffff"),
		muted:         t.TextMuted,
		hairline:      t.Border,
		surface:       t.Surface,
		canvas:        t.Surface,
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
