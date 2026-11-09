package com.aliothmoon.maafw.ui.logs

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.aliothmoon.maafw.R
import com.aliothmoon.maafw.log.AlasDailyLogInfo
import com.aliothmoon.maafw.log.AlasErrorDirInfo
import com.aliothmoon.maafw.log.AlasLogViewModel
import com.aliothmoon.maafw.theme.MaaDesignTokens
import com.aliothmoon.maafw.ui.components.MaaCardSurface
import com.aliothmoon.maafw.ui.components.maaClickable
import org.koin.androidx.compose.koinViewModel

/**
 * ALAS 日志列表（二级页面）：直读内部存储的 `rootfs/opt/alas/log`，不走 wrapper HTTP
 *
 * 两个分区：「错误记录」是 ALAS 出错时落的时间戳现场（log.txt + 截图），
 * 「按天日志」是整天 append 的 txt
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AlasLogScreen(
    onBack: () -> Unit,
    onOpenDaily: (fileName: String) -> Unit,
    onOpenError: (dirName: String) -> Unit,
    viewModel: AlasLogViewModel = koinViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    Scaffold(
        modifier = Modifier.fillMaxSize(),
        containerColor = MaterialTheme.colorScheme.background,
        topBar = {
            TopAppBar(
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.background,
                ),
                title = { Text(stringResource(R.string.alas_log_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.common_back),
                        )
                    }
                },
            )
        },
    ) { padding ->
        val empty = state.errorDirs.isEmpty() && state.dailyLogs.isEmpty()
        if (empty) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(padding)
                    .padding(MaaDesignTokens.Spacing.lg),
                contentAlignment = Alignment.TopStart,
            ) {
                Text(
                    text = stringResource(
                        if (state.loading) R.string.common_loading else R.string.alas_log_empty,
                    ),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            return@Scaffold
        }

        LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
            contentPadding = PaddingValues(MaaDesignTokens.Spacing.lg),
            verticalArrangement = Arrangement.spacedBy(MaaDesignTokens.Spacing.sm),
        ) {
            if (state.errorDirs.isNotEmpty()) {
                item(key = "header_errors") {
                    AlasLogSectionHeader(stringResource(R.string.alas_log_section_errors))
                }
                items(state.errorDirs, key = { "error_${it.name}" }) { dir ->
                    AlasErrorDirRow(dir = dir, onClick = { onOpenError(dir.name) })
                }
            }
            if (state.dailyLogs.isNotEmpty()) {
                item(key = "header_daily") {
                    AlasLogSectionHeader(stringResource(R.string.alas_log_section_daily))
                }
                items(state.dailyLogs, key = { "daily_${it.name}" }) { file ->
                    AlasDailyLogRow(file = file, onClick = { onOpenDaily(file.name) })
                }
            }
        }
    }
}

@Composable
private fun AlasLogSectionHeader(title: String) {
    Text(
        text = title,
        style = MaterialTheme.typography.titleSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = MaaDesignTokens.Spacing.sm),
    )
}

@Composable
private fun AlasErrorDirRow(dir: AlasErrorDirInfo, onClick: () -> Unit) {
    AlasLogRow(
        title = alasErrorTitle(dir.name),
        subtitle = stringResource(R.string.alas_log_error_meta, dir.fileCount),
        onClick = onClick,
    )
}

@Composable
private fun AlasDailyLogRow(file: AlasDailyLogInfo, onClick: () -> Unit) {
    AlasLogRow(
        title = file.name,
        subtitle = stringResource(
            R.string.app_log_meta,
            formatFileSize(file.sizeBytes),
            logTimestamp(file.lastModified),
        ),
        onClick = onClick,
    )
}

@Composable
private fun AlasLogRow(title: String, subtitle: String, onClick: () -> Unit) {
    MaaCardSurface(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .maaClickable(onClick = onClick)
                .padding(MaaDesignTokens.Card.innerPadding),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(
                modifier = Modifier.fillMaxWidth(),
                verticalArrangement = Arrangement.spacedBy(MaaDesignTokens.Spacing.xxs),
            ) {
                Text(text = title, style = MaterialTheme.typography.titleSmall)
                Text(
                    text = subtitle,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}
