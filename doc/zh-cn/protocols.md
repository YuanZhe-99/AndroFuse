# 线协议与核心引擎（M1a–M1b）

AndroFuse 在 Rust（`core/androfuse-core`）与 Go（`internal/adb`、`internal/scrcpy`、`internal/dex`、`internal/lease`）内直接实现协议引擎，不通过命令行派生 `scrcpy.exe`。

## 1. `scrcpy-server v5.0.1` 线协议

`tools/fetchdeps` 从上游拉取 `scrcpy-server-v5.0.1`（Apache-2.0，固定校验 SHA-256 `764eb6f79811d5211fe9df341120882ba9994c7a61b897d7bf3fb662e53bc536`）至 `.dev/cache/`。建立会话时将其推送到 `/data/local/tmp/androfuse-scrcpy-server.jar`，并通过 `adb forward tcp:<port> localabstract:scrcpy_<scid>` 建连（支持 USB、TLS 无线 ADB `_adb-tls-connect._tcp` 与 Android 模拟器 `emulator-*`）：

1. **视频 Socket 握手**：可选 `0x00` 占位字节（`tunnel_forward=true`）、64 字节 NUL 填充 UTF-8 设备名、4 字节大端 `codec_id`（`0x68323634` H.264、`0x68323635` H.265）。
2. **12 字节包头**：
   - **Session 包**（`byte[0] & 0x80 != 0`）：`client_resized = (byte[3] & 1) != 0`、`width = BE u32(4..8)`、`height = BE u32(8..12)`。
   - **Media 包**（`byte[0] & 0x80 == 0`）：`is_config`（第 62 位）、`is_keyframe`（第 61 位）、`pts_us`（低 61 位）、`size = BE u32(8..12)`。客户端自动缓存最近的配置包（CSD）并在关键帧前补齐。
3. **交互控制与启动参数规则**：
   - 严禁在 `com.genymobile.scrcpy.Server` 启动命令行传递 `start_app` 或 `mouse=uhid`，也不得同时指定 `display_id` 与 `new_display`。
   - 桌面交互按窗口级别隔离路由（`INV-12`）：通过 `TYPE_INJECT_TOUCH_EVENT`（`2`，支持 `PointerIDMouse = -1` 进行 1:1 悬停/左右中键控制，或切换 `PointerIDGenericFinger = -2` 触控模式）、`TYPE_INJECT_SCROLL_EVENT`（`3`，`i16` 定点数）、`TYPE_INJECT_KEYCODE`（`0`）、`TYPE_INJECT_TEXT`（`1`）、`TYPE_SET_DISPLAY_POWER`（`10`）、`TYPE_ROTATE_DEVICE`（`11`）、`TYPE_UHID_*`（`12–14`）、`TYPE_START_APP`（`16`）与 `TYPE_RESIZE_DISPLAY`（`21`）发送。

## 2. Samsung Native DeX 本地回环（`server/dexhelper`）

`tools/builddex` 将 `server/dexhelper/src/com/androfuse/dexhelper/Main.java` 编译打包为 `build/androfuse-dex-helper.jar`（`--min-api 30`，零 Root，UID 2000 `shell`）。

- **`probe`**：初始化 `Looper` 与 `ActivityThread`（`com.android.shell`）兼容壳，反射检测 `SemWifiDisplayConfig$Builder` 与 `IDisplayManager.connectWifiDisplayWithConfig`，输出 `AFDEX1|PROBE|{json}`。
- **`serve <token> <w> <h> <dpi> <grace_sec>`**：先通过 `KEYCODE_WAKEUP` 唤醒息屏设备，持有 `/data/local/tmp/androfuse-dex.lock` 后调用 `disconnectWifiDisplay()`，绑定本地 UDP 排空端口并调用 `connectWifiDisplayWithConfig`（`127.0.0.1:7236`、`"AndroFuseDeX"`、`mode=2`），完成 WFD RTSP 状态机并遵循**初始 DPI ≥ 160 引导规则**；支持运行期 `RESIZE <w> <h> <dpi>`（实测自动触发 `scrcpy-server` 会话重协商），并在收到 `STOP` 或 stdin 断开宽限到期后完整清理。
- **`EvaluateTruth`（`INV-06`）**：One UI 9.0（`SDK 37`）将 DeX 合并进 SystemUI/Launcher（无独立 `com.sec.android.app.desktoplauncher` 包），因此 `internal/dex` 通过三星厂商身份、活跃 `SemWifiDisplayConfig` 与显示属性进行真伪判定，而非死板依赖旧包名。

## 3. Miracast / Wi-Fi Direct 接收端（`core/androfuse-core/src/miracast.rs` — 已隐藏搁置）

- **全平台隐藏原因**：Windows 第三方应用无法仅凭公开 WinRT API 充当 Miracast 接收端，必须调用微软未公开的私有接口 `wlanapi.dll!WlanNotifyVsIeProviderInt`（`GUID b7d94b4d-5db1-4e70-b5c3-dd003eeebe66`）。在 Windows 11 ARM64（`build 26300`，高通 WCN685x WiFiCx 驱动 `qcwlan8280.sys` `1.0.4122.4000`）真机复测证实，外部手机对注入 `WlanNotifyVsIeProviderInt` 的 Wi-Fi Direct GO 接收端发起 Miracast 关联时，会直接触发 `qcwlan8280.sys` 内核工作线程访问违例蓝屏（`BugCheck 0x0000007E`，`0xC0000005`）。鉴于依赖未公开系统内部接口不具备稳定性保障，Miracast 方案已在全部平台界面中隐藏并在运行期禁用；有线 USB ADB 与 **加密 TLS 无线 ADB（`_adb-tls-connect._tcp`）** 作为正式支持的 DeX 双通道。
- **保留的纯协议离线单测**：`miracast.rs` 中的纯 Rust WFD RTSP 状态机、RTP 乱序重排、MPEG-TS H.264 解复用与 UIBC 报文编解码仍保留离线单元测试作为技术归档，不触碰任何无线网卡驱动。

## 4. C-ABI v3 NV12 管线与租约账本（`leases.json`）

| 组件 | 契约说明 |
|---|---|
| `androfuse_decoder_*` 与 `androfuse_miracast_*` | Media Foundation H.264/HEVC DXVA/D3D11 硬解或软解输出 NV12 平面（按 `MF_MT_FRAME_SIZE` 宏块高度与 `MF_MT_MINIMUM_DISPLAY_APERTURE` 裁剪） |
| `internal/core.NV12Ring` 与 `SessionWindow` | 可复用 NV12 缓冲槽直喂 `ui.Bitmap.UpdateYUV`（`ui.NV12`、`ui.BT709`），稳态每帧零堆分配（`INV-04`） |
| `internal/lease.Store` | 原子写入 `leases.json`，按 SHA-256 `device_hash`（绝不写明文序列号或局域网 IP）记录 `mirror`/`virtual_display`/`native_dex` 租约，支持启动自动清理恢复（`INV-03`、`INV-08`） |

## 5. 离线回放样本与真机验证套件

- 离线测试使用脱敏 64×48 H.264 数据（`internal/scrcpy/testdata/fold8_scrcpy_session.json`），经本地回环 Socket 跑通 Media Foundation NV12 → `ui.Bitmap.UpdateYUV`（`ui.NewTester`）全链路，不含任何真机屏幕图像或序列号。
- 真机实时串流测试（`Spike S1、S2、S4`）通过环境变量 `$env:ANDROFUSE_LIVE_DEVICE = "1"` 在 `internal/scrcpy` 与 `internal/dex` 中按需触发，结束时自动断言无残留显示与端口转发。
