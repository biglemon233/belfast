# ALAS-AOS 📱⚓

> 免 root，把自动化脚本 [ALAS](https://github.com/LmeSzinc/AzurLaneAutoScript) 装进你的 Android 手机——游戏跑在**后台虚拟屏**里挂机，前台刷视频、回消息、打游戏，互不干扰。🎉
>
> **修改自**：[Shinarin/ALAS-AOS](https://github.com/Shinarin/ALAS-AOS)——本仓库是它的延续开发版本，在其基础上持续演进。
>
> **名字由来**：`AOS` = **ALAS on Android OS**——ALAS 的 Android 系统版。
>
>⚠️ **兼容性说明：弹窗字体差异**
>各家手机的渲染方案不一致：游戏内渲染实际使用的是**手机系统提供的字体底层**，因此游戏部分弹窗的字体在不同机型上会有变动——可能影响识别精度导致报错。

[![License](https://img.shields.io/badge/license-AGPL--3.0-blue.svg)](./LICENSE)
[![Release](https://img.shields.io/github/v/release/Dreamry2C/ALAS-AOS)](https://github.com/Dreamry2C/ALAS-AOS/releases)

## ✨ 特性

-  **免 root**：提权只靠 [Shizuku](https://github.com/RikkaApps/Shizuku)，不解 BL、不刷机、不折腾。
-  **后台挂机**：游戏运行在独立的后台虚拟屏（1280×720），主屏该干嘛干嘛。
-  **两种安装方式**：完整版内置 Ubuntu 运行环境（Python 3.12 + ALAS 官方版 + OCR 模型）；通用 Lite 安装包更小，首次启动下载完整运行环境。
-  **开屏热更新**：每次启动自动检查 ALAS 上游更新，秒级快进；断网/超时自动跳过，绝不卡启动。
-  **本机直控**：截图与触控走手机本机的特权桥，不用电脑、不插线、不开 adb。
-  **悬浮窗面板**：App 不在前台也能随时启停挂机、盯实时日志。
-  **全屏手动模式**：想自己点两下？预览画面一键全屏，手指直接操作游戏，退出后画面无缝回卡。
-  **报错提醒**：ALAS 报错可同时显示手机系统通知，设置中默认开启；OnePush 远程推送继续独立使用。
-  **内置小工具**：半自动点击、活动剧情等 ALAS 工具任务可独立运行，与挂机自动互斥，不抢设备。

## 🧭 工作原理（一图流）

```
┌─ App 进程（Kotlin）────────────────────────────┐
│  主页（虚拟屏实时画面 + 运行配置 + 控制面板）     │
│  ALAS 控制台（WebUI :22267，内嵌 WebView）        │
│  主页/悬浮窗面板 ──HTTP──> wrapper(:22400)      │
├─ 特权进程（Shizuku 拉起）───────────────────────┤
│  后台虚拟屏 + 截屏/触控注入                       │
│  桥服务 :22300（ping/screencap/click/swipe/shell）│
├─ proot Ubuntu 环境（App 私有目录，免 root）──────┤
│  wrapper.py 监管 → gui.py（WebUI）               │
│                  → ALAS 原生任务进程             │
│  ALAS ──桥──> 虚拟屏里的游戏                      │
└─────────────────────────────────────────────────┘
```

## 📦 安装

### 前置条件

-  一台 **Android 9 及以上的 64 位 ARM 或 x86_64 安卓设备**（root 与否皆可）
-  已安装并激活 **Shizuku**（如有root可跳到下面第2步）

### 第 1 步：安装并启动 Shizuku

Shizuku 是开源的特权桥接工具，让普通 App 也能使用系统级能力（本项目用它创建后台虚拟屏、截屏和模拟触控），**全程无需 root**。

1. 前往官方仓库下载安装 👉 [RikkaApps/Shizuku](https://github.com/RikkaApps/Shizuku)。
2. 按 [Shizuku 官方使用指南](https://shizuku.rikka.app/zh-hans/guide/setup/) 激活，三种方式任选：
   -  **Android 11 及以上（推荐）**：开启「无线调试」配对激活，全程在手机上完成，不用电脑；
   -  **Android 10 及以下**：连接电脑执行一条 adb 命令激活；
   -  **已 root 设备**：打开 Shizuku 直接点「启动」即可。

> 💡 **嫌每次启动 Shizuku 都要连 WiFi 走无线调试？** 可以试试社区 fork 👉 [Shinarin/shizuku-m](https://github.com/Shinarin/shizuku-m)：在官方版基础上加了端口重新挂载，重启 Shizuku 不必再走无线调试，**没连 WLAN 也能启动**（手机重启后的第一次激活仍需按官方方式完成一次）。

### 第 2 步：安装 ALAS-AOS

1. 到 [Release 页](https://github.com/Dreamry2C/ALAS-AOS/releases) 下载：大多数手机选择 `arm64-v8a-full.apk` 完整版，x86_64 安卓设备或模拟器选择 `x86_64-full.apk` 完整版；也可选择支持两种架构的 `universal-lite.apk`，首次启动在线下载运行环境。
2. 安装并打开，阅读低精度识别模式的风险提示；确认和取消都需等待 5 秒，之后可在「设置 → 其他设置」中随时开关。按引导完成 **Shizuku 授权**，如有授权 root 则可直接使用 Root 模式。
3. 耐心等待首启部署（解压运行环境 + 热更新检查），完成后自动进入主页。🎊

## 🚀 使用

### 先认识三个界面

| 界面 | 在哪里 | 干什么用 |
|------|--------|----------|
|  **主页** | App 内主页 | 看虚拟屏实时画面、选运行配置、开始/停止挂机、看日志、跑小工具 |
|  **悬浮窗** | 任意界面之上 | 主页的随身迷你版：状态、启停、日志板——App 切后台后全靠它 |
|  **ALAS 控制台** | App 内 WebView | ALAS 官方 WebUI：任务配置、计划任务、截图回看……所有详细设置都在这 |


### 第一次使用

1. 打开 **ALAS 控制台**，按 ALAS 官方用法配置你的任务（出击、委托、科研……配置方法见 [ALAS 官方 Wiki](https://github.com/LmeSzinc/AzurLaneAutoScript/wiki)）。
2. 回到 **主页**，在「运行配置」下拉框选中你刚配好的实例。
3. 点击 **▶️ 开始挂机**——App 会自动在后台虚拟屏拉起游戏、处理登录弹窗，然后开始执行调度。🎉
4. 此刻起可以把 App 切到后台，用悬浮窗盯状态就行。

### 日常操作

-  **开始 / ⏹ 停止挂机**：主页或悬浮窗一键搞定。停止后环境保留，下次启动秒级恢复。
-  **实时日志板**：主页与悬浮窗都有，调度器在干什么一目了然。
-  **全屏手动模式**：点击预览画面进入全屏（自动横屏、隐藏系统栏），单指点击、拖拽直接操作游戏。半透明返回圆圈可直接拖动，靠边显示半圈；点半圈展开，再点返回 AOS，3 秒无操作自动收回。放在画面中间时保持原位。
-  **报错通知**：设置中的「ALAS 报错通知」默认开启，需要允许系统通知权限；关闭仅影响手机通知，不影响 ALAS 内的 OnePush 远程推送。
-  **小工具**：「半自动点击」「活动剧情」可独立开启（主页工具区与悬浮窗同款按钮）。**工具与挂机自动互斥**：开工具会自动停挂机，开始挂机也会自动停工具，永不抢设备。
-  **热更新**：每次打开 App 自动检查 ALAS 上游更新；断网/超时自动降级跳过，不阻塞启动。

### ⚠️重要提醒

- 🔁 **手机重启之后**：需要按官方方式重新启动 Shizuku（已 root：App 内点「启动」；未 root：重新走一次无线调试或电脑激活），再打开 ALAS-AOS 即可自动恢复环境。这是免 root 方案物理上不可省略的一步，请知悉。🙏


## ❓ 常见问题

**Q：需要电脑吗？**
A：不需要。安装、激活（Android 11+）、更新、挂机，全在手机上完成。

**Q：挂机费电吗？**
A：挂机毕竟是持续负载，长时间挂机建议插着电。

**Q：会被游戏检测吗？**
A：本项目不修改游戏本体，截图与触控均为系统级能力。但使用自动化脚本违反游戏用户协议，相关风险请自行评估、自行承担。（用别怕，怕别用）⚠️

**Q：支持渠道服 / 外服吗？**
A：支持国服，由于不同设备字体问题，外服欢迎实测后反馈。

**Q：断网能用吗？**
A：热更新检查会自动跳过；挂机本身不依赖外网（游戏自己的联网需求除外）。

**Q：卸载 App 会怎么样？**
A：App 私有目录里的运行环境和 ALAS 配置会一并清空，卸载前请留意。

## 🛠 构建与开发

想自己编译或参与开发？App 源码在 [`app/`](app/)，rootfs 烘焙与校验脚本在 [`rootfs/`](rootfs/)，CI 一键出包（`.github/workflows/`）。

## 🙏 致谢

ALAS-AOS 站在这些优秀开源项目的肩膀上，由衷感谢：💖

- [Shinarin/ALAS-AOS](https://github.com/Shinarin/ALAS-AOS)——本项目修改自它，后台虚拟屏挂机的整体方案从这里来 
- [LmeSzinc/AzurLaneAutoScript](https://github.com/LmeSzinc/AzurLaneAutoScript)（ALAS）——自动化的核心大脑 
- [Aliothmoon/MaaFwApp](https://github.com/Aliothmoon/MaaFwApp)——本 App 的上游基座（虚拟屏 + Shizuku 特权进程框架）
- [RikkaApps/Shizuku](https://github.com/RikkaApps/Shizuku)——优雅强大的免 root 特权桥 
- [termux/proot](https://github.com/termux/proot)——免 root 运行 Linux 用户空间的魔法 
- [breezedeus/cnocr](https://github.com/breezedeus/cnocr) 与 [Apache MXNet](https://mxnet.apache.org/)——游戏内文字识别引擎（原版 OCR 链）
- [Ubuntu](https://ubuntu.com/)（Canonical）——挂机运行环境的 Linux 地基

## 📄 许可

[AGPL-3.0](./LICENSE)。本仓库包含对 ALAS（GPL-3.0）的补丁与 MaaFwApp（AGPL-3.0）的 fork 修改，均按各自许可证义务公开源码。
