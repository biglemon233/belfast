package com.azurpilot.ghio.privileged

import android.os.Process
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.azurpilot.ghio.constant.DisplayMode
import com.azurpilot.ghio.domain.RemoteBackend
import com.azurpilot.ghio.settings.AppSettingsManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.Assume.assumeTrue
import org.junit.runner.RunWith

/**
 * 在已允许本应用 ADB 身份的设备上验证真实 shell 服务、虚拟屏和离线重连。
 *
 * 首次运行允许 Android 显示调试确认。测试结束销毁测试服务并恢复后端设置，
 * 不启动游戏、不清除应用数据，也不改变系统联网开关。
 *
 * Verifies the real shell service, virtual display, and offline reconnection on a device that
 * permits this app's ADB identity. Android may ask for debugging approval on the first run.
 * Destroys test services and restores the backend afterward without launching games, clearing
 * app data, or changing system connectivity settings.
 */
@RunWith(AndroidJUnit4::class)
class EmbeddedAdbInstrumentedTest {
    @Test
    fun pairWithLiveSystemCode() = runBlocking {
        val arguments = InstrumentationRegistry.getArguments()
        val port = arguments.getString("adbPairingPort")?.toIntOrNull()
        val code = arguments.getString("adbPairingCode")
        assumeTrue("Requires a live system pairing dialog", port != null && code != null)
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val settings = org.koin.core.context.GlobalContext.get().get<AppSettingsManager>()
        withTimeout(10_000) { settings.loaded.first { it } }
        val originalBackend = settings.startupBackend.value
        try {
            settings.setStartupBackend(RemoteBackend.SHIZUKU)
            withContext(Dispatchers.Main) {
                RemoteServiceManager.initialize(context) { settings.startupBackend.value }
            }
            assertTrue("System wireless pairing failed", EmbeddedAdbManager.pair(port!!, code!!))
            assertTrue(EmbeddedAdbManager.isReady)
            assertEquals(Process.SHELL_UID, RemoteServiceManager.getInstance(20_000).processUid())
            println("EMBEDDED_ADB_SYSTEM_PAIRING_VERIFIED")
        } finally {
            withContext(Dispatchers.Main) { RemoteServiceManager.unbind() }
            settings.setStartupBackend(originalBackend)
        }
    }

    @Test
    fun activateCreateVirtualDisplayAndReconnectWithoutExternalShizukuGrant() = runBlocking {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val settings = org.koin.core.context.GlobalContext.get().get<AppSettingsManager>()
        withTimeout(10_000) { settings.loaded.first { it } }
        val originalBackend = settings.startupBackend.value
        try {
            settings.setStartupBackend(RemoteBackend.SHIZUKU)
            withContext(Dispatchers.Main) {
                RemoteServiceManager.initialize(context) { settings.startupBackend.value }
            }
            delay(300)
            withContext(Dispatchers.Main) { RemoteServiceManager.unbind() }
            assertTrue("Local ADB activation failed", EmbeddedAdbManager.activate())
            assertTrue("Embedded grant was not published", ShizukuManager.isGranted())
            val first = RemoteServiceManager.getInstance(20_000)
            assertEquals(Process.SHELL_UID, first.processUid())
            val firstPid = first.pid()
            assertTrue("Native bridge did not load: ${first.version()}", first.version().contains("bridge=LibBridge"))
            assertTrue(first.setup(null, null, true))
            assertTrue(first.setVirtualDisplayMode(DisplayMode.BACKGROUND))
            first.setVirtualDisplayResolution(1280, 720, 240)
            val displayId = first.startVirtualDisplay()
            assertTrue("Virtual display creation failed: $displayId", displayId > 0)
            first.stopVirtualDisplay()

            withContext(Dispatchers.Main) { RemoteServiceManager.unbind() }
            delay(500)
            assertTrue("Silent offline reconnect failed", EmbeddedAdbManager.activate(interactive = false))
            val second = RemoteServiceManager.getInstance(20_000)
            assertEquals(Process.SHELL_UID, second.processUid())
            assertNotEquals("Old shell process was reused after destruction", firstPid, second.pid())
            assertTrue(second.version().contains("bridge=LibBridge"))
            val secondPid = second.pid()
            runCatching { second.destroy() }
            withTimeout(5_000) {
                RemoteServiceManager.serviceState.first { it == PrivilegedServiceState.Died }
            }
            val third = RemoteServiceManager.getInstance(20_000)
            assertEquals(Process.SHELL_UID, third.processUid())
            assertNotEquals("Dead shell process was reused", secondPid, third.pid())
            println("EMBEDDED_ADB_VERIFIED uid=${third.processUid()} firstPid=$firstPid secondPid=$secondPid thirdPid=${third.pid()} displayId=$displayId")
        } finally {
            withContext(Dispatchers.Main) { RemoteServiceManager.unbind() }
            settings.setStartupBackend(originalBackend)
        }
    }
}
