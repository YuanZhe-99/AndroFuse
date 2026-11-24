# Functions Index

| Package / Crate | Role | Key Symbols |
|---|---|---|
| `cmd/androfuse` | Unified MyGo native GUI entry point | `main` |
| `cmd/androfuse-svc` | Headless engine CLI (`version`, `check`) | `main` |
| `internal/version` | Single product version (`0.1.0`) | `Version` |
| `internal/core` | Rust core C-ABI loader (`LoadLibraryEx` / `purego`) | `Open`, `Lib.Start`, `Lib.SelfCheck`, `Lib.Status`, `Lib.Stop` |
| `internal/gui` | MyGo native UI shell, tray, tokens, host diagnostics | `New`, `ApplyTheme`, `RunDiagnostics`, `NewTray` |
| `locales` | English & Simplified Chinese ARB catalogs | `catalog_gen.go` |
| `tools/icon` | SVG-to-PNG icon renderer | `main` |
| `tools/checkdocs` | Enforces 1:1 `doc/en-us` ↔ `doc/zh-cn` parity | `main` |
| `core/androfuse-core` | Rust core library (C ABI v1 + host self-check) | `androfuse_abi_version`, `androfuse_start`, `androfuse_call`, `androfuse_free`, `androfuse_stop`, `diag::run_self_check` |
