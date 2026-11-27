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
- **`apps [--no-icons] [--resume <package>]`**：通过 `ActivityThread.systemMain()` 获取系统上下文，调用 `PackageManager` 查询 `ACTION_MAIN` / `CATEGORY_LAUNCHER`，过滤 WebAPK/十六进制哈希噪声包名，提取本地化应用名与 `resizeMode` 并渲染 `96x96` ARGB_8888 PNG 图标，逐行输出 `AFDEX1|APP|{json}` 与 `AFDEX1|APPS_DONE|<count>`。图标先取应用自身的图标资源，之后才用 `PackageManager.loadIcon`，因为在 One UI 上 `loadIcon` 会运行应用的「动态图标」代码（日历、时钟），在 shell uid 下会失败或让 `app_process` 在原生层中止。助手在绘制每个图标前输出 `AFDEX1|ICON|<package>`；若进程在此退出，电脑保留已得到的应用并运行 `apps --resume <package>`（最多三次），该应用不带图标列出，然后继续其后的应用。在 Galaxy Z Fold8（One UI 9）上，全部 158 个应用连同图标在 1.9 秒内返回。
- **`serve <token> <w> <h> <dpi> <grace_sec>`**：先通过 `KEYCODE_WAKEUP` 唤醒息屏设备，持有 `/data/local/tmp/androfuse-dex.lock` 后调用 `disconnectWifiDisplay()`，绑定本地 UDP 排空端口与本地控制监听端口（`127.0.0.1:<ctrlPort>`，作为 `AFDEX1|<token>|READY|<id>|<w>|<h>|<dpi>|<type>|<flags>|<pid>|<ctrlPort>` 的第 8 字段返回），并调用 `connectWifiDisplayWithConfig`（`127.0.0.1:7236`、`"AndroFuseDeX"`、`mode=2`）完成 WFD RTSP 状态机并遵循**初始 DPI ≥ 160 引导规则**；支持运行期 `RESIZE <w> <h> <dpi>`，在 stdin 意外断开时进入 `grace_sec` 宽限保活窗口（输出 `AFDEX1|<token>|GRACE|<grace_sec>`），并在收到 `STOP` 或宽限到期后完整清理。PC 以脱离会话方式启动 `serve`（`setsid -w nohup`；toybox 不支持 `setsid -w` 时退回普通启动）：传输断开（USB 拔线、Wi-Fi 中断）时 adbd 会结束 shell 所在进程组，此前助手因此在宽限期开始前即被终止。
- **`attach <token> <ctrl_port>`**：在 USB→TLS Wi-Fi 热切换期间（`INV-02`）连接仍在存活的 `127.0.0.1:<ctrl_port>`，发送 `AUTH <token>` 鉴权后接收 `AFDEX1|<token>|REATTACHED|<id>|<w>|<h>|<dpi>`，无缝接管后续 `PING` / `RESIZE` / `STOP` 指令。
- **`SelectRoute` 与 `EvaluateTruth`（`INV-06`）**：One UI 9.0（`SDK 37`）将 DeX 合并进 SystemUI/Launcher（无独立 `com.sec.android.app.desktoplauncher` 包），因此 `internal/dex` 通过三星厂商身份、活跃 `SemWifiDisplayConfig` 与显示属性进行真伪判定与自动选路，并支持在“原生 DeX”与明确标注的“虚拟桌面兼容模式”间手动切换。

## 3. Miracast / Wi-Fi Direct 接收端（`core/androfuse-core/src/miracast.rs` — 已隐藏搁置）

- **全平台隐藏原因**：Windows 第三方应用无法仅凭公开 WinRT API 充当 Miracast 接收端，必须调用微软未公开的私有接口 `wlanapi.dll!WlanNotifyVsIeProviderInt`（`GUID b7d94b4d-5db1-4e70-b5c3-dd003eeebe66`）。在 Windows 11 ARM64（`build 26300`，高通 WCN685x WiFiCx 驱动 `qcwlan8280.sys` `1.0.4122.4000`）真机复测证实，外部手机对注入 `WlanNotifyVsIeProviderInt` 的 Wi-Fi Direct GO 接收端发起 Miracast 关联时，会直接触发 `qcwlan8280.sys` 内核工作线程访问违例蓝屏（`BugCheck 0x0000007E`，`0xC0000005`）。鉴于依赖未公开系统内部接口不具备稳定性保障，Miracast 方案已在全部平台界面中隐藏并在运行期禁用；有线 USB ADB 与 **加密 TLS 无线 ADB（`_adb-tls-connect._tcp`）** 作为正式支持的 DeX 双通道。
- **保留的纯协议离线单测**：`miracast.rs` 中的纯 Rust WFD RTSP 状态机、RTP 乱序重排、MPEG-TS H.264 解复用与 UIBC 报文编解码仍保留离线单元测试作为技术归档，不触碰任何无线网卡驱动。

