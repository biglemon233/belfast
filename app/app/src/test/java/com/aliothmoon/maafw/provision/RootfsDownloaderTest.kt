package com.aliothmoon.maafw.provision

import com.sun.net.httpserver.HttpServer
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.File
import java.io.IOException
import java.net.InetSocketAddress
import java.nio.file.Files

class RootfsDownloaderTest {
    private fun withServer(test: (HttpServer, String, File) -> Unit) {
        val dir = Files.createTempDirectory(File(requireNotNull(System.getProperty("java.io.tmpdir"))).toPath(), "rootfs-http-").toFile()
        val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.start()
        try {
            test(server, "http://127.0.0.1:${server.address.port}", dir)
        } finally {
            server.stop(0)
            dir.deleteRecursively()
        }
    }

    @Test
    fun `truncated response is rejected before publishing a file`() = withServer { server, base, dir ->
        server.createContext("/short") { exchange ->
            exchange.sendResponseHeaders(200, 100)
            exchange.responseBody.use { it.write(byteArrayOf(1, 2, 3)) }
        }
        assertThrows(IOException::class.java) {
            RootfsDownloader(dir).download("rootfs.tar.xz", listOf("$base/short")) { }
        }
        assertFalse(File(dir, "rootfs.tar.xz").exists())
    }

    @Test
    fun `failed source falls back and relative redirect preserves the full payload`() = withServer { server, base, dir ->
        val body = ByteArray(300_000) { (it % 251).toByte() }
        server.createContext("/missing") { exchange -> exchange.sendResponseHeaders(404, -1); exchange.close() }
        server.createContext("/redirect") { exchange ->
            exchange.responseHeaders.add("Location", "/payload")
            exchange.sendResponseHeaders(302, -1)
            exchange.close()
        }
        server.createContext("/payload") { exchange ->
            exchange.sendResponseHeaders(200, body.size.toLong())
            exchange.responseBody.use { it.write(body) }
        }
        val result = RootfsDownloader(dir).download("rootfs.tar.xz", listOf("$base/missing", "$base/redirect")) { }
        assertArrayEquals(body, result.readBytes())
        assertFalse(File(dir, "rootfs.tar.xz.part").exists())
    }
}
