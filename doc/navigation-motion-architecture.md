# KernelSU 界面与主题移植

## 中文

本应用的外观基于 KernelSU 管理器提交 `6e3860443f8fea7c5fc226277e53888033796488`。
Material 与 Miuix 使用同一条 Miuix 导航栈。导航栏属于主页面，详情页推入时主页面和导航栏一起退场。

### 上游源码与业务适配

| 本应用入口 | KernelSU 来源 | 适配内容 |
|---|---|---|
| `AppRoot`、`AppNavigationScaffold` | `MainActivity`、`BottomBarMaterial`、`BottomBarMiuix`、两种 `NavigationRail` | 四个业务目的地、消息宿主、页面可见性 |
| `ui/ksu/material/` | `ui/component/material/` | 包名与 Compose opt-in |
| `theme/ksu/` | `ui/theme/`、`UiMode` | DataStore 设置、Android 28–30 种子色回落；省去 WebUI CSS 发布 |
| `AppearanceScreen` | 两种 `ColorPaletteScreen`、`ColorPaletteUiState`、`ScaleDialog` | 存储与动作回调；移除仅适用于 KSU 模块的描述行数选项 |
| `AboutSettingsPage` | 两种 `AboutScreen`、Miuix 背景效果 | 应用标识、版本、项目及许可证链接 |
| `AppPageScaffold`、`AppSettingsGroup` | `SettingsMaterial`、`SettingsMiuix` 的页面与分组容器 | 业务标题、滚动内容与返回动作 |
| 表单与控制面板 | KSU 使用的 Miuix preference/basic 与 Material expressive 组件 | 保留参数校验、保存状态、启动及停止动作 |
| 日志、OCR、保活、虚拟屏、统计等 | 共用上述主题组件与页面容器 | 保留 KSU 没有的业务数据、图表、画面和服务操作 |

逐文件来源和原始 SHA-256 记录在 `ui/ksu/UPSTREAM.json`。KSU 代码保留 GPL-3.0 协议及来源说明。
不要重新发明导航动画、主题组件或主题预览；上游已有实现时应优先移植并更新来源记录。

### 框架版本

- Material 3 `1.5.0-alpha28`，MaterialKolor `5.0.1`。
- Miuix `0.9.4`，提交 `39c40f99844227b853f0049a0933b1f3ae6c00ba`。
- `navigation-runtime` 导入上游导航、分页手势与测试。
- `miuix-runtime` 导入 core、UI、preference、blur、shader 及需要的图标。
- 原发布包的 JVM 21 与本项目 JVM 17 不兼容，因此从固定版本源码构建。平台声明只进行 Android expect/actual 合并。

重导入命令：

```powershell
uv run python app/scripts/vendor_miuix_nav.py <miuix-checkout>
uv run python app/scripts/vendor_miuix_ui.py <miuix-checkout>
```

外观设置通过现有 `@PrefSchema` / DataStore 保存。包含界面风格、明暗、动态色、种子色、色板、色彩规范、模糊、悬浮底栏、分页手势、页面缩放及返回设置。
默认 Miuix、系统明暗，已有明暗设置作为迁移回落。主题变化使用 KSU 的颜色动画。
Android 31 以下没有系统动态色，Material 使用基线种子；Miuix 使用上游自身的兼容回落。

### 返回与状态

使用上游 NavDisplay 默认转场与系统圆角。开启侧滑返回时按 RTL 镜像方向，关闭时使用上游 `NavSwipeDirection.None`。
系统预测式返回由 `navigationevent-compose` 与 Miuix 处理；Android 14 起的开关桥接沿用 KSU ApplicationInfo 实现。
主页面在其他分页按返回先回到首页；首页返回交给系统。页面参数保留原字符串，不经过 URL 编解码。

构建、119 个框架测试及 4 个独立设备测试通过。设备测试覆盖悬浮底栏、主题切换保留详情与草稿、Unicode 参数、侧滑取消与提交。两套主题的文本、图标、表单、弹窗、下拉、消息及页面容器分别使用对应框架组件；主题提供者结构固定，切换不会重建导航。Android 33 以下跳过 RuntimeShader 高光，关于页使用可直接解码的应用 PNG。悬浮底栏按上游容器居中，虚拟屏方向由主导航管理，离开后交回系统策略。

Android 11 实机可以检查侧滑取消/提交与系统返回，无法验证 Android 13+ 系统预测式返回的进度事件。

## English

The UI is based on KernelSU manager revision `6e3860443f8fea7c5fc226277e53888033796488`.
Material and Miuix share the same Miuix navigation stack. Main navigation bars belong to the main entry and leave with it when opening details.

`ui/ksu/material/`, both appearance screens, both About screens, floating navigation, backdrop effects, and `theme/ksu/` are transplanted upstream sources.
Adapters supply AzurPilot identity, destinations, settings storage, service actions, and data. OCR, virtual display, logs, provisioning, and statistics retain their business behavior through the shared themed components.
Per-file upstream hashes and adaptations are recorded in `ui/ksu/UPSTREAM.json`; GPL-3.0 attribution is preserved.

Material 3 is pinned to `1.5.0-alpha28`, MaterialKolor to `5.0.1`, and Miuix to `0.9.4` at revision `39c40f99844227b853f0049a0933b1f3ae6c00ba`.
`navigation-runtime` and `miuix-runtime` compile the pinned sources for JVM 17, mechanically merging Android platform declarations.
The two vendor scripts above verify the checkout revision and record source hashes.

Appearance preferences use the existing generated Preferences DataStore schema. Native framework components handle animation and interaction; application-specific durations and peer-page fades have been removed.
Android versions below 31 use compatible seed-color fallbacks. System predictive back is provided by NavigationEvent and Miuix; the Android 14 preference bridge follows KernelSU.
A connected Android 11 device can verify swipe cancellation, completion, and ordinary system back, but cannot verify Android 13+ system predictive-back progress events.

The debug APK builds successfully. All 119 framework tests and four isolated device tests pass, covering floating navigation, theme changes preserving entries and drafts, Unicode route arguments, and swipe cancellation/completion. The component adapters select matching native text, icons, forms, dialogs, menus, snackbars, and page containers. Stable theme providers preserve navigation composition. RuntimeShader highlights are skipped below API 33; About uses a directly decodable app PNG. Floating navigation uses the upstream centered container, and root navigation owns virtual-screen orientation restoration.
