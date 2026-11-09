# 开发指南 / Development guide

> 本文面向新维护者，覆盖环境准备、首次构建、日常验证、安装调试、签名、版本方案与发布流程。
> This guide onboards new maintainers: environment setup, first build, day-to-day
> verification, install and debugging, signing, the version scheme, and the release process.

## 中文

### 环境准备

| 工具 | 用途 |
|---|---|
| Android Studio | 打开与调试 Android 工程 |
| JDK 17 | JVM 编译目标（`app/build-logic/convention/src/main/kotlin/com/azurpilot/ghio/gradle/AndroidCommon.kt:20`） |
| Android SDK | SDK 基线：compileSdk 37 / targetSdk 36 / minSdk 28（`AndroidCommon.kt:15-17`），需安装 Platform 37 与 platform-tools（含 `adb`） |
| NDK + CMake 3.22.1 | native bridge（截屏/注入）编译，见 `app/app/src/main/native/CMakeLists.txt` 与 `app/app/build.gradle.kts:27-32` |
| Git | 版本号取自提交计数，**必须完整历史**，浅克隆会算错版本 |
| uv | Python 脚本与 rootfs 测试的统一入口 |
| adb | 设备安装、日志与端口转发 |

Windows 注意事项：

1. `python3` 可能是 Microsoft Store 的 App Installer 占位程序，先探测（`python --version`）再用。
2. Python `subprocess` 里的 `bash` 会落到 WSL，而不是 Git Bash；跑 Python 脚本请用 `uv run`。
3. shell 脚本（如 `app/scripts/fetch-proot-libs.sh`）在 Git Bash 中运行；`tools/watch-android-logs.ps1` 在 PowerShell 中运行。

### 首次构建

1. `git clone` 本仓（完整历史）。
2. 用 Android Studio 打开 `app/` 目录——Gradle 工程根在 `app/`，不在仓库根。
3. 快速编译验证：
   ```bash
   cd app && ./gradlew compileDebugKotlin -x verifyBundledAzurPilotRuntime
   ```
4. 出精简 debug 包（无需 rootfs）：
   ```bash
   ./gradlew assembleDebug -Pazurpilot.slimApk=true
   ```
   产物在 `app/app/build/outputs/apk/debug/`。
5. 理解 `verifyBundledAzurPilotRuntime`（`app/app/build.gradle.kts:49`）：`rootfs.tar.xz`（300 MB+）与 `BUILD_MANIFEST` 是 CI 产物、不入库，本地缺失时所有 `package*`/`assemble*` 任务都会在此明确失败（`app/app/build.gradle.kts:71-74`），避免打出没有运行时的残缺包。要么把 CI 产物放进 `app/app/src/main/assets/rootfs/`（见「发布流程」），要么用 `-Pazurpilot.slimApk=true` 跳过。
6. 可选：x86_64 的 proot 库同样不入库（`.gitignore:40`），需要时在 Git Bash 运行 `bash app/scripts/fetch-proot-libs.sh`；调试只想编 arm64 时，在 `app/local.properties` 写 `build.debugAbi=arm64-v8a` 收窄 ABI（`app/build-logic/convention/src/main/kotlin/com/azurpilot/ghio/gradle/AndroidApplicationConventionPlugin.kt:189`）。

### 日常验证

1. **Kotlin 门槛**：改动后跑 `./gradlew compileDebugKotlin -x verifyBundledAzurPilotRuntime`。
2. **不要运行 `:app:testDebugUnitTest`**——测试源集里有孤儿测试，编译不过；Kotlin 正确性一律用 `compileDebugKotlin` 验证。
3. **rootfs 客户机测试**（`rootfs/tests/`，离线回归，目前覆盖 `rootfs/overlays/android_process_compat.py`）：
   ```bash
   uv run --with psutil python -m unittest discover -s rootfs/tests
   ```
4. **i18n 一致性**：
   ```bash
   python app/scripts/check_i18n_strings.py
   ```
   只校验 `values-en/` 对 `values/`（zh-CN 源）：键位对齐、占位符匹配、英文残留中文、文件内重复键；有错非零退出。`--no-fail` 只看报告；`--clean` 删除冗余的 `values-zh/`，它只保证键集合归零差异，若其中存在不同译文会被默认值覆盖。改过任何 `strings.xml` 后都要跑一遍。

