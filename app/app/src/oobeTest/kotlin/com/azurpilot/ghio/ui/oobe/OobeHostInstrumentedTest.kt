package com.azurpilot.ghio.ui.oobe

import android.graphics.Bitmap
import android.os.SystemClock
import android.os.ParcelFileDescriptor
import android.view.accessibility.AccessibilityNodeInfo
import android.accessibilityservice.AccessibilityServiceInfo
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.runner.lifecycle.ActivityLifecycleMonitorRegistry
import androidx.test.runner.lifecycle.Stage
import com.azurpilot.ghio.MainActivity
import com.azurpilot.ghio.R
import com.azurpilot.ghio.domain.RemoteBackend
import com.azurpilot.ghio.privileged.PermissionManager
import com.azurpilot.ghio.provision.RootfsProvisioner
import com.azurpilot.ghio.provision.ProvisionState
import com.azurpilot.ghio.privileged.AdbActivationActivity
import android.content.Intent
import com.azurpilot.ghio.i18n.AppLocales
import com.azurpilot.ghio.settings.AppSettingsManager
import java.io.File
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.koin.core.context.GlobalContext

/**
 * 从真实主 Activity 验证语言重建、激活页往返、完成后的部署交接与设置重开入口。
 * 测试恢复原始偏好与语言，并输出宿主页面截图。
 *
 * Verifies locale recreation, activation round trips, completion handoff, and reopening from
 * settings through the real main Activity. Restores preferences and locale, exporting host screenshots.
 */
@RunWith(AndroidJUnit4::class)
class OobeHostInstrumentedTest {
    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()
    private val context get() = instrumentation.targetContext

