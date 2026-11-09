# 模块参考 / Module reference

> 本文为维护者提供按职责和进程边界组织的源代码地图。它说明从哪里开始追踪一次改动、
> 哪些约束不能跨越；具体协议与操作步骤请阅读链接的专题文档。
>
> This reference maps the source tree by responsibility and process boundary for maintainers. It
> shows where to begin tracing a change and which constraints must not be crossed; follow the linked
> focused documents for protocols and procedures.

## 中文

### 使用本参考

1. 先按要改变的用户行为定位下表中的责任模块，而不是从 UI 或全局单例直接绕过边界。
2. 再阅读该模块的主声明 KDoc；其中记录线程、生命周期、错误处理和平台兼容性约束。
3. 涉及 Runtime、特权服务、原生桥或发布时，同时执行表中列出的验证项。
4. 新能力应放在已有边界的实现侧，通过已有端口、仓库或状态流暴露；不要让 Compose、
   `ContentProvider`、JNI 或 `app_process` 直接互相依赖。

### 启动和依赖注入

| 职责 | 主要入口 | 维护约束 |
|---|---|---|
| 应用启动与进程分流 | `app/app/src/main/kotlin/com/azurpilot/ghio/AzurPilotApp.kt` | 仅主进程执行完整初始化；`:daemon` 进程只能启动它需要的最小组件。`postCreate` 在设置加载后按依赖顺序启动常驻服务。 |
| 窗口与 Compose 根节点 | `MainActivity.kt`、`ui/AppRoot.kt` | Activity 管理 splash、edge-to-edge 和保持亮屏；业务状态应来自 ViewModel、仓库或状态流，不应存放在 Activity。 |
| Koin 组合根 | `di/CoreModule.kt`、`di/HostModule.kt`、`di/LogModule.kt`、`di/OverlayModule.kt`、`di/PrivilegedModule.kt`、`di/ProvisionModule.kt`、`di/ProotModule.kt`、`di/ViewModelModule.kt` | 新依赖先定义接口或端口，再在所属模块中绑定。`AppCoroutineScope` 是进程级监督 scope；不能将 UI 生命周期工作放入其中。 |

### Runtime 与 PRoot

| 职责 | 主要入口 | 维护约束 |
|---|---|---|
| Runtime 首次部署与升级 | `provision/RootfsProvisioner.kt`、`provision/RuntimeArch.kt` | rootfs 是按 ABI 发布的不可变工件。部署、校验、解压和切换必须保持可恢复状态，详见 [runtime-provisioning.md](runtime-provisioning.md)。 |
| PRoot 会话生命周期 | `proot/ProotHost.kt` | App 持有子进程 stdin；关闭 stdin 是通知 Runtime 清理进程组的父进程死亡协议。不要把它替换为无父子关系的后台启动。 |
| 运行、配置与 Runtime API | `proot/AzurPilotRunController.kt`、`proot/AzurPilotGateway.kt`、`proot/AzurPilotRepository.kt`、`proot/AzurPilotConfigEditor.kt` | `AzurPilotGateway` 是 `/api/v1/ws` 的唯一订阅者，因为上游订阅是整体替换语义。UI 与业务层通过 repository 和状态流消费数据。 |
| 客户机覆盖层与种子 | `rootfs/overlays/`、`rootfs/seeds/`、`rootfs/build/` | 覆盖层只能处理 Android/PRoot 兼容性差异；用户配置不得被首启种子覆盖。构建需在目标 ABI 的原生环境执行，详见 [multi-arch.md](multi-arch.md)。 |

### 特权服务与原生桥

