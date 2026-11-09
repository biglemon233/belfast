package com.aliothmoon.maafw.proot

import org.junit.Assert.*
import org.junit.Test

class StartupProgressTest {
    @Test
    fun `steps retain warnings and allow late readiness recovery`() {
        var state = StartupChecklistState().begin(StartupStep.ENVIRONMENT).finish(StepStatus.WARNING)
        state = state.begin(StartupStep.UPDATE).finish(StepStatus.SKIPPED)
            .begin(StartupStep.SESSION).begin(StartupStep.SERVICES).finish(StepStatus.FAILED)
        assertEquals(StepStatus.DONE, state.steps.first { it.step == StartupStep.SESSION }.status)
        state = state.finish(StepStatus.DONE)
        assertEquals(StepStatus.WARNING, state.steps.first { it.step == StartupStep.ENVIRONMENT }.status)
        assertEquals(StepStatus.SKIPPED, state.steps.first { it.step == StartupStep.UPDATE }.status)
        assertEquals(StepStatus.DONE, state.steps.first { it.step == StartupStep.SERVICES }.status)
        assertTrue(StartupChecklistState().steps.all { it.status == StepStatus.WAITING })
    }

    @Test
    fun `unknown total never invents a percentage`() {
        val unknown = TransferProgress.parse("AOS_PROGRESS download 2048 -1 1024\n")!!
        assertNull(unknown.fraction)
        assertEquals(2048L, unknown.downloadedBytes)
        assertEquals(0.5f, TransferProgress.parse("AOS_PROGRESS download 2048 4096 1000")!!.fraction!!, 0.0001f)
        assertNull(TransferProgress.parse("AOS_PROGRESS download 2048 1024 1000")!!.fraction)
        assertEquals(TransferStage.UNPACK, TransferProgress.parse("AOS_PROGRESS unpack 10 -1 0")!!.stage)
    }

    @Test
    fun `private urls raw errors and malformed snapshots are rejected`() {
        for (text in listOf(
            "fatal: https://user:FAKE_TEST_SECRET@example.invalid/repo",
            "AOS_PROGRESS download 1 2 3 https://user:FAKE_TEST_SECRET@example.invalid",
            "AOS_PROGRESS download -5 2 3",
            "AOS_PROGRESS download 9223372036854775808 2 3",
            "x".repeat(100000),
        )) assertNull(TransferProgress.parse(text))
    }

    @Test
    fun `command output is bounded and retains the last verdict`() {
        val buffer = BoundedOutput(64)
        val noise = "x".repeat(4096).toCharArray()
        repeat(30) { buffer.append(noise, noise.size) }
        val verdict = "\nUPDATED " + "a".repeat(40) + "\n"
        buffer.append(verdict.toCharArray(), verdict.length)
        assertEquals(64, buffer.snapshot().length)
        assertTrue(buffer.snapshot().endsWith(verdict))
    }
}
