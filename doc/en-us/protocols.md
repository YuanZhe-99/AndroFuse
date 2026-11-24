# Wire & Core Protocols (M1a–M1b)

AndroFuse implements direct wire-protocol engines in Rust (`core/androfuse-core`) and Go (`internal/adb`, `internal/scrcpy`, `internal/dex`, `internal/lease`) without shelling out to `scrcpy.exe`.

## 1. `scrcpy-server v5.0.1` Wire Protocol

`tools/fetchdeps` downloads upstream `scrcpy-server-v5.0.1` (Apache-2.0, pinned SHA-256 `764eb6f79811d5211fe9df341120882ba9994c7a61b897d7bf3fb662e53bc536`) into `.dev/cache/`. Sessions push it to `/data/local/tmp/androfuse-scrcpy-server.jar` and connect over `adb forward tcp:<port> localabstract:scrcpy_<scid>` across USB ADB, TLS Wi-Fi ADB (`_adb-tls-connect._tcp`), or Android Emulator (`emulator-*`):

1. **Video socket handshake**: optional `0x00` dummy byte (`tunnel_forward=true`), 64-byte NUL-padded UTF-8 device name, 4-byte big-endian `codec_id` (`0x68323634` H.264, `0x68323635` H.265).
2. **12-byte packet header**:
   - **Session packet** (`byte[0] & 0x80 != 0`): `client_resized = (byte[3] & 1) != 0`, `width = BE u32(4..8)`, `height = BE u32(8..12)`.
   - **Media packet** (`byte[0] & 0x80 == 0`): `is_config` (bit 62), `is_keyframe` (bit 61), `pts_us` (lower 61 bits), `size = BE u32(8..12)`. Cached CSD config packets are prepended to keyframes automatically.
3. **Interactive control & CLI rules**:
   - Never pass `start_app` or `mouse=uhid` on the `com.genymobile.scrcpy.Server` CLI, and never pass both `display_id` and `new_display`.
   - Desktop interaction routes directly per window (`INV-12`) via `TYPE_INJECT_TOUCH_EVENT` (`2`, supporting both `PointerIDMouse = -1` for 1:1 hover/left/right/middle clicks and `PointerIDGenericFinger = -2` for touch mode), `TYPE_INJECT_SCROLL_EVENT` (`3`, fixed-point `i16`), `TYPE_INJECT_KEYCODE` (`0`), `TYPE_INJECT_TEXT` (`1`), `TYPE_SET_DISPLAY_POWER` (`10`), `TYPE_ROTATE_DEVICE` (`11`), `TYPE_UHID_*` (`12–14`), `TYPE_START_APP` (`16`), and `TYPE_RESIZE_DISPLAY` (`21`).

## 2. Samsung Native DeX Loopback (`server/dexhelper`)

`tools/builddex` compiles `server/dexhelper/src/com/androfuse/dexhelper/Main.java` into `build/androfuse-dex-helper.jar` (`--min-api 30`, zero Root, UID 2000 `shell`).

- **`probe`**: Bootstraps `Looper` + `ActivityThread` (`com.android.shell`) workarounds and reflects `SemWifiDisplayConfig$Builder` and `IDisplayManager.connectWifiDisplayWithConfig`, emitting `AFDEX1|PROBE|{json}`.
- **`serve <token> <w> <h> <dpi> <grace_sec>`**: Wakes the display (`KEYCODE_WAKEUP`), acquires `/data/local/tmp/androfuse-dex.lock`, invokes `disconnectWifiDisplay()`, binds loopback UDP drain ports, calls `connectWifiDisplayWithConfig` (`127.0.0.1:7236`, `"AndroFuseDeX"`, `mode=2`), drives the WFD RTSP `OPTIONS` → `GET_PARAMETER` → `SET_PARAMETER` → `SETUP` → `PLAY` state machine, enforces the **initial DPI ≥ 160 bootstrap rule** (`wm size` + `wm density`), supports live `RESIZE <w> <h> <dpi>` (verified live to trigger `scrcpy-server` automatic session reconfiguration), and tears down cleanly on `STOP` or stdin EOF grace expiry.
- **`EvaluateTruth` (`INV-06`)**: One UI 9.0 (`SDK 37`) merges DeX into SystemUI/Launcher (no `com.sec.android.app.desktoplauncher` package), so `internal/dex` validates Samsung OEM identity + active `SemWifiDisplayConfig` display ownership rather than the legacy package name.

## 3. Miracast / Wi-Fi Direct Sink (`core/androfuse-core/src/miracast.rs` — Hidden / Shelved)

- **Why Hidden Across All Platforms**: Third-party Windows Miracast Sink advertisement cannot be accomplished through public WinRT APIs alone — it requires invoking Microsoft's undocumented private `wlanapi.dll!WlanNotifyVsIeProviderInt` interface (`GUID b7d94b4d-5db1-4e70-b5c3-dd003eeebe66`). Live verification on Windows 11 ARM64 (`build 26300`, Qualcomm WCN685x WiFiCx `qcwlan8280.sys` `1.0.4122.4000`) proved that peer Miracast association against a `WlanNotifyVsIeProviderInt` GO Sink triggers a kernel-mode `BugCheck 0x0000007E` (`0xC0000005`) inside `qcwlan8280.sys`. Because AndroFuse refuses to ship features backed by unstable undocumented OS internals, the Miracast Sink entry is hidden in the UI and disabled at runtime across all architectures; wired USB ADB and **encrypted TLS Wi-Fi ADB (`_adb-tls-connect._tcp`)** serve as the supported DeX transports.
- **Preserved Offline Protocol Stack**: The pure-Rust WFD RTSP (`OPTIONS` → `GET_PARAMETER` → `SET_PARAMETER` → `SETUP` → `PLAY`), RTP reorder, MPEG-TS H.264 demuxer, and UIBC packet codecs remain unit-tested in `miracast.rs` as reference code without touching WLAN drivers.

## 4. C-ABI v3 NV12 Pipeline & Lease Ledger (`leases.json`)

| Component | Contract |
|---|---|
| `androfuse_decoder_*` & `androfuse_miracast_*` | Media Foundation H.264/HEVC DXVA/D3D11 or software decode into planar NV12 (`MF_MT_FRAME_SIZE` macroblock height + `MF_MT_MINIMUM_DISPLAY_APERTURE` crop) |
| `internal/core.NV12Ring` & `SessionWindow` | Reusable NV12 slots feeding `ui.Bitmap.UpdateYUV` (`ui.NV12`, `ui.BT709`) with 0 steady-state heap allocations (`INV-04`) |
| `internal/lease.Store` | Atomic `leases.json` tracking `mirror`, `virtual_display`, and `native_dex` leases via SHA-256 `device_hash` (never raw ADB serials or IPs) for startup crash recovery (`INV-03`, `INV-08`) |

## 5. Offline Fixtures & Live Device Suite

- Offline tests replay sanitized 64×48 H.264 packets (`internal/scrcpy/testdata/fold8_scrcpy_session.json`) over loopback TCP through Media Foundation NV12 → `ui.Bitmap.UpdateYUV` (`ui.NewTester`), containing zero phone screen imagery or serials.
- Live device validation (`Spikes S1, S2, S4`) is guarded behind `$env:ANDROFUSE_LIVE_DEVICE = "1"` in `internal/scrcpy` and `internal/dex` and verifies clean display/forward teardown automatically.
