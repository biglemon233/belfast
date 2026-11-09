# Android 模块说明

`app/` 是 Android 宿主 Gradle 工程根目录。产品概览见仓库根 [README.md](../README.md)；维护者应从 [开发指南](../doc/development-guide.md)、[模块参考](../doc/module-reference.md) 与 [文档索引](../doc/README.md) 开始。会话交接记录位于 [`handoff/`](../handoff/)。

## 责任范围

本模块包含 Kotlin/Compose 宿主、特权进程接口、native bridge、PRoot 库打包与构建逻辑。应用支持 Material 3 与 Miuix 两种界面风格，实际选择由 `UiStyle` 设置决定；不要将任一风格视为唯一 UI 实现。

构建与验证命令、Runtime 产物前置条件、签名规则以及本地 Windows 注意事项以 [开发指南](../doc/development-guide.md) 为准。PI 打包配置见 [构建 Profile](../doc/build-profiles.md)，特权回环桥的 framing 和端点契约见 [特权桥协议](../doc/privileged-bridge-protocol.md)。

## Module overview

`app/` is the Gradle root for the Android host project. Read the repository [README.md](../README.md) for the product overview; maintainers should begin with the [development guide](../doc/development-guide.md), [module reference](../doc/module-reference.md), and [documentation index](../doc/README.md). Session handoff notes live in [`handoff/`](../handoff/).

## Ownership

This module contains the Kotlin/Compose host, privileged-process interfaces, native bridge, PRoot-library packaging, and build logic. The app supports both Material 3 and Miuix UI styles; the `UiStyle` setting selects the active style, so neither style is the sole UI implementation.

Use the [development guide](../doc/development-guide.md) for build and validation commands, Runtime-artifact prerequisites, signing rules, and Windows-local notes. See [build profiles](../doc/build-profiles.md) for PI packaging configuration and the [privileged bridge protocol](../doc/privileged-bridge-protocol.md) for loopback framing and endpoint contracts.