### 安装与调试

1. 安装 slim debug 包（默认包名 `com.azurpilot.ghio`，`AndroidApplicationConventionPlugin.kt:31`；构建 profile 只会追加包名段）：
   ```bash
   adb install -r -d app/app/build/outputs/apk/debug/app-debug.apk
   ```
   `-d` 允许版本码回落，遇 `INSTALL_FAILED_VERSION_DOWNGRADE` 必需（见下「版本方案」）。
2. 看日志：
   - logcat：`adb logcat -v time --pid=$(adb shell pidof com.azurpilot.ghio)`。
   - 文件日志落在外部私有目录（`AppPaths.ROOT`，`app/app/src/main/kotlin/com/azurpilot/ghio/constant/AppPaths.kt:50`）下的 `log/`（`AppFiles.kt:24`）：`app.log` 是壳层全量（Timber），`proot/session.log` 是 proot stdout/stderr 与 WebUI 访问记录，设备路径 `/sdcard/Android/data/com.azurpilot.ghio/files/log/`。
   - Windows 一键跟踪：`tools/watch-android-logs.ps1`，`-Source app` 跟踪 logcat、`-Source session` 跟踪 session.log，`-Serial`/`-Adb` 按需传。脚本默认包名为 `com.azurpilot.ghio`；若使用带后缀的构建 profile，按实际 applicationId 改写脚本中的 `$package`。
3. 访问运行时：`adb forward tcp:25548 tcp:25548` 后，浏览器打开 `http://127.0.0.1:25548/` 即 WebUI；`/healthz` 探活，`/android/*` 需要令牌。完整「安装 → Runtime 更新 → 会话就绪 → 网关健康」的手动循环见 [adb-e2e-testing.md](adb-e2e-testing.md)，网关协议见 [architecture.md](architecture.md)。

### 签名与升级安装

- `keystore/` 目录已 gitignore（`.gitignore:34`），正式 keystore 放这里；没有它不影响 debug 构建。
- **Release 签名**：四项 `KEYSTORE_PATH` / `KEYSTORE_PASSWORD` / `KEY_ALIAS` / `KEY_PASSWORD`，环境变量优先、`app/local.properties` 同名键兜底（`app/build-logic/convention/src/main/kotlin/com/azurpilot/ghio/gradle/BuildSettings.kt:30`）；配置于 `AndroidApplicationConventionPlugin.kt:130-141`，v1+v2+v3 全开。没有 keystore 时 release 保持未签名、构建不失败，但产物装不上，只用于编译验证。
- **Debug 签名**：用仓内固定的 `app/app/debug.keystore`（公开调试密钥 androiddebugkey/android），保证所有机器、CI 产出的 debug 包签名一致，可互相覆盖安装（`AndroidApplicationConventionPlugin.kt:143-160`）。
- **升级安装规则**：新包签名必须与已装包一致，否则 `INSTALL_FAILED_UPDATE_INCOMPATIBLE`，只能卸载重装（数据全丢）；debug 与 release 签名不同，互相切换前先卸载。版本码回落加 `adb install -r -d`。

### 版本方案

- **versionCode** = 本仓 HEAD 提交时间戳（`app/build-logic/convention/src/main/kotlin/com/azurpilot/ghio/gradle/GitVersion.kt:40`）；事故时可设环境变量 `APP_VERSION_CODE` 手工钉版。
- **versionName** = `1.2.<X>`：主干 `1.2`（`GitVersion.kt:49`），`X = 提交计数 − 134`，即版本方案切换点是本仓第 134 个提交（`X=0` → `1.2.0`，`GitVersion.kt:59`）；X 随提交增长，不随发布递增。
- CI 的 `resolve` 步用同一公式（`.github/workflows/rootfs.yml:108-123`），并用正则 `^1\.(1|2)\.([0-9]+)$` 校验已发布版本（`.github/workflows/rootfs.yml:111`）；**改 `VERSION_BASE` 或基线计数时，`GitVersion.kt` 与 CI 正则必须同步**。
- CI 用 `git diff --quiet "$app_commit" HEAD -- app` 判断 Android 工程输入是否改变：未变时沿用已发布 app 版本；改变时 versionCode 在上次值上加一、versionName 仍按 `1.2.<X>` 公式计算。`rootfs` 与工作流变化也可触发产物重建，但不单独推进 app 版本。

