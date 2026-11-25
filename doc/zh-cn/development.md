# 开发指南

## 环境要求

- **Go**：`1.27.4`（桌面端纯 Go 构建，`CGO_ENABLED=0`）
- **MyGo**：`github.com/egoist/mygo v0.10.0`（通过 `go tool mygo` 调用）
- **Rust**：`1.99.1`（`rust-toolchain.toml`），含 `rustfmt`、`clippy` 与 `cargo-deny`
- **Windows 工具链**：Visual Studio / MSVC（支持 `aarch64-pc-windows-msvc` 与 `x86_64-pc-windows-msvc`）
- **Android（`M1a` DeX 辅助器与 `M3+` Companion）**：Android SDK（API 36）、`build-tools`（`d8`）、NDK 与 JDK 17+

## 常用命令

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
