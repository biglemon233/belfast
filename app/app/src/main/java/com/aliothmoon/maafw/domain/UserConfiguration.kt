package com.aliothmoon.maafw.domain

import kotlinx.serialization.Serializable

enum class ThemeMode { System, Light, Dark }

/** ALAS task configuration lives in the guest; the host only stores its own UI settings. */
@Serializable
data class UserConfiguration(
    val themeMode: ThemeMode = ThemeMode.System,
)
