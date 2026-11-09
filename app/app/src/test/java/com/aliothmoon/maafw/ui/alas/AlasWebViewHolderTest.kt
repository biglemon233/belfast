package com.aliothmoon.maafw.ui.alas

import android.webkit.WebView
import io.mockk.clearMocks
import io.mockk.mockk
import io.mockk.verify
import org.junit.After
import org.junit.Test

class AlasWebViewHolderTest {
    private val views = mutableListOf<WebView>()

    private fun view(): WebView = mockk<WebView>(relaxed = true).also { views += it }

    @After
    fun cleanUp() {
        views.forEach { AlasWebViewHolder.unregister(it) }
        AlasWebViewHolder.resumeAll()
    }

    @Test
    fun `register in background pauses instance and global timers`() {
        AlasWebViewHolder.pauseAll()
        val webView = view()
        AlasWebViewHolder.register(webView)
        verify(exactly = 1) { webView.onPause() }
        verify(exactly = 1) { webView.pauseTimers() }
        verify(exactly = 0) { webView.resumeTimers() }
    }

    @Test
    fun `empty resume clears global pause on next registration`() {
        val old = view()
        AlasWebViewHolder.register(old)
        AlasWebViewHolder.pauseAll()
        AlasWebViewHolder.unregister(old)
        AlasWebViewHolder.resumeAll()
        val replacement = view()
        AlasWebViewHolder.register(replacement)
        verify(exactly = 1) { replacement.onResume() }
        verify(exactly = 1) { replacement.resumeTimers() }
        verify(exactly = 0) { replacement.pauseTimers() }
    }

    @Test
    fun `removing an instance cannot pause remaining tabs`() {
        AlasWebViewHolder.resumeAll()
        val first = view()
        val second = view()
        AlasWebViewHolder.register(first)
        AlasWebViewHolder.register(second)
        clearMocks(first, second)
        AlasWebViewHolder.unregister(first)
        verify(exactly = 0) { first.pauseTimers() }
        verify(exactly = 0) { second.pauseTimers() }
        verify(exactly = 0) { second.onPause() }
    }

    @Test
    fun `pause and resume each instance but global timers only once`() {
        AlasWebViewHolder.resumeAll()
        val first = view()
        val second = view()
        AlasWebViewHolder.register(first)
        AlasWebViewHolder.register(second)
        clearMocks(first, second)
        AlasWebViewHolder.pauseAll()
        AlasWebViewHolder.resumeAll()
        verify(exactly = 1) { first.onPause() }
        verify(exactly = 1) { second.onPause() }
        verify(exactly = 1) { first.onResume() }
        verify(exactly = 1) { second.onResume() }
        verify(exactly = 1) { first.pauseTimers() }
        verify(exactly = 1) { first.resumeTimers() }
        verify(exactly = 0) { second.pauseTimers() }
        verify(exactly = 0) { second.resumeTimers() }
    }
}
