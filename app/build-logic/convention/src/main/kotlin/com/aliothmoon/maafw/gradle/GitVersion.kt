package com.aliothmoon.maafw.gradle

import org.gradle.api.Project

/** Installation versions are explicit: rewriting Git history must not downgrade an APK. */
internal fun Project.gitVersionCode(): Int {
    val code = providers.gradleProperty("app.versionCode").orNull?.toIntOrNull()
    require(code != null && code in 1..2_100_000_000) {
        "Set app.versionCode in gradle.properties to a positive Android version code"
    }
    return code
}

/** Display versions are explicit too: a shallow or rewritten history must never expose a SHA. */
internal fun Project.gitVersionName(): String {
    val name = providers.gradleProperty("app.versionName").orNull.orEmpty()
    require(Regex("""\d+\.\d+\.\d+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?""").matches(name)) {
        "Set app.versionName in gradle.properties to a version such as 0.1.6 or 0.1.6-alpha.1"
    }
    return name
}
