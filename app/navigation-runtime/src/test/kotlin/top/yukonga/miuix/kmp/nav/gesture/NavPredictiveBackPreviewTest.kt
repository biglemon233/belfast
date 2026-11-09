package top.yukonga.miuix.kmp.nav.gesture

import kotlin.test.Test
import kotlin.test.assertEquals

class NavPredictiveBackPreviewTest {
    @Test
    fun fullTravelForDefaultNavigation() {
        assertEquals(0.8f, previewFingerProgress(0.8f, 1f))
    }

    @Test
    fun settingsGestureOnlyPreviewsBeforeCommit() {
        assertEquals(0f, previewFingerProgress(0f, 0.16f))
        assertEquals(0.08f, previewFingerProgress(0.5f, 0.16f))
        assertEquals(0.16f, previewFingerProgress(1f, 0.16f))
    }

    @Test
    fun outOfRangeFingerDataIsClamped() {
        assertEquals(0f, previewFingerProgress(-1f, 0.16f))
        assertEquals(0.16f, previewFingerProgress(2f, 0.16f))
    }
}
