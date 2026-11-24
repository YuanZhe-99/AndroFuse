# 系统架构

## 进程模型

| 进程 | 二进制 | 运行身份 | 职责 |
|---|---|---|---|
| 桌面界面 | `AndroFuse`（MyGo） | 当前用户，永不提权 | 设备/模式/自检主页、系统托盘，以及分别承载 Samsung DeX、手机镜像和每个独立 Android 应用的原生 `mygo.Window`。 |
| 后台引擎 | `androfuse-svc` | 当前用户，子进程/服务 | 集中式设备发现（Miracast Wi-Fi Direct、TLS 无线 ADB、USB ADB）、单设备会话复用、虚拟显示与 UHID 租约账本，以及本地 AF_UNIX NDJSON API（M1+）。 |
| Android 辅助端 | `AndroFuse`（Android APK，M3+） | Android 用户 | 可选的同仓 MyGo + Kotlin（`//mygo:native`）+ Rust 核心应用，提供应用图标清单、通知同步、剪贴板协助、SAF 文件传输与断线显示回收。 |

## 包与 Crate 结构

| 包 / Crate | 职责 |
|---|---|
| `cmd/androfuse` | 统一 MyGo 原生 UI 入口（Windows 启用 `mygo_nowebview`） |
| `cmd/androfuse-svc` | 无头引擎命令行（`version`、`check [--json]`；M1+ 新增 `run`） |
| `internal/version` | 全局唯一产品版本号（`Version`），测试强制与 `mygo.json` 一致 |
| `internal/core` | 通过 `LoadLibraryEx`（Windows）或 `purego`（Unix/Android）加载 `androfuse_core` |
| `internal/gui` | MyGo 原生 UI 外壳、设计 Token、桌面/移动自适应布局、托盘与主机诊断 |
| `locales` | 英文（`en`）与简体中文（`zh-Hans`）ARB 词条目录及生成访问器 |
| `tools/icon` | 从 `resources/icon.svg` 渲染桌面、UI、托盘与 Android 自适应图标 PNG |
| `tools/checkdocs` | 校验 `doc/en-us/` 与 `doc/zh-cn/` 的 1:1 结构镜像一致性 |
| `core/androfuse-core` | Rust 2024 `cdylib`/`rlib` 核心库：C ABI v1 与被动主机自检（M0）；ADB/scrcpy 协议、Media Foundation NV12 解码、WASAPI 与 Miracast Sink（M1–M2） |

## 规划中的运行时模块（M1–M3）

运行时架构严格分离以下八个内聚职责（当前 M0 不创建空壳占位包，待各里程碑落地实现）：

1. **MyGo UI（`internal/gui`）**：设备卡片、三模式入口、独立应用原生窗口、浮动性能 HUD、设置、日志、托盘与 Ed25519 自动更新。
2. **设备与会话多路复用器（`androfuse-svc`）**：统一 ADB 设备追踪、单设备端口/SCID 分配、单路音频仲裁与持久化 `leases.json` 租约账本。
3. **三种连接后端**：Miracast / Wi-Fi Direct（不强求开启 USB 调试）、TLS 无线 ADB（`_adb-tls-pairing._tcp` / `_adb-tls-connect._tcp`）与 USB ADB。
4. **Samsung DeX 引擎**：Miracast 接收端 + ADB `SemWifiDisplayConfig` 回环（`127.0.0.1:7236`），以及基于 One UI 版本探测的虚拟/模拟显示回退。
5. **手机镜像引擎**：主屏（Display `0`）低延迟视频、键鼠触控、音频、剪贴板、横竖屏、截图录屏与亮屏/熄屏投屏控制。
6. **独立 APP 窗口引擎**：每应用独立 `mygo.NewWindow` + `scrcpy-server` 虚拟显示（`--new-display` / `--flex-display` / `--start-app`）、Snap Layouts 动态重设尺寸与单实例迁移提示。
7. **原生音视频管线**：Windows Media Foundation 硬件解码 H.264/HEVC NV12 直通 MyGo D3D11 `ui.Bitmap.UpdateYUV`（零 JS 转发、零每帧分配）与 WASAPI 音频播放。
8. **Android Companion（M3 `internal/androidhost`）**：同仓构建的可选签名 APK，采用分级可关闭权限开关。

## FFI 边界

`androfuse-core` 导出带版本校验的 C ABI（`androfuse_abi_version() -> u32`，当前为 `1`）：

| 导出函数 | 作用 |
|---|---|
| `androfuse_abi_version() -> u32` | 返回 ABI 版本号（`1`）；Go 侧不匹配则拒绝加载 |
| `androfuse_start(json, len, *out, *out_len) -> i32` | 以 `{version, state_dir, profile?}` 初始化核心库 |
| `androfuse_call(json, len, *out, *out_len) -> i32` | 分发 `{method, params}` 请求（`status`、`diagnostics.self_check`） |
| `androfuse_free(ptr, len)` | 释放 `androfuse_start` / `androfuse_call` 分配的响应缓冲区 |
| `androfuse_stop()` | 停止核心库并清理内存状态 |

在 Windows 上，`internal/core` 使用 `LoadLibraryEx(LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32)` 按绝对路径加载 `androfuse_core.dll`。响应槽位通过堆上 `sync.Pool` 分配，防止 Go 协程栈迁移导致指针失效。

## 状态目录

| 路径 | 用途 |
|---|---|
| `%APPDATA%\AndroFuse\`（`AndroFuse-<profile>`） | 用户级界面偏好（`gui.json`）与运行日志（`logs/`） |
| `%LOCALAPPDATA%\AndroFuse\`（M1+） | 设备配置、DPAPI 加密凭据与虚拟显示租约账本（`leases.json`） |
