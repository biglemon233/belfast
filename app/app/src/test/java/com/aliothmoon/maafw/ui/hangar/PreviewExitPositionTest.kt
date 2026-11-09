package com.aliothmoon.maafw.ui.hangar

import androidx.compose.ui.geometry.Offset
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PreviewExitPositionTest {
    @Test fun interiorReleaseStaysWhereItWasDragged() {
        assertEquals(PreviewExitDock.NONE, previewExitDock(Offset(0.35f, 0.5f), Offset(1000f, 500f), 32f))
    }

    @Test fun largerEdgeZoneRecognizesEachPhysicalEdge() {
        val range = Offset(1000f, 500f)
        assertEquals(PreviewExitDock.LEFT, previewExitDock(Offset(0.03f, 0.5f), range, 32f))
        assertEquals(PreviewExitDock.RIGHT, previewExitDock(Offset(0.97f, 0.5f), range, 32f))
        assertEquals(PreviewExitDock.TOP, previewExitDock(Offset(0.5f, 0.04f), range, 32f))
        assertEquals(PreviewExitDock.BOTTOM, previewExitDock(Offset(0.5f, 0.96f), range, 32f))
        assertEquals(PreviewExitDock.NONE, previewExitDock(Offset(0.5f, 0.5f), Offset.Zero, 32f))
    }

    @Test fun cornersHideInOnlyOneDirection() {
        assertEquals(PreviewExitDock.TOP, previewExitDock(Offset(0.02f, 0.01f), Offset(1000f, 500f), 32f))
    }

    @Test fun dockingHidesHalfAndRevealRestoresTheWholeCircle() {
        val range = Offset(1000f, 500f)
        val left = Offset(0f, 0.4f)
        assertEquals(Offset(-15f, 200f), previewExitOffset(left, range, PreviewExitDock.LEFT, 15f))
        assertEquals(Offset(0f, 200f), previewExitOffset(left, range, PreviewExitDock.NONE, 15f))
        assertEquals(Offset(1015f, 200f), previewExitOffset(Offset(1f, 0.4f), range, PreviewExitDock.RIGHT, 15f))
        assertEquals(Offset(500f, -15f), previewExitOffset(Offset(0.5f, 0f), range, PreviewExitDock.TOP, 15f))
        assertEquals(Offset(500f, 515f), previewExitOffset(Offset(0.5f, 1f), range, PreviewExitDock.BOTTOM, 15f))
    }

    @Test fun visibleHalfAndRevealedCircleBothAvoidSystemTouchWindows() {
        val range = Offset(1000f, 500f)
        val safe = Offset(32f, 40f)
        assertEquals(Offset(32f, 200f), previewExitVisibleOffset(
            Offset(0f, 0.4f), range, PreviewExitDock.NONE, 20f, safe, safe))
        assertEquals(Offset(12f, 200f), previewExitVisibleOffset(
            Offset(0f, 0.4f), range, PreviewExitDock.LEFT, 20f, safe, safe))
        assertEquals(Offset(968f, 200f), previewExitVisibleOffset(
            Offset(1f, 0.4f), range, PreviewExitDock.NONE, 20f, safe, safe))
        assertEquals(Offset(500f, 250f), previewExitVisibleOffset(
            Offset(0.5f, 0.5f), range, PreviewExitDock.NONE, 20f, safe, safe))
    }

    @Test fun onlyTheVisibleHalfAcceptsAPress() {
        val size = Offset(30f, 30f)
        assertTrue(previewExitHit(Offset(22f, 15f), size, PreviewExitDock.LEFT))
        assertFalse(previewExitHit(Offset(8f, 15f), size, PreviewExitDock.LEFT))
        assertTrue(previewExitHit(Offset(8f, 15f), size, PreviewExitDock.RIGHT))
        assertFalse(previewExitHit(Offset(22f, 15f), size, PreviewExitDock.RIGHT))
        assertTrue(previewExitHit(Offset(15f, 22f), size, PreviewExitDock.TOP))
        assertTrue(previewExitHit(Offset(15f, 8f), size, PreviewExitDock.BOTTOM))
        assertFalse(previewExitHit(Offset(0f, 0f), size, PreviewExitDock.NONE))
    }
}
