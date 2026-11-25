//go:build android

package androidbridge

func probePlatform() Capabilities {
	stateMu.RLock()
	c := granted
	stateMu.RUnlock()
	c.Platform = "android"
	c.Notes = "Reported from Android Companion permissions; respects FLAG_SECURE and user consent"
	return c
}
