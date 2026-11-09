package com.aliothmoon.maafw.privileged

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ShizukuBootstrapCommandTest {
    @Test
    fun `command uses Shizuku shell directly and quotes every external value`() {
        val command = ShizukuBootstrapCommand.build(
            apk = "/data/app/it's/base.apk",
            pkg = "io.example.aos",
            uid = 10_311,
            token = "token; touch /data/local/tmp/bad",
            starter = "example.Starter",
            service = "example.Service",
            log = "/data/user/0/io.example.aos/files/debug/start log.txt",
        )

        assertTrue(command.startsWith("CLASSPATH='/data/app/it'\"'\"'s/base.apk' exec /system/bin/app_process"))
        assertTrue(command.contains("'--token=token; touch /data/local/tmp/bad'"))
        assertTrue(command.endsWith(">'/data/user/0/io.example.aos/files/debug/start log.txt' 2>&1"))
        assertFalse(Regex("(^|[ ;])su([ ;]|$)").containsMatchIn(command))
    }
}
