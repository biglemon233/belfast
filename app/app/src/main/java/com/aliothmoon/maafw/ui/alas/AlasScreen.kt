package com.aliothmoon.maafw.ui.alas

import android.annotation.SuppressLint
import android.graphics.Bitmap
import android.os.Build
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import com.aliothmoon.maafw.ui.components.MaaButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.aliothmoon.maafw.BuildConfig
import com.aliothmoon.maafw.MaaDispatchers
import com.aliothmoon.maafw.R
import com.aliothmoon.maafw.proot.ProotHost
import com.aliothmoon.maafw.proot.ProotPhase
import com.aliothmoon.maafw.service.HostState
import com.aliothmoon.maafw.theme.MaaDesignTokens
import kotlinx.coroutines.withContext
import org.koin.compose.koinInject
import java.net.HttpURLConnection
import java.net.URL

/** ALAS WebUI：App 内置环境监听的本机回环地址 */
private const val ALAS_WEBUI_URL = "http://127.0.0.1:22267"

/** 向运行中的 ALAS（wrapper 22400 或 GUI 控制 22401）同步主题 */
private suspend fun syncAlasTheme(theme: String): Boolean = withContext(MaaDispatchers.IO) {
    for (port in listOf(ProotHost.WRAPPER_PORT, 22401)) {
        val success = runCatching {
            val url = URL("http://127.0.0.1:$port/theme?theme=$theme")
            val conn = url.openConnection() as HttpURLConnection
            try {
                conn.requestMethod = "POST"
                conn.connectTimeout = 1500
                conn.readTimeout = 3000
                conn.responseCode == 200
            } finally {
                conn.disconnect()
            }
        }.getOrDefault(false)
        if (success) return@withContext true
    }
    false
}

/**
 * 部分 WebView 的 `vh` 单位可能为 0：页面的 layout viewport
 * 错位成 0 高，所有 `100vh`（pywebio 脚手架 `#pywebio-scope-ROOT` 的高度）塌成 0，
 * 整页只画顶部一条标题带。`innerHeight` 正常，所以注入同值像素覆盖即可恢复。
 * 规则挂在 head 上对 JS 后续创建的 scope 元素同样生效；vh 正常的设备上本规则
 * 与 `100vh` 等值，无副作用。
 */
private const val SCOPE_HEIGHT_FIX_JS =
    """
    (() => {
      const h = Math.max(1, window.innerHeight);
      let s = document.getElementById('alas-scope-height-fix');
      if (!s) {
        s = document.createElement('style');
        s.id = 'alas-scope-height-fix';
        document.head.appendChild(s);
      }
      s.textContent = '#pywebio-scope-ROOT{height:' + h + 'px !important;min-height:' + h + 'px !important;}';
    })();
    """

/**
 * PyWebIO 把 loading 标记挂在外包装，而不是 Bootstrap spinner 上。去掉外框和动画，
 * 把真正的 spinner 统一到 24px；fill/secondary 静止，运行态保留旋转。
 * CSS 同时覆盖后来插入的节点，不改 ALAS 上游文件或任务状态。
 */
private const val IDLE_SPINNER_FIX_JS =
    """
    (() => {
      if (document.getElementById('alasaos-idle-spinner-fix')) return;
      const s = document.createElement('style');
      s.id = 'alasaos-idle-spinner-fix';
      s.textContent = `
        [style*="--loading-border"]:not(.spinner-border) {
          border: none !important;
          animation: none !important;
          width: 24px !important;
          height: 24px !important;
          display: inline-flex !important;
          align-items: center;
          justify-content: center;
          vertical-align: middle;
        }
        [style*="--loading-border"] .spinner-border,
        .spinner-border[style*="--loading-border"],
        .spinner-border.text-secondary {
          box-sizing: border-box !important;
          width: 24px !important;
          height: 24px !important;
          flex-shrink: 0;
          border: 3px solid currentColor !important;
          border-right-color: transparent !important;
          border-radius: 50% !important;
        }
        [style*="--loading-border-fill--"] .spinner-border,
        .spinner-border[style*="--loading-border-fill--"],
        .spinner-border.text-secondary {
          animation: none !important;
          border-right-color: currentColor !important;
        }
      `;
      document.head.appendChild(s);
    })();
    """

