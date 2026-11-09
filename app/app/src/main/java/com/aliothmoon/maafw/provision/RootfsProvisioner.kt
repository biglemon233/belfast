package com.aliothmoon.maafw.provision

import android.app.Application
import android.os.Build
import android.system.Os
import com.aliothmoon.maafw.BuildConfig
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.apache.commons.compress.archivers.tar.TarArchiveEntry
import org.apache.commons.compress.archivers.tar.TarArchiveInputStream
import org.tukaani.xz.XZInputStream
import timber.log.Timber
import java.io.File
import java.io.FilterInputStream
import java.io.IOException
import java.io.InputStream
import java.util.concurrent.atomic.AtomicBoolean

/** 首启 rootfs 部署状态机 */
sealed interface ProvisionState {
    /** 刚启动，正在比对内置版本与已装版本 */
    data object Checking : ProvisionState

    /** 安装包未内置 rootfs.tar.xz：lite 构建等待用户选源下载（开发构建可跳过走 /data/local/tmp 通道） */
    data object NotBundled : ProvisionState

    /** lite 下载中；totalBytes=0 表示服务器没给 Content-Length（进度条转不确定态） */
    data class Downloading(val doneBytes: Long, val totalBytes: Long, val bytesPerSecond: Long = 0) : ProvisionState

    /** 磁盘余量不足（最低要求：≥2GB） */
    data class LowDisk(val freeBytes: Long) : ProvisionState

    /** 解压中；进度按压缩字节读数 / 资产总长（流式解压拿不到的解压后总量不用） */
    data class Extracting(val doneBytes: Long, val totalBytes: Long, val bytesPerSecond: Long = 0) : ProvisionState

    data object Ready : ProvisionState

    data class Failed(val reason: String) : ProvisionState
}

/**
 * 首启解压流水线：assets 的 rootfs.tar.xz（完整版）或用户下载的 rootfs.tar.xz（lite）→ 内部存储 files/rootfs
 *
 * - **必须内部 filesDir**：/sdcard 模拟存储不支持符号链接（ubuntu-base 有 740 个），
 *   且 noexec；内部 filesDir 可供 proot 使用（targetSdk 35）。
 * - 版本闸门：包侧 `BUILD_MANIFEST`（完整版=assets，lite=下载落盘）与 marker
 *   `files/rootfs/.provisioned` 对版本号；不一致（或 python3 sanity 不过）就重解。升级=换新包重解，不做增量。
 * - 落盘走 `rootfs.tmp` 解完再换名，半途失败不留半拉子正式目录。
 * - lite（未内置构建）：已装（完整版覆盖安装）→ 直接 Ready；否则进 [ProvisionState.NotBundled]
 *   等用户选源，[startDownload] 下载完走与资产解压同一条 extract→sanity→marker 流水线。
 * - busybox tar 解 ubuntu-base 硬链接前向引用必炸（M1-d 坑②），故用纯 Java
 *   commons-compress + tukaani xz 流式解；硬链接物化成副本，符号链接走 [Os.symlink]。
 */
