//go:build !android

package androidbridge

import "time"

// Outside Android there are no wake locks, standby services or network callbacks.

func acquireActive(time.Duration) {}

func releaseActive() {}

// SetStandby is a no-op outside Android.
func SetStandby(bool, string, string) error { return nil }

// WatchNetwork never fires outside Android.
func WatchNetwork(func(kind string)) (stop func()) { return func() {} }
