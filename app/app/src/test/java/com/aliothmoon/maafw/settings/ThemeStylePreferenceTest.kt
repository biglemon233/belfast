package com.aliothmoon.maafw.settings

import com.aliothmoon.maafw.theme.ThemeStyle
import org.junit.Assert.assertEquals
import org.junit.Test

class ThemeStylePreferenceTest {
    @Test
    fun `new installations and unknown preferences use Material 3`() {
        assertEquals("MATERIAL", AppSettings().themeStyle)
        for (raw in listOf(null, "", "removed-theme")) {
            assertEquals(ThemeStyle.MATERIAL, ThemeStyle.fromPreference(raw))
        }
    }

    @Test
    fun `existing choices keep their meaning when the default changes`() {
        assertEquals(ThemeStyle.DEFAULT, ThemeStyle.fromPreference("DEFAULT"))
        assertEquals(ThemeStyle.SEMI_DESIGN, ThemeStyle.fromPreference("SEMI_DESIGN"))
        assertEquals(ThemeStyle.MATERIAL, ThemeStyle.fromPreference("MATERIAL"))
    }
}
