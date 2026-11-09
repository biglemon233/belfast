package com.azurpilot.ghio.project

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.util.zip.ZipInputStream

/**
 * 在 Android 设备上验证打包 PI archive 可由 [android.content.res.AssetManager] 顺序读取。
 *
 * JVM 测试无法覆盖 AssetManager 的 APK 资产行为，完整解包由真实应用启动流程覆盖。未配置
 * `pi.profile` 的构建不含 PI 时用 JUnit assume 跳过，因此该测试只约束声明包含 PI 的 APK。
 *
 * Verifies on an Android device that the packaged PI archive can be read sequentially through
 * [android.content.res.AssetManager].
 *
 * JVM tests cannot cover APK asset behavior of AssetManager; the real app startup flow covers full
 * extraction. Builds without `pi.profile` contain no PI and are skipped by a JUnit assumption, so
 * this test constrains only APKs that declare PI inclusion.
 */
@RunWith(AndroidJUnit4::class)
class PiPackageInstrumentedTest {

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    /**
     * 打开 PI archive；当前 APK 未打包 PI 时跳过测试。
     *
     * 该 assume 区分“可选资产未包含”和“已声明资产损坏”，后者仍会使后续读取断言失败。
     *
     * Opens the PI archive, skipping when the current APK does not bundle PI.
     *
     * This assumption distinguishes an omitted optional asset from a broken declared asset; the
     * latter still fails subsequent read assertions.
     */
    private fun packageOrSkip(): PiPackage {
        val pkg = AssetPiPackage(context)
        assumeTrue("当前包未含 PI", pkg.openArchive() != null)
        return pkg
    }

    /** 验证 zip 可顺序遍历到必需的 `interface.json`。 / Verifies the zip can be traversed to required `interface.json`. */
    @Test
    fun archiveContainsInterfaceJson() {
        val pkg = packageOrSkip()
        val names = pkg.openArchive()!!.use { raw ->
            ZipInputStream(raw).use { zip ->
                buildList {
                    while (true) {
                        val entry = zip.nextEntry ?: break
                        if (!entry.isDirectory) add(entry.name.replace('\\', '/').trimStart('/'))
                        zip.closeEntry()
                    }
                }
            }
        }
        assertTrue("pi.zip 应含 interface.json: $names", "interface.json" in names)
    }

    /** 验证 `interface.json` 可读且带有版本字段。 / Verifies `interface.json` is readable and contains its version field. */
    @Test
    fun interfaceJsonReadable() {
        val pkg = packageOrSkip()
        val content = pkg.openArchive()!!.use { raw ->
            ZipInputStream(raw).use { zip ->
                while (true) {
                    val entry = zip.nextEntry ?: break
                    val name = entry.name.replace('\\', '/').trimStart('/')
                    if (name == "interface.json") {
                        return@use zip.bufferedReader().readText()
                    }
                    zip.closeEntry()
                }
                ""
            }
        }
        assertTrue(content.contains("interface_version"))
    }
}
