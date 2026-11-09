package com.azurpilot.ghio.update

import android.os.Bundle
import android.os.SystemClock
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.azurpilot.download.ParallelDownloader
import okhttp3.Call
import okhttp3.EventListener
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.TimeUnit

/**
 * 在设备上对比同一 Release 资源的单连接与分段下载，并校验文件及实际 Range 响应。
 *
 * 通过 azurpilot.downloadBenchmark 构建，且仅显式传入 Release 资源参数时下载，避免消耗流量。
 * 结果保存到应用 externalFiles/debug/download-benchmark.json；临时资源完成后删除。
 *
 * Compares single and segmented downloads of one release asset on-device, verifying file integrity
 * and actual Range responses. Built with azurpilot.downloadBenchmark; downloads only with explicit
 * releaseUrl, releaseSize, and releaseSha256 arguments to avoid network usage. Results are saved to
 * externalFiles/debug/download-benchmark.json and temporary assets are deleted after completion.
 */
@RunWith(AndroidJUnit4::class)
class ReleaseDownloadInstrumentedTest {
    @Test
    fun compareSingleAndParallelDownloads() {
        val arguments = InstrumentationRegistry.getArguments()
        val url = arguments.getString("releaseUrl").orEmpty()
        assumeTrue("Pass explicit release asset arguments to run the benchmark", url.isNotEmpty())
        require(ReleaseUrls.isReleaseUrl(url))
        val size = requireNotNull(arguments.getString("releaseSize")).toLong()
        val sha256 = requireNotNull(arguments.getString("releaseSha256"))
        require(size >= 8L * 1024 * 1024 && sha256.matches(Regex("[0-9a-f]{64}")))
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val context = instrumentation.targetContext
        val results = JSONArray()
        for (parallel in listOf(false, true)) {
            val requests = ConcurrentLinkedQueue<JSONObject>()
            val listener = object : EventListener() {
                override fun responseHeadersEnd(call: Call, response: Response) {
                    requests.add(JSONObject().apply {
                        put("range", response.request.header("Range") ?: "")
                        put("status", response.code)
                        put("content_range", response.header("Content-Range") ?: "")
                        put("protocol", response.protocol.toString())
                    })
                }
            }
            val client = OkHttpClient.Builder()
                .protocols(listOf(Protocol.HTTP_1_1))
                .connectTimeout(20, TimeUnit.SECONDS)
                .readTimeout(120, TimeUnit.SECONDS)
                .eventListener(listener)
                .build()
            val file = File(context.cacheDir, "release-download-benchmark.part")
            var fallbacks = 0
            try {
                val start = SystemClock.elapsedRealtimeNanos()
                ParallelDownloader(client).download(url, file,
                    totalBytes = if (parallel) size else -1L,
                    onFallback = { fallbacks++ })
                val elapsed = (SystemClock.elapsedRealtimeNanos() - start) / 1_000_000_000.0
                assertEquals(size, file.length())
                assertEquals(sha256, sha256Hex(file))
                val parts = requests.filter {
                    it.getString("range").isNotEmpty() && it.getString("range") != "bytes=0-0"
                }
                if (parallel && fallbacks == 0 && parts.isNotEmpty()) {
                    assertEquals(4, parts.size)
                    assertTrue(parts.all { it.getInt("status") == 206 })
                }
                val result = JSONObject().apply {
                    put("mode", if (parallel) "parallel" else "single")
                    put("bytes", size)
                    put("seconds", elapsed)
                    put("mib_per_second", size / 1_048_576.0 / elapsed)
                    put("sha256_verified", true)
                    put("fallbacks", fallbacks)
                    put("part_responses", parts.size)
                    put("requests", JSONArray(requests.toList()))
                }
                results.put(result)
                instrumentation.sendStatus(0, Bundle().apply {
                    putString("stream", "Download benchmark: $result\n")
                })
                File(context.getExternalFilesDir("debug"), "download-benchmark.json").apply {
                    parentFile?.mkdirs()
                    writeText(results.toString(2))
                }
            } finally {
                file.delete()
                client.connectionPool.evictAll()
                client.dispatcher.executorService.shutdown()
            }
        }
    }
}
