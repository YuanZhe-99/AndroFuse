# Functions Index

| Package / Crate | Role | Key Symbols |
|---|---|---|
| `cmd/androfuse` | Unified MyGo native GUI entry point | `main` |
| `cmd/androfuse-svc` | Headless engine CLI (`version`, `check`, `probe`, `run`) | `main`, `runFullProbe`, `runDaemon` |
| `internal/version` | Single product version (`0.1.0`) | `Version` |
| `internal/core` | Rust core C-ABI v4 loader, zero-alloc NV12 ring & WASAPI audio player | `Open`, `Lib.Start`, `Lib.SelfCheck`, `Lib.OpenDecoder`, `Lib.OpenAudioPlayer`, `Decoder.PullInto`, `NV12Ring`, `AudioPlayer` |
| `internal/localapi` | Per-user AF_UNIX NDJSON server with Windows peer SID verification | `Listen`, `Server.Serve`, `RecoverLeases`, `Call` |
| `internal/vm` | On-demand downloadable headless Android VM manager (`INV-03`, `INV-10`) | `NewManager`, `NativeABIForArch`, `DefaultVMRoot`, `ParseCmdlineToolsManifest`, `Manager.Status`, `Manager.Provision`, `Manager.Start`, `Manager.Stop`, `Manager.DeleteManaged` |
| `internal/companion` | Encrypted TLS 1.3 NDJSON Companion client/server protocol (`INV-07`, `INV-09`) | `NewServer`, `Dial`, `GenerateSelfSignedCert`, `GeneratePairingToken`, `SanitizeFileName`, `Client.GetStatus`, `Client.PushFile`, `Client.RequestContinuity`, `Client.RunDisplayGuard` |
| `internal/androidhost` | Optional Android Companion APK host state, permissions & MyGo mobile UI (`INV-09`) | `New`, `Host.StartServer`, `Host.StopServer`, `Host.SetPermission`, `Host.RotateToken`, `Host.SetWirelessADBPort`, `RenderCompanionView` |
| `internal/androidbridge` | Platform-safe Android service capability & widget state bridge paired with `CompanionServices.kt` | `Capabilities`, `WidgetState`, `Probe`, `UpdateGranted`, `SyncCompanionWidget`, `CurrentWidgetState` |
| `internal/avbridge` | M5 phone-camera loopback MJPEG server, Windows `MFCreateVirtualCamera` probe & PC→Android uplink | `ProbeVirtualCamera`, `NewMJPEGServer`, `MJPEGServer.Start`, `MJPEGServer.PushNV12`, `NewUplinkBridge`, `UplinkBridge.PushMicPCM`, `UplinkBridge.PushCameraJPEG` |
| `internal/mcp` | Opt-in default-disabled loopback MCP server with TTL token, allowlist & emergency stop (`INV-07`, `INV-08`) | `NewServer`, `Server.Enable`, `Server.EmergencyStop`, `Server.Status`, `Server.ServeHTTP`, `Allowlist`, `AuditEntry` |
| `internal/mesh` | Optional default-disabled userspace EasyTier `--no-tun` supervisor (`INV-07`) | `BuildArgs`, `ScrubArgs`, `FindBinary`, `NewManager`, `Manager.Start`, `Manager.Stop`, `Manager.Status` |
| `internal/bundlesig` | Ed25519 helper bundle signing & verification (`bundle.json` / `bundle.sig`) | `Sign`, `Verify`, `Manifest` |
| `internal/adb` | Argument-vector ADB client, multi-transport deduplication, mDNS TLS wireless connect/pair & `--user 0` prober | `FindClient`, `MaskSerial`, `MaskEndpoint`, `MergeDevices`, `UnifiedDevice`, `Client.Devices`, `Client.MDNSServices`, `Client.ConnectTLS`, `Client.PairTLS`, `Client.WakeScreen`, `Client.ListLaunchableApps`, `IsNoisyAppPackage` |
| `internal/lease` | Atomic `leases.json` crash-recovery ledger | `Open`, `Store.Acquire`, `Store.Release`, `Store.List`, `Store.RecoverAll` |
| `internal/scrcpy` | `scrcpy-server v5.0.1` wire framing, interactive input, audio/clipboard, MP4 muxer & replay fixtures | `BuildServerArgs`, `StartADB`, `Session.Resize`, `Session.SetClipboard`, `Session.StartRecording`, `MP4Recorder`, `StartMockFixture` |
| `internal/dex` | Samsung Native DeX orchestration (`server/dexhelper` loopback + grace re-attach) & launcher icon cache | `PushAndProbe`, `StartNativeLoopback`, `LoopbackSession.Reattach`, `LoopbackSession.Resize`, `Recover`, `SelectRoute`, `FormatOneUIVersion`, `EvaluateTruth`, `ListAppsWithIcons` |
| `internal/gui` | MyGo native UI shell, unified device cards, App Launcher Grid, chromeless `SessionModeApp`, `SaveNV12AsPNG`, `InputTracker` & tray | `New`, `ApplyTheme`, `RunDiagnostics`, `NewTray`, `SessionWindow`, `EncoderHeadroom`, `SaveNV12AsPNG`, `InputTracker`, `App.LaunchDeX`, `App.LaunchAppWindow` |
| `locales` | English & Simplified Chinese ARB catalogs | `catalog_gen.go` |
| `server/dexhelper` | UID 2000 Java `app_process` helper (`probe`, `apps`, `serve`, `attach`, `recover`) | `com.androfuse.dexhelper.Main` |
| `tools/fetchdeps` | Verified `scrcpy-server-v5.0.1` downloader | `main` |
| `tools/builddex` | `javac` + `d8` builder for `androfuse-dex-helper.jar` | `main` |
| `tools/buildapk` | Android `arm64-v8a` Companion APK builder & 16 KB `zipalign` verifier | `main` |
| `tools/sign` | Ed25519 release keygen & `bundle.json`/`bundle.sig` signer | `main`, `keygen`, `bundle` |
| `tools/checkpe` | Native Windows PE `Machine` (`0xAA64`/`0x8664`) verifier (`INV-10`) | `CheckDir`, `ExpectedMachine` |
| `tools/icon` | SVG-to-PNG icon renderer | `main` |
| `tools/checkdocs` | Enforces 1:1 `doc/en-us` ↔ `doc/zh-cn` parity | `main` |
| `core/androfuse-core` | Rust core library (C ABI v4, diagnostics, `scrcpy`, Miracast sink, MF NV12, WASAPI audio) | `androfuse_abi_version`, `androfuse_decoder_*`, `androfuse_audio_*`, `androfuse_miracast_*`, `wasapi_audio::WasapiAudioPlayer`, `mf_video` |
