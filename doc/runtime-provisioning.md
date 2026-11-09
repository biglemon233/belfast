# Runtime 部署与更新 / Runtime provisioning and updates

> 本文说明 RootfsProvisioner 的状态机、下载链路、校验与解压流水线，以及首启行为。
> This document describes the RootfsProvisioner state machine, the download chain,
> verification and extraction, and first-boot behavior.

## 中文

### 状态机

`ProvisionState`（`provision/RootfsProvisioner.kt`）：

| 状态 | 含义 | 去向 |
|---|---|---|
| `Checking` | 检查已装版本；主动安装时比对内置包 | `NotInstalled` / `Downloading` / `Extracting` / `Ready` / `Failed` |
| `NotInstalled` | 本机尚未部署，等待用户安装 | 主页点击「下载 Runtime」后进入 `Checking` |
| `NotBundled` | 未内置且 Release 无本架构 Runtime | 用户重试或换完整版 APK |
| `LowDisk` | 剩余空间不足 2 GB | 清理后重试 |
| `Downloading` | 从 Release 下载（done/total 为压缩字节数） | `Extracting` |
| `Extracting` | 解压中（进度同样按压缩字节计） | `Ready` |
| `Ready` | 在位（已装或本次部署完成） | — |
| `Failed` | 任何异常，`reason` 为面向用户的文案 | 重试 |

### 部署决策

1. 恢复中断：存在 `rootfs.previous` 时先改名回 `rootfs`。
2. `isInstalled()`：marker `.provisioned` 的版本与 rootfs 内
   `opt/azurpilot/BUILD_MANIFEST` 一致，且 `rootfs_arch` 与设备 ABI 匹配
   （旧包无 `rootfs_arch` 字段时按 arm64 处理），且 Python 入口存在。
3. 在位 → 只做 `checkForUpdates()` 提示，不自动替换。
4. 未在位 → 主页显示「下载 Runtime」，不自动下载或解压。用户点击后，使用内置资产
   （架构匹配时）或 Release 下载。主页右上角的绑定按钮可直接进入远程 AP 绑定界面。

### 更新判断

- 更新检查使用 `BUILD_MANIFEST.azurpilot_commit` 表示当前 AP 源码版本；热更后立即刷新
  已查询的更新状态，无需重启 App 或重新下载发布清单。
- 发布版与当前 AP 提交相同时，不提示或下载整包 Runtime，即使版本号末段的构建输入
  哈希不同。该哈希还包含构建脚本、overlay 和种子配置，不能单独代表运行库变化。
- AP 提交不同时，沿用热更或整包更新流程。旧版非标准版本号继续按完整字符串比较。
- `.provisioned` 和 `BUILD_MANIFEST.rootfs_version` 保留原始部署版本，部署完整性检查
  仍比较这两个值，避免热更后误判为损坏并重装。

### 下载链路

新安装默认从 CNB 国内源获取固定发布清单，已保存的源选择保留。App、Runtime 和前端
共用下载源，可在部署页或设置页选择 GitHub 直连及代理镜像。两侧清单和附件对应同一版本，
换源只切换 URL，大小与 SHA-256 校验不变。

- **分段下载**：`update/ReleaseDownloader.kt` 共用 `download-core` 的 OkHttp 下载器。
  清单大小至少 8 MiB 时，先请求 `bytes=0-0` 验证服务器，再用四条连接下载互不重叠的区间。
  每段严格检查 HTTP 206、`Content-Range`、响应长度和可用的文件版本标识；禁用响应压缩。
  小文件和未知大小的文件直接单连接下载。使用 HTTP/1.1 保持分段连接独立，
  connect 20 s、read 120 s，跟随重定向。
- **兼容回退**：探测返回完整 HTTP 200 时复用该响应；分段请求失败或返回错误区间时，
  取消全部分段，等待写线程退出，进度归零，然后从头单连接重试一次。失败后删除半成品。
- **换源语义**：用户在下载中切换下载源 → `shouldAbort` 命中 →
  `DownloadAborted` → 删除半成品 → 按新源从头重下（`SourceChanged` 循环）。
  每 100 ms 检查换源并主动取消 OkHttp 请求，连接或读取卡住时也无需等待网络超时。
- **校验**：下载完成后先比大小再比 SHA-256（清单 `sha256` 字段），
  任一不符抛错进 `Failed`。
