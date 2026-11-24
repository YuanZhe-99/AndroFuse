<p align="center">
  <img src="resources/icon.svg" width="96" height="96" alt="AndroFuse icon" />
</p>

<h1 align="center">AndroFuse</h1>

<p align="center">
  <strong>Open-source Samsung DeX, Android phone mirroring, and independent Android app windows for Windows 11 (x64 &amp; native ARM64)</strong><br>
  Built with <a href="https://github.com/egoist/mygo">MyGo</a> native GPU UI, Go orchestration, and a shared Rust core — no Root required.
</p>

---

> **Current Status (`0.1.0` — M0 Architecture Baseline):** Project scaffolding, license & upstream research, icon asset pipeline, Go ↔ Rust C-ABI boundary (`androfuse_core`), MyGo native UI shell, and live host self-check diagnostics (`androfuse-svc check`) are implemented and verified on Windows 11 ARM64. Streaming, Miracast/ADB transports, DeX activation, independent app windows, and the optional Android Companion APK are specified in [PLAN.md](PLAN.md) across Milestones **M1–M5**.

[Project Plan (中文)](PLAN.md) · [Documentation](doc/en-us/README.md) · [中文文档](doc/zh-cn/README.md)

## Target Modes

1. **Samsung DeX Desktop (M2)**: Genuine Samsung DeX in a native Windows window via wireless Miracast (no USB debugging forced) or USB/Wi-Fi ADB loopback activation, with verified multi-window, taskbar, Recents, and desktop mouse behavior plus safe virtual-display fallback.
2. **Android Phone Mirroring (M1)**: Low-latency primary display mirroring over USB/Wi-Fi ADB with keyboard/mouse, audio, clipboard, orientation switching, fullscreen, screenshots, recording, and screen-on/screen-off modes.
3. **Independent App Windows (M1)**: Each Android app runs in its own native Windows window (taskbar, Alt+Tab, resize, Snap Layouts) backed by an isolated Android virtual display without occupying the physical phone screen.

All three modes are designed to coexist whenever device hardware and encoders permit, with real capability probing and transparent user feedback.

## Quick Verification (M0)

```bash
cargo build --manifest-path core/Cargo.toml --workspace
go run ./cmd/androfuse-svc check --json
CGO_ENABLED=0 go test ./...
go tool mygo vet .
go tool mygo i18n check
go run ./tools/checkdocs
```

## License & Trademarks

AndroFuse is licensed under **GPL-3.0-only** ([LICENSE](LICENSE)). See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for upstream attributions.

"Samsung", "Samsung DeX", "Galaxy", and "One UI" are trademarks of Samsung Electronics Co., Ltd. "Android" is a trademark of Google LLC. AndroFuse is an independent open-source project.
