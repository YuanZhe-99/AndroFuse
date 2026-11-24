# Functions Index

| Package / Crate | Role | Key Symbols |
|---|---|---|
| `cmd/androfuse` | Unified MyGo native GUI entry point | `main` |
| `cmd/androfuse-svc` | Headless engine CLI (`version`, `check`, `probe`) | `main`, `runFullProbe` |
| `internal/version` | Single product version (`0.1.0`) | `Version` |
| `internal/core` | Rust core C-ABI v3 loader & zero-alloc NV12 ring | `Open`, `Lib.Start`, `Lib.SelfCheck`, `Lib.OpenDecoder`, `Decoder.PullInto`, `NV12Ring` |
| `internal/adb` | Argument-vector ADB client, mDNS TLS wireless connect/pair & `--user 0` prober | `FindClient`, `MaskSerial`, `MaskEndpoint`, `Client.Devices`, `Client.MDNSServices`, `Client.ConnectTLS`, `Client.PairTLS`, `Client.WakeScreen`, `Client.ListLaunchableApps` |
| `internal/lease` | Atomic `leases.json` crash-recovery ledger | `Open`, `Store.Acquire`, `Store.Release`, `Store.List`, `Store.RecoverAll` |
| `internal/scrcpy` | `scrcpy-server v5.0.1` wire framing, interactive input, session & replay fixtures | `BuildServerArgs`, `StartADB`, `Session.Resize`, `Session.InjectTouch`, `Session.InjectScroll`, `Session.InjectKeycode`, `Session.InjectText`, `StartMockFixture` |
| `internal/dex` | Samsung Native DeX orchestration (`server/dexhelper` loopback; Miracast hidden) | `PushAndProbe`, `StartNativeLoopback`, `LoopbackSession.Resize`, `Recover`, `EvaluateTruth` |
| `internal/gui` | MyGo native UI shell, interactive `SessionWindow`, `InputTracker`, launcher & tray | `New`, `ApplyTheme`, `RunDiagnostics`, `NewTray`, `SessionWindow`, `InputTracker`, `App.LaunchDeX` |
| `locales` | English & Simplified Chinese ARB catalogs | `catalog_gen.go` |
| `server/dexhelper` | UID 2000 Java `app_process` helper (`probe`, `serve`, `recover`) | `com.androfuse.dexhelper.Main` |
| `tools/fetchdeps` | Verified `scrcpy-server-v5.0.1` downloader | `main` |
| `tools/builddex` | `javac` + `d8` builder for `androfuse-dex-helper.jar` | `main` |
| `tools/icon` | SVG-to-PNG icon renderer | `main` |
| `tools/checkdocs` | Enforces 1:1 `doc/en-us` ↔ `doc/zh-cn` parity | `main` |
| `core/androfuse-core` | Rust core library (C ABI v3, diagnostics, `scrcpy`, Miracast sink, MF NV12) | `androfuse_abi_version`, `androfuse_decoder_*`, `androfuse_miracast_*`, `miracast::MiracastReceiver`, `mf_video` |
