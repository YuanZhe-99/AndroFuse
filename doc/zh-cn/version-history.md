# 版本历史

## v0.2.2（2026-11-27）

- **增强协同（Companion）配对与会话安全加固（`INV-07`、`INV-09`）**：
  - 新增基于 TLS 1.3 `ExportKeyingMaterial` 的会话绑定 6 位校验码（`DerivePairSAS` / `RequestPairingSAS`），配合手机端乱序三选一数字确认按钮（`AnswerPairChoice`，通知正文隐藏校验码、过滤 `ClientName` 控制字符与换行注入、连续失败自动冷却）以及 `@androfuse-companion` 内核 `SO_PEERCRED` UID 校验。
  - 将配对令牌从 40 位升级为 160 位（32 字符）密码学随机串（`GeneratePairingToken`），移除硬编码兜底令牌，限制未鉴权并发握手数（32）与鉴权前单行帧上限（`8 KiB`），并增加按源 IP 维度的认证失败限流。
  - 将 `daemon.open`、`daemon.ctrl` 与 `apps.list` 统一纳入手机端 `ScreenShare` 开关门控，将 `daemon.ctrl` 控制通道绑定至发起电脑的令牌摘要；关闭 `ADBBeacon` 时在 `status.get` / `pair.res` 中一并脱敏 `WirelessADBPairPort` 与 `WifiIPv4` 并为配对端口设置 3 分钟 TTL；阻止远端 `mesh.join` 静默覆盖已加入的其他网络；`Daemon.java` 对 `/data/local/tmp` 助手与服务端 JAR 强制执行 `chmod 600` 与启动前 SHA-256 完整性锁定。
- **修复应用内更新无法自动退出并重启的问题（`internal/gui/update.go`）**：`InstallUpdate` 现在在替换文件前先安全停止内置 `platform-tools/adb.exe` 并暂停设备轮询，在更新完成后设置 `App.quitting = true`，避免关闭到托盘拦截窗口关闭事件，确保 `mygo.App.Relaunch()` 顺利退出并重启至新版本。

## v0.2.1（2026-11-27）

- **零配置托管 ADB（`internal/adb` 与发行打包）**：Windows 发行包现在将官方 Android Platform Tools 随附于 `resources/windows/platform-tools/`（含 `NOTICE.txt` 且与 `bin/` 隔离），同时桌面端通过 `adb.FindClient` 与 `adb.InstallPlatformTools` 在普通用户电脑缺少 ADB 时自动经 Google HTTPS 下载并安全校验（防 Zip-Slip）解压；主页在工具就绪时保持安静，仅在缺失或下载中显示清晰提示与重试入口。
- **免 ADB 跨网络首次配对与事后无损合并（`PairMeshPhone`）**：新增 `PairMeshPhone` 与 `rememberMeshPhone` / `mergeMeshRememberedLocked`，使已加入 EasyTier 组网且尚未插线的手机可直接经组网发起首次配对，手机端弹出 `IMPORTANCE_HIGH` 悬浮通知（`Power.showPairNotification`）并随 `PairResponse` 返回身份与实时状态，桌面先记入临时 `mesh:<devID>`，日后连上 ADB 时无损合并为真实硬件哈希（`ro.serialno`）。
- **手机端 ADB 状态反馈与按需一次拉起增强协同**：新装伴侣按 `PermDefaultsV2` 默认开启遥测、无线调试信标与移动网络投屏；`Power.kt` 上报 `USBDebugOn`、`WirelessDebugOn` 及 NSD 发现的 `_adb-tls-pairing._tcp` 配对端口（`WirelessADBPairPort`），`tunnel.open` 支持连接与配对双重端口转发；桌面通过静默连接或实时轮询的「开启增强协同」弹窗（`EnsureEnhancedAndRun` / `ActivatePairAndBootstrap`）一次引导拉起 `dex.StartDaemon`。
- **面向普通用户的界面与文案重制**：重构精简主页（「我的手机」标题栏、USB 授权提示、免裸 IP 的自动「发现的新手机」区域），新增侧栏「添加手机」与三标签添加向导弹窗（USB / 同 Wi-Fi 免输 IP 配对 / 跨网络组网及可折叠高级连接，`internal/gui/wizard.go`），配对等待卡片支持取消，将本机技术概览收纳到「设置 › 关于与更新」，并全面重写 604 条 `en` / `zh-Hans` 消费级交互文案。
- **已知边界与已核对上游**：Android 伴侣真机端到端配对实测（`Power.kt`）与 CI 打包流水线随本次标签发布验证。已核对上游版本：Go `1.27.4` / MyGo `v0.10.0` / Rust `1.99.1` / scrcpy `v5.0.1` / EasyTier `v2.7.1` / Kyokai `v0.8.1`（核对日期 `2026-11-27`）。

## v0.2.0（2026-11-27）

