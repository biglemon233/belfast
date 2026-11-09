package com.aliothmoon.maafw.settings

import com.aliothmoon.maafw.domain.RemoteBackend
import com.aliothmoon.maafw.domain.RunMode
import com.aliothmoon.maafw.domain.ThemeMode
import com.aliothmoon.maafw.privileged.RemoteAccessState
import com.aliothmoon.maafw.theme.ThemeStyle

/**
 * 设置页聚合态
 *
 * 后端的写走 PermissionGateway.setBackend（带 unbind 副作用），不直接落 AppSettings——
 * 跳过 unbind 会连着错的特权进程
 */
data class SettingsUiState(
    val remoteAccess: RemoteAccessState = RemoteAccessState(),
    val themeMode: ThemeMode = ThemeMode.System,
    val themeStyle: ThemeStyle = ThemeStyle.MATERIAL,
    val autoCleanLogs: Boolean = true,
    val alasErrorNotifications: Boolean = true,
    val lowPrecisionRecognition: Boolean = false,
    val runMode: RunMode = RunMode.BACKGROUND,
    val updateSource: String = "",
    val updateBranch: String = "master",
)

sealed interface SettingsIntent {
    data class SetAlasErrorNotifications(val enabled: Boolean) : SettingsIntent
    /** 切换 Shizuku / Root 后端；落到 AppSettings.startupBackend 并断开当前特权进程 */
    data class SetBackend(val backend: RemoteBackend) : SettingsIntent

    data class SetThemeMode(val mode: ThemeMode) : SettingsIntent

    data class SetThemeStyle(val style: ThemeStyle) : SettingsIntent

    /** null 恢复跟随系统；切换后 Activity 重建 */
    data class SetLanguage(val tag: String?) : SettingsIntent

    data class SetAutoCleanLogs(val enabled: Boolean) : SettingsIntent

    /** 运行模式：BACKGROUND=虚拟屏承载游戏，PRIMARY=主屏全屏（虚拟屏不被 ROM 放行时的回退） */
    data class SetRunMode(val mode: RunMode) : SettingsIntent

    /** ALAS 热更新源（git URL）；空串=默认 git://git.lyoko.io/AzurLaneAutoScript */
    data class SetUpdateSource(val url: String) : SettingsIntent

    /** ALAS 热更新分支；空串=master */
    data class SetUpdateBranch(val branch: String) : SettingsIntent
}
