package com.azurpilot.ghio.gradle

import com.android.build.api.dsl.CommonExtension
import org.gradle.api.JavaVersion
import org.gradle.api.Project
import org.gradle.kotlin.dsl.configure
import org.jetbrains.kotlin.gradle.dsl.JvmTarget
import org.jetbrains.kotlin.gradle.dsl.KotlinAndroidProjectExtension

/**
 * 全仓统一的 SDK / JVM 基线：只改这里即可带动所有模块
 *
 * One SDK / JVM baseline for the whole repo: changing it here moves every module.
 */
internal const val COMPILE_SDK = 37
internal const val TARGET_SDK = 36
internal const val MIN_SDK = 28

/** 与上述 SDK 基线配套的 Java / JVM 目标 / Java and JVM target matching the SDK baselines above. */
internal val JAVA_VERSION = JavaVersion.VERSION_17
internal val JVM_TARGET = JvmTarget.JVM_17

/**
 * 为 Android 模块套用公共基线。AGP 9 的 CommonExtension 只暴露 getter、没有 lambda 形式的
 * DSL 方法，因此这里直接赋值属性；Kotlin 扩展来自 AGP 9 内置的 Kotlin 支持，但类型仍是
 * KGP 的。
 *
 * Applies the shared baselines to an Android module. AGP 9's CommonExtension only exposes
 * getters, no lambda-shaped DSL methods, so properties are assigned directly; the Kotlin
 * extension comes from AGP 9's built-in Kotlin support and is still KGP's type.
 */
internal fun Project.configureAndroidCommon(extension: CommonExtension) {
    extension.compileSdk = COMPILE_SDK
    extension.defaultConfig.minSdk = MIN_SDK
    extension.compileOptions.sourceCompatibility = JAVA_VERSION
    extension.compileOptions.targetCompatibility = JAVA_VERSION

    // 否则单元测试里任何 android.os.Trace 区段都会以 "not mocked" 崩掉。逐项 stub 平台
    // API 买不到任何东西：真正关心平台行为的用例本来就是 instrumented 测试
    extension.testOptions.unitTests.isReturnDefaultValues = true

    extensions.configure<KotlinAndroidProjectExtension> {
        compilerOptions {
            jvmTarget.set(JVM_TARGET)
        }
    }
}
