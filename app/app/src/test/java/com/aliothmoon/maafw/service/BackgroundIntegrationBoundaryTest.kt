package com.aliothmoon.maafw.service

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/** Source contracts complement unit tests; real WebView rendering/IO cancellation needs device tests. */
class BackgroundIntegrationBoundaryTest {
    private fun source(path: String) = File("src/main/java/com/aliothmoon/maafw/$path").readText()

    @Test
    fun `both loops cancel old work and retain visibility as a refresh key`() {
        for (path in listOf("service/HostState.kt", "proot/AlasRunController.kt")) {
            val text = source(path)
            assertTrue(path, text.contains("compareAndSet(false, true)"))
            assertTrue(path, text.contains("BackgroundPolling.foreground"))
            assertTrue(path, text.contains("collectLatest"))
            assertTrue(path, text.contains("scope.launch(MaaDispatchers.Default)"))
            assertTrue(path, text.contains("withContext(MaaDispatchers.IO)"))
            assertTrue(path, text.contains("BackgroundPolling.intervalMs(foreground, active)"))
            assertTrue(path, text.contains("currentCoroutineContext().ensureActive()"))
        }
        val controller = source("proot/AlasRunController.kt")
        assertTrue(controller.contains("foreground to active"))
        assertTrue(controller.contains("if (it.reachable) lastKnownTaskActive = it.runnerAlive || it.toolAlive"))
        assertTrue(controller.contains("it.busy || lastKnownTaskActive"))
    }

    @Test
    fun `blocking bridge result checks cancellation before publishing`() {
        val probe = source("service/HostState.kt").substringAfter("val result = runCatching { pingBridge() }")
        assertTrue(probe.indexOf("ensureActive()") < probe.indexOf("_snapshot.update"))
    }

    @Test
    fun `all GET results check cancellation and always disconnect`() {
        val text = source("proot/AlasRunController.kt")
        for (endpoint in listOf("status", "configs", "logs?tail=")) {
            val afterRead = text.substringAfter("\$BASE/$endpoint")
            assertTrue(endpoint, afterRead.indexOf("ensureActive()") < afterRead.indexOf("_state.update"))
        }
        val get = text.substringAfter("private fun get(")
        assertTrue(get.contains("finally {\n            conn.disconnect()"))
        assertTrue(get.contains("throw IOException(\"http \$code\")"))
        assertFalse(get.contains("Timber."))
    }

    @Test
    fun `only Activity background pauses timers and recreation is excluded`() {
        val activity = source("MainActivity.kt")
        assertTrue(activity.contains("if (!isChangingConfigurations)"))
        assertTrue(activity.contains("BackgroundPolling.setForeground(true)"))
        assertTrue(activity.contains("AlasWebViewHolder.pauseAll()"))
        val screen = source("ui/alas/AlasScreen.kt")
        assertTrue(screen.contains("AlasWebViewHolder.register(this)"))
        assertTrue(screen.contains("AlasWebViewHolder.unregister(it)"))
        assertFalse(screen.contains("pauseAll()"))
        assertFalse(screen.contains("pauseTimers()"))
        assertTrue(screen.contains("MaaButton("))
    }

    @Test
    fun `CSS removes all wrapper frames and sizes the real spinner`() {
        val css = source("ui/alas/AlasScreen.kt").substringAfter("s.textContent = `").substringBefore("`;")
        assertTrue(css.contains("[style*=\"--loading-border\"]:not(.spinner-border)"))
        assertTrue(css.contains("[style*=\"--loading-border\"] .spinner-border"))
        assertTrue(css.contains(".spinner-border[style*=\"--loading-border\"]"))
        assertTrue(css.contains("width: 24px !important"))
        assertTrue(css.contains("height: 24px !important"))
        assertTrue(css.contains("box-sizing: border-box !important"))
        assertTrue(css.contains("border-radius: 50% !important"))
        assertTrue(css.contains("[style*=\"--loading-border-fill--\"] .spinner-border"))
        assertTrue(css.contains("border-right-color: currentColor !important"))
    }
}