## 4. C-ABI v6 NV12 / WASAPI 管线、本地 IPC 与租约账本（`leases.json`）

| 组件 | 契约说明 |
|---|---|
| `androfuse_decoder_*` 与 `androfuse_audio_*` | Media Foundation H.264/HEVC DXVA/D3D11 硬解或软解输出 NV12 平面（按 `MF_MT_FRAME_SIZE` 与 `MF_MT_MINIMUM_DISPLAY_APERTURE` 裁剪）以及共享模式 WASAPI 48kHz 双声道 PCM 播放 |
| `androfuse_call` `mesh.start` / `mesh.stop` / `mesh.status` | 内置 EasyTier v2.7.1（`easytier-ffi`，无 TUN）。`mesh.start` 接收 `{network_name, network_secret, device_name, peers[], port_forwards[{local_port, remote_host, remote_port}]}`（`remote_host` 为组网 IP），生成 `flags.no_tun = true`、`dhcp = true`、无监听端口且转发仅绑定 `127.0.0.1` 的 TOML；`mesh.status` 返回 `{running, engine}`，运行详情从不写入日志（`INV-08`） |
| `internal/core.NV12Ring` 与 `SessionWindow` | 可复用 NV12 缓冲槽直喂 `ui.Bitmap.UpdateYUV`（`ui.NV12`、`ui.BT709`），稳态每帧零堆分配（`INV-04`），并支持按需 `SaveNV12AsPNG` 截图与 `MP4Recorder` 直存录屏 |
| `internal/localapi` 与 `internal/lease.Store` | 同用户 AF_UNIX NDJSON 守护服务（`SIO_AF_UNIX_GETPEERPID` + SID 校验）及原子写入 `leases.json`（仅记录 SHA-256 `device_hash`，不写原始序列号或 IP，`INV-03`、`INV-08`） |

## 5. 离线回放样本与真机验证套件

- 离线测试使用脱敏 64×48 H.264 数据（`internal/scrcpy/testdata/fold8_scrcpy_session.json`），经本地回环 Socket 跑通 Media Foundation NV12 → `ui.Bitmap.UpdateYUV`（`ui.NewTester`）全链路，不含任何真机屏幕图像或序列号。
- 真机实时串流测试（`Spike S1、S2、S4`）通过环境变量 `$env:ANDROFUSE_LIVE_DEVICE = "1"` 在 `internal/scrcpy` 与 `internal/dex` 中按需触发，结束时自动断言无残留显示与端口转发。

## 6. 可选 Android Companion TLS 1.3 协同协议（`internal/companion`）

