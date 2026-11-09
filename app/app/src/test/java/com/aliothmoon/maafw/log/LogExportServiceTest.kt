package com.aliothmoon.maafw.log

import android.content.Context
import io.mockk.mockk
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.io.File
import java.util.zip.ZipFile
import kotlin.io.path.createTempDirectory

class LogExportServiceTest {
    private lateinit var base: File
    private lateinit var alas: File
    private lateinit var service: LogExportService

    @Before
    fun setUp() {
        base = createTempDirectory("log-selection").toFile()
        alas = File(base, "rootfs/opt/alas/log")
        service = LogExportService(mockk<Context>(), { base }, { listOf(File(base, "log")) }, { alas })
    }

    @After
    fun tearDown() {
        base.deleteRecursively()
    }

    private fun write(root: File, path: String): File = File(root, path).apply {
        parentFile?.mkdirs()
        writeText(path)
    }

    private fun entries(zip: File): Set<String> = ZipFile(zip).use { archive ->
        archive.entries().asSequence().map { it.name }.toSet()
    }

    @Test
    fun `choosing an error record exports all its attachments without other records`() = runBlocking {
        val timestamp = System.currentTimeMillis().toString()
        val otherTimestamp = (timestamp.toLong() - 1000).toString()
        val log = write(alas, "error/$timestamp/log.txt")
        write(alas, "error/$timestamp/screenshot.png")
        write(alas, "error/$timestamp/config.json")
        write(alas, "error/$otherTimestamp/log.txt")
        write(alas, "gui.out")

        val items = service.availableItems(LogExportKind.ALAS)
        val record = items.single { it.errorTimestamp == timestamp }
        assertEquals(3, record.files.size)
        assertEquals(3, items.size)

        val zip = requireNotNull(service.exportZip(LogExportKind.ALAS, setOf(record.id)))
        assertEquals(
            setOf("log/error/$timestamp/log.txt", "log/error/$timestamp/screenshot.png", "log/error/$timestamp/config.json"),
            entries(zip),
        )
        assertEquals("error/$timestamp/log.txt", log.readText())
    }

    @Test
    fun `empty unknown and removed selections never fall back to exporting all logs`() = runBlocking {
        val chosen = write(alas, "chosen.txt")
        write(alas, "gui.out")
        val chosenId = service.availableItems(LogExportKind.ALAS).single { it.id.endsWith("chosen.txt") }.id
        assertTrue(chosen.delete())

        assertNull(service.exportZip(LogExportKind.ALAS, emptySet()))
        assertNull(service.exportZip(LogExportKind.ALAS, setOf("log/../secret.txt")))
        assertNull(service.exportZip(LogExportKind.ALAS, setOf(chosenId)))
        assertTrue(File(alas, "gui.out").exists())
    }

    @Test
    fun `daily file selection stays fixed when another log appears before export`() = runBlocking {
        write(alas, "chosen.txt")
        val selected = service.availableItems(LogExportKind.ALAS).map { it.id }.toSet()
        write(alas, "later.txt")

        val zip = requireNotNull(service.exportZip(LogExportKind.ALAS, selected))
        assertEquals(setOf("log/chosen.txt"), entries(zip))
    }

    @Test
    fun `launcher selection preserves relative paths and excludes previous exports`() = runBlocking {
        write(base, "log/app.log")
        write(base, "log/session.log")
        write(base, "log/export/previous.zip")

        assertEquals(setOf("log/app.log", "log/session.log"), service.availableItems(LogExportKind.LAUNCHER).map { it.id }.toSet())
        val zip = requireNotNull(service.exportZip(LogExportKind.LAUNCHER, setOf("log/session.log")))
        assertEquals(setOf("log/session.log"), entries(zip) - "properties.txt")
    }
}
