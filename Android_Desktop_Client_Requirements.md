# 开源 Windows Android 桌面集成客户端：开发需求

## 项目目标

开发一款开源、现代化的 Windows Android 桌面集成客户端，以停更的 Samsung DeX for PC 的替代与增强为核心，兼容三星 DeX 设备及普通 Android 设备。前端使用 **MyGo**，正式支持 **Windows 11 x64 / ARM64**；Windows 10 兼容性可评估。优先实现稳定、低延迟、易用的本地连接，无需 Root。

## 必须支持的三种工作模式

1. **Samsung DeX 桌面**：在 Windows 原生窗口中使用手机内置的真正 Samsung DeX。支持无线 Miracast，以及通过 USB/ADB、Wi-Fi ADB 启动 DeX；根据设备和 One UI 版本自动选择可用方案，允许手动切换和回退。特别验证 DeX 多窗口、任务栏、最近任务、鼠标行为等是否完整，不能把普通 Android 虚拟桌面冒充完整 DeX。
2. **Android 手机镜像**：镜像并控制手机主屏幕，支持 USB/ADB 和 Wi-Fi ADB、键鼠、音频、剪贴板、横竖屏、窗口/全屏、截图及录屏；可以选择保持手机正常亮屏或熄屏投屏。
3. **独立 APP 窗口**：每个 Android APP 在 Windows 中占据独立的原生窗口（任务栏、Alt+Tab、窗口缩放、Snap Layouts），通过 Android 虚拟显示运行，原则上不占用手机主屏幕，手机可以继续操作其他应用。支持多窗口并发、独立输入与关闭恢复；明确标注不兼容的应用、设备和 Android 版本。

三种模式在技术可行时允许并存，不能由应用架构人为规定互斥。对实际不兼容的组合实行能力检测、用户提示与安全回退。

## 连接与系统架构

- 实现 **Miracast / Wi-Fi Direct、Wi-Fi ADB、USB/ADB** 三种连接后端，统一设备发现、配对、连接状态、会话管理、自动重连及切换。Miracast DeX 不应强制开启 USB 调试。
- 采用模块化结构，分离 **MyGo UI、设备/会话管理、传输层、DeX 引擎、镜像引擎、虚拟 APP 引擎、原生视频渲染、Android Companion**；允许后续独立升级或替换后端。
- 优先复用成熟的 Rust / scrcpy / ADB 核心能力，评估 MyGo 的 Rust Native Modules、FFI 或进程 IPC。视频使用 Windows 原生硬件解码/渲染路径，避免不必要的视频帧 JS 转发。
- 对同一设备集中管理 ADB 服务、端口转发、视频与音频流、虚拟显示、输入焦点及资源回收，防止多窗口相互干扰或退出后残留系统状态。
- 同时维护 **x64 与原生 ARM64** 构建、依赖与发布流程；不得仅以 x64 模拟运行宣称 ARM64 支持。针对 Wi-Fi Direct 驱动、ADB USB 驱动、硬件解码等提供自检与错误诊断。

## Android Companion APK

开发本项目自己的可选开源 Companion APK，参考 AndroMeld / DX-Manager 的体验。基础 Miracast 和基础 ADB 镜像不依赖 APK；需要增强能力时提示安装并取得明确授权。

- **优先**：APP 信息和图标、通知同步与回复、双向剪贴板、文件浏览/传输、拖放、设备状态、APP 接力、虚拟显示异常恢复。
- **后续**：Android Widget、手机摄像头作为 Windows 摄像头、PC 摄像头/麦克风传入 Android、可授权的 MCP 控制接口。
- 优先使用公开且可维护的 Android API；如需 ADB shell、Shizuku 或其他增强权限，采用分级授权、可关闭的功能开关，不要求 Root，不依赖绕过 Android 安全机制。

## 体验与工程要求

- 现代、简洁的 MyGo 桌面 UI；设备主页、三模式入口、APP 启动器、连接状态、设置、日志、系统托盘及自动更新。支持中英文、深浅色主题、高 DPI、多显示器。
- 提供分辨率、DPI、帧率、码率、编解码器、输入和音频设置；**明确显示实际协商的分辨率与实测 FPS**，不能把预设目标当成性能保证。
- 优先使用加密无线 ADB 配对；对旧式明文 `adb tcpip` 明确警告、默认禁用或要求单独确认。配对信息和日志本地保存、敏感信息脱敏；远程访问与 MCP 控制默认关闭。
- 明确处理 DRM/受保护画面、部分 APP 不支持辅助显示、多实例限制，以及不同 One UI 版本上的 DeX 差异；不得通过削弱系统安全性实现兼容。
- 项目开源，保留第三方许可与署名；复用 GPL 代码前先确定整体许可证方案。AndroMeld 的商业 APK/源码不能在无明确许可的情况下直接复制或重新分发。

## 开发顺序与验收

1. **先调研并出架构方案**：检查参考项目的实际代码、许可证、活跃版本和已知限制；建立「功能 × 连接方式 × Android/One UI × Windows 架构」兼容性矩阵，明确验证与未知项。
2. **首个可用版本**：MyGo 设备管理 + USB/Wi-Fi ADB 镜像 + 至少两个独立 APP 原生窗口；验证手机主屏幕可以继续正常使用。
3. **DeX 双通道**：接入 Miracast DeX 与 ADB DeX，优先实现完整原生 DeX；提供虚拟显示回退，并显示当前运行方案。
4. **增强与发布**：Companion APK、文件/剪贴板/通知、自动重连、稳定性优化、x64/ARM64 安装包及自动化测试。

关键验收包括：两种 Windows 架构的**实际运行验证**、至少一台支持 DeX 的三星手机与一台普通 Android 手机、断线与异常退出后恢复、多个 APP 并发、输入隔离、性能和资源占用测试。无法验证的功能不得标记为已支持。

## 重点参考项目

- MyGo：https://github.com/egoist/mygo
- DeXDesk（Rust Miracast）：https://github.com/adirh3/DeXDesk
- MyDeX（ADB、DeX、独立窗口）：https://github.com/Keremerk/MyDeX
- ScrcpyDeX（ADB 激活原生 DeX）：https://github.com/Aureliano021/ScrcpyDex
- scrcpy（镜像、输入、虚拟显示）：https://github.com/Genymobile/scrcpy
- DX-Manager（多窗口和 Companion）：https://github.com/maze-mei/DX-Manager
- DeXter（备用研究，GPL-3.0）：https://github.com/fabriciolima77/DeXter
- AndroMeld（交互和功能参考）：https://andromeld.catchingnow.com/

**交给 Coding Agent 的要求**：以上定义产品目标、功能边界和验收标准，而非指定逐文件实现方式。请先独立审查与验证，给出合理的技术架构、里程碑和风险处理方案，再分阶段编码；优先可维护性与真实兼容性，不要为了完成清单而使用假实现。
