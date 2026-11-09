plugins {
    id("azurpilot.android.library")
    id("azurpilot.android.compose")
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.azurpilot.navigation.runtime"
}

dependencies {
    // 上游发布包使用 JVM 21；源码构建复用本仓库 JVM 17 基线，动画实现保持上游版本。
    api(libs.androidx.navigationevent.compose)
    api(libs.androidx.lifecycle.runtime.compose)
    api(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.foundation)
    implementation(libs.miuix.squircle)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.collections.immutable)
    testImplementation(kotlin("test-junit"))
}
