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
| `cmd/androfuse-svc` | 无头引擎命令行（`version`、`check [--json]`、`probe [--json]`、`run [--socket <path>]`） |
| `internal/version` | 全局唯一产品版本号（`Version`），测试强制与 `mygo.json` 一致 |
| `internal/core` | `androfuse_core` C ABI v6 加载器、`NV12Ring` 零分配帧环、`AudioPlayer` 与 `MicCapturer` |
| `internal/localapi` | 同用户 AF_UNIX NDJSON 服务（`SIO_AF_UNIX_GETPEERPID` + Windows SID 校验），提供含 `unified_devices` 的设备列表与 `vm.*` 虚拟机控制 |
| `internal/vm` | 按需下载的轻量无头 Android 虚拟机管理器（`%LOCALAPPDATA%\AndroFuse\vm`，原生 `arm64-v8a` / `x86_64`，`INV-03`、`INV-10`） |
| `internal/companion` | 桌面与 Android APK 共用的 TLS 1.3 加密 Companion NDJSON 协议（遥测、剪贴板、通知回复、文件传输、应用接力、显示守护与 ADB 信标；`INV-07`、`INV-09`）；`pair.request`（仅限 adb 转发：本地套接字或回环；两边核对 6 位代码）与 `mesh.join`；`tunnel.open` 把连接变成通往手机自身无线调试端口的管道（`ServeTunnels`，adb 在局域网或跨网络都能连上，不必每个端口建一个转发）；`share.open` 以 scrcpy 帧格式传送手机屏幕，`input.key` 按返回、主屏幕或最近任务（移动网络下的屏幕共享） |
| `internal/androidhost` | 可选 Android 伴侣端宿主服务、配置持久化、sysfs 电量/温控探针、脱敏审计日志与 MyGo 分级授权界面（`INV-09`、`INV-11`）；可选后台待机与仅在传输期间持有的唤醒/Wi-Fi 活动租约；加入配对电脑交给它的组网（密钥用 Android Keystore 密封，启动时自动重新加入）并显示 adb 配对确认；三标签伴侣界面（主页、权限、安全；680 dp 起双栏），组网默认省电（单运行时线程、按需 P2P、关闭 UPnP）；自己发现无线调试端口（NsdManager）而不再让用户填写，只把隧道接到确认应答 STLS 的这个端口，并在 Android 同意后于移动网络下共享屏幕（权限默认关闭） |
| `internal/androidbridge` | 平台安全的 Android 系统服务能力探测包，与 `CompanionServices.kt`（`NotificationListenerService`、`TileService`、`MediaProjection`、`SAF`、`AppWidgetProvider`）配合；MyGo 移动插件 `androfusePower`（`android/Power.kt`：待机前台服务、`NetworkCallback` 事件、活动租约、用 NsdManager 发现本机的 `_adb-tls-connect._tcp`；`android/Share.kt`：在 `mediaProjection` 前台服务中用 MediaProjection + MediaCodec H.264 共享屏幕，经抽象套接字 `androfuse-share` 输出，另有提供返回、主屏幕和最近任务的无障碍服务） |
| `internal/androidcore` | MyGo Kotlin 原生模块：把内置 EasyTier 的 `libandrofuse_core.so`（`scripts/build-android-core.sh` 构建）打进 APK 并加载，另提供 Android Keystore 密封（`Vault`） |
| `internal/avbridge` | M5 双向音视频桥接：手机摄像头本地防越权 MJPEG 流、系统 `MFCreateVirtualCamera` 探测与加密 PC→Android 麦克风/摄像头传入链路 |
| `internal/mcp` | 默认关闭的本机回环（`127.0.0.1`）HTTP JSON-RPC 2.0 本地 MCP 服务，带 30 分钟临时密钥、紧急停止与脱敏审计（`INV-07`、`INV-08`）；白名单列出无需询问即可运行的工具，其他每次调用都等待用户「允许一次」或「拒绝」（`Handlers.Approve`，窗格上方的卡片） |
| `internal/mesh` | 内置于核心库、默认关闭的 EasyTier v2.7.1 组网客户端（`androfuse_call` `mesh.*`；无 TUN，仅回环端口转发；`INV-07`、`INV-08`）；每个网络一个命名实例（`Config.Instance`），`Info` 提供本机组网地址、NAT 类型，以及每个对端的地址、直连或中继和延迟 |
| `internal/bundlesig` | 沿用 Kyokai 规范的 Ed25519 辅助程序包清单（`bundle.json` + `bundle.sig`）签名与校验包 |
| `internal/adb` | 无 Shell 参数向量 `adb` 客户端、`MergeDevices`（USB 与 TLS 无线去重合并，`INV-02`）、mDNS TLS 无线连接配对、`MaskSerial`/`MaskEndpoint` 脱敏、`emulator-*` 识别、`--user 0` 探测与应用列表；mDNS `adb-<ro.serialno>-…` 实例映射到 `HardwareHash` 用于热切换重连；手机的营销名称、品牌、自己的名称与模拟器标记；`ProbeADB`（CNXN 握手：无线调试、未加密 adb 或都不是）与 `FindWirelessPort`（在一个地址上查找无线调试使用的端口范围）；`ExecOut` 取原始输出（如截图） |
| `internal/sysproc` | `HideConsole`：所有控制台子进程（adb、emulator、sdkmanager）均以 `CREATE_NO_WINDOW` 启动，不再弹出控制台或 Windows Terminal 窗口 |
| `internal/lease` | 原子 `leases.json` 账本，确保虚拟显示、DeX 回环与端口转发完整回收（`INV-03`） |
| `internal/scrcpy` | `scrcpy-server v5.0.1` 线协议编解码、鼠标/触控/滚动/键盘与 `flex_display` 会话、原始 PCM 音频、`MP4Recorder`、离线样本与实测遥测（`Stats`：已解码帧数、平均解码耗时，以及在 1 秒内没有新帧时归零的实测帧率，因为静止画面不发帧；`INV-05`） |
| `internal/dex` | Samsung Native DeX 调度器（`server/dexhelper` 回环 + 30 秒宽限 `Reattach`）、真实应用名称与 96x96 PNG 图标缓存（`ListAppsWithIcons`）、`SelectRoute` 及 `EvaluateTruth` 校验（`INV-06`；Miracast 已隐藏） |
| `internal/gui` | 参照 Kyokai 的 MyGo 原生界面外壳（design/README.md）：侧栏铺在窗口材质上（窄于 960 DIP 时为图标条），含主页、按显示方式分组的已打开窗格与独立窗口、网络状态、搜索、外观和设置；浮动窗格每个只有一个 44 DIP 标题栏（手机按键与工具、实测帧率标签及详情弹层、弹出、分屏、关闭，窗口按钮画在右上窗格；`INV-05`）；窗格上方的卡片用于等待用户的事（配对代码、本机助手的请求、第一次 USB 连接）；主页含「这台电脑」、「连接手机」（填地址或名称，端口自动查找：记住的端口、伴侣，再在无线调试的端口范围中查找）和每台手机一张卡片（自己的或本机起的名字、厂商标志、连接方式、伴侣实时状态、操作），以及已加入网络中的手机；设置为带搜索的面板（常规、手机与画面、摄像头与麦克风、网络（最多 8 个 EasyTier 网络）、电脑上的 Android、安全与隐私、关于与更新（MyGo 签名更新））；+ 选择器与 Ctrl K 搜索；托盘菜单每台手机一个子菜单；`SessionWindow` NV12 视频、经 adb 首次配对、伴侣重连与无线调试隧道、伴侣屏幕共享（`SessionModeShare`）；记住的手机保存在 `devices.json`（本机名称、厂商、自动重连、可撤销的忘记） |
| `locales` | 英文（`en`）与简体中文（`zh-Hans`）ARB 词条目录及生成访问器 |
| `server/dexhelper` | 洁净室 Java UID 2000 `app_process` 辅助器（`probe`、`apps`、`serve`、`attach`、`recover`），驱动 `SemWifiDisplayConfig` 回环、宽限期控制端重附着与图标提取 |
| `tools/fetchdeps` | 下载并校验 SHA-256 后缓存 `scrcpy-server-v5.0.1` 与构建期 `protoc` 36.2 至 `.dev/cache/`；`-check-upstream` 报告更新的 EasyTier 版本 |
| `tools/builddex` | 编译并打包 `server/dexhelper` 为 `build/androfuse-dex-helper.jar` |
| `tools/buildapk` | 构建 `arm64-v8a` Android Companion APK：在 Windows 上补充 `gradlew.bat`（将 MyGo 的 `ORG_GRADLE_PROJECT_*` 属性转为 `-P` 参数）、优先使用 JDK 17，缺少 `lib/arm64-v8a/*.so` 时报错，并校验 `zipalign -c -P 16 4` |
| `tools/sign` | Ed25519 发布密钥生成与辅助包签名命令行工具（`ANDROFUSE_SIGNING_KEY`，与 Kyokai 同格式） |
| `tools/checkpe` | 校验所有 `.exe` 与 `.dll` 是否为目标原生 PE 机器码（`0xAA64` / `0x8664`，`INV-10`），并校验 `androfuse.exe` 使用 Windows GUI 子系统 |
| `tools/icon` | 从 `resources/icon.svg` 渲染桌面、UI、托盘与 Android 自适应图标 PNG |
| `tools/checkdocs` | 校验 `doc/en-us/` 与 `doc/zh-cn/` 的 1:1 结构镜像一致性 |
| `core/androfuse-core` | Rust 2024 `cdylib`/`rlib` 核心库：C ABI v6、主机自检、内置 EasyTier 组网（`mesh.rs`，经上游 `easytier-ffi`）、`scrcpy v5.0.1` 协议、Media Foundation DXVA NV12 解码（`mf_video.rs`）、WASAPI 音频播放与麦克风采集（`wasapi_audio.rs`）及已隐藏的 Miracast 协议模块（`miracast.rs`） |

