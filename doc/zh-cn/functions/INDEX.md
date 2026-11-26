# 模块与包索引

| 包 / Crate | 职责 | 关键导出 / 符号 |
|---|---|---|
| `cmd/androfuse` | 统一 MyGo 原生 UI 入口 | `main` |
| `cmd/androfuse-svc` | 无头引擎命令行（`version`、`check`、`probe`、`run`） | `main`、`runFullProbe`、`runDaemon` |
| `internal/version` | 全局唯一版本号（`0.1.0`） | `Version` |
| `internal/core` | Rust 核心 C-ABI v6 加载器、零分配 NV12 环形缓冲与 WASAPI 播放/采集 | `Open`、`Lib.Start`、`Lib.SelfCheck`、`Lib.OpenDecoder`、`Lib.OpenAudioPlayer`、`Lib.OpenMicCapture`、`Decoder.PullInto`、`NV12Ring`、`AudioPlayer`、`MicCapturer` |
| `internal/localapi` | 同用户 AF_UNIX NDJSON 服务（Windows SID 校验） | `Listen`、`Server.Serve`、`RecoverLeases`、`Call` |
| `internal/vm` | 按需下载的轻量无头 Android 虚拟机管理器（`INV-03`、`INV-10`） | `NewManager`、`NativeABIForArch`、`DefaultVMRoot`、`ParseCmdlineToolsManifest`、`Manager.Status`、`Manager.Provision`、`Manager.Start`、`Manager.Stop`、`Manager.DeleteManaged` |
| `internal/companion` | TLS 1.3 加密 Companion NDJSON 协议栈（`INV-07`、`INV-09`） | `NewServer`、`PickPort`、`Dial`、`GenerateSelfSignedCert`、`GeneratePairingToken`、`SanitizeFileName`、`Client.GetStatus`、`Client.PushFile`、`Client.RequestContinuity`、`Client.RunDisplayGuard`、`RequestPairing`、`Client.JoinMesh` |
| `internal/androidhost` | 可选 Android Companion APK 宿主状态、授权管理与 MyGo 移动界面（`INV-09`） | `New`、`Host.StartServer`、`Host.StopServer`、`Host.SetPermission`、`Host.SetStandby`、`Host.JoinMesh`、`Host.LeaveMesh`、`Host.SetMeshFullSpeed`、`Host.AnswerPair`、`Host.RotateToken`、`Host.SetWirelessADBPort`、`RenderCompanionView` |
| `internal/androidbridge` | 平台安全的 Android 系统服务与桌面小组件状态桥接（配合 `CompanionServices.kt`） | `Capabilities`、`WidgetState`、`Probe`、`UpdateGranted`、`SyncCompanionWidget`、`CurrentWidgetState`、`TouchActive`、`ReleaseActive`、`ActiveHeld`、`SetStandby`、`WatchNetwork` |
| `internal/androidcore` | 在 APK 中携带并加载 Android 核心库，提供 Keystore 密封 | `NewLibrary`、`Library.Load`、`NewVault`、`Vault.Seal`、`Vault.Open` |
| `internal/avbridge` | M5 手机摄像头本地 MJPEG 服务、`MFCreateVirtualCamera` 探测与 PC→Android 音频/视频传入 | `ProbeVirtualCamera`、`NewMJPEGServer`、`MJPEGServer.Start`、`MJPEGServer.PushNV12`、`NewUplinkBridge`、`UplinkBridge.PushMicPCM`、`UplinkBridge.PushCameraJPEG`、`UplinkBridge.StreamMic` |
| `internal/mcp` | 默认关闭的本机回环 MCP 服务（TTL 令牌、白名单、紧急停止与脱敏审计，`INV-07`、`INV-08`） | `NewServer`、`Server.Enable`、`Server.EmergencyStop`、`Server.Status`、`Server.ServeHTTP`、`Allowlist`、`AuditEntry` |
| `internal/mesh` | 内置于核心库的 EasyTier v2.7.1 组网客户端（无 TUN，`INV-07`） | `NewManager`、`Manager.Start`、`Manager.Stop`、`Manager.Status`、`Manager.PeerIPv4`、`Hostname`、`ParseHostname`、`SameDevice` |
| `internal/bundlesig` | Ed25519 辅助包签名与校验器（`bundle.json` / `bundle.sig`） | `Sign`、`Verify`、`Manifest` |
| `internal/adb` | 无 Shell `adb` 客户端、多传输去重聚合、mDNS TLS 无线连接配对与 `--user 0` 设备探测 | `FindClient`、`MaskSerial`、`MaskEndpoint`、`MergeDevices`、`UnifiedDevice`、`Client.Devices`、`Client.MDNSServices`、`Client.ConnectTLS`、`Client.PairTLS`、`Client.WakeScreen`、`Client.ListLaunchableApps`、`IsNoisyAppPackage` |
| `internal/sysproc` | Windows 下无控制台窗口的子进程 | `HideConsole` |
| `internal/lease` | 原子 `leases.json` 崩溃回收租约账本 | `Open`、`Store.Acquire`、`Store.Release`、`Store.List`、`Store.RecoverAll` |
| `internal/scrcpy` | `scrcpy-server v5.0.1` 线协议、交互控制、音频/剪贴板、MP4 录屏与回放样本 | `BuildServerArgs`、`StartADB`、`Session.Resize`、`Session.SetClipboard`、`Session.StartRecording`、`MP4Recorder`、`StartMockFixture` |
| `internal/dex` | Samsung Native DeX 调度器（`server/dexhelper` 回环与宽限重连）与应用图标缓存 | `PushAndProbe`、`StartNativeLoopback`、`LoopbackSession.Reattach`、`LoopbackSession.Resize`、`Recover`、`SelectRoute`、`FormatOneUIVersion`、`EvaluateTruth`、`ListAppsWithIcons` |
| `internal/gui` | MyGo 原生界面外壳、统一设备卡片、应用启动器网格、无边框 `SessionModeApp`、`SaveNV12AsPNG`、`InputTracker` 与托盘 | `New`、`ApplyTheme`、`RunDiagnostics`、`NewTray`、`SessionWindow`、`EncoderHeadroom`、`SaveNV12AsPNG`、`InputTracker`、`App.LaunchDeX`、`App.LaunchAppWindow`、`App.PairCompanionOverADB`、`App.HandleURL`、`App.ReconnectCompanion`、`App.ForgetDevice`、`SessionWindow.PopOut`、`SessionWindow.Dock` |
| `locales` | 英文与简体中文 ARB 词条目录 | `catalog_gen.go` |
| `server/dexhelper` | UID 2000 Java `app_process` 辅助器（`probe`、`apps`、`serve`、`attach`、`recover`） | `com.androfuse.dexhelper.Main` |
| `tools/fetchdeps` | 带 SHA-256 校验的 `scrcpy-server-v5.0.1` 与构建期 `protoc` 下载器；EasyTier 上游版本检查 | `main`、`fetchProtoc`、`runUpstreamCheck` |
| `tools/builddex` | 编译并 dex 打包 `androfuse-dex-helper.jar` | `main` |
| `tools/buildapk` | Android `arm64-v8a` Companion APK 构建、原生库存在性与 16 KB `zipalign` 校验器 | `main`、`ensureGradlewBat`、`findJDK17`、`requireNativeLibs` |
| `tools/sign` | Ed25519 发布密钥生成与 `bundle.json`/`bundle.sig` 签名器 | `main`、`keygen`、`bundle` |
| `tools/checkpe` | 原生 Windows PE 机器码（`0xAA64`/`0x8664`）与 GUI 子系统校验器（`INV-10`） | `CheckDir`、`ExpectedMachine` |
| `tools/icon` | SVG 转 PNG 图标渲染器 | `main` |
| `tools/checkdocs` | 校验 `doc/en-us` ↔ `doc/zh-cn` 文档 1:1 一致性 | `main` |
| `core/androfuse-core` | Rust 核心库（C ABI v6、自检、`scrcpy`、Miracast 接收器、MF NV12、WASAPI 音频与麦克风采集） | `mesh::start`、`mesh::stop`、`mesh::status`、`androfuse_abi_version`、`androfuse_decoder_*`、`androfuse_audio_*`、`androfuse_mic_*`、`androfuse_miracast_*`、`wasapi_audio::WasapiAudioPlayer`、`wasapi_audio::WasapiMicCapturer`、`mf_video` |