- **认证加密通道（`INV-07`）**：`internal/androidhost` 托管可选 ECDSA P-256 TLS 1.3 服务，共两个监听：在所有网卡上监听 TCP，端口取由手机设备 ID 得出的 20000–48999 内四个端口中第一个空闲的（`companion.DevicePorts`，对 ID 做 SHA-256；`companion.PickDevicePort`，四个都被占用时才用随机空闲端口；保存在 `companion.json`，伴侣界面会显示）。不同手机得到不同端口；电脑从手机的组网主机名中得知 ID，手机被迫换掉保存的端口后也能再次找到，而纯随机端口一旦变化就再也找不到，以及电脑用 `adb forward tcp:0 localabstract:androfuse-companion` 访问的抽象本地套接字 `@androfuse-companion`，经 adb 配对因此无需固定端口。其证书只生成一次，并用 Android Keystore 密封保存（`identity.sealed`，`companion.MarshalIdentity`），因此电脑在配对时固定的 SHA-256 指纹在伴侣重启、更新或手机重启后仍然一致（每次启动重新生成证书会让所有免 adb 的重连失败）；电脑遇到不同证书（`companion.ErrFingerprint`）时，会在手机下次连上 adb 时重新配对。指纹由调用方固定；首帧 `auth` 必须通过常量时间配对码校验（`subtle.ConstantTimeCompare`）。
- **分级授权开关与异网 ADB 端口信标（`INV-09`、`INV-11`）**：`status.get`、`clipboard.set`、`notification.reply`、`app.continuity`、`file.push`（路径穿越拦截 + SHA-256 校验）、`display.guard` 与动态 `_adb-tls-connect._tcp` 端口信标均由手机伴侣端独立开关门控；卸载 Companion APK 时基础有线/无线投屏与 DeX 仍完全可用。
- **经 adb 或已加入组网首次配对（数字比对，`INV-09`）**：经 adb 时，电脑以 `adb install -r` 安装伴侣、`androfuse://pair` 拉起服务并转发 `@androfuse-companion` 发送 `pair.request {client_name, code}`；经已加入的 EasyTier 组网（`PairMeshPhone`）时，即使电脑尚未连接 ADB，桌面端也可通过回环端口转发向手机的 `companion.DevicePorts(id)` 直接发起 `pair.request`（新装应用按 `PermDefaultsV2` 默认开启 `Telemetry`、`ADBBeacon` 与 `ScreenShare`，手机端通过 `Power.showPairNotification` 弹出 `IMPORTANCE_HIGH` 悬浮通知）。伴侣只在**本地套接字或回环连接**上接受 `pair.request`（局域网未授权设备无法触发弹窗），核对 6 位数字并点「允许」后返回 `{token, cert_fingerprint, device_name, manufacturer, brand, android_release, sdk_int, status}`。桌面端先以临时 `mesh:<devID>` 记入 `devices.json`，日后连上 ADB 时无损合并（保留昵称、自动重连设置与绑定）到真实硬件哈希（`ro.serialno`）。忘记电脑会立即使令牌失效，拒绝或 `PairWait`（60 秒）超时则取消。
- **组网交接（`mesh.join`，`INV-07`、`INV-08`）**：仅当电脑用户勾选「同时加入组网」且电脑组网正在运行时发送 `{network_name, network_secret, peers}`。`peers` 为电脑的中继，以及把电脑监听地址中的通配主机替换为本机局域网 IPv4 后的地址。手机用 Android Keystore AES-GCM 密钥密封后保存密钥，以无 TUN、无监听端口的方式加入，并在每次启动伴侣服务时自动重连；手机上可随时退出组网。电脑开启「允许其他设备直接连接」时，在 20000–48999 范围内随机选取 TCP/UDP 均空闲的高端口并持续沿用；错误经 `mesh.status` 的 `error` 字段返回。
- **免 adb 重连与省电**：`pair.res` 还带回伴侣的 TCP `port`、`model` 与 `mesh_host`；`auth.res` 同样带有 `mesh_host`、`device_name`、`manufacturer` 与 `brand`，电脑每次连接时更新元数据。桌面端将指纹与 DPAPI 密封令牌存入 `devices.json`；手机连着 adb 时经 adb 本地套接字重连，否则通过组网主机名匹配设备 ID（`Manager.PeerIPv4`、`mesh.SameDevice`）并以 `127.0.0.1` 回环转发连接（`INV-07`）。`status.get` 还报告 `mesh_network`、`mesh_running`（绝不含密钥）、`usb_debug_on`、`wireless_debug_on`、`wireless_adb_pair_port`、`adb_tunnel_allowed`、`screen_share_allowed` 与 `BatteryManager` 电量。每次手机经 adb 上线，电脑会执行一次伴侣同步（`syncCompanion`）。
- **多个网络（V3）**：桌面端在 `gui.json` 中最多保存 8 个 EasyTier 网络，每个网络运行一个核心实例，每 3 秒读取对端路由与延迟。AndroFuse 设备统一以 `AF-<p|c>-<id> <name>` 加入网络（兼容早期 `AndroFuse-<phone|pc>-<id>`）。主页在「发现的新手机」区域自动显示未配对手机且不暴露原始虚拟 IP，设置 › 网络列出手机与电脑对端。
- **通过伴侣转发无线调试与配对端口（`tunnel.open`，`INV-07`）**：`tunnel.open {port}` 只接受手机已核实的无线调试连接端口（`_adb-tls-connect._tcp`）或活跃无线配对端口（`_adb-tls-pairing._tcp`），并在 `127.0.0.1:<port>` 回应 STLS 后转交字节流（`companion.ServeTunnels`）。首次 Wi-Fi 配置或一次性激活增强协同（`EnsureEnhancedAndRun` / `ActivatePairAndBootstrap`）时，桌面端优先直连局域网配对窗口，必要时也可通过伴侣配对端口隧道完成 `adb pair` 并拉起 `dex.StartDaemon`；若策略为 `companion_only`，引导后会自动断开临时无线 ADB。
- **无线调试端口自动发现，不必手填**：`Power.kt` 通过 Android NsdManager 同时发现 `_adb-tls-connect._tcp` 与 `_adb-tls-pairing._tcp`，核实本机地址后在 `status.get` 中上报 `wireless_adb_port` 与 `wireless_adb_pair_port`。
- **移动网络下的屏幕共享（`share.open`、`input.key`，`INV-09`）**：`share.open` 经 MediaProjection + MediaCodec H.264 在抽象套接字 `androfuse-share` 输出屏幕画面；开启无障碍服务后，`input.key` 支持发送全局返回/主页/最近任务按键。
- **引导添加与高级手动回退（`wizard.go`）**：+ 添加手机弹窗依次引导 USB 数据线、同 Wi-Fi 免输 IP 六位配对码（`_adb-tls-pairing._tcp`）与跨网络一键配对；原始 IP/端口填写与 `adb.FindWirelessPort`（30000–60999）收纳在可折叠的高级选项中。

