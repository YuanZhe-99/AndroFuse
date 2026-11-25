# Wire & Core Protocols (M1a–M1b)

AndroFuse implements direct wire-protocol engines in Rust (`core/androfuse-core`) and Go (`internal/adb`, `internal/scrcpy`, `internal/dex`, `internal/lease`) without shelling out to `scrcpy.exe`.

## 1. `scrcpy-server v5.0.1` Wire Protocol

`tools/fetchdeps` downloads upstream `scrcpy-server-v5.0.1` (Apache-2.0, pinned SHA-256 `764eb6f79811d5211fe9df341120882ba9994c7a61b897d7bf3fb662e53bc536`) into `.dev/cache/`. Sessions push it to `/data/local/tmp/androfuse-scrcpy-server.jar` and connect over `adb forward tcp:<port> localabstract:scrcpy_<scid>` across USB ADB, TLS Wi-Fi ADB (`_adb-tls-connect._tcp`), or Android Emulator (`emulator-*`):

1. **Video & audio socket handshake**: optional `0x00` dummy byte (`tunnel_forward=true`) on the first socket, 64-byte NUL-padded UTF-8 device name + 4-byte big-endian video `codec_id` (`0x68323634` H.264, `0x68323635` H.265). When `audio=true audio_codec=raw` is enabled, the subsequent audio socket reads a 4-byte `codec_id` (`0x00726177` raw 48kHz stereo s16le PCM) and gracefully exits if the device emits `0x00000000` (disabled) or `0x00000001` (unsupported).
2. **12-byte packet header**:
   - **Session packet** (`byte[0] & 0x80 != 0`): `client_resized = (byte[3] & 1) != 0`, `width = BE u32(4..8)`, `height = BE u32(8..12)`.
   - **Media packet** (`byte[0] & 0x80 == 0`): `is_config` (bit 62), `is_keyframe` (bit 61), `pts_us` (lower 61 bits), `size = BE u32(8..12)`. Cached CSD config packets are prepended to keyframes automatically and passed to `MP4Recorder` when recording.
3. **Interactive control, chromeless app windows, & CLI rules**:
   - Never pass `start_app` or `mouse=uhid` on the `com.genymobile.scrcpy.Server` CLI, and never pass both `display_id` and `new_display`.
   - Independent App Windows launch with 16px-aligned dimensions (`640x1120/220`, dynamically scaled at `1.5x` on resize), `flex_display=true`, `vd_system_decorations=false`, and `TYPE_START_APP` (`+pkg`) for pure chromeless rendering without Android status/nav bars.
   - Desktop interaction routes directly per window (`INV-12`) via `TYPE_INJECT_TOUCH_EVENT` (`2`, supporting both `PointerIDMouse = -1` and `PointerIDGenericFinger = -2`), `TYPE_INJECT_SCROLL_EVENT` (`3`, fixed-point `i16`), `TYPE_INJECT_KEYCODE` (`0`), `TYPE_INJECT_TEXT` (`1`), `TYPE_SET_CLIPBOARD` (`9`, plus device→PC clipboard callbacks), `TYPE_SET_DISPLAY_POWER` (`10`), `TYPE_ROTATE_DEVICE` (`11`), `TYPE_UHID_*` (`12–14`), `TYPE_START_APP` (`16`), and `TYPE_RESIZE_DISPLAY` (`21`).

## 2. Samsung Native DeX Loopback & App Icon Extraction (`server/dexhelper`)

`tools/builddex` compiles `server/dexhelper/src/com/androfuse/dexhelper/Main.java` into `build/androfuse-dex-helper.jar` (`--min-api 30`, zero Root, UID 2000 `shell`).

