package com.aliothmoon.maafw.provision

import android.app.Application
import io.mockk.mockk
import kotlinx.coroutines.test.TestScope
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.io.File
import java.nio.file.Files

class RootfsSourceTest {

    private lateinit var tempDir: File
    private lateinit var provisioner: RootfsProvisioner

    @Before
    fun setUp() {
        val base = File(System.getProperty("java.io.tmpdir"), "rootfs-source-test").apply { mkdirs() }
        tempDir = Files.createTempDirectory(base.toPath(), "test-").toFile()
        val mockApp = mockk<Application>(relaxed = true)
        provisioner = RootfsProvisioner(mockApp, TestScope())
    }

    // ── URL 红线校验 ──

    @Test
    fun `x86 Lite downloads its own runtime and paired manifest`() {
        val urls = RootfsDownloadSource(RootfsSourceKind.GITHUB).candidateUrls("x86_64")
        assertEquals(listOf("${RootfsSource.GITHUB_DOWNLOAD_BASE}/rootfs-amd64.tar.xz"), urls)
        assertEquals("${RootfsSource.GITHUB_DOWNLOAD_BASE}/BUILD_MANIFEST-amd64", RootfsSource.manifestUrl(urls.single()))
        val custom = RootfsDownloadSource(RootfsSourceKind.CUSTOM, "https://example.com/custom.tar.xz")
        assertEquals(listOf(custom.customUrl), custom.candidateUrls("x86_64"))
        assertEquals("https://example.com/BUILD_MANIFEST", RootfsSource.manifestUrl(custom.customUrl))
    }

    @Test
    fun `validator accepts github release of this repository`() {
        val result = RootfsSourceValidator.validate(RootfsSource.GITHUB_ARCHIVE_URL)
        assertTrue(result is RootfsSourceValidator.Result.Valid)
        assertEquals(RootfsSource.GITHUB_ARCHIVE_URL, (result as RootfsSourceValidator.Result.Valid).url)
    }

    @Test
    fun `default runtime URLs use latest version release without a separate prerelease`() {
        assertEquals("https://github.com/Dreamry2C/ALAS-AOS/releases/latest/download/rootfs.tar.xz",
            RootfsSource.GITHUB_ARCHIVE_URL)
        assertEquals("https://github.com/Dreamry2C/ALAS-AOS/releases/latest/download/BUILD_MANIFEST",
            RootfsSource.GITHUB_MANIFEST_URL)
        assertEquals(RootfsSource.GITHUB_MANIFEST_URL,
            RootfsSource.manifestUrl(RootfsSource.GITHUB_ARCHIVE_URL))
    }

    @Test
    fun `validator accepts custom direct links over http and https`() {
        for (url in listOf(
            "https://mirror.example.com/pkg/rootfs.tar.xz",
            "http://192.168.1.10:8080/rootfs.tar.xz",
            "https://example.com/path/to/file?name=rootfs.tar.xz&raw=1",
        )) {
            assertTrue("should accept $url", RootfsSourceValidator.validate(url) is RootfsSourceValidator.Result.Valid)
        }
    }

    @Test
    fun `validator rejects empty and malformed urls`() {
        for (url in listOf("", "   ", "not a url", "github.com/x/rootfs.tar.xz")) {
            val result = RootfsSourceValidator.validate(url)
            assertTrue("should reject '$url'", result is RootfsSourceValidator.Result.Rejected)
            assertEquals(RootfsSourceValidator.Reason.INVALID_URL, (result as RootfsSourceValidator.Result.Rejected).reason)
        }
    }

    @Test
    fun `validator rejects non http schemes`() {
        val result = RootfsSourceValidator.validate("ftp://example.com/rootfs.tar.xz")
        assertTrue(result is RootfsSourceValidator.Result.Rejected)
        assertEquals(RootfsSourceValidator.Reason.UNSUPPORTED_SCHEME, (result as RootfsSourceValidator.Result.Rejected).reason)
    }

