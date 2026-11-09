package com.aliothmoon.maafw.settings

import android.content.Context
import android.content.ComponentName
import android.content.pm.PackageManager
import com.aliothmoon.maafw.service.BootReceiver
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.preferencesDataStore
import com.aliothmoon.maafw.MaaDispatchers
import com.aliothmoon.maafw.domain.OverlayControlMode
import com.aliothmoon.maafw.domain.RemoteBackend
import com.aliothmoon.maafw.domain.RunMode
import com.aliothmoon.maafw.provision.RootfsSource
import com.aliothmoon.maafw.provision.RootfsSourceKind
import com.aliothmoon.maafw.theme.ThemeStyle
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

/**
 * app 设置的唯一读写入口
 *
 * 各项以 StateFlow 暴露。读盘是异步的，[loaded] 置位之前 `.value` 还是 schema 默认值——
 * 同步 `.value` 是 [com.aliothmoon.maafw.privileged.RemoteServiceManager] 那条链要的
 * （它收的是 `() -> RemoteBackend`，没有挂起点），所以读盘不能省，只能挪到构造之外
 *
 * **凡是在启动早期同步读 `.value` 的调用方都必须先等 [loaded]**：早读一步拿到的是
 * 默认值，Root 用户会被当成 Shizuku。启动首屏与 `MaaFwApp.postCreate` 都挂在这上面
 */
class AppSettingsManager(private val context: Context) : AppSettingsGateway {

    private val scope = CoroutineScope(SupervisorJob() + MaaDispatchers.IO)

    companion object {
        private val Context.dataStore: DataStore<Preferences> by preferencesDataStore(name = "app_settings")
    }

    val settings: Flow<AppSettings> = with(AppSettingsSchema) { context.dataStore.flow }

    private val defaults = AppSettings()

    private val _loaded = MutableStateFlow(false)

    /**
     * 首次读盘是否已落到下面各 StateFlow 上；置位后 `.value` 才是盘上的值
     *
     * 等待点：启动首屏（`MainActivity`）与 `MaaFwApp.postCreate`（`RemoteServiceManager`
     * 一初始化就同步读 startupBackend）
     */
    val loaded: StateFlow<Boolean> = _loaded.asStateFlow()

    private val _startOnBoot = MutableStateFlow(false)
    val startOnBoot: StateFlow<Boolean> = _startOnBoot.asStateFlow()

    private val _startupBackend = MutableStateFlow(parseBackend(defaults.startupBackend))
    val startupBackend: StateFlow<RemoteBackend> = _startupBackend.asStateFlow()

    private val _skipShizukuCheck = MutableStateFlow(defaults.skipShizukuCheck.toBoolean())
    val skipShizukuCheck: StateFlow<Boolean> = _skipShizukuCheck.asStateFlow()
    private val _skipArchitectureCheck = MutableStateFlow(defaults.skipArchitectureCheck.toBoolean())
    val skipArchitectureCheck: StateFlow<Boolean> = _skipArchitectureCheck.asStateFlow()

    private val _shizukuLaunchPackage = MutableStateFlow(defaults.shizukuLaunchPackage)
    val shizukuLaunchPackage: StateFlow<String> = _shizukuLaunchPackage.asStateFlow()

    private val _runMode = MutableStateFlow(parseRunMode(defaults.runMode))
    override val runMode: StateFlow<RunMode> = _runMode.asStateFlow()

    private val _overlayControlMode = MutableStateFlow(parseOverlayMode(defaults.overlayControlMode))
    override val overlayControlMode: StateFlow<OverlayControlMode> = _overlayControlMode.asStateFlow()

    private val _screenSaverEnabled = MutableStateFlow(defaults.screenSaverEnabled.toBoolean())
    override val screenSaverEnabled: StateFlow<Boolean> = _screenSaverEnabled.asStateFlow()

    private val _autoCleanLogs = MutableStateFlow(defaults.autoCleanLogs.toBoolean())
    override val autoCleanLogs: StateFlow<Boolean> = _autoCleanLogs.asStateFlow()

    private val _alasErrorNotifications = MutableStateFlow(defaults.alasErrorNotifications.toBoolean())
    override val alasErrorNotifications: StateFlow<Boolean> = _alasErrorNotifications.asStateFlow()

    private val _lowPrecisionRecognition = MutableStateFlow(false)
    override val lowPrecisionRecognition: StateFlow<Boolean> = _lowPrecisionRecognition.asStateFlow()
    private val _recognitionChoiceMade = MutableStateFlow(false)
    val recognitionChoiceMade: StateFlow<Boolean> = _recognitionChoiceMade.asStateFlow()

