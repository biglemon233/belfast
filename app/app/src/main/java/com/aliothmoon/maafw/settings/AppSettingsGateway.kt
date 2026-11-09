package com.aliothmoon.maafw.settings

import com.aliothmoon.maafw.domain.OverlayControlMode
import com.aliothmoon.maafw.domain.RunMode
import com.aliothmoon.maafw.provision.RootfsSourceKind
import com.aliothmoon.maafw.theme.ThemeStyle
import kotlinx.coroutines.flow.StateFlow

/**
 * ViewModel 侧看得见的那部分 app 设置；实现是 [AppSettingsManager]
 * 与 [com.aliothmoon.maafw.privileged.PermissionGateway] 同一路子：让 VM 测试能塞 fake，
 * 而不必把 DataStore 一起拖进来
 */
interface AppSettingsGateway {
    val runMode: StateFlow<RunMode>
    suspend fun setRunMode(mode: RunMode)

    val overlayControlMode: StateFlow<OverlayControlMode>
    suspend fun setOverlayControlMode(mode: OverlayControlMode)

    val screenSaverEnabled: StateFlow<Boolean>
    suspend fun setScreenSaverEnabled(enabled: Boolean)

    val autoCleanLogs: StateFlow<Boolean>
    suspend fun setAutoCleanLogs(enabled: Boolean)

    val alasErrorNotifications: StateFlow<Boolean>
    suspend fun setAlasErrorNotifications(enabled: Boolean)

    val lowPrecisionRecognition: StateFlow<Boolean>
    suspend fun setLowPrecisionRecognition(enabled: Boolean)

    val themeStyle: StateFlow<ThemeStyle>
    suspend fun setThemeStyle(style: ThemeStyle)

    /** ALAS 热更新源（git URL）；空串=默认 git://git.lyoko.io/AzurLaneAutoScript */
    val updateSource: StateFlow<String>
    suspend fun setUpdateSource(url: String)

    /** ALAS 热更新分支；默认 master */
    val updateBranch: StateFlow<String>
    suspend fun setUpdateBranch(branch: String)

    /** lite 构建首启 rootfs 下载源类型 */
    val rootfsSourceKind: StateFlow<RootfsSourceKind>
    suspend fun setRootfsSourceKind(kind: RootfsSourceKind)
    val rootfsRuntimeAbi: StateFlow<String>
    suspend fun setRootfsRuntimeAbi(abi: String)

    /** 自定义 rootfs 下载直链；仅 CUSTOM 源生效 */
    val rootfsCustomUrl: StateFlow<String>
    suspend fun setRootfsCustomUrl(url: String)

    /** GitHub 源是否走镜像前缀加速 */
    val rootfsUseMirror: StateFlow<Boolean>
    suspend fun setRootfsUseMirror(enabled: Boolean)

    /** 镜像前缀列表（按序尝试） */
    val rootfsMirrors: StateFlow<List<String>>
    suspend fun setRootfsMirrors(mirrors: List<String>)

}
