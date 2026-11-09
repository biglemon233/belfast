package com.aliothmoon.maafw.service

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

/** UI visibility and observed activity only; never starts/stops the native ALAS process. */
object BackgroundPolling {
    private val _foreground = MutableStateFlow(false)
    val foreground = _foreground.asStateFlow()

    private val _taskActive = MutableStateFlow(false)
    val taskActive = _taskActive.asStateFlow()

    fun setForeground(value: Boolean) { _foreground.value = value }
    fun setTaskActive(value: Boolean) { _taskActive.value = value }

    fun intervalMs(foreground: Boolean, active: Boolean): Long =
        if (foreground || active) 4_000L else 30_000L
}

/** Caller supplies a monotonic clock; access is serialized by the probe/refresh mutex. */
internal class PollFailureThrottle {
    var failures: Int = 0
        private set
    private var lastLogAtMs = 0L

    fun failed(nowMs: Long): Boolean {
        failures++
        return (failures == 1 || nowMs - lastLogAtMs >= 300_000L).also {
            if (it) lastLogAtMs = nowMs
        }
    }

    fun recovered(): Int = failures.also { failures = 0 }
}
