package com.aliothmoon.maafw.settings

import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import java.io.File
import java.nio.file.Files
import java.util.concurrent.Callable
import java.util.concurrent.Executors

class AlasSourceRepositoryTest {
    private lateinit var dir: File
    private lateinit var deploy: File
    private lateinit var repository: AlasSourceRepository

    @Before fun setUp() {
        val root = generateSequence(File(System.getProperty("user.dir")).absoluteFile) { it.parentFile }
            .first { File(it, "rootfs/seeds").isDirectory }
        val base = File(root, ".tmp/source-kotlin-tests").apply { mkdirs() }
        dir = Files.createTempDirectory(base.toPath(), "source-").toFile()
        deploy = File(dir, "config/deploy.yaml").apply { parentFile!!.mkdirs() }
        repository = AlasSourceRepository(dir)
        deploy.writeText(document(AlasSource.DOMESTIC, "master"))
    }

    @After fun tearDown() { dir.deleteRecursively() }

    private fun document(repo: String, branch: String) =
        "# retain\r\nDeploy:\r\n  Git:\r\n    Repository: $repo  # source\r\n    Branch: $branch # branch\r\n" +
            "  Python:\r\n    PypiMirror: keep\r\n  Webui:\r\n    Password: private\r\n"

    @Test fun savePreservesCommentsOtherKeysAndCrlf() {
        val before = repository.read()
        repository.save(before, AlasSource("https://gitee.com/custom/repo", "cloud"))
        assertEquals(document("https://gitee.com/custom/repo", "cloud"), deploy.readText())
        assertEquals("cloud", repository.read().source.branch)
    }

    @Test fun noOpSaveDoesNotRewriteDeploy() {
        val before = deploy.lastModified()
        val snapshot = repository.read()
        repository.save(snapshot, snapshot.source)
        assertEquals(before, deploy.lastModified())
        assertArrayEquals(snapshot.bytes, deploy.readBytes())
    }

    @Test fun customBranchOnDomesticSourceAlsoWinsMigration() {
        deploy.writeText(document(AlasSource.DOMESTIC, "cloud"))
        repository.migrateLegacy(AlasSource.GITHUB, "master")
        assertEquals("cloud", repository.read().source.branch)
        assertEquals(AlasSource.DOMESTIC, repository.read().source.repository)
    }

    @Test fun staleSnapshotDoesNotOverwriteExternalChange() {
        val before = repository.read()
        val external = deploy.readText().replace("Password: private", "Password: external")
        deploy.writeText(external)
        assertThrows(AlasSourceRepository.Conflict::class.java) {
            repository.save(before, AlasSource(AlasSource.GITHUB, "cloud"))
        }
        assertEquals(external, deploy.readText())
    }

    @Test fun concurrentWritersCannotBothSaveTheSameRevision() {
        val expected = repository.read()
        val executor = Executors.newFixedThreadPool(2)
        try {
            val results = executor.invokeAll(listOf("cloud", "feature/test").map { branch -> Callable {
                try {
                    AlasSourceRepository(dir).save(expected, AlasSource(AlasSource.GITHUB, branch))
                    true
                } catch (_: AlasSourceRepository.Conflict) { false }
            } }).map { it.get() }
            assertEquals(1, results.count { it })
        } finally { executor.shutdownNow() }
    }

    @Test fun legacyMigratesOnlyOnceEvenAfterReturningToDefaults() {
        repository.migrateLegacy("https://gitee.com/custom/repo", "cloud")
        assertEquals("cloud", repository.read().source.branch)
        repository.save(repository.read(), AlasSource(AlasSource.DOMESTIC, "master"))
        repository.migrateLegacy("https://gitee.com/stale/repo", "stale")
        assertEquals(AlasSource.DOMESTIC, repository.read().source.repository)
        assertEquals("master", repository.read().source.branch)
    }

    @Test fun existingCustomDeployWinsOverLegacy() {
        deploy.writeText(document("https://gitee.com/keep/repo", "cloud"))
        val before = deploy.readBytes()
        repository.migrateLegacy(AlasSource.GITHUB, "master")
        assertArrayEquals(before, deploy.readBytes())
        assertTrue(File(dir, "config/.alasaos-source-v1").isFile)
    }