| 职责 | 主要入口 | 维护约束 |
|---|---|---|
| 后端选择、授权和 Binder 连接 | `privileged/PermissionManager.kt`、`privileged/RemoteAccessCoordinator.kt`、`privileged/RemoteServiceManager.kt` | Shizuku 与 root 连接器只能通过统一状态机暴露无 Binder 的状态投影。所有过期连接和死亡回调必须按 Binder 身份丢弃。 |
| root `app_process` 启动与回投 | `privileged/RootRemoteServiceConnector.kt`、`root/RootServiceStarter.kt`、`root/RootServiceBootstrapProvider.kt`、`root/RootServiceBootstrapRegistry.kt` | 回投仅接受 root/shell UID 和一次性 token。`RootUserService` 的忽略安全检查 Context 仅限特权子进程，不能扩散到普通 app 进程。 |
| 特权 IPC 实现 | `remote/RemoteServiceImpl.kt`、`remote/internal/` | 该进程拥有虚拟显示器、窗口/电源控制和桥接服务。app 侧通过 AIDL 端口调用，不能直接访问 hidden API。 |
| JNI 与帧/输入 ABI | `bridge/`、`app/app/src/main/native/CMakeLists.txt`、`app/app/src/main/native/bridge.h` | `libbridge.so` 运行在特权进程。`GetLockedPixels` 与 `UnlockPixels` 必须严格配对；锁定帧指针在解锁后失效。输入操作码和 JNI 注册名是跨语言 ABI，不能重编号或任意改名。 |
| root 启动器 | `app/app/src/main/native/launcher.c` | 启动器必须输出为 `liblauncher.so`，使 AGP 安装到 `nativeLibraryDir`；这是当前 targetSdk 下可靠的 `execve` 位置。 |

### 设置、界面、叠层和小组件

| 职责 | 主要入口 | 维护约束 |
|---|---|---|
| 应用设置 | `settings/AppSettings.kt`、`settings/AppSettingsManager.kt`、`settings/SettingsViewModel.kt` | `@PrefSchema` 字段按 camelToSnakeCase 生成 DataStore 键。迁移和原始键必须使用 snake_case，详见 `AGENTS.md`。 |
| 用户配置 | `config/UserConfigurationStore.kt`、`domain/UserConfiguration.kt` | 这套 JSON DataStore 与应用设置的 Preferences DataStore 独立；不要混用文件、键或序列化规则。 |
| Compose 设置与业务页面 | `ui/settings/`、`ui/azurpilot/`、`ui/logs/`、`ui/components/` | 页面状态由 ViewModel 或 repository 提供。新增文案需同步翻译，且中文 `values/` 是源文件。 |
| 悬浮控件与屏保 | `overlay/`、`overlay/screensaver/`、`overlay/border/` | 悬浮窗属于独立 Window，不能假定能读到 Activity 的 `Configuration` 或 ViewModelStore。通过 `AppThemeState` 和 `OverlayController` 传递必要状态。 |
| 桌面小组件 | `widget/`、`res/xml/widget_*` | 使用 Glance/RemoteViews 可用的资源。部分旧版厂商系统不可靠地解析 `?attr` 矢量着色与 `cornerRadius`，小组件需要显式 drawable 资源。 |

### 保活、日志与故障诊断

| 职责 | 主要入口 | 维护约束 |
|---|---|---|
| 会话前台服务 | `service/RunForegroundService.kt` | Runtime/虚拟显示器活跃时保住 app 进程；不能将它错误标记为与实际用途不符的 FGS 类型。 |
| 多路径保活 | `keepalive/KeepAliveManager.kt`、`keepalive/`、`AndroidManifest.xml` | 保活子系统必须可重复启停，并以设置开关为最终裁决。接收器和服务可在没有 DI 的路径上启动，因此使用受控的进程级管理器入口。 |
| App、Runtime 和特权日志 | `log/`、`constant/AppPaths.kt`、`privileged/ServiceBootLogger.kt` | App 日志写入器不得反向经 Timber 记录自身故障。Runtime stdout/stderr 进入 `proot/session.log`；特权启动链有独立时间线。 |
| 人工设备排查 | `tools/watch-android-logs.ps1`、[adb-e2e-testing.md](adb-e2e-testing.md) | PowerShell 工具默认跟踪 `com.azurpilot.ghio`。带 package 后缀的构建 profile 需要同步修改 `$package`。 |

