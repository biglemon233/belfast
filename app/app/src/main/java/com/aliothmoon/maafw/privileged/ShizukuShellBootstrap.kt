package com.aliothmoon.maafw.privileged

import android.content.Context
import android.os.IBinder
import android.os.Process
import com.aliothmoon.maafw.MaaDispatchers
import com.aliothmoon.maafw.RemoteService
import com.aliothmoon.maafw.constant.AppPaths
import com.aliothmoon.maafw.domain.RemoteBackend
import com.aliothmoon.maafw.remote.RemoteServiceImpl
import com.aliothmoon.maafw.root.RootServiceBootstrapRegistry
import com.aliothmoon.maafw.root.RootServiceStarter
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout
import moe.shizuku.server.IRemoteProcess
import moe.shizuku.server.IShizukuService
import rikka.shizuku.Shizuku
import timber.log.Timber
import java.io.File
import java.util.UUID

/** OEM fallback: Shizuku's UserService can fail before our constructor runs.
 * Reuse the AOS packageContext bootstrap, launched by the authorized Shizuku
 * server. This path never invokes su or switches the configured backend.
 */
internal object ShizukuShellBootstrap {
    private val scope = CoroutineScope(MaaDispatchers.IO + SupervisorJob())
    private lateinit var context: Context

    @Volatile private var active: Launch? = null

    fun initialize(context: Context) {
        this.context = context.applicationContext
    }

    fun connect(callbacks: RemoteServiceConnectorBackend.Callbacks) {
        disconnect(null)
        val launch = Launch(UUID.randomUUID().toString())
        val ready = RootServiceBootstrapRegistry.register(launch.token)
        active = launch
        launch.deathListener = Shizuku.OnBinderDeadListener {
            if (active === launch) {
                disconnect(launch.binder)
                callbacks.onDisconnected(RemoteBackend.SHIZUKU)
            }
        }
        Shizuku.addBinderDeadListener(launch.deathListener)
        launch.job = scope.launch(start = CoroutineStart.LAZY) {
            try {
                val server = IShizukuService.Stub.asInterface(Shizuku.getBinder())
                    ?: error("Shizuku binder unavailable")
                val uid = server.uid
                check(uid == Process.SHELL_UID || uid == 0) { "Unexpected Shizuku uid=$uid" }
                val log = File(AppPaths.DEBUG_DIR, "shizuku_shell_bootstrap.log")
                log.parentFile?.mkdirs()
                log.writeText("")
                val command = ShizukuBootstrapCommand.build(
                    context.applicationInfo.sourceDir, context.packageName, Process.myUid(),
                    launch.token, RootServiceStarter::class.java.name,
                    RemoteServiceImpl::class.java.name, log.absolutePath,
                )
                val process = server.newProcess(arrayOf("/system/bin/sh", "-c", command), null, null)
                launch.process = process
                if (active !== launch) {
                    runCatching { process.destroy() }
                    return@launch
                }
                ServiceBootLogger.event("SHIZUKU_SHELL_STARTED", "uid=$uid")
                val binder = withTimeout(10_000L) { ready.await() }
                launch.binder = binder
                if (active !== launch) {
                    destroy(binder)
                    return@launch
                }
                ServiceBootLogger.event("SHIZUKU_SHELL_CONNECTED", "uid=$uid")
                callbacks.onConnected(RemoteBackend.SHIZUKU, binder)
            } catch (error: Throwable) {
                if (active === launch) {
                    disconnect(launch.binder)
                    if (error !is CancellationException || error is TimeoutCancellationException) {
                        ServiceBootLogger.event("SHIZUKU_SHELL_FAILED", error.javaClass.simpleName)
                        callbacks.onError(RemoteBackend.SHIZUKU, error)
                    }
                }
            } finally {
                RootServiceBootstrapRegistry.unregister(launch.token)
            }
        }
        launch.job?.start()
    }

    fun disconnect(binder: IBinder?) {
        val launch = active
        active = null
        if (launch != null) {
            Shizuku.removeBinderDeadListener(launch.deathListener)
            RootServiceBootstrapRegistry.unregister(launch.token)
            launch.job?.cancel()
            destroy(binder ?: launch.binder)
            runCatching { launch.process?.destroy() }
                .onFailure { Timber.w(it, "Shizuku bootstrap process cleanup failed") }
        }
    }

    private fun destroy(binder: IBinder?) {
        runCatching { binder?.let { RemoteService.Stub.asInterface(it).destroy() } }
    }

    private class Launch(val token: String) {
        lateinit var deathListener: Shizuku.OnBinderDeadListener
        @Volatile var job: Job? = null
        @Volatile var process: IRemoteProcess? = null
        @Volatile var binder: IBinder? = null
    }
}

internal object ShizukuBootstrapCommand {
    fun build(apk: String, pkg: String, uid: Int, token: String, starter: String,
              service: String, log: String): String =
        "CLASSPATH=${quote(apk)} exec /system/bin/app_process /system/bin " +
            "${quote("--nice-name=$pkg:shizuku_service")} ${quote(starter)} " +
            "${quote("--token=$token")} ${quote("--package=$pkg")} " +
            "${quote("--class=$service")} --uid=$uid >${quote(log)} 2>&1"

    private fun quote(value: String) = "'${value.replace("'", "'\"'\"'")}'"
}
