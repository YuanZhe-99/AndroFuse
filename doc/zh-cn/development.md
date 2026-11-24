# 开发指南

## 环境要求

- **Go**：`1.27.4`（桌面端纯 Go 构建，`CGO_ENABLED=0`）
- **MyGo**：`github.com/egoist/mygo v0.10.0`（通过 `go tool mygo` 调用）
- **Rust**：`1.99.1`（`rust-toolchain.toml`），含 `rustfmt`、`clippy` 与 `cargo-deny`
- **Windows 工具链**：Visual Studio / MSVC（支持 `aarch64-pc-windows-msvc` 与 `x86_64-pc-windows-msvc`）
- **Android（M3+）**：Android SDK（API 36）、NDK 与 JDK 17+

## 常用命令

```bash
# Generate PNG icons from resources/icon.svg
go run ./tools/icon

# Regenerate i18n Go catalog accessors after editing locales/*.json
go tool mygo generate

# Build the Rust core library (debug/release)
cargo build --manifest-path core/Cargo.toml --workspace

# Run host self-check diagnostics via the headless engine
go run ./cmd/androfuse-svc check --json

# Run full local verification gate
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
