package com.azurpilot.ghio.settings

import org.junit.Assert.assertEquals
import org.junit.Test

class ScreenSaverBrightnessTest {
    @Test
    fun `missing or invalid brightness uses one percent default`() {
        listOf("", "invalid", "NaN", "Infinity").forEach { value ->
            assertEquals(ScreenSaverBrightness.DEFAULT, ScreenSaverBrightness.parse(value), 0.0001f)
        }
    }

    @Test
    fun `saved brightness stays within safe readable bounds`() {
        assertEquals(ScreenSaverBrightness.MIN, ScreenSaverBrightness.parse("0"), 0.0001f)
        assertEquals(0.01f, ScreenSaverBrightness.DEFAULT, 0.0001f)
        assertEquals(ScreenSaverBrightness.MAX, ScreenSaverBrightness.parse("1.0"), 0.0001f)
        assertEquals(0.28f, ScreenSaverBrightness.parse("0.28"), 0.0001f)
    }
}
