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
- **经 adb 首次配对（数字比对，`INV-09`）**：电脑用 `adb install -r` 安装伴侣，以不含任何密钥的 `androfuse://pair` 打开它（仅启动伴侣服务），再 `adb forward` 伴侣的本地套接字并发送 `pair.request {client_name, code}`。伴侣只在**本地套接字或回环连接**上接受该帧（局域网设备无法弹出提示），在手机上显示电脑名称与 6 位码，用户点「允许」后才返回 `{token, cert_fingerprint}`。每台电脑单独配对（与 Kyokai 相同的 ID 方式）：`pair.request` 与 `auth` 带有 `computer_id`（电脑组网主机名中的 ID）与 `client_name`（Windows 计算机名，`companion.SetClient`）；手机为每台电脑签发各自的令牌，只保存其 SHA-256 以及名称、在手机上起的名称、配对与上次连接时间（`companion.json` 的 `computers`；主页 › 已配对的电脑，可重命名和忘记）。忘记一台电脑会立即使其令牌失效，其他电脑保持配对；早期版本的共享令牌仍被接受，首次连接时把该电脑加入列表，忘记该电脑时随之更换；拒绝或 `PairWait`（60 秒）超时则失败。电脑核对 TLS 指纹后用该令牌正常连接。
- **组网交接（`mesh.join`，`INV-07`、`INV-08`）**：仅当电脑用户勾选「同时加入组网」且电脑组网正在运行时发送 `{network_name, network_secret, peers}`。`peers` 为电脑的中继，以及把电脑监听地址中的通配主机替换为本机局域网 IPv4 后的地址。手机用 Android Keystore AES-GCM 密钥密封后保存密钥，以无 TUN、无监听端口的方式加入，并在每次启动伴侣服务时自动重连；手机上可随时退出组网。电脑开启「允许其他设备直接连接」时，在 20000–48999 范围内随机选取 TCP/UDP 均空闲的高端口（低于 Windows 动态端口段，并跳过被占用或保留的端口），之后在仍空闲时沿用。EasyTier 自身报告的错误（如监听端口被占用）经 `mesh.status` 的 `error` 字段显示，此时不视为运行中。
- **免 adb 重连与省电**：`pair.res` 还带回伴侣的 TCP `port`、`model`，以及它加入网络所用的主机名 `mesh_host`（见下）；`auth.res` 同样带有 `mesh_host`，电脑每次连接时更新保存的值。电脑将二者连同固定的指纹、经 DPAPI 密封的令牌保存在 `devices.json`；之后手机连着 adb 时经 adb 本地套接字重连，否则按该主机名在 EasyTier 路由中查到手机的组网 IPv4，按设备 ID 匹配，手机改名后仍能找到（`Manager.PeerIPv4`、`mesh.SameDevice`），在该网络的一次重启中添加到其保存端口和 `companion.DevicePorts(id)` 各端口的 `127.0.0.1` 端口转发，经第一个应答的端口连接并保存该端口（`INV-07`）。`status.get` 还会报告 `mesh_network` 与 `mesh_running`（绝不包含密钥），以及来自 Android `BatteryManager` 的电量（应用无法读取 `/sys/class/power_supply`；未知值显示为「—」）。每当手机经 adb 上线（USB、Wi-Fi 或手动输入的地址），电脑都会检查一次它的伴侣（`syncCompanion`）：已安装的 `base.apk` 与随附伴侣不同（两边比较 SHA-256）时用 `adb install -r` 安装随附伴侣；这台电脑尚未配对时进行配对（两边屏幕显示配对码，同上）；已配对时，如果手机没有在配对网络中运行，则再次发送 `mesh.join`。未安装伴侣的手机只在首次连接时提示安装；手机卡片的 ⋯ 菜单随之显示「安装并配对」「配对」或「重新配对」，已安装的伴侣不是随附版本时还有「更新」。手机也可以自己加入或更改网络（在网络卡片上点「编辑」：名称、服务器、密钥）；审计日志会记录是电脑还是手机选择的网络。除非用户关闭「省电模式」，手机以省电方式运行组网：`mesh.start` 的 `low_power` 会设置 EasyTier 的 `multi_thread = false`、`lazy_p2p`、`disable_upnp` 与 `disable_sym_hole_punching`。
- **多个网络（V3）**：桌面端在 `gui.json` 中最多保存 8 个 EasyTier 网络（`MeshPrefs.Networks`：本机显示名称、网络名、服务器、DPAPI 密封的密钥、加入开关，以及每个网络用于直接连接的随机高端口），每个已加入的网络运行一个核心实例（`mesh.start` 的 `instance`，由 1–32 个字母、数字、`-` 或 `_` 组成；`mesh.stop` / `mesh.status` 接受同样的 `instance`，`mesh.stop` 不带它时全部停止）。设置 › 网络的高级选项对应 `disable_p2p`（关闭「尝试直连」）与 `latency_first`。桌面端每 3 秒读取各运行实例的组网地址、UDP NAT 类型和对端（EasyTier 的 `peer_route_pairs`：主机名、地址、存在直接连接时为直连、延迟取自连接的 `latency_us` 或路由的 `path_latency`）。EasyTier 把对外的对端套接字绑定到物理网卡，因此同一台机器上的对端要用局域网地址而不是 `127.0.0.1` 连接。与 Kyokai 的 `Kyokai-<id>` 一样，每台 AndroFuse 设备以 `AF-<p|c>-<id> <name>` 加入网络（`mesh.Hostname`，将类型缩写为 `p`/`c` 以避免 EasyTier 的 32 字符上限截断设备名；`ParseHostname` 同时兼容早期 `AndroFuse-<phone|pc>-<id>` 格式）：`kind` 对应 `phone`（`p`）或 `pc`（`c`），`id` 是设备保留的 8 位随机十六进制数（电脑上为 `MeshPrefs.DeviceID`，伴侣设置中为 `mesh_id`），`name` 是手机在「设置 › 关于手机」中的设备名称（未设置时为 `Build.MODEL`）或 Windows 计算机名。EasyTier 主机名由各节点自定，因此名称只作显示。电脑只显示带该前缀的对端：网络中的公共中继服务器和其他 EasyTier 节点一律不列出，主页与 + 选择器只列出 `phone` 对端，设置 › 网络列出手机和电脑。
- **通过伴侣使用无线调试（`tunnel.open`，`INV-07`）**：桌面端在一个新的已认证连接上发送 `tunnel.open {port}`；手机只接受自己的无线调试端口（用 NsdManager 发现并保存的那个），并且要等 `127.0.0.1:<port>` 对 CNXN 握手回应 STLS；回复 `tunnel.res` 之后，该连接就成为通往这个端口的字节管道，直到任一方关闭。桌面端监听一个空闲回环端口，每接受一个连接就打开一条隧道（`companion.ServeTunnels`），于是 `adb connect 127.0.0.1:<port>` 在局域网或跨网络（经一个到伴侣端口的组网转发）都能通过伴侣连到手机的无线调试。需要手机上的「通过伴侣使用无线调试」权限（默认关闭）。手机报告处于 Wi-Fi 时，电脑先用伴侣报告的端口连接记住的手机 Wi-Fi 地址（经 OpenWRT 等路由的网络可以到达），且只有硬件哈希与这台手机一致时才保留该连接，因为另一个网络中可能有同一私有地址的其他设备；否则使用隧道。被替换的隧道的 adb 连接会被关闭，adb 不会把手机列出两次。电脑会自动重连：已配对的手机出现在运行中的网络时立即扫描，开启自动重连的离线手机每 30 秒重试一次，隧道每台手机最多每 30 秒尝试一次。手机上，只要已配对或已加入网络，伴侣服务（以及随之运行的网络、Wi-Fi 监听与无线调试发现）会随应用启动，除非用户自己停止了它（`AutoStart`，`companion.json` 的 `stopped`）。
- **无线调试端口自动发现，不必手填**：伴侣用 Android 的 NsdManager 发现 `_adb-tls-connect._tcp`，保留地址属于本机的那条广播，用 CNXN 探测核实端口后在 `status.get` 中报告；广播消失时（无线调试关闭或没有 Wi-Fi）报告 0。
- **移动网络下的屏幕共享（`share.open`、`input.key`，`INV-09`）**：无线调试只在 Wi-Fi 下运行，因此在移动网络下，桌面端可以请伴侣共享屏幕。`share.open` 让手机弹出 Android 的 MediaProjection 授权；用户允许后，`mediaProjection` 类型的前台服务（带「停止」通知）用 MediaCodec H.264 编码屏幕（最长边至多 1280 像素、30 fps、4 Mbit/s），以 scrcpy-server 的视频帧格式写入抽象套接字 `androfuse-share`，回复之后该连接就承载这条流，桌面端用 `scrcpy.StartFromConns` 解码。`input.key {action}` 通过伴侣的无障碍服务执行 `back`、`home` 或 `recents`（该服务不声明任何能力，因为 MyGo 插件无法附带它所需的 XML，所以只能执行全局操作，并且要用户先在系统里打开）。两者都需要手机上的「移动网络下共享屏幕」权限（默认关闭）；这种方式不能点按、输入或传声音。
- **只填地址即可连接**：主页的「连接手机」接受 IP 地址、域名或手机名称。带端口时直接连接；不带端口时，桌面端依次尝试手机记住的无线调试地址、它的伴侣（隧道），再对能直接路由到的地址用 TCP 连接加 CNXN 探测查找 30000–60999 端口（`adb.FindWirelessPort`，每次数百个，约二十秒，可取消）。某端口应答 STLS 却拒绝这台电脑时，界面会要求填写手机显示的配对码和配对端口（`adb pair`）。组网对端的地址只能经它的伴侣到达，因为桌面端没有虚拟网卡。全部失败后，卡片才请用户填写手机上显示的端口。

