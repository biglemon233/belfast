package com.aliothmoon.maafw.privileged

import com.aliothmoon.maafw.domain.RemoteBackend
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.assertFalse
import org.junit.Test

class RemoteAccessStateTest {
    @Test
    fun `root selection requires app grant even when su is available`() {
        val ungranted = RemoteAccessState(rootAvailable = true)
        assertFalse(ungranted.canSelect(RemoteBackend.ROOT))
        assertTrue(ungranted.canSelect(RemoteBackend.SHIZUKU))
        assertTrue(ungranted.copy(rootGranted = true).canSelect(RemoteBackend.ROOT))
        assertFalse(RemoteAccessState().canSelect(RemoteBackend.ROOT))
    }
    @Test
    fun `switching between two granted backends remains an observable change`() {
        val granted = RemoteAccessState(
            shizukuAvailable = true,
            shizukuGranted = true,
            rootAvailable = true,
            rootGranted = true,
        )
        val shizuku = granted.copy(configuredBackend = RemoteBackend.SHIZUKU).configuredGrant()
        val root = granted.copy(configuredBackend = RemoteBackend.ROOT).configuredGrant()

        assertTrue(shizuku.second)
        assertTrue(root.second)
        assertNotEquals(shizuku, root)
    }
}
