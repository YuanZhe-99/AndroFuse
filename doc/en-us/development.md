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

# Download and SHA-256 verify scrcpy-server-v5.0.1 into .dev/cache/
go run ./tools/fetchdeps

# Compile server/dexhelper into build/androfuse-dex-helper.jar
go run ./tools/builddex

# Regenerate i18n Go catalog accessors after editing locales/*.json
go tool mygo generate

# Build the Rust core library (debug/release)
cargo build --manifest-path core/Cargo.toml --workspace

# Build and launch the interactive desktop client into build/
Copy-Item core/target/debug/androfuse_core.dll build/androfuse_core.dll
go build -tags mygo_nowebview -o build/androfuse.exe ./cmd/androfuse

# Run host self-check and ADB / Samsung DeX capability probes
go run ./cmd/androfuse-svc probe --json

# Start an Android Emulator (AVD) for offline ADB mirror, independent app windows, and fallback work
emulator -avd Pixel_9a_API_36

# Run live-device S1/S2/S4 spikes with a physical Samsung DeX phone attached (PowerShell)
$env:CGO_ENABLED="0"; $env:ANDROFUSE_LIVE_DEVICE="1"; go test -v ./internal/dex ./internal/scrcpy

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