- **`probe`**: Bootstraps `Looper` + `ActivityThread` (`com.android.shell`) workarounds and reflects `SemWifiDisplayConfig$Builder` and `IDisplayManager.connectWifiDisplayWithConfig`, emitting `AFDEX1|PROBE|{json}`.
- **`apps [--no-icons]`**: Bootstraps `ActivityThread.systemMain()` system context, queries `ACTION_MAIN` / `CATEGORY_LAUNCHER` via `PackageManager`, filters WebAPK/hex-hash noise, reads localized labels and `resizeMode`, renders `96x96` ARGB_8888 PNG icons, and emits `AFDEX1|APP|{json}` + `AFDEX1|APPS_DONE|<count>`.
- **`serve <token> <w> <h> <dpi> <grace_sec>`**: Wakes the display (`KEYCODE_WAKEUP`), acquires `/data/local/tmp/androfuse-dex.lock`, invokes `disconnectWifiDisplay()`, binds loopback UDP drain ports plus a loopback TCP control listener (`127.0.0.1:<ctrlPort>`, advertised as the optional 8th field in `AFDEX1|<token>|READY|<id>|<w>|<h>|<dpi>|<type>|<flags>|<pid>|<ctrlPort>`), calls `connectWifiDisplayWithConfig` (`127.0.0.1:7236`, `"AndroFuseDeX"`, `mode=2`), drives the WFD RTSP `OPTIONS` → `GET_PARAMETER` → `SET_PARAMETER` → `SETUP` → `PLAY` state machine, enforces the **initial DPI ≥ 160 bootstrap rule** (`wm size` + `wm density`), supports live `RESIZE <w> <h> <dpi>`, holds the display alive during a `grace_sec` window (`AFDEX1|<token>|GRACE|<grace_sec>`) on unexpected stdin EOF, and tears down cleanly on `STOP` or grace expiry.
- **`attach <token> <ctrl_port>`**: Attaches to a surviving `serve` instance over `127.0.0.1:<ctrl_port>` during USB→TLS Wi-Fi handoff (`INV-02`), authenticates with `AUTH <token>`, receives `AFDEX1|<token>|REATTACHED|<id>|<w>|<h>|<dpi>`, and bridges `PING` / `RESIZE` / `STOP` seamlessly.
- **`SelectRoute` & `EvaluateTruth` (`INV-06`)**: One UI 9.0 (`SDK 37`) merges DeX into SystemUI/Launcher (no `com.sec.android.app.desktoplauncher` package), so `internal/dex` validates Samsung OEM identity + active `SemWifiDisplayConfig` display ownership rather than the legacy package name and allows manual switching between Native DeX and clearly labeled Virtual Display Fallback.

## 3. Miracast / Wi-Fi Direct Sink (`core/androfuse-core/src/miracast.rs` — Hidden / Shelved)

- **Why Hidden Across All Platforms**: Third-party Windows Miracast Sink advertisement cannot be accomplished through public WinRT APIs alone — it requires invoking Microsoft's undocumented private `wlanapi.dll!WlanNotifyVsIeProviderInt` interface (`GUID b7d94b4d-5db1-4e70-b5c3-dd003eeebe66`). Live verification on Windows 11 ARM64 (`build 26300`, Qualcomm WCN685x WiFiCx `qcwlan8280.sys` `1.0.4122.4000`) proved that peer Miracast association against a `WlanNotifyVsIeProviderInt` GO Sink triggers a kernel-mode `BugCheck 0x0000007E` (`0xC0000005`) inside `qcwlan8280.sys`. Because AndroFuse refuses to ship features backed by unstable undocumented OS internals, the Miracast Sink entry is hidden in the UI and disabled at runtime across all architectures; wired USB ADB and **encrypted TLS Wi-Fi ADB (`_adb-tls-connect._tcp`)** serve as the supported DeX transports.
- **Preserved Offline Protocol Stack**: The pure-Rust WFD RTSP (`OPTIONS` → `GET_PARAMETER` → `SET_PARAMETER` → `SETUP` → `PLAY`), RTP reorder, MPEG-TS H.264 demuxer, and UIBC packet codecs remain unit-tested in `miracast.rs` as reference code without touching WLAN drivers.

## 4. C-ABI v4 NV12 / WASAPI Pipeline, Local IPC & Lease Ledger (`leases.json`)

| Component | Contract |
|---|---|
| `androfuse_decoder_*` & `androfuse_audio_*` | Media Foundation H.264/HEVC DXVA/D3D11 or software decode into planar NV12 (`MF_MT_FRAME_SIZE` + `MF_MT_MINIMUM_DISPLAY_APERTURE` crop) and shared-mode WASAPI 48kHz stereo PCM playback |
| `internal/core.NV12Ring` & `SessionWindow` | Reusable NV12 slots feeding `ui.Bitmap.UpdateYUV` (`ui.NV12`, `ui.BT709`) with 0 steady-state heap allocations (`INV-04`), plus on-demand `SaveNV12AsPNG` and direct `MP4Recorder` muxing |
| `internal/localapi` & `internal/lease.Store` | Same-user AF_UNIX NDJSON daemon (`SIO_AF_UNIX_GETPEERPID` + SID check) and atomic `leases.json` tracking `mirror`, `virtual_display`, and `native_dex` leases via SHA-256 `device_hash` (`INV-03`, `INV-08`) |

## 5. Offline Fixtures & Live Device Suite

- Offline tests replay sanitized 64×48 H.264 packets (`internal/scrcpy/testdata/fold8_scrcpy_session.json`) over loopback TCP through Media Foundation NV12 → `ui.Bitmap.UpdateYUV` (`ui.NewTester`), containing zero phone screen imagery or serials.
- Live device validation (`Spikes S1, S2, S4`) is guarded behind `$env:ANDROFUSE_LIVE_DEVICE = "1"` in `internal/scrcpy` and `internal/dex` and verifies clean display/forward teardown automatically.
