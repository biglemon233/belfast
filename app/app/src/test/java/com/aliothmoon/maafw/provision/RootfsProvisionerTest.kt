package com.aliothmoon.maafw.provision

import android.app.Application
import io.mockk.mockk
import kotlinx.coroutines.test.TestScope
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.io.File
import java.nio.file.Files

class RootfsProvisionerTest {

    private lateinit var tempDir: File
    private lateinit var provisioner: RootfsProvisioner

    @Before
    fun setUp() {
        val base = File(System.getProperty("java.io.tmpdir"), "rootfs-provisioner-test").apply { mkdirs() }
        tempDir = Files.createTempDirectory(base.toPath(), "test-").toFile()
        val mockApp = mockk<Application>(relaxed = true)
        provisioner = RootfsProvisioner(mockApp, TestScope())
    }

    @After
    fun tearDown() {
        tempDir.deleteRecursively()
    }

    @Test
    fun `backupUserConfigs preserves user configurations and skips templates`() {
        val srcDir = File(tempDir, "old_rootfs")
        val dstDir = File(tempDir, "new_rootfs")

        val srcConfig = File(srcDir, "opt/alas/config").apply { mkdirs() }
        val dstConfig = File(dstDir, "opt/alas/config").apply { mkdirs() }

        // 旧 rootfs 中的用户配置
        File(srcConfig, "alas.json").writeText("{\"user\": \"main_task\"}")
        File(srcConfig, "instance2.json").writeText("{\"user\": \"sub_instance\"}")
        File(srcConfig, "deploy.yaml").writeText("WebuiPort: 22267")
        File(srcConfig, ".alasaos-source-v1").writeText("1")
        File(srcConfig, "template.json").writeText("{\"old\": \"template\"}")

        // 新 rootfs 中自带的模板
        File(dstConfig, "template.json").writeText("{\"new\": \"template\"}")

        provisioner.backupUserConfigs(srcDir, dstDir)

        // 验证用户配置均已迁移保留
        assertEquals("{\"user\": \"main_task\"}", File(dstConfig, "alas.json").readText())
        assertEquals("{\"user\": \"sub_instance\"}", File(dstConfig, "instance2.json").readText())
        assertEquals("WebuiPort: 22267", File(dstConfig, "deploy.yaml").readText())
        assertEquals("1", File(dstConfig, ".alasaos-source-v1").readText())

        // 验证新 rootfs 的 template.json 没有被旧 template 覆盖
        assertEquals("{\"new\": \"template\"}", File(dstConfig, "template.json").readText())
    }

    @Test
    fun `backup failure aborts migration and retains the original configuration`() {
        val old = File(tempDir, "old").apply { mkdirs() }
        val config = File(old, "opt/alas/config").apply { mkdirs() }
        File(config, "alas.json").writeText("user data")
        val replacement = File(tempDir, "replacement").apply { mkdirs() }
        File(replacement, "opt").writeText("blocks destination directory")
        org.junit.Assert.assertThrows(java.io.IOException::class.java) {
            provisioner.backupUserConfigs(old, replacement)
        }
        assertEquals("user data", File(config, "alas.json").readText())
    }

    @Test
    fun `isExtractComplete validates marker python and manifest version`() {
        val testRoot = File(tempDir, "mock_tmp").apply { mkdirs() }
        val expectedVersion = "0.2.0-ocr"

        // 1. 什么都没有时 -> false
        assertFalse(provisioner.isExtractComplete(testRoot, expectedVersion))

        // 2. 只有 .extract_complete 标记 -> false
        val marker = File(testRoot, ".extract_complete")
        marker.createNewFile()
        assertFalse(provisioner.isExtractComplete(testRoot, expectedVersion))

        // 3. 补上可执行的 python3 -> 但缺 manifest -> false
        val python = File(testRoot, "usr/bin/python3").apply {
            parentFile?.mkdirs()
            createNewFile()
            setExecutable(true)
        }
        assertFalse(provisioner.isExtractComplete(testRoot, expectedVersion))

        // 4. 补上版本不符的 manifest -> false
        val manifest = File(testRoot, "opt/alas/BUILD_MANIFEST").apply {
            parentFile?.mkdirs()
            writeText("{\"rootfs_version\": \"0.1.9\"}")
        }
        assertFalse(provisioner.isExtractComplete(testRoot, expectedVersion))

        // 5. 版本与 expectedVersion 匹配 -> true
        manifest.writeText("{\"rootfs_version\": \"0.2.0-ocr\"}")
        assertTrue(provisioner.isExtractComplete(testRoot, expectedVersion))

        // 6. 若 python3 被删除 -> false
        python.delete()
        assertFalse(provisioner.isExtractComplete(testRoot, expectedVersion))

        // 7. 在支持 POSIX 权限的系统上，若 python3 无法执行 -> false
        python.createNewFile()
        if (python.setExecutable(false) && !python.canExecute()) {
            assertFalse(provisioner.isExtractComplete(testRoot, expectedVersion))
        }
    }
}
