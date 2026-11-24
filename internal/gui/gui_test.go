package gui

import (
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/core"
	_ "github.com/YuanZhe-99/AndroFuse/locales"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a := New("test", t.TempDir())
	a.diagReady = true
	a.diag = HostDiagnostics{
		CoreLoaded: true,
		CoreABI:    core.ABIVersion,
		SelfCheck: core.SelfCheck{
			OS:                     "windows",
			ProcessArch:            "arm64",
			NativeArch:             "arm64",
			OSBuild:                26300,
			MediaFoundationPresent: true,
			H264HWDecoder:          true,
			H264AnyDecoder:         true,
			HEVCHWDecoder:          true,
			HEVCAnyDecoder:         true,
			WiFiDirectAPI:          true,
			MiracastIEProvider:     true,
		},
		ADBFound:   true,
		ADBVersion: "Android Debug Bridge version 1.0.41",
	}
	return a
}

func TestViewsRender(t *testing.T) {
	pages := []navPage{pageDevices, pageModes, pageDiagnostics, pageSettings, pageAbout}
	sizes := []struct {
		name string
		w, h int
	}{
		{"desktop", 1080, 700},
		{"mobile", 412, 915},
	}

	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocaleFunc(func() string { return lang })
		for _, sz := range sizes {
			for _, dark := range []bool{false, true} {
				for _, pg := range pages {
					a := testApp(t)
					a.page = string(pg)
					tt := ui.NewTester(a.view, sz.w, sz.h)
					tt.SetDark(dark)
					tt.Frame()
					img := tt.Image()
					if img.Bounds().Dx() != sz.w || img.Bounds().Dy() != sz.h {
						t.Fatalf("unexpected frame bounds for %s/%s/%s: %v", lang, sz.name, pg, img.Bounds())
					}
				}
			}
		}
	}
}
