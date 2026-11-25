# 平台验证记录

本文件只记录经过实际构建和运行的项；未在真机或目标架构上运行验证的功能一律明确标注为未验证。

## Windows 11 ARM64（`windows/arm64`）

| 模块 | 状态 | 实测证据 |
|---|---|---|
| Rust 核心库（`core/androfuse-core`）C ABI v4 | 已验证（M1b） | 在 `aarch64-pc-windows-msvc`（`rustc 1.99.1`）原生编译；`cargo test` 通过（含真实 64×48 Media Foundation H.264 NV12 解码、`MFT_MESSAGE_COMMAND_DRAIN`、`wasapi_audio::PcmRingBuffer` 与 `build_wfd_sink_replies` RTSP 应答器）、通过 `cargo clippy -- -D warnings` 与 `cargo deny check licenses bans sources` |
| DXVA/D3D11 视频解码与零分配 NV12 → MyGo `UpdateYUV` | 已验证（M1a–M1b） | `androfuse-svc probe` 实测 `h264_hw_decoder = true`、`hevc_hw_decoder = true`；Fold 8 有线 USB 与无线 TLS Wi-Fi 流均通过 DXVA 硬解（`hw=true`），并通过 `TestNV12RingZeroAllocSteadyState`（每帧 0 分配）与 MyGo `Bitmap.UpdateYUV` 渲染 |
| Miracast / Wi-Fi Direct 接收端（`Spike S3` 与实时接收器） | 已隐藏禁用（依赖未公开 Windows 接口） | 依赖未公开接口 `wlanapi.dll!WlanNotifyVsIeProviderInt`；在 Windows 11 ARM64（`build 26300`）真机关联复测中固定触发高通 **`qcwlan8280.sys`**（`1.0.4122.4000` WiFiCx，WER 事件 `1019`）内核态 `BugCheck 0x0000007E`（`0xC0000005`）蓝屏。已在全平台隐藏禁用，无线 DeX 统一采用加密 TLS Wi-Fi ADB Native DeX |
| MyGo 原生界面、`localapi` 与采集管线（`internal/gui`、`internal/localapi`、`internal/scrcpy`） | 已在真机桌面与模拟器实测通过（M1b） | 有线 USB 与 TLS 无线 ADB 手机镜像及交互式原生 Samsung DeX 已在 `androfuse.exe` 桌面窗口实测正常；`internal/localapi`（`SIO_AF_UNIX_GETPEERPID` SID 校验）、`SaveNV12AsPNG`、`MP4Recorder`、`CGO_ENABLED=0 go test ./...`、`go tool mygo vet .` 与 `go tool mygo i18n check` 全部通过 |
| 原生 PE 校验、Bundle 签名与 `--no-tun` 组网（`tools/checkpe`、`internal/bundlesig`、`internal/mesh`） | 已验证（M3–M4） | `go run ./tools/checkpe -dir build -arch arm64` 实测校验 3 个原生 `0xAA64` ARM64 PE 二进制文件（`androfuse.exe`、`androfuse-svc.exe`、`androfuse_core.dll`）；`internal/bundlesig` Ed25519 签名/验签与 `internal/mesh`（`--no-tun` 默认禁用）单测全部通过；`.github/workflows/ci.yml` 与 `release.yml` 已就绪 |
| 内建无头 Android 虚拟机（`internal/vm`） | 已在 Windows 11 ARM64 实测通过（M3a） | `TestLiveHeadlessVMStartStop` 在 `Pixel_9a_API_36`（原生 `arm64-v8a` WHPX）实测通过：`-no-window` 无头启动至 `sys.boot_completed=1` 并通过 `adb emu kill` 干净关闭（耗时 `38.37s`，退出后 `adb devices -l` 为空）；冷下载流程 `Provision` 通过 `ParseCmdlineToolsManifest` 完成离线校验单测 |

## Windows 11 x64（`windows/amd64`）与 Windows 10

| 目标 | 状态 | 说明 |
|---|---|---|
| Windows 11 `x64` | CI 编译与 PE/单测已通过；待 x64 真机实测 | GitHub Actions `#45528710584`（`Windows (amd64)` 与 `Rust (windows-latest)`）成功构建并通过 `tools/checkpe -arch amd64`（`0x8664`）与全套 Go/Rust 单测及 `mygo vet`；真实 x64 硬件验证仍为 M4 门禁 |
| Windows 10（`19045+`） | 待评估（M4） | MyGo D3D11 与 Media Foundation 可运行（无 Mica 材质回退为纯色）；Wi-Fi Direct Miracast 接收依赖网卡 NDIS/WDDM 驱动 |

## Android 设备与 Companion APK

| 目标 | 状态 | 说明 |
|---|---|---|
| 三星 Galaxy Z Fold 8（`SM-F971U1`，Android 17 / SDK 37，One UI 9.0 `90000`，`SM8850`） | 已在 `androfuse.exe` 通过 USB 与 TLS Wi-Fi ADB 实测 | **有线 USB 与无线 TLS Wi-Fi（`_adb-tls-connect._tcp`）**：手机主屏镜像与交互式原生 Samsung DeX（`SemWifiDisplayConfig` 回环、`1920x1080`、DXVA `hw=true`、鼠标悬停/左键/右键/滚轮/最近任务、动态缩放至 `1600x900`）均已在自动化套件与 `androfuse.exe` 桌面窗口实测正常（`leases.json` 零残留）。**Miracast**：因依赖未公开 `WlanNotifyVsIeProviderInt` 且触发 `qcwlan8280.sys` 内核 `BugCheck 0x7E` 已全平台隐藏禁用。**独立应用窗口体验**：下一阶段在模拟器按 AndroMeld 标准重构无边框窗口与图标网格 |
| Android 模拟器（`Pixel_9a_API_36`，Android 16 / SDK 36）与普通 Android 设备 | 已在 AVD 实测通过（M1b） | `TestLiveEmulatorAppWindowsAndIcons` 在 `Pixel_9a_API_36`（`arm64-v8a` WHPX）实测通过：`ListAppsWithIcons` 成功提取 18/18 真实本地化应用名称与 `96x96` PNG 图标并过滤 WebAPK；并发 2 路 `vd_system_decorations=false` + `flex_display=true` 无边框独立应用窗口（`544x960` 动态缩放至 `720x1280`、`resizedAck=true`、DXVA `hw=true`、音频 Socket 激活），退出后零残留虚拟屏与 `leases.json` 记录（`INV-03`、`INV-06`） |
| Android Companion APK（`arm64-v8a`） | CI 已构建并在模拟器实测通过（M3b） | GitHub Actions `#45528710584`（`Android Companion (arm64-v8a)`）产出 `AndroFuse-0.1.0-android-arm64.apk`（`15,062,609` 字节）并通过 `zipalign -c -P 16 4`（16 KB 页对齐）；已实测安装并冷启动至 `Pixel_9a_API_36`（`arm64-v8a`，Android 16 / SDK 36）：`adb install -r` 成功、`com.yuanzhe.androfuse/dev.mygo.MyGoActivity` 启动正常（`TotalTime: 3316ms`、`libmygoapp.so` 加载成功、`PID=2714`、零崩溃），同时 `internal/companion` TLS 1.3 与 `internal/androidhost` 单测全部通过 |
