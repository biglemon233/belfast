package com.aliothmoon.maafw.ui.logs

import android.content.Intent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.toggleable
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Save
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material3.Icon
import androidx.compose.material3.Checkbox
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import com.aliothmoon.maafw.R
import com.aliothmoon.maafw.log.LogExportKind
import com.aliothmoon.maafw.log.LogExportItem
import com.aliothmoon.maafw.log.LogExportService
import com.aliothmoon.maafw.theme.MaaDesignTokens
import com.aliothmoon.maafw.ui.components.MaaModalSheet
import com.aliothmoon.maafw.ui.components.MaaSheetHeader
import com.aliothmoon.maafw.ui.components.maaClickable
import kotlinx.coroutines.launch
import kotlinx.coroutines.CancellationException
import org.koin.compose.koinInject

/**
 * 导出日志：sheet 显隐、SAF 选位置、分享 Intent、重复点击保护
 *
 * [kind] 为空 = 不显示。两类导出（ALAS / 启动器）共用这一个 sheet，标题与产物按类型走
 *
 * 必须无条件挂在调用方的组合顶层——`rememberLauncherForActivityResult` 的注册要稳定，
 * 跟着 sheet 的显隐一起装卸的话，回调回来时注册已经没了
 *
 * 反馈经 [onMessage] 交给调用方：提示该显示在哪（snackbar / toast）由承载它的那一层决定
 */
@Composable
fun LogExportController(
    kind: LogExportKind?,
    onDismiss: () -> Unit,
    onMessage: (String) -> Unit,
    service: LogExportService = koinInject(),
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    // Keep the exact selection across sheet dismissal and SAF activity recreation.
    var pendingKind by rememberSaveable { mutableStateOf(LogExportKind.LAUNCHER) }
    var pendingIds by rememberSaveable { mutableStateOf(arrayListOf<String>()) }

    val runningText = stringResource(R.string.log_export_running)
    val failedText = stringResource(R.string.log_export_failed)

    fun runExport(action: suspend () -> Unit) {
        scope.launch {
            try {
                action()
            } catch (error: CancellationException) {
                throw error
            } catch (_: Exception) {
                onMessage(failedText)
            } finally {
                busy = false
            }
        }
    }

    val saveLauncher = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.CreateDocument(MIME_ZIP),
    ) { uri ->
        if (uri == null) {
            pendingIds = arrayListOf()
            busy = false
            return@rememberLauncherForActivityResult
        }
        val targetKind = pendingKind
        val targetIds = pendingIds.toSet()
        pendingIds = arrayListOf()
        busy = true
        onMessage(runningText)
        runExport {
            val name = service.exportTo(targetKind, targetIds, uri)
            onMessage(name?.let { context.getString(R.string.log_export_saved, it) } ?: failedText)
        }
    }

    if (kind == null) return

    var entries by remember(kind) { mutableStateOf<List<LogExportItem>?>(null) }
    var selectedIds by remember(kind) { mutableStateOf(emptySet<String>()) }
    var loadFailed by remember(kind) { mutableStateOf(false) }
    LaunchedEffect(kind) {
        try {
            val loaded = service.availableItems(kind)
            entries = loaded
            selectedIds = loaded.map { it.id }.toSet()
        } catch (error: CancellationException) {
            throw error
        } catch (_: Exception) {
            loadFailed = true
            entries = emptyList()
        }
    }
    val selectionEnabled = !busy && pendingIds.isEmpty()
    val canExport = selectionEnabled && selectedIds.isNotEmpty()

    val title = stringResource(
        when (kind) {
            LogExportKind.ALAS -> R.string.log_export_alas_title
            LogExportKind.LAUNCHER -> R.string.log_export_launcher_title
        },
    )

    MaaModalSheet(onDismiss = onDismiss) { modifier ->
        Column(modifier = modifier) {
            MaaSheetHeader(title = title, onClose = onDismiss)
            val available = entries.orEmpty()
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = stringResource(R.string.log_export_selected, selectedIds.size, available.size),
                    style = MaterialTheme.typography.bodyMedium,
                    modifier = Modifier.weight(1f),
                )
                val allSelected = available.isNotEmpty() && selectedIds.size == available.size
                TextButton(
                    enabled = selectionEnabled && available.isNotEmpty(),
                    onClick = {
                        selectedIds = if (allSelected) emptySet() else available.map { it.id }.toSet()
                    },
                ) {
                    Text(stringResource(if (allSelected) R.string.log_export_select_none else R.string.log_export_select_all))
                }
            }
            LazyColumn(modifier = Modifier.weight(1f)) {
                if (available.isEmpty()) {
                    item {
                        Text(
                            text = stringResource(when {
                                entries == null -> R.string.common_loading
                                loadFailed -> R.string.log_export_failed
                                else -> R.string.log_export_empty
                            }),
                            modifier = Modifier.padding(vertical = MaaDesignTokens.Spacing.md),
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
                items(available, key = { it.id }) { entry ->
                    ExportSelectionRow(
                        entry = entry,
                        checked = entry.id in selectedIds,
                        enabled = selectionEnabled,
                        onCheckedChange = { checked ->
                            selectedIds = if (checked) selectedIds + entry.id else selectedIds - entry.id
                        },
                    )
                }
            }
            HorizontalDivider()
            ExportAction(
                label = stringResource(R.string.log_export_share),
                icon = Icons.Outlined.Share,
                enabled = canExport,
                onClick = {
                    if (busy || pendingIds.isNotEmpty() || selectedIds.isEmpty()) return@ExportAction
                    val selection = selectedIds.toSet()
                    busy = true
                    onDismiss()
                    onMessage(runningText)
                    runExport {
                        val intent = service.shareIntent(kind, selection)
                        if (intent == null) onMessage(failedText)
                        else context.startActivity(Intent.createChooser(intent, title))
                    }
                },
            )
            ExportAction(
                label = stringResource(R.string.log_export_save),
                icon = Icons.Outlined.Save,
                enabled = canExport,
                onClick = {
                    if (busy || pendingIds.isNotEmpty() || selectedIds.isEmpty()) return@ExportAction
                    busy = true
                    pendingKind = kind
                    pendingIds = ArrayList(selectedIds)
                    onDismiss()
                    // 打包推迟到用户选完位置：选一半退出去就白打了
                    try {
                        saveLauncher.launch(service.suggestedFileName(kind))
                    } catch (_: Exception) {
                        pendingIds = arrayListOf()
                        busy = false
                        onMessage(failedText)
                    }
                },
            )
        }
    }
}

