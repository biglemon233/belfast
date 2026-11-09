package com.azurpilot.ghio.gradle

import com.android.build.api.dsl.CommonExtension
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.kotlin.dsl.dependencies
import org.gradle.kotlin.dsl.getByType

/**
 * 为模块铺 Compose 公共底座；必须在 azurpilot.android.application 或 azurpilot.android.library
 * 之后应用，因为它读取二者注册的 android 扩展。
 * 这里只放共享的下限（BOM、ui、ui-graphics），material3 / icons / tooling 留给各模块自行决定。
 *
 * Applies after azurpilot.android.application or azurpilot.android.library: this reads the
 * android extension they register. Only the shared floor lands here (BOM, ui, ui-graphics);
 * material3 / icons / tooling stay per module.
 */
class AndroidComposeConventionPlugin : Plugin<Project> {
    override fun apply(target: Project) {
        with(target) {
            pluginManager.apply("org.jetbrains.kotlin.plugin.compose")

            extensions.getByType<CommonExtension>().buildFeatures.compose = true

            val bom = libs.library("androidx-compose-bom")
            dependencies {
                add("implementation", platform(bom))
                add("androidTestImplementation", platform(bom))
                add("implementation", libs.library("androidx-ui"))
                add("implementation", libs.library("androidx-ui-graphics"))
            }
        }
    }
}
