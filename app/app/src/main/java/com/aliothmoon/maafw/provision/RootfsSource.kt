package com.aliothmoon.maafw.provision

import java.net.URI

/**
 * lite 构建的 rootfs 下载源模型：源类型 + 自定义直链 + 镜像前缀，纯 JVM 无 Android 依赖
 *
 * - GitHub 源 = 本仓最新正式版本 Release 的环境包；不依赖额外的预发布；
 * - 自定义直链 = 用户自己给的 http(s) 文件地址；
 * - 镜像只对 GitHub 源生效（Moontier 同款前缀拼接：最终 URL = 镜像前缀 + 原始 URL），
 *   下载时按序尝试，失败自动换下一个，全部失败回落 GitHub 直链
 */
enum class RootfsSourceKind {
    GITHUB,
    CUSTOM,
}

object RootfsSource {

    /** GitHub redirects to the latest published, non-prerelease version. */
    const val GITHUB_DOWNLOAD_BASE = "https://github.com/Dreamry2C/ALAS-AOS/releases/latest/download"

    /** GitHub 源的 rootfs 包地址 */
    const val GITHUB_ARCHIVE_URL =
        "$GITHUB_DOWNLOAD_BASE/rootfs.tar.xz"

    /** GitHub 源的构建清单地址（解压版本闸门的 marker 版本来源） */
    const val GITHUB_MANIFEST_URL =
        "$GITHUB_DOWNLOAD_BASE/BUILD_MANIFEST"

    /** 镜像前缀默认值，与 Moontier 的 DEFAULT_CORE_DOWNLOAD_PROXIES 同款 */
    const val DEFAULT_MIRRORS_JOINED = "https://ghfast.top/,https://gh-proxy.com/,https://mirror.ghproxy.com/"

    const val MIRROR_SEPARATOR = ","

    fun splitMirrors(joined: String): List<String> =
        joined.split(MIRROR_SEPARATOR)
            .map { it.trim() }
            .filter { it.isNotEmpty() }

    /** 镜像前缀拼接：补齐缺失的结尾斜杠（ghfast.top 与 ghfast.top/ 等价） */
    fun composeMirrorUrl(prefix: String, url: String): String {
        val trimmed = prefix.trim()
        return if (trimmed.endsWith("/")) trimmed + url else "$trimmed/$url"
    }

    /** 清单与包同目录：去掉地址最后一段路径接 BUILD_MANIFEST，兼容自定义直链的任意文件名 */
    fun manifestUrl(archiveUrl: String): String {
        val name = if (archiveUrl.substringAfterLast('/').substringBefore('?') == "rootfs-amd64.tar.xz") {
            "BUILD_MANIFEST-amd64"
        } else "BUILD_MANIFEST"
        return archiveUrl.substringBeforeLast('/') + "/$name"
    }
}

/** Validate HTTP(S) file URLs; archive contents are checked after download. */
object RootfsSourceValidator {

    sealed interface Result {
        /** 校验通过；url 为 trim 后的原始直链（未拼镜像） */
        data class Valid(val url: String) : Result

        data class Rejected(val reason: Reason) : Result
    }

    enum class Reason {
        /** 空串或不是合法 URL */
        INVALID_URL,

        /** 协议不是 http/https */
        UNSUPPORTED_SCHEME,

    }

    fun validate(rawUrl: String): Result {
        val trimmed = rawUrl.trim()
        if (trimmed.isEmpty()) return Result.Rejected(Reason.INVALID_URL)
        val uri = runCatching { URI(trimmed) }.getOrElse { return Result.Rejected(Reason.INVALID_URL) }
        // 无 scheme（"github.com/x" 这类）按无效直链处理；有 scheme 但不是 http(s) 单独提示
        val scheme = uri.scheme?.lowercase() ?: return Result.Rejected(Reason.INVALID_URL)
        if (scheme != "http" && scheme != "https") {
            return Result.Rejected(Reason.UNSUPPORTED_SCHEME)
        }
        val host = uri.host?.lowercase()
        if (host.isNullOrEmpty()) return Result.Rejected(Reason.INVALID_URL)

        return Result.Valid(trimmed)
    }

}

/** 用户在首启页配置好的下载源；[candidateUrls] 给出按序尝试的完整候选 */
data class RootfsDownloadSource(
    val kind: RootfsSourceKind,
    val customUrl: String = "",
    val useMirror: Boolean = false,
    val mirrors: List<String> = emptyList(),
    val abi: String? = null,
) {
    /**
     * 依次尝试的下载地址：
     * - GitHub 源勾了镜像 → 镜像候选在前、GitHub 直链兜底；
     * - 自定义直链 → 只有这一个候选（镜像不生效，直链用户自管加速）
     */
    fun candidateUrls(abi: String = "arm64-v8a"): List<String> = when (kind) {
        RootfsSourceKind.GITHUB -> {
            val archiveUrl = if (abi == "x86_64") "${RootfsSource.GITHUB_DOWNLOAD_BASE}/rootfs-amd64.tar.xz" else RootfsSource.GITHUB_ARCHIVE_URL
            val direct = listOf(archiveUrl)
            if (useMirror) {
                mirrors.filter { it.isNotBlank() }.map { RootfsSource.composeMirrorUrl(it, archiveUrl) } + direct
            } else {
                direct
            }
        }

        RootfsSourceKind.CUSTOM -> listOf(customUrl.trim())
    }
}
