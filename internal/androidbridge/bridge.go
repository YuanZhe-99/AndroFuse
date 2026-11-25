// Package androidbridge exposes platform-safe Android system capability probing
// for NotificationListenerService, Quick Settings TileService, MediaProjection,
// and Storage Access Framework (SAF) integrations.
package androidbridge

import "sync"

// Capabilities describes which Android Companion OS integrations are available and granted.
type Capabilities struct {
	Platform             string `json:"platform"`
	NotificationListener bool   `json:"notificationListener"`
	QuickSettingsTile    bool   `json:"quickSettingsTile"`
	MediaProjection      bool   `json:"mediaProjection"`
	SAF                  bool   `json:"saf"`
	Notes                string `json:"notes,omitempty"`
}

// WidgetState summarizes the Android home-screen widget state synced from androidhost.
type WidgetState struct {
	CompanionActive bool `json:"companionActive"`
	WirelessADBPort int  `json:"wirelessAdbPort,omitempty"`
	BatteryPct      int  `json:"batteryPct,omitempty"`
}

var (
	stateMu     sync.RWMutex
	granted     Capabilities
	widgetState WidgetState
)

// UpdateGranted records permission state reported by the Android Companion Kotlin layer.
// Inputs: booleans indicating granted OS permissions without bypassing FLAG_SECURE or Root checks.
// Side effects: Updates in-memory capability flags returned by Probe on Android.
func UpdateGranted(notificationListener, quickTile, mediaProjection, saf bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	granted.NotificationListener = notificationListener
	granted.QuickSettingsTile = quickTile
	granted.MediaProjection = mediaProjection
	granted.SAF = saf
}

// SyncCompanionWidget updates home-screen widget state rendered by AndroFuseWidgetProvider.
// Side effects: Stores latest companion status for Quick Settings & home widget display.
func SyncCompanionWidget(active bool, adbPort, batteryPct int) {
	stateMu.Lock()
	defer stateMu.Unlock()
	widgetState = WidgetState{
		CompanionActive: active,
		WirelessADBPort: adbPort,
		BatteryPct:      batteryPct,
	}
}

// CurrentWidgetState returns a snapshot of the synced home-screen widget state.
func CurrentWidgetState() WidgetState {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return widgetState
}

// Probe returns the current platform's safe capability summary.
func Probe() Capabilities {
	return probePlatform()
}
