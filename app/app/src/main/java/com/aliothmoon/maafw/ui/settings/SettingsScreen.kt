package com.aliothmoon.maafw.ui.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.TextButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.res.stringResource
import com.aliothmoon.maafw.BuildConfig
import com.aliothmoon.maafw.R
import com.aliothmoon.maafw.domain.RemoteBackend
import com.aliothmoon.maafw.privileged.RemoteAccessPort
import com.aliothmoon.maafw.privileged.PermissionManager
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import com.aliothmoon.maafw.domain.ThemeMode
import com.aliothmoon.maafw.i18n.AppLocales
import com.aliothmoon.maafw.settings.SettingsIntent
import com.aliothmoon.maafw.settings.SettingsUiState
import com.aliothmoon.maafw.theme.MaaTheme
import com.aliothmoon.maafw.theme.ThemeStyle
import com.aliothmoon.maafw.theme.MaaDesignTokens
import com.aliothmoon.maafw.ui.components.MaaCard
import com.aliothmoon.maafw.ui.components.MaaFieldLabel
import com.aliothmoon.maafw.ui.components.MaaInfoRow
import com.aliothmoon.maafw.ui.components.MaaLabeledControlRow
import com.aliothmoon.maafw.ui.components.MaaNavigationRow
import com.aliothmoon.maafw.ui.components.MaaSingleChoiceFlow
import com.aliothmoon.maafw.ui.components.MaaSwitch
import androidx.compose.runtime.LaunchedEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.platform.LocalContext
import com.aliothmoon.maafw.MaaDispatchers
import com.aliothmoon.maafw.settings.AlasSource
import com.aliothmoon.maafw.settings.AlasSourceRepository
import com.aliothmoon.maafw.settings.AppSettingsManager
import com.aliothmoon.maafw.settings.AppSettingsGateway
import com.aliothmoon.maafw.ui.components.LowPrecisionRecognitionDialog
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.koin.compose.koinInject
import java.io.File
import androidx.compose.ui.text.input.PasswordVisualTransformation
import com.aliothmoon.maafw.ui.components.ITextField

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    state: SettingsUiState,
    onIntent: (SettingsIntent) -> Unit,
    // 二级页面与 SAF 都需要 Activity 宿主，导航与弹窗归 AppRoot 那一层
    onOpenAppLog: () -> Unit,
    onOpenAlasLog: () -> Unit,
    onExportAlasLogs: () -> Unit,
    onExportLauncherLogs: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.fillMaxSize()) {
        TopAppBar(
            title = {
                Text(
                    text = stringResource(R.string.nav_settings),
                    style = MaterialTheme.typography.headlineMedium,
                    fontWeight = if (MaaTheme.isMaterial) FontWeight.Normal else FontWeight.SemiBold,
                )
            },
            windowInsets = WindowInsets(0, 0, 0, 0),
            colors = TopAppBarDefaults.topAppBarColors(
                containerColor = MaterialTheme.colorScheme.background,
                titleContentColor = MaterialTheme.colorScheme.onBackground,
                actionIconContentColor = MaterialTheme.colorScheme.primary,
            ),
        )
        Column(
            modifier = Modifier
                .weight(1f)
                .fillMaxWidth()
                .imePadding()
                .verticalScroll(rememberScrollState())
                .padding(
                    start = MaaDesignTokens.Spacing.lg,
                    end = MaaDesignTokens.Spacing.lg,
                    top = MaaDesignTokens.Spacing.sm,
                    bottom = MaaDesignTokens.Spacing.lg,
                ),
            verticalArrangement = Arrangement.spacedBy(MaaDesignTokens.Spacing.lg),
        ) {
            DisplayCard(state, onIntent)
            LogCard(state, onIntent, onOpenAppLog, onOpenAlasLog, onExportAlasLogs, onExportLauncherLogs)
            OtherCard(state, onIntent)
            StartupCard(state, onIntent)
            UpdateCard()
            AboutCard()
        }
    }
}