    @Test fun missingDeployDoesNotCreateMigrationMarker() {
        deploy.delete()
        assertThrows(IllegalStateException::class.java) { repository.migrateLegacy("", "master") }
        assertFalse(File(dir, "config/.alasaos-source-v1").exists())
        assertFalse(deploy.exists())
    }

    @Test fun parserPreservesQuotedValuesAndInlineComments() {
        val text = document("'https://user:token@example.invalid/repo'", "'feature/user''s'")
        assertEquals("feature/user's", AlasDeployCodec.read(text).branch)
        val changed = AlasDeployCodec.replace(text, AlasSource(AlasSource.GITHUB, "#topic"))
        assertEquals(document("'${AlasSource.GITHUB}'", "'#topic'"), changed)
        assertEquals("#topic", AlasDeployCodec.read(changed).branch)
    }

    @Test fun gitValidSpecialBranchNamesRoundTrip() {
        for (branch in listOf("cloud", "feature/cloud", "中文", "#topic", "True", "@", "user's")) {
            assertTrue(AlasSource.validBranch(branch))
            repository.save(repository.read(), AlasSource(AlasSource.GITHUB, branch))
            assertEquals(branch, repository.read().source.branch)
        }
    }

    @Test fun invalidAndAmbiguousSourceIsRejectedWithoutWrite() {
        val before = deploy.readBytes()
        for (branch in listOf("-bad", "a..b", "a.lock", "a//b", "a b", "a\\b", "a@{b")) {
            assertThrows(IllegalArgumentException::class.java) {
                repository.save(repository.read(), AlasSource(AlasSource.GITHUB, branch))
            }
        }
        assertArrayEquals(before, deploy.readBytes())
        assertThrows(IllegalArgumentException::class.java) {
            AlasDeployCodec.read(deploy.readText().replace("  Python:", "    Branch: duplicate\r\n  Python:"))
        }
    }

    @Test fun unrelatedSourceKeysArePreserved() {
        val extra = "Other:\r\n  Repository: unrelated\r\n  Branch: keep\r\n"
        deploy.appendText(extra)
        repository.save(repository.read(), AlasSource(AlasSource.GITHUB, "cloud"))
        assertTrue(deploy.readText().endsWith(extra))
        assertEquals("cloud", repository.read().source.branch)
    }

    @Test fun otherBlocksAndLiteralTextCannotSupplyGitConfiguration() {
        for (text in listOf(
            "Deploy:\n  Other:\n    Repository: ${AlasSource.GITHUB}\n    Branch: cloud\n",
            "Deploy:\n  Note: |\n    Git:\n      Repository: ${AlasSource.GITHUB}\n      Branch: cloud\n",
        )) assertThrows(IllegalArgumentException::class.java) { AlasDeployCodec.read(text) }
    }

    @Test fun unicodeEscapesFromYamlEditorsAreDecoded() {
        assertEquals("中文", AlasDeployCodec.read(document(AlasSource.GITHUB, "\"\\u4e2d\\u6587\"")).branch)
    }

    @Test fun privateModelsDoNotPrintCredentials() {
        deploy.writeText(document("https://u:FAKE_SECRET@example.invalid/repo", "cloud"))
        val snapshot = repository.read()
        assertFalse(snapshot.toString().contains("FAKE_SECRET"))
        assertFalse(snapshot.source.toString().contains("FAKE_SECRET"))
    }

    @Test fun readThemeReturnsDefaultWhenMissing() {
        assertEquals("default", repository.readTheme())
    }

    @Test fun updateThemeInsertsUnderWebuiWhenMissing() {
        assertTrue(repository.updateTheme("dark"))
        assertEquals("dark", repository.readTheme())
        assertTrue(deploy.readText().contains("Theme: dark"))
    }

    @Test fun updateThemeModifiesExistingTheme() {
        deploy.writeText(document(AlasSource.GITHUB, "master") + "    Theme: default\r\n")
        assertEquals("default", repository.readTheme())
        assertTrue(repository.updateTheme("dark"))
        assertEquals("dark", repository.readTheme())
        // 幂等
        assertTrue(repository.updateTheme("dark"))
        assertEquals("dark", repository.readTheme())
        // 切回 default
        assertTrue(repository.updateTheme("default"))
        assertEquals("default", repository.readTheme())
    }

    @Test fun updateThemeRejectsInvalidTheme() {
        assertThrows(IllegalArgumentException::class.java) {
            repository.updateTheme("invalid_theme")
        }
    }
}
