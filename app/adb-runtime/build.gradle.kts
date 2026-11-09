plugins {
    id("azurpilot.android.library")
}

android {
    namespace = "com.azurpilot.adb"
    buildFeatures.prefab = true
    packaging.resources.merges += listOf("META-INF/LICENSE.md", "META-INF/LICENSE.txt", "META-INF/NOTICE.md")
    externalNativeBuild.cmake {
        path = file("src/main/cpp/CMakeLists.txt")
        version = "3.22.1"
    }
    defaultConfig {
        ndk.abiFilters += listOf("arm64-v8a", "x86_64")
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        externalNativeBuild.cmake.arguments += "-DANDROID_STL=c++_shared"
        consumerProguardFiles("consumer-rules.pro")
    }
}

dependencies {
    implementation(libs.androidx.core.ktx)
    implementation(libs.adb.bcpkix)
    implementation(libs.adb.boringssl)
    implementation(libs.hidden.api.bypass)
    implementation(libs.timber)
    testImplementation(libs.junit)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.junit)
}