- **进度口径**：`Downloading` 与 `Extracting` 都按压缩字节计，total 取清单 `size`。
  分段进度聚合所有线程实际写入的字节，约每 200 ms 在调用线程报告一次。
- **步骤图标**：使用 useAnimations 的下载、开盖归档、文件检查和刷新资源，通过 Lottie
  Compose 播放。APK 下载、校验和热更共用这套图标；阶段切换淡入淡出，进入后台时暂停，
  关闭系统动画时显示首帧，进度数字保持真实值。资源来源及许可见
  [动态图标资源](transfer-icons.md)。

下载器的本地 HTTP 回归测试独立运行，不依赖 Android 测试桩：
`cd app && ./gradlew :download-core:test`。

设备下载实测使用 `:app:assembleDebugAndroidTest -Pazurpilot.downloadBenchmark=true` 单独编译。
运行 `ReleaseDownloadInstrumentedTest` 时显式传入 `releaseUrl`、`releaseSize` 和 `releaseSha256`，
对同一发布资源各下载一次单连接和四连接；两次都校验摘要，结果写入应用
`externalFiles/debug/download-benchmark.json`。未提供资源参数时跳过，不在普通测试中消耗流量。

### 解压流水线

1. 解到 `rootfs.tmp`，全部成功后原子改名——半途失败不留半成品。
2. 纯 Java 流式解（commons-compress tar + tukaani xz）。busybox tar 解
   ubuntu-base 的硬链接有前向引用坑，不使用。
3. 符号链接用 `Os.symlink`；硬链接物化为副本，前向引用登记后补拷。
4. 路径防护：拒绝越界路径与穿过已解出符号链接的条目。
5. 可执行位按 tar mode 还原（proot/python 依赖它）。
6. 重部署时保留用户配置目录（含 SQLite、实例子目录、`reloadalas`）、`password.txt`
   与 `log/`。模板和部署种子使用新镜像版本，旧热更状态与事务日志不迁移。
   原子换名前再次同步，并删除首轮暂存的旧 WAL，防止数据库恢复错位。
7. 写 marker `.provisioned` = 清单版本，随后 `rootfs.tmp` → `rootfs` 改名。

### 首启行为

- 后台自动更新只处理已经在位的 Runtime；首次部署由用户从主页主动发起，下载与解压进度
  显示在部署页。Runtime 未在位时，不记录当天的自动检查日期。
- 解压剥离了 `__pycache__`，新 rootfs 首次启动要重建全部字节码，
  WebUI 可能超过 90 秒才就绪——ProotHost 会打
  `AzurPilot WebUI not ready within 90000ms` 警告，属预期，服务随后自然就绪。
- 第二次启动起恢复正常速度。

## English

### State machine

`ProvisionState` (`provision/RootfsProvisioner.kt`):

| State | Meaning | Next |
|---|---|---|
| `Checking` | Check installation; compare bundled assets on explicit installation | `NotInstalled` / `Downloading` / `Extracting` / `Ready` / `Failed` |
| `NotInstalled` | No local deployment; waits for the user | Home action “Download Runtime” enters `Checking` |
| `NotBundled` | Nothing bundled and the release has no Runtime for this ABI | User retries or installs the full APK |
| `LowDisk` | Fewer than 2 GB free | Retry after cleanup |
| `Downloading` | Fetching from the release (done/total count compressed bytes) | `Extracting` |
| `Extracting` | Extraction in progress (same compressed-byte unit) | `Ready` |
| `Ready` | In place (already installed or deployed now) | — |
| `Failed` | Any error; `reason` carries a user-facing message | Retry |

### Deployment decision

1. Restore an interrupted run: rename `rootfs.previous` back to `rootfs`.
2. `isInstalled()`: the `.provisioned` marker matches the in-rootfs
   `opt/azurpilot/BUILD_MANIFEST`, `rootfs_arch` matches the device ABI
   (manifests without the field count as arm64), and the Python entry point exists.
3. When installed → only `checkForUpdates()` runs; nothing is replaced
   automatically.
4. When missing → show “Download Runtime” on home without downloading or extracting automatically.
   After the user taps it, extract the bundled asset (if the ABI matches) or download from the
   release. The bind button at the top right of home opens remote AP pairing directly.

### Update decisions

- Update checks use `BUILD_MANIFEST.azurpilot_commit` as the current AP source version. Once
  a hot update settles, the cached check refreshes without an app restart or another index download.
