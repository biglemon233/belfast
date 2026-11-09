package com.azurpilot.ghio.privileged

import android.content.Intent
import android.graphics.Bitmap
import android.os.SystemClock
import android.os.ParcelFileDescriptor
import android.view.accessibility.AccessibilityNodeInfo
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.runner.lifecycle.ActivityLifecycleMonitorRegistry
import androidx.test.runner.lifecycle.Stage
import com.azurpilot.ghio.R
import com.azurpilot.ghio.settings.AppSettingsManager
import java.io.File
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.koin.core.context.GlobalContext
import org.junit.Test
import org.junit.runner.RunWith

/**
 * 验证两种主题及明暗模式下的已激活页面与渐进式配对表单，并导出真机截图。
 *
 * Verifies the ready page and progressive pairing forms in both themes and color modes, exporting
 * device screenshots. Restores the user's appearance settings after the test.
 */
@RunWith(AndroidJUnit4::class)
class AdbActivationUiInstrumentedTest {
    @Test
    fun displaysReadyStateAndProgressiveFormsInBothThemes() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val context = instrumentation.targetContext
        // 先模拟用户打开主页面，避免 MIUI 把测试进程发起的 Activity 当作后台弹窗拒绝。
        ParcelFileDescriptor.AutoCloseInputStream(instrumentation.uiAutomation.executeShellCommand(
            "am start -n ${context.packageName}/com.azurpilot.ghio.MainActivity",
        )).use { it.bufferedReader().readText() }
        val foregroundDeadline = SystemClock.elapsedRealtime() + 10_000
        var opened = false
        while (!opened && SystemClock.elapsedRealtime() < foregroundDeadline) {
            instrumentation.runOnMainSync {
                val main = ActivityLifecycleMonitorRegistry.getInstance()
                    .getActivitiesInStage(Stage.RESUMED)
                    .filterIsInstance<com.azurpilot.ghio.MainActivity>().firstOrNull()
                if (main != null) {
                    main.startActivity(Intent(main, AdbActivationActivity::class.java))
                    opened = true
                }
            }
            if (!opened) SystemClock.sleep(100)
        }
        val manager = GlobalContext.get().get<AppSettingsManager>()
        val original = runBlocking { manager.settings.first() }
        try {
            assertTrue("Could not open activation page", opened)
            assertTrue("Device must be activated", runBlocking { EmbeddedAdbManager.activate() })
            for (theme in listOf("miuix", "material")) {
                for (mode in listOf("1", "2")) {
                    runBlocking { manager.setAppearance { it.copy(uiMode = theme, colorMode = mode) } }
                    waitForText(R.string.adb_ready)
                    waitForText(R.string.adb_return)
                    // 等主题重组及系统栏更新完成，避免把上一种主题的帧当作新截图。
                    SystemClock.sleep(600)
                    assertFalse(hasText(R.string.adb_pair_port))
                    assertFalse(hasText(R.string.adb_connect_port))
                    saveScreenshot("adb_${theme}_${if (mode == "1") "light" else "dark"}")

                    clickText(R.string.adb_pairing_title)
                    clickText(R.string.adb_manual_pair_title, scroll = true)
                    waitForText(R.string.adb_pair_port, scroll = true)
                    waitForText(R.string.adb_pair_start, scroll = true)
                    assertFalse("Empty pairing form must be disabled", enabledText(R.string.adb_pair_start))
                    saveScreenshot("adb_${theme}_${mode}_manual_pair")
                    clickText(R.string.adb_manual_pair_title, scroll = true, backwards = true)
                    clickText(R.string.adb_pairing_title, scroll = true, backwards = true)

                    clickText(R.string.adb_manual_connect_title, scroll = true)
                    waitForText(R.string.adb_connect_port, scroll = true)
                    waitForText(R.string.adb_connect, scroll = true)
                    assertFalse("Empty connection form must be disabled", enabledText(R.string.adb_connect))
                    clickText(R.string.adb_manual_connect_title, scroll = true, backwards = true)
                    scrollToTop()
                }
            }
        } finally {
            runBlocking { manager.setAppearance { original } }
            instrumentation.runOnMainSync {
                ActivityLifecycleMonitorRegistry.getInstance().getActivitiesInStage(Stage.RESUMED)
                    .filterIsInstance<AdbActivationActivity>().forEach { it.finish() }
            }
        }
    }

    private fun nodes(): List<AccessibilityNodeInfo> =
        InstrumentationRegistry.getInstrumentation().uiAutomation.rootInActiveWindow
            ?.let(::visibleNodes).orEmpty()

    private fun hasText(resource: Int): Boolean {
        val text = InstrumentationRegistry.getInstrumentation().targetContext.getString(resource)
        return nodes().any { it.text?.toString() == text }
    }

    private fun enabledText(resource: Int): Boolean {
        val text = InstrumentationRegistry.getInstrumentation().targetContext.getString(resource)
        var node = nodes().first { it.text?.toString() == text }
        while (!node.isClickable && node.parent != null) node = node.parent
        return node.isEnabled
    }

    private fun waitForText(resource: Int, scroll: Boolean = false, backwards: Boolean = false) {
        val deadline = SystemClock.elapsedRealtime() + 15_000
        while (SystemClock.elapsedRealtime() < deadline) {
            if (hasText(resource)) return
            if (scroll) scrollPage(backwards)
            SystemClock.sleep(150)
        }
        fail("Missing text: ${InstrumentationRegistry.getInstrumentation().targetContext.getString(resource)}")
    }

    private fun clickText(resource: Int, scroll: Boolean = false, backwards: Boolean = false) {
        waitForText(resource, scroll, backwards)
        val text = InstrumentationRegistry.getInstrumentation().targetContext.getString(resource)
        var node = nodes().first { it.text?.toString() == text }
        while (!node.isClickable && node.parent != null) node = node.parent
        assertTrue("Could not click $text", node.performAction(AccessibilityNodeInfo.ACTION_CLICK))
        SystemClock.sleep(200)
    }

    private fun scrollPage(backwards: Boolean): Boolean =
        nodes().firstOrNull { it.isScrollable }?.performAction(
            if (backwards) AccessibilityNodeInfo.ACTION_SCROLL_BACKWARD
            else AccessibilityNodeInfo.ACTION_SCROLL_FORWARD,
        ) ?: false

    private fun scrollToTop() {
        repeat(8) {
            if (!scrollPage(backwards = true)) return
            SystemClock.sleep(150)
        }
    }

    private fun saveScreenshot(name: String) {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val bitmap = checkNotNull(instrumentation.uiAutomation.takeScreenshot())
        val directory = checkNotNull(instrumentation.targetContext.getExternalFilesDir("ui-review"))
        File(directory, "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        bitmap.recycle()
    }

    private fun visibleNodes(root: AccessibilityNodeInfo): List<AccessibilityNodeInfo> = buildList {
        if (root.isVisibleToUser) add(root)
        repeat(root.childCount) { index -> root.getChild(index)?.let { addAll(visibleNodes(it)) } }
    }
}
