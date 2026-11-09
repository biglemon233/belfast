package com.aliothmoon.maafw.provision

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import java.nio.file.Files

class RuntimeArchitectureTest {
    @Test
    fun `device ELF wins over ARM app ABI on a translated emulator`() {
        val file = Files.createTempFile("native-device", ".elf").toFile()
        try {
            val bytes = ByteArray(20)
            byteArrayOf(127, 69, 76, 70, 2, 1).copyInto(bytes)
            bytes[18] = 62
            file.writeBytes(bytes)
            val device = RuntimeArchitecture.deviceAbi(file, listOf("arm64-v8a", "x86_64"))
            assertEquals("x86_64", device)
            assertEquals("x86_64", RuntimeArchitecture.runtimeAbi("universal", device))
            assertEquals("arm64-v8a", RuntimeArchitecture.runtimeAbi("arm64-v8a", device))
            assertEquals("arm64-v8a", RuntimeArchitecture.runtimeAbi("universal", device, "arm64-v8a"))
            bytes[18] = 183.toByte()
            file.writeBytes(bytes)
            assertEquals("arm64-v8a", RuntimeArchitecture.deviceAbi(file, listOf("x86_64", "arm64-v8a")))
            file.writeText("not an ELF")
            assertEquals("riscv64", RuntimeArchitecture.deviceAbi(file, listOf("riscv64")))
        } finally {
            file.delete()
        }
    }

    @Test
    fun `universal runtime filenames do not depend on the ABI chosen for the app`() {
        assertEquals("libproot_x86_64.so", RuntimeArchitecture.prootFileName("x86_64", true))
        assertEquals("libproot_loader_x86_64.so", RuntimeArchitecture.prootFileName("x86_64", true, true))
        assertEquals("libproot_arm64.so", RuntimeArchitecture.prootFileName("arm64-v8a", true))
        assertEquals("libproot.so", RuntimeArchitecture.prootFileName("x86_64", false))
        assertEquals("libproot-loader.so", RuntimeArchitecture.prootFileName("arm64-v8a", false, true))
    }

    @Test
    fun `universal Lite accepts both supported architectures and rejects others`() {
        org.junit.Assert.assertTrue(RuntimeArchitecture.supportsPackage("universal", "arm64-v8a"))
        org.junit.Assert.assertTrue(RuntimeArchitecture.supportsPackage("universal", "x86_64"))
        for (abi in listOf("x86", "armeabi-v7a", "riscv64", "")) {
            org.junit.Assert.assertFalse(RuntimeArchitecture.supportsPackage("universal", abi))
        }
    }
    @Test
    fun `ARM translation does not make an ARM runtime native on x86`() {
        assertThrows(IllegalArgumentException::class.java) {
            RuntimeArchitecture.requireCompatible("arm64-v8a", listOf("x86_64", "arm64-v8a"), "{}")
        }
    }

    @Test
    fun `legacy manifests are ARM and new manifests must match the package`() {
        RuntimeArchitecture.requireCompatible("arm64-v8a", listOf("arm64-v8a"), "{}")
        RuntimeArchitecture.requireCompatible("x86_64", listOf("x86_64"), """{"architecture":"amd64"}""")
        assertThrows(IllegalArgumentException::class.java) {
            RuntimeArchitecture.requireCompatible("x86_64", listOf("x86_64"), "{}")
        }
        assertThrows(IllegalArgumentException::class.java) {
            RuntimeArchitecture.requireCompatible("arm64-v8a", listOf("arm64-v8a"), """{"architecture":"amd64"}""")
        }
    }

    @Test
    fun `ELF machine rejects a mislabeled Python binary`() {
        val file = Files.createTempFile("runtime-arch", ".elf").toFile()
        try {
            val bytes = ByteArray(20)
            byteArrayOf(127, 69, 76, 70, 2, 1).copyInto(bytes)
            bytes[18] = 62
            file.writeBytes(bytes)
            assertEquals("amd64", RuntimeArchitecture.elfArchitecture(file))
            assertThrows(IllegalArgumentException::class.java) {
                RuntimeArchitecture.requireCompatible("arm64-v8a", listOf("arm64-v8a"), "{}", file)
            }
            bytes[18] = 183.toByte()
            file.writeBytes(bytes)
            assertEquals("arm64", RuntimeArchitecture.elfArchitecture(file))
        } finally {
            file.delete()
        }
    }
}
