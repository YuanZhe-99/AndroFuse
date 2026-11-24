# Platform Notes

This file records only what has actually been built and run. Unverified targets and devices remain explicitly marked as not yet verified.

## Windows 11 ARM64 (`windows/arm64`)

| Area | Status | Evidence |
|---|---|---|
| Rust core (`core/androfuse-core`) | Verified (M0) | Built natively for `aarch64-pc-windows-msvc` (`rustc 1.99.1`), `cargo clippy -- -D warnings`, `cargo test`, `cargo deny check licenses bans sources` |
| Go + Rust C-ABI (`internal/core`) & `androfuse-svc check` | Verified (M0) | Loaded real `androfuse_core.dll` via `LoadLibraryEx`, confirmed `native_arch = "arm64"`, `is_emulated = false`, `os_build = 26300`, real Media Foundation MFT & `wlanapi.dll` probes |
| MyGo Native UI (`internal/gui`) | Verified (M0) | `CGO_ENABLED=0 go test ./...`, `go tool mygo vet .`, `go tool mygo i18n check`; headless `ui.NewTester` rendered all 5 pages across `en`/`zh-Hans`, light/dark, and `1080x700`/`412x915` |
| ADB Mirroring & Independent App Windows | Not yet implemented (M1) | Pending M1 + Spike S1 |
| Miracast & ADB Native Samsung DeX | Not yet implemented (M2) | Pending M2 + Spikes S2–S3 |

## Windows 11 x64 (`windows/amd64`) & Windows 10

| Target | Status | Notes |
|---|---|---|
| Windows 11 `x64` | Not yet verified on hardware | Equal first-release gate alongside native `ARM64` in M4 |
| Windows 10 (`19045+`) | Evaluation pending (M4) | MyGo D3D11 + Media Foundation work on Win10, while Mica vibrancy falls back cleanly; Wi-Fi Direct Miracast sink varies by NDIS/WDDM driver |

## Android Devices & Companion APK

| Target | Status | Notes |
|---|---|---|
| Samsung Galaxy DeX (One UI 6/7/8/9) | Not yet verified on device | Planned in Spikes S2–S4 and M1–M2 |
| Non-Samsung Android (Android 11–17) | Not yet verified on device | Planned in M1 |
| Android Companion APK (`arm64-v8a`) | Not yet built (M3) | Adaptive icon foreground and `mygo.json` Android metadata prepared in M0 |
