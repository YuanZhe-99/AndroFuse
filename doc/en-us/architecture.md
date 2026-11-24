# Architecture

## Processes

| Process | Binary | Runs as | Role |
|---|---|---|---|
| Desktop GUI | `AndroFuse` (MyGo) | the user, never elevated | Main device/mode/diagnostics hub, system tray, and independent native `mygo.Window` instances for Samsung DeX, phone mirroring, and each independent Android app window. |
| Headless Engine | `androfuse-svc` | the user, child/service | Centralized device discovery (Miracast Wi-Fi Direct, TLS Wi-Fi ADB, USB ADB), single-device multiplexer, virtual display & UHID lease ledger, and local AF_UNIX NDJSON API (M1+). |
| Android Companion | `AndroFuse` (Android APK, M3+) | Android user | Optional in-process MyGo + Kotlin (`//mygo:native`) + Rust core app providing enhanced app metadata, notifications, clipboard assist, SAF file sync, and display cleanup guardian. |

## Packages and Crates

| Package / Crate | Role |
|---|---|
| `cmd/androfuse` | Unified MyGo native UI entry point (`mygo_nowebview` on Windows) |
| `cmd/androfuse-svc` | Headless engine CLI (`version`, `check [--json]`; `run` in M1+) |
| `internal/version` | Single product version (`Version`), verified against `mygo.json` |
| `internal/core` | `LoadLibraryEx` (Windows) / `purego` (Unix/Android) loader for `androfuse_core` |
| `internal/gui` | MyGo native UI shell, design tokens, adaptive desktop/mobile layout, tray, host diagnostics |
| `locales` | English (`en`) and Simplified Chinese (`zh-Hans`) ARB catalogs + generated accessors |
| `tools/icon` | Renders `resources/icon.svg` into desktop, UI, tray, and Android adaptive icon PNGs |
| `tools/checkdocs` | Verifies 1:1 structural parity between `doc/en-us/` and `doc/zh-cn/` |
| `core/androfuse-core` | Rust 2024 `cdylib`/`rlib` core library: C ABI v1 and passive host self-check diagnostics (M0); ADB/scrcpy wire, Media Foundation NV12 decode, WASAPI, and Miracast Sink (M1–M2) |

## Planned Runtime Modules (M1–M3)

The runtime architecture separates eight cohesive responsibilities without creating empty stub packages ahead of implementation:

1. **MyGo UI (`internal/gui`)**: Device cards, three-mode launcher, per-app native windows, floating telemetry HUD, settings, logs, tray, and Ed25519 auto-updates.
2. **Device & Session Multiplexer (`androfuse-svc`)**: Single ADB tracker, per-device port/SCID allocator, single-audio-stream arbiter, and persisted `leases.json` virtual-display cleanup ledger.
3. **Three Connection Backends**: Miracast / Wi-Fi Direct (no USB debugging required), TLS Wi-Fi ADB (`_adb-tls-pairing._tcp` / `_adb-tls-connect._tcp`), and USB ADB.
4. **Samsung DeX Engine**: Miracast Sink + ADB `SemWifiDisplayConfig` loopback (`127.0.0.1:7236`), plus virtual/simulated display fallbacks with One UI capability detection.
5. **Phone Mirroring Engine**: Display `0` low-latency video, input, audio, clipboard, rotation, screenshot, recording, and screen-on/screen-off power control.
6. **Independent App Window Engine**: Per-app `mygo.NewWindow` + `scrcpy-server` virtual displays (`--new-display` / `--flex-display` / `--start-app`), Snap Layouts dynamic resize, and single-instance migration prompts.
7. **Native Video & Audio Pipeline**: Windows Media Foundation hardware H.264/HEVC NV12 decode directly into MyGo D3D11 `ui.Bitmap.UpdateYUV` (zero JS frame forwarding, zero per-frame allocations) + WASAPI audio.
8. **Android Companion (`internal/androidhost` in M3)**: Optional signed APK built from this repository with graded, toggleable permissions.

## FFI

`androfuse-core` exports a versioned C ABI (`androfuse_abi_version() -> u32`, currently `1`):

| Export | Does |
|---|---|
| `androfuse_abi_version() -> u32` | Returns ABI version (`1`); Go refuses mismatches |
| `androfuse_start(json, len, *out, *out_len) -> i32` | Initializes core with `{version, state_dir, profile?}` |
| `androfuse_call(json, len, *out, *out_len) -> i32` | Dispatches `{method, params}` (`status`, `diagnostics.self_check`) |
| `androfuse_free(ptr, len)` | Frees response buffer allocated by `androfuse_start` / `androfuse_call` |
| `androfuse_stop()` | Stops core and clears in-memory state |

On Windows, `internal/core` loads `androfuse_core.dll` by absolute path with `LoadLibraryEx(LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32)`. Response output slots live in a heap `sync.Pool` so Go stack growth never invalidates pointers across C calls.

## State

| Path | Holds |
|---|---|
| `%APPDATA%\AndroFuse\` (`AndroFuse-<profile>`) | Per-user GUI preferences (`gui.json`) and logs (`logs/`) |
| `%LOCALAPPDATA%\AndroFuse\` (M1+) | Device profiles, paired credentials (DPAPI-protected), and virtual-display lease ledger (`leases.json`) |
