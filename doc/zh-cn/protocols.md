# 线协议与核心引擎（M1a–M1b）

AndroFuse 在 Rust（`core/androfuse-core`）与 Go（`internal/adb`、`internal/scrcpy`、`internal/dex`、`internal/lease`）内直接实现协议引擎，不通过命令行派生 `scrcpy.exe`。

## 1. `scrcpy-server v5.0.1` 线协议

`tools/fetchdeps` 从上游拉取 `scrcpy-server-v5.0.1`（Apache-2.0，固定校验 SHA-256 `764eb6f79811d5211fe9df341120882ba9994c7a61b897d7bf3fb662e53bc536`）至 `.dev/cache/`。建立会话时将其推送到 `/data/local/tmp/androfuse-scrcpy-server.jar`，并通过 `adb forward tcp:<port> localabstract:scrcpy_<scid>` 建连（支持 USB、TLS 无线 ADB `_adb-tls-connect._tcp` 与 Android 模拟器 `emulator-*`）：

1. **音视频 Socket 握手**：首个 Socket 读取可选 `0x00` 占位字节（`tunnel_forward=true`）、64 字节 NUL 填充 UTF-8 设备名与 4 字节大端视频 `codec_id`（`0x68323634` H.264、`0x68323635` H.265）。启用 `audio=true audio_codec=raw` 时，紧随其后的音频 Socket 读取 4 字节 `codec_id`（`0x00726177` 原始 48kHz 双声道 s16le PCM），若设备返回 `0x00000000`（关闭）或 `0x00000001`（不支持）则静默退出音频协程而不影响视频。
2. **12 字节包头**：
   - **Session 包**（`byte[0] & 0x80 != 0`）：`client_resized = (byte[3] & 1) != 0`、`width = BE u32(4..8)`、`height = BE u32(8..12)`。
   - **Media 包**（`byte[0] & 0x80 == 0`）：`is_config`（第 62 位）、`is_keyframe`（第 61 位）、`pts_us`（低 61 位）、`size = BE u32(8..12)`。客户端自动缓存最近的配置包（CSD）并在关键帧前补齐，录屏开启时同步写入 `MP4Recorder`。
3. **交互控制、无边框应用窗口与启动参数规则**：
   - 严禁在 `com.genymobile.scrcpy.Server` 启动命令行传递 `start_app` 或 `mouse=uhid`，也不得同时指定 `display_id` 与 `new_display`。
   - 独立 APP 窗口默认以 16 像素对齐的 `640x1120/220`（缩放时按 `1.5×` 像素倍率请求）、`flex_display=true`、`vd_system_decorations=false` 与 `TYPE_START_APP`（`+pkg`）启动，剥离 Android 状态栏、导航栏与壁纸装饰。
   - 桌面交互按窗口级别隔离路由（`INV-12`）：通过 `TYPE_INJECT_TOUCH_EVENT`（`2`，支持 `PointerIDMouse = -1` 与 `PointerIDGenericFinger = -2`）、`TYPE_INJECT_SCROLL_EVENT`（`3`，`i16` 定点数）、`TYPE_INJECT_KEYCODE`（`0`）、`TYPE_INJECT_TEXT`（`1`）、`TYPE_SET_CLIPBOARD`（`9`，配合设备→电脑剪贴板回传）、`TYPE_SET_DISPLAY_POWER`（`10`）、`TYPE_ROTATE_DEVICE`（`11`）、`TYPE_UHID_*`（`12–14`）、`TYPE_START_APP`（`16`）与 `TYPE_RESIZE_DISPLAY`（`21`）发送。

## 2. Samsung Native DeX 本地回环与应用图标提取（`server/dexhelper`）

`tools/builddex` 将 `server/dexhelper/src/com/androfuse/dexhelper/Main.java` 编译打包为 `build/androfuse-dex-helper.jar`（`--min-api 30`，零 Root，UID 2000 `shell`）。

