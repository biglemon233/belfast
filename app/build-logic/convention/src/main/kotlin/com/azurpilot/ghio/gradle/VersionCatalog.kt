package com.azurpilot.ghio.gradle

import org.gradle.api.Project
import org.gradle.api.artifacts.VersionCatalog
import org.gradle.api.artifacts.VersionCatalogsExtension
import org.gradle.kotlin.dsl.getByType

/**
 * 插件代码没有构建脚本那样的 libs 访问器，版本目录只能从扩展里取
 *
 * Plugin code has no libs accessor like build scripts do; the catalog comes from the extension.
 */
internal val Project.libs: VersionCatalog
    get() = extensions.getByType<VersionCatalogsExtension>().named("libs")

/**
 * 按别名取库，缺失立即失败：插件配置期的缺别名应当场暴露而不是静默返回 null
 *
 * Fetches a library by alias, failing immediately when it is missing — a missing alias in
 * plugin code should surface at configure time, not come back as a silent null.
 */
internal fun VersionCatalog.library(alias: String) = findLibrary(alias).orElseThrow {
    IllegalStateException("no $alias in the version catalog")
}
