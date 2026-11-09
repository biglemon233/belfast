plugins {
    id("azurpilot.android.library")
    id("azurpilot.android.compose")
}
android { namespace = "com.azurpilot.miuix.runtime" }
dependencies {
    testImplementation(kotlin("test-junit"))
    api(project(":navigation-runtime"))
    api(libs.androidx.foundation)
    api(libs.miuix.squircle)
    implementation(libs.androidx.material3.window.size)
    implementation(libs.material.color.utilities)
}

kotlin { compilerOptions { freeCompilerArgs.add("-Xcontext-parameters") } }