    @Test
    fun `validator has no host specific restriction for domestic sources`() {
        for (url in listOf(
            "https://git.lyoko.io/AzurLaneAutoScript",
            "http://git.lyoko.io/AzurLaneAutoScript/archive/master.tar.gz",
        )) {
            val result = RootfsSourceValidator.validate(url)
            assertTrue("valid HTTP URL: $url", result is RootfsSourceValidator.Result.Valid)
        }
    }

    @Test
    fun `validator checks URL structure rather than repository names`() {
        for (url in listOf(
            "https://github.com/LmeSzinc/AzurLaneAutoScript",
            "https://github.com/LmeSzinc/AzurLaneAutoScript.git",
            "https://github.com/LmeSzinc/AzurLaneAutoScript/releases/tag/v1.0",
            "https://GITHUB.COM/LMESZINC/AZURLANEAUTOSCRIPT",
            "https://gitee.com/LmeSzinc/AzurLaneAutoScript",
            "https://gitee.com/lmeszinc/azurlaneautoscript.git",
            "https://raw.githubusercontent.com/LmeSzinc/AzurLaneAutoScript/master/README.md",
            "https://codeload.github.com/LmeSzinc/AzurLaneAutoScript/tar.gz/refs/tags/master",
        )) {
            val result = RootfsSourceValidator.validate(url)
            assertTrue("valid HTTP URL: $url", result is RootfsSourceValidator.Result.Valid)
        }
    }

    @Test
    fun `validator allows release downloads even if repo is named AzurLaneAutoScript`() {
        for (url in listOf(
            "https://github.com/Dreamry2C/ALAS-AOS/releases/download/rootfs-latest/rootfs.tar.xz",
            "https://github.com/LmeSzinc/AzurLaneAutoScript/releases/download/v1.0/rootfs.tar.xz",
            "https://gitee.com/someone/AzurLaneAutoScript/releases/download/tag1/rootfs.tar.xz",
            "https://gitee.com/someone/other-project/raw/master/rootfs.tar.xz",
        )) {
            assertTrue("should accept release download asset: $url", RootfsSourceValidator.validate(url) is RootfsSourceValidator.Result.Valid)
        }
    }

    // ── 镜像拼接 ──

    @Test
    fun `composeMirrorUrl appends slash when missing`() {
        assertEquals(
            "https://ghfast.top/https://github.com/a/b",
            RootfsSource.composeMirrorUrl("https://ghfast.top", "https://github.com/a/b"),
        )
        assertEquals(
            "https://ghfast.top/https://github.com/a/b",
            RootfsSource.composeMirrorUrl("https://ghfast.top/", "https://github.com/a/b"),
        )
        assertEquals(
            "https://ghfast.top/https://github.com/a/b",
            RootfsSource.composeMirrorUrl("  https://ghfast.top  ", "https://github.com/a/b"),
        )
    }

    @Test
    fun `splitMirrors keeps order and drops blanks`() {
        assertEquals(
            listOf("https://a.top/", "https://b.com/", "https://c.io/"),
            RootfsSource.splitMirrors(" https://a.top/ , https://b.com/ ,, https://c.io/ ,"),
        )
        assertTrue(RootfsSource.splitMirrors(" , ,").isEmpty())
    }

    // ── 候选源顺序 ──

    @Test
    fun `github source without mirror falls back to direct link only`() {
        val source = RootfsDownloadSource(RootfsSourceKind.GITHUB, useMirror = true, mirrors = emptyList())
        assertEquals(listOf(RootfsSource.GITHUB_ARCHIVE_URL), source.candidateUrls())
    }

