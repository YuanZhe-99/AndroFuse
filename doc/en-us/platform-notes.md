# Platform Notes

This file records only what has actually been built and run. Unverified targets and devices remain explicitly marked as not yet verified.

## Windows 11 ARM64 (`windows/arm64`)

| Area | Status | Evidence |
|---|---|---|
| Rust core (`core/androfuse-core`) C ABI v4 | Verified (M1b) | Built natively for `aarch64-pc-windows-msvc` (`rustc 1.99.1`); `cargo test` passing including live 64×48 Media Foundation H.264 NV12 decode + `MFT_MESSAGE_COMMAND_DRAIN`, `wasapi_audio::PcmRingBuffer`, `build_wfd_sink_replies` RTSP responder, `cargo clippy -- -D warnings`, and `cargo deny check licenses bans sources` |
| DXVA/D3D11 Video Decode & Zero-Alloc NV12 → MyGo `UpdateYUV` | Verified (M1a–M1b) | `androfuse-svc probe` reports `h264_hw_decoder = true`, `hevc_hw_decoder = true`; live Fold 8 USB & TLS Wi-Fi streams decoded via DXVA (`hw=true`) and passed `TestNV12RingZeroAllocSteadyState` (`0` allocs/frame) + MyGo `Bitmap.UpdateYUV` |
| Miracast / Wi-Fi Direct Sink (`Spike S3` & Live Receiver) | Hidden / Disabled (Undocumented Windows API) | Requires undocumented `wlanapi.dll!WlanNotifyVsIeProviderInt`; live Fold 8 association on Windows 11 ARM64 (`build 26300`) triggered kernel-mode `BugCheck 0x0000007E` (`0xC0000005`) in Qualcomm **`qcwlan8280.sys`** (`1.0.4122.4000` WiFiCx, WER Event `1019`) even after full WFD/RTSP lifecycle fixes. Hidden across all platforms; wireless DeX uses encrypted TLS Wi-Fi ADB Native DeX |
| MyGo Native UI, `localapi`, & Capture (`internal/gui`, `internal/localapi`, `internal/scrcpy`) | Verified live on desktop & AVD (M1b) | Wired USB & TLS Wi-Fi ADB Phone Mirroring and interactive Native Samsung DeX verified live in `androfuse.exe`; `internal/localapi` (`SIO_AF_UNIX_GETPEERPID` SID check), `SaveNV12AsPNG`, `MP4Recorder`, `CGO_ENABLED=0 go test ./...`, `go tool mygo vet .`, and `go tool mygo i18n check` passing |

## Windows 11 x64 (`windows/amd64`) & Windows 10

| Target | Status | Notes |
|---|---|---|
| Windows 11 `x64` | Not yet verified on hardware | Equal first-release gate alongside native `ARM64` in M4 |
| Windows 10 (`19045+`) | Evaluation pending (M4) | MyGo D3D11 + Media Foundation work on Win10, while Mica vibrancy falls back cleanly; Wi-Fi Direct Miracast sink varies by NDIS/WDDM driver |

## Android Devices & Companion APK

| Target | Status | Notes |
|---|---|---|
| Samsung Galaxy Z Fold 8 (`SM-F971U1`, Android 17 / SDK 37, One UI 9.0 `90000`, `SM8850`) | Verified live in `androfuse.exe` over USB & TLS Wi-Fi ADB | **Wired USB & Wireless TLS Wi-Fi (`_adb-tls-connect._tcp`)**: Both Phone Mirroring and interactive Native Samsung DeX (`SemWifiDisplayConfig` loopback, `1920x1080`, DXVA `hw=true`, mouse hover/left/right/scroll/Recents, dynamic `Resize` to `1600x900`) verified live in both automated tests and interactive `androfuse.exe` desktop windows (`leases.json` clean). **Miracast**: hidden/disabled across the project due to undocumented `WlanNotifyVsIeProviderInt` and `qcwlan8280.sys` kernel `BugCheck 0x7E`. **App Window UX**: AndroMeld-style chromeless launcher/icons scheduled next on AVD |
| Android Emulator (`Pixel_9a_API_36`, Android 16 / SDK 36) & Non-Samsung Android | Verified live on AVD (M1b) | `TestLiveEmulatorAppWindowsAndIcons` passed on `Pixel_9a_API_36` (`arm64-v8a` WHPX): `ListAppsWithIcons` extracted 18/18 real localized app labels and `96x96` PNG icons with WebAPK filtering; 2 concurrent chromeless `vd_system_decorations=false` + `flex_display=true` app windows (`544x960` -> `720x1280` `resizedAck=true`, DXVA `hw=true`, audio socket active) verified with zero residual displays or `leases.json` leaks (`INV-03`, `INV-06`) |
| Android Companion APK (`arm64-v8a`) | Not yet built (M3) | Adaptive icon foreground and `mygo.json` Android metadata prepared in M0 |