    private val _themeStyle = MutableStateFlow(parseThemeStyle(defaults.themeStyle))
    override val themeStyle: StateFlow<ThemeStyle> = _themeStyle.asStateFlow()

    private val _updateSource = MutableStateFlow(defaults.updateSource)
    override val updateSource: StateFlow<String> = _updateSource.asStateFlow()

    private val _updateBranch = MutableStateFlow(defaults.updateBranch)
    override val updateBranch: StateFlow<String> = _updateBranch.asStateFlow()

    private val _rootfsSourceKind = MutableStateFlow(parseRootfsSourceKind(defaults.rootfsSourceKind))
    override val rootfsSourceKind: StateFlow<RootfsSourceKind> = _rootfsSourceKind.asStateFlow()
    private val _rootfsRuntimeAbi = MutableStateFlow(defaults.rootfsRuntimeAbi)
    override val rootfsRuntimeAbi: StateFlow<String> = _rootfsRuntimeAbi.asStateFlow()

    private val _rootfsCustomUrl = MutableStateFlow(defaults.rootfsCustomUrl)
    override val rootfsCustomUrl: StateFlow<String> = _rootfsCustomUrl.asStateFlow()

    private val _rootfsUseMirror = MutableStateFlow(defaults.rootfsUseMirror.toBoolean())
    override val rootfsUseMirror: StateFlow<Boolean> = _rootfsUseMirror.asStateFlow()

    private val _rootfsMirrors = MutableStateFlow(parseMirrors(defaults.rootfsMirrors))
    override val rootfsMirrors: StateFlow<List<String>> = _rootfsMirrors.asStateFlow()

    init {
        // 一处 collect 铺开到各字段，而不是每个字段各起一条 stateIn：
        // 那样 loaded 置位与各字段拿到首值是两件并发的事，早读的人仍可能读到默认值
        scope.launch {
            settings.collect { s ->
                _startOnBoot.value = s.startOnBoot.toBoolean()
                configureBootReceiver(_startOnBoot.value)
                _startupBackend.value = parseBackend(s.startupBackend)
                _skipShizukuCheck.value = s.skipShizukuCheck.toBoolean()
                _skipArchitectureCheck.value = s.skipArchitectureCheck.toBoolean()
                _shizukuLaunchPackage.value = s.shizukuLaunchPackage
                _runMode.value = parseRunMode(s.runMode)
                _overlayControlMode.value = parseOverlayMode(s.overlayControlMode)
                _screenSaverEnabled.value = s.screenSaverEnabled.toBoolean()
                _autoCleanLogs.value = s.autoCleanLogs.toBoolean()
                _alasErrorNotifications.value = s.alasErrorNotifications.toBoolean()
                _lowPrecisionRecognition.value = s.lowPrecisionRecognition.toBoolean()
                _recognitionChoiceMade.value = s.lowPrecisionRecognition in listOf("true", "false")
                _themeStyle.value = parseThemeStyle(s.themeStyle)
                _updateSource.value = s.updateSource
                _updateBranch.value = s.updateBranch
                _rootfsSourceKind.value = parseRootfsSourceKind(s.rootfsSourceKind)
                _rootfsRuntimeAbi.value = s.rootfsRuntimeAbi
                _rootfsCustomUrl.value = s.rootfsCustomUrl
                _rootfsUseMirror.value = s.rootfsUseMirror.toBoolean()
                _rootfsMirrors.value = parseMirrors(s.rootfsMirrors)
                // 必须是最后一行：置位即宣告上面全部就位
                _loaded.value = true
            }
        }
    }

    suspend fun setStartupBackend(backend: RemoteBackend) = with(AppSettingsSchema) {
        context.dataStore.edit { it[startupBackend] = backend.name }
    }

    suspend fun setStartOnBoot(enabled: Boolean) = with(AppSettingsSchema) {
        context.dataStore.edit { it[startOnBoot] = enabled.toString() }
    }

    private fun configureBootReceiver(enabled: Boolean) {
        val component = ComponentName(context, BootReceiver::class.java)
        val state = if (enabled) PackageManager.COMPONENT_ENABLED_STATE_ENABLED
            else PackageManager.COMPONENT_ENABLED_STATE_DISABLED
        if (context.packageManager.getComponentEnabledSetting(component) != state) {
            context.packageManager.setComponentEnabledSetting(component, state, PackageManager.DONT_KILL_APP)
        }
    }

    suspend fun setSkipArchitectureCheck(skip: Boolean) = with(AppSettingsSchema) {
        context.dataStore.edit { it[skipArchitectureCheck] = skip.toString() }
    }

    suspend fun setSkipShizukuCheck(skip: Boolean) = with(AppSettingsSchema) {
        context.dataStore.edit { it[skipShizukuCheck] = skip.toString() }
    }

