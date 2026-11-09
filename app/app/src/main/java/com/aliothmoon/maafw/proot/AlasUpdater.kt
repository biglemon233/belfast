package com.aliothmoon.maafw.proot

import kotlinx.coroutines.CancellationException
import timber.log.Timber

/** Runs the deploy-selected source update; dependency sources and TLS are untouched. */
class AlasUpdater(
    private val exec: suspend (guestCmd: List<String>, timeoutMs: Long) -> ProotHost.ExecResult,
) {
    data class Result(val updated: Boolean, val summary: String)

    suspend fun update(): Result {
        val result = try {
            exec(listOf("/bin/bash", "seeds/alasaos_update.sh"), TIMEOUT_MS)
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            // Exceptions can contain command arguments, proxy credentials and URLs.
            Timber.w("hot update exec failed")
            return Result(false, "SKIPPED exec")
        }
        val safe = summarize(result)
        Timber.i("hot update: %s", safe.summary)
        return safe
    }

    companion object {
        // The probe can take 60s in addition to the bounded 240s fetch.
        const val TIMEOUT_MS = 360_000L
        private val commit = Regex("(UPDATED|UNCHANGED) ([0-9a-f]{40}|[0-9a-f]{64})( \\(cdn\\))?")
        private val failed = Regex("FAILED (deploy-config|workdir|invalid-branch|source-marker|git-init|remote|remote-ref|fetch|revision|checkout|reset|cdn-reset)")

        internal fun summarize(result: ProotHost.ExecResult): Result {
            if (result.timedOut) return Result(false, "SKIPPED timeout")
            // Last nonempty line only, bounded. Never print even a prefix-matching raw line.
            val line = result.output.takeLast(256).lineSequence().lastOrNull { it.isNotBlank() }
                ?: return Result(false, "SKIPPED no-verdict")
            if (result.exit == 0 && commit.matches(line)) return Result(line.startsWith("UPDATED "), line)
            if (result.exit != 0 && failed.matches(line)) return Result(false, "SKIPPED $line")
            return Result(false, "SKIPPED invalid-verdict")
        }
    }
}
