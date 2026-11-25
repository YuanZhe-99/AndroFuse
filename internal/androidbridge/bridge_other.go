//go:build !android

package androidbridge

import "runtime"

func probePlatform() Capabilities {
	return Capabilities{
		Platform: runtime.GOOS,
		Notes:    "Android system services run only inside the Android Companion APK",
	}
}
