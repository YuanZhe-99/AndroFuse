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
| `internal/core` | `LoadLibraryEx` / `purego` loader for `androfuse_core` C ABI v6, `NV12Ring` zero-alloc frames, `AudioPlayer` & `MicCapturer` |
| `internal/localapi` | Per-user AF_UNIX NDJSON IPC server (`SIO_AF_UNIX_GETPEERPID` + Windows SID peer verification) exposing `devices` (including `unified_devices`) and `vm.*` control |
| `internal/vm` | On-demand downloadable headless Android VM manager (`%LOCALAPPDATA%\AndroFuse\vm`, native `arm64-v8a` / `x86_64`, `INV-03`, `INV-10`) |
| `internal/companion` | Encrypted TLS 1.3 NDJSON Companion protocol shared by desktop and Android APK (telemetry, clipboard, notifications, files, continuity, display guard, ADB beacon & pairing-port tunnel; `INV-07`, `INV-09`); `PermDefaultsV2` (`Telemetry`, `ADBBeacon`, and `ScreenShare` on by default); `pair.request` (adb forward or EasyTier mesh loopback; 6-digit code compared on both screens, returning identity + `DeviceStatus`) and `mesh.join`; `tunnel.open` turns a connection into a pipe to the phone's own Wireless debugging connect or pairing port (`ServeTunnels`); `share.open` carries the phone's screen in scrcpy framing and `input.key` presses Back, Home or Recents (screen sharing on mobile data); `daemon.open` / `daemon.ctrl` (`DialDaemonStream`, TCP keepalive `3s` + `TCP_NODELAY`) stream full `scrcpy-server` mirroring, Samsung Native DeX, and `start_app` independent app windows (`INV-01`) through the attached UID 2000 daemon without Wireless debugging |
| `internal/androidhost` | Optional Android Companion host service, persistent config, sysfs battery/thermal probe, audit log, and MyGo graded-permission UI (`INV-09`, `INV-11`); opt-in background standby and a transfer-only wake/Wi-Fi activity lease; joins a mesh handed over by the paired computer (secret sealed with Android Keystore, rejoined on start) and shows `IMPORTANCE_HIGH` heads-up adb/mesh pairing notifications; a three-tab Companion view (Home, Permissions, Security; two columns from 680 dp) showing Enhanced mode status (the attached persistent UID 2000 daemon) and a low-power mesh by default (Save battery: one runtime thread, lazy P2P, no UPnP); discovers both `_adb-tls-connect._tcp` and `_adb-tls-pairing._tcp` (NsdManager), reports `USBDebugOn` / `WirelessDebugOn` / `WirelessADBPairPort` in `DeviceStatus`, joins tunnels only to its verified ports, bridges `daemon.open` / `daemon.ctrl` streams to the UID 2000 daemon without Wireless debugging, triggers a debounced mesh rebind when `WatchNetwork` detects a live transport switch (`cellular` ↔ `wifi`), and falls back to `share.open` on mobile data after restart |
| `internal/androidbridge` | Platform-safe Android OS service capability probe paired with `CompanionServices.kt` (`NotificationListenerService`, `TileService`, `MediaProjection`, `SAF`, `AppWidgetProvider`); MyGo mobile plugin `androfusePower` (`android/Power.kt`: standby foreground service, `NetworkCallback` events, activity lease, NsdManager discovery of `_adb-tls-connect._tcp` + `_adb-tls-pairing._tcp`, `adbSettings` developer-toggle readback, and `showPairNotification` / `cancelPairNotification`; `android/Share.kt`: MediaProjection + MediaCodec H.264 screen sharing in a `mediaProjection` foreground service on the abstract socket `androfuse-share`, and an accessibility service for Back, Home and Recents; `android/Daemon.kt`: exported `AndroFuseDaemonProvider` accepting `sendBinder` only from UID 2000/0, `linkToDeath`, and `Os.socketpair` `ParcelFileDescriptor` IPC verification) |
| `internal/androidcore` | MyGo Kotlin native module that packs `libandrofuse_core.so` with the embedded EasyTier (built by `scripts/build-android-core.sh`) into the APK and loads it, plus Android Keystore sealing (`Vault`) |
| `internal/avbridge` | M5 phone-camera MJPEG loopback bridge, honest Windows `MFCreateVirtualCamera` probe, and encrypted PC→Android mic/camera uplink |
| `internal/mcp` | Opt-in, default-disabled loopback (`127.0.0.1`) HTTP JSON-RPC 2.0 MCP server with ephemeral 30-min bearer token, emergency stop, and scrubbed audit log (`INV-07`, `INV-08`); the allowlist names the tools that run without asking, and every other call waits for the user's Allow once or Deny (`Handlers.Approve`, a card above the panes) |
| `internal/mesh` | Client for the default-disabled EasyTier v2.7.1 mesh embedded in the core (`androfuse_call` `mesh.*`; no TUN, loopback port forwards only; `INV-07`, `INV-08`); one named instance per network (`Config.Instance`), `Info` with this node's mesh address, NAT type and each peer's address, direct or relayed, and latency; the compact `AF-<p|c>-<id> <name>` hostname (with legacy `AndroFuse-<kind>-<id>` compatibility) every AndroFuse device joins under, which tells them from relay servers (`Hostname`, `ParseHostname`, `SameDevice`) |
| `internal/bundlesig` | Ed25519 manifest (`bundle.json` + `bundle.sig`) helper signer/verifier matching Kyokai's release bundle layout |
| `internal/adb` | Argument-vector ADB client with bundled `platform-tools` discovery and runtime HTTPS download/extraction (`InstallPlatformTools`, `DefaultPlatformToolsDir`), `MergeDevices` USB+TLS Wi-Fi deduplication (`INV-02`), mDNS TLS wireless connect/pair (`_adb-tls-connect._tcp` and `_adb-tls-pairing._tcp`), `MaskSerial`/`MaskEndpoint` scrubbing, `emulator-*` classification, `--user 0` probes & app list; mDNS `adb-<ro.serialno>-…` instances map to `HardwareHash` for handoff reconnects; the phone's marketing name, brand, own name and emulator flag; `ProbeADB` (CNXN handshake: Wireless debugging, plain adb or neither) and `FindWirelessPort` (the port range a phone's Wireless debugging uses, searched on an address); `ExecOut` for raw output such as screenshots |
| `internal/sysproc` | `HideConsole`: every console child (adb, emulator, sdkmanager) starts with `CREATE_NO_WINDOW`, so no console or Windows Terminal window opens |
| `internal/lease` | Atomic `leases.json` ledger guaranteeing cleanup of virtual displays, DeX loopbacks, and port forwards (`INV-03`) |
| `internal/scrcpy` | `scrcpy-server v5.0.1` wire framing, interactive touch/mouse/scroll/keyboard/flex-display sessions, raw PCM audio, `MP4Recorder`, offline replay fixtures, and measured telemetry (`Stats`: decoded frames, average decode time, and an fps that falls to 0 when no frame has arrived for a second, since a still screen sends none; `INV-05`) |
| `internal/dex` | Samsung Native DeX orchestration (`server/dexhelper` loopback + 30s grace-period `Reattach`), real app label + 96x96 PNG icon cache (`ListAppsWithIcons`), `SelectRoute`, and `EvaluateTruth` (`INV-06`; Miracast hidden) |
| `internal/gui` | MyGo native UI shell after Kyokai's (design/README.md): the sidebar on the window's material (a rail below 960 DIPs) with Home, `+ Add a phone` (`wizard.go`), the open panes grouped as they show and the windows of their own, the networks' state, search, appearance and Settings; floating panes with one 44-DIP header each (phone keys and tools, a measured-fps chip with a details popover, pop out, split, close, and the window's buttons on the top-right pane; `INV-05`); cards above the panes for what waits on the user (a pairing code with Cancel, a local assistant's request, a first USB connection); consumer Home with a clean `My phones` hero, conditional ADB download/USB-auth banners, one card per phone (its own or given name, maker's mark, route, live Companion figures, actions distinguishing active Enhanced mode vs one-time activation via `EnsureEnhancedAndRun` / `OpenActivateModal`), and an automatic `Found nearby or in your networks` section for Wi-Fi pairing windows and unpaired mesh peers (`PairMeshPhone`, without exposing raw mesh IPs); guided 3-tab `+ Add a phone` modal (USB / Same Wi-Fi 6-digit pairing / Cross-network mesh, plus collapsible Advanced manual IP entry); Settings as a sheet with search (General, Phones and picture, Camera and microphone, Network with up to 8 EasyTier networks, Android on this PC, Security and privacy, About and updates with Phone connection tools download/retry and Technical details); the + picker and Ctrl K search; a tray menu with a submenu per phone; `SessionWindow` NV12 video, first pairing over adb or mesh (`mesh:<devID>` merged losslessly into real `HardwareHash` when ADB connects), Companion reconnect and Wireless debugging connect/pairing tunnels, and Companion screen sharing (`SessionModeShare`); remembered phones in `devices.json` (aliases, maker, automatic reconnect, Forget with Undo); failures show as plain sentences naming what did not work, while the full error (non-loopback IPv4 addresses masked) goes to the last 50 entries Save logs writes |
| `locales` | English (`en`) and Simplified Chinese (`zh-Hans`) ARB catalogs + generated accessors |
| `server/dexhelper` | Clean-room Java UID 2000 `app_process` helper (`probe`, `apps`, `serve`, `attach`, `recover`, `daemon`) driving `SemWifiDisplayConfig` loopback, grace control re-attach, launcher icon extraction, and the zero-idle-wakeup persistent daemon (`Daemon.java`: Binder handshake, `Os.socketpair` `ParcelFileDescriptor` pumps, `scrcpy-server` video+control sessions) |
| `tools/fetchdeps` | Downloads and SHA-256 verifies `scrcpy-server-v5.0.1` and the build-time `protoc` 36.2 into `.dev/cache/`; `-check-upstream` reports newer EasyTier releases |
| `tools/builddex` | Compiles and dexes `server/dexhelper` into `build/androfuse-dex-helper.jar` |
| `tools/buildapk` | Builds the `arm64-v8a` Android Companion APK: on Windows supplies `gradlew.bat` (forwarding MyGo's `ORG_GRADLE_PROJECT_*` properties as `-P` arguments), prefers a JDK 17, fails when `lib/arm64-v8a/*.so` is missing, and checks `zipalign -c -P 16 4` |
| `tools/sign` | Ed25519 release keygen & helper bundle signing CLI (`ANDROFUSE_SIGNING_KEY`, Kyokai format) |
| `tools/checkpe` | Verifies every `.exe` and `.dll` matches target native PE machine (`0xAA64` / `0x8664`, `INV-10`), and that `androfuse.exe` uses the Windows GUI subsystem |
| `tools/icon` | Renders `resources/icon.svg` into desktop, UI, tray, and Android adaptive icon PNGs |
| `tools/checkdocs` | Verifies 1:1 structural parity between `doc/en-us/` and `doc/zh-cn/` |
| `core/androfuse-core` | Rust 2024 `cdylib`/`rlib` core library: C ABI v6, host diagnostics, embedded EasyTier mesh (`mesh.rs` via upstream `easytier-ffi`), `scrcpy v5.0.1` wire framing, Media Foundation DXVA NV12 decode (`mf_video.rs`), WASAPI audio render & mic capture (`wasapi_audio.rs`), and hidden Miracast protocol module (`miracast.rs`) |

## Planned Runtime Modules (M2–M4)

1. **USB-to-TLS Hot Handoff & Telemetry Multiplexing (M2 core landed; M3 telemetry push)**: `UnifiedDevice` deduplicates transports via `ro.serialno` hash (`INV-02`) and `SessionWindow` performs a 30s grace-period `dexhelper attach` loopback re-attach over paired TLS Wi-Fi ADB; background telemetry streaming over `internal/localapi` expands in M3.
2. **Android Companion (`internal/androidhost` in M3)**: Optional signed APK built from this repository with graded, toggleable permissions.
3. **Cross-Network / 5G Remote Transport (`M3–M4`)**: Across different Wi-Fi subnets over BYO-VPN or embedded userspace (`--no-tun`) EasyTier mesh, the Companion beacons `_adb-tls-connect._tcp` dynamic port changes for full `scrcpy`/DeX; over pure 5G cellular (where stock non-root Android blocks `adbd` wireless debugging), Companion uses native `MediaProjection` H.264/HEVC + optional `AccessibilityService` input over the encrypted overlay.

## FFI

`androfuse-core` exports a versioned C ABI (`androfuse_abi_version() -> u32`, currently `6`):

| Export | Does |
|---|---|
| `androfuse_abi_version() -> u32` | Returns ABI version (`6`); Go refuses mismatches |
| `androfuse_start(json, len, *out, *out_len) -> i32` | Initializes core with `{version, state_dir, profile?}` |
| `androfuse_call(json, len, *out, *out_len) -> i32` | Dispatches `{method, params}` (`status`, `diagnostics.self_check`, `miracast.test_sink`, `mesh.start`, `mesh.stop`, `mesh.status`) |
| `androfuse_free(ptr, len)` | Frees response buffer allocated by `androfuse_start` / `androfuse_call` |
| `androfuse_stop()` | Stops core, destroys active decoders, audio players, mic capturers, and Miracast sessions, and clears in-memory state |
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
| `androfuse_mic_open(sample_rate, channels, *out_handle) -> i32` | Opens a shared-mode WASAPI microphone PCM capture stream (`48000 Hz`, mono/stereo s16le) |
| `androfuse_mic_read_pcm(handle, dst, dst_cap, *out_len) -> i32` | Copies queued captured s16le PCM bytes into caller buffer `dst` and writes count into `*out_len` |
| `androfuse_mic_close(handle)` | Closes the WASAPI microphone capture handle and joins its capture thread |

On Windows, `internal/core` loads `androfuse_core.dll` by absolute path with `LoadLibraryEx(LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32)`. Response output slots live in a heap `sync.Pool` so Go stack growth never invalidates pointers across C calls.

## State

| Path | Holds |
|---|---|
| `%APPDATA%\AndroFuse\` (`AndroFuse-<profile>`) | Per-user GUI preferences (`gui.json`), remembered devices (`devices.json`, Companion tokens sealed with DPAPI) and logs (`logs/`) |
| `%LOCALAPPDATA%\AndroFuse\` | Device profiles, paired credentials (DPAPI-protected), and virtual-display lease ledger (`leases.json`) |
| `%LOCALAPPDATA%\AndroFuse\vm\` | Optional on-demand built-in Android VM (`sdk/` and `avd/`), deleted cleanly via VM manager |
