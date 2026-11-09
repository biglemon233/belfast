package com.aliothmoon.maafw.ui.alas

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.aliothmoon.maafw.R
import com.aliothmoon.maafw.proot.ProotSnapshot
import com.aliothmoon.maafw.proot.StartupStep
import com.aliothmoon.maafw.proot.StepStatus
import com.aliothmoon.maafw.proot.TransferStage
import java.util.Locale

@Composable
internal fun StartupChecklist(snapshot: ProotSnapshot) {
    val darkBackground = MaterialTheme.colorScheme.background.luminance() < 0.5f
    Column(
        modifier = Modifier.fillMaxWidth().heightIn(max = 380.dp).verticalScroll(rememberScrollState()),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(stringResource(R.string.startup_checklist), style = MaterialTheme.typography.titleMedium)
        snapshot.startup.steps.forEach { entry ->
            val statusText = when (entry.status) {
                StepStatus.WAITING -> R.string.startup_waiting
                StepStatus.ACTIVE -> R.string.startup_active
                StepStatus.DONE -> R.string.startup_done
                StepStatus.WARNING -> R.string.startup_warning
                StepStatus.FAILED -> R.string.startup_failed
                StepStatus.SKIPPED -> R.string.startup_skipped
            }
            val label = when (entry.step) {
                StartupStep.RUNTIME -> R.string.startup_runtime
                StartupStep.CLEANUP -> R.string.startup_cleanup
                StartupStep.RESTORE -> R.string.startup_restore
                StartupStep.OVERLAY -> R.string.startup_overlay
                StartupStep.ENVIRONMENT -> R.string.startup_environment
                StartupStep.CONFIG -> R.string.startup_config
                StartupStep.UPDATE -> R.string.startup_update
                StartupStep.PATCHES -> R.string.startup_patches
                StartupStep.ARGUMENTS -> R.string.startup_arguments
                StartupStep.SESSION -> R.string.startup_session
                StartupStep.SERVICES -> R.string.startup_services
            }
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(stringResource(label), modifier = Modifier.weight(1f), style = MaterialTheme.typography.bodySmall)
                Text(
                    stringResource(statusText),
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.SemiBold,
                    color = when (entry.status) {
                        StepStatus.DONE -> if (darkBackground) Color(0xFF86EFAC) else Color(0xFF166534)
                        StepStatus.ACTIVE -> if (darkBackground) Color(0xFF93C5FD) else Color(0xFF1D4ED8)
                        StepStatus.WARNING -> if (darkBackground) Color(0xFFFCD34D) else Color(0xFF92400E)
                        StepStatus.FAILED -> if (darkBackground) Color(0xFFFCA5A5) else Color(0xFFB91C1C)
                        StepStatus.WAITING, StepStatus.SKIPPED ->
                            if (darkBackground) Color(0xFFCBD5E1) else Color(0xFF475569)
                    },
                )
            }
        }
        snapshot.transfer?.let { transfer ->
            if (transfer.stage == TransferStage.UNPACK) {
                Text(stringResource(R.string.transfer_unpacking), style = MaterialTheme.typography.bodySmall)
                LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
            } else {
                val fraction = transfer.fraction
                if (fraction == null) LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                else LinearProgressIndicator(progress = { fraction }, modifier = Modifier.fillMaxWidth())
                Text(
                    if (fraction == null) stringResource(
                        R.string.transfer_unknown, formatTransferBytes(transfer.downloadedBytes),
                        formatTransferBytes(transfer.bytesPerSecond),
                    ) else stringResource(
                        R.string.transfer_known, formatTransferBytes(transfer.downloadedBytes),
                        formatTransferBytes(transfer.totalBytes!!), (fraction * 100).toInt(),
                        formatTransferBytes(transfer.bytesPerSecond),
                    ),
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        }
    }
}

internal fun formatTransferBytes(bytes: Long): String = when {
    bytes >= 1024L * 1024 * 1024 -> String.format(Locale.US, "%.1f GiB", bytes / (1024.0 * 1024 * 1024))
    bytes >= 1024L * 1024 -> String.format(Locale.US, "%.1f MiB", bytes / (1024.0 * 1024))
    bytes >= 1024 -> String.format(Locale.US, "%.1f KiB", bytes / 1024.0)
    else -> "$bytes B"
}
