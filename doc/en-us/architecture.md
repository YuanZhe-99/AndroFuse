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
| `cmd/androfuse-svc` | Headless engine CLI (`version`, `check [--json]`, `probe [--json]`, `run [--socket <path>]`) |
| `internal/version` | Single product version (`Version`), verified against `mygo.json` |
| `internal/core` | `LoadLibraryEx` / `purego` loader for `androfuse_core` C ABI v4, `NV12Ring` zero-alloc frames, and `AudioPlayer` |
| `internal/localapi` | Per-user AF_UNIX NDJSON IPC server (`SIO_AF_UNIX_GETPEERPID` + Windows SID peer verification) exposing `devices` (including `unified_devices`) and `vm.*` control |
| `internal/vm` | On-demand downloadable headless Android VM manager (`%LOCALAPPDATA%\AndroFuse\vm`, native `arm64-v8a` / `x86_64`, `INV-03`, `INV-10`) |
| `internal/companion` | Encrypted TLS 1.3 NDJSON Companion protocol shared by desktop and Android APK (telemetry, clipboard, notifications, files, continuity, display guard, ADB beacon; `INV-07`, `INV-09`) |
| `internal/androidhost` | Optional Android Companion host service, persistent config, sysfs battery/thermal probe, audit log, and MyGo graded-permission UI (`INV-09`, `INV-11`) |
| `internal/adb` | Argument-vector ADB client, `MergeDevices` USB+TLS Wi-Fi deduplication (`INV-02`), mDNS TLS wireless connect/pair, `MaskSerial`/`MaskEndpoint` scrubbing, `emulator-*` classification, `--user 0` probes & app list |
| `internal/lease` | Atomic `leases.json` ledger guaranteeing cleanup of virtual displays, DeX loopbacks, and port forwards (`INV-03`) |
| `internal/scrcpy` | `scrcpy-server v5.0.1` wire framing, interactive touch/mouse/scroll/keyboard/flex-display sessions, raw PCM audio, `MP4Recorder`, and offline replay fixtures |
| `internal/dex` | Samsung Native DeX orchestration (`server/dexhelper` loopback + 30s grace-period `Reattach`), real app label + 96x96 PNG icon cache (`ListAppsWithIcons`), `SelectRoute`, and `EvaluateTruth` (`INV-06`; Miracast hidden) |
| `internal/gui` | MyGo native UI shell, unified device cards with USB+Wi-Fi hot-handoff & `EncoderHeadroom` (`INV-01`/`INV-02`), DeX route selector (`INV-06`), App Launcher Grid, chromeless `SessionModeApp` & `SessionWindow` NV12 video, `SaveNV12AsPNG`, clipboard/audio controls, tray, and diagnostics |
| `locales` | English (`en`) and Simplified Chinese (`zh-Hans`) ARB catalogs + generated accessors |
| `server/dexhelper` | Clean-room Java UID 2000 `app_process` helper (`probe`, `apps`, `serve`, `attach`, `recover`) driving `SemWifiDisplayConfig` loopback, grace control re-attach, and launcher icon extraction |
| `tools/fetchdeps` | Downloads and SHA-256 verifies official Apache-2.0 `scrcpy-server-v5.0.1` into `.dev/cache/` |
| `tools/builddex` | Compiles and dexes `server/dexhelper` into `build/androfuse-dex-helper.jar` |
| `tools/icon` | Renders `resources/icon.svg` into desktop, UI, tray, and Android adaptive icon PNGs |
| `tools/checkdocs` | Verifies 1:1 structural parity between `doc/en-us/` and `doc/zh-cn/` |
| `core/androfuse-core` | Rust 2024 `cdylib`/`rlib` core library: C ABI v4, host diagnostics, `scrcpy v5.0.1` wire framing, Media Foundation DXVA NV12 decode (`mf_video.rs`), WASAPI audio output (`wasapi_audio.rs`), and hidden Miracast protocol module (`miracast.rs`) |

## Planned Runtime Modules (M2–M4)

