//go:build windows

package avbridge

import "syscall"

func probeVirtualCameraOS() VirtualCameraCapability {
	proc := syscall.NewLazyDLL("mfsensorgroup.dll").NewProc("MFCreateVirtualCamera")
	ok := proc.Find() == nil
	note := "Loopback MJPEG phone-camera bridge ready; COM virtual camera media source driver is not registered"
	if !ok {
		note = "MFCreateVirtualCamera unavailable on this Windows build; using loopback MJPEG + native preview"
	}
	return VirtualCameraCapability{
		OS:                 "windows",
		MFVirtualCameraAPI: ok,
		Notes:              note,
	}
}