## 7. 默认关闭的可授权本地 MCP 控制接口（`internal/mcp`）

- **默认关闭与本机回环绑定（`INV-07`、`INV-08`）**：进程启动时默认关闭（`Enabled=false`）；用户仅在设置页主动开启后才监听 `127.0.0.1:0` HTTP（`POST`），生成 30 分钟有效期随机 Bearer 令牌并通过常量时间校验（`subtle.ConstantTimeCompare`）。
- **白名单门控、紧急停止与脱敏审计**：`tools/list` 与 `tools/call` 严格遵循按工具 `Allowlist` 开关（启用时默认开放只读的 `list_devices` 与 `run_diagnostics`，`launch_app` 需单独授权）。`EmergencyStop()`（应用退出时自动调用）立即吊销密钥并关闭端口；审计日志永不记录令牌或原始设备序列号。 自 V3 起桌面端还会提供 `Handlers.Approve`：`tools/list` 因而列出全部工具，白名单只表示无需询问即可运行的工具（设置 › 安全与隐私），其余每次调用都会在窗格上方显示卡片（并发出通知），最多等待两分钟让用户选择「允许一次」或「拒绝」；结果写入审计（`approval`、`user_allowed_once` 或 `user_denied`）。

## 8. 双向摄像头与麦克风桥接协议（`internal/avbridge`，`M5`）

