# 版本历史

## v0.1.0（2026-11-26）

- **原生桌面端与核心库（`windows/arm64` 与 `windows/amd64`）**：基于 `GPL-3.0-only` 构建统一的 Go（`1.27`）、MyGo（`v0.10.0`）与 Rust 2024（`core/androfuse-core`，C ABI v6）应用，配套 Ed25519 辅助程序包签名（`tools/sign`）、MyGo 签名更新配置与原生 PE 机器码校验（`tools/checkpe`）。
- **Media Foundation 硬解与音频管线**：零分配 DXVA/D3D11 H.264/HEVC NV12 视频解码直连 `ui.Bitmap.UpdateYUV`、WASAPI 共享模式 PCM 播放与麦克风采集（`androfuse_mic_*`）、PNG 截图及零重编码分片 MP4 录制。
- **三星原生 DeX、手机镜像、独立应用窗口与摄像头**：基于 `SemWifiDisplayConfig` 的原生 DeX 回环（`server/dexhelper`、UHID 键鼠/滚轮交互、动态缩放、脱离 ADB 会话的宽限期热切换 `Reattach`）、并发手机镜像、带真实应用图标的无边框独立应用窗口（支持跳过 One UI 动态图标崩溃并恢复列表），以及支持前后置原位切换的手机摄像头串流。
- **Kyokai 风格 V3 桌面界面**：主窗口停靠窗格、分屏视图、免重连弹出与放回、只填地址查找端口连接、`+` 选择器、`Ctrl K` 全局搜索、记住的手机（`devices.json`）、多网络 EasyTier 设置（最多 8 个网络）、托盘分机子菜单，以及由脱敏「保存日志…」兜底的通俗错误提示。
- **内置 EasyTier v2.7.1 组网与伴侣 APK（`arm64-v8a`）**：桌面与 Android 核心均内置用户态 `--no-tun` 组网，支持紧凑 `AF-<p|c>-<id>` 对端过滤与移动端省电模式；可选 ECDSA P-256 TLS 1.3 Android 伴侣端（`AndroFuse-0.1.0-android-arm64.apk`），具备 Keystore 密封身份、多电脑独立配对、后台待机服务、无线调试自动发现/隧道与移动网络下屏幕共享（`MediaProjection`）。
- **内建无头 Android 虚拟机与默认关闭的本机 MCP**：支持按需下载并原生运行 Android 16 虚拟机（`internal/vm`），以及默认关闭、仅绑定 `127.0.0.1` 并支持逐次审批卡片的本地 MCP 控制面。
- **已知边界**：Miracast 接收端因依赖未公开 Windows 接口且触发 `qcwlan8280.sys` 内核蓝屏已全平台隐藏禁用；Windows 11 `x64` / Windows 10 真机验证、物理 USB 拔线复测与 5G 重启后免线方案留待后续版本完成。
- **已核对上游版本**：Go `1.27.4` / MyGo `v0.10.0` / Rust `1.99.1` / scrcpy `v5.0.1` / EasyTier `v2.7.1` / Kyokai `v0.8.1`（核对日期 `2026-11-26`）。
