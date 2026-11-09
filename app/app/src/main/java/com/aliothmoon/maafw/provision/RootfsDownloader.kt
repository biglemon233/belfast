package com.aliothmoon.maafw.provision

import timber.log.Timber
import java.io.File
import java.io.IOException
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL

/**
 * rootfs.tar.xz / BUILD_MANIFEST 的直链下载器（lite 构建首启用）
 *
 * - 候选 URL 依次尝试：镜像在前、GitHub 直链兜底，前一个失败自动换下一个；
 * - 手动跟 3xx 重定向（跨协议跳转 HttpURLConnection 不自动跟，镜像服务常见）；
 * - 落盘 `<name>.part` 再原子换名，半途失败不留半拉子正式文件；
 * - 每 256KB 推一次进度（与解压侧的 CountingInputStream 同一节奏）
 */
class RootfsDownloader(private val workDir: File) {

    data class Progress(val doneBytes: Long, val totalBytes: Long, val bytesPerSecond: Long)

    /** 依次尝试 [candidates]；全部失败抛最后一个异常。成功返回落盘文件 */
    @Throws(IOException::class)
    fun download(fileName: String, candidates: List<String>, onProgress: (Progress) -> Unit): File {
        require(candidates.isNotEmpty()) { "no download candidate" }
        workDir.mkdirs()
        var lastError: IOException? = null
        for ((index, url) in candidates.withIndex()) {
            try {
                Timber.i("rootfs download try %d/%d: %s", index + 1, candidates.size, url)
                return downloadOne(url, workDir, fileName, onProgress)
            } catch (e: IOException) {
                Timber.w(e, "rootfs download failed from %s", url)
                lastError = e
            }
        }
        throw lastError ?: IOException("all download candidates failed")
    }

    /** 清空下载目录（重下/换源时调用） */
    fun clear() {
        workDir.listFiles()?.forEach { it.deleteRecursively() }
    }

    private fun downloadOne(
        rawUrl: String,
        dir: File,
        fileName: String,
        onProgress: (Progress) -> Unit,
    ): File {
        val partFile = File(dir, "$fileName.part")
        partFile.delete()
        val startedAt = System.nanoTime()

        // HttpURLConnection 本身不是 Closeable，流用完关，连接显式断开
        val connection = openFollowing(rawUrl)
        try {
            val contentLength = connection.contentLengthLong
            connection.inputStream.use { input ->
                partFile.outputStream().buffered(BUFFER_SIZE).use { output ->
                    copyCounting(input, output) { read ->
                        val seconds = ((System.nanoTime() - startedAt) / 1_000_000_000.0).coerceAtLeast(0.001)
                        onProgress(Progress(read, contentLength, (read / seconds).toLong()))
                    }
                }
            }
            if (contentLength >= 0 && partFile.length() != contentLength) {
                throw IOException("incomplete download: ${partFile.length()} of $contentLength bytes")
            }
        } finally {
            connection.disconnect()
        }
        if (partFile.length() <= 0L) {
            throw IOException("empty body from $rawUrl")
        }
        val finalFile = File(dir, fileName)
        finalFile.delete()
        if (!partFile.renameTo(finalFile)) throw IOException("rename ${partFile.name} -> $fileName failed")
        Timber.i("rootfs download done: %s (%d bytes)", fileName, finalFile.length())
        return finalFile
    }

    /** 手动跟重定向（上限 5 跳），返回最终可读的连接；调用方负责断开 */
    private fun openFollowing(rawUrl: String): HttpURLConnection {
        var url = rawUrl
        repeat(MAX_REDIRECTS) {
            val connection = URL(url).openConnection() as HttpURLConnection
            connection.connectTimeout = CONNECT_TIMEOUT_MS
            connection.readTimeout = READ_TIMEOUT_MS
            connection.instanceFollowRedirects = false
            connection.setRequestProperty("User-Agent", USER_AGENT)
            val code = connection.responseCode
            if (code in setOf(
                    HttpURLConnection.HTTP_MOVED_PERM,
                    HttpURLConnection.HTTP_MOVED_TEMP,
                    HttpURLConnection.HTTP_SEE_OTHER,
                    307, 308,
                )
            ) {
                val location = connection.getHeaderField("Location")
                connection.disconnect()
                if (location == null) throw IOException("redirect without Location: $url")
                url = URL(URL(url), location.replace(" ", "%20")).toString()
                Timber.d("rootfs download redirect -> %s", url)
                return@repeat
            }
            if (code == HttpURLConnection.HTTP_OK) return connection
            connection.disconnect()
            throw IOException("HTTP $code from $url")
        }
        throw IOException("too many redirects: $rawUrl")
    }

    private fun copyCounting(input: InputStream, output: java.io.OutputStream, onProgress: (Long) -> Unit) {
        val buffer = ByteArray(BUFFER_SIZE)
        var read = 0L
        var sinceEmit = 0L
        while (true) {
            val n = input.read(buffer)
            if (n < 0) break
            output.write(buffer, 0, n)
            read += n
            sinceEmit += n
            if (sinceEmit >= 256 * 1024) {
                sinceEmit = 0
                onProgress(read)
            }
        }
        output.flush()
        onProgress(read)
    }

    private companion object {
        const val BUFFER_SIZE = 256 * 1024
        const val MAX_REDIRECTS = 5
        const val CONNECT_TIMEOUT_MS = 15_000
        const val READ_TIMEOUT_MS = 30_000
        const val USER_AGENT = "ALAS-AOS/rootfs-downloader"
    }
}
