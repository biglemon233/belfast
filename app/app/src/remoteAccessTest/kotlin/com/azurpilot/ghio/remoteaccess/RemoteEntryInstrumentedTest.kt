package com.azurpilot.ghio.remoteaccess

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.azurpilot.ghio.MainActivity
import com.azurpilot.ghio.R
import com.azurpilot.ghio.proot.AzurPilotRepository
import com.azurpilot.ghio.proot.ProotHost
import com.azurpilot.ghio.provision.RootfsProvisioner
import com.azurpilot.ghio.settings.AppSettingsManager
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.koin.core.context.GlobalContext
import java.io.File
import kotlinx.coroutines.runBlocking

/**
 * 从真正的首次启动界面验证远程入口，防止孤立配对卡测试漏掉外层部署门。
 *
 * Tests remote access from the real first-launch activity, including outer provisioning gates.
 */
@RunWith(AndroidJUnit4::class)
class RemoteEntryInstrumentedTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()

    @Test fun freshInstallOpensRemoteWithoutLocalRuntime() {
        val args = InstrumentationRegistry.getArguments()
        assumeTrue("仅限独立全新安装的验收包", args.getString("freshRemoteEntry") == "true")
        val context = compose.activity
        val koin = GlobalContext.get()
        val settings = koin.get<AppSettingsManager>()
        val provisioner = koin.get<RootfsProvisioner>()
        val host = koin.get<ProotHost>()
        val repository = koin.get<AzurPilotRepository>()
        compose.waitUntil(15_000) { settings.loaded.value }
        assertFalse("验收必须从未完成向导开始", settings.oobeCompleted.value)
        assertFalse(provisioner.isInstalled())
        assertFalse(host.startRequested)

        val remote = context.getString(R.string.remote_pair_title)
        compose.onNode(hasText(remote) and hasClickAction()).assertIsDisplayed().performClick()
        compose.waitUntil(10_000) {
            compose.onAllNodes(hasText(context.getString(R.string.oobe_welcome_guidelines_accept)))
                .fetchSemanticsNodes().isNotEmpty()
        }
        compose.onNodeWithText(context.getString(R.string.oobe_welcome_guidelines_accept))
            .performScrollTo().performClick()
        compose.onNode(hasText(remote) or hasContentDescription(remote)).performClick()
        compose.waitUntil(15_000) { settings.oobeCompleted.value && settings.remoteClientMode.value }
        val inRemotePage = hasAnyAncestor(hasTestTag("remote-connection-page"))
        compose.onNode(hasSetTextAction() and inRemotePage).assertIsDisplayed()
        compose.onNode(hasText(context.getString(R.string.remote_pair_scan)) and inRemotePage).assertIsDisplayed()
        assertFalse(provisioner.isInstalled())
        assertFalse(host.startRequested)
        screenshot("remote-entry-no-runtime.png")

        compose.runOnUiThread { compose.activity.onBackPressedDispatcher.onBackPressed() }
        compose.waitForIdle()
        compose.onNode(hasText(context.getString(R.string.nav_hangar)) and hasClickAction()).performClick()
        compose.waitForIdle()
        compose.onNode(hasText(remote) and hasClickAction()).assertIsDisplayed().performClick()
        compose.onNode(hasSetTextAction() and inRemotePage).assertIsDisplayed()

        val code = args.getString("pairingCode").orEmpty()
        if (code.isNotEmpty()) {
            compose.onNode(hasSetTextAction() and inRemotePage).performTextInput(code)
            compose.onNode(hasText(context.getString(R.string.remote_pair_connect)) and inRemotePage).performClick()
            compose.waitUntil(90_000) {
                repository.isRemote && repository.connected.value &&
                    repository.schema.value != null && repository.overview.value != null
            }
            compose.onNodeWithText(context.getString(R.string.ap_status_stopped)).assertIsDisplayed()
            assertTrue(runBlocking { RemotePairings(context).list().isNotEmpty() })
            screenshot("remote-entry-native-console.png")
        }
        compose.activityRule.scenario.recreate()
        compose.waitForIdle()
        assertTrue(settings.remoteClientMode.value)
        assertFalse("远程重建不得启动本机 Runtime", host.startRequested)
        assertFalse(provisioner.isInstalled())
        assertFalse(File(context.filesDir, "rootfs").exists())
    }

    @Test fun coldStartRetainsRemoteModeWithoutLocalRuntime() {
        assumeTrue("先完成独立安装的远程入口验收", InstrumentationRegistry.getArguments()
            .getString("resumeRemoteEntry") == "true")
        val koin = GlobalContext.get()
        val settings = koin.get<AppSettingsManager>()
        compose.waitUntil(15_000) { settings.loaded.value }
        assertTrue(settings.oobeCompleted.value)
        assertTrue(settings.remoteClientMode.value)
        compose.onNodeWithText(compose.activity.getString(R.string.remote_pair_scan)).assertIsDisplayed()
        assertFalse(koin.get<ProotHost>().startRequested)
        assertFalse(koin.get<RootfsProvisioner>().isInstalled())
        assertFalse(File(compose.activity.filesDir, "rootfs").exists())
        screenshot("remote-cold-start-no-runtime.png")
    }

    /** 保存实际 Compose 界面，避免系统截图捕获过渡帧。 / Saves the settled Compose view. */
    private fun screenshot(name: String) {
        compose.waitForIdle()
        val bitmap = compose.onRoot().captureToImage().asAndroidBitmap()
        File(compose.activity.filesDir, name).outputStream().use {
            bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it)
        }
    }
}
