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
	paneRadius  float32 = 14
	paneHeaderH float32 = 44
	cardRadius  float32 = 12
	cardPad     float32 = 16
	gap         float32 = 12
	navIconSize float32 = 16
	winButtonW  float32 = 40
	winButtonH  float32 = 30
	winGlyph    float32 = 10
	mobileBreak float32 = 640
)

var (
	paneShadow     = ui.RGBA(16, 24, 40, 0.08)
	closeHover     = ui.RGB(196, 43, 28)
	closeHoverText = ui.RGB(255, 255, 255)
)

// tokens holds semantic colors derived from the active theme and the
// AndroFuse icon palette (#1D2C33 slate + #B8F1D2 mint).
type tokens struct {
	brand, onBrand  ui.Color
	brandSoft       ui.Color
	good, warn, bad ui.Color
	onBad           ui.Color
	muted, hairline ui.Color
	surface, canvas ui.Color
}

func tok(c *ui.Context) tokens {
	t := c.Theme()
	if t.Dark {
		return tokens{
			brand:     ui.Hex("#B8F1D2"),
			onBrand:   ui.Hex("#1D2C33"),
			brandSoft: ui.RGBA(184, 241, 210, 0.14),
			good:      t.Success,
			warn:      t.Warning,
			bad:       t.Danger,
			onBad:     ui.Hex("#ffffff"),
			muted:     t.TextMuted,
			hairline:  t.Border,
			surface:   t.Surface,
			canvas:    ui.Hex("#131D22"),
		}
	}
	return tokens{
		brand:     ui.Hex("#1D6F4F"),
		onBrand:   ui.Hex("#ffffff"),
		brandSoft: ui.RGBA(29, 111, 79, 0.10),
		good:      t.Success,
		warn:      t.Warning,
		bad:       t.Danger,
		onBad:     ui.Hex("#ffffff"),
		muted:     t.TextMuted,
		hairline:  t.Border,
		surface:   t.Surface,
		canvas:    t.Surface,
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