### 发布流程

推送 `main` 或手动 dispatch 触发 `rootfs.yml`（`.github/workflows/rootfs.yml:3-35`，dispatch 可传上游 `azurpilot_ref`、`publish_update` 等）：

1. **resolve**：取上游 AzurPilot dev head；上游提交与 `app`、`rootfs`、工作流等产物输入均未变时才跳过构建。
2. **build**：按 ABI 矩阵在原生 runner 上构建 rootfs（`rootfs/build/build-azurpilot.sh`），产物 `rootfs-<abi>`（含 `rootfs.tar.xz` 与 `BUILD_MANIFEST`）。
3. **apk**：先跑 `app/scripts/fetch-proot-libs.sh`（`.github/workflows/rootfs.yml:242`）；再构建 ① per-arch 完整 APK（把对应 rootfs 放进 `assets/rootfs/` 后 `assembleRelease -Pazurpilot.releaseAbi=<abi>`，`.github/workflows/rootfs.yml:286-292`）与 ② 通用轻量包（`-Pazurpilot.slimApk=true`，双 ABI 无 rootfs，`.github/workflows/rootfs.yml:297-300`）。
4. **publish**：push 或 `publish_update=true` 时上传固定 Release `azurpilot-android-latest` 并更新 `latest.json`；字段与镜像源见 [release-channel.md](release-channel.md)，架构矩阵见 [multi-arch.md](multi-arch.md)。

本地复刻完整 release 构建：

1. 从成功的 CI run 下载 `rootfs-<abi>` 产物。
2. 把 `rootfs.tar.xz` 与 `BUILD_MANIFEST` 复制到 `app/app/src/main/assets/rootfs/`。
3. 在 `app/local.properties`（或环境变量）配好 `KEYSTORE_*` 四项。
4. `cd app && ./gradlew assembleRelease -Pazurpilot.releaseAbi=arm64-v8a`（省略该属性则出双 ABI 全量包；release 开 R8 混淆，`assembleRelease` 末尾自动跑 `verifyReleaseR8Keeps`）。

### 常见问题

| 现象 | 原因与处理 |
|---|---|
| `verifyBundledAzurPilotRuntime` 失败 | `assets/rootfs/` 缺 `rootfs.tar.xz`/`BUILD_MANIFEST`，或 `rootfs_arch` 与 `-Pazurpilot.releaseAbi` 不符；放产物或改用 `-Pazurpilot.slimApk=true` |
| i18n 校验失败 | `values-en/` 漏翻、多余键、占位符不匹配或残留中文；先改 `values/`（源）再同步 `values-en/` |
| 本地缺 x86_64 proot 库 | 该目录不入库（`.gitignore:40`）；跑 `app/scripts/fetch-proot-libs.sh`。新增原生库必须命名为 `lib*.so` 放进 `jniLibs`/`prootLibs`——这是 targetSdk 35+ 唯一保证可 `execve` 的位置 |
| 启动即崩 | 外部存储不可用时 `AppPaths.init` 刻意快速失败（`AppPaths.kt:50`）；确认存储已挂载 |
| `INSTALL_FAILED_UPDATE_INCOMPATIBLE` | 签名不一致（debug/release 互切或换 keystore）；卸载重装 |
| `INSTALL_FAILED_VERSION_DOWNGRADE` | 本地时间戳版本码高于 CI 递增码；加 `adb install -d` |
| 版本号异常为 `1.2.0` | 浅克隆导致提交计数为 0；重新取完整历史 |
| Windows 下脚本行为怪异 | `python3` 可能是商店占位程序；Python `subprocess` 里的 `bash` 是 WSL；shell 脚本用 Git Bash，Python 用 `uv run` |

### 相关文档