### 更新、发布和构建基础设施

| 职责 | 主要入口 | 维护约束 |
|---|---|---|
| APK 与 Runtime 更新 | `update/`、`provision/`、`release-channel.md` | APK 与 Runtime 是独立通道。下载完成后必须核对大小与 SHA-256；安装确认永远交给系统安装器。 |
| Android 构建约定 | `app/build-logic/convention/`、`app/app/build.gradle.kts` | SDK、ABI、签名、R8 和 Runtime 验证集中在约定插件。修改版本方案时同步 `GitVersion.kt` 与 CI 的解析规则。 |
| 注解处理与 hidden API 编译桩 | `app/annotation-api/`、`app/ksp-processor/`、`app/hidden-api/` | `@PrefSchema` 生成访问器是设置层的一部分。hidden API 桩只提供编译时签名，运行时由框架提供。 |
| CI 和运行时产物 | `.github/workflows/rootfs.yml`、`.github/workflows/check-upstream.yml`、`app/scripts/fetch-proot-libs.sh` | CI 生成未提交的 rootfs 与 x86_64 PRoot 库。依赖包、哈希、ABI 和 ELF 重写计划必须一起更新；流程说明见 [development-guide.md](development-guide.md)。 |

### 变更后的最小验证

| 变更范围 | 最小验证 |
|---|---|
| Kotlin、Koin、Compose、AIDL 调用 | `cd app && ./gradlew compileDebugKotlin -x verifyBundledAzurPilotRuntime` |
| 原生桥或 CMake | `cd app && ./gradlew assembleDebug -Pazurpilot.slimApk=true` |
| 文案 | `python app/scripts/check_i18n_strings.py` |
| rootfs Python 覆盖层 | `uv run --with psutil python -m unittest discover -s rootfs/tests` |
| Shell 或 PowerShell 工具 | 对应解释器的语法检查，再在授权设备上执行只读或受控测试 |
| 特权、叠层、小组件或保活 UI | 真机全量回归并逐张审查截图；模拟器或编译成功不能覆盖 OEM 行为差异 |

更多构建、安装、签名和排障步骤见 [development-guide.md](development-guide.md)；全局分层和
Runtime 接口见 [architecture.md](architecture.md)。

## English

### Use this reference

1. Locate the owning module in the tables before changing a user-visible behavior; do not bypass a
   boundary from the UI or a global singleton.
2. Read that module's primary-declaration KDoc next. It records the threading, lifecycle, error,
   and platform-compatibility contracts.
3. When a change touches the Runtime, privileged service, native bridge, or publishing, run the
   validation named in the table as well.
4. Put new capabilities behind an existing boundary and expose them through an existing port,
   repository, or state flow. Do not make Compose, `ContentProvider`, JNI, or `app_process`
   depend directly on one another.

### Startup and dependency injection

| Responsibility | Primary entry points | Maintenance contract |
|---|---|---|
| Application startup and process routing | `app/app/src/main/kotlin/com/azurpilot/ghio/AzurPilotApp.kt` | Only the main process performs full initialization; the `:daemon` process starts only its minimum components. `postCreate` starts resident services in dependency order after settings load. |
| Window and Compose root | `MainActivity.kt`, `ui/AppRoot.kt` | The Activity owns splash, edge-to-edge, and keep-screen-on behavior. Business state belongs in a ViewModel, repository, or state flow, never in the Activity. |
| Koin composition root | `di/CoreModule.kt`, `di/HostModule.kt`, `di/LogModule.kt`, `di/OverlayModule.kt`, `di/PrivilegedModule.kt`, `di/ProvisionModule.kt`, `di/ProotModule.kt`, `di/ViewModelModule.kt` | Define an interface or port before adding a dependency, then bind it in its owning module. `AppCoroutineScope` is a process-level supervised scope; do not put UI-lifecycle work in it. |

