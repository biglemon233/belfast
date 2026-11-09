package com.aliothmoon.maafw.ui.alas

import android.net.Uri
import android.webkit.JsPromptResult
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.platform.LocalContext
import com.aliothmoon.maafw.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private class PendingFiles {
    var chooser: ValueCallback<Array<Uri>>? = null
    var download: AlasConfigDownload? = null
}

@Composable
internal fun rememberAlasFileTransfer(onDelete: (WebView, String) -> Unit): WebChromeClient {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val pending = remember { PendingFiles() }
    val deleteConfig = rememberUpdatedState(onDelete)
    fun message(resource: Int) = Toast.makeText(context, resource, Toast.LENGTH_LONG).show()

    val chooseFile = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        val callback = pending.chooser
        pending.chooser = null
        callback?.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(result.resultCode, result.data))
    }
    val saveFile = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/json")) { uri ->
        val download = pending.download
        pending.download = null
        if (uri != null && download != null) scope.launch {
            val saved = withContext(Dispatchers.IO) {
                runCatching {
                    requireNotNull(context.contentResolver.openOutputStream(uri, "wt")).use { it.write(download.bytes) }
                }.isSuccess
            }
            message(if (saved) R.string.alas_config_export_saved else R.string.alas_config_export_failed)
        }
    }
    DisposableEffect(pending) {
        onDispose {
            pending.chooser?.onReceiveValue(null)
            pending.chooser = null
            pending.download = null
        }
    }
    return remember {
        object : WebChromeClient() {
            override fun onShowFileChooser(view: WebView, callback: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean {
                if (!AlasConfigDownload.isAlasOrigin(view.url)) return false
                pending.chooser?.onReceiveValue(null)
                pending.chooser = callback
                try {
                    // Android cannot resolve the HTML extension filter ".json" as a MIME type.
                    val intent = params.createIntent().apply { type = "application/json" }
                    chooseFile.launch(intent)
                } catch (_: android.content.ActivityNotFoundException) {
                    pending.chooser = null
                    callback.onReceiveValue(null)
                    message(R.string.alas_config_picker_failed)
                }
                return true
            }

            override fun onJsPrompt(view: WebView, url: String?, text: String?, defaultValue: String?, result: JsPromptResult): Boolean {
                if (text?.startsWith("alasaos-delete:") == true) {
                    result.cancel()
                    if (AlasConfigDownload.isAlasOrigin(url) && AlasConfigDownload.isAlasOrigin(view.url)) {
                        val name = text.removePrefix("alasaos-delete:")
                        if (name.length in 1..128) deleteConfig.value(view, name)
                    }
                    return true
                }
                if (text?.startsWith(AlasConfigDownload.PROMPT_PREFIX) != true) return false
                result.cancel()
                if (!AlasConfigDownload.isAlasOrigin(url) || !AlasConfigDownload.isAlasOrigin(view.url)) return true
                if (pending.download != null) return true
                val download = runCatching {
                    AlasConfigDownload.parse(text.removePrefix(AlasConfigDownload.PROMPT_PREFIX), defaultValue.orEmpty())
                }.getOrNull()
                if (download == null) {
                    message(R.string.alas_config_export_failed)
                    return true
                }
                pending.download = download
                try {
                    saveFile.launch(download.name)
                } catch (_: android.content.ActivityNotFoundException) {
                    pending.download = null
                    message(R.string.alas_config_picker_failed)
                }
                return true
            }
        }
    }
}
