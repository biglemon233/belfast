package com.aliothmoon.maafw.privileged

import android.content.ComponentName
import android.content.ServiceConnection
import android.os.IBinder
import com.aliothmoon.maafw.BuildConfig
import com.aliothmoon.maafw.domain.RemoteBackend
import com.aliothmoon.maafw.remote.RemoteServiceImpl
import rikka.shizuku.Shizuku
import timber.log.Timber
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.util.UUID
import java.util.concurrent.atomic.AtomicInteger

object ShizukuRemoteServiceConnector : RemoteServiceConnectorBackend {

    override val backend: RemoteBackend = RemoteBackend.SHIZUKU

    private val serviceTag = UUID.randomUUID().toString()
    private val serviceVersion = AtomicInteger(100)
    private val scope = CoroutineScope(Dispatchers.Main.immediate + SupervisorJob())

    @Volatile
    private var activeBinding: ActiveBinding? = null

    override fun connect(callbacks: RemoteServiceConnectorBackend.Callbacks) {
        val args = createServiceArgs()
        val connection = object : ServiceConnection {
            override fun onServiceConnected(name: ComponentName?, binder: IBinder?) {
                ServiceBootLogger.event("SHIZUKU_ON_CONNECTED", "name=$name binderNull=${binder == null}")
                val binding = activeBinding
                if (binding?.connection !== this || binding.usingFallback) {
                    Timber.w("Ignoring stale Shizuku connection: %s", name)
                    if (binding?.usingFallback == true) {
                        runCatching { Shizuku.unbindUserService(binding.args, this, true) }
                            .onFailure { Timber.w(it, "Late UserService cleanup failed") }
                    }
                    return
                }
                if (binder == null) {
                    callbacks.onError(
                        backend,
                        IllegalStateException("RemoteService binder is null")
                    )
                    return
                }
                Timber.i("RemoteService connected by Shizuku: %s", name)
                binding.connected = true
                binding.fallbackJob?.cancel()
                callbacks.onConnected(backend, binder)
            }

            override fun onServiceDisconnected(name: ComponentName?) {
                ServiceBootLogger.event("SHIZUKU_ON_DISCONNECTED", "name=$name")
                if (activeBinding?.connection !== this || activeBinding?.usingFallback == true) {
                    return
                }
                Timber.i("RemoteService disconnected by Shizuku: %s", name)
                callbacks.onDisconnected(backend)
            }
        }

        val binding = ActiveBinding(args, connection)
        activeBinding = binding

        binding.fallbackJob = scope.launch {
            delay(5_000)
            startFallback(binding, callbacks)
        }

        runCatching {
            ServiceBootLogger.event("SHIZUKU_BIND_CALL", "version=${serviceVersion.get()} tag=$serviceTag")
            Timber.i("Binding remote service via Shizuku: %s", args)
            Shizuku.bindUserService(args, connection)
        }.onFailure { throwable ->
            ServiceBootLogger.event("SHIZUKU_BIND_THROW", "${throwable.javaClass.simpleName}: ${throwable.message}")
            Timber.e(throwable, "bindUserService failed")
            scope.launch { startFallback(binding, callbacks) }
        }
    }

    private fun startFallback(binding: ActiveBinding, callbacks: RemoteServiceConnectorBackend.Callbacks) {
        if (activeBinding !== binding || binding.connected || binding.usingFallback) return
        binding.usingFallback = true
        ServiceBootLogger.event("SHIZUKU_SHELL_FALLBACK", "UserService did not return a binder")
        runCatching { Shizuku.unbindUserService(binding.args, binding.connection, true) }
            .onFailure { Timber.w(it, "Unbind failed UserService") }
        ShizukuShellBootstrap.connect(object : RemoteServiceConnectorBackend.Callbacks {
            override fun onConnected(backend: RemoteBackend, binder: IBinder) {
                if (activeBinding === binding) callbacks.onConnected(backend, binder)
            }
            override fun onDisconnected(backend: RemoteBackend) {
                if (activeBinding === binding) callbacks.onDisconnected(backend)
            }
            override fun onError(backend: RemoteBackend, throwable: Throwable) {
                if (activeBinding === binding) callbacks.onError(backend, throwable)
            }
        })
    }

    override fun disconnect(currentBinder: IBinder?) {
        val binding = activeBinding ?: return
        activeBinding = null
        binding.fallbackJob?.cancel()
        if (binding.usingFallback) {
            ShizukuShellBootstrap.disconnect(currentBinder)
            return
        }
        runCatching {
            Shizuku.unbindUserService(binding.args, binding.connection, true)
        }.onFailure {
            Timber.w(it, "unbindUserService failed")
        }
    }

    private fun createServiceArgs(): Shizuku.UserServiceArgs {
        return Shizuku.UserServiceArgs(
            ComponentName(BuildConfig.APPLICATION_ID, RemoteServiceImpl::class.java.name)
        ).apply {
            processNameSuffix("service")
            daemon(false)
            tag(serviceTag)
            version(serviceVersion.incrementAndGet())
            debuggable(BuildConfig.DEBUG)
        }
    }

    private data class ActiveBinding(
        val args: Shizuku.UserServiceArgs,
        val connection: ServiceConnection,
        @Volatile var connected: Boolean = false,
        @Volatile var usingFallback: Boolean = false,
        var fallbackJob: Job? = null,
    )
}
