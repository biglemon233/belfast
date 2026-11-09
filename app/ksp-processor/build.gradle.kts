plugins {
    id("azurpilot.kotlin.jvm")
}

dependencies {
    // 注解类型定义在独立模块，processor 与使用方共享同一份
    implementation(project(":annotation-api"))
    implementation(libs.symbol.processing.api)
    implementation(libs.kotlinpoet)
    // kotlinpoet-ksp 提供 writeTo(codeGenerator, Dependencies) 这类 KSP 桥接 API
    implementation(libs.kotlinpoet.ksp)
}
