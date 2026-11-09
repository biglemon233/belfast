package com.aliothmoon.maafw.ui.setup

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import com.aliothmoon.maafw.R
import com.aliothmoon.maafw.provision.ProvisionIntent
import com.aliothmoon.maafw.provision.ProvisionState
import com.aliothmoon.maafw.provision.RootfsDownloadUiState
import com.aliothmoon.maafw.provision.RootfsSourceKind
import com.aliothmoon.maafw.provision.RootfsSourceValidator
import com.aliothmoon.maafw.theme.MaaDesignTokens
import com.aliothmoon.maafw.theme.MaaTheme
import com.aliothmoon.maafw.ui.alas.formatTransferBytes
import com.aliothmoon.maafw.ui.components.ITextField
import com.aliothmoon.maafw.ui.components.MaaButton
import com.aliothmoon.maafw.ui.components.MaaFieldLabel
import com.aliothmoon.maafw.ui.components.MaaSingleChoiceFlow
import com.aliothmoon.maafw.ui.components.MaaSwitch

/**
 * 首启 rootfs 部署页：未完成时整屏接管（AppRoot 的门）
 *
 * 完整版全程唯一动作是「等」；lite 版（未内置构建）在 NotBundled 停下让用户选源下载；
 * 失败/低磁盘给原因与重试，跳过只留给开发包
 */
@Composable
fun ProvisionScreen(
    state: ProvisionState,
    onRetry: () -> Unit,
    onSkip: (() -> Unit)?,
    downloadState: RootfsDownloadUiState?,
    onDownloadIntent: (ProvisionIntent) -> Unit,
    modifier: Modifier = Modifier,
) {
    Surface(
        modifier = modifier.fillMaxSize(),
        color = MaterialTheme.colorScheme.background,
    ) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(MaaTheme.style.cardInnerPadding),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(
                text = stringResource(R.string.provision_title),
                style = MaterialTheme.typography.headlineMedium,
            )
            Spacer(Modifier.height(MaaDesignTokens.Spacing.md))
            Text(
                text = stringResource(R.string.provision_subtitle),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
            Spacer(Modifier.height(MaaDesignTokens.Spacing.xl))

            when (state) {
                ProvisionState.Checking -> {
                    LinearProgressIndicator(Modifier.fillMaxWidth())
                    StatusLine(stringResource(R.string.provision_checking))
                }

                is ProvisionState.Downloading -> DownloadingSection(state)

                is ProvisionState.Extracting -> {
                    val progress =
                        if (state.totalBytes > 0) state.doneBytes.toFloat() / state.totalBytes else 0f
                    LinearProgressIndicator(
                        progress = { progress },
                        modifier = Modifier.fillMaxWidth(),
                    )
                    StatusLine(
                        stringResource(
                            R.string.provision_extracting,
                            (progress * 100).toInt(),
                            state.doneBytes / 1_000_000,
                            state.totalBytes / 1_000_000,
                        )
                    )
                    Text(
                        stringResource(R.string.provision_throughput, formatTransferBytes(state.bytesPerSecond)),
                        style = MaterialTheme.typography.bodySmall,
                    )
                }

                is ProvisionState.LowDisk -> {
                    StatusLine(
                        stringResource(
                            R.string.provision_low_disk,
                            state.freeBytes / 1_000_000,
                        ),
                        isError = true,
                    )
                    RetryButton(onRetry)
                }

                ProvisionState.NotBundled -> {
                    if (downloadState != null) {
                        DownloadSourcePicker(downloadState, onDownloadIntent)
                    } else {
                        StatusLine(
                            stringResource(R.string.provision_not_bundled),
                            isError = true,
                        )
                        RetryButton(onRetry)
                    }
                    if (onSkip != null) {
                        Spacer(Modifier.height(MaaDesignTokens.Spacing.sm))
                        MaaButton(onClick = onSkip) {
                            Text(stringResource(R.string.provision_skip))
                        }
                    }
                }

                is ProvisionState.Failed -> {
                    StatusLine(
                        stringResource(R.string.provision_failed, state.reason),
                        isError = true,
                    )
                    RetryButton(onRetry)
                }

                ProvisionState.Ready -> Unit // 门已开，本页不再渲染
            }
        }
    }
}

