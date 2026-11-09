package com.aliothmoon.maafw.proot

import android.content.Context
import android.net.ConnectivityManager
import android.net.Uri
import java.net.URI

/** Process-local overrides only. Never log this map or put it in ProotHost.baseEnv(). */
object AlasUpdateProxy {
    fun environment(context: Context, inherited: Map<String, String> = System.getenv()): Map<String, String> {
        val info = runCatching { context.getSystemService(ConnectivityManager::class.java)?.defaultProxy }
            .getOrNull() ?: return emptyMap()
        return staticEnvironment(info.host, info.port, info.pacFileUrl != Uri.EMPTY &&
            !info.pacFileUrl?.toString().isNullOrEmpty(), info.exclusionList?.toList().orEmpty(), inherited)
    }

    internal fun staticEnvironment(
        host: String?, port: Int, hasPac: Boolean,
        exclusions: List<String>, inherited: Map<String, String>,
    ): Map<String, String> {
        // A PAC URL (even with a local resolver host/port) is NOT a static proxy.
        if (hasPac || host.isNullOrBlank() || port !in 1..65535) return emptyMap()
        val plainHost = host.removePrefix("[").removeSuffix("]")
        if (!Regex("[A-Za-z0-9.:-]+").matches(plainHost)) return emptyMap()
        val proxy = runCatching { URI("http", null, plainHost, port, null, null, null).toASCIIString() }
            .getOrNull() ?: return emptyMap()
        // Android wildcard/PAC matching is not equivalent to curl/Python NO_PROXY matching.
        // Refuse injection rather than silently dropping an exclusion or interpreting <local>.
        val bypass = exclusions.map { it.trim() }.filter { it.isNotEmpty() }
        if (bypass.any { !Regex("[A-Za-z0-9.:-]+").matches(it) }) return emptyMap()
        val noProxy = (listOfNotNull(inherited["NO_PROXY"], inherited["no_proxy"]) +
            bypass + listOf("localhost", "127.0.0.1", "::1"))
            .filter { it.isNotEmpty() }.joinToString(",")
        return mapOf("http_proxy" to proxy, "https_proxy" to proxy, "HTTP_PROXY" to proxy,
            "HTTPS_PROXY" to proxy, "NO_PROXY" to noProxy, "no_proxy" to noProxy)
    }
}
