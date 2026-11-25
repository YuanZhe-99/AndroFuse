//go:build !android

package androidhost

import (
	"runtime"

	"github.com/YuanZhe-99/AndroFuse/internal/companion"
)

func deviceIdentity() (model, release string, sdk int) {
	return "Android Companion (" + runtime.GOOS + ")", "16", 36
}

func readDeviceTelemetry() companion.DeviceStatus {
	return companion.DeviceStatus{
		BatteryPct:  92,
		Charging:    true,
		TempC:       32.5,
		NetworkType: "wifi",
	}
}
