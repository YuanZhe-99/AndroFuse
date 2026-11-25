//go:build android

package androidhost

import (
	"os"
	"strconv"
	"strings"

	"github.com/YuanZhe-99/AndroFuse/internal/companion"
)

func deviceIdentity() (model, release string, sdk int) {
	return "Android Device", "Android", 36
}

func readDeviceTelemetry() companion.DeviceStatus {
	st := companion.DeviceStatus{
		BatteryPct:  -1,
		NetworkType: "wifi/cellular",
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
