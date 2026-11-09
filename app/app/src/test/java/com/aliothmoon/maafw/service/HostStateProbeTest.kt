package com.aliothmoon.maafw.service

import android.os.SystemClock
import com.aliothmoon.maafw.MaaDispatchers
import io.mockk.every
import io.mockk.mockk
import io.mockk.mockkConstructor
import io.mockk.mockkObject
import io.mockk.mockkStatic
import io.mockk.unmockkAll
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.runBlocking
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.net.ConnectException
import java.net.Socket
import java.util.concurrent.Executors

class HostStateProbeTest {
    private val io = Executors.newSingleThreadExecutor { Thread(it, "bridge-probe-io") }
        .asCoroutineDispatcher()
    private var offline = false
    private val connectThreads = mutableListOf<String>()

    @Before
    fun setUp() {
        mockkObject(MaaDispatchers)
        every { MaaDispatchers.IO } returns io
        mockkStatic(SystemClock::class)
        every { SystemClock.elapsedRealtime() } returns 1_000L
        mockkConstructor(Socket::class, JSONObject::class)
        every { anyConstructed<Socket>().connect(any(), any()) } answers {
            connectThreads += Thread.currentThread().name.substringBefore(" @coroutine")
            if (offline) throw ConnectException("test bridge offline")
            Unit
        }
        every { anyConstructed<Socket>().soTimeout = any() } answers { Unit }
        every { anyConstructed<Socket>().getOutputStream() } answers { ByteArrayOutputStream() }
        every { anyConstructed<Socket>().getInputStream() } answers {
            ByteArrayInputStream("{\"pong\":true}\n".toByteArray())
        }
        every { anyConstructed<Socket>().close() } answers { Unit }
        every { anyConstructed<JSONObject>().optBoolean("pong", false) } returns true
    }

    @After
    fun tearDown() {
        unmockkAll()
        io.close()
    }

    @Test
    fun `page initiated probes use IO without relying on caller dispatcher`() = runBlocking {
        val host = HostState(mockk(), mockk(), this, mockk())
        repeat(10) {
            assertTrue(host.probeBridgeNow())
            assertTrue(host.snapshot.value.bridgeReachable)
        }
        assertEquals(List(10) { "bridge-probe-io" }, connectThreads)
    }

    @Test
    fun `real connection failures still mark bridge down and recover`() = runBlocking {
        val host = HostState(mockk(), mockk(), this, mockk())
        assertTrue(host.probeBridgeNow())
        offline = true
        assertFalse(host.probeBridgeNow())
        assertTrue(host.snapshot.value.bridgeReachable)
        assertFalse(host.probeBridgeNow())
        assertFalse(host.snapshot.value.bridgeReachable)
        offline = false
        assertTrue(host.probeBridgeNow())
        assertTrue(host.snapshot.value.bridgeReachable)
    }
}
