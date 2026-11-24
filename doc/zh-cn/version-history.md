# 版本历史

## 未发布（`0.1.0` M0 架构基线）

- 基于 `GPL-3.0-only` 初始化统一的 Go + MyGo（`v0.10.0`）+ Rust（`core/androfuse-core`，ABI v1）工程骨架。
- 接入 `resources/icon.svg` 并实现确定性图标渲染工具（`tools/icon`），生成桌面端、界面、托盘与 Android 自适应图标前景层。
- 实现被动主机环境自检（`diagnostics.self_check`）：Windows 原生架构与 WOW64 转译识别、Media Foundation H.264/HEVC 解码器枚举、Wi-Fi Direct / Miracast 符号探测与 ADB 路径检测。
- 完成 MyGo、Kyokai、scrcpy、DeXDesk、ScrcpyDeX、DX-Manager、MyDeX、DeXter 与 AndroMeld 交互参考的源码、许可证及兼容性调研，并将完整计划写入 `PLAN.md`。
- 已核对上游版本：Go `1.27.4` / MyGo `v0.10.0` / Rust `1.99.1` / scrcpy `v5.0.1` / Kyokai `v0.8.1`（核对日期 `2026-11-23`）。
