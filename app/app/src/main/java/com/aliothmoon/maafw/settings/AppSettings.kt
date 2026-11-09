package com.aliothmoon.maafw.settings

import com.aliothmoon.preferences.PrefKey
import com.aliothmoon.preferences.PrefSchema

/** Shizuku 官方包名，manifest 的 `<queries>` 里声明的就是它 */
private const val SHIZUKU_PACKAGE = "moe.shizuku.privileged.api"

/**
 * app 级设置，与 `UserConfiguration` 分开存
 *
 * `UserConfiguration` 是运行配置的聚合根，走 schemaVersion 信封 + 版本不符即重置；
 * 提权后端这类设置不该跟着运行配置一起被重置，所以另起一个 Preferences DataStore
 *
 * 字段一律声明成 String：`@PrefSchema` 生成的 key 按字段类型选 preferencesKey，
 * 枚举与布尔都以文本落盘，改默认值不会让老数据变成非法值（见 [AppSettingsManager] 的解析）
 */
@PrefSchema
data class AppSettings(
    /** Start the foreground runtime after the system's unlocked boot broadcast. */
    @PrefKey(default = "false")
    val startOnBoot: String = "false",

    /** [com.aliothmoon.maafw.domain.RemoteBackend] 的 name */
    @PrefKey(default = "SHIZUKU")
    val startupBackend: String = "SHIZUKU",

    /** 用户在引导弹窗上点过「不再提醒」 */
    @PrefKey(default = "false")
    val skipShizukuCheck: String = "false",
    @PrefKey(default = "false")
    val skipArchitectureCheck: String = "false",

    /** Shizuku 管理器的包名；有 ROM 内置了自己的分发，允许指到别处 */
    @PrefKey(default = SHIZUKU_PACKAGE)
    val shizukuLaunchPackage: String = SHIZUKU_PACKAGE,

    /** [com.aliothmoon.maafw.domain.RunMode] 的 name */
    @PrefKey(default = "BACKGROUND")
    val runMode: String = "BACKGROUND",

    /** [com.aliothmoon.maafw.domain.OverlayControlMode] 的 name；仅前台模式生效 */
    @PrefKey(default = "FLOAT_BALL")
    val overlayControlMode: String = "FLOAT_BALL",

    /** 后台模式运行期是否自动盖上屏保；默认关，盖住整块屏幕这种事要用户先点头 */
    @PrefKey(default = "false")
    val screenSaverEnabled: String = "false",

    /** 冷启动自动清理过期日志（ALAS 7 天前日志、过期 session.log 截尾）；默认开 */
    @PrefKey(default = "true")
    val autoCleanLogs: String = "true",

    @PrefKey(default = "true")
    val alasErrorNotifications: String = "true",

    /** Empty until the user explicitly chooses on the first launch. */
    @PrefKey(default = "")
    val lowPrecisionRecognition: String = "",

    /** ThemeStyle name: Material 3 by default; existing classic and Semi preferences remain valid. */
    @PrefKey(default = "MATERIAL")
    val themeStyle: String = "MATERIAL",

    /** Legacy migration input only. Runtime source of truth is config/deploy.yaml. */
    @PrefKey(default = "")
    val updateSource: String = "",

    /** Legacy migration input only; new saves atomically update deploy Repository and Branch. */
    @PrefKey(default = "master")
    val updateBranch: String = "master",

    /** [com.aliothmoon.maafw.provision.RootfsSourceKind] 的 name；lite 构建首启 rootfs 下载源 */
    @PrefKey(default = "GITHUB")
    val rootfsSourceKind: String = "GITHUB",
    /** Empty means automatic native device detection; explicit Lite selection survives restart. */
    @PrefKey(default = "")
    val rootfsRuntimeAbi: String = "",

    /** 自定义 rootfs 下载直链；仅 sourceKind=CUSTOM 时生效 */
    @PrefKey(default = "")
    val rootfsCustomUrl: String = "",

    /** GitHub 源是否走镜像前缀加速（Moontier 同款前缀拼接） */
    @PrefKey(default = "false")
    val rootfsUseMirror: String = "false",

    /** 镜像前缀列表，逗号分隔按序尝试；默认值与 Moontier DEFAULT_CORE_DOWNLOAD_PROXIES 同款（与 RootfsSource.DEFAULT_MIRRORS_JOINED 保持一致） */
    @PrefKey(default = "https://ghfast.top/,https://gh-proxy.com/,https://mirror.ghproxy.com/")
    val rootfsMirrors: String = com.aliothmoon.maafw.provision.RootfsSource.DEFAULT_MIRRORS_JOINED,

)
