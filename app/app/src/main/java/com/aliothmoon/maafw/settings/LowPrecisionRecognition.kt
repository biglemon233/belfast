package com.aliothmoon.maafw.settings

import java.io.File

/** The existing /dev/shm bind lets running ALAS tasks observe a setting change. */
object LowPrecisionRecognition {
    const val MARKER = "alasaos-low-precision"

    fun write(shmDir: File, enabled: Boolean) {
        val marker = File(shmDir, MARKER)
        if (enabled) {
            check(shmDir.isDirectory || shmDir.mkdirs()) { "Cannot prepare recognition settings" }
            check(marker.isFile || marker.createNewFile()) { "Cannot enable low precision recognition" }
        } else {
            check(!marker.exists() || marker.delete()) { "Cannot disable low precision recognition" }
        }
    }
}
