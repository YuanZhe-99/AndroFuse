# 系统架构

## 进程模型

| 进程 | 二进制 | 运行身份 | 职责 |
|---|---|---|---|
| 桌面界面 | `AndroFuse`（MyGo） | 当前用户，永不提权 | 设备/模式/自检主页、系统托盘，以及分别承载 Samsung DeX、手机镜像和每个独立 Android 应用的原生 `mygo.Window`。 |
| 后台引擎 | `androfuse-svc` | 当前用户，子进程/服务 | M1a 阶段负责主机、ADB 与 DeX 能力探测（`probe`、`check`）；M1b+ 阶段负责集中式设备发现、单设备复用、`leases.json` 守护清理（`run`）及本地 AF_UNIX NDJSON API。 |
| Android 辅助端 | `AndroFuse`（Android APK，M3+） | Android 用户 | 可选的同仓 MyGo + Kotlin（`//mygo:native`）+ Rust 核心应用，提供应用图标清单、通知同步、剪贴板协助、SAF 文件传输与断线显示回收。 |

## 包、Crate 与设备端辅助程序

| 包 / Crate | 职责 |
|---|---|
| `cmd/androfuse` | 统一 MyGo 原生 UI 入口（Windows 启用 `mygo_nowebview`） |
| `cmd/androfuse-svc` | 无头引擎命令行（`version`、`check [--json]`、`probe [--json]`；M1b+ 新增 `run`） |
| `internal/version` | 全局唯一产品版本号（`Version`），测试强制与 `mygo.json` 一致 |
| `internal/core` | `androfuse_core` C ABI v3 加载器与 `NV12Ring` 零分配帧环 |
| `internal/adb` | 无 Shell 参数向量 `adb` 客户端、mDNS TLS 无线连接配对、`MaskSerial`/`MaskEndpoint` 脱敏、`emulator-*` 分类与 `--user 0` 应用枚举 |
| `internal/lease` | 原子 `leases.json` 账本，确保虚拟显示、DeX 回环与端口转发完整回收（`INV-03`） |
| `internal/scrcpy` | `scrcpy-server v5.0.1` 线协议编解码、鼠标/触控/滚动/键盘与 `flex_display` 会话及离线样本 |
| `internal/dex` | Samsung Native DeX 调度器（有线 USB / 加密 TLS Wi-Fi ADB `server/dexhelper` 回环）与 `EvaluateTruth` 校验（`INV-06`；Miracast 接收器已隐藏） |
| `internal/gui` | MyGo 原生 UI 外壳、交互式 `SessionWindow` 视频窗口、`InputTracker`、无线/应用启动界面、托盘与主机诊断 |
| `locales` | 英文（`en`）与简体中文（`zh-Hans`）ARB 词条目录及生成访问器 |
| `server/dexhelper` | 洁净室 Java UID 2000 `app_process` 辅助器（`probe`、`serve`、`recover`），驱动 `SemWifiDisplayConfig` 回环 |
| `tools/fetchdeps` | 从上游下载并校验 SHA-256 后缓存 Apache-2.0 `scrcpy-server-v5.0.1` 至 `.dev/cache/` |
| `tools/builddex` | 编译并打包 `server/dexhelper` 为 `build/androfuse-dex-helper.jar` |
| `tools/icon` | 从 `resources/icon.svg` 渲染桌面、UI、托盘与 Android 自适应图标 PNG |
| `tools/checkdocs` | 校验 `doc/en-us/` 与 `doc/zh-cn/` 的 1:1 结构镜像一致性 |
| `core/androfuse-core` | Rust 2024 `cdylib`/`rlib` 核心库：C ABI v3、主机自检、`scrcpy v5.0.1` 协议、Media Foundation DXVA NV12 解码（`mf_video.rs`）及已隐藏的 Miracast 协议模块（`miracast.rs`） |

## 规划中的运行时模块（M1b–M4）

