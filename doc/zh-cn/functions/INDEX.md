# 模块与包索引

| 包 / Crate | 职责 | 关键导出 / 符号 |
|---|---|---|
| `cmd/androfuse` | 统一 MyGo 原生 UI 入口 | `main` |
| `cmd/androfuse-svc` | 无头引擎命令行（`version`、`check`、`probe`） | `main`、`runFullProbe` |
| `internal/version` | 全局唯一版本号（`0.1.0`） | `Version` |
| `internal/core` | Rust 核心 C-ABI v3 加载器与零分配 NV12 环形缓冲 | `Open`、`Lib.Start`、`Lib.SelfCheck`、`Lib.OpenDecoder`、`Decoder.PullInto`、`NV12Ring` |
| `internal/adb` | 无 Shell `adb` 客户端、mDNS TLS 无线连接配对与 `--user 0` 设备探测 | `FindClient`、`MaskSerial`、`MaskEndpoint`、`Client.Devices`、`Client.MDNSServices`、`Client.ConnectTLS`、`Client.PairTLS`、`Client.WakeScreen`、`Client.ListLaunchableApps` |
| `internal/lease` | 原子 `leases.json` 崩溃回收租约账本 | `Open`、`Store.Acquire`、`Store.Release`、`Store.List`、`Store.RecoverAll` |
| `internal/scrcpy` | `scrcpy-server v5.0.1` 线协议、交互控制、会话调度与回放样本 | `BuildServerArgs`、`StartADB`、`Session.Resize`、`Session.InjectTouch`、`Session.InjectScroll`、`Session.InjectKeycode`、`Session.InjectText`、`StartMockFixture` |
| `internal/dex` | Samsung Native DeX 调度器（`server/dexhelper` 回环；Miracast 已隐藏） | `PushAndProbe`、`StartNativeLoopback`、`LoopbackSession.Resize`、`Recover`、`EvaluateTruth` |
| `internal/gui` | MyGo 原生界面外壳、交互式 `SessionWindow`、`InputTracker`、启动器与托盘 | `New`、`ApplyTheme`、`RunDiagnostics`、`NewTray`、`SessionWindow`、`InputTracker`、`App.LaunchDeX` |
| `locales` | 英文与简体中文 ARB 词条目录 | `catalog_gen.go` |
| `server/dexhelper` | UID 2000 Java `app_process` 辅助器（`probe`、`serve`、`recover`） | `com.androfuse.dexhelper.Main` |
| `tools/fetchdeps` | 带 SHA-256 校验的 `scrcpy-server-v5.0.1` 下载器 | `main` |
| `tools/builddex` | 编译并 dex 打包 `androfuse-dex-helper.jar` | `main` |
| `tools/icon` | SVG 转 PNG 图标渲染器 | `main` |
| `tools/checkdocs` | 校验 `doc/en-us` ↔ `doc/zh-cn` 文档 1:1 一致性 | `main` |
| `core/androfuse-core` | Rust 核心库（C ABI v3、自检、`scrcpy`、Miracast 接收器、MF NV12） | `androfuse_abi_version`、`androfuse_decoder_*`、`androfuse_miracast_*`、`miracast::MiracastReceiver`、`mf_video` |
