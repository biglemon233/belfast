package com.aliothmoon.maafw.ui.components

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.WarningAmber
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.DialogProperties
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.aliothmoon.maafw.R
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** Shared by the first-launch gate and the settings switch. */
@Composable
fun LowPrecisionRecognitionDialog(onDecision: suspend (Boolean) -> Unit) {
    var remaining by remember { mutableIntStateOf(5) }
    var saving by remember { mutableStateOf(false) }
    var saveFailed by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val lifecycleOwner = LocalLifecycleOwner.current
    LaunchedEffect(lifecycleOwner) {
        lifecycleOwner.lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
            while (remaining > 0) {
                delay(1_000)
                remaining--
            }
        }
    }
    fun choose(enabled: Boolean) {
        if (saving || remaining > 0) return
        saving = true
        scope.launch {
            try {
                onDecision(enabled)
            } catch (error: CancellationException) {
                throw error
            } catch (_: Exception) {
                saveFailed = true
            } finally {
                saving = false
            }
        }
    }

    AlertDialog(
        onDismissRequest = {},
        properties = DialogProperties(dismissOnBackPress = false, dismissOnClickOutside = false),
        icon = { Icon(Icons.Outlined.WarningAmber, contentDescription = null) },
        title = { Text(stringResource(R.string.recognition_dialog_title)) },
        text = {
            Column(
                modifier = Modifier.verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Text(stringResource(R.string.recognition_dialog_message))
                Text(
                    stringResource(R.string.recognition_dialog_risk),
                    color = MaterialTheme.colorScheme.error,
                    fontWeight = FontWeight.SemiBold,
                )
                Text(stringResource(R.string.recognition_dialog_disclaimer))
                Text(stringResource(R.string.recognition_settings_location))
                if (saveFailed) Text(
                    stringResource(R.string.recognition_save_failed),
                    color = MaterialTheme.colorScheme.error,
                )
            }
        },
        confirmButton = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                MaaButton(
                    onClick = { choose(true) },
                    enabled = remaining == 0 && !saving,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text(
                        if (remaining > 0) stringResource(R.string.recognition_enable_countdown, remaining)
                        else stringResource(R.string.recognition_enable_recommended),
                        fontWeight = FontWeight.Bold,
                    )
                }
                MaaOutlinedButton(
                    onClick = { choose(false) },
                    enabled = remaining == 0 && !saving,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text(
                        if (remaining > 0) stringResource(R.string.recognition_cancel_countdown, remaining)
                        else stringResource(R.string.recognition_cancel_native_android),
                    )
                }
            }
        },
    )
}