@Composable
private fun ExportSelectionRow(
    entry: LogExportItem,
    checked: Boolean,
    enabled: Boolean,
    onCheckedChange: (Boolean) -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .toggleable(value = checked, enabled = enabled, role = Role.Checkbox, onValueChange = onCheckedChange)
            .padding(vertical = MaaDesignTokens.Spacing.sm),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(MaaDesignTokens.Spacing.sm),
    ) {
        Checkbox(checked = checked, onCheckedChange = null, enabled = enabled)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = entry.errorTimestamp?.let(::alasErrorTitle) ?: entry.id,
                style = MaterialTheme.typography.bodyMedium,
            )
            Text(
                text = if (entry.errorTimestamp != null) {
                    stringResource(R.string.log_export_error_meta, entry.files.size, formatFileSize(entry.sizeBytes))
                } else {
                    stringResource(R.string.app_log_meta, formatFileSize(entry.sizeBytes), logTimestamp(entry.lastModified))
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun ExportAction(label: String, icon: ImageVector, enabled: Boolean, onClick: () -> Unit) {
    val color = if (enabled) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurface.copy(alpha = 0.38f)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .maaClickable(enabled = enabled, onClick = onClick)
            .padding(vertical = MaaDesignTokens.Spacing.md),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(MaaDesignTokens.Spacing.md),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = color,
            modifier = Modifier.size(MaaDesignTokens.IconSize.md),
        )
        Text(text = label, style = MaterialTheme.typography.bodyLarge, color = color)
    }
}

private const val MIME_ZIP = "application/zip"