/**
 * ALAS tab：全屏 WebView 容器，承载 App 内置环境里的 ALAS WebUI
 *
 * [active] 标记当前是否为 pager 可见页：ALAS 页不在前台时（pager 仍预组合着它）
 * 不该抢返回键。WebUI 历史能后退就 goBack，否则把返回键让回原有导航逻辑
 *
 * 本页可见且特权连接就绪时自动补一次「开始」链路建虚拟屏（HostState 内幂等，
 * 断线重连后随 privilegedConnected 翻转会再触发）
 */
@SuppressLint("SetJavaScriptEnabled")
@Composable
fun AlasScreen(
    active: Boolean,
    darkTheme: Boolean = false,
    modifier: Modifier = Modifier,
    hostState: HostState = koinInject(),
    prootHost: ProotHost = koinInject(),
) {
    var loadFailed by remember { mutableStateOf(false) }
    var pageReady by remember { mutableStateOf(false) }
    var hasShownPage by remember { mutableStateOf(false) }
    var canGoBack by remember { mutableStateOf(false) }
    var webView by remember { mutableStateOf<WebView?>(null) }
    val hostSnapshot by hostState.snapshot.collectAsStateWithLifecycle()
    val prootState by prootHost.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val mobileAdaptation = remember { context.assets.open("alas-webview.js").bufferedReader().use { it.readText() } }
    var deleteRequest by remember { mutableStateOf<Pair<WebView, String>?>(null) }
    val fileTransfer = rememberAlasFileTransfer { view, name -> deleteRequest = view to name }
    val targetTheme = if (darkTheme) "dark" else "default"
    var lastAppliedTheme by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(targetTheme, prootState.phase) {
        if (prootState.phase == ProotPhase.RUNNING) {
            if (lastAppliedTheme != targetTheme) {
                val synced = syncAlasTheme(targetTheme)
                if (synced) {
                    val wasInitial = lastAppliedTheme == null
                    lastAppliedTheme = targetTheme
                    if (!wasInitial && hasShownPage) {
                        webView?.reload()
                    }
                }
            }
        }
    }

    deleteRequest?.let { request ->
        AlasConfigDeleteDialog(request.second, onDismiss = { deleteRequest = null }) {
            deleteRequest = null
            if (webView === request.first) {
                // A deleted instance must not remain the WebUI's saved selection.
                request.first.evaluateJavascript("localStorage.removeItem('aside');", null)
                request.first.reload()
            }
        }
    }

    LaunchedEffect(active, hostSnapshot.privilegedConnected) {
        if (active && hostSnapshot.privilegedConnected) {
            hostState.ensureEnvironmentStarted()
        }
    }

    // 内置环境转 RUNNING（首启/热更新/崩溃重拉完成）时自动重载，不用用户点重试
    LaunchedEffect(prootState.phase) {
        if (prootState.phase == ProotPhase.RUNNING) {
            loadFailed = false
            webView?.loadUrl(ALAS_WEBUI_URL)
        }
    }

    BackHandler(enabled = active && canGoBack) {
        webView?.let {
            it.goBack()
            canGoBack = it.canGoBack()
        }
    }

    Box(modifier = modifier.fillMaxSize().imePadding()) {
        AndroidView(
            factory = { ctx ->
                WebView(ctx).apply {
                    // debug 包开 WebView 调试口：本地排查页面渲染用
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.KITKAT && BuildConfig.DEBUG) {
                        WebView.setWebContentsDebuggingEnabled(true)
                    }
                    // pywebio 是 SPA，JS 与 localStorage 都要开
                    settings.javaScriptEnabled = true
                    settings.domStorageEnabled = true
                    webChromeClient = fileTransfer
                    webViewClient = object : WebViewClient() {
                        override fun shouldOverrideUrlLoading(
                            view: WebView,
                            request: WebResourceRequest,
                        ): Boolean = when (request.url?.scheme) {
                            // http/https 一律留在 WebView 内打开，不放给外部浏览器
                            "http", "https" -> false
                            // 其余协议（intent:/tel:/mailto:...）不交给外部处理
                            else -> true
                        }

                        override fun onPageStarted(view: WebView, url: String?, favicon: Bitmap?) {
                            loadFailed = false
                            // Internal navigation is not a new environment startup.
                            if (!hasShownPage) pageReady = false
                        }

                        override fun onPageFinished(view: WebView, url: String?) {
                            canGoBack = view.canGoBack()
                            // 主文档失败不算就绪（onReceivedError 已置位），开屏就不淡出
                            if (!loadFailed) {
                                pageReady = true
                                hasShownPage = true
                            }
                            // 见 SCOPE_HEIGHT_FIX_JS：vh=0 时补像素高度
                            view.evaluateJavascript(SCOPE_HEIGHT_FIX_JS, null)
                            // Fix marked wrapper frames and real spinners; only fill/idle stops rotating.
                            view.evaluateJavascript(IDLE_SPINNER_FIX_JS, null)
                            if (AlasConfigDownload.isAlasOrigin(url)) view.evaluateJavascript(mobileAdaptation, null)
                        }

                        override fun onReceivedError(
                            view: WebView,
                            request: WebResourceRequest,
                            error: WebResourceError,
                        ) {
                            // 只对主文档报错；子资源（图片/脚本）失败不算整页失败
                            if (request.isForMainFrame) {
                                canGoBack = view.canGoBack()
                                loadFailed = true
                                pageReady = false
                            }
                        }
                    }
                    webView = this
                    AlasWebViewHolder.register(this)
                    loadUrl(ALAS_WEBUI_URL)
                }
            },
            onRelease = {
                AlasWebViewHolder.unregister(it)
                if (webView === it) webView = null
                it.destroy()
            },
            modifier = Modifier.fillMaxSize().onSizeChanged {
                // Keep the existing viewport workaround in sync without reloading ALAS on rotation.
                webView?.post { webView?.evaluateJavascript(SCOPE_HEIGHT_FIX_JS, null) }
            },
        )

        AnimatedVisibility(
            visible = !pageReady,
            modifier = Modifier.fillMaxSize(),
            enter = EnterTransition.None,
            exit = fadeOut(),
        ) {
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .background(MaterialTheme.colorScheme.background)
                    .pointerInput(Unit) {
                        // 挡住穿透到 WebView 上的漏点，载入/错误时只留重试按钮可点
                        awaitPointerEventScope {
                            while (true) {
                                awaitPointerEvent().changes.forEach { it.consume() }
                            }
                        }
                    }
                    .padding(MaaDesignTokens.Spacing.lg),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center,
            ) {
                // 只有这两种算真失败：启动链自己挂了，或服务明明该活着页却进不来
                val failureText = when {
                    prootState.phase == ProotPhase.FAILED ->
                        stringResource(R.string.proot_phase_failed, prootState.detail)

                    loadFailed && (prootState.phase == ProotPhase.IDLE ||
                        prootState.phase == ProotPhase.RUNNING) ->
                        stringResource(R.string.alas_webui_not_running)

                    else -> null
                }
                StartupChecklist(prootState)
                Spacer(Modifier.height(MaaDesignTokens.Spacing.md))
                if (failureText == null) {
                    // 载入开屏：首次 loadUrl 撞上服务未起是必然事件，不给用户看错误脸
                    Text(
                        text = when (prootState.phase) {
                            ProotPhase.PREPARING -> stringResource(R.string.proot_phase_preparing)
                            ProotPhase.UPDATING -> stringResource(R.string.proot_phase_updating)
                            ProotPhase.STARTING -> stringResource(R.string.proot_phase_starting)
                            else -> stringResource(R.string.alas_webui_loading)
                        },
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                } else {
                    Text(
                        text = failureText,
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Spacer(Modifier.height(MaaDesignTokens.Spacing.lg))
                    MaaButton(
                        onClick = {
                            loadFailed = false
                            if (prootState.phase == ProotPhase.FAILED || prootState.phase == ProotPhase.IDLE) {
                                prootHost.ensureStarted()
                            }
                            webView?.loadUrl(ALAS_WEBUI_URL)
                        },
                    ) {
                        Text(stringResource(R.string.alas_webui_retry))
                    }
                }
            }
        }
    }
}