- **`probe`**：初始化 `Looper` 与 `ActivityThread`（`com.android.shell`）兼容壳，反射检测 `SemWifiDisplayConfig$Builder` 与 `IDisplayManager.connectWifiDisplayWithConfig`，输出 `AFDEX1|PROBE|{json}`。
- **`apps [--no-icons]`**：通过 `ActivityThread.systemMain()` 获取系统上下文，调用 `PackageManager` 查询 `ACTION_MAIN` / `CATEGORY_LAUNCHER`，过滤 WebAPK/十六进制哈希噪声包名，提取本地化应用名与 `resizeMode` 并渲染 `96x96` ARGB_8888 PNG 图标，逐行输出 `AFDEX1|APP|{json}` 与 `AFDEX1|APPS_DONE|<count>`。
- **`serve <token> <w> <h> <dpi> <grace_sec>`**：先通过 `KEYCODE_WAKEUP` 唤醒息屏设备，持有 `/data/local/tmp/androfuse-dex.lock` 后调用 `disconnectWifiDisplay()`，绑定本地 UDP 排空端口与本地控制监听端口（`127.0.0.1:<ctrlPort>`，作为 `AFDEX1|<token>|READY|<id>|<w>|<h>|<dpi>|<type>|<flags>|<pid>|<ctrlPort>` 的第 8 字段返回），并调用 `connectWifiDisplayWithConfig`（`127.0.0.1:7236`、`"AndroFuseDeX"`、`mode=2`）完成 WFD RTSP 状态机并遵循**初始 DPI ≥ 160 引导规则**；支持运行期 `RESIZE <w> <h> <dpi>`，在 stdin 意外断开时进入 `grace_sec` 宽限保活窗口（输出 `AFDEX1|<token>|GRACE|<grace_sec>`），并在收到 `STOP` 或宽限到期后完整清理。
- **`attach <token> <ctrl_port>`**：在 USB→TLS Wi-Fi 热切换期间（`INV-02`）连接仍在存活的 `127.0.0.1:<ctrl_port>`，发送 `AUTH <token>` 鉴权后接收 `AFDEX1|<token>|REATTACHED|<id>|<w>|<h>|<dpi>`，无缝接管后续 `PING` / `RESIZE` / `STOP` 指令。
- **`SelectRoute` 与 `EvaluateTruth`（`INV-06`）**：One UI 9.0（`SDK 37`）将 DeX 合并进 SystemUI/Launcher（无独立 `com.sec.android.app.desktoplauncher` 包），因此 `internal/dex` 通过三星厂商身份、活跃 `SemWifiDisplayConfig` 与显示属性进行真伪判定与自动选路，并支持在“原生 DeX”与明确标注的“虚拟桌面兼容模式”间手动切换。

## 3. Miracast / Wi-Fi Direct 接收端（`core/androfuse-core/src/miracast.rs` — 已隐藏搁置）

- **全平台隐藏原因**：Windows 第三方应用无法仅凭公开 WinRT API 充当 Miracast 接收端，必须调用微软未公开的私有接口 `wlanapi.dll!WlanNotifyVsIeProviderInt`（`GUID b7d94b4d-5db1-4e70-b5c3-dd003eeebe66`）。在 Windows 11 ARM64（`build 26300`，高通 WCN685x WiFiCx 驱动 `qcwlan8280.sys` `1.0.4122.4000`）真机复测证实，外部手机对注入 `WlanNotifyVsIeProviderInt` 的 Wi-Fi Direct GO 接收端发起 Miracast 关联时，会直接触发 `qcwlan8280.sys` 内核工作线程访问违例蓝屏（`BugCheck 0x0000007E`，`0xC0000005`）。鉴于依赖未公开系统内部接口不具备稳定性保障，Miracast 方案已在全部平台界面中隐藏并在运行期禁用；有线 USB ADB 与 **加密 TLS 无线 ADB（`_adb-tls-connect._tcp`）** 作为正式支持的 DeX 双通道。
- **保留的纯协议离线单测**：`miracast.rs` 中的纯 Rust WFD RTSP 状态机、RTP 乱序重排、MPEG-TS H.264 解复用与 UIBC 报文编解码仍保留离线单元测试作为技术归档，不触碰任何无线网卡驱动。

