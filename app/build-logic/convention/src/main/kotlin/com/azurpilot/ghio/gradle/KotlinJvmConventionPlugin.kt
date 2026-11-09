package com.azurpilot.ghio.gradle

import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.api.plugins.JavaPluginExtension
import org.gradle.kotlin.dsl.configure
import org.jetbrains.kotlin.gradle.dsl.KotlinJvmProjectExtension

/**
 * 纯 JVM 模块（annotation-api / ksp-processor）的公共基线：Java 与 jvmTarget 与 Android 侧同级
 *
 * Plain JVM modules (annotation-api / ksp-processor): same Java and jvmTarget level as the
 * Android side.
 */
class KotlinJvmConventionPlugin : Plugin<Project> {
    override fun apply(target: Project) {
        with(target) {
            pluginManager.apply("org.jetbrains.kotlin.jvm")

            extensions.configure<JavaPluginExtension> {
                sourceCompatibility = JAVA_VERSION
                targetCompatibility = JAVA_VERSION
            }
            extensions.configure<KotlinJvmProjectExtension> {
                compilerOptions {
                    jvmTarget.set(JVM_TARGET)
                }
            }
        }
    }
}
