package com.aliothmoon.maafw.ui.alas

import java.net.URI
import java.util.Base64

internal data class AlasConfigDownload(val name: String, val bytes: ByteArray) {
    companion object {
        const val PROMPT_PREFIX = "alasaos-export:"
        private const val MAX_BYTES = 8 * 1024 * 1024

        fun isAlasOrigin(url: String?): Boolean = runCatching {
            val uri = URI(url ?: "")
            uri.scheme == "http" && uri.host == "127.0.0.1" && uri.port == 22267 && uri.userInfo == null
        }.getOrDefault(false)

        fun parse(name: String, dataUrl: String): AlasConfigDownload {
            val safeName = name.substringAfterLast('/').substringAfterLast('\\').trim()
            require(safeName.length in 6..128 && safeName.endsWith(".json", ignoreCase = true))
            require(safeName.none { it.isISOControl() })
            require(dataUrl.length <= MAX_BYTES * 4 / 3 + 128)
            val split = dataUrl.indexOf(',')
            require(split in 5..100 && dataUrl.startsWith("data:") && dataUrl.substring(0, split).endsWith(";base64"))
            val bytes = Base64.getDecoder().decode(dataUrl.substring(split + 1))
            require(bytes.isNotEmpty() && bytes.size <= MAX_BYTES)
            return AlasConfigDownload(safeName, bytes)
        }
    }
}
