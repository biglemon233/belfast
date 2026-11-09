package com.aliothmoon.maafw.proot

import org.junit.Assert.*
import org.junit.Test

class AlasUpdaterVerdictTest {
    private val sha = "a".repeat(40)
    private fun summary(output: String, exit: Int? = 0, timedOut: Boolean = false) =
        AlasUpdater.summarize(ProotHost.ExecResult(exit, output, timedOut))

    @Test fun onlyExactSuccessfulVerdictsAreAccepted() {
        assertTrue(summary("PHASE fetch\nUPDATED $sha\n").updated)
        assertEquals("UNCHANGED $sha (cdn)", summary("UNCHANGED $sha (cdn)\n").summary)
        assertFalse(summary("UPDATED $sha", exit = 1).updated)
        assertEquals("SKIPPED invalid-verdict",
            summary("UNCHANGED backoff-until-tomorrow current=$sha").summary)
    }

    @Test fun rawErrorsCredentialsAndMalformedPrefixesAreNeverEchoed() {
        val secret = "https://u:FAKE_SECRET@example.invalid/repo"
        for (output in listOf("FAILED $secret", "UPDATED $secret", "UPDATED $sha $secret",
                              "UPDATEDLY $sha", "UPDATED $sha\nraw $secret", "raw $secret")) {
            val result = summary(output, exit = 1)
            assertFalse(result.updated)
            assertFalse(result.summary.contains("FAKE_SECRET"))
            assertEquals("SKIPPED invalid-verdict", result.summary)
        }
    }

    @Test fun oversizedSingleLineCannotSmuggleAValidSuffix() {
        assertFalse(summary("x".repeat(10000) + "UPDATED $sha").updated)
    }

    @Test fun timeoutAndFixedFailuresAreSafe() {
        assertEquals("SKIPPED timeout", summary("FAKE_SECRET", timedOut = true).summary)
        assertEquals("SKIPPED FAILED fetch", summary("FAILED fetch\n", exit = 1).summary)
        assertEquals("SKIPPED FAILED deploy-config", summary("FAILED deploy-config\n", exit = 1).summary)
        assertEquals("SKIPPED no-verdict", summary("").summary)
    }
}
