//go:build !windows

package avbridge

import "runtime"

func probeVirtualCameraOS() VirtualCameraCapability {
	return VirtualCameraCapability{
		OS:                 runtime.GOOS,
		MFVirtualCameraAPI: false,
		Notes:              "Windows MFCreateVirtualCamera unavailable on non-Windows host; loopback MJPEG active",
	}
}