- [architecture.md](architecture.md) — 系统分层与网关协议
- [runtime-provisioning.md](runtime-provisioning.md) — Runtime 部署与更新状态机
- [release-channel.md](release-channel.md) — `latest.json` 字段、镜像源与版本规则
- [multi-arch.md](multi-arch.md) — 架构矩阵与 32 位不可行的原因
- [adb-e2e-testing.md](adb-e2e-testing.md) — ADB 全流程测试手册
- [comment-style.md](comment-style.md) — 注释规范
- [build-profiles.md](build-profiles.md) — PI 打包配方、身份覆盖与验证
- [privileged-bridge-protocol.md](privileged-bridge-protocol.md) — 特权回环桥协议与修改规则
- [AGENTS.md](../AGENTS.md) — 构建命令与仓库约定的速查表

## English

### Prerequisites

| Tool | Purpose |
|---|---|
| Android Studio | Opening and debugging the Android project |
| JDK 17 | JVM compile target (`app/build-logic/convention/src/main/kotlin/com/azurpilot/ghio/gradle/AndroidCommon.kt:20`) |
| Android SDK | Baselines: compileSdk 37 / targetSdk 36 / minSdk 28 (`AndroidCommon.kt:15-17`); install Platform 37 and platform-tools (includes `adb`) |
| NDK + CMake 3.22.1 | Building the native bridge (capture/injection), see `app/app/src/main/native/CMakeLists.txt` and `app/app/build.gradle.kts:27-32` |
| Git | Versions derive from the commit count, so a **full history** is required; shallow clones compute wrong versions |
| uv | Single entry point for Python scripts and rootfs tests |
| adb | Device install, logs, and port forwarding |

Windows notes:

1. `python3` may be the Microsoft Store App Installer stub; probe with `python --version` before using it.
2. `bash` inside a Python `subprocess` resolves to WSL, not Git Bash; run Python scripts through `uv run`.
3. Run shell scripts (such as `app/scripts/fetch-proot-libs.sh`) in Git Bash; run `tools/watch-android-logs.ps1` in PowerShell.

### First build

1. `git clone` the repository (full history).
2. Open the `app/` directory in Android Studio — the Gradle root is `app/`, not the repository root.
3. Fast compile check:
   ```bash
   cd app && ./gradlew compileDebugKotlin -x verifyBundledAzurPilotRuntime
   ```
4. Build a slim debug APK (no rootfs needed):
   ```bash
   ./gradlew assembleDebug -Pazurpilot.slimApk=true
   ```
   Output lands in `app/app/build/outputs/apk/debug/`.
5. Understand `verifyBundledAzurPilotRuntime` (`app/app/build.gradle.kts:49`): `rootfs.tar.xz` (300 MB+) and `BUILD_MANIFEST` are CI artifacts and are not committed; when they are missing locally, every `package*`/`assemble*` task fails there on purpose (`app/app/build.gradle.kts:71-74`) so an APK without a runtime can never ship. Either copy CI artifacts into `app/app/src/main/assets/rootfs/` (see "Release process") or skip with `-Pazurpilot.slimApk=true`.
6. Optional: the x86_64 proot libraries are not committed either (`.gitignore:40`); run `bash app/scripts/fetch-proot-libs.sh` in Git Bash when needed. To compile arm64 only while debugging, set `build.debugAbi=arm64-v8a` in `app/local.properties` (`app/build-logic/convention/src/main/kotlin/com/azurpilot/ghio/gradle/AndroidApplicationConventionPlugin.kt:189`).

### Day-to-day verification

1. **Kotlin gate**: after any change run `./gradlew compileDebugKotlin -x verifyBundledAzurPilotRuntime`.
2. **Do not run `:app:testDebugUnitTest`** — the test source set contains orphaned tests that fail to compile; validate Kotlin changes with `compileDebugKotlin` instead.
3. **rootfs guest tests** (`rootfs/tests/`, offline regression, currently covering `rootfs/overlays/android_process_compat.py`):
   ```bash
   uv run --with psutil python -m unittest discover -s rootfs/tests
   ```
4. **i18n consistency**:
   ```bash
   python app/scripts/check_i18n_strings.py
   ```
   It validates `values-en/` against `values/` (the zh-CN source): key parity, placeholder match, residual Chinese in English strings, and duplicate keys within a file; it exits non-zero on errors. `--no-fail` reports without failing; `--clean` removes the redundant `values-zh/`. It guarantees only zero key-set difference: divergent translated text in that directory is discarded in favor of default values. Run it after touching any `strings.xml`.

