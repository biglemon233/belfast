# 虚拟屏原生分辨率与启动聚焦 / Native resolution and virtual-display launch focus

## 中文

### 原生 720P 与 DPI

[Issue #25](https://github.com/wess09/AzurPilot-for-Android/issues/25) 报告 MagicOS
将游戏的 APS 比例 `0.84375` 带入虚拟屏，产生 `mGlobalScale=1.1851852` 和画面裁切。
修复保持虚拟屏与采集缓冲为 1280×720，在启动游戏前处理目标包的兼容配置：

- `app_prepare` 将已配置的 APS 静态及动态分辨率比例设为 1.0，关闭标准屏幕兼容模式，
  并验证回读结果。APS 的动态接口可能在写入成功后返回 -5（没有已注册的回调）；
  仅在回读为 1.0 时接受该结果。
- 只修改目标包，不调整全局低分辨率开关、帧率、纹理或亮度。目标包的 APS 静态修改由
  ROM 持久保存；每次启动重新检查，覆盖 ROM 后续恢复的缩放配置。
- APS 设备每次准备后强停旧游戏进程，防止物理屏上创建的 `CompatibilityInfo` 被复用。
  JNI 启动入口执行同一策略。
- ROM 权限拒绝、无法解析接口或回读不符合要求时，准备失败。AP 以
  `RequestHumanTakeover` 停止任务，避免同一裁切画面反复触发游戏重启。
- 旧桥没有 `nativeGameResolution` 能力时保留旧启动流程。

虚拟屏 DPI 默认在每次建屏时读取主屏的当前逻辑 DPI。用户可在“设置 → 虚拟屏”
保存 72–960 的自定义 DPI，输入 0 或选择“跟随设备”恢复默认，下次启动虚拟屏生效。
读取设备 DPI 失败时回落 160。修改 DPI 不改变 1280×720 的截图和触摸坐标约定。

APS 接口是厂商接口，参考其 [分辨率配置实现](https://github.com/dstmath/HWFramework/blob/master/P40_HarmonyOS_2.0.0_Developer_Beta1/src/main/java/com/android/server/aps/HwApsManagerServiceConfig.java)。
运行时按服务实际 AIDL 描述符查找接口，不写死华为或荣耀的类名、Binder transaction 编号。
APS 模拟回归覆盖 issue 中的比例、权限拒绝和回读失败；Magic7 Pro / API 37 的实际权限与
窗口缩放效果仍需机型复测，不能用无 APS 设备的成功结果代替。

### 问题与证据

2026-10-13 在 M2104K10AC（Android 11 / API 30）上复现：AP 用
`am start --display` 启动碧蓝航线后，Activity 已经 `RESUMED`，但副屏的
`mCurrentFocus` 和 `mFocusedApp` 都为空。采集器仍收到帧，画面却固定为
RGB `(32, 31, 35)`，因此帧计数前进不能证明游戏已经完成启动。

向副屏发送 `ACTION_DOWN` 后立即发送 `ACTION_CANCEL`，窗口获得焦点，Unity
开始初始化并出现登录画面。无需打开最近任务列表，也不发送会完成点击的 `ACTION_UP`。
此设备上仅调用 `IActivityTaskManager.setFocusedTask` 没有恢复焦点。

[Android 的每屏焦点说明](https://source.android.com/docs/core/display/multi_display/displays#per-display-focus)
说明窗口焦点由设备的每屏焦点策略决定，Activity 恢复状态不能替代窗口焦点。

### 启动契约

- `android_app_launch.py` 保留 AP 的原始启动参数、返回值和 `wait` 行为。
  启动成功后用 `ping.capabilities.appFocus` 协商，再调用 `app_focus`。
- 请求包含 `package` 和最新 `displayId`。桥拒绝主屏、已经失效的虚拟屏和不匹配的
  顶层包；窗口尚未就绪时在 3 秒重试预算内等待。
- 聚焦序列与设备操作、显示生命周期和手势分别串行化。有进行中的触摸手势时不抢占。
  按下失败仍发送取消，两个事件均成功注入才返回成功。
- 旧桥、聚焦失败或桥连接中断只记录警告，保留原始启动结果。
  原始启动抛出的异常仍向上传播。
- JNI 的 `DriverClass.startApp` 同样在落屏校验后请求焦点。
- APK 在 Runtime 启动前同步适配器；上游源码热更不会移除此修复。

## English

### Native 720p and DPI

[Issue #25](https://github.com/wess09/AzurPilot-for-Android/issues/25) reports that MagicOS
carries the game's APS ratio of `0.84375` onto the virtual display, resulting in
`mGlobalScale=1.1851852` and a cropped frame. The fix keeps the display and capture buffer at
1280×720 and prepares package compatibility settings before launch:

- `app_prepare` sets configured persistent and dynamic APS resolution ratios to 1.0, disables
  standard screen compatibility mode, and verifies readback. Dynamic APS may return -5 after
  a successful write when no callback is registered; this is accepted only with a readback of 1.0.
- Only the target package is changed. Global low-resolution settings, frame rate, texture, and
  brightness are untouched. The ROM persists the package's static APS setting; each launch checks
  again in case the ROM has restored its scaling configuration.
- Preparation on APS devices always force-stops the old game process to avoid reusing
  `CompatibilityInfo` created on the primary display. JNI launches use the same policy.
- Permission rejection, an unresolvable interface, or unexpected readback fails preparation.
  AP stops the task with `RequestHumanTakeover`, avoiding repeated restarts on the same cropped frame.
- Older bridges without the `nativeGameResolution` capability retain their original launch flow.

Each display creation uses the primary display's current logical DPI by default. In
Settings > Virtual display, users can save a custom value of 72–960, enter 0, or select
Follow device to restore the default. Changes apply on the next virtual-display start.
An unavailable device DPI falls back to 160. DPI does not change the 1280×720 capture or touch space.

APS is a vendor interface; see its [resolution configuration implementation](https://github.com/dstmath/HWFramework/blob/master/P40_HarmonyOS_2.0.0_Developer_Beta1/src/main/java/com/android/server/aps/HwApsManagerServiceConfig.java).
Runtime lookup uses the service's actual AIDL descriptor rather than hard-coded Huawei or Honor
class names or Binder transaction numbers. APS fixtures cover the reported ratio, permission
denial, and failed readback. Actual permissions and window scaling on Magic7 Pro / API 37 still
require device verification; success on a device without APS is not a substitute.

### Problem and evidence

On 2026-10-13, M2104K10AC running Android 11 / API 30 reproduced the failure:
after AP launched Azur Lane with `am start --display`, its Activity was `RESUMED`,
but the secondary display's `mCurrentFocus` and `mFocusedApp` were null. Capture
still received frames fixed at RGB `(32, 31, 35)`, so an advancing frame counter
does not establish that the game has completed startup.

Sending `ACTION_DOWN` immediately followed by `ACTION_CANCEL` to the secondary
display established window focus, started Unity initialization, and produced the
login screen. Opening Recents was unnecessary, and no `ACTION_UP` was sent to
complete a click. Calling `IActivityTaskManager.setFocusedTask` alone did not
restore focus on this device.

[Android's per-display focus documentation](https://source.android.com/docs/core/display/multi_display/displays#per-display-focus)
describes the device policy controlling window focus. Activity resume state does
not substitute for window focus.

### Launch contract

- `android_app_launch.py` preserves AP's original launch arguments, return value,
  and `wait` behavior. After successful launch, it negotiates
  `ping.capabilities.appFocus` before calling `app_focus`.
- Requests include `package` and the latest `displayId`. The bridge rejects the
  primary display, stale virtual displays, and mismatched top packages. Windows
  that are not ready are retried within a three-second budget.
- The sequence serializes with device operations, display lifecycle, and touch
  gestures. It does not interrupt an active gesture. Cancel is sent even when
  down fails; both events must be accepted for a successful response.
- Older bridges, focus failure, and disconnects only warn and preserve the
  original launch result. Exceptions from the original launch still propagate.
- JNI's `DriverClass.startApp` also requests focus after verifying placement.
- The APK syncs the adapter before Runtime startup, preserving the fix across
  upstream source updates.
