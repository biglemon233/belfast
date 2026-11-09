// build-logic 是独立构建（settings.gradle.kts 里 includeBuild），
// 主构建的版本目录不会自动带过来，这里显式指同一份 libs.versions.toml
dependencyResolutionManagement {
    repositories {
        mavenLocal()
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
        google {
            content {
                includeGroupByRegex("com\\.android.*")
                includeGroupByRegex("com\\.google.*")
                includeGroupByRegex("androidx.*")
                excludeGroupByRegex("com\\.google\\.devtools.*")
            }
        }
        maven {
            name = "AliyunCentral"
            url = uri("https://maven.aliyun.com/repository/central")
        }
        mavenCentral()
        gradlePluginPortal()
    }
    versionCatalogs {
        create("libs") {
            from(files("../gradle/libs.versions.toml"))
        }
    }
}

rootProject.name = "build-logic"
include(":convention")
