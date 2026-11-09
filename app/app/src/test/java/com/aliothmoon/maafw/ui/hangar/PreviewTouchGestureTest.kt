package com.aliothmoon.maafw.ui.hangar

import androidx.compose.ui.geometry.Offset
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class PreviewTouchGestureTest {
    private val events = mutableListOf<Pair<Offset, PreviewTouchAction>>()
    private val origin = Offset(100f, 200f)
    private fun gesture() = PreviewTouchGesture(origin, 8f) { position, action ->
        events += position to action
    }

    @Test
    fun `tap does not focus the game until the host finger is released`() {
        val gesture = gesture()
        gesture.move(Offset(103f, 202f))
        assertTrue(events.isEmpty())
        gesture.up(Offset(103f, 202f))
        gesture.cancel()
        assertEquals(listOf(Offset(103f, 202f) to PreviewTouchAction.Click), events)
    }

    @Test
    fun `drag streams its original down and moves and releases once`() {
        val gesture = gesture()
        gesture.move(Offset(110f, 200f))
        gesture.move(Offset(120f, 210f))
        gesture.up(Offset(130f, 220f))
        gesture.cancel()
        assertEquals(listOf(
            origin to PreviewTouchAction.Down,
            Offset(110f, 200f) to PreviewTouchAction.Move,
            Offset(120f, 210f) to PreviewTouchAction.Move,
            Offset(130f, 220f) to PreviewTouchAction.Up,
        ), events)
    }

    @Test
    fun `cancelled tap sends no click`() {
        val gesture = gesture()
        gesture.cancel()
        gesture.up(origin)
        assertTrue(events.isEmpty())
    }

    @Test
    fun `cancelled drag releases its last position without clicking`() {
        val gesture = gesture()
        gesture.move(Offset(120f, 200f))
        gesture.cancel()
        gesture.cancel()
        assertEquals(listOf(
            origin to PreviewTouchAction.Down,
            Offset(120f, 200f) to PreviewTouchAction.Move,
            Offset(120f, 200f) to PreviewTouchAction.Up,
        ), events)
    }

    @Test
    fun `long press keeps a held contact until release`() {
        val gesture = gesture()
        gesture.hold()
        assertEquals(listOf(origin to PreviewTouchAction.Down), events)
        gesture.up(origin)
        assertEquals(listOf(origin to PreviewTouchAction.Down, origin to PreviewTouchAction.Up), events)
    }

    @Test
    fun `movement reported only on release still counts as a drag`() {
        val gesture = gesture()
        gesture.up(Offset(120f, 200f))
        assertEquals(listOf(
            origin to PreviewTouchAction.Down,
            Offset(120f, 200f) to PreviewTouchAction.Move,
            Offset(120f, 200f) to PreviewTouchAction.Up,
        ), events)
    }
}
