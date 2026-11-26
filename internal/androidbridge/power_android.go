//go:build android

package androidbridge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/egoist/mygo"
)

// power is the Kotlin half in android/Power.kt (mygo-plugin.json id "androfusePower").
var power = mygo.Native("androfusePower")

func callPower(method string, args any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return power.Call(ctx, method, args, nil)
}

func acquireActive(timeout time.Duration) {
	_ = callPower("acquireActive", map[string]int64{"timeoutMs": timeout.Milliseconds()})
}

func releaseActive() { _ = callPower("releaseActive", nil) }

// SetStandby starts or stops the opt-in foreground standby service (minimum-importance
// notification titled title / text).
func SetStandby(on bool, title, text string) error {
	if !on {
		return callPower("stopStandby", nil)
	}
	return callPower("startStandby", map[string]string{"title": title, "text": text})
}

// WatchNetwork calls fn with "wifi", "cellular", "ethernet", "other" or "none" whenever the
// default network changes (ConnectivityManager.NetworkCallback; no polling).
// Returns: a function that stops delivering events.
func WatchNetwork(fn func(kind string)) (stop func()) {
	off := power.On("network", func(data []byte) {
		var kind string
		if json.Unmarshal(data, &kind) == nil {
			fn(kind)
		}
	})
	_ = callPower("watchNetwork", nil)
	return off
}

// WatchWirelessDebugging calls fn with this phone's Wireless debugging port whenever the system
// advertises it (NsdManager discovery of its own _adb-tls-connect._tcp service), and with 0 when
// the advertisement goes away (Wireless debugging off, or Wi-Fi lost).
// Returns: a function that stops delivering events.
func WatchWirelessDebugging(fn func(port int)) (stop func()) {
	off := power.On("adbPort", func(data []byte) {
		var port int
		if json.Unmarshal(data, &port) == nil {
			fn(port)
		}
	})
	_ = callPower("watchWirelessDebugging", nil)
	return off
}

// StartShare asks Android for screen-capture consent and, when the user allows it, starts the
// Companion's screen sharing (android/Share.kt): an H.264 stream at most maxSize pixels on its
// longest side, served in scrcpy framing on the abstract socket "androfuse-share".
// Returns: an error when the user declines or capture cannot start.
func StartShare(maxSize, bitRate int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return power.Call(ctx, "startShare", map[string]int{"maxSize": maxSize, "bitRate": bitRate}, nil)
}

// StopShare ends screen sharing.
func StopShare() { _ = callPower("stopShare", nil) }

// GlobalAction runs Back, Home or Recents ("back", "home", "recents") through the Companion's
// accessibility service; false when that service is off.
func GlobalAction(action string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ok bool
	_ = power.Call(ctx, "globalAction", map[string]string{"action": action}, &ok)
	return ok
}

// AccessibilityOn reports whether the user turned the Companion's accessibility service on.
func AccessibilityOn() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ok bool
	_ = power.Call(ctx, "accessibilityOn", nil, &ok)
	return ok
}

// OpenAccessibilitySettings opens Android's accessibility settings, where the user turns the
// Companion's service on.
func OpenAccessibilitySettings() { _ = callPower("openAccessibility", nil) }