- Matching AP commits suppress full Runtime prompts and downloads even when the build-input hash
  suffix differs. That hash also covers build scripts, overlays, and seed configuration, so its
  change alone does not establish a runtime-library change.
- Different AP commits retain the hot-update or full-update paths. Legacy version formats still
  use complete-string comparisons.
- `.provisioned` and `BUILD_MANIFEST.rootfs_version` retain the original deployment version.
  Installation integrity still compares these values to avoid reinstalling after a hot update.

### Download chain

New installations use CNB's fixed release index by default; saved source choices are retained.
App, Runtime, and frontend downloads share the source selected in provisioning or Settings.
GitHub direct access and proxies remain available. Switching sources changes the URL while
preserving version, size, and SHA-256 verification.

- **Segmented downloads**: `update/ReleaseDownloader.kt` shares the OkHttp downloader in
  `download-core`. Manifest sizes of at least 8 MiB trigger a `bytes=0-0` probe, followed by four
  connections covering disjoint ranges. Every part validates HTTP 206, `Content-Range`, response
  length, and available entity validators; response compression is disabled. Small files and
  unknown sizes use one connection. HTTP/1.1 keeps segment connections independent. Connect timeout
  is 20 s, read timeout 120 s; redirects follow.
- **Compatibility fallback**: a whole HTTP 200 probe response is reused. Failed parts or invalid
  ranges cancel every part and wait for writers to finish, reset progress, and restart once over
  one connection. Failures delete partial files.
- **Source-switch semantics**: switching the download source mid-download
  trips `shouldAbort` → `DownloadAborted` → the partial file is deleted and
  the download restarts from the new source (`SourceChanged` loop). Polling every 100 ms actively
  cancels OkHttp calls, including stalled connections and reads, without waiting for network timeouts.
- **Verification**: after download, compare size and then SHA-256 against the
  manifest's `sha256`. Any mismatch raises into `Failed`.
- **Progress unit**: both `Downloading` and `Extracting` count compressed
  bytes; the total comes from the manifest's `size`.
  Segmented progress sums bytes actually written by every worker and reports on the calling thread
  approximately every 200 ms.
- **Stage icons**: bundled useAnimations download, opening archive, file-text, and refresh assets
  play through Lottie Compose. APK downloads, verification, and hot updates share the same icons.
  Stage changes crossfade; background pages pause playback, and disabled system animations show
  the first frame. Progress numbers retain actual values. See [animated icon assets](transfer-icons.md)
  for provenance and licensing.

The downloader's local HTTP regression tests run independently of Android test stubs:
`cd app && ./gradlew :download-core:test`.

Build the isolated device benchmark with
`:app:assembleDebugAndroidTest -Pazurpilot.downloadBenchmark=true`. Run
`ReleaseDownloadInstrumentedTest` with explicit `releaseUrl`, `releaseSize`, and `releaseSha256`
arguments to download the same release asset once in each mode. Both transfers verify checksums
and save results to the app's `externalFiles/debug/download-benchmark.json`. Missing asset arguments
skip the test, preventing network usage in ordinary test runs.

### Extraction pipeline

1. Extract into `rootfs.tmp`, then atomically rename — a failed run leaves
   no partial tree.
2. Pure-Java streaming (commons-compress tar + tukaani xz). busybox tar is
   not used: it breaks on ubuntu-base hard links with forward references.
3. Symlinks go through `Os.symlink`; hard links are materialized as copies,
   with forward references deferred to a second pass.
4. Path hardening: entries outside the root or traversing an extracted
   symlink are rejected.
5. The executable bit is restored from the tar mode (proot and python need it).
6. Preserve user configuration, including SQLite, instance directories, and `reloadalas`,
   plus `password.txt` and `log/`. Keep new templates and deployment seeds and exclude old
   hot-update state and journals. Resync before swapping and remove stale staged WAL files.
7. The `.provisioned` marker is written with the manifest version, then
   `rootfs.tmp` renames to `rootfs`.

### First boot

- Background automatic updates only handle an installed Runtime. The user starts first-time
  deployment from home, and the foreground pipeline shows download and extraction progress. An absent Runtime does not
  consume the day's scheduled check.
- Extraction strips `__pycache__`, so the first boot of a fresh rootfs
  rebuilds all bytecode. The WebUI can take longer than 90 seconds; ProotHost
  logs `AzurPilot WebUI not ready within 90000ms` as an expected warning, and
  the service comes up afterward.
- Subsequent boots run at normal speed.