## 4. C-ABI v4 NV12 / WASAPI 管线、本地 IPC 与租约账本（`leases.json`）

| 组件 | 契约说明 |
|---|---|
| `androfuse_decoder_*` 与 `androfuse_audio_*` | Media Foundation H.264/HEVC DXVA/D3D11 硬解或软解输出 NV12 平面（按 `MF_MT_FRAME_SIZE` 与 `MF_MT_MINIMUM_DISPLAY_APERTURE` 裁剪）以及共享模式 WASAPI 48kHz 双声道 PCM 播放 |
| `internal/core.NV12Ring` 与 `SessionWindow` | 可复用 NV12 缓冲槽直喂 `ui.Bitmap.UpdateYUV`（`ui.NV12`、`ui.BT709`），稳态每帧零堆分配（`INV-04`），并支持按需 `SaveNV12AsPNG` 截图与 `MP4Recorder` 直存录屏 |
| `internal/localapi` 与 `internal/lease.Store` | 同用户 AF_UNIX NDJSON 守护服务（`SIO_AF_UNIX_GETPEERPID` + SID 校验）及原子写入 `leases.json`（仅记录 SHA-256 `device_hash`，不写原始序列号或 IP，`INV-03`、`INV-08`） |

## 5. 离线回放样本与真机验证套件

- 离线测试使用脱敏 64×48 H.264 数据（`internal/scrcpy/testdata/fold8_scrcpy_session.json`），经本地回环 Socket 跑通 Media Foundation NV12 → `ui.Bitmap.UpdateYUV`（`ui.NewTester`）全链路，不含任何真机屏幕图像或序列号。
- 真机实时串流测试（`Spike S1、S2、S4`）通过环境变量 `$env:ANDROFUSE_LIVE_DEVICE = "1"` 在 `internal/scrcpy` 与 `internal/dex` 中按需触发，结束时自动断言无残留显示与端口转发。

## 6. 可选 Android Companion TLS 1.3 协同协议（`internal/companion`）

- **认证加密通道（`INV-07`）**：`internal/androidhost` 默认在 `27189` 端口托管可选 ECDSA P-256 TLS 1.3 服务并支持 SHA-256 证书指纹固定；首帧 `auth` 必须通过常量时间配对码校验（`subtle.ConstantTimeCompare`）。
- **分级授权开关与异网 ADB 端口信标（`INV-09`、`INV-11`）**：`status.get`、`clipboard.set`、`notification.reply`、`app.continuity`、`file.push`（路径穿越拦截 + SHA-256 校验）、`display.guard` 与动态 `_adb-tls-connect._tcp` 端口信标均由手机伴侣端独立开关门控；卸载 Companion APK 时基础有线/无线投屏与 DeX 仍完全可用。

## 7. 默认关闭的可授权本地 MCP 控制接口（`internal/mcp`）

- **默认关闭与本机回环绑定（`INV-07`、`INV-08`）**：进程启动时默认关闭（`Enabled=false`）；用户仅在设置页主动开启后才监听 `127.0.0.1:0` HTTP（`POST`），生成 30 分钟有效期随机 Bearer 令牌并通过常量时间校验（`subtle.ConstantTimeCompare`）。
- **白名单门控、紧急停止与脱敏审计**：`tools/list` 与 `tools/call` 严格遵循按工具 `Allowlist` 开关（启用时默认开放只读的 `list_devices` 与 `run_diagnostics`，`launch_app` 需单独授权）。`EmergencyStop()`（应用退出时自动调用）立即吊销密钥并关闭端口；审计日志永不记录令牌或原始设备序列号。
