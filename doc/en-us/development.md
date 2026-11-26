# Development

## Prerequisites

- **Go**: `1.27.4` (pure-Go desktop builds with `CGO_ENABLED=0`)
- **MyGo**: `github.com/egoist/mygo v0.10.0` (invoked via `go tool mygo`)
- **Rust**: `1.99.1` (`rust-toolchain.toml`) with `rustfmt`, `clippy`, and `cargo-deny`
- **Windows Toolchain**: Visual Studio / MSVC build tools for `aarch64-pc-windows-msvc` and `x86_64-pc-windows-msvc`
- **Android (`M1a` DeX Helper & `M3+` Companion)**: Android SDK (API 36), `build-tools` (`d8`), NDK, and JDK 17+

## Common Commands

```bash
# Generate PNG icons from resources/icon.svg
go run ./tools/icon

# Download and SHA-256 verify scrcpy-server-v5.0.1 and the build-time protoc 36.2 into .dev/cache/
go run ./tools/fetchdeps

# Report whether EasyTier has a newer release than the embedded pin (exit code 2 if so)
go run ./tools/fetchdeps -check-upstream

# Compile server/dexhelper into build/androfuse-dex-helper.jar
go run ./tools/builddex

# Regenerate i18n Go catalog accessors after editing locales/*.json
go tool mygo generate

# Build the Rust core library (debug/release); on Windows ARM64, put LLVM clang on PATH first
# (for example Visual Studio's VCToolslvmarm64in) because the embedded mesh builds ring
cargo build --manifest-path core/Cargo.toml --workspace

# Build the Android core (embedded EasyTier) into the androidcore native module before building the APK
bash scripts/build-android-core.sh arm64-v8a

# Build and launch the interactive desktop client into build/
Copy-Item core/target/debug/androfuse_core.dll build/androfuse_core.dll
go build -tags mygo_nowebview -ldflags=-H=windowsgui -o build/androfuse.exe ./cmd/androfuse

# Run host self-check and ADB / Samsung DeX capability probes
go run ./cmd/androfuse-svc probe --json

# Start an Android Emulator (AVD) for offline ADB mirror, independent app windows, and fallback work
emulator -avd Pixel_9a_API_36

# Run live-device S1/S2/S4 spikes with a physical Samsung DeX phone attached (PowerShell)
$env:CGO_ENABLED="0"; $env:ANDROFUSE_LIVE_DEVICE="1"; go test -v ./internal/dex ./internal/scrcpy

# Reproduce the USB↔Wi-Fi handoff path by dropping the phone's TLS Wi-Fi transport (PowerShell)
$env:CGO_ENABLED="0"; $env:ANDROFUSE_LIVE_DEVICE="1"; go test -v -run TestLiveDeXSurvivesTransportDrop ./internal/dex

# First pairing over adb + mesh hand-over against a phone or emulator (tap Allow when prompted; PowerShell)
$env:CGO_ENABLED="0"; $env:ANDROFUSE_LIVE_COMPANION="emulator-5554"; go test -v -run TestLiveCompanionPairAndMeshJoin ./internal/gui

# Run full offline verification gate
gofmt -l cmd internal tools
go vet ./...
CGO_ENABLED=0 go test ./...
go tool mygo vet .
go tool mygo i18n check
go run ./tools/checkdocs
cargo fmt --manifest-path core/Cargo.toml --all --check
cargo clippy --manifest-path core/Cargo.toml --workspace --all-targets -- -D warnings
cargo test --manifest-path core/Cargo.toml --workspace
cargo deny --manifest-path core/Cargo.toml check licenses bans sources
```

## Release Signing

The Android Companion APK is signed with a fixed release keystore (PKCS12, alias `androfuse`, RSA 4096, certificate SHA-256 `07:10:13:27:F1:D5:B8:7C:63:95:13:DD:02:4C:63:66:BE:66:C2:A0:4B:0C:5A:63:55:EF:1B:09:DE:14:1B:5C`). `ci.yml` and `release.yml` read it from the repository secrets `ANDROFUSE_ANDROID_KEYSTORE` (base64), `ANDROFUSE_ANDROID_KEYSTORE_PASSWORD`, `ANDROFUSE_ANDROID_KEY_ALIAS` and `ANDROFUSE_ANDROID_KEY_PASSWORD`; the keystore and its password are kept in the maintainer's private data repository and never committed here. Never regenerate it: Android refuses updates signed with a different key. Local builds (`go run ./tools/buildapk`) stay debug-signed, so uninstall a debug build before installing a release one.

Windows releases are signed twice, both keys Ed25519 and kept in the maintainer's private data repository: `MYGO_UPDATER_PRIVATE_KEY` signs MyGo updates (its public half is `updates.publicKey` in `mygo.json`; installed apps only accept updates signed with it, so never regenerate it) and `ANDROFUSE_SIGNING_KEY` signs the helper bundle (`bundle.json` + `bundle.sig`, `tools/sign`). The release workflow builds the APK first and downloads it into `resources/windows/companion/`, so every Windows package ships the same-version Companion in `companion/` next to `androfuse.exe`; `scripts/build.sh` does the same with `build/AndroFuse-<version>-android-arm64.apk` when it exists.