    @Test
    fun actualHostRetainsProgressAcrossLanguagesAndReopensFromSettings() {
        val settings = GlobalContext.get().get<AppSettingsManager>()
        val original = runBlocking { withTimeout(10_000) { settings.loaded.first { it } }; settings.settings.first() }
        val locale = AppLocales.currentTag()
        val testStyle = InstrumentationRegistry.getArguments().getString("oobeStyle") ?: "miuix"
        val testLocale = InstrumentationRegistry.getArguments().getString("oobeLocale") ?: "zh-CN"
        val serviceInfo = instrumentation.uiAutomation.serviceInfo
        serviceInfo.flags = serviceInfo.flags or AccessibilityServiceInfo.FLAG_RETRIEVE_INTERACTIVE_WINDOWS
        instrumentation.uiAutomation.serviceInfo = serviceInfo
        try {
            runBlocking {
                settings.setOobeCompleted(true)
                // 旧提示已确认仍须阅读新协议，不能把旧记录当作新版同意。
                settings.setCommunityNoticeAcknowledged(true)
                settings.setLegalNoticeVersion("")
                settings.setSkipShizukuCheck(true)
                settings.setCompatNoticeShown(true)
                settings.setStartupBackend(RemoteBackend.SHIZUKU)
                settings.setAppearance { it.copy(uiMode = testStyle, colorMode = "1") }
                // OOBE 宿主测试只验证部署交接；本地关闭端口使轻量包进入可重试失败态。
                settings.setGithubMirrorCustom("http://127.0.0.1:9/")
                settings.setGithubMirror("custom")
            }
            instrumentation.runOnMainSync { AppLocales.apply(null) }
            ParcelFileDescriptor.AutoCloseInputStream(instrumentation.uiAutomation.executeShellCommand(
                "am start -n ${context.packageName}/com.azurpilot.ghio.MainActivity",
            )).use { it.bufferedReader().readText() }
            await { currentActivity() is MainActivity }
            runBlocking { settings.setOobeCompleted(false) }
            await { hasProgress(1) }
            await { statusBarVisible() == false }
            instrumentation.runOnMainSync {
                val window = currentActivityOnMain()!!.window
                @Suppress("DEPRECATION")
                assertEquals(android.graphics.Color.TRANSPARENT, window.navigationBarColor)
                if (android.os.Build.VERSION.SDK_INT >= 29) assertFalse(window.isNavigationBarContrastEnforced)
            }
            SystemClock.sleep(2200)
            screenshot("host_${testStyle}_glow_welcome")
            if (InstrumentationRegistry.getArguments().getString("glowCapture") == "true") {
                fun shell(command: String) = ParcelFileDescriptor.AutoCloseInputStream(
                    instrumentation.uiAutomation.executeShellCommand(command),
                ).use { it.bufferedReader().readText() }
                shell("dumpsys gfxinfo ${context.packageName} reset")
                SystemClock.sleep(12_000)
                File(context.getExternalFilesDir("oobe-tests"), "glow_frame_stats.txt")
                    .writeText(shell("dumpsys gfxinfo ${context.packageName} framestats"))
            }
            SystemClock.sleep(1600)
            screenshot("host_${testStyle}_glow_welcome_later")
            click(R.string.oobe_action_next)
            for ((tag, label) in listOf("en" to "English", "ja" to "日本語",
                "zh-TW" to "繁體中文", "zh-CN" to "简体中文")) {
                clickLabel(label)
                await { AppLocales.currentTag() == tag && hasProgress(2) && statusBarVisible() == false }
                screenshot("host_language_$tag")
            }
            if (testLocale != "zh-CN") {
                instrumentation.runOnMainSync { AppLocales.apply(testLocale) }
                await { AppLocales.currentTag() == testLocale && hasProgress(2) }
            }
            click(R.string.oobe_action_next)
            click(R.string.oobe_welcome_guidelines_accept, scroll = true)
            click(R.string.oobe_action_next)
            SystemClock.sleep(1000)
            screenshot("host_${testStyle}_appearance_$testLocale")
            click(R.string.oobe_action_next)
            val permissions = GlobalContext.get().get<PermissionManager>()
            if (!permissions.state.value.isGranted(RemoteBackend.SHIZUKU) &&
                !permissions.state.value.shizukuAvailable) click(R.string.oobe_priv_action_pair, scroll = true)
            else instrumentation.runOnMainSync {
                currentActivityOnMain()!!.startActivity(Intent(context, AdbActivationActivity::class.java))
            }
            await { currentActivity()?.javaClass?.simpleName == "AdbActivationActivity" }
            instrumentation.runOnMainSync { currentActivityOnMain()?.finish() }
            await { hasProgress(5) }
            await { statusBarVisible() == false }
            val granted = permissions.state.value.isGranted(RemoteBackend.SHIZUKU)
            click(if (granted) R.string.oobe_action_next else R.string.oobe_action_skip)
            screenshot("host_${testStyle}_tutorial_$testLocale")
            click(R.string.oobe_action_next)
            assertTrue(hasText(if (granted) R.string.oobe_ready_title else R.string.oobe_ready_pending_title))
            screenshot("host_pending_summary")
            click(R.string.oobe_action_start)
            await { settings.oobeCompleted.value && settings.communityNoticeAcknowledged.value }
            assertEquals(com.azurpilot.ghio.settings.CURRENT_LEGAL_NOTICE_VERSION, settings.legalNoticeVersion.value)
            await { !hasProgress(7) }
            await { statusBarVisible() == true }
            val provisioner = GlobalContext.get().get<RootfsProvisioner>()
            await { provisioner.state.value is ProvisionState.Ready || hasText(R.string.provision_skip) }
            if (provisioner.state.value !is ProvisionState.Ready) click(R.string.provision_skip, scroll = true)
            screenshot("host_completed")
            click(R.string.nav_settings)
            click(R.string.settings_reopen_oobe, scroll = true)
            await { hasProgress(1) }
            await { statusBarVisible() == false }
            assertFalse("Preview must hide the settings action from accessibility",
                hasText(R.string.settings_reopen_oobe))
            screenshot("host_reopened")
            repeat(4) { click(R.string.oobe_action_next) }
            val previewGranted = permissions.state.value.isGranted(RemoteBackend.SHIZUKU)
            click(if (previewGranted) R.string.oobe_action_next else R.string.oobe_action_skip)
            click(R.string.oobe_action_next)
            click(R.string.oobe_action_start)
            await { !hasProgress(7) }
            click(R.string.settings_reopen_oobe, scroll = true)
            await { hasProgress(1) }
            assertFalse("A new preview must keep background settings inaccessible",
                hasText(R.string.settings_reopen_oobe))
            click(R.string.dialog_cancel)
            await { !hasProgress(1) }
            await { statusBarVisible() == true }
        } finally {
            runBlocking {
                settings.setAppearance { original }
                settings.setGithubMirrorCustom(original.githubMirrorCustom)
                settings.setGithubMirror(original.githubMirror)
                settings.setStartupBackend(RemoteBackend.valueOf(original.startupBackend))
                settings.setSkipShizukuCheck(original.skipShizukuCheck.toBoolean())
                settings.setCompatNoticeShown(original.compatNoticeShown.toBoolean())
                settings.setCommunityNoticeAcknowledged(original.communityNoticeAcknowledged.toBoolean())
                settings.setOobeCompleted(original.oobeCompleted.toBoolean())
                settings.setLegalNoticeVersion(original.legalNoticeVersion)
            }
            instrumentation.runOnMainSync { AppLocales.apply(locale) }
        }
    }

