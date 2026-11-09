package com.aliothmoon.maafw.ui.alas

import android.webkit.WebView

/** Main-thread only. Tab visibility must not control process-global WebView timers. */
internal object AlasWebViewHolder {
    private val webViews = mutableSetOf<WebView>()
    private var paused = true

    fun register(webView: WebView) {
        webViews += webView
        // Even an empty pauseAll/resumeAll must affect the next WebView after recreation.
        if (paused) {
            webView.onPause()
            webView.pauseTimers()
        } else {
            webView.onResume()
            webView.resumeTimers()
        }
    }

    fun unregister(webView: WebView) {
        webViews -= webView
        // Removing a tab/instance must never pause the process-global timers.
    }

    fun pauseAll() {
        paused = true
        webViews.forEach { it.onPause() }
        webViews.firstOrNull()?.pauseTimers()
    }

    fun resumeAll() {
        paused = false
        webViews.firstOrNull()?.resumeTimers()
        webViews.forEach { it.onResume() }
    }
}
