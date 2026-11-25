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