### Install and debug

1. Install the slim debug APK (default applicationId `com.azurpilot.ghio`, `AndroidApplicationConventionPlugin.kt:31`; a build profile only appends package segments):
   ```bash
   adb install -r -d app/app/build/outputs/apk/debug/app-debug.apk
   ```
   `-d` allows a version-code downgrade; it is required when you hit `INSTALL_FAILED_VERSION_DOWNGRADE` (see "Version scheme" below).
2. Read the logs:
   - logcat: `adb logcat -v time --pid=$(adb shell pidof com.azurpilot.ghio)`.
   - File logs live under `log/` (`AppFiles.kt:24`) in the external private dir (`AppPaths.ROOT`, `app/app/src/main/kotlin/com/azurpilot/ghio/constant/AppPaths.kt:50`): `app.log` holds the full app-shell log (Timber), `proot/session.log` holds proot stdout/stderr and the WebUI access log. On-device path: `/sdcard/Android/data/com.azurpilot.ghio/files/log/`.
   - One-command tailing on Windows: `tools/watch-android-logs.ps1`, with `-Source app` for logcat and `-Source session` for session.log; pass `-Serial`/`-Adb` as needed. Its default package is `com.azurpilot.ghio`; when using a suffixed build profile, update `$package` in the script to the effective applicationId.
3. Reach the runtime: after `adb forward tcp:25548 tcp:25548`, open `http://127.0.0.1:25548/` in a browser for the WebUI; `/healthz` is the readiness probe and `/android/*` requires a token. The full manual loop "install → Runtime update → session ready → gateway health" is in [adb-e2e-testing.md](adb-e2e-testing.md); the gateway protocol is in [architecture.md](architecture.md).

### Signing and in-place upgrades

- The `keystore/` directory is gitignored (`.gitignore:34`); keep the release keystore there. Debug builds work without it.
- **Release signing**: four values `KEYSTORE_PATH` / `KEYSTORE_PASSWORD` / `KEY_ALIAS` / `KEY_PASSWORD`; environment variables win over same-named keys in `app/local.properties` (`app/build-logic/convention/src/main/kotlin/com/azurpilot/ghio/gradle/BuildSettings.kt:30`); wired at `AndroidApplicationConventionPlugin.kt:130-141` with v1+v2+v3 all enabled. Without a keystore the release stays unsigned and the build does not fail, but the APK cannot install — it is only good for compile verification.
- **Debug signing**: uses the repo-checked-in `app/app/debug.keystore` (public debug key androiddebugkey/android), so debug APKs from every machine and from CI share one signature and upgrade over each other (`AndroidApplicationConventionPlugin.kt:143-160`).
- **Upgrade rule**: the new APK's signature must match the installed package, otherwise you get `INSTALL_FAILED_UPDATE_INCOMPATIBLE` and must uninstall first (losing all data); debug and release signatures differ, so uninstall before switching between them. Add `adb install -r -d` for version-code downgrades.

### Version scheme

- **versionCode** = the commit timestamp of this repository's HEAD (`app/build-logic/convention/src/main/kotlin/com/azurpilot/ghio/gradle/GitVersion.kt:40`); set the `APP_VERSION_CODE` environment variable to hand-pin during a release incident.
- **versionName** = `1.2.<X>`: the stem `1.2` (`GitVersion.kt:49`), `X = commit count − 134`, i.e. the commit that switched the scheme is #134 in this repository (`X=0` → `1.2.0`, `GitVersion.kt:59`); X grows with commits, not with releases.
- The CI `resolve` step uses the same formula (`.github/workflows/rootfs.yml:108-123`) and validates published versions with the regex `^1\.(1|2)\.([0-9]+)$` (`.github/workflows/rootfs.yml:111`); **when changing `VERSION_BASE` or the baseline count, keep `GitVersion.kt` and the CI regex in sync**.
- CI uses `git diff --quiet "$app_commit" HEAD -- app` to decide whether Android-project inputs changed. With no change it retains the published app version; with change it increments versionCode from the previous value while still calculating versionName from `1.2.<X>`. `rootfs` and workflow changes can also trigger an artifact rebuild, but do not independently advance the app version.

### Release process

