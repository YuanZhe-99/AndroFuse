# 术语翻译表

AndroFuse 文档（`doc/en-us/` → `doc/zh-cn/`）的权威英文→简体中文术语对照表。

| 英文 | 简体中文 | 说明 |
|---|---|---|
| Samsung DeX Desktop | Samsung DeX 桌面 | 原生三星 DeX 完整桌面工作区 |
| Android Phone Mirroring | Android 手机镜像 | 主屏幕（`display 0`）投屏与控制 |
| Independent App Windows | 独立 APP 窗口 | 基于虚拟显示的每应用独立 Windows 原生窗口 |
| Virtual Display | 虚拟显示 | Android `DisplayManager` / `scrcpy --new-display` 虚拟屏幕 |
| Simulated Display | 模拟显示 | Android `overlay_display_devices` 回退显示方案 |
| Native DeX Loopback | 原生 DeX 回环 | 基于 `SemWifiDisplayConfig` `127.0.0.1:7236` 的激活方案 |
| Miracast Sink | Miracast 接收端 | Wi-Fi Direct + RTSP + RTP/MPEG-TS + UIBC 接收链路 |
| Android Companion APK | Android Companion 辅助端 APK | 同仓构建的可选 Android 增强端应用 |
| Lease Ledger | 租约账本 | 持久化的虚拟显示与 UHID 清理追踪记录 |
| Self-Check Diagnostics | 主机自检诊断 | 被动检测处理器架构、Media Foundation、Wi-Fi Direct 与 ADB |
| Negotiated Resolution | 实际协商分辨率 | 编解码器实际协商输出的画面分辨率 |
| Measured FPS | 实测帧率 | 实际解码并渲染呈现的帧率 |