1. **USB-to-TLS Hot Handoff & Telemetry Multiplexing (M2 core landed; M3 telemetry push)**: `UnifiedDevice` deduplicates transports via `ro.serialno` hash (`INV-02`) and `SessionWindow` performs a 30s grace-period `dexhelper attach` loopback re-attach over paired TLS Wi-Fi ADB; background telemetry streaming over `internal/localapi` expands in M3.
2. **Android Companion (`internal/androidhost` in M3)**: Optional signed APK built from this repository with graded, toggleable permissions.
3. **Cross-Network / 5G Remote Transport (`M3–M4`)**: Across different Wi-Fi subnets over BYO-VPN or embedded userspace (`--no-tun`) EasyTier mesh, the Companion beacons `_adb-tls-connect._tcp` dynamic port changes for full `scrcpy`/DeX; over pure 5G cellular (where stock non-root Android blocks `adbd` wireless debugging), Companion uses native `MediaProjection` H.264/HEVC + optional `AccessibilityService` input over the encrypted overlay.

## FFI

`androfuse-core` exports a versioned C ABI (`androfuse_abi_version() -> u32`, currently `4`):

| Export | Does |
|---|---|
| `androfuse_abi_version() -> u32` | Returns ABI version (`4`); Go refuses mismatches |
| `androfuse_start(json, len, *out, *out_len) -> i32` | Initializes core with `{version, state_dir, profile?}` |
| `androfuse_call(json, len, *out, *out_len) -> i32` | Dispatches `{method, params}` (`status`, `diagnostics.self_check`, `miracast.test_sink`) |
| `androfuse_free(ptr, len)` | Frees response buffer allocated by `androfuse_start` / `androfuse_call` |
| `androfuse_stop()` | Stops core, destroys active decoders, audio players, and Miracast sessions, and clears in-memory state |
| `androfuse_decoder_open(codec_id, w, h, *out_handle) -> i32` | Opens a Media Foundation H.264/HEVC decoder preferring `MF_SA_D3D11_AWARE` hardware acceleration |
| `androfuse_decoder_push(handle, data, len, pts_us, flags) -> i32` | Submits an Annex-B access unit (`flags` bit 0: discontinuity, bit 1: drain) |
| `androfuse_decoder_pull_nv12(handle, dst, dst_cap, *out_meta) -> i32` | Copies decoded planar NV12 frame into caller-owned ring buffer (`0` ok, `1` grow buffer, `2` need input) |
| `androfuse_decoder_close(handle)` | Closes decoder handle and flushes Media Foundation / D3D11 resources |
| `androfuse_miracast_start(rtsp_port, *out_handle) -> i32` | Hidden / disabled at runtime (undocumented `WlanNotifyVsIeProviderInt` API) |
| `androfuse_miracast_poll_nv12(handle, dst, dst_cap, *out_meta, *out_state) -> i32` | Polls decoded Miracast NV12 frame into ring buffer (hidden / disabled) |
| `androfuse_miracast_input(handle, action, x_norm, y_norm) -> i32` | Forwards UIBC `GENERIC` pointer packet (hidden / disabled) |
| `androfuse_miracast_stop(handle)` | Tears down Miracast handle if opened (hidden / disabled) |
| `androfuse_audio_open(sample_rate, channels, *out_handle) -> i32` | Opens a shared-mode WASAPI PCM render player (`48000 Hz`, stereo s16le) |
| `androfuse_audio_push_pcm(handle, data, len) -> i32` | Enqueues interleaved s16le PCM audio frames into the bounded WASAPI ring buffer |
| `androfuse_audio_close(handle)` | Closes the WASAPI audio player and joins its render thread |

On Windows, `internal/core` loads `androfuse_core.dll` by absolute path with `LoadLibraryEx(LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32)`. Response output slots live in a heap `sync.Pool` so Go stack growth never invalidates pointers across C calls.

## State

| Path | Holds |
|---|---|
| `%APPDATA%\AndroFuse\` (`AndroFuse-<profile>`) | Per-user GUI preferences (`gui.json`) and logs (`logs/`) |
| `%LOCALAPPDATA%\AndroFuse\` | Device profiles, paired credentials (DPAPI-protected), and virtual-display lease ledger (`leases.json`) |
| `%LOCALAPPDATA%\AndroFuse\vm\` | Optional on-demand built-in Android VM (`sdk/` and `avd/`), deleted cleanly via VM manager |
