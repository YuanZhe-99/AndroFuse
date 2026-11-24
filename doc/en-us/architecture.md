# Architecture

## Processes

| Process | Binary | Runs as | Role |
|---|---|---|---|
| Desktop GUI | `AndroFuse` (MyGo) | the user, never elevated | Main device/mode/diagnostics hub, system tray, and independent native `mygo.Window` instances for Samsung DeX, phone mirroring, and each independent Android app window. |
| Headless Engine | `androfuse-svc` | the user, child/service | Host & ADB/DeX capability prober (`probe`, `check`) in M1a; centralized device discovery, single-device multiplexer, `leases.json` recovery daemon (`run`), and local AF_UNIX NDJSON API in M1b+. |
| Android Companion | `AndroFuse` (Android APK, M3+) | Android user | Optional in-process MyGo + Kotlin (`//mygo:native`) + Rust core app providing enhanced app metadata, notifications, clipboard assist, SAF file sync, and display cleanup guardian. |

## Packages, Crates, and Device Helpers

| Package / Crate | Role |
|---|---|
| `cmd/androfuse` | Unified MyGo native UI entry point (`mygo_nowebview` on Windows) |
| `cmd/androfuse-svc` | Headless engine CLI (`version`, `check [--json]`, `probe [--json]`; `run` in M1b+) |
| `internal/version` | Single product version (`Version`), verified against `mygo.json` |
| `internal/core` | `LoadLibraryEx` / `purego` loader for `androfuse_core` C ABI v3 and `NV12Ring` zero-alloc frames |
| `internal/adb` | Argument-vector ADB client, mDNS TLS wireless connect/pair, `MaskSerial`/`MaskEndpoint` scrubbing, `emulator-*` classification, `--user 0` probes & app list |
| `internal/lease` | Atomic `leases.json` ledger guaranteeing cleanup of virtual displays, DeX loopbacks, and port forwards (`INV-03`) |
| `internal/scrcpy` | `scrcpy-server v5.0.1` wire framing, interactive touch/mouse/scroll/keyboard/flex-display sessions, and offline sanitized replay fixtures |
| `internal/dex` | Samsung Native DeX orchestration (`server/dexhelper` loopback over USB/TLS Wi-Fi ADB) and `EvaluateTruth` check (`INV-06`; Miracast receiver hidden) |
| `internal/gui` | MyGo native UI shell, interactive `SessionWindow` NV12 video windows, `InputTracker`, wireless/app launcher UI, tray, and host diagnostics |
| `locales` | English (`en`) and Simplified Chinese (`zh-Hans`) ARB catalogs + generated accessors |
| `server/dexhelper` | Clean-room Java UID 2000 `app_process` helper (`probe`, `serve`, `recover`) driving `SemWifiDisplayConfig` loopback |
| `tools/fetchdeps` | Downloads and SHA-256 verifies official Apache-2.0 `scrcpy-server-v5.0.1` into `.dev/cache/` |
| `tools/builddex` | Compiles and dexes `server/dexhelper` into `build/androfuse-dex-helper.jar` |
| `tools/icon` | Renders `resources/icon.svg` into desktop, UI, tray, and Android adaptive icon PNGs |
| `tools/checkdocs` | Verifies 1:1 structural parity between `doc/en-us/` and `doc/zh-cn/` |
| `core/androfuse-core` | Rust 2024 `cdylib`/`rlib` core library: C ABI v3, host diagnostics, `scrcpy v5.0.1` wire framing, Media Foundation DXVA NV12 decode (`mf_video.rs`), and hidden Miracast protocol module (`miracast.rs`) |

## Planned Runtime Modules (M1b–M4)

1. **Daemon IPC (`androfuse-svc run`, `internal/localapi`)**: AF_UNIX NDJSON server (`SIO_AF_UNIX_GETPEERPID` check), USB-to-TLS hot handoff, and background telemetry multiplexing.
2. **Media & Input Wiring (M1b)**: WASAPI audio capture/playback, bidirectional clipboard sync, PNG capture, MP4 muxing.
3. **Android Companion (`internal/androidhost` in M3)**: Optional signed APK built from this repository with graded, toggleable permissions.
4. **Cross-Network / 5G Remote Transport (`M3–M4`)**: Across different Wi-Fi subnets over BYO-VPN or embedded userspace (`--no-tun`) EasyTier mesh, the Companion beacons `_adb-tls-connect._tcp` dynamic port changes for full `scrcpy`/DeX; over pure 5G cellular (where stock non-root Android blocks `adbd` wireless debugging), Companion uses native `MediaProjection` H.264/HEVC + optional `AccessibilityService` input over the encrypted overlay.

## FFI

`androfuse-core` exports a versioned C ABI (`androfuse_abi_version() -> u32`, currently `3`):

| Export | Does |
|---|---|
| `androfuse_abi_version() -> u32` | Returns ABI version (`3`); Go refuses mismatches |
| `androfuse_start(json, len, *out, *out_len) -> i32` | Initializes core with `{version, state_dir, profile?}` |
| `androfuse_call(json, len, *out, *out_len) -> i32` | Dispatches `{method, params}` (`status`, `diagnostics.self_check`, `miracast.test_sink`) |
| `androfuse_free(ptr, len)` | Frees response buffer allocated by `androfuse_start` / `androfuse_call` |
| `androfuse_stop()` | Stops core, destroys active decoders/Miracast sessions, and clears in-memory state |
| `androfuse_decoder_open(codec_id, w, h, *out_handle) -> i32` | Opens a Media Foundation H.264/HEVC decoder preferring `MF_SA_D3D11_AWARE` hardware acceleration |
| `androfuse_decoder_push(handle, data, len, pts_us, flags) -> i32` | Submits an Annex-B access unit (`flags` bit 0: discontinuity, bit 1: drain) |
| `androfuse_decoder_pull_nv12(handle, dst, dst_cap, *out_meta) -> i32` | Copies decoded planar NV12 frame into caller-owned ring buffer (`0` ok, `1` grow buffer, `2` need input) |
| `androfuse_decoder_close(handle)` | Closes decoder handle and flushes Media Foundation / D3D11 resources |
| `androfuse_miracast_start(rtsp_port, *out_handle) -> i32` | Hidden / disabled at runtime (undocumented `WlanNotifyVsIeProviderInt` API) |
| `androfuse_miracast_poll_nv12(handle, dst, dst_cap, *out_meta, *out_state) -> i32` | Polls decoded Miracast NV12 frame into ring buffer (hidden / disabled) |
| `androfuse_miracast_input(handle, action, x_norm, y_norm) -> i32` | Forwards UIBC `GENERIC` pointer packet (hidden / disabled) |
| `androfuse_miracast_stop(handle)` | Tears down Miracast handle if opened (hidden / disabled) |

On Windows, `internal/core` loads `androfuse_core.dll` by absolute path with `LoadLibraryEx(LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32)`. Response output slots live in a heap `sync.Pool` so Go stack growth never invalidates pointers across C calls.

## State

| Path | Holds |
|---|---|
| `%APPDATA%\AndroFuse\` (`AndroFuse-<profile>`) | Per-user GUI preferences (`gui.json`) and logs (`logs/`) |
| `%LOCALAPPDATA%\AndroFuse\` | Device profiles, paired credentials (DPAPI-protected), and virtual-display lease ledger (`leases.json`) |
