package com.aliothmoon.maafw.ui.alas

import android.widget.Toast
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import com.aliothmoon.maafw.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

@Composable
internal fun AlasConfigDeleteDialog(name: String, onDismiss: () -> Unit, onDeleted: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var busy by remember(name) { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(stringResource(R.string.alas_config_delete_title)) },
        text = { Text(stringResource(R.string.alas_config_delete_confirm, name)) },
        dismissButton = {
            TextButton(enabled = !busy, onClick = onDismiss) { Text(stringResource(android.R.string.cancel)) }
        },
        confirmButton = {
            TextButton(enabled = !busy, onClick = {
                busy = true
                scope.launch {
                    val error = withContext(Dispatchers.IO) {
                        runCatching {
                            val encoded = URLEncoder.encode(name, "UTF-8")
                            val connection = URL("http://127.0.0.1:22400/configs/delete?config=$encoded").openConnection() as HttpURLConnection
                            try {
                                connection.requestMethod = "POST"
                                connection.connectTimeout = 3000
                                connection.readTimeout = 5000
                                if (connection.responseCode == 200) null
                                else connection.errorStream?.bufferedReader()?.use { JSONObject(it.readText()).optString("error") } ?: "failed"
                            } finally {
                                connection.disconnect()
                            }
                        }.getOrDefault("failed")
                    }
                    busy = false
                    val message = when (error) {
                        null -> R.string.alas_config_deleted
                        "configuration_running" -> R.string.alas_config_delete_running
                        "last_configuration" -> R.string.alas_config_delete_last
                        else -> R.string.alas_config_delete_failed
                    }
                    Toast.makeText(context, message, Toast.LENGTH_LONG).show()
                    if (error == null) onDeleted()
                }
            }) { Text(stringResource(R.string.alas_config_delete_title)) }
        },
    )
}
