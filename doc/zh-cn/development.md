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

## 发布签名

Android 伴侣 APK 使用固定的发布密钥库签名（PKCS12，别名 `androfuse`，RSA 4096，证书 SHA-256 `07:10:13:27:F1:D5:B8:7C:63:95:13:DD:02:4C:63:66:BE:66:C2:A0:4B:0C:5A:63:55:EF:1B:09:DE:14:1B:5C`）。`ci.yml` 与 `release.yml` 从仓库机密 `ANDROFUSE_ANDROID_KEYSTORE`（base64）、`ANDROFUSE_ANDROID_KEYSTORE_PASSWORD`、`ANDROFUSE_ANDROID_KEY_ALIAS`、`ANDROFUSE_ANDROID_KEY_PASSWORD` 读取；密钥库与口令另存于维护者的私有数据仓库，永不提交到本仓库。切勿重新生成：Android 会拒绝用不同密钥签名的更新。本机构建（`go run ./tools/buildapk`）仍使用调试签名，安装发布版前需先卸载调试版。

Windows 发布包有两重签名，两把密钥均为 Ed25519，保存在维护者的私有数据仓库：`MYGO_UPDATER_PRIVATE_KEY` 为 MyGo 更新签名（公钥即 `mygo.json` 中的 `updates.publicKey`；已安装的应用只接受用它签名的更新，切勿重新生成），`ANDROFUSE_SIGNING_KEY` 为辅助程序包签名（`bundle.json` + `bundle.sig`，`tools/sign`）。发布流程先构建 APK，再把它下载到 `resources/windows/companion/`，因此每个 Windows 安装包都在 `androfuse.exe` 旁的 `companion/` 中附带同版本伴侣；`scripts/build.sh` 在 `build/AndroFuse-<版本>-android-arm64.apk` 存在时也会这样做。
