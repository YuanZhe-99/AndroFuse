// Command icon renders resources/icon.svg into desktop, UI, tray, and Android
// adaptive icon PNGs using MyGo's headless SVG renderer. Run from the repo root:
//
//	go run ./tools/icon
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/egoist/mygo/ui"
)

const src = "resources/icon.svg"

func main() {
	raw, err := os.ReadFile(src)
	if err != nil {
		fail(err)
	}
	fullSVG, err := ui.ParseSVG(raw)
	if err != nil {
		fail(err)
	}

	writePNG(renderMatte(fullSVG, 1024), "resources/icon.png")
	writePNG(renderMatte(fullSVG, 64), "internal/gui/assets/appicon.png")

	// Extract the inner mark (everything except the outer background tile) for
	// monochrome tray icons and the Android adaptive foreground layer.
	tileRe := regexp.MustCompile(`<rect[^>]*width="96"[^>]*height="96"[^>]*/>\s*`)
	inner := tileRe.ReplaceAllString(string(raw), "")
	// Expand viewBox to crop closely around the 31.75..64.25 mark for tray glyphs.
	traySource := strings.Replace(inner, `viewBox="0 0 96 96"`, `viewBox="26 26 44 44"`, 1)

	trayWhite := strings.ReplaceAll(traySource, "#B8F1D2", "#FFFFFF")
	trayWhiteSVG, err := ui.ParseSVG([]byte(trayWhite))
	if err != nil {
		fail(err)
	}
	writePNG(renderMatte(trayWhiteSVG, 64), "internal/gui/assets/tray.png")

	trayBlack := strings.ReplaceAll(traySource, "#B8F1D2", "#000000")
	trayBlackSVG, err := ui.ParseSVG([]byte(trayBlack))
	if err != nil {
		fail(err)
	}
	writePNG(renderMatte(trayBlackSVG, 64), "internal/gui/assets/tray-black.png")

	// Android adaptive icons keep important content inside the inner 72/108 zone.
	// The 32.5-unit mark inside a 96-unit viewBox occupies 33.8%; zooming slightly
	// to a 64-unit viewBox places it at ~51% of 1024px, comfortably centered.
	fgSource := strings.Replace(inner, `viewBox="0 0 96 96"`, `viewBox="16 16 64 64"`, 1)
	fgSVG, err := ui.ParseSVG([]byte(fgSource))
	if err != nil {
		fail(err)
	}
	writePNG(renderMatte(fgSVG, 1024), "packaging/android/foreground.png")
}

func renderMatte(svg *ui.SVG, size int) *image.NRGBA {
	black := render(svg, ui.Hex("#000000"), size)
	white := render(svg, ui.Hex("#FFFFFF"), size)
	return matte(black, white)
}

func writePNG(img image.Image, dst string) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		fail(err)
	}
	f, err := os.Create(dst)
	if err != nil {
		fail(err)
	}
	if err := png.Encode(f, img); err != nil {
		fail(err)
	}
	if err := f.Close(); err != nil {
		fail(err)
	}
	fmt.Println("wrote", dst)
}

func render(svg *ui.SVG, bg ui.Color, size int) *image.RGBA {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Background(bg).Children(func() {
			ui.Image(c, svg).Width(float32(size)).Height(float32(size))
		})
	}, size, size)
	tt.SetScale(1)
	tt.Frame()
	return tt.Image()
}

// matte recovers straight alpha from opaque black/white renders.
func matte(black, white *image.RGBA) *image.NRGBA {
	b := black.Bounds()
	out := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			k := black.RGBAAt(x, y)
			w := white.RGBAAt(x, y)
			diff := (int(w.R) - int(k.R) + int(w.G) - int(k.G) + int(w.B) - int(k.B)) / 3
			a := 255 - diff
			if a <= 0 {
				continue
			}
			if a > 255 {
				a = 255
			}
			un := func(v uint8) uint8 { return uint8(min(255, int(v)*255/a)) }
			out.SetNRGBA(x, y, color.NRGBA{un(k.R), un(k.G), un(k.B), uint8(a)})
		}
	}
	return out
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "icon:", err)
	os.Exit(1)
}
