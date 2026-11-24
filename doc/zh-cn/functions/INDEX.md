# 模块与包索引

| 包 / Crate | 职责 | 关键导出 / 符号 |
|---|---|---|
| `cmd/androfuse` | 统一 MyGo 原生 UI 入口 | `main` |
| `cmd/androfuse-svc` | 无头引擎命令行（`version`、`check`） | `main` |
| `internal/version` | 全局唯一版本号（`0.1.0`） | `Version` |
| `internal/core` | Rust 核心 C-ABI 加载器（`LoadLibraryEx` / `purego`） | `Open`、`Lib.Start`、`Lib.SelfCheck`、`Lib.Status`、`Lib.Stop` |
| `internal/gui` | MyGo 原生界面外壳、托盘、设计 Token 与主机自检 | `New`、`ApplyTheme`、`RunDiagnostics`、`NewTray` |
| `locales` | 英文与简体中文 ARB 词条目录 | `catalog_gen.go` |
| `tools/icon` | SVG 转 PNG 图标渲染器 | `main` |
| `tools/checkdocs` | 校验 `doc/en-us` ↔ `doc/zh-cn` 文档 1:1 一致性 | `main` |
| `core/androfuse-core` | Rust 核心库（C ABI v1 与主机自检） | `androfuse_abi_version`、`androfuse_start`、`androfuse_call`、`androfuse_free`、`androfuse_stop`、`diag::run_self_check` |
