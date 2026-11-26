//go:build android

package androidhost

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/YuanZhe-99/AndroFuse/internal/androidbridge"
	"github.com/YuanZhe-99/AndroFuse/internal/androidcore"
	"github.com/YuanZhe-99/AndroFuse/internal/companion"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
)

// identity caches what Android reports about this phone once it answered.
var identity struct {
	sync.Mutex
	name, model, release string
	sdk                  int
}

// deviceIdentity is the phone's name (Settings › About phone), model number and Android version.
// Notes: asks the Kotlin side until it answers; "Android" until then.
func deviceIdentity() (name, model, release string, sdk int) {
	identity.Lock()
	defer identity.Unlock()
	if identity.model == "" {
		identity.name, identity.model, identity.release, identity.sdk = androidbridge.Identity()
	}
	if identity.model == "" {
		return "Android", "Android", "", 0
	}
	return identity.name, identity.model, identity.release, identity.sdk
}

// readDeviceTelemetry reads the battery from Android's BatteryManager, and from sysfs only on phones
// that still let apps read it; the network type comes from the Host's network callback.
func readDeviceTelemetry() companion.DeviceStatus {
	st := companion.DeviceStatus{BatteryPct: -1}
	if pct, charging, temp, ok := androidbridge.Battery(); pct >= 0 {
		st.BatteryPct, st.Charging = pct, charging
		if ok {
			st.TempC = temp
			st.ThermalWarning = temp >= 43.0
		}
		return st
	}
	if raw, err := os.ReadFile("/sys/class/power_supply/battery/capacity"); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			st.BatteryPct = v
		}
	}
	if raw, err := os.ReadFile("/sys/class/power_supply/battery/status"); err == nil {
		s := strings.TrimSpace(string(raw))
		st.Charging = strings.EqualFold(s, "Charging") || strings.EqualFold(s, "Full")
	}
	if raw, err := os.ReadFile("/sys/class/power_supply/battery/temp"); err == nil {
		if tenths, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			st.TempC = float64(tenths) / 10.0
			st.ThermalWarning = st.TempC >= 43.0
		}
	}
	return st
}

// openCore loads the core library that the androidcore native module packs into the APK and
// starts it with the Companion's state directory.
func openCore(stateDir string) (*core.Lib, error) {
	l, err := androidcore.NewLibrary()
	if err != nil {
		return nil, err
	}
	if err := l.Load(); err != nil {
		return nil, err
	}
	lib, err := core.Open("libandrofuse_core.so")
	if err != nil {
		return nil, err
	}
	if _, err := lib.Start(core.Config{Version: version.Version, StateDir: stateDir, Profile: "companion"}); err != nil {
		lib.Stop()
		return nil, err
	}
	return lib, nil
}

// seal encrypts b with the Android Keystore vault key.
func seal(b []byte) ([]byte, error) {
	v, err := androidcore.NewVault()
	if err != nil {
		return nil, err
	}
	return v.Seal(b)
}

// unseal decrypts what seal returned.
func unseal(b []byte) ([]byte, error) {
	v, err := androidcore.NewVault()
	if err != nil {
		return nil, err
	}
	return v.Open(b)
}

// localSocketAddr is the abstract local socket `adb forward localabstract:` reaches.
const localSocketAddr = "@" + companion.LocalSocket
