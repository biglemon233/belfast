package com.azurpilot.download

import okhttp3.Call
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import java.io.File
import java.io.IOException
import java.io.RandomAccessFile
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.ExecutionException
import java.util.concurrent.ExecutorCompletionService
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.Future
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

/**
 * 表示调用方主动取消下载，区别于需要回退的网络异常。
 *
 * Identifies caller cancellation separately from network failures that allow fallback.
 */
class DownloadCancelledException : IOException("下载已取消")

/**
 * 通过 OkHttp 下载完整文件，对已知大小的大文件使用四条并行连接。
 *
 * 每次调用拥有独立的工作线程和连接集合；返回前等待所有写入结束。分段失败后从头单连接
 * 重试一次。调用方仍需校验文件摘要，同一个目标文件不得同时下载。阻塞 API 应在 IO 线程调用。
 *
 * Downloads whole files through OkHttp, using four parallel connections for large known sizes.
 *
 * Each invocation owns its workers and calls and waits for all writes before returning. A failed
 * segmented attempt restarts once over a single connection. Callers must still verify checksums
 * and must not download to the same target concurrently. Invoke this blocking API on an IO thread.
 *
 * @param client 连接与读取超时由此客户端决定 / client providing connect and read timeouts
 */
class ParallelDownloader(
    private val client: OkHttpClient = OkHttpClient.Builder()
        // HTTP/2 会把多个请求复用到同一条连接；下载分段需要独立连接分摊限速。
        .protocols(listOf(Protocol.HTTP_1_1))
        .connectTimeout(20, TimeUnit.SECONDS)
        .readTimeout(120, TimeUnit.SECONDS)
        .build(),
) {
    /**
     * 覆盖下载到 [target]，并在调用线程串行报告进度。
     *
     * 已知大小至少 8 MiB 时先请求首字节；服务器忽略 Range 时直接复用该完整响应。
     * 只有精确匹配的 206 响应才能分段写入。取消每 100 ms 检查一次并关闭活动请求。
     * 回退时进度归零，所有失败或取消都会删除半成品。
     *
     * Overwrites [target] and reports progress serially on the calling thread.
     *
     * Known sizes of at least 8 MiB trigger a one-byte probe; a server ignoring Range has its whole
     * response reused. Only exactly matching 206 responses may write segments. Cancellation is
     * polled every 100 ms and cancels active calls. Fallback resets progress; failures and
     * cancellation delete the partial file.
     *
     * @param totalBytes 清单大小，未知传 -1；也是进度分母 / manifest size, or -1; progress total
     * @param onFallback 单连接重试前在调用线程通知 / called on the caller thread before fallback
     * @throws DownloadCancelledException 主动取消时 / when the caller cancels
     * @throws IOException HTTP、大小或文件写入失败时 / on HTTP, size, or file write failures
     */
    fun download(
        url: String,
        target: File,
        totalBytes: Long = -1L,
        shouldAbort: () -> Boolean = { false },
        onFallback: (IOException) -> Unit = {},
        onProgress: (Long, Long) -> Unit = { _, _ -> },
    ) {
        val workers = Executors.newFixedThreadPool(CONNECTIONS)
        val transfer = Transfer(workers, shouldAbort) { done -> onProgress(done, totalBytes) }
        try {
            transfer.checkAbort()
            onProgress(0, totalBytes)
            if (totalBytes >= MIN_PARALLEL_BYTES) {
                try {
                    val remote = transfer.runAll(listOf {
                        probe(url, target, totalBytes, transfer)
                    }).single()
                    if (remote != null) {
                        RandomAccessFile(target, "rw").use { it.setLength(totalBytes) }
                        val partSize = totalBytes / CONNECTIONS
                        transfer.runAll((0 until CONNECTIONS).map { index ->
                            {
                                val start = index * partSize
                                val end = if (index == CONNECTIONS - 1) {
                                    totalBytes - 1
                                } else {
                                    start + partSize - 1
                                }
                                downloadPart(remote, target, start, end, totalBytes, transfer)
                            }
                        })
                    }
                } catch (cancelled: DownloadCancelledException) {
                    throw cancelled
                } catch (failure: IOException) {
                    transfer.checkAbort()
                    onFallback(failure)
                    // 所有分段已退出，才能清空计数和覆盖文件，避免旧线程污染重试结果。
                    transfer.reset()
                    transfer.runAll(listOf { downloadSingle(url, target, totalBytes, transfer) })
                }
            } else {
                transfer.runAll(listOf { downloadSingle(url, target, totalBytes, transfer) })
            }
            transfer.checkAbort()
            onProgress(transfer.done.get(), totalBytes)
        } catch (failure: Throwable) {
            target.delete()
            throw failure
        } finally {
            workers.shutdown()
        }
    }

    private fun probe(url: String, target: File, size: Long, transfer: Transfer): Remote? =
        transfer.withResponse(request(url).header("Range", "bytes=0-0").build()) { response ->
            if (response.code == 200) {
                writeSingle(response, target, size, transfer)
                null
            } else {
                validateRange(response, 0, 0, size)
                response.body.byteStream().use { input ->
                    if (input.read() < 0 || input.read() != -1) throw IOException("下载分段长度不符")
                }
                val etag = response.header("ETag")?.takeUnless { it.startsWith("W/") }
                val validator = etag ?: response.header("Last-Modified")
                Remote(
                    response.request.url.toString(),
                    validator,
                    if (etag != null) "ETag" else "Last-Modified",
                )
            }
        }

    private fun downloadPart(
        remote: Remote,
        target: File,
        start: Long,
        end: Long,
        total: Long,
        transfer: Transfer,
    ) {
        val request = request(remote.url).header("Range", "bytes=$start-$end")
        remote.validator?.let { request.header("If-Range", it) }
        transfer.withResponse(request.build()) { response ->
            validateRange(response, start, end, total)
            if (remote.validator != null && response.header(remote.validatorHeader) != remote.validator) {
                throw IOException("下载文件在传输中发生变化")
            }
            response.body.byteStream().use { input ->
                RandomAccessFile(target, "rw").use { output ->
                    output.seek(start)
                    val buffer = ByteArray(BUFFER_SIZE)
                    var remaining = end - start + 1
                    while (remaining > 0) {
                        transfer.checkStopped()
                        val count = input.read(buffer, 0, minOf(buffer.size.toLong(), remaining).toInt())
                        if (count < 0) throw IOException("下载分段提前结束")
                        output.write(buffer, 0, count)
                        transfer.done.addAndGet(count.toLong())
                        remaining -= count
                    }
                    if (input.read() != -1) throw IOException("下载分段长度不符")
                }
            }
        }
    }

    private fun downloadSingle(url: String, target: File, size: Long, transfer: Transfer) {
        transfer.withResponse(request(url).build()) { response ->
            if (response.code != 200) throw IOException("下载失败（HTTP ${response.code}）")
            writeSingle(response, target, size, transfer)
        }
    }

    private fun writeSingle(response: Response, target: File, size: Long, transfer: Transfer) {
        validateEncoding(response)
        val length = response.body.contentLength()
        if (size > 0 && length >= 0 && length != size) throw IOException("下载文件大小不符")
        response.body.byteStream().use { input ->
            target.outputStream().buffered(BUFFER_SIZE).use { output ->
                val buffer = ByteArray(BUFFER_SIZE)
                while (true) {
                    transfer.checkStopped()
                    val count = input.read(buffer)
                    if (count < 0) break
                    if (size > 0 && transfer.done.get() + count > size) {
                        throw IOException("下载文件大小不符")
                    }
                    output.write(buffer, 0, count)
                    transfer.done.addAndGet(count.toLong())
                }
            }
        }
        if (size > 0 && transfer.done.get() != size) throw IOException("下载文件大小不符")
    }

    private fun request(url: String) = Request.Builder().url(url)
        // 分段偏移必须对应磁盘字节，禁止透明解压改变字节位置。
        .header("Accept-Encoding", "identity")

    private fun validateEncoding(response: Response) {
        val encoding = response.header("Content-Encoding")
        if (encoding != null && !encoding.equals("identity", ignoreCase = true)) {
            throw IOException("下载源返回了不支持的压缩响应")
        }
    }

    private fun validateRange(response: Response, start: Long, end: Long, total: Long) {
        validateEncoding(response)
        val range = CONTENT_RANGE.matchEntire(response.header("Content-Range")?.trim().orEmpty())
        val values = range?.groupValues?.drop(1)?.map { it.toLongOrNull() }
        val length = response.body.contentLength()
        if (response.code != 206 || values != listOf(start, end, total) ||
            (length >= 0 && length != end - start + 1)) {
            throw IOException("下载源不支持可靠的分段下载（HTTP ${response.code}）")
        }
    }

    private data class Remote(val url: String, val validator: String?, val validatorHeader: String)

    private inner class Transfer(
        private val workers: ExecutorService,
        private val shouldAbort: () -> Boolean,
        private val onProgress: (Long) -> Unit,
    ) {
        val done = AtomicLong()
        private val stopped = AtomicBoolean()
        private val calls = ConcurrentHashMap.newKeySet<Call>()

        fun checkAbort() {
            if (shouldAbort() || Thread.currentThread().isInterrupted) throw DownloadCancelledException()
        }

        fun checkStopped() {
            if (stopped.get()) throw DownloadCancelledException()
        }

        fun reset() {
            stopped.set(false)
            done.set(0)
            onProgress(0)
        }

        fun <T> withResponse(request: Request, action: (Response) -> T): T {
            val call = client.newCall(request)
            calls.add(call)
            try {
                // 先登记再查中止，保证取消与新增请求交错时也不会漏掉连接。
                checkStopped()
                return call.execute().use(action)
            } finally {
                calls.remove(call)
            }
        }

        fun <T> runAll(tasks: List<() -> T>): List<T> {
            val completion = ExecutorCompletionService<T>(workers)
            val futures = tasks.map { task ->
                completion.submit {
                    checkStopped()
                    task()
                }
            }
            val results = mutableListOf<T>()
            var lastProgress = System.nanoTime()
            try {
                while (results.size < tasks.size) {
                    checkAbort()
                    val finished = completion.poll(ABORT_INTERVAL_MS, TimeUnit.MILLISECONDS)
                    if (finished != null) results += finished.get()
                    val now = System.nanoTime()
                    if (now - lastProgress >= PROGRESS_INTERVAL_NS) {
                        onProgress(done.get())
                        lastProgress = now
                    }
                }
                return results
            } catch (failure: Throwable) {
                stopped.set(true)
                calls.forEach { it.cancel() }
                // Future.cancel 并不等待写线程退出；必须等实际完成后才允许回退或删除文件。
                var interrupted = failure is InterruptedException
                futures.forEach { future -> interrupted = awaitFinished(future) || interrupted }
                if (interrupted) Thread.currentThread().interrupt()
                checkAbort()
                throw if (failure is ExecutionException) failure.cause ?: failure else failure
            }
        }

        private fun awaitFinished(future: Future<*>): Boolean {
            var interrupted = false
            while (true) {
                try {
                    future.get()
                    return interrupted
                } catch (_: ExecutionException) {
                    return interrupted
                } catch (_: InterruptedException) {
                    interrupted = true
                }
            }
        }
    }

    private companion object {
        const val CONNECTIONS = 4
        // 小文件避免探测和多连接建立的额外延迟，大包保持每段至少约 2 MiB。
        const val MIN_PARALLEL_BYTES = 8L * 1024 * 1024
        const val BUFFER_SIZE = 256 * 1024
        const val ABORT_INTERVAL_MS = 100L
        const val PROGRESS_INTERVAL_NS = 200_000_000L
        val CONTENT_RANGE = Regex("bytes (\\d+)-(\\d+)/(\\d+)", RegexOption.IGNORE_CASE)
    }
}