/** lite 首启下载进度：分母可用走确定进度，拿不到（chunked）转不确定态只报已下载量 */
@Composable
private fun DownloadingSection(state: ProvisionState.Downloading) {
    if (state.totalBytes > 0) {
        val progress = state.doneBytes.toFloat() / state.totalBytes
        LinearProgressIndicator(
            progress = { progress },
            modifier = Modifier.fillMaxWidth(),
        )
        StatusLine(
            stringResource(
                R.string.provision_downloading,
                (progress * 100).toInt(),
                state.doneBytes / 1_000_000,
                state.totalBytes / 1_000_000,
            )
        )
    } else {
        LinearProgressIndicator(Modifier.fillMaxWidth())
        StatusLine(stringResource(R.string.provision_downloaded_unknown, state.doneBytes / 1_000_000))
    }
    Text(
        stringResource(R.string.provision_download_throughput, formatTransferBytes(state.bytesPerSecond)),
        style = MaterialTheme.typography.bodySmall,
    )
}

/** lite 构建的首启下载源选择：GitHub Release（可挂镜像）或自定义直链 */
@Composable
private fun DownloadSourcePicker(
    ui: RootfsDownloadUiState,
    onIntent: (ProvisionIntent) -> Unit,
) {
    Text(
        text = stringResource(R.string.provision_lite_intro),
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
    )
    Spacer(Modifier.height(MaaDesignTokens.Spacing.lg))

    MaaFieldLabel(stringResource(R.string.provision_runtime_architecture), Modifier.fillMaxWidth())
    MaaSingleChoiceFlow(
        options = listOf("arm64-v8a" to "ARM64", "x86_64" to "x86_64 (amd64)"),
        selected = ui.abi,
        onSelect = { onIntent(ProvisionIntent.SetArchitecture(it)) },
    )
    Text(stringResource(R.string.provision_architecture_hint), style = MaterialTheme.typography.bodySmall)
    Spacer(Modifier.height(MaaDesignTokens.Spacing.sm))

    MaaFieldLabel(stringResource(R.string.provision_download_source), Modifier.fillMaxWidth())
    Spacer(Modifier.height(MaaDesignTokens.Spacing.xs))
    MaaSingleChoiceFlow(
        options = listOf(
            RootfsSourceKind.GITHUB to stringResource(R.string.provision_source_github),
            RootfsSourceKind.CUSTOM to stringResource(R.string.provision_source_custom),
        ),
        selected = ui.sourceKind,
        onSelect = { onIntent(ProvisionIntent.SetSourceKind(it)) },
    )

    Spacer(Modifier.height(MaaDesignTokens.Spacing.sm))
    when (ui.sourceKind) {
        RootfsSourceKind.GITHUB -> {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(stringResource(R.string.provision_use_mirror), style = MaterialTheme.typography.bodyMedium)
                MaaSwitch(
                    checked = ui.useMirror,
                    onCheckedChange = { onIntent(ProvisionIntent.SetUseMirror(it)) },
                )
            }
            if (ui.useMirror) {
                Spacer(Modifier.height(MaaDesignTokens.Spacing.xs))
                ui.mirrors.forEachIndexed { index, prefix ->
                    ITextField(
                        value = prefix,
                        onValueChange = { onIntent(ProvisionIntent.SetMirror(index, it)) },
                        label = stringResource(R.string.provision_mirror_label, index + 1),
                        singleLine = true,
                    )
                    Spacer(Modifier.height(MaaDesignTokens.Spacing.xs))
                }
            }
        }

        RootfsSourceKind.CUSTOM -> {
            ITextField(
                value = ui.customUrl,
                onValueChange = { onIntent(ProvisionIntent.SetCustomUrl(it)) },
                placeholder = stringResource(R.string.provision_custom_url_placeholder),
                singleLine = true,
            )
            Text(
                text = stringResource(R.string.provision_archive_url_hint),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }

    ui.rejection?.let {
        Spacer(Modifier.height(MaaDesignTokens.Spacing.xs))
        Text(
            text = stringResource(R.string.provision_error_invalid_url),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
            textAlign = TextAlign.Center,
        )
    }

    Spacer(Modifier.height(MaaDesignTokens.Spacing.lg))
    MaaButton(onClick = { onIntent(ProvisionIntent.StartDownload) }) {
        Text(stringResource(R.string.provision_download))
    }
}

@Composable
private fun StatusLine(text: String, isError: Boolean = false) {
    Spacer(Modifier.height(MaaDesignTokens.Spacing.md))
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = if (isError) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
    )
}

@Composable
private fun RetryButton(onRetry: () -> Unit) {
    Spacer(Modifier.height(MaaDesignTokens.Spacing.lg))
    MaaButton(onClick = onRetry) {
        Text(stringResource(R.string.provision_retry))
    }
}
