package com.aliothmoon.maafw.domain

import com.aliothmoon.maafw.config.UserConfigurationSerializer
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream

class UserConfigurationMigrationTest {
    @Test
    fun `retired PI fields do not reset the selected theme`() = runTest {
        val previous = """{"schemaVersion":1,"config":{"initialized":true,"themeMode":"Dark","configurations":[{"id":"old","name":"PI","tasks":[]}],"activeConfigurationId":"old","globalOptionValues":{}}}"""
        val config = UserConfigurationSerializer.readFrom(ByteArrayInputStream(previous.toByteArray()))
        assertEquals(ThemeMode.Dark, config.themeMode)
        val output = ByteArrayOutputStream()
        UserConfigurationSerializer.writeTo(config, output)
        assertEquals(config, UserConfigurationSerializer.readFrom(ByteArrayInputStream(output.toByteArray())))
    }

    @Test
    fun `old empty settings retain the system theme`() = runTest {
        val previous = """{"schemaVersion":1,"config":{"initialized":false}}"""
        val config = UserConfigurationSerializer.readFrom(ByteArrayInputStream(previous.toByteArray()))
        assertEquals(ThemeMode.System, config.themeMode)
    }
}
