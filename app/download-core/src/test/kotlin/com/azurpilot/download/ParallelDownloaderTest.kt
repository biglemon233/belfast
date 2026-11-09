package com.azurpilot.download

import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import okhttp3.OkHttpClient
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.File
import java.io.IOException
import java.net.InetSocketAddress
import java.nio.file.Files
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

/**
 * 使用本地 HTTP 服务验证并行写入、镜像兼容回退和阻塞期间取消。
 *
 * Exercises parallel writes, mirror fallback, and cancellation during stalled local HTTP calls.
 */
class ParallelDownloaderTest {
    private val downloader = ParallelDownloader(OkHttpClient.Builder()
        .connectTimeout(3, TimeUnit.SECONDS)
        .readTimeout(3, TimeUnit.SECONDS)
        .build())

    @Test fun downloadsFourDisjointRangesAndReportsProgressOnCallerThread() {
        Fixture(Mode.RANGES).use { fixture ->
            val caller = Thread.currentThread()
            val progress = mutableListOf<Long>()
            downloader.download(fixture.url, fixture.target, DATA.size.toLong()) { done, total ->
                assertEquals(caller, Thread.currentThread())
                assertEquals(DATA.size.toLong(), total)
                progress += done
            }
            fixture.assertContent()
            assertEquals(5, fixture.ranges.size)
            assertEquals(4, fixture.maxActive.get())
            assertEquals(0L, progress.first())
            assertEquals(DATA.size.toLong(), progress.last())
            assertTrue(progress.zipWithNext().all { (left, right) -> left <= right })
            assertTrue(progress.all { it in 0..DATA.size.toLong() })
            assertEquals(4, fixture.ifRanges.count { it == ETAG })
        }
    }

    @Test fun reusesWholeProbeResponseWhenServerIgnoresRanges() {
        Fixture(Mode.IGNORE_ALL).use { fixture ->
            downloader.download(fixture.url, fixture.target, DATA.size.toLong())
            fixture.assertContent()
            assertEquals(listOf("bytes=0-0"), fixture.ranges.toList())
            assertEquals(0, fixture.wholeRequests.get())
        }
    }

    @Test fun smallFilesSkipRangeProbeAndOverwriteExistingFile() {
        Fixture(Mode.RANGES, 100_003).use { fixture ->
            fixture.target.writeBytes(DATA)
            downloader.download(fixture.url, fixture.target, fixture.size.toLong())
            fixture.assertContent()
            assertTrue(fixture.ranges.isEmpty())
            assertEquals(1, fixture.wholeRequests.get())
        }
    }

    @Test fun unknownSizesUseSingleConnectionAndKeepUnknownProgressTotal() {
        Fixture(Mode.RANGES, 100_003).use { fixture ->
            downloader.download(fixture.url, fixture.target) { _, total -> assertEquals(-1L, total) }
            fixture.assertContent()
            assertTrue(fixture.ranges.isEmpty())
        }
    }

    @Test fun fallsBackWhenOnePartIgnoresRange() = assertFallback(Mode.IGNORE_PART)
    @Test fun fallsBackWhenContentRangeIsMissing() = assertFallback(Mode.MISSING_RANGE)
    @Test fun fallsBackWhenRangeOffsetIsWrong() = assertFallback(Mode.WRONG_RANGE)
    @Test fun fallsBackWhenRangeTotalIsWrong() = assertFallback(Mode.WRONG_TOTAL)
    @Test fun fallsBackWhenContentLengthIsWrong() = assertFallback(Mode.WRONG_LENGTH)
    @Test fun fallsBackWhenPartIsTruncated() = assertFallback(Mode.TRUNCATED)
    @Test fun fallsBackWhenChunkedPartHasExcessBytes() = assertFallback(Mode.EXCESS)
    @Test fun fallsBackWhenRangeIsRejected() = assertFallback(Mode.REJECT_RANGE)
    @Test fun fallsBackWhenEntityChanges() = assertFallback(Mode.CHANGED_ETAG)
    @Test fun fallsBackWhenResponseIsEncoded() = assertFallback(Mode.ENCODED)
    @Test fun fallsBackWhenProbeIsMalformed() = assertFallback(Mode.BAD_PROBE)

    @Test fun acceptsValidChunkedRangeWithoutContentLength() {
        Fixture(Mode.CHUNKED).use { fixture ->
            downloader.download(fixture.url, fixture.target, DATA.size.toLong())
            fixture.assertContent()
            assertEquals(0, fixture.wholeRequests.get())
        }
    }

    @Test fun usesLastModifiedInsteadOfWeakEtag() {
        Fixture(Mode.WEAK_ETAG).use { fixture ->
            downloader.download(fixture.url, fixture.target, DATA.size.toLong())
            fixture.assertContent()
            assertEquals(4, fixture.ifRanges.count { it == LAST_MODIFIED })
        }
    }

    @Test fun followsRedirectAndUsesResolvedUrlForParts() {
        Fixture(Mode.RANGES).use { fixture ->
            downloader.download("${fixture.url}/redirect", fixture.target, DATA.size.toLong())
            fixture.assertContent()
            assertEquals(1, fixture.redirects.get())
        }
    }

