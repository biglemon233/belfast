package com.aliothmoon.maafw.settings

import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.nio.file.Files

class LowPrecisionRecognitionTest {
    private fun withDirectory(test: (File) -> Unit) {
        val root = generateSequence(File(".").canonicalFile) { it.parentFile }
            .first { File(it, "rootfs/tests").isDirectory }
        val base = File(root, ".tmp/low-precision-tests").apply { mkdirs() }
        val dir = Files.createTempDirectory(base.toPath(), "marker-").toFile()
        try {
            test(dir)
        } finally {
            dir.deleteRecursively()
        }
    }

    @Test
    fun `explicit consent enables running tasks and disabling removes only the mode marker`() = withDirectory { dir ->
        val shm = File(dir, "shm")
        val marker = File(shm, LowPrecisionRecognition.MARKER)
        LowPrecisionRecognition.write(shm, false)
        assertFalse(shm.exists())
        LowPrecisionRecognition.write(shm, true)
        assertTrue(marker.isFile)
        val semaphore = File(shm, "sem.existing").apply { writeText("keep") }
        LowPrecisionRecognition.write(shm, true)
        LowPrecisionRecognition.write(shm, false)
        LowPrecisionRecognition.write(shm, false)
        assertFalse(marker.exists())
        assertTrue(semaphore.isFile)
    }

    @Test
    fun `an invalid shared memory path cannot silently enable relaxed matching`() = withDirectory { dir ->
        val file = File(dir, "not-a-directory").apply { writeText("keep") }
        assertThrows(IllegalStateException::class.java) { LowPrecisionRecognition.write(file, true) }
        assertTrue(file.isFile)
    }
}