## 7. 默认关闭的可授权本地 MCP 控制接口（`internal/mcp`）

- **默认关闭与本机回环绑定（`INV-07`、`INV-08`）**：进程启动时默认关闭（`Enabled=false`）；用户仅在设置页主动开启后才监听 `127.0.0.1:0` HTTP（`POST`），生成 30 分钟有效期随机 Bearer 令牌并通过常量时间校验（`subtle.ConstantTimeCompare`）。
- **白名单门控、紧急停止与脱敏审计**：`tools/list` 与 `tools/call` 严格遵循按工具 `Allowlist` 开关（启用时默认开放只读的 `list_devices` 与 `run_diagnostics`，`launch_app` 需单独授权）。`EmergencyStop()`（应用退出时自动调用）立即吊销密钥并关闭端口；审计日志永不记录令牌或原始设备序列号。 自 V3 起桌面端还会提供 `Handlers.Approve`：`tools/list` 因而列出全部工具，白名单只表示无需询问即可运行的工具（设置 › 安全与隐私），其余每次调用都会在窗格上方显示卡片（并发出通知），最多等待两分钟让用户选择「允许一次」或「拒绝」；结果写入审计（`approval`、`user_allowed_once` 或 `user_denied`）。

## 8. 双向摄像头与麦克风桥接协议（`internal/avbridge`，`M5`）