    @Test
    fun `github source with mirrors tries mirrors first then direct`() {
        val source = RootfsDownloadSource(
            RootfsSourceKind.GITHUB,
            useMirror = true,
            mirrors = listOf("https://ghfast.top/", "https://gh-proxy.com/"),
        )
        val candidates = source.candidateUrls()
        assertEquals(
            listOf(
                "https://ghfast.top/${RootfsSource.GITHUB_ARCHIVE_URL}",
                "https://gh-proxy.com/${RootfsSource.GITHUB_ARCHIVE_URL}",
                RootfsSource.GITHUB_ARCHIVE_URL,
            ),
            candidates,
        )
        // 清单与包同目录：镜像候选同样带上 GitHub 原始路径
        assertEquals(
            candidates.map { RootfsSource.manifestUrl(it) },
            listOf(
                "https://ghfast.top/${RootfsSource.GITHUB_MANIFEST_URL}",
                "https://gh-proxy.com/${RootfsSource.GITHUB_MANIFEST_URL}",
                RootfsSource.GITHUB_MANIFEST_URL,
            ),
        )
    }

    @Test
    fun `custom source ignores mirror setting and keeps single candidate`() {
        val source = RootfsDownloadSource(
            RootfsSourceKind.CUSTOM,
            customUrl = " https://example.com/pkg/rootfs.tar.xz ",
            useMirror = true,
            mirrors = listOf("https://ghfast.top/"),
        )
        assertEquals(listOf("https://example.com/pkg/rootfs.tar.xz"), source.candidateUrls())
        assertEquals(
            listOf("https://example.com/pkg/BUILD_MANIFEST"),
            source.candidateUrls().map { RootfsSource.manifestUrl(it) },
        )
    }

    // ── 下载续跑入口（下载目录状态机） ──

    @Test
    fun `downloadedPackage returns null when nothing downloaded`() {
        assertNull(provisioner.downloadedPackage(tempDir))
    }

    @Test
    fun `downloadedPackage accepts manifest and xz archive pair`() {
        val archive = File(tempDir, "rootfs.tar.xz")
        archive.writeBytes(byteArrayOf(0xFD.toByte(), 0x37, 0x7A, 0x58, 0x5A, 0x00) + ByteArray(64))
        File(tempDir, "BUILD_MANIFEST").writeText("{\"rootfs_version\": \"0.2.0-ocr\"}")

        val result = provisioner.downloadedPackage(tempDir)
        assertTrue(result != null)
        assertEquals("0.2.0-ocr", result?.first)
        assertEquals(archive, result?.second)
    }

    @Test
    fun `downloadedPackage rejects non xz content manifest without version or missing files`() {
        // 下载到的是一页 HTML（假包）
        val archive = File(tempDir, "rootfs.tar.xz")
        archive.writeText("<html>not found</html>".repeat(4))
        File(tempDir, "BUILD_MANIFEST").writeText("{\"rootfs_version\": \"0.2.0-ocr\"}")
        assertNull(provisioner.downloadedPackage(tempDir))

        // 清单缺版本号
        archive.writeBytes(byteArrayOf(0xFD.toByte(), 0x37, 0x7A, 0x58, 0x5A, 0x00) + ByteArray(64))
        File(tempDir, "BUILD_MANIFEST").writeText("{\"alas_commit\": \"deadbeef\"}")
        assertNull(provisioner.downloadedPackage(tempDir))

        // 缺清单
        File(tempDir, "BUILD_MANIFEST").delete()
        assertNull(provisioner.downloadedPackage(tempDir))
    }

    @Test
    fun `manifest version parsing tolerates extra keys and unknown version`() {
        val manifest = "{\"rootfs_version\": \"9.9.9-lite\", \"build_time_utc\": \"x\"}"
        assertEquals("9.9.9-lite", provisioner.readExtractedVersion(tempDir.apply {
            File(this, "opt/alas/BUILD_MANIFEST").apply { parentFile?.mkdirs() }.writeText(manifest)
        }))
        assertFalse(provisioner.isExtractComplete(tempDir, "9.9.9-lite"))
    }
}
