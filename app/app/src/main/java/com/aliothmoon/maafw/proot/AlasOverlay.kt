package com.aliothmoon.maafw.proot

import android.content.Context
import timber.log.Timber
import java.io.File

/**
 * App 资产 `alas/` 对 rootfs `/opt/alas` 的运行时覆盖
 *
 * 两份职责：
 * - wrapper/runner/seed/alasaos_update.sh 随 App 版本演进，改它们不必重烘焙 rootfs
 *   （rootfs.tar.xz 里那份只是兜底；每次启动都被这里盖成 App 当前版本）
 * - 热更新 `git reset --hard` 会把上游跟踪文件打回原版：patches（module/）
 *   与 in-proc OCR rpc.py 必须在 UPDATED 后重放
 *
 * 覆盖方式是按字节比对的幂等拷贝；STALE_FILES 在拷贝后删除——断代更名
 * （如 maaal→alasaos）留下的旧名文件只删不盖，防旧码被误加载。
 * 图片模板与 Button 元数据使用当前 ALAS 源的原版。
 */
class AlasOverlay(private val context: Context) {

    data class Result(val copied: Int, val skipped: Int, val failed: Int)

    /** 把全部映射铺到 [alasDir]；返回统计，failed>0 由调用方决定是否中止启动 */
    fun apply(alasDir: File): Result {
        var copied = 0
        var skipped = 0
        var failed = 0
        for ((assetRoot, targetRoot) in MAPPINGS) {
            val (c, s, f) = copyTree(assetRoot, File(alasDir, targetRoot))
            copied += c; skipped += s; failed += f
        }
        var removed = 0
        for (rel in STALE_FILES) {
            val stale = File(alasDir, rel)
            if (stale.isFile && stale.delete()) removed++
        }
        if (removed > 0) Timber.i("ALAS overlay stale removed: %d", removed)
        Timber.i("ALAS overlay applied: copied=%d skipped=%d failed=%d", copied, skipped, failed)
        return Result(copied, skipped, failed)
    }

    /** 递归铺一棵资产子树；AssetManager.list 对文件返回空数组，据此区分文件/目录 */
    private fun copyTree(assetPath: String, target: File): Triple<Int, Int, Int> {
        val children = context.assets.list(assetPath) ?: return Triple(0, 0, 1)
        if (children.isEmpty()) return copyFile(assetPath, target)
        var copied = 0
        var skipped = 0
        var failed = 0
        for (name in children) {
            val (c, s, f) = copyTree("$assetPath/$name", File(target, name))
            copied += c; skipped += s; failed += f
        }
        return Triple(copied, skipped, failed)
    }

    private fun copyFile(assetPath: String, target: File): Triple<Int, Int, Int> {
        return runCatching {
            val bytes = context.assets.open(assetPath).use { it.readBytes() }
            if (target.isFile && target.readBytes().contentEquals(bytes)) {
                return Triple(0, 1, 0)
            }
            target.parentFile?.mkdirs()
            target.writeBytes(bytes)
            if (assetPath.endsWith(".sh")) target.setExecutable(true, false)
            Triple(1, 0, 0)
        }.getOrElse {
            Timber.w(it, "overlay copy failed: %s", assetPath)
            Triple(0, 0, 1)
        }
    }

    private companion object {
        /** (资产子树 → /opt/alas 内目标子树) */
        val MAPPINGS = listOf(
            "alas/overlay" to "",
            "alas/patches/module" to "module",
        )

        /** 断代遗留的旧名文件（烘焙 tar 仍带 maaal 时代命名）：覆盖后删除，幂等 */
        val STALE_FILES = listOf(
            "seeds/assets_fix.py",
            "module/device/method/maaal.py",
            "seeds/maaal_update.sh",
            // AOS 自研 OCR 引擎（numpy azur_lane + PP-OCR）随换源 AlasToFox 退役：
            // 原版特调 OCR（cnocr+mxnet）回归后这些覆盖件/权重只删不盖。
            // 注意 module/ocr/rpc.py 是源码件（zerorpc 客户端），不在此列。
            "module/ocr/al_numpy.py",
            "models/ocr/azur_lane/weights.npz",
            "models/ocr/azur_lane/label_cn.txt",
        )
    }
}
