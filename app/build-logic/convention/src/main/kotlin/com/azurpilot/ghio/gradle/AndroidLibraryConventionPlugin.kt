package com.azurpilot.ghio.gradle

import com.android.build.api.dsl.LibraryExtension
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.kotlin.dsl.getByType

/**
 * 给库模块套用与 :app 相同的 Android 公共基线（SDK / JVM、单元测试行为）
 *
 * Applies the same shared Android baselines (SDK / JVM, unit-test behavior) to library modules.
 */
class AndroidLibraryConventionPlugin : Plugin<Project> {
    override fun apply(target: Project) {
        with(target) {
            // AGP 9 已把 library 插件放上构建类路径，这里无需再声明版本
            pluginManager.apply("com.android.library")
            configureAndroidCommon(extensions.getByType<LibraryExtension>())
        }
    }
}