    @Test fun mismatchedWholeFileSizeFailsAndDeletesPartial() {
        Fixture(Mode.RANGES, 100_003).use { fixture ->
            fixture.target.writeText("old partial")
            try {
                downloader.download(fixture.url, fixture.target, fixture.size + 1L)
                fail("Expected size mismatch")
            } catch (_: IOException) {
                assertFalse(fixture.target.exists())
            }
        }
    }

    @Test fun finalHttpFailureDeletesPartialAndDoesNotRetryForever() {
        Fixture(Mode.HTTP_ERROR).use { fixture ->
            try {
                downloader.download(fixture.url, fixture.target, DATA.size.toLong())
                fail("Expected HTTP failure")
            } catch (_: IOException) {
                assertFalse(fixture.target.exists())
                assertEquals(1, fixture.ranges.size)
                assertEquals(1, fixture.wholeRequests.get())
            }
        }
    }

    @Test fun abortsBeforeMakingAnyRequest() {
        Fixture(Mode.RANGES).use { fixture ->
            try {
                downloader.download(fixture.url, fixture.target, DATA.size.toLong(), { true })
                fail("Expected cancellation")
            } catch (_: DownloadCancelledException) {
                assertTrue(fixture.ranges.isEmpty())
                assertEquals(0, fixture.wholeRequests.get())
            }
        }
    }

    @Test fun cancelsWhileWaitingForResponseHeaders() = assertStalledCancellation(Mode.STALL_HEADERS)
    @Test fun cancelsAllWorkersDuringStalledSegmentReads() = assertStalledCancellation(Mode.STALL_PARTS)
    @Test fun cancelsDuringStalledSingleConnectionRead() =
        assertStalledCancellation(Mode.STALL_SINGLE, 100_003)

    @Test fun independentDownloadsDoNotCancelEachOther() {
        Fixture(Mode.STALL_HEADERS).use { stalled ->
            Fixture(Mode.RANGES, 100_003).use { healthy ->
                val abort = AtomicBoolean()
                val executor = Executors.newSingleThreadExecutor()
                try {
                    val pending = executor.submit<Boolean> {
                        try {
                            downloader.download(stalled.url, stalled.target, DATA.size.toLong(), abort::get)
                            false
                        } catch (_: DownloadCancelledException) { true }
                    }
                    assertTrue(stalled.stalled.await(2, TimeUnit.SECONDS))
                    abort.set(true)
                    downloader.download(healthy.url, healthy.target, healthy.size.toLong())
                    assertTrue(pending.get(2, TimeUnit.SECONDS))
                    healthy.assertContent()
                } finally { executor.shutdownNow() }
            }
        }
    }

    private fun assertFallback(mode: Mode) {
        Fixture(mode).use { fixture ->
            var fallbacks = 0
            val progress = mutableListOf<Long>()
            downloader.download(fixture.url, fixture.target, DATA.size.toLong(),
                onFallback = { fallbacks++ }, onProgress = { done, _ -> progress += done })
            fixture.assertContent()
            assertEquals(1, fallbacks)
            assertEquals(1, fixture.wholeRequests.get())
            assertTrue(progress.count { it == 0L } >= 2)
            assertEquals(DATA.size.toLong(), progress.last())
            // 返回后旧分段不得继续写入单连接重试所得文件。
            assertEquals(0, fixture.active.get())
        }
    }

    private fun assertStalledCancellation(mode: Mode, size: Int = DATA.size) {
        Fixture(mode, size).use { fixture ->
            val abort = AtomicBoolean()
            val executor = Executors.newSingleThreadExecutor()
            try {
                val pending = executor.submit<Boolean> {
                    try {
                        downloader.download(fixture.url, fixture.target, size.toLong(), abort::get)
                        false
                    } catch (_: DownloadCancelledException) { true }
                }
                assertTrue(fixture.stalled.await(2, TimeUnit.SECONDS))
                abort.set(true)
                assertTrue(pending.get(2, TimeUnit.SECONDS))
                assertFalse(fixture.target.exists())
                assertEquals(if (mode == Mode.STALL_SINGLE) 1 else 0, fixture.wholeRequests.get())
            } finally { executor.shutdownNow() }
        }
    }

    private enum class Mode {
        RANGES, IGNORE_ALL, IGNORE_PART, MISSING_RANGE, WRONG_RANGE, WRONG_TOTAL, WRONG_LENGTH,
        TRUNCATED, EXCESS, REJECT_RANGE, CHANGED_ETAG, ENCODED, BAD_PROBE, CHUNKED, WEAK_ETAG,
        HTTP_ERROR, STALL_HEADERS, STALL_PARTS, STALL_SINGLE,
    }

