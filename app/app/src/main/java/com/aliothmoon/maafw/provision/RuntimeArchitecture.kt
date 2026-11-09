package com.aliothmoon.maafw.provision

import java.io.File

/** ELF is authoritative: emulators can expose ARM translation and spoof uname. */
object RuntimeArchitecture {
    fun deviceAbi(systemExecutable: File, reportedAbis: List<String>): String =
        when (elfArchitecture(systemExecutable)) {
            "amd64" -> "x86_64"
            "arm64" -> "arm64-v8a"
            else -> reportedAbis.firstOrNull().orEmpty()
        }

    fun runtimeAbi(packageAbi: String, deviceAbi: String, selectedAbi: String = ""): String =
        if (packageAbi != "universal") packageAbi else selectedAbi.ifBlank { deviceAbi }

    fun prootFileName(abi: String, universal: Boolean, loader: Boolean = false): String {
        if (!universal) return if (loader) "libproot-loader.so" else "libproot.so"
        val suffix = when (abi) {
            "arm64-v8a" -> "arm64"
            "x86_64" -> "x86_64"
            else -> error("Unsupported runtime ABI: $abi")
        }
        return if (loader) "libproot_loader_$suffix.so" else "libproot_$suffix.so"
    }

    fun supportsPackage(packageAbi: String, deviceAbi: String): Boolean =
        deviceAbi in listOf("arm64-v8a", "x86_64") && (packageAbi == "universal" || packageAbi == deviceAbi)

    fun linuxArchitecture(abi: String): String = when (abi) {
        "arm64-v8a" -> "arm64"
        "x86_64" -> "amd64"
        else -> error("Unsupported runtime ABI: $abi")
    }

    fun elfArchitecture(file: File): String? = runCatching {
        file.inputStream().use { input ->
            val header = ByteArray(20)
            if (input.read(header) != header.size ||
                !header.copyOfRange(0, 4).contentEquals(byteArrayOf(127, 69, 76, 70)) ||
                header[4].toInt() != 2 || header[5].toInt() != 1) return@use null
            when ((header[18].toInt() and 255) or ((header[19].toInt() and 255) shl 8)) {
                183 -> "arm64"
                62 -> "amd64"
                else -> null
            }
        }
    }.getOrNull()

    fun requireCompatible(abi: String, supportedAbis: List<String>, manifest: String?, python: File? = null) {
        val expected = linuxArchitecture(abi)
        require(supportedAbis.firstOrNull() == abi) {
            "运行包架构 $abi 与设备 ${supportedAbis.firstOrNull() ?: "未知"} 不匹配，请安装对应架构的 AOS"
        }
        val declared = manifest?.let { Regex(""""architecture"\s*:\s*"([^"]+)"""").find(it)?.groupValues?.get(1) }
        // Legacy manifests without architecture belong to the existing ARM64 releases.
        require((declared ?: "arm64") == expected) {
            "运行环境架构 ${declared ?: "arm64"} 与安装包 $expected 不匹配，请选择对应架构的环境包"
        }
        if (python != null) require(elfArchitecture(python) == expected) {
            "Python ELF 架构与安装包 $expected 不匹配，请重新部署对应运行环境"
        }
    }
}
