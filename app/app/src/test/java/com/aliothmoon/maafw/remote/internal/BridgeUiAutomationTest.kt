package com.aliothmoon.maafw.remote.internal

import android.app.UiAutomation
import android.os.DeadObjectException
import android.os.HandlerThread
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.*
import org.junit.Test
import java.lang.reflect.InvocationTargetException

class BridgeUiAutomationTest {
    private val automation = BridgeUiAutomation::class.java.getDeclaredField("automation").apply { isAccessible = true }
    private val handler = BridgeUiAutomation::class.java.getDeclaredField("handlerThread").apply { isAccessible = true }

    @After fun tearDown() {
        automation.set(BridgeUiAutomation, null)
        handler.set(BridgeUiAutomation, null)
    }

    @Test fun onlyDisconnectedSessionsAreInvalidated() {
        assertTrue(BridgeUiAutomation.isDisconnected(IllegalStateException("UiAutomation not connected!")))
        assertTrue(BridgeUiAutomation.isDisconnected(InvocationTargetException(DeadObjectException())))
        for (error in listOf(
            IllegalStateException("UiAutomationService already registered!"),
            IllegalStateException("UI node changed; query the selector again"),
            SecurityException("Permission denied"),
            RuntimeException("UiAutomation not connected!"),
        )) {
            assertFalse(BridgeUiAutomation.isDisconnected(error))
        }
    }

    @Test fun disconnectedRequestReleasesOurSessionWithoutReplaying() {
        for (action in listOf("dump", "click", "set_text")) {
            val ui = mockk<UiAutomation>(relaxed = true)
            val thread = mockk<HandlerThread>(relaxed = true)
            val request = mockk<JSONObject>()
            val failure = IllegalStateException("UiAutomation not connected!")
            every { request.getString("action") } returns action
            every { ui.windows } throws failure
            automation.set(BridgeUiAutomation, ui)
            handler.set(BridgeUiAutomation, thread)

            assertSame(failure, assertThrows(IllegalStateException::class.java) { BridgeUiAutomation.handle(request) })
            assertNull(automation.get(BridgeUiAutomation))
            assertNull(handler.get(BridgeUiAutomation))
            verify(exactly = 1) { ui.windows }
            verify(exactly = 1) { thread.quitSafely() }
        }
    }

    @Test fun unrelatedStateErrorPreservesSessionAndOriginalFailure() {
        val ui = mockk<UiAutomation>(relaxed = true)
        val request = mockk<JSONObject>()
        val failure = IllegalStateException("UI window disappeared; query the selector again")
        every { request.getString("action") } returns "click"
        every { ui.windows } throws failure
        automation.set(BridgeUiAutomation, ui)

        assertSame(failure, assertThrows(IllegalStateException::class.java) { BridgeUiAutomation.handle(request) })
        assertSame(ui, automation.get(BridgeUiAutomation))
        verify(exactly = 1) { ui.windows }
    }
}
