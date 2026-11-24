// androfuse is the unified MyGo native UI client for Windows desktop (x64/ARM64)
// and Android Companion (M3).
package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/gui"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
	L "github.com/YuanZhe-99/AndroFuse/locales"
)

func main() {
	profile := os.Getenv("ANDROFUSE_PROFILE")
	name := "AndroFuse"
	if profile != "" {
		name += "-" + profile
	}
	mygo.App.SetName(name)
	mygo.App.SetVersion(version.Version)
	if runtime.GOOS != "android" && !mygo.App.RequestSingleInstanceLock() {
		return
	}

	stateDir, err := gui.DefaultStateDir(profile)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "logs"), 0o755); err != nil {
		log.Fatal(err)
	}

	app := gui.New(profile, stateDir)
	gui.ApplyTheme(app.ThemePref())
	if lang := app.LanguagePref(); lang != "" {
		mygo.App.SetLocale(lang)
	}

	mygo.App.OnSecondInstance(func([]string, string) { app.Focus() })
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			app.Focus()
		}
	})

	mygo.App.WhenReady(func() {
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleWindowMenu},
		}))
		tray := app.NewTray()
		app.SetTray(tray)
		app.Focus()
		if runtime.GOOS != "android" {
			mygo.App.OnWindowAllClosed(func() {})
		}
		mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
			app.CloseAllSessions()
		})
		mygo.App.OnLocaleChanged(func(string) {
			app.RefreshChrome()
			tray.Refresh()
		})
		app.Start()
	})

	_ = L.AppName
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	_ = ui.Text
}
