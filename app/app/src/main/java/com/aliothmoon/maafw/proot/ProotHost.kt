package com.aliothmoon.maafw.proot

import android.app.Application
import android.os.Build
import com.aliothmoon.maafw.BuildConfig
import com.aliothmoon.maafw.provision.RuntimeArchitecture
import com.aliothmoon.maafw.MaaDispatchers
import com.aliothmoon.maafw.constant.AppPaths
import com.aliothmoon.maafw.service.RunForegroundService
import com.aliothmoon.maafw.settings.AppSettingsManager
import com.aliothmoon.maafw.settings.LowPrecisionRecognition
import com.aliothmoon.maafw.settings.AlasSourceRepository
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import timber.log.Timber
import java.io.File
import java.io.RandomAccessFile
import java.net.HttpURLConnection
import java.net.URL
import java.nio.file.Files
import java.util.concurrent.TimeUnit

/**
 * proot 会话宿主：以 App 进程为父，拉起 rootfs 内的 wrapper.py（WebUI 由 wrapper 监管）
 *
 * 运行链路：
 * 自愈清锁 → 离线还原工作区（缺 marker 时从 .git pack 物化）→ 铺 overlay → 播种实例配置
 * → 热更新（降级不阻塞）→ 必要时重放补丁
 * → ProcessBuilder 拉起 proot 长跑会话 → 崩溃/退出带退避重拉
 *
 * 生命周期约定：
 * - **stdin 管道必须保持敞开**：wrapper 挂 stdin 监控线程，App 进程一死管道 EOF，
 *   wrapper 杀 runner/gui 进程组后自尽（防孤儿主链路；stop() 也是先关 stdin）
 * - 重拉走 supervisor 协程，退避 3s 翻倍至 60s；显式启停重查更新，崩溃重拉不重复
 * - FGS 保活：会话活跃期间 RunForegroundService 钉住 app 进程（其退出判据已并入本会话状态）
 */
