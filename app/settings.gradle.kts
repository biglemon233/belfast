pluginManagement {
    // 构建约定插件（maafw.*）在这个独立构建里，模块脚本只按 id 应用
    includeBuild("build-logic")
    resolutionStrategy {
        eachPlugin {
            if (requested.id.id == "com.google.devtools.ksp") {
                // KSP 2.x is published to Central; avoid stale/missing Google plugin markers.
                useModule("com.google.devtools.ksp:symbol-processing-gradle-plugin:${requested.version}")
            }
        }
    }
    repositories {
        mavenLocal()
        // 大陆网络环境 dl.google.com 偶发握手中断，Aliyun 镜像优先、官方源兜底。
        // CI（GitHub Actions 海外 runner）只用官方源：Aliyun 偶发 502 会把整个解析拖死
        if (System.getenv("CI") != "true") {
            maven {
                name = "AliyunGoogle"
                url = uri("https://maven.aliyun.com/repository/google")
                content {
                    includeGroupByRegex("com\\.android.*")
                    includeGroupByRegex("com\\.google.*")
                    includeGroupByRegex("androidx.*")
                    excludeGroupByRegex("com\\.google\\.devtools.*")
                }
            }
        }
        google {
            content {
                includeGroupByRegex("com\\.android.*")
                includeGroupByRegex("com\\.google.*")
                includeGroupByRegex("androidx.*")
                excludeGroupByRegex("com\\.google\\.devtools.*")
            }
        }
        if (System.getenv("CI") != "true") {
            maven {
                name = "AliyunCentral"
                url = uri("https://maven.aliyun.com/repository/central")
            }
        }
        mavenCentral()
        gradlePluginPortal()
    }
}
plugins {
    id("org.gradle.toolchains.foojay-resolver-convention") version "1.0.0"
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        // CI 只用官方源，理由见 pluginManagement 注释
        if (System.getenv("CI") != "true") {
            maven {
                name = "AliyunGoogle"
                url = uri("https://maven.aliyun.com/repository/google")
                content { excludeGroupByRegex("com\\.google\\.devtools.*") }
            }
        }
        google {
            content { excludeGroupByRegex("com\\.google\\.devtools.*") }
        }
        if (System.getenv("CI") != "true") {
            maven {
                name = "AliyunCentral"
                url = uri("https://maven.aliyun.com/repository/central")
            }
        }
        mavenCentral()
        maven { url = uri("https://jitpack.io") }
    }
}

rootProject.name = "MaaFwApp"
include(":app")
include(":hidden-api")
// Preferences DataStore 的 schema 代码生成（@PrefSchema / @PrefKey）
include(":annotation-api")
include(":ksp-processor")
