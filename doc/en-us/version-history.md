# Version History

## Unreleased (`0.1.0` M0 Baseline)

- Initialized unified Go + MyGo (`v0.10.0`) + Rust (`core/androfuse-core`, ABI v1) project structure under `GPL-3.0-only`.
- Integrated `resources/icon.svg` and deterministic PNG rendering pipeline (`tools/icon`) for desktop, UI, tray, and Android adaptive icon foreground.
- Implemented passive host self-check diagnostics (`diagnostics.self_check`): Windows native vs WOW64 emulation detection, Media Foundation H.264/HEVC decoder enumeration, Wi-Fi Direct / Miracast symbol probing, and ADB path detection.
- Completed code, license, and behavior audit of MyGo, Kyokai, scrcpy, DeXDesk, ScrcpyDeX, DX-Manager, MyDeX, DeXter, and AndroMeld UX; consolidated full product plan in `PLAN.md`.
- Checked upstreams: Go `1.27.4` / MyGo `v0.10.0` / Rust `1.99.1` / scrcpy `v5.0.1` / Kyokai `v0.8.1` (checked `2026-11-23`).