- **手机摄像头 + 麦克风回传**：`internal/scrcpy` 支持 `video_source=camera`（`camera_facing`、`camera_size`、`camera_fps`，强制 `control=false`）与 `audio_source=mic` 直采进入桌面 `SessionWindow` 及仅绑定 `127.0.0.1` 的随机路径令牌 MJPEG 服务（`avbridge.MJPEGServer`，无本地订阅客户端时零编码开销）；同时通过 `ProbeVirtualCamera` 诚实探测系统 `MFCreateVirtualCamera` 接口并明确提示未安装独立 COM 虚拟摄像头驱动。摄像头启动立即失败时会再尝试两次、间隔 1.5 秒，因为刚结束另一个摄像头会话时手机可能仍占用摄像头，其 scrcpy-server 会因此中止。摄像头窗格打开时为静音（手机麦克风在电脑扬声器上很响），标题栏提供「切换前后摄像头」、声音开关、截图与全屏，而不是手机按键；scrcpy 在启动时选定摄像头，因此「切换前后摄像头」保留同一个窗格或窗口、声音设置与 MJPEG 桥，在最后一帧上显示「正在切换到前置摄像头…」，关闭视频流（释放摄像头）后把另一个摄像头接入同一会话（`startCameraStream`）。
- **非 Root 下 PC→Android 回传（`INV-09`）**：`internal/companion` 的 `av.push` 帧在 TLS 1.3 加密隧道上传输有界（`<= 2 MiB`）的 `mic_pcm` / `camera_jpeg` 媒体帧，严格受手机 Companion 端默认关闭的 `MediaBridge` 开关门控。
