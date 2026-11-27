# 版本历史

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
