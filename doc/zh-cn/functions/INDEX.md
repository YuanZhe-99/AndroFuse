# 模块与包索引

| 包 / Crate | 职责 | 关键导出 / 符号 |
|---|---|---|
| `cmd/androfuse` | 统一 MyGo 原生 UI 入口 | `main` |
| `cmd/androfuse-svc` | 无头引擎命令行（`version`、`check`、`probe`、`run`） | `main`、`runFullProbe`、`runDaemon` |
| `internal/version` | 全局唯一版本号（`0.1.0`） | `Version` |
| `internal/core` | Rust 核心 C-ABI v4 加载器、零分配 NV12 环形缓冲与 WASAPI 音频 | `Open`、`Lib.Start`、`Lib.SelfCheck`、`Lib.OpenDecoder`、`Lib.OpenAudioPlayer`、`Decoder.PullInto`、`NV12Ring`、`AudioPlayer` |
| `internal/localapi` | 同用户 AF_UNIX NDJSON 服务（Windows SID 校验） | `Listen`、`Server.Serve`、`RecoverLeases`、`Call` |
| `internal/vm` | 按需下载的轻量无头 Android 虚拟机管理器（`INV-03`、`INV-10`） | `NewManager`、`NativeABIForArch`、`DefaultVMRoot`、`ParseCmdlineToolsManifest`、`Manager.Status`、`Manager.Provision`、`Manager.Start`、`Manager.Stop`、`Manager.DeleteManaged` |
| `internal/companion` | TLS 1.3 加密 Companion NDJSON 协议栈（`INV-07`、`INV-09`） | `NewServer`、`Dial`、`GenerateSelfSignedCert`、`GeneratePairingToken`、`SanitizeFileName`、`Client.GetStatus`、`Client.PushFile`、`Client.RequestContinuity`、`Client.RunDisplayGuard` |
| `internal/androidhost` | 可选 Android Companion APK 宿主状态、授权管理与 MyGo 移动界面（`INV-09`） | `New`、`Host.StartServer`、`Host.StopServer`、`Host.SetPermission`、`Host.RotateToken`、`Host.SetWirelessADBPort`、`RenderCompanionView` |
| `internal/adb` | 无 Shell `adb` 客户端、多传输去重聚合、mDNS TLS 无线连接配对与 `--user 0` 设备探测 | `FindClient`、`MaskSerial`、`MaskEndpoint`、`MergeDevices`、`UnifiedDevice`、`Client.Devices`、`Client.MDNSServices`、`Client.ConnectTLS`、`Client.PairTLS`、`Client.WakeScreen`、`Client.ListLaunchableApps`、`IsNoisyAppPackage` |
| `internal/lease` | 原子 `leases.json` 崩溃回收租约账本 | `Open`、`Store.Acquire`、`Store.Release`、`Store.List`、`Store.RecoverAll` |
| `internal/scrcpy` | `scrcpy-server v5.0.1` 线协议、交互控制、音频/剪贴板、MP4 录屏与回放样本 | `BuildServerArgs`、`StartADB`、`Session.Resize`、`Session.SetClipboard`、`Session.StartRecording`、`MP4Recorder`、`StartMockFixture` |
| `internal/dex` | Samsung Native DeX 调度器（`server/dexhelper` 回环与宽限重连）与应用图标缓存 | `PushAndProbe`、`StartNativeLoopback`、`LoopbackSession.Reattach`、`LoopbackSession.Resize`、`Recover`、`SelectRoute`、`FormatOneUIVersion`、`EvaluateTruth`、`ListAppsWithIcons` |
| `internal/gui` | MyGo 原生界面外壳、统一设备卡片、应用启动器网格、无边框 `SessionModeApp`、`SaveNV12AsPNG`、`InputTracker` 与托盘 | `New`、`ApplyTheme`、`RunDiagnostics`、`NewTray`、`SessionWindow`、`EncoderHeadroom`、`SaveNV12AsPNG`、`InputTracker`、`App.LaunchDeX`、`App.LaunchAppWindow` |
| `locales` | 英文与简体中文 ARB 词条目录 | `catalog_gen.go` |
| `server/dexhelper` | UID 2000 Java `app_process` 辅助器（`probe`、`apps`、`serve`、`attach`、`recover`） | `com.androfuse.dexhelper.Main` |
| `tools/fetchdeps` | 带 SHA-256 校验的 `scrcpy-server-v5.0.1` 下载器 | `main` |
| `tools/builddex` | 编译并 dex 打包 `androfuse-dex-helper.jar` | `main` |
| `tools/icon` | SVG 转 PNG 图标渲染器 | `main` |
| `tools/checkdocs` | 校验 `doc/en-us` ↔ `doc/zh-cn` 文档 1:1 一致性 | `main` |
| `core/androfuse-core` | Rust 核心库（C ABI v4、自检、`scrcpy`、Miracast 接收器、MF NV12、WASAPI 音频） | `androfuse_abi_version`、`androfuse_decoder_*`、`androfuse_audio_*`、`androfuse_miracast_*`、`wasapi_audio::WasapiAudioPlayer`、`mf_video` |
