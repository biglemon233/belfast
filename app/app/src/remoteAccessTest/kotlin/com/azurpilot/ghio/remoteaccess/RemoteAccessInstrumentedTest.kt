package com.azurpilot.ghio.remoteaccess

import android.content.Intent
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.azurpilot.ghio.R
import com.azurpilot.ghio.proot.AzurPilotGateway
import com.azurpilot.ghio.proot.AzurPilotRepository
import com.azurpilot.ghio.proot.ProotHost
import com.azurpilot.ghio.provision.RootfsProvisioner
import com.azurpilot.ghio.provision.ProvisionState
import com.azurpilot.ghio.settings.AppSettingsManager
import com.azurpilot.ghio.theme.AzurPilotTheme
import com.azurpilot.ghio.ui.azurpilot.AzurPilotPage
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.CompletableDeferred
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.koin.core.context.GlobalContext
import java.io.File
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy

/**
 * 在独立 Android 验收实例验证实际配对服务、Keystore、原生控制台和 CameraX。
 *
 * Tests the real registry, Keystore, native console, and CameraX on an isolated Android device.
 */
@RunWith(AndroidJUnit4::class)
class RemoteAccessInstrumentedTest {
    @get:Rule val compose = createComposeRule()
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val context = instrumentation.targetContext
    private val arguments = InstrumentationRegistry.getArguments()

    @Test fun sixDigitPairingOpensNativeConsoleAndSurvivesReconnection() = runBlocking {
        val code = arguments.getString("pairingCode").orEmpty()
        assumeTrue("需要隔离 AP 的一次性配对码", code.matches(Regex("[0-9]{6}")))
        val repository = GlobalContext.get().get<AzurPilotRepository>()
        val gateway = GlobalContext.get().get<AzurPilotGateway>()
        val store = RemotePairings(context)
        repository.start()
        var opened by mutableStateOf(false)
        compose.setContent {
            AzurPilotTheme {
                if (opened) AzurPilotPage(active = false, onNavigate = {})
                else RemotePairingCard(onConnected = { opened = true }, repository = repository)
            }
        }
        compose.onNode(hasSetTextAction()).performTextInput(code)
        compose.onNodeWithText(context.getString(R.string.remote_pair_connect), substring = false).performClick()
        compose.waitUntil(60_000) { opened && repository.connected.value && repository.overview.value != null }
        assertTrue(repository.isRemote)
        assertNotNull(repository.schema.value)
        val paired = store.list().last()
        assertTrue(RemotePairings(context).list().contains(paired))
        assertFalse(File(context.noBackupFilesDir, "remote-pairings.enc").readText().contains(paired.accessToken))
        compose.onNodeWithText("remote-demo").assertIsDisplayed().performClick()
        compose.waitUntil(20_000) { repository.overview.value?.instance == "remote-demo" }
        compose.waitForIdle()
        val config = gateway.call("config.get", JSONObject().put("instance", "remote-demo"))
        assertTrue(config is AzurPilotGateway.Reply.Ok)
        phase("native-ready")
        if (arguments.getString("reconnect") == "true") {
            withTimeout(120_000) { gateway.connected.first { !it } }
            withTimeout(180_000) { gateway.connected.first { it } }
            assertTrue(gateway.call("system.ping") is AzurPilotGateway.Reply.Ok)
            assertEquals(paired.url, store.list().last().url)
            phase("reconnected")
        }
        if (arguments.getString("revoke") == "true") {
            withTimeout(90_000) { gateway.authRequired.first { it } }
            assertFalse(gateway.connected.value)
            phase("revoked")
        }
    }

