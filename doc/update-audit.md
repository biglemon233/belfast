# 更新机制检查 / Update mechanism audit

## 中文

2026-10-12 检查 App APK 更新、整包 Runtime 部署、源码热更和 GitHub/CNB 发布。
参考 [ALAS-AOS 更新脚本](https://github.com/Shinarin/ALAS-AOS/blob/9be72778433608d4fd6039ba37e71cc71efecf16/rootfs/seeds/alasaos_update.sh)
的运行时内 Git 获取方式，保留本项目的 Android API 与父监督器恢复协议。
上游 AzurPilot 检查基于提交 `3181a1ad7f3dc77d902d97f0f1757b08fa6ef70a`。

| 范围 | 发现 | 处理 |
|---|---|---|
| 工具链 | 已有 Git，无 Node；打包删除 `.git`，首次热更没有本地回滚对象 | 内置固定版本 Node/npm，保留浅克隆；构建与设备端共用工具链 |
| 增量来源 | 官方增量只依赖 GitHub 下载链路 | 优先 CNB，失败时从原构建源取同一提交；自定义仓库不改写 |
| 源码通道 | 构建跟随 `dev`，部署种子却写 `master` | 清单记录仓库和分支，热更读取清单；种子改为 `dev` |
| 前端版本 | 上游热更重置源码后仍用旧清单提交下载 dist | 新通道在候选源码中构建目标 dist，通过后才切换 |
| CI 耦合 | 更新前必须有该提交的 Release dist；滚动清理也会删除旧提交资源 | 新镜像不再依赖 dist 发布，旧通道继续兼容 |
| Python 依赖 | 禁止安装依赖，却会允许改变锁文件的源码进入正式目录 | 锁变化不修改源码，明确要求整包更新 |
| 回滚 | 原实现仅在 dist 失败时尝试 reset，不能完整恢复 dist、清单或中断状态 | 切换日志保存旧提交和清单，备份 dist；启动前恢复 |
| 监督器 | `EnableReload=false` 导致热更所需重启事件未创建 | 启用父监督器并迁移旧配置，继续关闭定时更新 |
| Android IPC | `/dev/shm` 缺失，补目录后仍因 SELinux 拒绝 `linkat` 而无法创建信号量 | 绑定私有目录并启用 PRoot `--link2symlink`；真机验证事件和队列跨进程传递 |
| 宿主退出 | 子进程先退出时，stdin daemon 持有缓冲锁会导致 Python 中止 | 用 `os.read` 读取管道，验证保持 stdin 开启时也能正常退出 |
| 通道互斥 | 热更与整包只有状态检查，没有共同流水线锁 | 两条 App 更新链共用部署锁和更新锁 |
| 状态探测 | 客户端 12/20 秒读取超时短于服务端 30 秒 Git 探测；HTTP 200 被当作可热更 | 放宽至 45 秒；解析 enabled 和 requiresRuntimeUpdate |
| 轮询 | 三秒进度轮询可能重复查询上游 | 热更忙时只读本地状态 |
| 等待边界 | 按轮询次数计算的 30 分钟不含 HTTP 等待，可能明显超时 | 使用单调时钟截止时间；请求结果不明时继续持锁轮询 |
| 用户数据 | 整包只迁移顶层 JSON，遗漏 SQLite、实例目录和口令；首轮复制 WAL 可能残留 | 迁移用户配置目录及口令，切换前清理暂存旧 WAL 后重新同步 |
| APK 并发 | 连点可能在协程置 busy 前重复检查或下载同一临时文件 | 在调度协程前原子取得状态 |

### 保留的边界

APK 仍校验大小和 SHA-256，再交系统安装器。整包 Runtime 仍按 ABI 校验和解压，
拒绝越界路径，经暂存目录换名；自动整包更新保留旧镜像并进行服务健康检查。
前端 npm 锁文件可以热更，Python 锁环境与宿主 overlay 变化仍需整包。

新工具链使构建输入哈希变化，旧设备要先安装一次新 Runtime；只更新轻量 APK
不会凭空得到 Node。前端兼容资产仍保留供旧 Runtime 使用。
设备本地构建和保留 Git 对象增加空间及 CPU/内存成本。源码热更验证构建产物，
不提供整包更新的 90 秒服务健康回滚；新版本服务兼容性需真机验证。
GitHub 滚动 Release 的固定文件名仍可能在上传窗口出现清单/资源校验不匹配，
下载验证会拒绝该包，原部署保留，稍后重试。

### 验证入口

`rootfs/tests/test_android_update.py` 使用真实 Git 和 npm 验证更新成功、构建失败、
锁变化、切换失败和中断恢复。rootfs 构建在内置工具链下执行这些测试，并真实
导入上游服务验证适配器和已有前端指纹。Android 使用 `compileDebugKotlin`
验证，翻译使用 `check_i18n_strings.py`，不运行已知不可编译的 Android 单元测试。

首轮完成 Android 编译、Linux PRoot 事务测试、上游真实前端构建及指纹校验、
翻译和相关适配器/发布检查。随后在 M2104K10AC（Android 11、ARM64）安装调试
APK，补齐固定版本 Node/npm 和 Git 元数据，运行 20 个真机回归并全部通过。
真实更新从 CNB 将 `65b5c271b01d` 推进至 `3181a1ad7f3d`，耗时约 78 秒；前端
指纹一致，`rootfs_version` 保持原值，配置文件 SHA-256 未变，SQLite 完整性为
`ok`，WebUI 重载后健康检查及冷启动均通过。

未构建完整双 ABI rootfs，也未启动实际游戏挂机实例；实例恢复文件协议及跨进程
事件/队列在隔离测试中验证。可重复测试入口见 [hot-update.md](hot-update.md)。

## English

The 2026-10-12 audit covers APK updates, full Runtime provisioning, source hot updates,
and GitHub/CNB publication. Runtime Git acquisition follows the
[ALAS-AOS updater](https://github.com/Shinarin/ALAS-AOS/blob/9be72778433608d4fd6039ba37e71cc71efecf16/rootfs/seeds/alasaos_update.sh),
while retaining this project's Android API and supervisor recovery protocol.
AzurPilot was inspected at `3181a1ad7f3dc77d902d97f0f1757b08fa6ef70a`.

| Area | Finding | Resolution |
|---|---|---|
| Toolchain | Git existed without Node; removed `.git` left no local rollback objects | Bundle pinned Node/npm and retain the shallow clone; share the toolchain with image builds |
| Incremental source | Official updates relied solely on GitHub downloads | Prefer CNB with exact-commit fallback; preserve custom sources |
| Source channel | Builds used `dev`, deployment seed specified `master` | Record and use manifest source/branch; change the seed to `dev` |
| Frontend version | Upstream fetched dist using the old manifest commit after resetting source | Build target dist in a candidate before switching |
| CI coupling | Updates required a matching Release dist, vulnerable to rolling pruning | New images build locally; retain legacy assets |
| Python dependencies | Source could change its lock while dependency installation stayed disabled | Keep source unchanged and require a full Runtime update |
| Rollback | Reset alone did not restore dist, manifest, or interrupted state | Journal the old commit/manifest, back up dist, and recover before startup |
| Supervisor | Disabled reload left required restart events uninitialized | Enable supervision and migrate legacy config while keeping scheduled updates disabled |
| Android IPC | Missing `/dev/shm` and SELinux `linkat` denial prevented semaphore creation | Bind private storage and use PRoot `--link2symlink`; verify cross-process events and queues |
| Host exit | A daemon holding stdin's buffered lock aborted Python after child exit | Read with `os.read`; verify normal exit even with stdin held open |
| Mutual exclusion | Separate channels lacked a common pipeline reservation | Share deployment and update locks |
| Probes | Client 12/20-second timeouts were shorter than the 30-second Git probe; HTTP 200 implied capability | Allow 45 seconds and parse enabled/requiresRuntimeUpdate |
| Polling | Three-second polls could repeatedly query upstream | Read local progress while busy |
| Wait bounds | Poll-count limits excluded HTTP waits and could exceed 30 minutes | Use a monotonic deadline and keep polling with the reservation after uncertain requests |
| User data | JSON-only migration missed databases, instance directories, passwords, and stale WAL cleanup | Migrate user configuration/passwords and replace staged WAL before the final sync |
| APK concurrency | Rapid taps could start overlapping requests on the same partial file | Atomically reserve state before coroutine dispatch |

### Retained boundaries

APK downloads still verify size and SHA-256 before using the system installer. Full
Runtime updates retain ABI checks, verified extraction, traversal protection, staging
renames, and automatic-update service health checks with a previous-image backup.
npm lock changes can hot-update; Python locks and host overlays require full updates.

The new toolchain changes the input hash, so existing devices need one full Runtime
upgrade. A slim APK alone cannot add Node. Legacy frontend assets remain available.
Local builds and Git objects add storage, CPU, and memory costs. Source hot updates
validate build output but do not provide the full updater's 90-second service-health
rollback; new service compatibility requires device validation. Fixed filenames in
rolling GitHub Releases can still yield transient manifest/asset mismatches during
uploads. Download checks reject them, retain the deployment, and allow later retries.

### Verification entry points

`rootfs/tests/test_android_update.py` exercises real Git/npm success, build failure,
lock changes, switch failure, and interrupted recovery. Rootfs builds run it with the
bundled toolchain, then import the upstream service to verify adaptation and existing
frontend fingerprints. Android validation uses `compileDebugKotlin`; translations use
`check_i18n_strings.py`. Known uncompilable Android unit tests are excluded.

Initial checks passed Android compilation, Linux PRoot transactions, upstream frontend
build/fingerprint validation, translations, and adapter/publication regressions. A debug
APK was then installed on M2104K10AC (Android 11, ARM64), with pinned Node/npm and Git
metadata added to the existing runtime. All 20 device regressions passed. A real CNB
update advanced `65b5c271b01d` to `3181a1ad7f3d` in about 78 seconds. The frontend
fingerprint matched, `rootfs_version` remained unchanged, configuration SHA-256 stayed
the same, SQLite integrity returned `ok`, and reload health and cold-start checks passed.

Full dual-ABI rootfs images were not rebuilt, and no live game scheduler was started.
Recovery file protocols and cross-process IPC were exercised in isolation. See
[hot-update.md](hot-update.md) for repeatable device commands.
