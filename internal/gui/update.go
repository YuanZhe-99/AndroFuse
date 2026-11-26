package gui

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// updateState is Settings › About and updates' Updates section.
type updateState struct {
	Checking   bool
	Found      *mygo.Update // a newer version, nil when up to date or not checked
	Installing bool
	Progress   float64
	Err        string
	LastCheck  time.Time
	failed     string // the version whose install failed in this run, not retried automatically
}

// Prefs.AutoInstall's modes.
const (
	autoIdle  = "idle"  // when no picture is open
	autoNight = "night" // between 3:00 and 4:00
)

// updatesEnabled reports whether this build can update itself (MyGo's signed updater, configured
// in mygo.json; a development build or an unwritable install location cannot).
var updatesEnabled = sync.OnceValue(mygo.Updater.Enabled)

// updateLoop runs the daily check while AndroFuse runs (the first shortly after launch) and, each
// minute, the automatic install the user chose.
func (a *App) updateLoop(ctx context.Context) {
	if !updatesEnabled() {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}
	for {
		a.mu.Lock()
		daily, last, mode := !a.prefs.NoDailyCheck, a.update_.LastCheck, a.prefs.AutoInstall
		a.mu.Unlock()
		if (daily || mode != "") && time.Since(last) >= 24*time.Hour {
			a.CheckForUpdates()
		}
		a.autoInstall(mode)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}

// CheckForUpdates asks the update feed for a newer version.
func (a *App) CheckForUpdates() {
	a.mu.Lock()
	if a.update_.Checking || a.update_.Installing {
		a.mu.Unlock()
		return
	}
	a.update_.Checking = true
	a.update_.Err = ""
	a.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		up, err := mygo.Updater.Check(ctx)
		a.update(func() {
			a.update_.Checking = false
			a.update_.LastCheck = time.Now()
			a.update_.Found = up
			if err != nil && !errors.Is(err, mygo.ErrUpdatesDisabled) {
				a.update_.Err = err.Error()
			}
		})
		a.refreshTray()
	}()
}

// InstallUpdate downloads and installs the version found, then relaunches AndroFuse.
// Side effects: closes the open pictures as AndroFuse relaunches.
func (a *App) InstallUpdate() {
	a.mu.Lock()
	up := a.update_.Found
	if up == nil || a.update_.Installing {
		a.mu.Unlock()
		return
	}
	a.update_.Installing, a.update_.Progress, a.update_.Err = true, 0, ""
	a.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		err := up.Install(ctx, func(done, total int64) {
			if total > 0 {
				a.update(func() { a.update_.Progress = float64(done) / float64(total) })
			}
		})
		if err != nil {
			a.update(func() {
				a.update_.Installing = false
				a.update_.Err = err.Error()
				a.update_.failed = up.Version
			})
			return
		}
		a.CloseAllSessions()
		a.stopTunnels()
		mygo.App.Relaunch()
	}()
}

// autoInstall installs a found update when the chosen mode allows it now: never while a picture is
// open, "idle" whenever none is, "night" between 3:00 and 4:00.
func (a *App) autoInstall(mode string) {
	if mode == "" {
		return
	}
	a.mu.Lock()
	up, failed, busy := a.update_.Found, a.update_.failed, len(a.sessions) > 0
	a.mu.Unlock()
	if up == nil || up.Version == failed || busy {
		return
	}
	if mode == autoNight && time.Now().Hour() != 3 {
		return
	}
	a.InstallUpdate()
}
