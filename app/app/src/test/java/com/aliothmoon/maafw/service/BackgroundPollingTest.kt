package com.aliothmoon.maafw.service

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class BackgroundPollingTest {
    @Test
    fun `only background idle work backs off to thirty seconds`() {
        assertEquals(30_000L, BackgroundPolling.intervalMs(foreground = false, active = false))
        assertEquals(4_000L, BackgroundPolling.intervalMs(foreground = true, active = false))
        assertEquals(4_000L, BackgroundPolling.intervalMs(foreground = false, active = true))
        assertEquals(4_000L, BackgroundPolling.intervalMs(foreground = true, active = true))
    }

    @Test
    fun `continuous failures log first then at five minute intervals`() {
        val throttle = PollFailureThrottle()
        assertTrue(throttle.failed(0L))
        assertFalse(throttle.failed(4_000L))
        assertFalse(throttle.failed(299_999L))
        assertTrue(throttle.failed(300_000L))
        assertFalse(throttle.failed(304_000L))
        assertTrue(throttle.failed(600_000L))
        assertEquals(6, throttle.failures)
    }

    @Test
    fun `recovery reports streak once and next failure logs immediately`() {
        val throttle = PollFailureThrottle()
        assertTrue(throttle.failed(100L))
        assertFalse(throttle.failed(200L))
        assertEquals(2, throttle.recovered())
        assertEquals(0, throttle.recovered())
        assertTrue(throttle.failed(201L))
        assertEquals(1, throttle.failures)
    }
}
