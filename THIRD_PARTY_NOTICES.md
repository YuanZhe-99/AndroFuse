# Third-Party Notices

AndroFuse is licensed under **GNU General Public License v3.0 only** (`GPL-3.0-only`). See [LICENSE](LICENSE).

## Active Dependencies (M0–M1a)

### Go Modules
- **MyGo** (`github.com/egoist/mygo`) — MIT License. Native GPU UI toolkit, `ui.Bitmap.UpdateYUV` NV12 shader, windowing, tray, and Android/Windows build pipeline.
- **purego** (`github.com/ebitengine/purego`) — Apache-2.0 License. Zero-cgo C ABI loader on Unix/Android.
- **golang.org/x/sys** — BSD-3-Clause License. Low-level OS system calls.

### Rust Crates
- **serde**, **serde_json** — MIT OR Apache-2.0.
- **windows**, **windows-sys** — MIT OR Apache-2.0. Windows Media Foundation, D3D11/DXGI, Wi-Fi Direct WinRT, and Win32 system APIs.
- **EasyTier** v2.7.1 (`easytier-ffi`, `easytier`, `easytier-core`, `easytier-proto`, and the EasyTier organisation's forks of `tun`, `tokio-websockets`, `kcp-sys`, `windivert`, `service-manager`, `thunk`) — LGPL-3.0, Copyright (C) EasyTier contributors (https://github.com/EasyTier/EasyTier). Statically linked into `androfuse_core` as the embedded no-TUN mesh; AndroFuse as a whole is distributed under GPL-3.0-only, and the corresponding source of the pinned revision is available upstream. Its transitive crates add 0BSD, CDLA-Permissive-2.0, bzip2-1.0.6, CC0-1.0 and WTFPL licences (see `core/deny.toml`).
- **protoc** 36.2 (Protocol Buffers) — BSD-3-Clause. Build-time tool only, fetched by `tools/fetchdeps`; not distributed.

## Integrated Protocols & Upstream References (M1a–M4)

Every reused or adapted component retains its upstream license text and attribution:
- **Genymobile/scrcpy** (`scrcpy-server` v5.0.1 / wire protocol) — Apache-2.0. Downloaded dynamically into `.dev/cache/` by `tools/fetchdeps` with pinned SHA-256 verification.
- **Android Platform Tools (`adb`)** — Apache-2.0.
- **EasyTier** (optional user-supplied external binary driven in `--no-tun` userspace mode via `internal/mesh`) — LGPL-3.0. Never statically linked or bundled directly inside the AndroFuse Go/Rust binaries.
- **adirh3/DeXDesk** (Miracast / Wi-Fi Direct Sink, RTSP/RTP/MPEG-TS, UIBC, Media Foundation DXVA reference) — MIT License.
- **Aureliano021/ScrcpyDex** (`SemWifiDisplayConfig` loopback DeX activator reference) — Apache-2.0 License.
- **maze-mei/DX-Manager** (multi-session registry, initial DPI ≥ 160 bootstrap & companion UX reference) — MIT License.
- **Keremerk/MyDeX** (virtual display behavior findings) — MIT License.
- **fabriciolima77/DeXter** (fallback research reference) — GPL-3.0-or-later License.
- **AndroMeld** (`catchingnow`) — Proprietary commercial product; referenced strictly at a functional/UX level. No APK, bytecode, source code, or asset from AndroMeld may ever be copied, decompiled, or redistributed.
- **YuanZhe-99/Kyokai** — AGPL-3.0-only (same author). Only architectural patterns and author-owned scaffolding are adapted; no AGPL RustDesk vendored code is copied into AndroFuse.

## Trademarks

"Samsung", "Samsung DeX", "Galaxy", and "One UI" are trademarks of Samsung Electronics Co., Ltd.
"Android" is a trademark of Google LLC.
"Windows" is a trademark of Microsoft Corporation.
AndroFuse is an independent open-source project and is not affiliated with or endorsed by Samsung, Google, or Microsoft.