@Composable
private fun StartupCard(state: SettingsUiState, onIntent: (SettingsIntent) -> Unit) {
    val accessPort = koinInject<RemoteAccessPort>()
    val permissionManager = koinInject<PermissionManager>()
    var checkingRoot by remember { mutableStateOf(false) }
    val context = LocalContext.current
    val settings: AppSettingsManager = koinInject()
    val boot by settings.startOnBoot.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    val repository = remember { AlasSourceRepository(File(context.filesDir, "rootfs/opt/alas")) }
    var configs by remember { mutableStateOf<List<String>>(emptyList()) }
    var selected by remember { mutableStateOf("") }
    var available by remember { mutableStateOf(false) }
    var saving by remember { mutableStateOf(false) }
    var failed by remember { mutableStateOf(false) }
    LaunchedEffect(repository) {
        withContext(MaaDispatchers.IO) {
            runCatching { repository.readRunConfig() to repository.runConfigs() }
        }.onSuccess { (run, names) ->
            selected = run
            configs = names
            available = true
        }.onFailure { failed = true }
    }
    MaaCard(title = stringResource(R.string.settings_section_startup), collapsible = true) {
        Text(
            stringResource(R.string.permission_backend),
            style = MaterialTheme.typography.bodyLarge,
        )
        MaaSingleChoiceFlow(
            options = RemoteBackend.entries.map { it to it.display },
            selected = state.remoteAccess.configuredBackend,
            onSelect = { onIntent(SettingsIntent.SetBackend(it)) },
            optionEnabled = { state.remoteAccess.canSelect(it) && !checkingRoot },
        )
        if (!state.remoteAccess.rootGranted) {
            Text(
                text = stringResource(if (state.remoteAccess.rootAvailable)
                    R.string.root_permission_required else R.string.root_unavailable),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            TextButton(
                enabled = !checkingRoot,
                onClick = {
                    checkingRoot = true
                    scope.launch {
                        try { accessPort.request(RemoteBackend.ROOT) }
                        finally { checkingRoot = false; permissionManager.refresh() }
                    }
                },
                contentPadding = PaddingValues(horizontal = MaaDesignTokens.Spacing.sm, vertical = 0.dp),
                modifier = Modifier.heightIn(min = MaaDesignTokens.RowHeight.compact),
            ) { Text(stringResource(if (checkingRoot) R.string.permission_requesting else R.string.root_check_permission)) }
        }
        Spacer(Modifier.height(MaaDesignTokens.Spacing.xs))
        Text(
            stringResource(R.string.settings_autorun_config),
            style = MaterialTheme.typography.bodyLarge,
        )
        MaaSingleChoiceFlow(
            options = listOf("" to stringResource(R.string.settings_autorun_none)) +
                (configs + listOf(selected).filter { it.isNotEmpty() }).distinct().map { it to it },
            selected = selected,
            enabled = available && !saving,
            onSelect = { name ->
                saving = true
                scope.launch {
                    try {
                        withContext(MaaDispatchers.IO) { repository.saveRunConfig(name) }
                        selected = name
                        failed = false
                    } catch (cancelled: CancellationException) {
                        throw cancelled
                    } catch (_: Exception) {
                        failed = true
                    } finally { saving = false }
                }
            },
        )
        Text(stringResource(R.string.settings_autorun_desc), style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant)
        MaaLabeledControlRow(
            label = stringResource(R.string.settings_start_on_boot),
            trailing = {
                MaaSwitch(checked = boot, onCheckedChange = { enabled ->
                    scope.launch {
                        try {
                            settings.setStartOnBoot(enabled)
                        } catch (cancelled: CancellationException) { throw cancelled
                        } catch (_: Exception) { failed = true }
                    }
                })
            },
        )
        Text(stringResource(R.string.settings_start_on_boot_desc), style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant)
        if (failed) Text(stringResource(R.string.settings_startup_save_failed),
            color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
    }
}

/** Material 3 appearance and application language. */
@Composable
private fun DisplayCard(state: SettingsUiState, onIntent: (SettingsIntent) -> Unit) {
    MaaCard(title = stringResource(R.string.settings_section_display), collapsible = true) {
        MaaFieldLabel(stringResource(R.string.settings_theme))
        val modes = listOf(
            ThemeMode.System to stringResource(R.string.settings_follow_system),
            ThemeMode.Light to stringResource(R.string.settings_theme_light),
            ThemeMode.Dark to stringResource(R.string.settings_theme_dark),
        )
        MaaSingleChoiceFlow(
            options = modes,
            selected = state.themeMode,
            onSelect = { onIntent(SettingsIntent.SetThemeMode(it)) },
        )
        Spacer(Modifier.height(MaaDesignTokens.Spacing.sm))
        MaaFieldLabel(stringResource(R.string.settings_theme_style))
        MaaSingleChoiceFlow(
            options = listOf(
                ThemeStyle.MATERIAL to stringResource(R.string.settings_theme_style_material),
                ThemeStyle.DEFAULT to stringResource(R.string.settings_theme_style_default),
                ThemeStyle.SEMI_DESIGN to stringResource(R.string.settings_theme_style_semi),
            ),
            selected = state.themeStyle,
            onSelect = { onIntent(SettingsIntent.SetThemeStyle(it)) },
        )
        Spacer(Modifier.height(MaaDesignTokens.Spacing.sm))
        MaaFieldLabel(stringResource(R.string.settings_language))
        LanguageChoice(onIntent)
    }
}

@Composable
private fun ColumnScope.LanguageChoice(onIntent: (SettingsIntent) -> Unit) {
    // 事实来源在平台侧 per-app locale（AppLocales），不进 UserConfiguration；
    // 切换后 Activity 重建，本处在新组合中重新读取，无需观察流
    // 语言名按惯例保持本族语原文，不随界面语言翻译
    val options = listOf<Pair<String?, String>>(
        null to stringResource(R.string.settings_follow_system),
        "zh-CN" to "简体中文",
        "en" to "English",
    )
    // 选中态用本地 state 立即回显：切到效果相同的档位（如 跟随系统(中文) ↔ 简体中文）
    // 不触发 Activity 重建，重新读 AppLocales 的时机不会到来
    var selectedTag by remember {
        mutableStateOf(
            when (val tag = AppLocales.currentTag()) {
                null -> null
                else -> if (tag.startsWith("zh")) "zh-CN" else "en"
            },
        )
    }
    MaaSingleChoiceFlow(
        options = options,
        selected = selectedTag,
        // 重复点选当前档位不发 Intent：避免无意义的 Activity 重建闪屏
        onSelect = { tag ->
            if (tag != selectedTag) {
                selectedTag = tag
                onIntent(SettingsIntent.SetLanguage(tag))
            }
        },
    )
    Text(
        text = stringResource(R.string.settings_language_hint),
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

/**
 * 日志区：两个查看入口 + 两条导出 + 自动清理开关
 *
 * 前四项都是「离开这一页」，只有自动清理是就地开关；关闭走确认弹窗（占空间警告）
 */
@Composable
private fun LogCard(
    state: SettingsUiState,
    onIntent: (SettingsIntent) -> Unit,
    onOpenAppLog: () -> Unit,
    onOpenAlasLog: () -> Unit,
    onExportAlasLogs: () -> Unit,
    onExportLauncherLogs: () -> Unit,
) {
    var showDisableConfirm by remember { mutableStateOf(false) }
    MaaCard(title = stringResource(R.string.settings_section_log), collapsible = true) {
        MaaNavigationRow(
            label = stringResource(R.string.app_log_title),
            description = stringResource(R.string.settings_log_launcher_desc),
            onClick = onOpenAppLog,
        )
        MaaNavigationRow(
            label = stringResource(R.string.alas_log_title),
            description = stringResource(R.string.settings_log_alas_desc),
            onClick = onOpenAlasLog,
        )
        MaaNavigationRow(
            label = stringResource(R.string.log_export_alas_title),
            description = stringResource(R.string.settings_log_export_alas_desc),
            onClick = onExportAlasLogs,
        )
        MaaNavigationRow(
            label = stringResource(R.string.log_export_launcher_title),
            description = stringResource(R.string.settings_log_export_launcher_desc),
            onClick = onExportLauncherLogs,
        )
        // 开启直接落盘；关闭先弹确认：关掉之后过期日志只增不减
        MaaLabeledControlRow(
            label = stringResource(R.string.settings_auto_clean_logs),
            trailing = {
                MaaSwitch(
                    checked = state.autoCleanLogs,
                    onCheckedChange = { enabled ->
                        if (enabled) onIntent(SettingsIntent.SetAutoCleanLogs(true))
                        else showDisableConfirm = true
                    },
                )
            },
        )
        Text(
            text = stringResource(R.string.settings_auto_clean_logs_desc),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
    if (showDisableConfirm) {
        AlertDialog(
            onDismissRequest = { showDisableConfirm = false },
            title = { Text(stringResource(R.string.dialog_disable_auto_clean_title)) },
            text = { Text(stringResource(R.string.dialog_disable_auto_clean_message)) },
            confirmButton = {
                TextButton(onClick = {
                    showDisableConfirm = false
                    onIntent(SettingsIntent.SetAutoCleanLogs(false))
                }) { Text(stringResource(R.string.dialog_confirm)) }
            },
            dismissButton = {
                TextButton(onClick = { showDisableConfirm = false }) {
                    Text(stringResource(R.string.dialog_cancel))
                }
            },
        )
    }
}

/** Recognition and notification preferences. */
@Composable
private fun OtherCard(state: SettingsUiState, onIntent: (SettingsIntent) -> Unit) {
    val appSettings = koinInject<AppSettingsGateway>()
    val scope = rememberCoroutineScope()
    var showRecognitionDialog by remember { mutableStateOf(false) }
    if (showRecognitionDialog) {
        LowPrecisionRecognitionDialog { enabled ->
            if (enabled) appSettings.setLowPrecisionRecognition(true)
            showRecognitionDialog = false
        }
    }
    MaaCard(title = stringResource(R.string.settings_section_other), collapsible = true) {
        MaaLabeledControlRow(
            label = stringResource(R.string.settings_low_precision_recognition),
            trailing = {
                MaaSwitch(
                    checked = state.lowPrecisionRecognition,
                    onCheckedChange = { enabled ->
                        if (enabled) showRecognitionDialog = true
                        else scope.launch { appSettings.setLowPrecisionRecognition(false) }
                    },
                )
            },
        )
        Text(
            stringResource(R.string.settings_low_precision_recognition_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        MaaLabeledControlRow(
            label = stringResource(R.string.settings_alas_error_notifications),
            trailing = {
                MaaSwitch(checked = state.alasErrorNotifications,
                    onCheckedChange = { onIntent(SettingsIntent.SetAlasErrorNotifications(it)) })
            },
        )
        Text(stringResource(R.string.settings_alas_error_notifications_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant)

    }
}

@Composable
private fun AboutCard() {
    MaaCard(title = stringResource(R.string.settings_about), collapsible = true) {
        MaaInfoRow(stringResource(R.string.settings_version), BuildConfig.VERSION_NAME)
        MaaInfoRow(stringResource(R.string.settings_build), BuildConfig.VERSION_CODE.toString())
    }
}

/** Deploy is the source of truth. Drafts are saved as one revision-checked transaction. */
@Composable
private fun UpdateCard() {
    val context = LocalContext.current.applicationContext
    val legacySettings = koinInject<AppSettingsManager>()
    val repository = remember(context) { AlasSourceRepository(File(context.filesDir, "rootfs/opt/alas")) }
    val scope = rememberCoroutineScope()
    var snapshot by remember { mutableStateOf<AlasSourceRepository.Snapshot?>(null) }
    var sourceDraft by remember { mutableStateOf("") }
    var branchDraft by remember { mutableStateOf("master") }
    var customDraft by remember { mutableStateOf("") }
    var sourceVisible by remember { mutableStateOf(false) }
    var preset by remember { mutableStateOf("custom") }
    var busy by remember { mutableStateOf(true) }
    var message by remember { mutableStateOf<Int?>(null) }

    fun display(value: AlasSourceRepository.Snapshot) {
        snapshot = value
        sourceVisible = false
        sourceDraft = value.source.repository
        branchDraft = value.source.branch
        preset = when (sourceDraft) {
            AlasSource.DOMESTIC -> "domestic"
            AlasSource.GITHUB -> "github"
            else -> "custom"
        }
        if (preset == "custom") customDraft = sourceDraft
    }

    suspend fun reload() {
        busy = true
        try {
            // One DataStore emission is an atomic legacy pair; never read two StateFlows separately.
            val legacy = legacySettings.settings.first()
            display(withContext(MaaDispatchers.IO) { repository.migrateLegacy(legacy.updateSource, legacy.updateBranch) })
            message = null
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            snapshot = null
            message = R.string.settings_source_unavailable
        } finally { busy = false }
    }
    LaunchedEffect(repository) { reload() }

    MaaCard(title = stringResource(R.string.settings_section_update), collapsible = true) {
        MaaFieldLabel(stringResource(R.string.settings_update_source))
        MaaSingleChoiceFlow(
            options = listOf(
                "domestic" to stringResource(R.string.settings_source_domestic),
                "github" to "GitHub",
                "custom" to stringResource(R.string.settings_update_branch_custom),
            ),
            selected = preset,
            enabled = !busy,
            onSelect = { selected ->
                if (!busy) {
                    if (preset == "custom") customDraft = sourceDraft
                    preset = selected
                    sourceDraft = when (selected) {
                        "domestic" -> AlasSource.DOMESTIC
                        "github" -> AlasSource.GITHUB
                        else -> customDraft
                    }
                }
            },
        )
        if (preset == "custom") {
            ITextField(
                value = sourceDraft,
                onValueChange = { if (!busy) { sourceDraft = it; customDraft = it } },
                placeholder = stringResource(R.string.settings_update_source_placeholder),
                visualTransformation = if (sourceVisible) androidx.compose.ui.text.input.VisualTransformation.None
                    else PasswordVisualTransformation(),
                trailingIcon = {
                    IconButton(onClick = { sourceVisible = !sourceVisible }) {
                        Icon(
                            imageVector = if (sourceVisible) Icons.Default.VisibilityOff else Icons.Default.Visibility,
                            contentDescription = stringResource(R.string.settings_update_source_toggle),
                        )
                    }
                },
                singleLine = true,
            )
        }
        Spacer(Modifier.height(MaaDesignTokens.Spacing.xs))
        Text(stringResource(R.string.settings_source_deploy_hint), style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.height(MaaDesignTokens.Spacing.sm))
        MaaFieldLabel(stringResource(R.string.settings_update_branch))
        ITextField(value = branchDraft, onValueChange = { if (!busy) branchDraft = it },
            placeholder = "master", singleLine = true)
        TextButton(enabled = !busy && snapshot != null, onClick = {
            val expected = snapshot ?: return@TextButton
            busy = true
            scope.launch {
                try {
                    val source = AlasSource.fromInput(sourceDraft, branchDraft)
                    display(withContext(MaaDispatchers.IO) { repository.save(expected, source) })
                    message = R.string.settings_source_saved
                } catch (e: CancellationException) {
                    throw e
                } catch (_: AlasSourceRepository.Conflict) {
                    message = R.string.settings_source_conflict
                } catch (_: IllegalArgumentException) {
                    message = R.string.settings_source_invalid
                } catch (_: Exception) {
                    message = R.string.settings_source_unavailable
                } finally { busy = false }
            }
        }) { Text(stringResource(R.string.settings_update_save)) }
        TextButton(enabled = !busy, onClick = { scope.launch { reload() } }) {
            Text(stringResource(R.string.settings_source_reload))
        }
        message?.let { Text(stringResource(it), style = MaterialTheme.typography.bodySmall) }
    }
}