### Runtime and PRoot

| Responsibility | Primary entry points | Maintenance contract |
|---|---|---|
| First deployment and upgrade | `provision/RootfsProvisioner.kt`, `provision/RuntimeArch.kt` | The rootfs is an immutable artifact published per ABI. Deployment, verification, extraction, and switching must stay recoverable. See [runtime-provisioning.md](runtime-provisioning.md). |
| PRoot session lifecycle | `proot/ProotHost.kt` | The app holds the child process stdin. Closing stdin is the parent-death protocol that tells the Runtime to clean up its process group; do not replace it with an unrelated background launch. |
| Run control, configuration, and Runtime APIs | `proot/AzurPilotRunController.kt`, `proot/AzurPilotGateway.kt`, `proot/AzurPilotRepository.kt`, `proot/AzurPilotConfigEditor.kt` | `AzurPilotGateway` is the sole `/api/v1/ws` subscriber because upstream subscriptions replace the complete set. UI and business logic consume repository and state-flow data. |
| Guest overlays and seeds | `rootfs/overlays/`, `rootfs/seeds/`, `rootfs/build/` | Overlays handle only Android/PRoot compatibility differences; first-run seeding must not overwrite user configuration. Build on the native target ABI. See [multi-arch.md](multi-arch.md). |

### Privileged service and native bridge

| Responsibility | Primary entry points | Maintenance contract |
|---|---|---|
| Backend selection, authorization, and Binder connection | `privileged/PermissionManager.kt`, `privileged/RemoteAccessCoordinator.kt`, `privileged/RemoteServiceManager.kt` | Shizuku and root connectors expose a binder-less state projection through one state machine. Discard stale connection and death callbacks by Binder identity. |
| root `app_process` startup and handback | `privileged/RootRemoteServiceConnector.kt`, `root/RootServiceStarter.kt`, `root/RootServiceBootstrapProvider.kt`, `root/RootServiceBootstrapRegistry.kt` | The handback accepts only root/shell UIDs with a one-time token. The security-ignoring Context in `RootUserService` is limited to the privileged child process and must not spread into ordinary app-process paths. |
| Privileged IPC implementation | `remote/RemoteServiceImpl.kt`, `remote/internal/` | This process owns virtual displays, window/power control, and bridge services. The app calls it through an AIDL port and must not access hidden APIs directly. |
| JNI and frame/input ABI | `bridge/`, `app/app/src/main/native/CMakeLists.txt`, `app/app/src/main/native/bridge.h` | `libbridge.so` runs in the privileged process. Pair `GetLockedPixels` and `UnlockPixels` exactly; a locked frame pointer becomes invalid after unlock. Input opcodes and JNI registration names are cross-language ABI and must not be renumbered or casually renamed. |
| root launcher | `app/app/src/main/native/launcher.c` | The launcher must be emitted as `liblauncher.so` so AGP installs it in `nativeLibraryDir`, the reliable `execve` location at the current targetSdk. |

### Settings, UI, overlays, and widgets

| Responsibility | Primary entry points | Maintenance contract |
|---|---|---|
| Application settings | `settings/AppSettings.kt`, `settings/AppSettingsManager.kt`, `settings/SettingsViewModel.kt` | `@PrefSchema` fields generate camelToSnakeCase DataStore keys. Migrations and raw keys must use snake_case; see `AGENTS.md`. |
| User configuration | `config/UserConfigurationStore.kt`, `domain/UserConfiguration.kt` | This JSON DataStore is independent from the Preferences DataStore for app settings. Do not mix their files, keys, or serialization rules. |
| Compose settings and operational pages | `ui/settings/`, `ui/azurpilot/`, `ui/logs/`, `ui/components/` | Page state comes from a ViewModel or repository. New copy requires translated strings; Chinese `values/` is the source file. |
| Floating controls and screen saver | `overlay/`, `overlay/screensaver/`, `overlay/border/` | Overlays are separate windows and cannot assume they can read an Activity `Configuration` or ViewModelStore. Pass required state through `AppThemeState` and `OverlayController`. |
| Home-screen widgets | `widget/`, `res/xml/widget_*` | Use resources that Glance/RemoteViews can render. Some older vendor systems do not reliably resolve `?attr` vector tints or `cornerRadius`, so widgets require explicit drawable resources. |

