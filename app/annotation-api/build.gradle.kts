// 纯 JVM 模块：仅承载 @PrefSchema / @PrefKey 注解且零依赖，
// ksp-processor 与 Android 侧（:app）引用的是同一份注解类型
plugins {
    id("azurpilot.kotlin.jvm")
}
