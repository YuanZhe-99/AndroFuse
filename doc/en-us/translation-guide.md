# Translation Guide

Authoritative English → Simplified Chinese glossary for AndroFuse documentation (`doc/en-us/` → `doc/zh-cn/`).

| English | Simplified Chinese | Notes |
|---|---|---|
| Samsung DeX Desktop | Samsung DeX 桌面 | Genuine Samsung DeX workspace |
| Android Phone Mirroring | Android 手机镜像 | Primary display (`display 0`) stream |
| Independent App Windows | 独立 APP 窗口 | Per-app native Windows window on virtual display |
| Virtual Display | 虚拟显示 | Android `DisplayManager` / `scrcpy --new-display` screen |
| Simulated Display | 模拟显示 | Android `overlay_display_devices` fallback |
| Native DeX Loopback | 原生 DeX 回环 | `SemWifiDisplayConfig` `127.0.0.1:7236` activation |
| Miracast Sink | Miracast 接收端 | Wi-Fi Direct + RTSP + RTP/MPEG-TS + UIBC |
| Android Companion APK | Android Companion 辅助端 APK | Optional same-repo Android app |
| Lease Ledger | 租约账本 | Persisted virtual display / UHID cleanup tracker |
| Self-Check Diagnostics | 主机自检诊断 | Passive architecture, Media Foundation, WFD, and ADB probes |
| Negotiated Resolution | 实际协商分辨率 | Actual stream dimensions reported by encoder/decoder |
| Measured FPS | 实测帧率 | Actual decoded/presented frame rate |
| Protocol Replay Fixture | 协议回放样本 | Sanitized synthetic/recorded stream for offline replay tests |
| Zero-Allocation NV12 Ring | 零分配 NV12 环形缓冲 | Two-slot reusable buffer feeding `ui.Bitmap.UpdateYUV` |
| DeX Truth Verification | DeX 真伪校验 | Validates genuine Samsung DeX vs virtual desktop fallback (`INV-06`) |
| Overlay Mesh (EasyTier / VPN) | 异网组网（EasyTier / VPN） | Cross-Wi-Fi or 5G remote tunnel (`--no-tun` userspace mode avoids `VpnService` collision) |
| Android Emulator (AVD) | Android 模拟器（AVD） | Local virtual Android target (`emulator-*`) for offline iteration |
| Mouse Mode | 鼠标模式 | Per-display `PointerIDMouse` hover, click, right-click, and scroll forwarding |
| Touch Mode | 触控模式 | `PointerIDGenericFinger` direct touchscreen/swipe emulation |
| App Launcher Grid | 应用启动器网格 | Tile grid displaying real localized Android app names and cached 96x96 PNG icons |
| Chromeless App Window | 无系统边框独立应用窗口 | Virtual-display app window launched with `vd_system_decorations=false` and native titlebar |
| Bounded Back | 有限返回 | Single-app navigation control that goes back inside the app and stops at the root screen without closing into a black display |
| Fragmented MP4 Recording | 分片 MP4 直存录屏 | Zero-reencode H.264 Annex-B muxing into `ftyp`/`moov`/`moof`/`mdat` |
| Background Standby | 后台待机 | Opt-in minimum-importance foreground service that keeps the Companion reachable |
| Activity Lease | 活动租约 | Wake lock + Wi-Fi lock held only during transfers and released after 30 s idle |
| Delay-Load | 延迟加载 | A DLL import resolved only when one of its functions is first called (e.g. `packet.dll`) |
| Phone screen | 手机屏幕 | Phone mirroring as the interface names it (V3) |
| App window | 应用窗口 | Independent app window as the interface names it (V3) |
| Android desktop mode | Android 桌面模式 | The fallback backend (Android's own desktop mode on a virtual display), never called DeX (`INV-06`) |
| Cross-network | 跨网络 | The EasyTier networks in the interface (instead of "mesh") |
| Companion screen sharing | 伴侣共享屏幕 | MediaProjection screen sharing on mobile data |
| Local assistant | 本机助手 | The opt-in local MCP control (instead of "MCP") |
| Android on this PC | 电脑上的 Android | The managed VM (instead of an AVD name) |
| Android emulator | Android 模拟器 | An emulator found over adb (never its AVD name) |
| Direct / Relayed | 直连 / 中继 | How a network peer is reached |
| Remembered phones | 记住的手机 | `devices.json` |
