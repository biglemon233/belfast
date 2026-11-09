package com.aliothmoon.maafw.ui.hangar

import androidx.compose.ui.geometry.Offset

enum class PreviewTouchAction { Click, Down, Move, Up }

/** Complete taps after release; stream a drag once it passes touch slop. */
internal class PreviewTouchGesture(
    private val origin: Offset,
    private val touchSlop: Float,
    private val emit: (Offset, PreviewTouchAction) -> Unit,
) {
    private var last = origin
    private var dragging = false
    private var finished = false

    fun hold() {
        if (finished || dragging) return
        dragging = true
        emit(origin, PreviewTouchAction.Down)
    }

    fun move(position: Offset) {
        if (finished) return
        last = position
        if (!dragging && (position - origin).getDistance() > touchSlop) {
            hold()
        }
        if (dragging) emit(position, PreviewTouchAction.Move)
    }

    fun up(position: Offset) {
        if (finished) return
        if (!dragging && (position - origin).getDistance() > touchSlop) move(position)
        finished = true
        emit(position, if (dragging) PreviewTouchAction.Up else PreviewTouchAction.Click)
    }

    fun cancel() {
        if (finished) return
        finished = true
        if (dragging) emit(last, PreviewTouchAction.Up)
    }
}
