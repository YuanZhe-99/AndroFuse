//go:build android

package androidbridge

import (
	"context"
	"encoding/json"
	"errors"
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

// WatchWirelessPairing calls fn with this phone's temporary `_adb-tls-pairing._tcp` port while
// Android's Wireless debugging pairing dialog is open, and with 0 when it closes.
// Returns: a function that stops delivering events.
func WatchWirelessPairing(fn func(port int)) (stop func()) {
	off := power.On("adbPairPort", func(data []byte) {
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
func StartShare(maxSize, bitRate int, title, text, stop string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return power.Call(ctx, "startShare", map[string]any{"maxSize": maxSize, "bitRate": bitRate, "title": title, "text": text, "stop": stop}, nil)
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

// Identity is what the phone calls itself: name is the device name its person set in Settings ›
// About phone (Build.MODEL when unset), model the model number, and release and sdk Android's
// version.
func Identity() (name, model, release string, sdk int) {
	name, _, _, model, release, sdk = DeviceDetails()
	return name, model, release, sdk
}

// DeviceDetails returns full hardware/OS naming for the Android device: user-configured name,
// manufacturer, brand, model, Android release string, and SDK level.
func DeviceDetails() (name, manufacturer, brand, model, release string, sdk int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var r struct {
		Name         string `json:"name"`
		Manufacturer string `json:"manufacturer"`
		Brand        string `json:"brand"`
		Model        string `json:"model"`
		Release      string `json:"release"`
		SDK          int    `json:"sdk"`
	}
	_ = power.Call(ctx, "identity", nil, &r)
	return r.Name, r.Manufacturer, r.Brand, r.Model, r.Release, r.SDK
}

// ADBSettings reads Android's global ADB developer option toggles (`adb_enabled` and `adb_wifi_enabled`).
func ADBSettings() (usbDebug, wirelessDebug bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var r struct {
		USBDebug      bool `json:"usbDebug"`
		WirelessDebug bool `json:"wirelessDebug"`
	}
	_ = power.Call(ctx, "adbSettings", nil, &r)
	return r.USBDebug, r.WirelessDebug
}

// Battery is the phone's charge in percent (-1 when unknown), whether it charges, and its battery
// temperature in °C (ok false when Android did not report one), from BatteryManager's sticky
// broadcast; apps cannot read /sys/class/power_supply.
func Battery() (pct int, charging bool, tempC float64, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var r struct {
		Pct        int  `json:"pct"`
		Charging   bool `json:"charging"`
		TempTenths int  `json:"tempTenths"`
	}
	r.Pct = -1
	if err := power.Call(ctx, "battery", nil, &r); err != nil {
		return -1, false, 0, false
	}
	return r.Pct, r.Charging, float64(r.TempTenths) / 10, r.TempTenths > -1000
}

// OpenNotificationSettings opens Android's notification settings for AndroFuse.
func OpenNotificationSettings() { _ = callPower("openNotificationSettings", nil) }

// GetDaemonStatus queries the current state of the persistent UID 2000 daemon (`PLAN` §9.2).
func GetDaemonStatus() DaemonStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var st DaemonStatus
	_ = power.Call(ctx, "daemonStatus", nil, &st)
	return st
}

// WatchDaemon calls fn whenever the UID 2000 daemon attaches or detaches, and invokes fn once
// immediately if a daemon is already attached.
func WatchDaemon(fn func(DaemonStatus)) (stop func()) {
	off := power.On("daemon", func(data []byte) {
		var st DaemonStatus
		if json.Unmarshal(data, &st) == nil {
			fn(st)
		}
	})
	if cur := GetDaemonStatus(); cur.Ready {
		fn(cur)
	}
	return off
}

// TestDaemonFD verifies that an `Os.socketpair` FD transferred over Binder is readable and
// writable by the UID 2000 daemon (`TX_ECHO_FD`).
func TestDaemonFD() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := power.Call(ctx, "daemonTestFD", nil, &res); err != nil {
		return false, err
	}
	return res.OK, nil
}

// StopDaemon asks the attached UID 2000 daemon to shut down cleanly (`TX_STOP`).
func StopDaemon() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ok bool
	_ = power.Call(ctx, "daemonStop", nil, &ok)
	return ok
}

// OpenDaemonSession launches a `scrcpy-server` stream inside the UID 2000 daemon and bridges
// its video and optional control `Os.socketpair` FDs onto local abstract sockets (`PLAN` §9.2).
func OpenDaemonSession(cfg DaemonSessionConfig) (DaemonSessionHandles, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var h DaemonSessionHandles
	if err := power.Call(ctx, "daemonOpenSession", cfg, &h); err != nil {
		return DaemonSessionHandles{}, err
	}
	if !h.OK {
		msg := h.Error
		if msg == "" {
			msg = "daemon failed to open session"
		}
		return DaemonSessionHandles{}, errors.New(msg)
	}
	return h, nil
}

// CloseDaemonSession stops a running daemon scrcpy-server session cleanly (`INV-03`).
func CloseDaemonSession(sessionID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ok bool
	_ = power.Call(ctx, "daemonCloseSession", map[string]string{"sessionId": sessionID}, &ok)
	return ok
}

// APKHash returns the SHA-256 hex digest of the installed Companion APK binary.
func APKHash() string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var sum string
	_ = power.Call(ctx, "apkHash", nil, &sum)
	return sum
}

// ListDaemonApps queries the JSON array of launchable apps from the attached UID 2000 daemon,
// omitting icons for any comma-separated package names listed in knownCSV.
func ListDaemonApps(knownCSV string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var raw string
	if err := power.Call(ctx, "daemonListApps", map[string]string{"knownIcons": knownCSV}, &raw); err != nil {
		return "", err
	}
	return raw, nil
}

// ShowPairNotification posts a high-importance Android notification alerting the user to confirm
// an incoming pairing request in the Companion UI.
// Side effects: posts notification ID 0x4147 under channel `androfuse_pair` (best-effort).
func ShowPairNotification(title, body string) {
	_ = callPower("showPairNotification", map[string]string{"title": title, "body": body})
}

// CancelPairNotification dismisses any active pairing confirmation notification.
// Side effects: cancels notification ID 0x4147 on Android.
func CancelPairNotification() {
	_ = callPower("cancelPairNotification", nil)
}
