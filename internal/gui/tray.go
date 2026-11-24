package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"

	"github.com/egoist/mygo"

	L "github.com/YuanZhe-99/AndroFuse/locales"
)

// Tray owns the system tray icon and menu on desktop platforms.
type Tray struct {
	app  *App
	tray *mygo.Tray
	dark bool
}

// NewTray creates the desktop status tray icon on the main thread.
// Returns nil on platforms without tray support (e.g., Android).
func (a *App) NewTray() *Tray {
	t, err := mygo.NewTray(mygo.TrayOptions{
		Icon:    trayPNG(false),
		ToolTip: "AndroFuse",
	})
	if err != nil {
		return nil
	}
	tr := &Tray{app: a, tray: t, dark: t.Dark()}
	_ = t.SetIcon(trayPNG(tr.dark), false)
	t.OnAppearanceChange(func() {
		tr.dark = t.Dark()
		_ = t.SetIcon(trayPNG(tr.dark), false)
	})
	tr.Refresh()
	return tr
}

// Refresh rebuilds the tray menu in the active interface language.
func (tr *Tray) Refresh() {
	if tr == nil || tr.tray == nil {
		return
	}
	tr.tray.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{
			Label: L.TrayShow(),
			Click: func(*mygo.MenuItem, *mygo.Window) { tr.app.Focus() },
		},
		{Type: mygo.MenuItemSeparator},
		{
			Label: L.TrayQuit(),
			Click: func(*mygo.MenuItem, *mygo.Window) { tr.app.Quit() },
		},
	}))
}

func trayPNG(darkBar bool) []byte {
	srcBytes := trayDarkPNG
	if darkBar {
		srcBytes = trayLightPNG
	}
	src, err := png.Decode(bytes.NewReader(srcBytes))
	if err != nil {
		return srcBytes
	}
	b := src.Bounds()
	dst := image.NewNRGBA(b)
	tint := color.NRGBA{0x1D, 0x2C, 0x33, 0xff}
	if darkBar {
		tint = color.NRGBA{0xB8, 0xF1, 0xD2, 0xff}
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := src.At(x, y).RGBA()
			dst.SetNRGBA(x, y, color.NRGBA{tint.R, tint.G, tint.B, uint8(a >> 8)})
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, dst)
	return out.Bytes()
}
