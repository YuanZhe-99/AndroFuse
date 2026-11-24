# Upstream Research

Verified technical findings from inspecting the reference repositories and upstream toolkits (checked `2026-11-23`).

## Summary Matrix

| Project | Version / Tag | License | Language | Primary Reuse / Reference Value | Known Limits & Risks |
|---|---|---|---|---|---|
| `egoist/mygo` | `v0.10.0` | MIT | Go | Native D3D11 UI (`package ui`), `ui.Bitmap.UpdateYUV` NV12 GPU shader, `mygo_nowebview`, `MYGO_UNCAPPED`, `//mygo:native` Kotlin Android builds | Windows on ARM64 uses D3D11; verify `UpdateYUV` multi-window frame pacing under `MYGO_UNCAPPED` in Spike S1 |
| `YuanZhe-99/Kyokai` | `v0.8.1` (`01321d7`) | AGPL-3.0-only | Go + Rust + Kotlin | Monorepo desktop + Android layout, `internal/core` `LoadLibraryEx`/`purego` ABI pattern, `tools/checkdocs`, `tools/icon`, `internal/androidhost` | AGPL-3.0-only (same author); AndroFuse uses `GPL-3.0-only` and copies no AGPL RustDesk vendored crates |
| `Genymobile/scrcpy` | `v5.0.1` | Apache-2.0 | C / Java | `scrcpy-server-v5.0.1` wire protocol, `--new-display`, `--flex-display`, `--start-app`, `/dev/uhid` HID mouse/keyboard, `SET_DISPLAY_POWER`, native `win64` + `winarm64` | Plain `--new-display` lacks full Samsung DeX windowing/Recents on One UI 7–8.5; concurrent hardware encoder limit per SoC |
| `adirh3/DeXDesk` | `v0.1.1` | MIT | Rust | Wi-Fi Direct Sink (`WiFiDirectAdvertisementPublisher` + `WFDOpenHandle` + `wlanapi.dll!WlanNotifyVsIeProviderInt` GUID `b7d94b4d-5db1-4e70-b5c3-dd003eeebe66`), RTSP CEA negotiation, RTP/MPEG-TS H.264 Media Foundation decode, UIBC `GENERIC` pointer + `HIDC` keyboard | `WlanNotifyVsIeProviderInt` is undocumented and triggered Win11 ARM64 Qualcomm WCN685x kernel `BugCheck 0x7E` on active connect; 30 FPS cap; no native HID mouse or audio in v0.1.1 |
| `Aureliano021/ScrcpyDex` | `v2.1.0` | Apache-2.0 | C# + Java | `scrcpydex-server.jar` (`app_process` UID 2000, no Root) invoking `SemWifiDisplayConfig$Builder` loopback (`127.0.0.1:7236`) to allocate genuine DeX display (`DEX_ACTIVATED_DISPLAY_ID`), `/dev/uhid` mouse, `ACTION_STOP_DESKTOP_MODE` teardown, Win32 Job Object containment | Requires Samsung firmware exposing `SemWifiDisplayConfig`; requires ADB (USB or wireless) |
| `maze-mei/DX-Manager` | `v3.0.1` | MIT | C# + Kotlin | Multi-phone session registry, One UI version probe (`ro.build.version.oneui`/`sem`), Native DeX 30s reconnect grace period, DPI ≥ 160 bootstrap rule, up to 3 app windows, `overlay_display_devices` fallback, optional Companion guardian cleanup & phone-to-PC transfers | Native DeX verified on One UI 9 / Android 17; older One UI variants need fallback or diagnostic stop |
| `Keremerk/MyDeX` | `v0.3.0` | MIT | C# | Empirical findings on One UI 7/8.5 `--new-display` vs `overlay_display_devices`, single-stream Android audio routing, plaintext `adb tcpip` security risks | Plain `--new-display` leaves DeX Recents (`|||`) empty and forces full-screen apps |
| `fabriciolima77/DeXter` | `v0.5.0` | GPL-3.0-or-later | C# + Java | Backup study combining `ScrcpyDeX` loopback + `overlay_display_devices` fallback + mid-session USB→Wi-Fi recovery | Single-window tray utility; Portuguese UI |
| `AndroMeld` (`catchingnow`) | Commercial | Proprietary | Web + Android | UX reference for multi-window web/browser app isolation, clipboard/drag-drop, and audio integration | **Closed source / commercial:** strictly UX inspiration only; never decompile, copy, or bundle |

## Key Technical Takeaways

1. **Why AndroFuse Uses ADB Native DeX and Hides Miracast Sink**:
   - **Miracast (`DeXDesk` — Hidden / Shelved in AndroFuse)** works without USB debugging, but relies on Microsoft's undocumented private `wlanapi.dll!WlanNotifyVsIeProviderInt` entrypoint (which triggered kernel-mode `BugCheck 0x0000007E` inside Qualcomm `qcwlan8280.sys` on Windows 11 ARM64), typically caps at 30 FPS on Galaxy devices, and lacks native HID mouse hover/wheel/audio. AndroFuse therefore hides the Miracast Sink feature rather than depending on undocumented OS internals.
   - **ADB Native DeX (`ScrcpyDeX` / `DX-Manager` — Active Primary Path)** uses `SemWifiDisplayConfig` loopback (`127.0.0.1:7236`) + `scrcpy` over wired USB or encrypted TLS Wi-Fi ADB, achieving true 60 FPS, genuine desktop pointer/hover/right-click/scroll, dynamic resize, and audio forwarding, with zero Root and zero undocumented Windows WLAN driver calls.
   - **Plain `--new-display` (`MyDeX`)** is ideal for **Mode 3 (Independent App Windows)** using `--flex-display` and `--start-app`, but must **not** be misrepresented as full Samsung DeX unless genuine DeX taskbar, multi-window freeform, and Recents (`|||`) are present.
