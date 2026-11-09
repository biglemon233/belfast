# Miuix navigation runtime

## 中文

本模块从 Miuix `v0.9.4` 构建导航框架，供 App 的 Material 3 界面使用。
上游提交为 `39c40f99844227b853f0049a0933b1f3ae6c00ba`。
KernelSU 在 Material 模式下也使用该版本的 Miuix 导航。

上游发布的导航包以 JVM 21 编译，其中内联 API 无法被本仓库 JVM 17 代码调用。
本模块使用现有 Android library 和 Compose convention 插件，以 JVM 17 编译同一份源码。
导航转场、手势仲裁、生命周期、状态保存和测试保持上游实现。
圆角模块仍通过 `miuix-squircle-android:0.9.4` 依赖引用。

Android 源码适配仅包括两处：将设备圆角的 `actual` 声明改为普通声明；
将窗口导航事件桥的 `expect` 声明与 Android `actual` 实现合并。
各原始文件的 SHA-256 记录在 [UPSTREAM.json](UPSTREAM.json)，版权头和
[Apache 2.0 许可证](LICENSE) 保留。

更新时先检出固定上游版本，再运行：

```powershell
uv run python app/scripts/vendor_miuix_nav.py C:/path/to/miuix
app/gradlew.bat -p app :navigation-runtime:testDebugUnitTest compileDebugKotlin -x verifyBundledAzurPilotRuntime
```

脚本拒绝非固定提交。升级需同时更新脚本的提交、依赖版本与本说明，并重新验证手势和状态恢复。
不要直接修改导入的动画实现；如需修复上游缺陷，应记录补丁或先向上游提交修复。

## English

This module builds the navigation framework from Miuix `v0.9.4` for the app's Material 3 UI.
The pinned revision is `39c40f99844227b853f0049a0933b1f3ae6c00ba`.
KernelSU also uses this Miuix navigation version in Material mode.

The published navigation artifact targets JVM 21, so its inline APIs cannot be called from
this repository's JVM 17 code. This module uses the existing Android library and Compose
convention plugins to build the same sources for JVM 17. Navigation transitions, gesture
arbitration, lifecycle, state saving, and tests retain the upstream implementation.
Corner clipping remains a dependency on `miuix-squircle-android:0.9.4`.

Only two Android source adaptations are applied: the corner-radius `actual` declaration
becomes an ordinary declaration; the window navigation bridge's `expect` declaration
is merged with its Android `actual` implementation. Original source hashes are recorded
in [UPSTREAM.json](UPSTREAM.json), with copyright headers and the
[Apache 2.0 license](LICENSE) preserved.

To update, check out the pinned upstream revision and run the commands above.
The importer rejects other revisions. An upgrade must update the pinned revision,
dependency versions, and this document, then revalidate gestures and state restoration.
Do not edit imported animation behavior directly; record necessary patches or submit
fixes upstream first.