    suspend fun setShizukuLaunchPackage(packageName: String) = with(AppSettingsSchema) {
        context.dataStore.edit { it[shizukuLaunchPackage] = packageName }
    }

    override suspend fun setUpdateSource(url: String): Unit = with(AppSettingsSchema) {
        context.dataStore.edit { it[updateSource] = url.trim() }
    }

    override suspend fun setUpdateBranch(branch: String): Unit = with(AppSettingsSchema) {
        context.dataStore.edit { it[updateBranch] = branch.trim() }
    }

    override suspend fun setRootfsSourceKind(kind: RootfsSourceKind): Unit = with(AppSettingsSchema) {
        context.dataStore.edit { it[rootfsSourceKind] = kind.name }
    }

    override suspend fun setRootfsRuntimeAbi(abi: String): Unit = with(AppSettingsSchema) {
        require(abi in listOf("", "arm64-v8a", "x86_64"))
        context.dataStore.edit { it[rootfsRuntimeAbi] = abi }
        _rootfsRuntimeAbi.value = abi
    }

    override suspend fun setRootfsCustomUrl(url: String): Unit = with(AppSettingsSchema) {
        context.dataStore.edit { it[rootfsCustomUrl] = url.trim() }
    }

    override suspend fun setRootfsUseMirror(enabled: Boolean): Unit = with(AppSettingsSchema) {
        context.dataStore.edit { it[rootfsUseMirror] = enabled.toString() }
    }

    override suspend fun setRootfsMirrors(mirrors: List<String>): Unit = with(AppSettingsSchema) {
        context.dataStore.edit {
            it[rootfsMirrors] = mirrors.joinToString(com.aliothmoon.maafw.provision.RootfsSource.MIRROR_SEPARATOR) { m -> m.trim() }
        }
    }

    override suspend fun setRunMode(mode: RunMode): Unit = with(AppSettingsSchema) {
        context.dataStore.edit { it[runMode] = mode.name }
    }

    override suspend fun setOverlayControlMode(mode: OverlayControlMode): Unit = with(AppSettingsSchema) {
        context.dataStore.edit { it[overlayControlMode] = mode.name }
    }

    override suspend fun setScreenSaverEnabled(enabled: Boolean): Unit = with(AppSettingsSchema) {
        context.dataStore.edit { it[screenSaverEnabled] = enabled.toString() }
    }

    override suspend fun setAutoCleanLogs(enabled: Boolean): Unit = with(AppSettingsSchema) {
        context.dataStore.edit { it[autoCleanLogs] = enabled.toString() }
    }

    override suspend fun setAlasErrorNotifications(enabled: Boolean): Unit = with(AppSettingsSchema) {
        context.dataStore.edit { it[alasErrorNotifications] = enabled.toString() }
        _alasErrorNotifications.value = enabled
    }

    override suspend fun setThemeStyle(style: ThemeStyle): Unit = with(AppSettingsSchema) {
        context.dataStore.edit { it[themeStyle] = style.name }
    }

    override suspend fun setLowPrecisionRecognition(enabled: Boolean): Unit = with(AppSettingsSchema) {
        context.dataStore.edit { it[lowPrecisionRecognition] = enabled.toString() }
        _lowPrecisionRecognition.value = enabled
        withContext(MaaDispatchers.IO) {
            LowPrecisionRecognition.write(File(context.filesDir, "proot-tmp/shm"), enabled)
        }
        _recognitionChoiceMade.value = true
    }

    /** 盘上是历史遗留或手改的非法值时回落默认，不让设置读取本身抛异常 */
    private fun parseBackend(raw: String): RemoteBackend =
        runCatching { RemoteBackend.valueOf(raw) }.getOrDefault(RemoteBackend.SHIZUKU)

    private fun parseRunMode(raw: String): RunMode =
        runCatching { RunMode.valueOf(raw) }.getOrDefault(RunMode.BACKGROUND)

    private fun parseOverlayMode(raw: String): OverlayControlMode =
        runCatching { OverlayControlMode.valueOf(raw) }.getOrDefault(OverlayControlMode.FLOAT_BALL)

    private fun parseThemeStyle(raw: String): ThemeStyle =
        ThemeStyle.fromPreference(raw)

    private fun parseRootfsSourceKind(raw: String): RootfsSourceKind =
        runCatching { RootfsSourceKind.valueOf(raw) }.getOrDefault(RootfsSourceKind.GITHUB)

    /** 盘上镜像列表为空时回落默认三件套，避免「清空了所有框又存盘」把默认值永久抹掉 */
    private fun parseMirrors(raw: String): List<String> =
        RootfsSource.splitMirrors(raw).ifEmpty { RootfsSource.splitMirrors(defaults.rootfsMirrors) }
}