    @Test fun cameraXReturnsValidatedOpticalSecret() {
        val expected = arguments.getString("scanSecret").orEmpty()
        assumeTrue("需要相机视频中的一次性扫描密钥", expected.matches(Regex("[a-f0-9]{32}")))
        val executor = Executors.newFixedThreadPool(2)
        val scenario = ActivityScenario.launchActivityForResult<QrScanActivity>(
            Intent(context, QrScanActivity::class.java),
        )
        try {
            if (arguments.getString("cameraDiagnostics") == "true") {
                var reader: ImageAnalysis? = null
                var activity: QrScanActivity? = null
                val field = QrScanActivity::class.java.getDeclaredField("analysis").apply { isAccessible = true }
                val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10)
                while (reader == null && System.nanoTime() < deadline) {
                    scenario.onActivity { activity = it; reader = field.get(it) as? ImageAnalysis }
                    if (reader == null) Thread.sleep(50)
                }
                val analyze = QrScanActivity::class.java.getDeclaredMethod("analyze", ImageProxy::class.java)
                    .apply { isAccessible = true }
                var captured = 0
                reader?.setAnalyzer(executor) { image ->
                    // 仅显式启用的合成相机验收写帧，生产扫码仍只在内存处理。
                    if (captured in listOf(5, 13)) {
                        val plane = image.planes[0]
                        val bytes = ByteArray(plane.buffer.remaining())
                        plane.buffer.duplicate().get(bytes)
                        val prefix = File(context.filesDir, "camera-$captured")
                        File(prefix.path + ".rgba").writeBytes(bytes)
                        File(prefix.path + ".txt").writeText("${image.width},${image.height},${plane.rowStride},${plane.pixelStride},${image.cropRect}")
                    }
                    captured++
                    analyze.invoke(activity, image)
                }
            }
            val result = executor.submit<android.app.Instrumentation.ActivityResult> { scenario.result }.get(60, TimeUnit.SECONDS)
            assertEquals(android.app.Activity.RESULT_OK, result.resultCode)
            assertEquals(expected, result.resultData.getStringExtra(QrScanActivity.EXTRA_PAIRING_SECRET))
            runBlocking { assertNotNull(RemotePairings(context).pair(expected)) }
            phase("camera-paired")
        } finally {
            scenario.close()
            executor.shutdownNow()
        }
    }

    @Test fun bundledRuntimeHostsQuickTunnelAndKeepsIdentityAfterRestart(): Unit = runBlocking {
        assumeTrue("仅在显式启用时部署独立验收 Runtime", arguments.getString("runtimeHosting") == "true")
        val koin = GlobalContext.get()
        val provisioner = koin.get<RootfsProvisioner>()
        val host = koin.get<ProotHost>()
        val settings = koin.get<AppSettingsManager>()
        val local = koin.get<AzurPilotGateway>()
        var showNative by mutableStateOf(false)
        compose.setContent {
            AzurPilotTheme {
                if (showNative) AzurPilotPage(active = false, onNavigate = {})
                else androidx.compose.material3.Text("Quick Tunnel runtime acceptance")
            }
        }
        provisioner.start(installIfMissing = true)
        val provisioned = withTimeout(600_000) {
            provisioner.state.first { it is ProvisionState.Ready || it is ProvisionState.Failed || it is ProvisionState.NotBundled }
        }
        assertEquals(ProvisionState.Ready, provisioned)
        phase("runtime-provisioned")
        settings.setRemoteAccessEnabled(true)
        withTimeout(10_000) { settings.remoteAccessEnabled.first { it } }
        host.ensureStarted()
        local.selectTarget(null)
        withTimeout(240_000) { local.connected.first { it } }
        suspend fun readyEntry(): String = withTimeout(240_000) {
            var address = ""
            while (address.isEmpty()) {
                val reply = local.call("settings.get") as? AzurPilotGateway.Reply.Ok
                val remote = reply?.obj?.optJSONObject("remote")
                if (remote?.optString("state") == "cloudflare_ready") address = remote.optString("address")
                if (address.isEmpty()) delay(1_000)
            }
            address
        }
        val fixed = readyEntry()
        val identityFile = File(context.filesDir, "rootfs/opt/azurpilot/config/remote-access.json")
        val identity = JSONObject(identityFile.readText())
        val pairReply = local.call("remote.pair.create") as AzurPilotGateway.Reply.Ok
        val store = RemotePairings(context)
        val pairing = store.pair(pairReply.obj.getString("code"))
        assertEquals(fixed, pairing.url)
        val oldTunnel = store.resolve(pairing)
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val remote = AzurPilotGateway(scope, store)
        try {
            remote.selectTarget(pairing)
            withTimeout(90_000) { remote.connected.first { it } }
            assertTrue(remote.call("schema.get", JSONObject().put("language", "zh-CN")) is AzurPilotGateway.Reply.Ok)
            phase("runtime-hosting")
            host.stopAndAwait()
            withTimeout(30_000) { local.connected.first { !it } }
            withTimeout(60_000) { remote.connected.first { !it } }
            host.ensureStarted()
            withTimeout(240_000) { local.connected.first { it } }
            assertEquals(fixed, readyEntry())
            assertNotEquals(oldTunnel, store.resolve(pairing))
            val restartedIdentity = JSONObject(identityFile.readText())
            assertEquals(identity.getString("device_id"), restartedIdentity.getString("device_id"))
            assertEquals(identity.getString("device_key"), restartedIdentity.getString("device_key"))
            withTimeout(180_000) { remote.connected.first { it } }
            assertTrue(remote.call("system.ping") is AzurPilotGateway.Reply.Ok)
            val repository = koin.get<AzurPilotRepository>()
            repository.start()
            val switched = CompletableDeferred<Boolean>()
            repository.connectTarget(pairing) { switched.complete(it) }
            assertTrue(withTimeout(15_000) { switched.await() })
            compose.runOnIdle { showNative = true }
            compose.waitUntil(60_000) { repository.connected.value && repository.overview.value != null && repository.schema.value != null }
            compose.waitForIdle()
            compose.onNodeWithText(context.getString(R.string.ap_status_stopped)).assertIsDisplayed()
            phase("runtime-restarted")
            val rendered = compose.onRoot().captureToImage().asAndroidBitmap()
            File(context.filesDir, "remote-runtime-native.png").outputStream().use {
                rendered.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it)
            }
        } finally {
            remote.stop()
            scope.cancel()
        }
    }

    /** 把验收阶段写进测试实例私有文件，供外部协调 Tunnel 重启。 / Publishes phases for tunnel restart coordination. */
    private fun phase(value: String) {
        File(context.filesDir, "remote-acceptance-phase").writeText(value)
        instrumentation.uiAutomation.takeScreenshot()?.let { image ->
            File(context.filesDir, "remote-$value.png").outputStream().use {
                image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it)
            }
            image.recycle()
        }
    }
}