## 规划中的运行时模块（M2–M4）

1. **USB 拔线热切换与遥测复用（M2 核心已落地；M3 补全遥测推送）**：通过 `ro.serialno` 哈希聚合 `UnifiedDevice`（`INV-02`），并在断线 30 秒宽限期内经由配对 TLS 无线 ADB 执行 `dexhelper attach` 回环重连；`internal/localapi` 后台遥测推流将在 M3 扩展。
2. **Android Companion（M3 `internal/androidhost`）**：同仓构建的可选签名 APK，采用分级可关闭权限开关。
3. **跨网络与 5G 远程传输（`M3–M4`）**：跨 Wi-Fi 子网借助用户 VPN 或用户态（`--no-tun`）EasyTier 组网，由 Companion 自动广播 `_adb-tls-connect._tcp` 动态端口以运行完整 `scrcpy`/DeX；纯 5G 蜂窝网络（原厂非 Root Android 禁止启用 `adbd` 无线调试）则由 Companion 通过加密隧道提供 `MediaProjection` H.264/HEVC 视频与可选 `AccessibilityService` 输入通道。

## FFI 边界

`androfuse-core` 导出带版本校验的 C ABI（`androfuse_abi_version() -> u32`，当前为 `6`）：

| 导出函数 | 作用 |
|---|---|
| `androfuse_abi_version() -> u32` | 返回 ABI 版本号（`6`）；Go 侧不匹配则拒绝加载 |
| `androfuse_start(json, len, *out, *out_len) -> i32` | 以 `{version, state_dir, profile?}` 初始化核心库 |
| `androfuse_call(json, len, *out, *out_len) -> i32` | 分发 `{method, params}` 请求（`status`、`diagnostics.self_check`、`miracast.test_sink`、`mesh.start`、`mesh.stop`、`mesh.status`） |
| `androfuse_free(ptr, len)` | 释放 `androfuse_start` / `androfuse_call` 分配的响应缓冲区 |
| `androfuse_stop()` | 停止核心库、销毁活跃解码器、音频播放器、麦克风采集器与 Miracast 会话并清理内存状态 |
| `androfuse_decoder_open(codec_id, w, h, *out_handle) -> i32` | 打开 Media Foundation H.264/HEVC 解码器（优先 `MF_SA_D3D11_AWARE` 硬解） |
| `androfuse_decoder_push(handle, data, len, pts_us, flags) -> i32` | 送入 Annex-B 帧单元（`flags` 第 0 位为不连续标记，第 1 位触发立即 drain） |
| `androfuse_decoder_pull_nv12(handle, dst, dst_cap, *out_meta) -> i32` | 将解码后 NV12 平面写入调用方环形缓冲区（`0` 成功、`1` 需扩容、`2` 需更多输入） |
| `androfuse_decoder_close(handle)` | 关闭解码器句柄并释放 Media Foundation / D3D11 资源 |
| `androfuse_miracast_start(rtsp_port, *out_handle) -> i32` | 运行期已隐藏禁用（依赖未公开 `WlanNotifyVsIeProviderInt` 接口） |
| `androfuse_miracast_poll_nv12(handle, dst, dst_cap, *out_meta, *out_state) -> i32` | 拉取解码后 Miracast NV12 平面（已隐藏禁用） |
| `androfuse_miracast_input(handle, action, x_norm, y_norm) -> i32` | 发送 UIBC `GENERIC` 指针控制包（已隐藏禁用） |
| `androfuse_miracast_stop(handle)` | 关闭已打开的 Miracast 句柄（已隐藏禁用） |
| `androfuse_audio_open(sample_rate, channels, *out_handle) -> i32` | 打开共享模式 WASAPI PCM 播放流（`48000 Hz` 双声道 s16le） |
| `androfuse_audio_push_pcm(handle, data, len) -> i32` | 将交错 s16le PCM 音频帧写入有界 WASAPI 环形缓冲区 |
| `androfuse_audio_close(handle)` | 关闭 WASAPI 音频播放器句柄并回收渲染线程 |
| `androfuse_mic_open(sample_rate, channels, *out_handle) -> i32` | 打开共享模式 WASAPI 麦克风 PCM 采集流（`48000 Hz` 单/双声道 s16le） |
| `androfuse_mic_read_pcm(handle, dst, dst_cap, *out_len) -> i32` | 从 WASAPI 采集缓冲区复制捕获的 s16le PCM 字节至 `dst` 并写入 `*out_len` |
| `androfuse_mic_close(handle)` | 关闭 WASAPI 麦克风采集句柄并回收采集线程 |

在 Windows 上，`internal/core` 使用 `LoadLibraryEx(LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32)` 按绝对路径加载 `androfuse_core.dll`。响应槽位通过堆上 `sync.Pool` 分配，防止 Go 协程栈迁移导致指针失效。

## 状态目录

| 路径 | 用途 |
|---|---|
| `%APPDATA%\AndroFuse\`（`AndroFuse-<profile>`） | 用户级界面偏好（`gui.json`）、已记住的设备（`devices.json`，伴侣令牌经 DPAPI 密封）与运行日志（`logs/`） |
| `%LOCALAPPDATA%\AndroFuse\` | 设备配置、DPAPI 加密凭据与虚拟显示租约账本（`leases.json`） |
| `%LOCALAPPDATA%\AndroFuse\vm\` | 按需下载的可选内建 Android 虚拟机（`sdk/` 与 `avd/`），支持通过虚拟机管理器一键清理 |
