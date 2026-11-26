# Third-Party Notices & Licensing

AndroFuse is licensed under **GNU General Public License v3.0 only** (`GPL-3.0-only`, see [LICENSE](../../LICENSE)).

## License Compatibility

| Component | License | Status | Notes |
|---|---|---|---|
| `github.com/egoist/mygo` (`v0.10.0`) | MIT | Active (M0) | Compatible with `GPL-3.0-only` |
| `github.com/ebitengine/purego` | Apache-2.0 | Active (M0) | Compatible with `GPL-3.0-only` |
| `golang.org/x/sys` | BSD-3-Clause | Active (M0) | Compatible with `GPL-3.0-only` |
| Rust `serde`, `serde_json`, `windows-sys`, `windows` | MIT OR Apache-2.0 | Active (M1a) | Verified via `cargo deny check licenses bans sources` |
| `Genymobile/scrcpy` (`scrcpy-server` v5.0.1) | Apache-2.0 | Active (M1a) | Fetched with pinned SHA-256 by `tools/fetchdeps`; wire protocol integrated |
| Android Platform Tools (`adb`) | Apache-2.0 | Active (M1a) | Host discovery & transport via `internal/adb`; ship notice when bundled |
| `EasyTier/EasyTier` v2.7.1 (`easytier-ffi`, `easytier`, `easytier-core`, `easytier-proto` and EasyTier-org forks) | LGPL-3.0 | Active | Statically linked into `androfuse_core` (compatible with `GPL-3.0-only`); pinned rev `0d9c52b`; `packet.dll` delay-loaded and never shipped; the RustSec advisories in its own dependency tree (hickory 0.25, build-time `zip`, unmaintained crates) are not patched here but followed through EasyTier updates, each listed with a reason in `core/deny.toml` and re-checked whenever the pinned revision moves |
| Transitive Rust crates of EasyTier (`smoltcp`, `webpki-roots`, `libbz2-rs-sys`, `encoding-index-*`, `tun`, …) | 0BSD, CDLA-Permissive-2.0, bzip2-1.0.6, CC0-1.0, WTFPL | Active | All GPL-3.0-compatible; allowed explicitly in `core/deny.toml` |
| `protocolbuffers/protobuf` (`protoc` 36.2) | BSD-3-Clause | Build-time only | Fetched with pinned SHA-256 by `tools/fetchdeps`; not shipped |
| `adirh3/DeXDesk` (protocol/Miracast insights) | MIT | Active (M1a) | Reference for WFD Sink / RTSP / RTP-TS / UIBC & MF DXVA decoder |
| `Aureliano021/ScrcpyDex` (loopback DeX activator) | Apache-2.0 | Active (M1a) | Reference for clean-room `SemWifiDisplayConfig` loopback DeX helper |
| `maze-mei/DX-Manager` | MIT | Reference / Active | Multi-session lifecycle & initial DPI ≥ 160 bootstrap reference |
| `AndroMeld` (`catchingnow`) | Proprietary | UX inspiration only | Strictly forbidden to copy, decompile, or redistribute code/APK/assets |

## Trademarks

"Samsung", "Samsung DeX", "Galaxy", and "One UI" are trademarks of Samsung Electronics Co., Ltd.
"Android" is a trademark of Google LLC.
"Windows" is a trademark of Microsoft Corporation.
AndroFuse is an independent community project with no affiliation or endorsement.
