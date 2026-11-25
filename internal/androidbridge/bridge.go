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

var (
	stateMu sync.RWMutex
	granted Capabilities
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

// Probe returns the current platform's safe capability summary.
func Probe() Capabilities {
	return probePlatform()
}
