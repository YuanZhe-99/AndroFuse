//go:build !android

package androidbridge

import (
	"errors"
	"time"
)

// Outside Android there are no wake locks, standby services or network callbacks.

func acquireActive(time.Duration) {}

func releaseActive() {}

// SetStandby is a no-op outside Android.
func SetStandby(bool, string, string) error { return nil }

// WatchNetwork never fires outside Android.
func WatchNetwork(func(kind string)) (stop func()) { return func() {} }

// WatchWirelessDebugging never fires outside Android.
func WatchWirelessDebugging(func(port int)) (stop func()) { return func() {} }

// StartShare is unavailable outside Android.
func StartShare(int, int) error { return errors.New("screen sharing runs on Android only") }

// StopShare is a no-op outside Android.
func StopShare() {}

// GlobalAction is unavailable outside Android.
func GlobalAction(string) bool { return false }

// AccessibilityOn is false outside Android.
func AccessibilityOn() bool { return false }

// OpenAccessibilitySettings is a no-op outside Android.
func OpenAccessibilitySettings() {}
