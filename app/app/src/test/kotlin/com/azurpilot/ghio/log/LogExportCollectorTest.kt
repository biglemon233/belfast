package com.azurpilot.ghio.log

import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.io.File
import java.time.Instant
import java.time.ZoneId
import kotlin.io.path.createTempDirectory

/**
 * 验证 [LogExportCollector] 的按时间窗筛选与豁免规则
 *
 * Verifies the time-window filtering and exemption rules of
 * [LogExportCollector].
 */
class LogExportCollectorTest {

    private lateinit var base: File

    /**
     * 固定「现在」：用真实时钟的话跨天跑测试会飘
     *
     * A fixed "now": with the real clock the test would flake when it runs
     * across a day boundary.
     */
    private val now = 1_800_000_000_000L
    private val day = 24L * 60 * 60 * 1000

    @Before
    fun setUp() {
        base = createTempDirectory("log-export").toFile()
    }

    @After
    fun tearDown() {
        base.deleteRecursively()
    }

    private fun write(path: String, ageDays: Long = 0): File =
        File(base, path).apply {
            parentFile?.mkdirs()
            writeText("x")
            setLastModified(now - ageDays * day)
        }

    private fun collect(): List<String> =
        LogExportCollector.collect(listOf(File(base, "log"), File(base, "debug")), now)
            .map { it.relativeTo(base).invariantSeparatorsPath }

    private fun collectAzurPilot(): Set<String> =
        LogExportCollector.collectAzurPilot(File(base, "azurpilot/log"), now)
            .map { it.relativeTo(File(base, "azurpilot/log")).invariantSeparatorsPath }
            .toSet()

    private fun date(ageDays: Long): String =
        Instant.ofEpochMilli(now).atZone(ZoneId.systemDefault()).toLocalDate()
            .minusDays(ageDays).toString()

    /**
     * 这几份自己就有大小或份数上限，不必再按时间筛
     *
     * These files already carry their own size or count caps, so no age
     * filtering is needed.
     */
    @Test
    fun `capped files are collected no matter how old`() {
        write("log/app.log", ageDays = 400)
        write("log/framework.log", ageDays = 90)
        write("log/schedule-trigger.log", ageDays = 30)
        write("debug/root_launch_debug.log", ageDays = 60)

        assertEquals(4, collect().size)
    }

    /**
     * 按次堆文件的目录只留近 7 天，否则一年后的导出包会有上千个文件
     *
     * Directories that pile up one file per run keep only the last 7 days;
     * otherwise a year-old export would carry thousands of files.
     */
    @Test
    fun `rolling dirs drop anything past the window`() {
        write("log/run/run_a.jsonl", ageDays = 1)
        write("log/run/run_b.jsonl", ageDays = 30)
        write("log/crash/crash_a.txt", ageDays = 2)
        write("log/crash/crash_b.txt", ageDays = 8)
        write("log/focus/focus_0.png", ageDays = 100)
        write("debug/logcat/app/logcat_a.log", ageDays = 3)
        write("debug/logcat/app/logcat_b.log", ageDays = 9)

        val kept = collect()
        assertTrue("log/run/run_a.jsonl" in kept)
        assertTrue("log/crash/crash_a.txt" in kept)
        assertTrue("debug/logcat/app/logcat_a.log" in kept)
        assertFalse("log/run/run_b.jsonl" in kept)
        assertFalse("log/crash/crash_b.txt" in kept)
        assertFalse("log/focus/focus_0.png" in kept)
        assertFalse("debug/logcat/app/logcat_b.log" in kept)
    }

    /**
     * 上一次的 zip 再打进来，导一次体积翻一倍
     *
     * Packing the previous export zip in again would double the archive size
     * on every export.
     */
    @Test
    fun `previous exports are never packed again`() {
        write("log/export/azurpilot_logs_old.zip")
        write("log/app.log")

        assertEquals(listOf("log/app.log"), collect())
    }

    @Test
    fun `a missing root is not an error`() {
        write("log/app.log")

        // debug/ 压根没建过：没开过特权进程的设备就是这样
        assertEquals(listOf("log/app.log"), collect())
    }