    private class Fixture(private val mode: Mode, val size: Int = DATA.size) : AutoCloseable {
        val target: File = Files.createTempDirectory("parallel-download-test").resolve("asset.part").toFile()
        val ranges = ConcurrentLinkedQueue<String>()
        val ifRanges = ConcurrentLinkedQueue<String>()
        val wholeRequests = AtomicInteger()
        val redirects = AtomicInteger()
        val active = AtomicInteger()
        val maxActive = AtomicInteger()
        val stalled = CountDownLatch(1)
        private val release = CountDownLatch(1)
        private val allParts = CountDownLatch(4)
        private val executor = Executors.newCachedThreadPool { runnable ->
            Thread(runnable, "download-test-http").apply { isDaemon = true }
        }
        private val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0).apply {
            this.executor = this@Fixture.executor
            createContext("/asset") { exchange ->
                try { serve(exchange) } catch (_: IOException) { } finally { exchange.close() }
            }
            start()
        }
        val url = "http://127.0.0.1:${server.address.port}/asset"

        fun assertContent() = assertArrayEquals(DATA.copyOf(size), target.readBytes())

        private fun serve(exchange: HttpExchange) {
            assertEquals("identity", exchange.requestHeaders.getFirst("Accept-Encoding"))
            if (exchange.requestURI.path.endsWith("/redirect")) {
                redirects.incrementAndGet()
                exchange.responseHeaders.add("Location", url)
                exchange.sendResponseHeaders(302, -1)
                return
            }
            val range = exchange.requestHeaders.getFirst("Range")
            if (range == null) wholeRequests.incrementAndGet() else ranges.add(range)
            if (mode == Mode.HTTP_ERROR) {
                exchange.sendResponseHeaders(503, -1)
                return
            }
            if (mode == Mode.STALL_HEADERS) {
                stalled.countDown()
                release.await(5, TimeUnit.SECONDS)
                return
            }
            val isPart = range != null && range != "bytes=0-0"
            if (range == null || mode == Mode.IGNORE_ALL || (isPart && mode == Mode.IGNORE_PART)) {
                exchange.sendResponseHeaders(200, size.toLong())
                if (mode == Mode.STALL_SINGLE) {
                    exchange.responseBody.write(DATA, 0, 1)
                    exchange.responseBody.flush()
                    stalled.countDown()
                    release.await(5, TimeUnit.SECONDS)
                } else exchange.responseBody.write(DATA, 0, size)
                return
            }
            if (isPart && mode == Mode.REJECT_RANGE) {
                exchange.sendResponseHeaders(416, -1)
                return
            }
            val offsets = range.removePrefix("bytes=").split('-').map { it.toInt() }
            val start = offsets[0]
            val end = offsets[1]
            val length = end - start + 1
            exchange.requestHeaders.getFirst("If-Range")?.let { ifRanges.add(it) }
            val bad = isPart || mode == Mode.BAD_PROBE
            if (!bad || mode != Mode.MISSING_RANGE) {
                val rangeStart = if (bad && mode in listOf(Mode.WRONG_RANGE, Mode.BAD_PROBE)) start + 1 else start
                val rangeTotal = if (bad && mode == Mode.WRONG_TOTAL) size + 1 else size
                exchange.responseHeaders.add("Content-Range", "bytes $rangeStart-$end/$rangeTotal")
            }
            exchange.responseHeaders.add("ETag", when {
                mode == Mode.WEAK_ETAG -> "W/$ETAG"
                isPart && mode == Mode.CHANGED_ETAG -> "\"changed\""
                else -> ETAG
            })
            exchange.responseHeaders.add("Last-Modified", LAST_MODIFIED)
            if (isPart && mode == Mode.ENCODED) exchange.responseHeaders.add("Content-Encoding", "gzip")
            val chunked = mode == Mode.CHUNKED || (isPart && mode == Mode.EXCESS)
            val declared = if (isPart && mode == Mode.WRONG_LENGTH) length + 1L else length.toLong()
            exchange.sendResponseHeaders(206, if (chunked) 0 else declared)
            if (isPart) {
                maxActive.accumulateAndGet(active.incrementAndGet(), ::maxOf)
                allParts.countDown()
            }
            try {
                if (isPart && mode in listOf(Mode.RANGES, Mode.STALL_PARTS)) {
                    allParts.await(2, TimeUnit.SECONDS)
                }
                if (isPart && mode == Mode.STALL_PARTS) {
                    exchange.responseBody.write(DATA, start, 1)
                    exchange.responseBody.flush()
                    stalled.countDown()
                    release.await(5, TimeUnit.SECONDS)
                } else {
                    val actual = if (isPart && mode == Mode.TRUNCATED) length / 2 else length
                    exchange.responseBody.write(DATA, start, actual)
                    if (isPart && mode == Mode.EXCESS) exchange.responseBody.write(42)
                }
            } finally {
                if (isPart) active.decrementAndGet()
            }
        }

        override fun close() {
            release.countDown()
            server.stop(0)
            executor.shutdownNow()
            target.parentFile.deleteRecursively()
        }
    }

    private companion object {
        const val ETAG = "\"release-v1\""
        const val LAST_MODIFIED = "Wed, 14 Oct 2026 00:00:00 GMT"
        val DATA = ByteArray(8 * 1024 * 1024 + 19) { ((it * 31 + it / 65536) % 251).toByte() }
    }
}