- **手机摄像头 + 麦克风回传**：`internal/scrcpy` 支持 `video_source=camera`（`camera_facing`、`camera_size`、`camera_fps`，强制 `control=false`）与 `audio_source=mic` 直采进入桌面 `SessionWindow` 及仅绑定 `127.0.0.1` 的随机路径令牌 MJPEG 服务（`avbridge.MJPEGServer`，无本地订阅客户端时零编码开销）；同时通过 `ProbeVirtualCamera` 诚实探测系统 `MFCreateVirtualCamera` 接口并明确提示未安装独立 COM 虚拟摄像头驱动。摄像头启动立即失败时会再尝试两次、间隔 1.5 秒，因为刚结束另一个摄像头会话时手机可能仍占用摄像头，其 scrcpy-server 会因此中止。摄像头窗格打开时为静音（手机麦克风在电脑扬声器上很响），标题栏提供「切换前后摄像头」、声音开关、截图与全屏，而不是手机按键；scrcpy 在启动时选定摄像头，因此「切换前后摄像头」保留同一个窗格或窗口、声音设置与 MJPEG 桥，在最后一帧上显示「正在切换到前置摄像头…」，关闭视频流（释放摄像头）后把另一个摄像头接入同一会话（`startCameraStream`）。
- **非 Root 下 PC→Android 回传（`INV-09`）**：`internal/companion` 的 `av.push` 帧在 TLS 1.3 加密隧道上传输有界（`<= 2 MiB`）的 `mic_pcm` / `camera_jpeg` 媒体帧，严格受手机 Companion 端默认关闭的 `MediaBridge` 开关门控。
- **UID 2000 常驻守护进程本机 IPC 与媒体流（v0.2.0 与功能对齐）**：`dex.StartDaemon` 推送 `androfuse-dex-helper.jar` 与 `androfuse-scrcpy-server.jar`，并在 UID 2000（`shell`）下脱离会话启动 `server/dexhelper` 的 `daemon` 子命令；其持有 `/data/local/tmp/androfuse-daemon.lock`，经 `IActivityManager.getContentProviderExternal` + `IContentProvider.call("sendBinder")` 向 `com.yuanzhe.androfuse.daemon`（`AndroFuseDaemonProvider`）推送权限收敛的 `IBinder`（`com.androfuse.daemon.IDaemon`），强制执行内核级 `Binder.getCallingUid() == expectedAppUid` 校验，探测 `ADD_TRUSTED_DISPLAY`，验证 `Os.socketpair` `ParcelFileDescriptor` 零拷贝通道（`TX_ECHO_FD`），绑定双向 `linkToDeath` 清理，并在 `status.get` 中上报 `daemon_ready`、`daemon_pid`、`daemon_trusted_display` 与 `daemon_fd_verified`。在不开无线调试时，桌面端可通过 `daemon.open`（`DaemonStreamRequest`，含可选 `start_app`）与 `daemon.ctrl`（`companion.DialDaemonStream`，启用 `3s` TCP Keepalive 与 `TCP_NODELAY`）触发 `TX_OPEN_SESSION`，由守护进程拉起 `scrcpy-server v5.0.1`（采用 `cleanup=false` 以保留共享服务端 JAR 供后续跨网复用，同时靠进程退出与 Binder 死亡保证虚拟屏回收，`INV-03`），并将视频与控制 `LocalSocket` 经 `Os.socketpair` FD 桥接回 Companion TLS 1.3。为消除慢速链路上的多兆级视频队列积压（Bufferbloat），`companionRoutes` 优先尝试 `adb forward` 与局域网直连 TCP（`WifiIPv4` / `Endpoint`），仅在直连不可达时回退到 EasyTier 组网转发；`companionStreamProfile` 按实际链路动态适配码率、分辨率与编码（ADB / 局域网直连为 `6–8 Mbps`、最高 `60 fps`，跨网 P2P 为 `2–2.5 Mbps`，5G 中继路径降为 `1–1.5 Mbps` / `30 fps` / `960`px 或 `1280×720` 并在支持硬解时优先 H.265）；同时 `Daemon.java`、`Daemon.kt` 与 `companion.Server`（`tuneVideoConn`）将视频 Socket 收发缓冲限制在 `64 KB`、管线拷贝缓冲限制在 `32 KB`。桌面端在 `PairCompanionOverADB` 与 `syncCompanion` 中会在 `daemon_ready` 为假时自动经 ADB 拉起守护进程（第一层引导）；`LaunchShare` 会先查询 `status.get`，当 `daemon_ready` 为真时直接通过 `DialDaemonStream` 建立完整交互式投屏（第二层），仅在守护进程不可用时退回到 `DialShare`（第三层）；`LaunchDaemonDesktop` 在三星机型优先尝试原生 DeX（直连 `dex:1920x1080/160`、中继 `dex:1280x720/140`）并回退至 `1920x1080/200` 或 `1280x720/160` `FlexDisplay`；`LaunchDaemonAppWindow` 基于缓存应用列表（`icon-cache/<hwHash>/apps.json`）通过 `start_app`（`+pkg`）打开独立应用窗口（`INV-01`）。桌面「设置 › 手机与画面」提供连接策略选项（`both_adb`、`both_companion`、`companion_only`）与「自动快速切换并升回 ADB」（5G 掉线自动切换 Companion、切回 Wi-Fi 后自动升回健康 ADB，并在会话顶部显示状态提示与手动重连按钮）。在手机端，切回 Wi-Fi 时 `Power.kt` 会自动重启 `_adb-tls-connect._tcp` 的 NSD 发现，且 `status.get` 完整上报手机端状态（`app_version`、`app_build_hash`、`companion_port`、`standby_running`、`accessibility_on`、`wireless_adb_port`、`wifi_ipv4` 与 `daemon_*`），使桌面端能够实时展示状态、在版本不一致或增强协同未激活时按需至少拉起一次 ADB 完成 APK 更新与守护引导（即使在 `companion_only` 下也会在一次引导后安全断开临时无线 ADB），并跨子网（如 OpenWRT）感知变化后的无线调试端口（辅以限频 `adb.FindWirelessPort` 探测）。在无 ADB 场景下，桌面端还可通过 `apps.list`（`TX_LIST_APPS`）直接从 UID 2000 守护进程读取应用清单（`dexhelper apps --no-icons`）并复用本地缓存图标，随时打开独立应用窗口。