class RootfsProvisioner(
    private val app: Application,
    private val scope: CoroutineScope,
    private val selectedRuntimeAbi: () -> String = { "" },
) {

    private val _state = MutableStateFlow<ProvisionState>(ProvisionState.Checking)
    val state: StateFlow<ProvisionState> = _state.asStateFlow()

    private val running = AtomicBoolean(false)

    private val rootDir: File get() = File(app.filesDir, "rootfs")
    private val tmpDir: File get() = File(app.filesDir, "rootfs.tmp")
    private val downloadDir: File get() = File(app.filesDir, "rootfs-download")
    private val markerFile: File get() = File(rootDir, MARKER_NAME)
    val runtimeAbi: String get() = RuntimeArchitecture.runtimeAbi(BuildConfig.RUNTIME_ABI,
        RuntimeArchitecture.deviceAbi(File("/system/bin/app_process64"), Build.SUPPORTED_ABIS.toList()), selectedRuntimeAbi())

    // lazy：构造期不碰 app.filesDir（单测塞 relaxed mock 时 File(mock, child) 会 NPE）
    private val downloader: RootfsDownloader by lazy { RootfsDownloader(downloadDir) }

    fun start() {
        if (running.compareAndSet(false, true)) {
            scope.launch { run() }
        }
    }

    /** 失败/低磁盘/版本过期后手动重跑 */
    fun retry() = start()

    /** 已装版本（marker 内容），设置页/诊断展示用 */
    fun installedVersion(): String? =
        runCatching { markerFile.takeIf { it.isFile }?.readText()?.trim() }.getOrNull()

    private suspend fun run() = withContext(Dispatchers.IO) {
        _state.value = ProvisionState.Checking
        cleanupTrashDirs()
        var expectedVersion: String? = null
        try {
            val bundled = bundledVersion()
            expectedVersion = bundled
            if (bundled == null) {
                // lite 构建：覆盖安装到已有完整版数据的设备直接复用已装环境
                if (isInstalledAny() && runCatching { validateArchitecture(rootDir) }.isSuccess) {
                    Timber.i("rootfs already provisioned, lite build reuses it")
                    _state.value = ProvisionState.Ready
                    return@withContext
                }
                // 上次下载完成后解压失败的重试：不再问源，直接续解压（tmpDir 有半成品也一并复用）
                downloadedPackage()?.let { (version, archive) ->
                    expectedVersion = version
                    Timber.i("resuming extraction of downloaded rootfs $version")
                    val ready = try {
                        extractAndFinalize(version, ArchiveInput.Local(archive), skipDiskGate = isExtractComplete(tmpDir, version))
                    } catch (e: Exception) {
                        // 包解不开多半是包本身坏（镜像截断等）：弃包，重试回下载源选择，避免死循环
                        Timber.e(e, "extracting downloaded rootfs failed, discarding package")
                        if (!isExtractComplete(tmpDir, version)) runCatching { downloader.clear() }
                        throw e
                    }
                    if (ready) downloader.clear()
                    return@withContext
                }
                val free = app.filesDir.let { it.mkdirs(); it.usableSpace }
                if (free < MIN_FREE_BYTES) {
                    Timber.w("low disk: ${free / 1_000_000}MB free, need ${MIN_FREE_BYTES / 1_000_000}MB")
                    _state.value = ProvisionState.LowDisk(free)
                    return@withContext
                }
                Timber.w("rootfs archive not bundled in this build, waiting for user download")
                _state.value = ProvisionState.NotBundled
                return@withContext
            }
            if (isInstalled(bundled)) {
                Timber.i("rootfs $bundled already provisioned")
                _state.value = ProvisionState.Ready
                return@withContext
            }

            val alreadyExtracted = isExtractComplete(tmpDir, bundled)
            val free = app.filesDir.let { it.mkdirs(); it.usableSpace }
            if (!alreadyExtracted && free < MIN_FREE_BYTES) {
                Timber.w("low disk: ${free / 1_000_000}MB free, need ${MIN_FREE_BYTES / 1_000_000}MB")
                _state.value = ProvisionState.LowDisk(free)
                return@withContext
            }

            extractAndFinalize(bundled, ArchiveInput.Bundled(app), skipDiskGate = alreadyExtracted)
        } catch (e: Exception) {
            Timber.e(e, "rootfs provision failed")
            // 保护：若 tmpDir 已经完整解压完毕，保留以供重试秒级换名，不白费几十秒
            val retainedVersion = expectedVersion
            if (retainedVersion == null || !isExtractComplete(tmpDir, retainedVersion)) {
                tmpDir.deleteRecursively()
            }
            _state.value = ProvisionState.Failed(e.message ?: e.javaClass.simpleName)
        } finally {
            running.set(false)
        }
    }

    /** 用户选定下载源后触发（lite 构建）；下载产物走与内置资产同一条 extract→sanity→marker 流水线 */
    fun startDownload(source: RootfsDownloadSource) {
        if (running.compareAndSet(false, true)) {
            scope.launch { runDownload(source) }
        }
    }

    private suspend fun runDownload(source: RootfsDownloadSource) = withContext(Dispatchers.IO) {
        _state.value = ProvisionState.Checking
        cleanupTrashDirs()
        var downloadedVersion: String? = null
        try {
            val candidates = source.candidateUrls(source.abi ?: runtimeAbi)
            val usable = candidates.filter { RootfsSourceValidator.validate(it) is RootfsSourceValidator.Result.Valid }
            if (usable.isEmpty()) {
                // UI 层已先拦非法源，这里是直调兜底；镜像个别非法不拦，合法子集照常尝试
                _state.value = ProvisionState.Failed("download source URL is invalid")
                return@withContext
            }

            // 下载 + 解压都吃 filesDir，同一条 ≥2GB 硬校验
            val free = app.filesDir.let { it.mkdirs(); it.usableSpace }
            if (free < MIN_FREE_BYTES) {
                Timber.w("low disk: ${free / 1_000_000}MB free, need ${MIN_FREE_BYTES / 1_000_000}MB")
                _state.value = ProvisionState.LowDisk(free)
                return@withContext
            }

            downloader.clear()
            // A mirror can serve a stale manifest: retry the entire pair on one source.
            var downloaded: Pair<String, File>? = null
            var lastError: Exception? = null
            for (url in usable) {
                try {
                    downloader.clear()
                    val manifest = downloader.download(MANIFEST_NAME, listOf(RootfsSource.manifestUrl(url))) { }
                    val selectedAbi = source.abi ?: runtimeAbi
                    RuntimeArchitecture.requireCompatible(selectedAbi, listOf(selectedAbi), manifest.readText())
                    val version = parseVersion(manifest.readText())
                        ?: throw IOException("BUILD_MANIFEST missing rootfs_version")
                    val archive = downloader.download(ARCHIVE_NAME, listOf(url)) { progress ->
                        _state.value = ProvisionState.Downloading(progress.doneBytes, progress.totalBytes, progress.bytesPerSecond)
                    }
                    checkXzMagic(archive)
                    downloaded = version to archive
                    break
                } catch (e: IOException) {
                    lastError = e
                }
            }
            val (version, archive) = downloaded ?: throw (lastError ?: IOException("all download sources failed"))
            downloadedVersion = version
            Timber.i("downloaded rootfs $version, starting extraction")
            extractAndFinalize(version, ArchiveInput.Local(archive), skipDiskGate = isExtractComplete(tmpDir, version))
                .also { ready -> if (ready) scope.launch(Dispatchers.IO) { runCatching { downloader.clear() } } }
        } catch (e: Exception) {
            Timber.e(e, "rootfs download provision failed")
            // 新下载的包解不开多半是包本身坏（镜像截断、假直链等）：弃包，重试回下载源选择
            val retainedVersion = downloadedVersion
            if (retainedVersion == null || !isExtractComplete(tmpDir, retainedVersion)) {
                runCatching { downloader.clear() }
            }
            _state.value = ProvisionState.Failed(e.message ?: e.javaClass.simpleName)
        } finally {
            running.set(false)
        }
    }

    /** 解压 + sanity + marker + Ready；内置资产与下载包共用这最后一公里。返回是否完成部署 */
    private fun extractAndFinalize(version: String, archive: ArchiveInput, skipDiskGate: Boolean): Boolean {
        if (!skipDiskGate) {
            val free = app.filesDir.let { it.mkdirs(); it.usableSpace }
            if (free < MIN_FREE_BYTES) {
                Timber.w("low disk: ${free / 1_000_000}MB free, need ${MIN_FREE_BYTES / 1_000_000}MB")
                _state.value = ProvisionState.LowDisk(free)
                return false
            }
        }
        extract(version, archive)
        validateArchitecture(rootDir)

        // 解完 sanity：解释器在且可执行，版本以镜像内清单为准
        val python = File(rootDir, PYTHON_REL)
        check(python.isFile && python.canExecute()) { "$PYTHON_REL missing or not executable" }
        val installed = readExtractedVersion(rootDir) ?: version
        markerFile.writeText(installed)
        File(rootDir, COMPLETE_MARKER).delete()
        Timber.i("rootfs $installed provisioned")
        _state.value = ProvisionState.Ready
        return true
    }

    /** lite 覆盖安装：不比版本号，marker 在且 python3 可执行就当已装 */
    private fun isInstalledAny(): Boolean =
        markerFile.isFile && File(rootDir, PYTHON_REL).let { it.isFile && it.canExecute() }

    /** 下载目录里是否有下载完成的完整包（重试续解压入口；非 xz 的残次品一律视为没有） */
    internal fun downloadedPackage(): Pair<String, File>? {
        if (!runCatching {
                RuntimeArchitecture.requireCompatible(runtimeAbi, listOf(runtimeAbi), File(downloadDir, MANIFEST_NAME).readText())
            }.isSuccess) return null
        return downloadedPackage(downloadDir)
    }

    internal fun downloadedPackage(dir: File): Pair<String, File>? {
        val manifest = File(dir, MANIFEST_NAME)
        val archive = File(dir, ARCHIVE_NAME)
        if (!manifest.isFile || !archive.isFile || archive.length() <= 0L) return null
        if (!hasXzMagic(archive)) return null
        val version = parseVersion(runCatching { manifest.readText() }.getOrNull() ?: return null) ?: return null
        return version to archive
    }

    private fun checkXzMagic(file: File) {
        if (!hasXzMagic(file)) throw IOException("downloaded file is not a xz archive: ${file.name}")
    }

    private fun hasXzMagic(file: File): Boolean = runCatching {
        file.inputStream().use { input ->
            val head = ByteArray(XZ_MAGIC.size)
            var off = 0
            while (off < head.size) {
                val n = input.read(head, off, head.size - off)
                if (n < 0) break
                off += n
            }
            off == XZ_MAGIC.size && head.contentEquals(XZ_MAGIC)
        }
    }.getOrDefault(false)

    private fun isInstalled(bundled: String): Boolean =
        installedVersion() == bundled &&
                File(rootDir, PYTHON_REL).let { it.isFile && it.canExecute() } &&
                runCatching { validateArchitecture(rootDir) }.isSuccess

    internal fun isExtractComplete(dir: File, expectedVersion: String): Boolean =
        File(dir, COMPLETE_MARKER).isFile &&
                File(dir, PYTHON_REL).let { it.isFile && it.canExecute() } &&
                readExtractedVersion(dir) == expectedVersion

    /** 内置包版本；资产缺任一件（清单或压缩包）都视为未内置 */
    private fun bundledVersion(): String? {
        val manifest = runCatching {
            app.assets.open(ASSET_ARCHIVE).use { }
            app.assets.open(ASSET_MANIFEST).bufferedReader().use { it.readText() }
        }.getOrNull() ?: return null
        RuntimeArchitecture.requireCompatible(runtimeAbi, listOf(runtimeAbi), manifest)
        return parseVersion(manifest) ?: error("BUILD_MANIFEST missing rootfs_version")
    }

    private fun validateArchitecture(dir: File) {
        RuntimeArchitecture.requireCompatible(runtimeAbi, listOf(runtimeAbi),
            File(dir, MANIFEST_REL).readText(), File(dir, PYTHON_REL))
    }

    internal fun readExtractedVersion(dir: File = rootDir): String? = runCatching {
        parseVersion(File(dir, MANIFEST_REL).readText())
    }.getOrNull()

    private fun parseVersion(manifestJson: String): String? =
        VERSION_KEY.find(manifestJson)?.groupValues?.get(1)

    // ── 解压 ──

    /** 解压输入：内置资产流或下载落盘的本地文件，同一套流式解压管线消费 */
    private sealed interface ArchiveInput {
        /** 返回 (输入流, 进度分母总长)；流由解压侧 use 链关闭（一路带到资产 fd） */
        fun open(): Pair<InputStream, Long>

        class Bundled(private val app: Application) : ArchiveInput {
            override fun open(): Pair<InputStream, Long> {
                // openFd 拿未压缩资产的真实长度做进度分母（noCompress "xz" 已配）
                val afd = app.assets.openFd(ASSET_ARCHIVE)
                return afd.createInputStream().buffered(BUFFER_SIZE) to afd.length
            }
        }

        class Local(private val file: File) : ArchiveInput {
            override fun open(): Pair<InputStream, Long> = file.inputStream().buffered(BUFFER_SIZE) to file.length()
        }
    }

    private fun extract(version: String, archive: ArchiveInput) {
        killResidualProcesses()

        if (isExtractComplete(tmpDir, version) && runCatching { validateArchitecture(tmpDir) }.isSuccess) {
            Timber.i("Valid extracted rootfs found in $tmpDir (version $version), skipping extraction")
        } else {
            tmpDir.deleteRecursively()
            check(tmpDir.mkdirs()) { "cannot create $tmpDir" }

            val (input, total) = archive.open()
            val startedAt = System.nanoTime()
            val counting = CountingInputStream(input) { read ->
                val seconds = ((System.nanoTime() - startedAt) / 1_000_000_000.0).coerceAtLeast(0.001)
                _state.value = ProvisionState.Extracting(read, total, (read / seconds).toLong())
            }
            val extracted = mutableMapOf<String, File>()
            val deferredLinks = mutableListOf<Pair<String, String>>()

            TarArchiveInputStream(XZInputStream(counting, -1)).use { tar ->
                generateSequence { tar.nextEntry }.forEach { entry ->
                    extractEntry(tar, entry, extracted, deferredLinks)
                }
            }

            // 硬链接前向引用兜底：解完全量后目标必然在（否则包本身坏）
            for ((name, linkName) in deferredLinks) {
                val src = File(tmpDir, linkName)
                check(src.isFile) { "hard link target missing: $linkName (for $name)" }
                src.copyTo(File(tmpDir, name), overwrite = true)
            }

            check(File(tmpDir, PYTHON_REL).let { it.isFile && it.canExecute() }) {
                "$PYTHON_REL missing or not executable in extracted rootfs"
            }
            check(readExtractedVersion(tmpDir) == version) { "archive and BUILD_MANIFEST versions differ" }
            validateArchitecture(tmpDir)
            File(tmpDir, COMPLETE_MARKER).createNewFile()
        }

        // 迁移配置并原子替换
        atomicSwapRootfs()
    }

    private fun atomicSwapRootfs() {
        killResidualProcesses()
        runCatching { Thread.sleep(50) }

        if (rootDir.exists()) {
            backupUserConfigs(rootDir, tmpDir)
            val trashDir = File(app.filesDir, "$TRASH_PREFIX${System.currentTimeMillis()}")
            check(rootDir.renameTo(trashDir)) { "rename $rootDir -> $trashDir failed" }
            val swapped = tmpDir.renameTo(rootDir)
            if (!swapped) {
                if (trashDir.exists() && !rootDir.exists()) {
                    trashDir.renameTo(rootDir)
                }
                error("rename $tmpDir -> $rootDir failed")
            }
            cleanupTrashDirs()
        } else {
            check(tmpDir.renameTo(rootDir)) { "rename $tmpDir -> $rootDir failed" }
        }
    }

    /** 跨版本升级时迁移保留用户 ALAS 配置与实例 */
    internal fun backupUserConfigs(srcDir: File, dstDir: File) {
        val srcConfig = File(srcDir, "opt/alas/config")
        val dstConfig = File(dstDir, "opt/alas/config")
        if (!srcConfig.isDirectory) return
        dstConfig.mkdirs()
        val configs = srcConfig.listFiles() ?: throw IOException("cannot read user configuration directory")
        configs.forEach { file ->
            if (!file.isFile) return@forEach
            val name = file.name
            val isUserConfig = (name.endsWith(".json") && !name.startsWith("template.")) ||
                    name == "deploy.yaml" ||
                    name.startsWith(".alasaos")
            if (isUserConfig) {
                file.copyTo(File(dstConfig, name), overwrite = true)
                Timber.i("Preserved user config during provision: $name")
            }
        }
    }

    /** 终结同 UID 下遗留的孤儿进程（proot/python 等），防止写文件导致换名/删除锁死 */
    private fun killResidualProcesses() {
        runCatching {
            val myUid = android.os.Process.myUid()
            val myPid = android.os.Process.myPid()
            val procDir = File("/proc")
            val pids = procDir.listFiles()?.mapNotNull { it.name.toIntOrNull() } ?: return
            var killedCount = 0
            for (pid in pids) {
                if (pid == myPid) continue
                runCatching {
                    val statusFile = File("/proc/$pid/status")
                    if (statusFile.isFile) {
                        val isSameUid = statusFile.useLines { lines ->
                            lines.firstOrNull { it.startsWith("Uid:") }
                                ?.split(Regex("\\s+"))
                                ?.getOrNull(1)
                                ?.toIntOrNull() == myUid
                        }
                        if (isSameUid) {
                            Timber.w("Killing residual process before provision: pid=$pid")
                            android.os.Process.killProcess(pid)
                            killedCount++
                        }
                    }
                }
            }
            if (killedCount > 0) {
                Timber.i("Killed $killedCount residual processes before provision")
            }
        }.onFailure { Timber.w(it, "Failed to check or kill residual processes") }
    }

    /** 异步清理 rootfs.trash.* 历史残留，避免占用空间 */
    private fun cleanupTrashDirs() {
        scope.launch(Dispatchers.IO) {
            val filesDir = app.filesDir ?: return@launch
            filesDir.listFiles { file -> file.isDirectory && file.name.startsWith(TRASH_PREFIX) }
                ?.forEach { trash ->
                    runCatching {
                        trash.deleteRecursively()
                        Timber.i("Cleaned trash directory: ${trash.name}")
                    }.onFailure {
                        Timber.w(it, "Failed to delete trash directory: ${trash.name}")
                    }
                }
        }
    }

    private fun extractEntry(
        tar: TarArchiveInputStream,
        entry: TarArchiveEntry,
        extracted: MutableMap<String, File>,
        deferredLinks: MutableList<Pair<String, String>>,
    ) {
        val name = entry.name.removePrefix("./").trimEnd('/')
        if (name.isEmpty()) return
        val target = File(tmpDir, name)
        // zip-slip：条目必须落在解压根内
        if (target.canonicalPath != tmpDir.canonicalPath &&
            !target.canonicalPath.startsWith(tmpDir.canonicalPath + File.separator)
        ) {
            throw IOException("illegal entry path: $name")
        }

        when {
            entry.isDirectory -> target.mkdirs()

            entry.isSymbolicLink -> {
                target.parentFile?.mkdirs()
                target.delete()
                Os.symlink(entry.linkName, target.absolutePath)
            }

            entry.isLink -> {
                // 硬链接物化成副本：目标已解出直接拷，前向引用登记后补
                val linkName = entry.linkName.removePrefix("./")
                val src = extracted[linkName]
                if (src != null && src.isFile) {
                    target.parentFile?.mkdirs()
                    src.copyTo(target, overwrite = true)
                } else {
                    deferredLinks += name to linkName
                }
            }

            entry.isFile -> {
                target.parentFile?.mkdirs()
                target.outputStream().buffered(BUFFER_SIZE).use { out ->
                    tar.copyTo(out, BUFFER_SIZE)
                }
                // 可执行位必须保：proot/python/busybox 全靠它（缺少可执行权限会导致 Permission denied）
                if (entry.mode and 0b001_001_001 != 0) target.setExecutable(true, false)
                extracted[name] = target
            }
        }
    }

    /** 每 256KB 才推一次状态，StateFlow 合流前刷太勤纯属白跑重组 */
    private class CountingInputStream(
        input: InputStream,
        private val onProgress: (Long) -> Unit,
    ) : FilterInputStream(input) {
        private var read = 0L
        private var sinceEmit = 0L

        override fun read(): Int = super.read().also { if (it >= 0) add(1) }

        override fun read(b: ByteArray, off: Int, len: Int): Int =
            super.read(b, off, len).also { if (it > 0) add(it.toLong()) }

        private fun add(n: Long) {
            read += n
            sinceEmit += n
            if (sinceEmit >= 256 * 1024) {
                sinceEmit = 0
                onProgress(read)
            }
        }
    }

    private companion object {
        const val ASSET_ARCHIVE = "rootfs/rootfs.tar.xz"
        const val ASSET_MANIFEST = "rootfs/BUILD_MANIFEST"
        const val ARCHIVE_NAME = "rootfs.tar.xz"
        const val MANIFEST_NAME = "BUILD_MANIFEST"
        const val MANIFEST_REL = "opt/alas/BUILD_MANIFEST"
        const val PYTHON_REL = "usr/bin/python3"
        const val MARKER_NAME = ".provisioned"
        const val COMPLETE_MARKER = ".extract_complete"
        const val TRASH_PREFIX = "rootfs.trash."
        const val MIN_FREE_BYTES = 2L * 1024 * 1024 * 1024
        const val BUFFER_SIZE = 256 * 1024
        /** xz 文件头 6 字节魔数；拦「自定义直链 200 回了一页 HTML」这类假包 */
        val XZ_MAGIC = byteArrayOf(0xFD.toByte(), 0x37, 0x7A, 0x58, 0x5A, 0x00)
        val VERSION_KEY = Regex(""""rootfs_version"\s*:\s*"([^"]+)"""")
    }
}
