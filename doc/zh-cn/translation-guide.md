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
| Protocol Replay Fixture | 协议回放样本 | 不含真实画面与序列号的离线协议测试数据 |
| Zero-Allocation NV12 Ring | 零分配 NV12 环形缓冲 | 直喂 `ui.Bitmap.UpdateYUV` 的双槽位可复用缓冲区 |
| DeX Truth Verification | DeX 真伪校验 | 严格区分完整三星 DeX 与虚拟桌面兼容回退（`INV-06`） |
| Overlay Mesh (EasyTier / VPN) | 异网组网（EasyTier / VPN） | 跨 Wi-Fi 或 5G 远程加密隧道（`--no-tun` 用户态模式避免与系统 `VpnService` 冲突） |
| Android Emulator (AVD) | Android 模拟器（AVD） | 无真机期间用于线下迭代的本地 Android 虚拟设备（`emulator-*`） |
| Mouse Mode | 鼠标模式 | 按 `display_id` 路由的 `PointerIDMouse` 悬停、左/右键点击与滚轮控制 |
| Touch Mode | 触控模式 | `PointerIDGenericFinger` 触屏点击与滑动模拟 |
| App Launcher Grid | 应用启动器网格 | 展示真实本地化 Android 应用名称与 96x96 PNG 缓存图标的瓷砖启动网格 |
| Chromeless App Window | 无系统边框独立应用窗口 | 通过 `vd_system_decorations=false` 启动并绑定原生标题栏的虚拟屏应用窗口 |
| Fragmented MP4 Recording | 分片 MP4 直存录屏 | 零重编码将 H.264 Annex-B 封装为 `ftyp`/`moov`/`moof`/`mdat` 分片 MP4 |
| Background Standby | 后台待机 | 用户开启后由最低重要性前台服务保持伴侣在后台可被连接 |
| Activity Lease | 活动租约 | 仅在传输期间持有、空闲 30 秒后释放的唤醒锁与 Wi-Fi 锁 |
| Delay-Load | 延迟加载 | DLL 仅在首次调用其函数时才解析加载（如 `packet.dll`） |
