package com.azurpilot.ghio.gradle

import org.gradle.api.Project
import java.util.Properties

private fun Project.loadLocalProperties(): Properties = Properties().apply {
    val file = rootProject.file("local.properties")
    if (file.exists()) file.inputStream().use { load(it) }
}

/**
 * 路径类开关：local.properties 是开发者本机配置，环境变量作为回退
 *
 * 空白视为未设置，调用方据此走各自的软失败路径。
 *
 * Path switches: local.properties is the developer machine config, the env var is the fallback.
 *
 * Blank counts as unset; callers treat that as a soft failure.
 */
internal fun Project.pathSetting(key: String, envName: String): String? =
    (loadLocalProperties().getProperty(key) ?: System.getenv(envName))?.takeIf { it.isNotBlank() }

/**
 * 签名材料的优先级相反：release 构建会注入环境变量，local.properties 里的陈旧条目
 * 不得覆盖它们
 *
 * Signing material flips the precedence: a release build injects env vars and a stale
 * local.properties entry must not override them.
 */
internal fun Project.signingSetting(envName: String, key: String): String =
    System.getenv(envName) ?: loadLocalProperties().getProperty(key, "")

/**
 * 逗号分隔的列表开关，仅从 local.properties 读取
 *
 * Comma separated list switch, read from local.properties only.
 */
internal fun Project.listSetting(key: String): List<String> =
    (loadLocalProperties().getProperty(key) ?: "").split(',')
        .map(String::trim)
        .filter(String::isNotEmpty)
