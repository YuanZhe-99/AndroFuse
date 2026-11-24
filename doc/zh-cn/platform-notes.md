# 平台验证记录

本文件只记录经过实际构建和运行的项；未在真机或目标架构上运行验证的功能一律明确标注为未验证。

## Windows 11 ARM64（`windows/arm64`）

| 模块 | 状态 | 实测证据 |
|---|---|---|
| Rust 核心库（`core/androfuse-core`） | 已验证（M0） | 在 `aarch64-pc-windows-msvc`（`rustc 1.99.1`）原生编译，通过 `cargo clippy -- -D warnings`、`cargo test` 与 `cargo deny check licenses bans sources` |
| Go ↔ Rust C-ABI（`internal/core`）与 `androfuse-svc check` | 已验证（M0） | 通过 `LoadLibraryEx` 加载真实 `androfuse_core.dll`，确认 `native_arch = "arm64"`、`is_emulated = false`、`os_build = 26300` 及真实 Media Foundation / `wlanapi.dll` 探测 |
| MyGo 原生界面（`internal/gui`） | 已验证（M0） | 通过 `CGO_ENABLED=0 go test ./...`、`go tool mygo vet .` 与 `go tool mygo i18n check`；`ui.NewTester` 无头渲染全部 5 个页面（`en`/`zh-Hans`、深浅色、`1080x700`/`412x915`） |
| ADB 手机镜像与独立 APP 窗口 | 尚未实现（M1） | 待 M1 与 Spike S1 实施 |
| Miracast 与 ADB 原生 Samsung DeX | 尚未实现（M2） | 待 M2 与 Spike S2–S3 实施 |

## Windows 11 x64（`windows/amd64`）与 Windows 10

| 目标 | 状态 | 说明 |
|---|---|---|
| Windows 11 `x64` | 尚未实机验证 | M4 阶段与原生 `ARM64` 同等作为首版发布门槛 |
| Windows 10（`19045+`） | 待评估（M4） | MyGo D3D11 与 Media Foundation 可运行（无 Mica 材质回退为纯色）；Wi-Fi Direct Miracast 接收依赖网卡 NDIS/WDDM 驱动 |

## Android 设备与 Companion APK

| 目标 | 状态 | 说明 |
|---|---|---|
| 三星 Galaxy DeX（One UI 6/7/8/9） | 尚未真机验证 | 规划于 Spike S2–S4 与 M1–M2 |
| 普通 Android 手机（Android 11–17） | 尚未真机验证 | 规划于 M1 |
| Android Companion APK（`arm64-v8a`） | 尚未构建（M3） | M0 已预备自适应图标前景层与 `mygo.json` Android 配置 |
