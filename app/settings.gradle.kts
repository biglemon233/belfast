pluginManagement {
    // 构建约定插件（azurpilot.*）在这个独立构建里，模块脚本只按 id 应用
    includeBuild("build-logic")
    repositories {
        // 排在最前：本地联调时允许以 mavenLocal 里已发布的构件覆盖远端
        mavenLocal()
        // CI 优先官方插件仓库；大陆网络仍可回退 Aliyun 镜像。
        gradlePluginPortal()
        google {
            content {
                includeGroupByRegex("com\\.android.*")
                includeGroupByRegex("com\\.google.*")
                includeGroupByRegex("androidx.*")
            }
        }
        mavenCentral()
        maven {
            name = "AliyunGoogle"
            url = uri("https://maven.aliyun.com/repository/google")
            content {
                includeGroupByRegex("com\\.android.*")
                includeGroupByRegex("com\\.google.*")
                includeGroupByRegex("androidx.*")
            }
        }
        maven {
            name = "AliyunCentral"
            url = uri("https://maven.aliyun.com/repository/central")
        }
    }
}
plugins {
    // 按需自动下载匹配的 JDK 工具链，CI 与新机器无需预装指定版本
    id("org.gradle.toolchains.foojay-resolver-convention") version "1.0.0"
}
dependencyResolutionManagement {
    // 仓库统一在此声明：任何模块私加 repositories 直接构建失败，镜像策略不被绕过
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
        maven {
            name = "AliyunGoogle"
            url = uri("https://maven.aliyun.com/repository/google")
        }
        maven {
            name = "AliyunCentral"
            url = uri("https://maven.aliyun.com/repository/central")
        }
        maven { url = uri("https://jitpack.io") }
    }
}

rootProject.name = "AzurPilotApp"
include(":app")
include(":download-core")
include(":adb-runtime")
include(":hidden-api")
include(":navigation-runtime")
// Preferences DataStore 的 schema 代码生成（@PrefSchema / @PrefKey）
include(":annotation-api")
include(":ksp-processor")

include(":miuix-runtime")
