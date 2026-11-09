package com.aliothmoon.maafw.proot

import java.io.File
import java.io.RandomAccessFile

/** Independent steps, not a made-up overall percentage. */
enum class StartupStep {
    RUNTIME, CLEANUP, RESTORE, OVERLAY, ENVIRONMENT, CONFIG, UPDATE, PATCHES, ARGUMENTS, SESSION, SERVICES,
}

enum class StepStatus { WAITING, ACTIVE, DONE, WARNING, FAILED, SKIPPED }

data class StartupStepState(val step: StartupStep, val status: StepStatus = StepStatus.WAITING)

data class StartupChecklistState(
    val steps: List<StartupStepState> = StartupStep.entries.map { StartupStepState(it) },
    val current: StartupStep? = null,
) {
    fun begin(step: StartupStep): StartupChecklistState = copy(
        current = step,
        steps = steps.map {
            when {
                it.step == step -> it.copy(status = StepStatus.ACTIVE)
                it.status == StepStatus.ACTIVE -> it.copy(status = StepStatus.DONE)
                else -> it
            }
        },
    )

    fun finish(status: StepStatus): StartupChecklistState = copy(
        steps = steps.map { if (it.step == current) it.copy(status = status) else it },
    )
}

enum class TransferStage { DOWNLOAD, UNPACK }

data class TransferProgress(
    val stage: TransferStage,
    val downloadedBytes: Long,
    val totalBytes: Long?,
    val bytesPerSecond: Long,
) {
    val fraction: Float?
        get() = totalBytes?.let { (downloadedBytes.toDouble() / it).coerceIn(0.0, 1.0).toFloat() }

    companion object {
        private const val MAX_BYTES = 512
        private val FORMAT = Regex("""AOS_PROGRESS (download|unpack) (\d{1,19}) (-1|\d{1,19}) (\d{1,19})""")

        fun parse(text: String): TransferProgress? {
            if (text.length > MAX_BYTES) return null
            val match = FORMAT.matchEntire(text.trim()) ?: return null
            val done = match.groupValues[2].toLongOrNull() ?: return null
            val total = match.groupValues[3].toLongOrNull() ?: return null
            val speed = match.groupValues[4].toLongOrNull() ?: return null
            return TransferProgress(
                if (match.groupValues[1] == "download") TransferStage.DOWNLOAD else TransferStage.UNPACK,
                done,
                total.takeIf { it > 0 && it >= done },
                speed,
            )
        }

        /** The file is an atomic numeric snapshot, never raw git stderr or an unbounded tail. */
        fun read(file: File): TransferProgress? = runCatching {
            RandomAccessFile(file, "r").use { input ->
                val length = input.length()
                if (length !in 1..MAX_BYTES.toLong()) return null
                val bytes = ByteArray(length.toInt())
                input.readFully(bytes)
                parse(bytes.toString(Charsets.UTF_8))
            }
        }.getOrNull()
    }
}

/** Bounded and synchronized because the process reader can outlive its join timeout. */
internal class BoundedOutput(private val limit: Int = 64 * 1024) {
    private val text = StringBuilder()

    @Synchronized
    fun append(chars: CharArray, count: Int) {
        text.append(chars, 0, count)
        if (text.length > limit) text.delete(0, text.length - limit)
    }

    @Synchronized
    fun snapshot(): String = text.toString()
}