- **UID 2000 常驻增强协同守护进程（`INV-01`、`INV-03`）**：新增 `server/dexhelper` `Daemon.java`、`Daemon.kt` 与 `AndroFuseDaemonProvider`，通过内核 UID 校验的作用域 `IBinder` 交接（`getContentProviderExternal`）、`ADD_TRUSTED_DISPLAY` 探测、`Os.socketpair` `ParcelFileDescriptor` 零拷贝通道（`TX_ECHO_FD`）及 `linkToDeath` 自动重连，在免无线调试下经 Companion TLS 1.3 提供完整交互式手机镜像、三星原生 DeX（`dex:1920x1080/160` / `FlexDisplay`）与无边框独立应用窗口（`start_app` + `apps.list` / `TX_LIST_APPS` + 分设备 `icon-cache/<hwHash>/apps.json`）。
- **桌面端连接策略与无感自动切换**：在「设置 › 手机与画面」新增连接策略（`both_adb`、`both_companion`、`companion_only`）与自动切换开关，支持切到 5G 时快速切换至 Companion、`both_adb` 下回 Wi-Fi 后自动升回健康 ADB，并在会话窗口顶部显示状态条与手动重连按钮。
- **完整手机状态上报与自愈引导**：`status.get` 新增上报 `wifi_ipv4`、`wireless_adb_port`、`app_version`、`app_build_hash`、`companion_port`、`standby_running`、`accessibility_on` 与 `daemon_*`；`Power.kt` 在 Wi-Fi 切换时重启 `_adb-tls-connect._tcp` NSD 发现；桌面端支持跨子网感知动态无线调试端口（辅以限频 `adb.FindWirelessPort` 回退探测），并在安装包哈希不一致或增强协同未激活时按需至少拉起一次 ADB 完成自愈安装与引导。
- **局域网直连优先与 5G 中继低积压视频管线**：`companionRoutes` 优先尝试 `WifiIPv4` / `Endpoint` 局域网直连 TLS 1.3 TCP 再回退 EasyTier 组网转发（`isMeshRoute`）；`companionStreamProfile` 按实际链路适配码率、分辨率与编码（5G 中继路径降为 `1.0–1.5 Mbps` / `960`px 或 `1280×720` 并优先硬件 H.265）；`tuneVideoConn`、`Daemon.java` 与 `Daemon.kt` 将视频 Socket 收发缓冲限制为 `64 KB`、拷贝缓冲限制为 `32 KB`，消除多秒队列积压。
- **已知边界与已核对上游**：在带宽受限的 5G 公网中继下执行全屏动画时仍可能短暂降帧，基于拥塞反馈的动态码率调节留待后续版本实现。已核对上游版本：Go `1.27.4` / MyGo `v0.10.0` / Rust `1.99.1` / scrcpy `v5.0.1` / EasyTier `v2.7.1` / Kyokai `v0.8.1`（核对日期 `2026-11-27`）。

## v0.1.0（2026-11-26）

- **原生桌面端与核心库（`windows/arm64` 与 `windows/amd64`）**：基于 `GPL-3.0-only` 构建统一的 Go（`1.27`）、MyGo（`v0.10.0`）与 Rust 2024（`core/androfuse-core`，C ABI v6）应用，配套 Ed25519 辅助程序包签名（`tools/sign`）、MyGo 签名更新配置与原生 PE 机器码校验（`tools/checkpe`）。
- **Media Foundation 硬解与音频管线**：零分配 DXVA/D3D11 H.264/HEVC NV12 视频解码直连 `ui.Bitmap.UpdateYUV`、WASAPI 共享模式 PCM 播放与麦克风采集（`androfuse_mic_*`）、PNG 截图及零重编码分片 MP4 录制。
- **三星原生 DeX、手机镜像、独立应用窗口与摄像头**：基于 `SemWifiDisplayConfig` 的原生 DeX 回环（`server/dexhelper`、UHID 键鼠/滚轮交互、动态缩放、脱离 ADB 会话的宽限期热切换 `Reattach`）、并发手机镜像、带真实应用图标的无边框独立应用窗口（支持跳过 One UI 动态图标崩溃并恢复列表），以及支持前后置原位切换的手机摄像头串流。
- **Kyokai 风格 V3 桌面界面**：主窗口停靠窗格、分屏视图、免重连弹出与放回、只填地址查找端口连接、`+` 选择器、`Ctrl K` 全局搜索、记住的手机（`devices.json`）、多网络 EasyTier 设置（最多 8 个网络）、托盘分机子菜单，以及由脱敏「保存日志…」兜底的通俗错误提示。
- **内置 EasyTier v2.7.1 组网与伴侣 APK（`arm64-v8a`）**：桌面与 Android 核心均内置用户态 `--no-tun` 组网，支持紧凑 `AF-<p|c>-<id>` 对端过滤与移动端省电模式；可选 ECDSA P-256 TLS 1.3 Android 伴侣端（`AndroFuse-0.1.0-android-arm64.apk`），具备 Keystore 密封身份、多电脑独立配对、后台待机服务、无线调试自动发现/隧道与移动网络下屏幕共享（`MediaProjection`）。
- **内建无头 Android 虚拟机与默认关闭的本机 MCP**：支持按需下载并原生运行 Android 16 虚拟机（`internal/vm`），以及默认关闭、仅绑定 `127.0.0.1` 并支持逐次审批卡片的本地 MCP 控制面。
- **已知边界**：Miracast 接收端因依赖未公开 Windows 接口且触发 `qcwlan8280.sys` 内核蓝屏已全平台隐藏禁用；Windows 11 `x64` / Windows 10 真机验证、物理 USB 拔线复测与 5G 重启后免线方案留待后续版本完成。
- **已核对上游版本**：Go `1.27.4` / MyGo `v0.10.0` / Rust `1.99.1` / scrcpy `v5.0.1` / EasyTier `v2.7.1` / Kyokai `v0.8.1`（核对日期 `2026-11-26`）。