1. **后台守护与 IPC（`androfuse-svc run`、`internal/localapi`）**：AF_UNIX NDJSON 服务（校验 `SIO_AF_UNIX_GETPEERPID`）、USB 拔线切换 TLS 无线续接与后台遥测复用。
2. **音视频与输入完善（M1b）**：WASAPI 音频播放、双向剪贴板同步、PNG 截图、MP4 录屏。
3. **Android Companion（M3 `internal/androidhost`）**：同仓构建的可选签名 APK，采用分级可关闭权限开关。
4. **跨网络与 5G 远程传输（`M3–M4`）**：跨 Wi-Fi 子网借助用户 VPN 或用户态（`--no-tun`）EasyTier 组网，由 Companion 自动广播 `_adb-tls-connect._tcp` 动态端口以运行完整 `scrcpy`/DeX；纯 5G 蜂窝网络（原厂非 Root Android 禁止启用 `adbd` 无线调试）则由 Companion 通过加密隧道提供 `MediaProjection` H.264/HEVC 视频与可选 `AccessibilityService` 输入通道。

## FFI 边界

`androfuse-core` 导出带版本校验的 C ABI（`androfuse_abi_version() -> u32`，当前为 `3`）：

| 导出函数 | 作用 |
|---|---|
| `androfuse_abi_version() -> u32` | 返回 ABI 版本号（`3`）；Go 侧不匹配则拒绝加载 |
| `androfuse_start(json, len, *out, *out_len) -> i32` | 以 `{version, state_dir, profile?}` 初始化核心库 |
| `androfuse_call(json, len, *out, *out_len) -> i32` | 分发 `{method, params}` 请求（`status`、`diagnostics.self_check`、`miracast.test_sink`） |
| `androfuse_free(ptr, len)` | 释放 `androfuse_start` / `androfuse_call` 分配的响应缓冲区 |
| `androfuse_stop()` | 停止核心库、销毁活跃解码器/Miracast 会话并清理内存状态 |
| `androfuse_decoder_open(codec_id, w, h, *out_handle) -> i32` | 打开 Media Foundation H.264/HEVC 解码器（优先 `MF_SA_D3D11_AWARE` 硬解） |
| `androfuse_decoder_push(handle, data, len, pts_us, flags) -> i32` | 送入 Annex-B 帧单元（`flags` 第 0 位为不连续标记，第 1 位触发立即 drain） |
| `androfuse_decoder_pull_nv12(handle, dst, dst_cap, *out_meta) -> i32` | 将解码后 NV12 平面写入调用方环形缓冲区（`0` 成功、`1` 需扩容、`2` 需更多输入） |
| `androfuse_decoder_close(handle)` | 关闭解码器句柄并释放 Media Foundation / D3D11 资源 |
| `androfuse_miracast_start(rtsp_port, *out_handle) -> i32` | 运行期已隐藏禁用（依赖未公开 `WlanNotifyVsIeProviderInt` 接口） |
| `androfuse_miracast_poll_nv12(handle, dst, dst_cap, *out_meta, *out_state) -> i32` | 拉取解码后 Miracast NV12 平面（已隐藏禁用） |
| `androfuse_miracast_input(handle, action, x_norm, y_norm) -> i32` | 发送 UIBC `GENERIC` 指针控制包（已隐藏禁用） |
| `androfuse_miracast_stop(handle)` | 关闭已打开的 Miracast 句柄（已隐藏禁用） |

在 Windows 上，`internal/core` 使用 `LoadLibraryEx(LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32)` 按绝对路径加载 `androfuse_core.dll`。响应槽位通过堆上 `sync.Pool` 分配，防止 Go 协程栈迁移导致指针失效。

## 状态目录

| 路径 | 用途 |
|---|---|
| `%APPDATA%\AndroFuse\`（`AndroFuse-<profile>`） | 用户级界面偏好（`gui.json`）与运行日志（`logs/`） |
| `%LOCALAPPDATA%\AndroFuse\` | 设备配置、DPAPI 加密凭据与虚拟显示租约账本（`leases.json`） |
