package com.aliothmoon.maafw.ui.alas

import org.junit.Assert.*
import org.junit.Test
import java.util.Base64

class AlasConfigDownloadTest {
    private fun data(text: String) = "data:application/octet-stream;base64," + Base64.getEncoder().encodeToString(text.toByteArray())

    @Test fun `export preserves UTF8 config bytes and only uses a filename`() {
        val json = "{\"name\":\"配置\"}"
        val export = AlasConfigDownload.parse("../folder/测试.json", data(json))
        assertEquals("测试.json", export.name)
        assertArrayEquals(json.toByteArray(), export.bytes)
    }

    @Test fun `bridge only accepts the exact local ALAS origin`() {
        assertTrue(AlasConfigDownload.isAlasOrigin("http://127.0.0.1:22267/?app=manage"))
        for (url in listOf(null, "https://127.0.0.1:22267/", "http://127.0.0.1:22400/", "http://127.0.0.1:22267.evil/", "http://user@127.0.0.1:22267/", "file:///tmp/alas")) {
            assertFalse(url, AlasConfigDownload.isAlasOrigin(url))
        }
    }

    @Test fun `rejects malformed and oversized downloads`() {
        for ((name, value) in listOf(
            "alas.json" to "blob:http://127.0.0.1/file",
            "alas.json" to "data:application/json;base64,%%%",
            "alas.json" to "data:application/json;base64,",
            "alas.json" to "x".repeat(12 * 1024 * 1024),
            "bad\n.json" to data("{}"),
            "alas.html" to data("{}"),
        )) {
            assertTrue(runCatching { AlasConfigDownload.parse(name, value) }.isFailure)
        }
    }
}