### Keep-alive, logs, and diagnosis

| Responsibility | Primary entry points | Maintenance contract |
|---|---|---|
| Session foreground service | `service/RunForegroundService.kt` | Keeps the app process present while the Runtime or virtual display is active; do not label it with an FGS type that does not match its actual purpose. |
| Multi-path keep-alive | `keepalive/KeepAliveManager.kt`, `keepalive/`, `AndroidManifest.xml` | Each subsystem must be idempotently startable and stoppable, with the settings toggle as final authority. Receivers and services can start without DI, so they use a controlled process-level manager entry point. |
| App, Runtime, and privileged logging | `log/`, `constant/AppPaths.kt`, `privileged/ServiceBootLogger.kt` | The app file writer must not log its own failures through Timber. Runtime stdout/stderr go to `proot/session.log`; the privileged startup chain has its own timeline. |
| Manual device diagnosis | `tools/watch-android-logs.ps1`, [adb-e2e-testing.md](adb-e2e-testing.md) | The PowerShell tool defaults to `com.azurpilot.ghio`. A build profile with an application-id suffix requires updating `$package`. |

### Updates, release, and build infrastructure

| Responsibility | Primary entry points | Maintenance contract |
|---|---|---|
| APK and Runtime updates | `update/`, `provision/`, `release-channel.md` | APK and Runtime are independent channels. Verify downloaded size and SHA-256; always leave install confirmation to the system installer. |
| Android build conventions | `app/build-logic/convention/`, `app/app/build.gradle.kts` | SDK levels, ABIs, signing, R8, and Runtime verification are centralized in convention plugins. Keep `GitVersion.kt` and CI parsing rules aligned when changing the version scheme. |
| Annotation processing and hidden-API compile stubs | `app/annotation-api/`, `app/ksp-processor/`, `app/hidden-api/` | `@PrefSchema` accessor generation belongs to the settings layer. Hidden-API stubs provide compile-time signatures only; Android supplies the runtime implementation. |
| CI and Runtime artifacts | `.github/workflows/rootfs.yml`, `.github/workflows/check-upstream.yml`, `app/scripts/fetch-proot-libs.sh` | CI produces the uncommitted rootfs and x86_64 PRoot libraries. Update package pins, hashes, ABI assumptions, and the ELF rewrite plan together. See [development-guide.md](development-guide.md). |

### Minimum validation after a change

| Changed surface | Minimum validation |
|---|---|
| Kotlin, Koin, Compose, or AIDL call paths | `cd app && ./gradlew compileDebugKotlin -x verifyBundledAzurPilotRuntime` |
| Native bridge or CMake | `cd app && ./gradlew assembleDebug -Pazurpilot.slimApk=true` |
| User-facing strings | `python app/scripts/check_i18n_strings.py` |
| rootfs Python overlay | `uv run --with psutil python -m unittest discover -s rootfs/tests` |
| Shell or PowerShell tool | Run the matching interpreter's syntax check, then a read-only or controlled test on an authorized device. |
| Privileged, overlay, widget, or keep-alive UI | Perform full on-device regression and inspect each screenshot; emulator coverage or compilation cannot cover OEM behavior. |

For detailed build, installation, signing, and troubleshooting steps, see
[development-guide.md](development-guide.md). For global layering and Runtime interfaces, see
[architecture.md](architecture.md).
