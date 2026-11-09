// Adapted for AzurPilot for Android: merged platform declarations for JVM 17.
// Copyright 2025, compose-miuix-ui contributors
// SPDX-License-Identifier: Apache-2.0

package top.yukonga.miuix.kmp.utils

import androidx.compose.runtime.Composable
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.window.DialogProperties

/**
 * Platform enum class.
 */
enum class Platform {
    Android,
    IOS,
    Desktop,
    WasmJs,
    MacOS,
    Js,
}
