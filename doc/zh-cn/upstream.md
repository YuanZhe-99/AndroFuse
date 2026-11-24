# 上游项目调研

对参考项目源码、许可证与实测行为的核查记录（核查日期：`2026-11-23`）。

## 调研总表

| 项目 | 版本 / 标签 | 许可证 | 主要语言 | 核心复用 / 参考价值 | 已知限制与风险 |
|---|---|---|---|---|---|
| `egoist/mygo` | `v0.10.0` | MIT | Go | 原生 D3D11 UI（`package ui`）、`ui.Bitmap.UpdateYUV` NV12 GPU 着色器、`mygo_nowebview`、`MYGO_UNCAPPED`、`//mygo:native` Kotlin Android 构建 | Windows ARM64 走 D3D11；需在 Spike S1 实测多窗口与 `MYGO_UNCAPPED` 帧调度 |
| `YuanZhe-99/Kyokai` | `v0.8.1`（`01321d7`） | AGPL-3.0-only | Go + Rust + Kotlin | 桌面 + Android 同仓结构、`internal/core` `LoadLibraryEx`/`purego` ABI 模式、`tools/checkdocs`、`tools/icon`、`internal/androidhost` | AGPL-3.0-only（同作者）；AndroFuse 采用 `GPL-3.0-only`，不复制 Kyokai 中 AGPL 的 RustDesk 代码 |
| `Genymobile/scrcpy` | `v5.0.1` | Apache-2.0 | C / Java | `scrcpy-server-v5.0.1` 线协议、`--new-display`、`--flex-display`、`--start-app`、`/dev/uhid` 键鼠、`SET_DISPLAY_POWER`、原生 `win64` + `winarm64` | 普通 `--new-display` 在 One UI 7–8.5 上缺少完整 DeX 多窗口与最近任务；受限于手机硬件编码器并发上限 |
| `adirh3/DeXDesk` | `v0.1.1` | MIT | Rust | Wi-Fi Direct Sink（`WiFiDirectAdvertisementPublisher` + `WFDOpenHandle` + `wlanapi.dll!WlanNotifyVsIeProviderInt` GUID `b7d94b4d-5db1-4e70-b5c3-dd003eeebe66`）、RTSP CEA 协商、RTP/MPEG-TS H.264 Media Foundation 解码、UIBC `GENERIC` 指针 + `HIDC` 键盘 | `WlanNotifyVsIeProviderInt` 为未公开入口；实测机型锁定 30 FPS；Miracast 拒绝原生 HID 鼠标（右键需 720 ms 长按转换、滚轮转触控滑动）；v0.1.1 无音频；与系统“无线显示器”互斥 |
| `Aureliano021/ScrcpyDex` | `v2.1.0` | Apache-2.0 | C# + Java | `scrcpydex-server.jar`（`app_process` UID 2000，无需 Root）反射调用 `SemWifiDisplayConfig$Builder` 本地回环（`127.0.0.1:7236`）创建真 DeX 虚拟屏（`DEX_ACTIVATED_DISPLAY_ID`）、`/dev/uhid` 鼠标、`ACTION_STOP_DESKTOP_MODE` 清理、Win32 Job Object | 依赖三星固件保留 `SemWifiDisplayConfig`；需要开启 USB 或无线 ADB |
| `maze-mei/DX-Manager` | `v3.0.1` | MIT | C# + Kotlin | 多手机会话注册表、One UI 版本诊断（`ro.build.version.oneui`/`sem`）、Native DeX 30 秒重连宽限期、DPI ≥ 160 初始化规则、最多 3 个独立应用窗口、`overlay_display_devices` 回退、可选 Companion 守护清理与手机→PC 文件传输 | Native DeX 已在 One UI 9 / Android 17 验证；老版本 One UI 需走兼容路径或明确报错 |
| `Keremerk/MyDeX` | `v0.3.0` | MIT | C# | One UI 7/8.5 下 `--new-display` 与 `overlay_display_devices` 的实际表现差异、Android 单路音频归属规则、明文 `adb tcpip` 安全风险说明 | 普通 `--new-display` 下 DeX 最近任务（`|||`）为空且应用默认全屏 |
| `fabriciolima77/DeXter` | `v0.5.0` | GPL-3.0-or-later | C# + Java | 备用研究参考：`ScrcpyDeX` 回环激活 + `overlay_display_devices` 兼容模式 + 会话中途 USB 转 Wi-Fi 重连 | 单窗口托盘工具，葡萄牙语界面 |
| `AndroMeld`（`catchingnow`） | 商业软件 | 专有闭源 | Web + Android | 浏览器内独立应用窗口、配对码、剪贴板/拖放/音频交互参考 | **闭源商业软件：** 仅作交互体验参考，严禁反编译、复制或分发任何源码与 APK |

## 核心技术结论

1. **Miracast DeX 与 ADB Native DeX 互补**：
   - **Miracast（`DeXDesk`）** 不需要 USB 调试、ADB 或 Companion APK，但依赖支持 Wi-Fi Direct 的网卡驱动与未公开 `wlanapi.dll` 入口，实测机型多协商为 30 FPS，且 UIBC 鼠标为 `GENERIC` 指针模拟而非真 HID 光标。
   - **ADB Native DeX（`ScrcpyDeX` / `DX-Manager`）** 基于 `SemWifiDisplayConfig` 回环（`127.0.0.1:7236`）+ `scrcpy` + `/dev/uhid`，在 USB 或 TLS 无线 ADB 下提供 60 FPS、原生桌面指针/悬停/右键/滚轮与音频回传，且无需 Root。
   - **普通 `--new-display`（`MyDeX`）** 非常适合 **模式三（独立 APP 窗口）**（配合 `--flex-display` 与 `--start-app`），但在缺少 DeX 任务栏、自由多窗口和最近任务（`|||`）时，绝不能将其冒充为完整 Samsung DeX。