    private fun currentActivityOnMain() = ActivityLifecycleMonitorRegistry.getInstance()
        .getActivitiesInStage(Stage.RESUMED).firstOrNull()

    private fun currentActivity(): android.app.Activity? {
        var activity: android.app.Activity? = null
        instrumentation.runOnMainSync { activity = currentActivityOnMain() }
        return activity
    }

    private fun statusBarVisible(): Boolean? {
        var visible: Boolean? = null
        instrumentation.runOnMainSync {
            currentActivityOnMain()?.window?.decorView?.let { view ->
                visible = androidx.core.view.ViewCompat.getRootWindowInsets(view)
                    ?.isVisible(androidx.core.view.WindowInsetsCompat.Type.statusBars())
            }
        }
        return visible
    }

    private fun string(resource: Int, vararg args: Any) =
        (currentActivity() ?: context).getString(resource, *args)

    private fun nodes(): List<AccessibilityNodeInfo> {
        // Android 15 在语言重建后可能保留旧 Compose 节点的点击属性。
        if (android.os.Build.VERSION.SDK_INT >= 35) instrumentation.uiAutomation.clearCache()
        fun flatten(node: AccessibilityNodeInfo): List<AccessibilityNodeInfo> {
            if (!node.refresh()) return emptyList()
            return listOf(node) + (0 until node.childCount)
                .flatMap { node.getChild(it)?.let(::flatten).orEmpty() }
        }
        val roots = instrumentation.uiAutomation.windows.mapNotNull { it.root }
        return roots.flatMap(::flatten).filter { it.isVisibleToUser }
    }

    private fun hasText(resource: Int, vararg args: Any): Boolean {
        val expected = string(resource, *args)
        return nodes().any { it.text?.toString() == expected }
    }

    private fun await(predicate: () -> Boolean) {
        val deadline = SystemClock.elapsedRealtime() + 20_000
        while (SystemClock.elapsedRealtime() < deadline) {
            if (predicate()) return
            SystemClock.sleep(150)
        }
        screenshot("host_failure")
        fail("Missing expected host state; activity=${currentActivity()?.javaClass?.simpleName}; visible: ${nodes().mapNotNull { it.text?.toString() }.filter { it.isNotEmpty() }}")
    }

    private fun click(resource: Int, scroll: Boolean = false) {
        await {
            val label = string(resource)
            if (nodes().any { it.text?.toString() == label || it.contentDescription?.toString() == label }) true
            else {
                if (scroll) nodes().firstOrNull { it.isScrollable }?.performAction(AccessibilityNodeInfo.ACTION_SCROLL_FORWARD)
                false
            }
        }
        clickLabel(string(resource))
    }

    private fun clickLabel(label: String) {
        var action: AccessibilityNodeInfo? = null
        await {
            var node = nodes().firstOrNull { it.text?.toString() == label || it.contentDescription?.toString() == label }
            while (node != null && !node.isClickable) {
                node = node.parent?.also { it.refresh() }
            }
            action = node?.takeIf { it.isEnabled && it.isClickable }
            action != null
        }
        assertTrue("Cannot click host action: $label", action!!.performAction(AccessibilityNodeInfo.ACTION_CLICK))
        SystemClock.sleep(950)
    }

    private fun hasProgress(step: Int) = nodes().any {
        it.stateDescription?.toString() == string(R.string.oobe_progress_format, step, OobeStep.entries.size)
    }

    private fun screenshot(name: String) {
        val output = File(context.getExternalFilesDir("oobe-tests"), "$name.png")
        output.parentFile?.mkdirs()
        instrumentation.uiAutomation.takeScreenshot()!!.let { bitmap ->
            output.outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            bitmap.recycle()
        }
    }
}
