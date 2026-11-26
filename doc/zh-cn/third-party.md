# 第三方声明与许可证

AndroFuse 基于 **GNU General Public License v3.0 only**（`GPL-3.0-only`，见 [LICENSE](../../LICENSE)）开源。

## 许可证兼容性矩阵

| 组件 | 许可证 | 状态 | 说明 |
|---|---|---|---|
| `github.com/egoist/mygo`（`v0.10.0`） | MIT | 已启用（M0） | 与 `GPL-3.0-only` 兼容 |
| `github.com/ebitengine/purego` | Apache-2.0 | 已启用（M0） | 与 `GPL-3.0-only` 兼容 |
| `golang.org/x/sys` | BSD-3-Clause | 已启用（M0） | 与 `GPL-3.0-only` 兼容 |
| Rust `serde`、`serde_json`、`windows-sys`、`windows` | MIT OR Apache-2.0 | 已启用（M1a） | 通过 `cargo deny check licenses bans sources` 校验 |
| `Genymobile/scrcpy`（`scrcpy-server` v5.0.1） | Apache-2.0 | 已启用（M1a） | 由 `tools/fetchdeps` 校验 SHA-256 下载；已集成其线协议 |
| Android Platform Tools（`adb`） | Apache-2.0 | 已启用（M1a） | 由 `internal/adb` 调用；打包分发时附带上游许可声明 |
| `EasyTier/EasyTier` v2.7.1（`easytier-ffi`、`easytier`、`easytier-core`、`easytier-proto` 及 EasyTier 组织分支） | LGPL-3.0 | 已启用 | 静态链接进 `androfuse_core`（与 `GPL-3.0-only` 兼容）；固定修订 `0d9c52b`；`packet.dll` 延迟加载且不随包分发；其依赖树中的 RustSec 安全公告（hickory 0.25、仅构建期使用的 `zip`、无人维护的 crate）不在本仓库修补，而是随 EasyTier 更新跟进，逐条附原因列于 `core/deny.toml`，每次更换固定修订时重新核查 |
| EasyTier 传递依赖 Rust crate（`smoltcp`、`webpki-roots`、`libbz2-rs-sys`、`encoding-index-*`、`tun` 等） | 0BSD、CDLA-Permissive-2.0、bzip2-1.0.6、CC0-1.0、WTFPL | 已启用 | 均与 GPL-3.0 兼容；已在 `core/deny.toml` 中显式允许 |
| `protocolbuffers/protobuf`（`protoc` 36.2） | BSD-3-Clause | 仅构建期 | 由 `tools/fetchdeps` 校验 SHA-256 下载；不随包分发 |
| `adirh3/DeXDesk`（Miracast 协议与实现参考） | MIT | 已启用（M1a） | 参考 WFD Sink / RTSP / RTP-TS / UIBC 与 MF DXVA 解码 |
| `Aureliano021/ScrcpyDex`（回环 DeX 激活） | Apache-2.0 | 已启用（M1a） | 参考并采用洁净室重写的 `SemWifiDisplayConfig` 回环辅助器 |
| `maze-mei/DX-Manager` | MIT | 参考 / 已启用 | 参考多会话管理与初始 DPI ≥ 160 引导规则 |
| `AndroMeld`（`catchingnow`） | 专有商业软件 | 仅作交互参考 | 严禁复制、反编译或重新分发其源码、APK 或任何资源 |

## 商标声明

"Samsung"、"Samsung DeX"、"Galaxy" 与 "One UI" 是三星电子株式会社的商标；"Android" 是 Google LLC 的商标；"Windows" 是微软公司的商标。AndroFuse 为独立开源项目，与上述公司无关联或背书关系。