class ProotHost(
    private val app: Application,
    private val scope: CoroutineScope,
    private val settings: AppSettingsManager,
    private val prepareAutomation: suspend () -> Boolean = { true },
) {

    private val _state = MutableStateFlow(ProotSnapshot())
    val state: StateFlow<ProotSnapshot> = _state.asStateFlow()

    private val startMutex = Mutex()
    @Volatile
    private var session: Process? = null
    private var supervisorJob: kotlinx.coroutines.Job? = null
    private var lateReadinessJob: kotlinx.coroutines.Job? = null
    private val errorNotifications = AlasErrorNotifications(app) { settings.alasErrorNotifications.value }

    @Volatile
    private var wantRunning = false

    /** 显式停止后重查更新；supervisor 崩溃重拉不重复更新。 */
    @Volatile
    private var updateAttempted = false

    private val rootfsDir: File get() = File(app.filesDir, "rootfs")
    private val alasDir: File get() = File(rootfsDir, "opt/alas")
    private val prootTmpDir: File get() = File(app.filesDir, "proot-tmp")
    // Android /dev has no /dev/shm. Python multiprocessing.Event (GUI reload)
    // needs named POSIX semaphores here; bind app-private storage after /dev.
    // Keep it under the existing session temp cleanup.
    private val prootShmDir: File get() = File(prootTmpDir, "shm")
    private val sessionLog: File get() = File(AppPaths.LOG_DIR, "proot/session.log")
    private val nativeLibDir: String get() = app.applicationInfo.nativeLibraryDir
    private val runtimeAbi: String get() = RuntimeArchitecture.runtimeAbi(BuildConfig.RUNTIME_ABI,
        RuntimeArchitecture.deviceAbi(File("/system/bin/app_process64"), Build.SUPPORTED_ABIS.toList()),
        settings.rootfsRuntimeAbi.value)
    private val prootFile: File get() = File(nativeLibDir,
        RuntimeArchitecture.prootFileName(runtimeAbi, BuildConfig.RUNTIME_ABI == "universal"))
    private val loaderFile: File get() = File(nativeLibDir,
        RuntimeArchitecture.prootFileName(runtimeAbi, BuildConfig.RUNTIME_ABI == "universal", loader = true))

    // ------------------------------------------------------------------ 对外入口

    /** 幂等：已在跑/在起直接返回；失败后可重复调（手动重试同一入口） */
    fun ensureStarted() {
        wantRunning = true
        scope.launch(MaaDispatchers.IO) { startLocked() }
    }

    /** 停会话：关 stdin 让 wrapper 自尽，超时兜底 destroyForcibly */
    fun stop() {
        wantRunning = false
        supervisorJob?.cancel()
        lateReadinessJob?.cancel()
        scope.launch(MaaDispatchers.IO) {
            startMutex.withLock {
                val proc = session
                if (proc != null) {
                    Timber.i("proot session: stopping")
                    runCatching { proc.outputStream.close() }
                    withTimeoutOrNull(STOP_GRACE_MS) { runInterruptible { proc.waitFor() } }
                    if (proc.isAlive) {
                        Timber.w("proot session: still alive after stdin close, destroyForcibly")
                        proc.destroyForcibly()
                    }
                }
                session = null
                errorNotifications.stop()
                updateAttempted = false
                _state.update { it.copy(phase = ProotPhase.IDLE, detail = "", transfer = null) }
            }
        }
    }

    // ------------------------------------------------------------------ 启动链

    private suspend fun startLocked() = startMutex.withLock {
        if (!wantRunning || session?.isAlive == true) return@withLock
        _state.update { it.copy(startup = StartupChecklistState(), transfer = null) }
        beginStep(StartupStep.RUNTIME, ProotPhase.PREPARING, "检查运行环境")
        if (!sanityCheck()) return@withLock

        beginStep(StartupStep.CLEANUP, ProotPhase.PREPARING, "清理残留")
        cleanupStale()
        writeResolvConf()

        beginStep(StartupStep.RESTORE, ProotPhase.PREPARING, "还原 ALAS 文件")
        if (!restoreAlasTree()) return@withLock

        beginStep(StartupStep.OVERLAY, ProotPhase.PREPARING, "同步运行文件")
        val overlay = AlasOverlay(app).apply(alasDir)
        if (overlay.failed > 0) {
            fail("运行文件覆盖失败（${overlay.failed} 项）")
            return@withLock
        }

        beginStep(StartupStep.ENVIRONMENT, ProotPhase.PREPARING, "环境自检修复")
        // Optional repair failures remain visible, without leaking raw pip/Git diagnostics.
        recordGuestResult(runGuest(listOf("/bin/bash", "seeds/env_fix.sh"), ENV_FIX_TIMEOUT_MS))

        beginStep(StartupStep.CONFIG, ProotPhase.PREPARING, "播种实例配置")
        // Stdlib-only helpers do not need the site hook that eagerly imports NumPy.
        val config = runGuest(
            listOf("/usr/bin/python3", "-S", "seeds/seed_config.py"), SHORT_EXEC_MS,
            mapOf("ALASAOS_ALAS_ROOT" to GUEST_ALAS_ROOT),
        )
        val deploy = runGuest(
            listOf("/usr/bin/python3", "-S", "seeds/seed_deploy.py"), SHORT_EXEC_MS,
            mapOf(
                "ALASAOS_ALAS_ROOT" to GUEST_ALAS_ROOT,
                "ALASAOS_THEME" to AlasSourceRepository(alasDir).readTheme(),
            ),
        )
        recordGuestResult(if (config?.exit == 0 && !config.timedOut) deploy else config)

        beginStep(StartupStep.UPDATE, ProotPhase.UPDATING, "检查 ALAS 热更新")
        var updated = false
        if (!updateAttempted) {
            updateAttempted = true
            val update = updateWithProgress()
            updated = update.updated
            _state.update { it.copy(updateResult = update.summary) }
            finishStep(if (update.summary.startsWith("SKIPPED")) StepStatus.WARNING else StepStatus.DONE)
        } else {
            finishStep(StepStatus.SKIPPED)
        }

        beginStep(StartupStep.PATCHES, ProotPhase.PREPARING, "应用本地补丁")
        if (updated && AlasOverlay(app).apply(alasDir).failed > 0) {
            fail("更新后运行文件覆盖失败，请重试")
            return@withLock
        }

        beginStep(StartupStep.ARGUMENTS, ProotPhase.PREPARING, "再生 args 配置")
        // The helper starts the upstream generator in a separate, normal Python.
        recordGuestResult(runGuest(listOf("/usr/bin/python3", "-S", "seeds/regen_args.py"), REGEN_ARGS_TIMEOUT_MS))
        if (!wantRunning) return@withLock

        beginStep(StartupStep.SESSION, ProotPhase.STARTING, "拉起 proot 会话")
        // Native deploy.Webui.Run starts inside the GUI, so the bridge and VD
        // must be ready before spawning it, including when no Activity exists.
        val autoRun = runCatching { AlasSourceRepository(alasDir).readRunConfig() }.getOrDefault("")
        if (autoRun.isNotEmpty() && !prepareAutomation()) {
            fail("自动运行需要 Root 或已启动并授权的 Shizuku，请检查权限后重试")
            return@withLock
        }
        errorNotifications.start(alasDir)
        val proc = runCatching { spawnSession() }.getOrElse {
            fail("无法启动 proot（${it.javaClass.simpleName}），请查看运行环境")
            return@withLock
        }
        session = proc
        RunForegroundService.start(app)
        supervise(proc)

        beginStep(StartupStep.SERVICES, ProotPhase.STARTING, "等待控制服务与 WebUI 就绪")
        if (awaitServices(SERVICES_UP_MS, proc)) {
            setState(ProotPhase.RUNNING)
            Timber.i("proot session up: wrapper ready on %d", WRAPPER_PORT)
        } else if (wantRunning && session === proc && proc.isAlive) {
            fail("ALAS 控制服务启动超时，正在等待恢复")
            watchLateReadiness(proc)
        }
    }

    private suspend fun updateWithProgress(): AlasUpdater.Result = kotlinx.coroutines.coroutineScope {
        val progressFile = File(alasDir, ".alasaos_update_progress")
        val canTrack = !progressFile.exists() || progressFile.delete()
        val poller = launch(MaaDispatchers.IO) {
            while (canTrack) {
                val progress = TransferProgress.read(progressFile)
                if (progress != null) _state.update {
                    if (it.phase == ProotPhase.UPDATING) it.copy(transfer = progress) else it
                }
                delay(500)
            }
        }
        try {
            val legacy = settings.settings.first()
            try {
                AlasSourceRepository(alasDir).migrateLegacy(legacy.updateSource, legacy.updateBranch)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (_: Exception) {
                return@coroutineScope AlasUpdater.Result(false, "SKIPPED deploy-config")
            }
            val updateEnv = AlasUpdateProxy.environment(app)
            AlasUpdater { cmd, timeout -> runGuestRaw(cmd, timeout, updateEnv) }.update()
        } finally {
            poller.cancel()
            poller.join()
        }
    }

    private fun recordGuestResult(result: ExecResult?) {
        if (result == null || result.exit != 0 || result.timedOut) {
            Timber.w("startup step=%s failed exit=%s timeout=%s", _state.value.startup.current, result?.exit, result?.timedOut)
            finishStep(StepStatus.WARNING)
        }
    }

    private fun sanityCheck(): Boolean {
        if (!File(rootfsDir, "usr/bin/python3").exists()) {
            fail("rootfs 未部署（python3 缺失）")
            return false
        }
        if (!prootFile.exists()) {
            fail("libproot.so 缺失（当前 ABI 不支持？）")
            return false
        }
        if (!loaderFile.exists()) {
            fail("libproot-loader.so 缺失")
            return false
        }
        return true
    }

    /**
     * 首启还原：rootfs 镜像只带浅克隆 .git（瘦身——pack 体积≈整树压缩，树+pack 两份
     * 快照只发一份），git 跟踪的工作区文件在打包前已剥离，untracked 的 AOS 自有资产
     * （seeds/、config/deploy.yaml、BUILD_MANIFEST、wrapper 等）在镜像里完好。
     * 缺 marker 时由 guest 脚本从 pack 离线物化（git reset --hard，无需网络，一次性
     * 数十秒），成功后落 marker，后续启动本方法宿主侧零开销跳过；旧版完整工作区的
     * rootfs 升级上来时脚本同样补 marker。失败即 fatal——树不全 ALAS 必然起不来
     * （官方 Windows 一键包 deploy/git.py 同款方案）。
     */
    private suspend fun restoreAlasTree(): Boolean {
        if (File(alasDir, RESTORE_MARKER).isFile && !File(alasDir, RESTORE_PENDING).isFile) return true
        // Restore runs before the full overlay. Refresh its seed first so an old
        // rootfs cannot block an APK's restore fix or omit the script altogether.
        val prepared = runCatching {
            val seed = File(alasDir, RESTORE_SEED)
            val bytes = app.assets.open("alas/overlay/$RESTORE_SEED").use { it.readBytes() }
            if (!seed.isFile || !seed.readBytes().contentEquals(bytes)) {
                seed.parentFile?.mkdirs()
                seed.writeBytes(bytes)
            }
        }.onFailure { Timber.w(it, "restore seed preparation failed") }.isSuccess
        if (!prepared) {
            fail("ALAS 还原脚本准备失败，请重试")
            return false
        }
        val r = runGuest(listOf("/bin/bash", "seeds/alasaos_restore.sh"), RESTORE_TIMEOUT_MS)
        if (r != null && r.exit == 0 && !r.timedOut) return true
        fail("ALAS 文件还原失败（exit=${r?.exit ?: "?"}），请重试或重新安装整包")
        return false
    }

    // ------------------------------------------------------------------ 会话

    /** 拉起长跑会话；调用方持有返回的 Process（stdin 保持敞开，见类头约定） */
    private fun spawnSession(): Process {
        prootTmpDir.mkdirs()
        check(prootShmDir.isDirectory || prootShmDir.mkdirs()) { "Cannot prepare proot shared memory" }
        LowPrecisionRecognition.write(prootShmDir, settings.lowPrecisionRecognition.value)
        sessionLog.parentFile?.mkdirs()
        val cmd = listOf(
            prootFile.absolutePath,
            // glibc sem_open uses hard links, forbidden in Android app_data_file.
            "--link2symlink",
            "-w", GUEST_ALAS_ROOT,
            "-r", rootfsDir.absolutePath,
            "-b", "/dev:/dev", "-b", "/proc:/proc", "-b", "/sys:/sys",
            "-b", "${prootShmDir.absolutePath}:/dev/shm",
            // The stdlib supervisor starts the ALAS GUI with normal site initialization.
            "/usr/bin/python3", "-S", "wrapper.py",
        )
        Timber.i("proot session spawn: %s", cmd.joinToString(" "))
        val proc = ProcessBuilder(cmd)
            .directory(alasDir)
            .apply { environment().remove("LD_PRELOAD"); environment().putAll(baseEnv()) }
            .start()
        drainTo(proc.inputStream, "proot-out")
        drainTo(proc.errorStream, "proot-err")
        return proc
    }

    /** proot 进程环境与原生库搜索路径 */
    private fun baseEnv(): Map<String, String> = mapOf(
        "LD_LIBRARY_PATH" to nativeLibDir,
        "PROOT_LOADER" to loaderFile.absolutePath,
        "PROOT_TMP_DIR" to prootTmpDir.absolutePath,
        "TMPDIR" to prootTmpDir.absolutePath,
        "HOME" to app.filesDir.absolutePath,
        "PATH" to "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
        "LANG" to "C.UTF-8",
        // rootfs 未装 tzdata：用 POSIX 形式 CST-8（UTC+8 无 DST），不依赖 zoneinfo 文件；
        // 不设则全环境 UTC，ALAS 日志/调度时间比设备慢 8 小时
        "TZ" to "CST-8",
        "ALASAOS_ALAS_ROOT" to GUEST_ALAS_ROOT,
        "ALASAOS_WEBUI" to "1",
    )

    /** stdout/stderr 汇进 session 日志（带行级时间戳太贵，纯追加即可） */
    private fun drainTo(stream: java.io.InputStream, tag: String) {
        Thread {
            runCatching {
                sessionLog.parentFile?.mkdirs()
                java.io.FileOutputStream(sessionLog, true).bufferedWriter().use { w ->
                    stream.bufferedReader().forEachLine {
                        w.append("[$tag] ").append(it)
                        w.newLine()
                        // 行量小（wrapper 生命周期事件），逐行 flush 保证现场随时可查
                        w.flush()
                    }
                }
            }.onFailure { Timber.d("drain %s closed: %s", tag, it.message) }
        }.apply { isDaemon = true; name = "proot-drain-$tag" }.start()
    }

    /** 崩溃/退出重拉：退避 3s 翻倍至 60s；wantRunning 撤了就不拉。
     *  熔断：会话连续秒退（<QUICK_DEATH_MS）MAX_RAPID_DEATHS 次即放弃——典型诱因是
     *  端口被同机旧装 App 的残留会话占用，此时 awaitServices 会被占位者喂成假 RUNNING，
     *  不退熔断就是 3s 一轮的无限崩溃循环 */
    private fun supervise(first: Process) {
        supervisorJob?.cancel()
        supervisorJob = scope.launch(MaaDispatchers.IO) {
            var proc = first
            var backoff = RESTART_BACKOFF_INIT_MS
            var rapidDeaths = 0
            var spawnedAt = System.currentTimeMillis()
            while (true) {
                val code = try {
                    runInterruptible { proc.waitFor() }
                } catch (cancelled: CancellationException) {
                    throw cancelled
                }
                if (session !== proc) break
                val livedMs = System.currentTimeMillis() - spawnedAt
                Timber.w("proot session exited code=%s lived=%dms", code, livedMs)
                session = null
                if (!wantRunning) break
                rapidDeaths = if (livedMs < QUICK_DEATH_MS) rapidDeaths + 1 else 0
                if (rapidDeaths >= MAX_RAPID_DEATHS) {
                    fail("会话连续 $MAX_RAPID_DEATHS 次秒退（端口被占用？），已停止重拉")
                    break
                }
                beginStep(StartupStep.SESSION, ProotPhase.STARTING, "会话退出($code)，${backoff / 1000}s 后重拉")
                delay(backoff)
                backoff = (backoff * 2).coerceAtMost(RESTART_BACKOFF_MAX_MS)
                if (!wantRunning) break
                cleanupStale()
                val next = runCatching { spawnSession() }
                    .onFailure { Timber.w(it, "proot respawn failed") }
                    .getOrNull() ?: continue
                session = next
                proc = next
                spawnedAt = System.currentTimeMillis()
                beginStep(StartupStep.SERVICES, ProotPhase.STARTING, "等待控制服务与 WebUI 就绪")
                if (awaitServices(SERVICES_UP_MS, next)) {
                    backoff = RESTART_BACKOFF_INIT_MS
                    setState(ProotPhase.RUNNING)
                    Timber.i("proot session respawned, wrapper ready")
                } else {
                    fail("ALAS 控制服务启动超时，正在等待恢复")
                    watchLateReadiness(next)
                }
            }
            Timber.i("proot supervisor exited")
        }
    }

    /**
     * 轮询直到 wrapper(22400) 与 WebUI(22267) 双双可达（1s 一拍）
     *
     * RUNNING 的语义必须是「WebUI 真的能服务」：gui.py 进程活着但 uvicorn 还在
     * import 的几秒里，WebView 自动重载会吃 connection refused 卡进错误页
     */
    private fun watchLateReadiness(proc: Process) {
        lateReadinessJob?.cancel()
        lateReadinessJob = scope.launch(MaaDispatchers.IO) {
            while (wantRunning && session === proc && proc.isAlive) {
                if (awaitServices(5_000L, proc)) {
                    if (wantRunning && session === proc && proc.isAlive) {
                        setState(ProotPhase.RUNNING)
                    }
                    return@launch
                }
                delay(1_000L)
            }
        }
    }

    private suspend fun awaitServices(timeoutMs: Long, proc: Process): Boolean {
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeoutMs)
        while (System.nanoTime() < deadline) {
            if (!wantRunning || session !== proc || !proc.isAlive) return false
            if (httpOk("http://127.0.0.1:$WRAPPER_PORT/status") &&
                httpOk("http://127.0.0.1:$WEBUI_PORT/")
            ) {
                return wantRunning && session === proc && proc.isAlive
            }
            delay(1_000)
        }
        return false
    }

    private fun httpOk(url: String): Boolean = runCatching {
        val conn = URL(url).openConnection(java.net.Proxy.NO_PROXY) as HttpURLConnection
        try {
            conn.connectTimeout = 800
            conn.readTimeout = 800
            conn.responseCode == 200
        } finally {
            conn.disconnect()
        }
    }.getOrDefault(false)

    // ------------------------------------------------------------------ 一次性 proot 执行

    data class ExecResult(
        val exit: Int?,
        val output: String,
        val timedOut: Boolean,
    )

    /** 带默认环境的 [runGuestRaw]（seed 用） */
    private suspend fun runGuest(
        guestCmd: List<String>,
        timeoutMs: Long,
        extraEnv: Map<String, String> = emptyMap(),
    ): ExecResult? = try {
        runGuestRaw(guestCmd, timeoutMs, extraEnv)
    } catch (cancelled: CancellationException) {
        throw cancelled
    } catch (error: Exception) {
        Timber.w("guest exec failed: %s", error.javaClass.simpleName)
        null
    }

    /** 一次性 proot 执行：合并 stderr，限时强杀；输出整体回收（更新脚本的 verdict 在里面） */
    private suspend fun runGuestRaw(
        guestCmd: List<String>,
        timeoutMs: Long,
        extraEnv: Map<String, String> = emptyMap(),
    ): ExecResult = withContext(MaaDispatchers.IO) {
        prootTmpDir.mkdirs()
        check(prootShmDir.isDirectory || prootShmDir.mkdirs()) { "Cannot prepare proot shared memory" }
        val cmd = listOf(
            prootFile.absolutePath,
            "--link2symlink",
            "-w", GUEST_ALAS_ROOT,
            "-r", rootfsDir.absolutePath,
            "-b", "/dev:/dev", "-b", "/proc:/proc", "-b", "/sys:/sys",
            "-b", "${prootShmDir.absolutePath}:/dev/shm",
        ) + guestCmd
        val proc = ProcessBuilder(cmd)
            .directory(alasDir)
            .redirectErrorStream(true)
            .apply { environment().remove("LD_PRELOAD"); environment().putAll(baseEnv()); environment().putAll(extraEnv) }
            .start()
        val out = BoundedOutput()
        val reader = Thread {
            runCatching {
                proc.inputStream.reader().use { input ->
                    val chars = CharArray(4096)
                    while (true) {
                        val count = input.read(chars)
                        if (count < 0) break
                        out.append(chars, count)
                    }
                }
            }
        }.apply { isDaemon = true; name = "proot-exec-reader" }
        reader.start()
        try {
            val finished = runInterruptible { proc.waitFor(timeoutMs, TimeUnit.MILLISECONDS) }
            if (!finished) proc.destroyForcibly()
            reader.join(2_000)
            ExecResult(if (finished) proc.exitValue() else null, out.snapshot(), !finished)
        } finally {
            if (proc.isAlive) proc.destroyForcibly()
            runCatching { proc.inputStream.close() }
        }
    }

    // ------------------------------------------------------------------ 自愈清理与 DNS

    /** 自愈清锁：proot 临时目录整体重来 + git 锁 + reloadalas（会话不在跑时才可调） */
    private suspend fun cleanupStale() {
        runCatching {
            prootTmpDir.deleteRecursively()
            prootTmpDir.mkdirs()
        }.onFailure { Timber.w(it, "cleanup proot-tmp failed") }
        runCatching { File(alasDir, "config/reloadalas").delete() }
        runCatching {
            val gitDir = File(alasDir, ".git")
            if (gitDir.isDirectory) {
                gitDir.walkTopDown().filter { it.isFile && it.name.endsWith(".lock") }
                    .forEach { it.delete() }
            }
        }.onFailure { Timber.w(it, "cleanup git locks failed") }
        truncateSessionLogIfStale()
    }

    /**
     * session.log 截尾：mtime 超 7 天且体积超上限时只留最后 [SESSION_LOG_KEEP_BYTES]
     *
     * 放在这里做是因为 startLocked 每次启动必经、且早于 spawnSession——此刻没有
     * drain 线程在写，无竞争；截断会刷新 mtime，崩溃重拉循环里不会再重复截
     */
    private suspend fun truncateSessionLogIfStale() {
        // 设置读盘是异步的：最多等一拍，等不到就本次跳过（下轮启动再判），不卡启动链
        val loaded = withTimeoutOrNull(SETTINGS_LOADED_WAIT_MS) {
            settings.loaded.first { it }
            true
        } ?: false
        if (!loaded || !settings.autoCleanLogs.value) return

        val file = sessionLog
        val length = file.length()
        if (!file.isFile || length <= SESSION_LOG_KEEP_BYTES) return
        if (System.currentTimeMillis() - file.lastModified() < SESSION_LOG_STALE_MS) return

        runCatching {
            RandomAccessFile(file, "rw").use { raf ->
                val tail = ByteArray(SESSION_LOG_KEEP_BYTES.toInt())
                raf.seek(length - tail.size)
                raf.readFully(tail)
                // 切口多半落在半行/半个 UTF-8 字符上：从第一个换行之后开始留
                val firstNewline = tail.indexOf('\n'.code.toByte())
                val body = if (firstNewline in 0 until tail.size - 1) {
                    tail.copyOfRange(firstNewline + 1, tail.size)
                } else {
                    tail
                }
                val marker = buildString {
                    append("[host] ")
                    synchronized(sessionLogLock) { append(phaseTs.format(java.util.Date())) }
                    append(" TRUNCATED 过期 session.log，仅保留尾部 ")
                    append(SESSION_LOG_KEEP_BYTES / 1024 / 1024).append("MB\n")
                }.toByteArray()
                raf.setLength(0)
                raf.write(marker)
                raf.write(body)
            }
            Timber.w("session.log 过期且超 %dMB，已截尾（原 %dKB）", SESSION_LOG_KEEP_BYTES / 1024 / 1024, length / 1024)
        }.onFailure { Timber.w(it, "session.log 截尾失败") }
    }

    /**
     * 写死 DNS：烘焙包里的 /etc/resolv.conf 是指向 /run/systemd 的悬空软链，
     * 设备上解析必挂（热更新需要网络）。写普通文件， mainland 默认 AliDNS
     */
    private fun writeResolvConf() {
        runCatching {
            val f = File(rootfsDir, "etc/resolv.conf")
            if (Files.isSymbolicLink(f.toPath()) || f.exists()) f.delete()
            f.writeText("nameserver 223.5.5.5\nnameserver 223.6.6.6\n")
        }.onFailure { Timber.w(it, "write resolv.conf failed") }
    }

    // ------------------------------------------------------------------ 状态

    private val sessionLogLock = Any()
    private val phaseTs = java.text.SimpleDateFormat("MM-dd HH:mm:ss", java.util.Locale.US)

    private fun beginStep(step: StartupStep, phase: ProotPhase, detail: String) {
        _state.update { it.copy(phase = phase, detail = detail, startup = it.startup.begin(step), transfer = null) }
        logPhase(step.name, detail)
    }

    private fun finishStep(status: StepStatus) {
        _state.update { it.copy(startup = it.startup.finish(status)) }
    }

    private fun setState(phase: ProotPhase, detail: String = "") {
        _state.update {
            it.copy(phase = phase, detail = detail,
                startup = if (phase == ProotPhase.RUNNING) it.startup.finish(StepStatus.DONE) else it.startup)
        }
        logPhase(phase.name, detail)
    }

    /**
     * 阶段迁移落盘 session.log：release 版 Timber 只落 W+，准备链 2~5 分钟全程静默
     * 曾致「App 假死」误判——session.log 本就是生命周期时间线，[host] 行与 [proot-out] 交错即全貌
     */
    private fun logPhase(tag: String, detail: String) {
        runCatching {
            sessionLog.parentFile?.mkdirs()
            val line = buildString {
                append("[host] ")
                synchronized(sessionLogLock) { append(phaseTs.format(java.util.Date())) }
                append(' ').append(tag)
                if (detail.isNotEmpty()) append(' ').append(detail)
                append('\n')
            }
            java.io.FileOutputStream(sessionLog, true).use { it.write(line.toByteArray()) }
        }
    }

    private fun fail(reason: String) {
        Timber.e("ProotHost failed: %s", reason)
        _state.update { it.copy(phase = ProotPhase.FAILED, detail = reason, startup = it.startup.finish(StepStatus.FAILED)) }
        logPhase("FAILED", reason)
    }

    companion object {
        /** wrapper 薄 HTTP（与 rootfs wrapper.py 的 PORT 对齐；避开桥 22300 与 WebUI 22267） */
        const val WRAPPER_PORT = 22400

        /** WebUI 端口（deploy.yaml WebuiPort；AlasScreen 与外部浏览器都打它） */
        const val WEBUI_PORT = 22267

        private const val GUEST_ALAS_ROOT = "/opt/alas"
        private const val SERVICES_UP_MS = 90_000L
        private const val SHORT_EXEC_MS = 60_000L
        private const val ENV_FIX_TIMEOUT_MS = 300_000L

        /** 首启从 pack 物化整树：9367 文件 ~130MB 在 proot 下写入，慢设备留足余量 */
        private const val RESTORE_TIMEOUT_MS = 600_000L

        /** 还原完成 marker（/opt/alas 内 untracked 文件，由 alasaos_restore.sh 落） */
        private const val RESTORE_MARKER = ".alasaos_restored"

        /** 剥离工作区时写入；优先于旧的完成标记 */
        private const val RESTORE_PENDING = ".alasaos_restore_pending"

        /** 还原前从当前 APK 刷新的 AOS 自有脚本 */
        private const val RESTORE_SEED = "seeds/alasaos_restore.sh"

        private const val REGEN_ARGS_TIMEOUT_MS = 360_000L
        private const val STOP_GRACE_MS = 8_000L
        private const val RESTART_BACKOFF_INIT_MS = 3_000L
        private const val RESTART_BACKOFF_MAX_MS = 60_000L
        private const val QUICK_DEATH_MS = 10_000L
        private const val MAX_RAPID_DEATHS = 5

        /** session.log 截尾：保留尾部 2MB；mtime 超 7 天才算过期 */
        private const val SESSION_LOG_KEEP_BYTES = 2L * 1024 * 1024
        private const val SESSION_LOG_STALE_MS = 7L * 24 * 60 * 60 * 1000
        private const val SETTINGS_LOADED_WAIT_MS = 2_000L
    }
}
