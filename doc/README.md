# AzurPilot for Android 技术文档 / Technical Documentation

> 本目录是面向维护者的技术文档，遵循 [Google 开发者文档风格指南](https://developers.google.com/style)。
> 每篇均为中英双语：先中文，后英文，两段内容互为镜像。
>
> This directory contains maintainer-facing technical documentation following the
> [Google Developer Documentation Style Guide](https://developers.google.com/style).
> Every document is bilingual: Chinese first, then English, with mirrored content.

## 文档索引 / Document index

| 文档 / Document | 内容 / Contents |
|---|---|
| [architecture.md](architecture.md) | 系统架构：App 壳层、Runtime、网关与更新通道 / System architecture: app shell, runtime, gateway, and update channels |
| [runtime-provisioning.md](runtime-provisioning.md) | Runtime 部署与更新：状态机、下载、校验、解压 / Runtime provisioning and updates: state machine, download, verification, extraction |
| [release-channel.md](release-channel.md) | 发布通道：`latest.json` 字段、镜像源、版本规则 / Release channel: `latest.json` fields, mirrors, version rules |
| [cnb-ci.md](cnb-ci.md) | GitHub 产物分发到 CNB Releases / Mirroring GitHub artifacts to CNB Releases |
| [multi-arch.md](multi-arch.md) | 多架构支持：arm64 与 x86_64、CI 矩阵、限制 / Multi-architecture support: arm64 and x86_64, CI matrix, limitations |
| [adb-e2e-testing.md](adb-e2e-testing.md) | ADB 全流程测试手册 / ADB end-to-end testing guide |
| [comment-style.md](comment-style.md) | 注释规范：Google 风格基准 + 双语 KDoc 惯例 / Comment conventions: Google style baseline + bilingual KDoc rules |
| [development-guide.md](development-guide.md) | 开发指南：环境、构建、调试、签名与发布 / Development guide: environment, build, debug, signing, and release |
| [build-profiles.md](build-profiles.md) | 构建 Profile：PI 打包配方、身份覆盖与验证 / Build profiles: PI packaging recipes, identity overrides, and verification |
| [module-reference.md](module-reference.md) | 模块参考：责任边界、关键入口与变更验证 / Module reference: ownership boundaries, key entry points, and change validation |
| [privileged-bridge-protocol.md](privileged-bridge-protocol.md) | 特权桥协议：回环 framing、端点、并发与变更规则 / Privileged bridge protocol: loopback framing, endpoints, concurrency, and change rules |

## 阅读约定 / Conventions

- 中文部分面向中文维护者，术语与代码注释保持一致；英文部分遵循 Google 开发者文档风格指南（句首大写标题、现在时、主动语态、步骤用编号列表）。
- Chinese sections target Chinese maintainers and keep terminology aligned with the code comments; English sections follow the Google Developer Documentation Style Guide (sentence-case headings, present tense, active voice, and numbered procedures).
- 代码引用 / Code references use `path:line` form against the repository root.
- 示例命令 / Example commands run on a host with `adb` unless stated otherwise.
- UI 文案引用（按钮、菜单）保留设备上的实际显示语言，中英文档一致；若你的设备语言不同，以对应译名为准。
- UI labels (buttons, menus) quote the on-device text as displayed, identically in both language halves; substitute your locale's label if the device language differs.