Pushing to `main` — or a manual dispatch with inputs such as the upstream `azurpilot_ref` and `publish_update` — triggers `rootfs.yml` (`.github/workflows/rootfs.yml:3-35`):

1. **resolve**: picks the upstream AzurPilot dev head; it skips the build only when the upstream commit and artifact inputs such as `app`, `rootfs`, and the workflow are unchanged.
2. **build**: builds the rootfs per ABI on native runners (`rootfs/build/build-azurpilot.sh`), producing artifacts `rootfs-<abi>` (containing `rootfs.tar.xz` and `BUILD_MANIFEST`).
3. **apk**: first runs `app/scripts/fetch-proot-libs.sh` (`.github/workflows/rootfs.yml:242`); then builds ① per-arch full APKs (copies the matching rootfs into `assets/rootfs/` and runs `assembleRelease -Pazurpilot.releaseAbi=<abi>`, `.github/workflows/rootfs.yml:286-292`) and ② the universal slim APK (`-Pazurpilot.slimApk=true`, both ABIs, no rootfs, `.github/workflows/rootfs.yml:297-300`).
4. **publish**: on push or `publish_update=true`, uploads to the fixed release `azurpilot-android-latest` and refreshes `latest.json`; for fields and mirrors see [release-channel.md](release-channel.md), for the architecture matrix see [multi-arch.md](multi-arch.md).

Reproducing a full release build locally:

1. Download the `rootfs-<abi>` artifacts from a successful CI run.
2. Copy `rootfs.tar.xz` and `BUILD_MANIFEST` into `app/app/src/main/assets/rootfs/`.
3. Configure the four `KEYSTORE_*` values in `app/local.properties` (or environment variables).
4. `cd app && ./gradlew assembleRelease -Pazurpilot.releaseAbi=arm64-v8a` (omit the property for the dual-ABI full package; release runs R8 minification and `assembleRelease` finishes with `verifyReleaseR8Keeps`).

### Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `verifyBundledAzurPilotRuntime` fails | `assets/rootfs/` is missing `rootfs.tar.xz`/`BUILD_MANIFEST`, or `rootfs_arch` mismatches `-Pazurpilot.releaseAbi`; place the artifacts or use `-Pazurpilot.slimApk=true` |
| i18n check fails | `values-en/` has missing or extra keys, placeholder mismatch, or residual Chinese; edit `values/` (the source) first, then sync `values-en/` |
| x86_64 proot libraries missing locally | That directory is not committed (`.gitignore:40`); run `app/scripts/fetch-proot-libs.sh`. Any new native library must be named `lib*.so` inside `jniLibs`/`prootLibs` — the only location guaranteed `execve`-able at targetSdk 35+ |
| Crash right at startup | `AppPaths.init` fails fast when external storage is unavailable (`AppPaths.kt:50`); confirm storage is mounted |
| `INSTALL_FAILED_UPDATE_INCOMPATIBLE` | Signature mismatch (debug/release switch or a new keystore); uninstall and reinstall |
| `INSTALL_FAILED_VERSION_DOWNGRADE` | Local timestamp version code is higher than the CI-incremented one; add `adb install -d` |
| Version stuck at `1.2.0` | Shallow clone zeroes the commit count; re-clone with full history |
| Scripts behave oddly on Windows | `python3` may be the Store stub; `bash` inside Python `subprocess` is WSL; use Git Bash for shell scripts and `uv run` for Python |

### Related documents

- [architecture.md](architecture.md) — system layering and the gateway protocol
- [runtime-provisioning.md](runtime-provisioning.md) — the Runtime provisioning and update state machine
- [release-channel.md](release-channel.md) — `latest.json` fields, mirrors, and version rules
- [multi-arch.md](multi-arch.md) — the architecture matrix and why 32-bit is not feasible
- [adb-e2e-testing.md](adb-e2e-testing.md) — the ADB end-to-end testing guide
- [comment-style.md](comment-style.md) — comment conventions
- [build-profiles.md](build-profiles.md) — PI packaging recipes, identity overrides, and verification
- [privileged-bridge-protocol.md](privileged-bridge-protocol.md) — privileged loopback bridge protocol and change rules
- [AGENTS.md](../AGENTS.md) — quick reference for build commands and repository conventions