    @Test
    fun `AzurPilot collects nested logs and non txt diagnostics`() {
        val paths = setOf(
            "${date(1)}_alas.txt",
            "runtime.log",
            "instances/alas/worker.log",
            "llm/analysis.json",
            "bak/${date(2)}_gui.zip",
            "bak/${date(3)}_alas.txt",
        )
        paths.forEach { write("azurpilot/log/$it") }

        assertEquals(paths, collectAzurPilot())
    }

    @Test
    fun `AzurPilot includes complete scenes for every instance and legacy layout`() {
        val timestamp = now - day
        val paths = setOf(
            "error/alas/$timestamp/log.txt",
            "error/alas/$timestamp/${date(100)}_screenshot.png",
            "error/alas/$timestamp/diagnostics/device.json",
            "error/another_instance/$timestamp/log.txt",
            "error/42/$timestamp/log.txt",
            "error/$timestamp/log.txt",
            "error/$timestamp/screenshot.png",
        )
        paths.forEach { write("azurpilot/log/$it", ageDays = 100) }

        assertEquals(paths, collectAzurPilot())
    }

    @Test
    fun `AzurPilot filters expired scenes as a whole and includes the cutoff`() {
        val expired = now - 8 * day
        val cutoff = now - 7 * day
        write("azurpilot/log/error/alas/$expired/log.txt")
        write("azurpilot/log/error/alas/$expired/${date(0)}_screenshot.png")
        write("azurpilot/log/error/$expired/log.txt")
        write("azurpilot/log/error/alas/bak/$expired/log.txt")
        write("azurpilot/log/error/alas/$cutoff/log.txt", ageDays = 100)
        write("azurpilot/log/error/$cutoff/screenshot.png", ageDays = 100)

        assertEquals(
            setOf("error/alas/$cutoff/log.txt", "error/$cutoff/screenshot.png"),
            collectAzurPilot(),
        )
    }

    @Test
    fun `AzurPilot preserves scenes with unrecognized names`() {
        val paths = setOf(
            "error/alas/latest/log.txt",
            "error/imported/screenshot.png",
            "error/alas/999999999999999999999999/log.txt",
        )
        paths.forEach { write("azurpilot/log/$it", ageDays = 100) }

        assertEquals(paths, collectAzurPilot())
    }

    @Test
    fun `AzurPilot dates logs with the supplied clock and keeps the cutoff day`() {
        write("azurpilot/log/${date(7)}_alas.txt", ageDays = 100)
        write("azurpilot/log/${date(8)}_alas.txt")
        write("azurpilot/log/bak/${date(8)}_gui.zip")
        write("azurpilot/log/nested/${date(8)}_worker.log")

        assertEquals(setOf("${date(7)}_alas.txt"), collectAzurPilot())
    }

    @Test
    fun `AzurPilot retains archives whose date range overlaps the window`() {
        val recent = "bak/${date(30)}~${date(1)}_alas.tar.bz2"
        val old = "bak/${date(30)}~${date(8)}_alas.tar.xz"
        val errorBackup = "error/alas/bak/${date(30)}~${date(1)}_alas.zip"
        write("azurpilot/log/$recent")
        write("azurpilot/log/$old")
        write("azurpilot/log/$errorBackup")

        assertEquals(setOf(recent, errorBackup), collectAzurPilot())
    }

    @Test
    fun `AzurPilot preserves logs with invalid or unknown date names`() {
        val paths = setOf("latest.txt", "2026-99-99_alas.txt", "bak/archive.tar.gz")
        paths.forEach { write("azurpilot/log/$it", ageDays = 100) }

        assertEquals(paths, collectAzurPilot())
    }

    @Test
    fun `AzurPilot excludes previous exports without excluding an instance named export`() {
        write("azurpilot/log/export/azurpilot_logs_old.zip")
        val scene = "error/export/$now/log.txt"
        write("azurpilot/log/$scene")

        assertEquals(setOf(scene), collectAzurPilot())
    }

    @Test
    fun `a missing AzurPilot log root is not an error`() {
        assertTrue(collectAzurPilot().isEmpty())
    }
}
