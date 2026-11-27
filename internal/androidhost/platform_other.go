//go:build !android

package androidhost

import (
	"errors"
	"runtime"

	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
)

// deviceIdentity stands in for a phone outside Android (tests and the desktop preview).
func deviceIdentity() (name, model, release string, sdk int) {
	return "Android Companion (" + runtime.GOOS + ")", "Android Companion", "16", 36
}

func deviceDetails() (name, manufacturer, brand, model, release string, sdk int) {
	return "Android Companion (" + runtime.GOOS + ")", "Google", "google", "Android Companion", "16", 36
}

func readADBSettings() (usbDebug, wirelessDebug bool) {
	return false, false
}

func readDeviceTelemetry() companion.DeviceStatus {
	return companion.DeviceStatus{
		BatteryPct:  92,
		Charging:    true,
		TempC:       32.5,
		NetworkType: "wifi",
	}
}

// errNoAndroid reports features that exist only in the Android app.
var errNoAndroid = errors.New("only the Android app runs the embedded mesh")

func openCore(string) (*core.Lib, error) { return nil, errNoAndroid }

func seal([]byte) ([]byte, error) { return nil, errNoAndroid }

func unseal([]byte) ([]byte, error) { return nil, errNoAndroid }

// localSocketAddr is empty off Android: Linux abstract sockets exist only there.
const localSocketAddr = ""
