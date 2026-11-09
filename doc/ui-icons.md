# 界面图标 / UI icons

## 中文

### 双轨图标设计规范

应用采用“动态操作 + 静态导航与设置”的双轨图标体系：

1. **静态图标（导航、分类与列表行）**：
   - 统一采用官方 `androidx.compose.material.icons:material-icons-extended` 中的 `Icons.Outlined.*` 矢量图标。
   - 视觉尺寸固定为 24 dp，保持标准的单线轮廓（Outlined）与光学权重。
   - 常规状态使用主题色 `onSurfaceVariant`，激活/选中状态使用 `primary`。
   - 基础行组件（`AppNavigationRow`、`AppLabeledControlRow`、`AppDropdownPreference`、`AppSwitchPreference`、`AppCheckboxPreference`、`AppInfoRow`、`AppEditablePreference`）默认 `icon = null`，仅在具有明确分类或语义区分度时显式传入，避免普通配置行出现千篇一律或强制默认图标（如强行显示调节器、开关或编辑笔）。

2. **动态图标（操作按钮与交互反馈）**：
   - 操作按钮（`AppActionLabel`）、忙碌/加载指示器、下载/传输动效（`AnimatedTransferIcon`）以及具备显式状态切换的控件独占使用 `useAnimations` Lottie 动效。
   - `AppPreferenceIcon` 支持重载多态：传入 `AppSymbol` 时渲染 Lottie 动效，传入 `ImageVector` 时渲染静态矢量图标，同时适配 Material 3 与 Miuix 双主题色彩。

### 动效与资源

动态图标来自 [useAnimations](https://github.com/useAnimations/react-useanimations)，固定到提交 `88b845d9a4330b95bc7f456a17f32ce54157b32f`。JSON 资源存放于 `app/app/src/main/res/raw/icon_*.json`，MIT 许可保留在应用开源许可清单中。资源保持上游原样，通过固定绘制缩放抵消 32 单位画布的留白，使可见线条对齐 24 dp。

图标由现有 Lottie Compose 渲染，不添加网络请求或额外播放依赖。首次显示保持静态。按钮点击、设置值变化及导航选择使用资源自身的帧率和时长，系统动画缩放只应用一次。图标播放不阻塞业务操作。

只有明确的忙碌状态循环播放。页面离开前台或系统关闭动画时显示静态图形，停止播放。系统动画设置变化立即生效，快速切换状态从当前进度继续，剩余时长按进度距离计算。

下载使用 `download2` 的简洁箭头；刷新和立即更新使用 `refresh2` 的单圈更新箭头，避免复杂的下载进度演出或重复旋转。

## English

### Dual-Icon Architecture

The application adopts a dual-track icon system: "dynamic for actions, static for navigation and settings":

1. **Static Icons (Navigation, Categories, and Rows)**:
   - Uses standard `androidx.compose.material.icons:material-icons-extended` (`Icons.Outlined.*`).
   - Sized at standard 24 dp with uniform stroke weight and optical balance.
   - Tinted with `onSurfaceVariant` in resting states and `primary` when active/selected.
   - Base preference components (`AppNavigationRow`, `AppLabeledControlRow`, `AppDropdownPreference`, `AppSwitchPreference`, `AppCheckboxPreference`, `AppInfoRow`, `AppEditablePreference`) default to `icon = null`. Icons are supplied only when providing distinct semantic value, eliminating redundant or forced default icons (e.g., repeated sliders, switches, or edit pencils across general rows).

2. **Dynamic Icons (Action Buttons and Interactive Feedback)**:
   - Action buttons (`AppActionLabel`), busy/loading indicators, transfer animations (`AnimatedTransferIcon`), and stateful transitions exclusively utilize `useAnimations` Lottie assets.
   - `AppPreferenceIcon` polymorphically handles both `AppSymbol` (Lottie) and `ImageVector` (Material Icons), supporting both Material 3 and Miuix theme tokens seamlessly.

### Motion and Assets

Animated icons come from [useAnimations](https://github.com/useAnimations/react-useanimations), pinned to commit `88b845d9a4330b95bc7f456a17f32ce54157b32f`. JSON assets are bundled under `app/app/src/main/res/raw/icon_*.json`, with their MIT license cataloged in the open-source license list. Assets are preserved upstream; a fixed draw scale compensates for padding in the 32-unit canvas to align visible strokes with 24 dp.

Lottie Compose handles playback without extra network requests or external dependencies. Icons initially remain static. Button taps and transitions use native frame rates and durations, with system animation scales applied once. Playback never blocks business operations.

Only explicit busy states loop. Background pages and disabled system animations show static glyphs and stop playback. System animation changes apply immediately; rapid state changes continue from current progress, with remaining duration proportional to the remaining distance.

Download uses the simplified `download2` arrow; refresh and immediate updates use the single-turn `refresh2` loop.
