package gui

import (
	"embed"
	"strings"
	"sync"
	"unicode"

	"github.com/egoist/mygo/ui"
)

// Phone makers' marks (sources, licenses and trademark notice: assets/logos/SOURCES.md).
//
//go:embed assets/logos/*.svg
var logoFS embed.FS

// makerWords maps words a phone may use about itself to a logo file, makers before product lines.
var makerWords = []struct {
	logo  string
	words []string
}{
	{"samsung", []string{"samsung", "galaxy"}},
	{"google", []string{"google", "pixel"}},
	{"xiaomi", []string{"xiaomi", "redmi", "poco", "mi"}},
	{"huawei", []string{"huawei"}},
	{"sony", []string{"sony", "xperia"}},
	{"lg", []string{"lg", "lge"}},
	{"lenovo", []string{"lenovo", "motorola", "moto"}},
	{"asus", []string{"asus", "rog", "zenfone"}},
}

var logoCache = struct {
	sync.Mutex
	m map[string]*ui.SVG
}{m: map[string]*ui.SVG{}}

// phoneLogoName returns the logo file (without ".svg") for a phone, or "" when none matches.
// Inputs: what the phone says about itself, most reliable first (maker, brand, model, names).
// Notes: whole words only, so "mi" matches "Mi 11" but not "Mint".
func phoneLogoName(fields ...string) string {
	for _, f := range fields {
		w := nameWords(f)
		for _, m := range makerWords {
			for _, kw := range m.words {
				if w[kw] {
					return m.logo
				}
			}
		}
	}
	return ""
}

// phoneLogo returns the parsed mark for a phone, or nil when none matches.
// Side effects: parses an embedded SVG on first use and caches it.
func phoneLogo(fields ...string) *ui.SVG {
	name := phoneLogoName(fields...)
	if name == "" {
		return nil
	}
	logoCache.Lock()
	defer logoCache.Unlock()
	if s, ok := logoCache.m[name]; ok {
		return s
	}
	var s *ui.SVG
	if b, err := logoFS.ReadFile("assets/logos/" + name + ".svg"); err == nil {
		if p, err := ui.ParseSVG(b); err == nil {
			s = p
		}
	}
	logoCache.m[name] = s
	if s != nil && wordmarks[name] {
		wordmarkSVGs.Store(s, true)
	}
	return s
}

// wordmarks are the marks that spell the maker's name: unreadable in a small avatar, where the
// name's first letter shows instead.
var wordmarks = map[string]bool{"samsung": true, "sony": true, "lenovo": true, "asus": true}

var wordmarkSVGs sync.Map

// nameWords splits s into lower-case words at anything that is not a letter or digit, and at
// letter–digit boundaries ("Mi11" → "mi", "11").
func nameWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out[strings.ToLower(p)] = true
		start := 0
		rs := []rune(p)
		for i := 1; i < len(rs); i++ {
			if unicode.IsDigit(rs[i]) != unicode.IsDigit(rs[i-1]) {
				out[strings.ToLower(string(rs[start:i]))] = true
				start = i
			}
		}
		if start > 0 {
			out[strings.ToLower(string(rs[start:]))] = true
		}
	}
	return out
}

// phoneAvatar draws a round avatar: the maker's mark, else the glyph (emulators and the VM), else
// the first letter of name.
func phoneAvatar(c *ui.Context, size float32, logo *ui.SVG, glyph *ui.SVG, name string) {
	k := tok(c)
	t := c.Theme()
	ui.Box(c).Size(size, size).Shrink(0).Radius(size/2).Background(t.SurfaceHover).Border(1, t.Border).Center().Children(func() {
		switch {
		case logo != nil && !(size < 30 && isWordmark(logo)):
			w, h := logo.Size()
			lw := size * 0.72
			if isWordmark(logo) {
				lw = size * 0.86
			}
			if h > w {
				lw = lw * w / h
			}
			ui.Icon(c, logo).Width(lw).TextColor(t.Text)
		case glyph != nil:
			ui.Icon(c, glyph).Size(size*0.55, size*0.55).TextColor(k.text2)
		default:
			ui.Text(c, appInitial(name)).Bold().FontSize(size * 0.42).TextColor(t.Text)
		}
	})
}

// isWordmark reports whether logo spells its maker's name.
func isWordmark(logo *ui.SVG) bool {
	_, ok := wordmarkSVGs.Load(logo)
	return ok
}
