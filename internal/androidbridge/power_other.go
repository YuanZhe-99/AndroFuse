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

// WatchWirelessPairing never fires outside Android.
func WatchWirelessPairing(func(port int)) (stop func()) { return func() {} }

// Identity is empty outside Android.
func Identity() (name, model, release string, sdk int) { return "", "", "", 0 }

// DeviceDetails is empty outside Android.
func DeviceDetails() (name, manufacturer, brand, model, release string, sdk int) {
	return "", "", "", "", "", 0
}

// ADBSettings is false outside Android.
func ADBSettings() (usbDebug, wirelessDebug bool) { return false, false }

// Battery is unknown outside Android.
func Battery() (pct int, charging bool, tempC float64, ok bool) { return -1, false, 0, false }

// OpenNotificationSettings is a no-op outside Android.
func OpenNotificationSettings() {}

// BatteryRestricted is false outside Android.
func BatteryRestricted() bool { return false }

// OpenBatterySettings is a no-op outside Android.
func OpenBatterySettings() {}

// GetDaemonStatus returns an empty status outside Android.
func GetDaemonStatus() DaemonStatus { return DaemonStatus{} }

// WatchDaemon never fires outside Android.
func WatchDaemon(func(DaemonStatus)) (stop func()) { return func() {} }

// TestDaemonFD is unavailable outside Android.
func TestDaemonFD() (bool, error) { return false, nil }

// StopDaemon is a no-op outside Android.
func StopDaemon() bool { return false }

// OpenDaemonSession is unavailable outside Android.
func OpenDaemonSession(DaemonSessionConfig) (DaemonSessionHandles, error) {
	return DaemonSessionHandles{}, errors.New("daemon sessions run on Android only")
}

// CloseDaemonSession is a no-op outside Android.
func CloseDaemonSession(string) bool { return false }

// APKHash returns "" outside Android.
func APKHash() string { return "" }

// ListDaemonApps returns an empty JSON array outside Android.
func ListDaemonApps(string) (string, error) { return "[]", nil }

// ShowPairNotification is a no-op outside Android.
func ShowPairNotification(string, string) {}

// CancelPairNotification is a no-op outside Android.
func CancelPairNotification() {}
