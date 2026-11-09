package com.azurpilot.adb

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Handler
import android.os.Looper
import timber.log.Timber
import java.net.NetworkInterface

/**
 * 发现本机无线调试端口，忽略其他设备；发现状态和回调统一归主线程所有。
 *
 * 采用 shizuku-m 的本机地址筛选方式，串行解析服务以兼容旧版 NsdManager。
 * 启停请求可重叠；停止后的迟到解析不能重新公布端口。
 *
 * Discovers local wireless-debugging ports while ignoring other devices. The main thread owns
 * all discovery state and callbacks. Uses shizuku-m's local-address filtering and serial
 * resolution for older NsdManager implementations. Start/stop requests may overlap; late
 * resolutions after stopping cannot republish a port.
 */
class AdbMdns(context: Context, private val serviceType: String, private val observer: (Int) -> Unit) {
    private val manager = context.getSystemService(NsdManager::class.java)
    private val handler = Handler(Looper.getMainLooper())
    private var wanted = false
    private var started = false
    private var stopping = false
    private var listener: NsdManager.DiscoveryListener? = null
    private val queue = ArrayDeque<Pair<NsdManager.DiscoveryListener, NsdServiceInfo>>()
    private var resolving = false
    private var resolvedName: String? = null

    /**
     * 请求开始发现；重复请求合并，停机中的新请求会在停止回调后执行。
     *
     * Requests discovery; deduplicates starts and waits for an in-flight stop.
     */
    fun start() = onMain { wanted = true; startIfNeeded() }

    /**
     * 请求停止并立即清除端口；晚到的回调只能做资源清理。
     *
     * Requests a stop and clears the port immediately; late callbacks only clean up.
     */
    fun stop() = onMain {
        wanted = false
        queue.clear()
        resolvedName = null
        observer(-1)
        stopIfNeeded()
    }

    private fun startIfNeeded() {
        if (!wanted || listener != null) return
        val current = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(type: String) = onMain {
                if (listener === this) { started = true; if (!wanted) stopIfNeeded() }
            }
            override fun onStartDiscoveryFailed(type: String, error: Int) = onMain {
                if (listener === this) {
                    Timber.w("ADB discovery failed: %d", error)
                    listener = null
                    started = false
                    stopping = false
                    observer(-1)
                }
            }
            override fun onDiscoveryStopped(type: String) = finishStop(this)
            override fun onStopDiscoveryFailed(type: String, error: Int) = finishStop(this)
            override fun onServiceFound(info: NsdServiceInfo) = onMain {
                if (wanted && listener === this && !stopping) {
                    queue.addLast(this to info)
                    resolveNext()
                }
            }
            override fun onServiceLost(info: NsdServiceInfo) = onMain {
                if (listener === this && info.serviceName == resolvedName) {
                    resolvedName = null
                    observer(-1)
                }
            }
        }
        listener = current
        try {
            manager.discoverServices(serviceType, NsdManager.PROTOCOL_DNS_SD, current)
        } catch (error: Exception) {
            listener = null
            Timber.w(error, "ADB discovery unavailable")
            observer(-1)
        }
    }

    private fun stopIfNeeded() {
        val current = listener ?: return
        if (!started || stopping) return
        stopping = true
        try {
            manager.stopServiceDiscovery(current)
        } catch (error: Exception) {
            Timber.w(error, "ADB discovery stop failed")
            finishStop(current)
        }
    }

    private fun finishStop(current: NsdManager.DiscoveryListener) = onMain {
        if (listener === current) {
            listener = null
            started = false
            stopping = false
            startIfNeeded()
        }
    }

    @Suppress("DEPRECATION")
    private fun resolveNext() {
        if (resolving || !wanted || stopping) return
        while (queue.isNotEmpty()) {
            val (owner, info) = queue.removeFirst()
            if (owner !== listener) continue
            resolving = true
            val callback = object : NsdManager.ResolveListener {
                override fun onResolveFailed(info: NsdServiceInfo, error: Int) = onMain {
                    resolving = false
                    resolveNext()
                }
                override fun onServiceResolved(info: NsdServiceInfo) = onMain {
                    resolving = false
                    if (wanted && !stopping && owner === listener && isLocal(info)) {
                        resolvedName = info.serviceName
                        observer(info.port)
                    }
                    resolveNext()
                }
            }
            try {
                manager.resolveService(info, callback)
                return
            } catch (error: Exception) {
                resolving = false
                Timber.d(error, "ADB service resolution failed")
            }
        }
    }

    @Suppress("DEPRECATION")
    private fun isLocal(info: NsdServiceInfo): Boolean = runCatching {
        val host = info.host ?: return false
        info.port in 1..65535 && (host.isLoopbackAddress || NetworkInterface.getNetworkInterfaces()
            .asSequence().any { network -> network.inetAddresses.asSequence().any { it == host } })
    }.getOrDefault(false)

    private fun onMain(block: () -> Unit) {
        if (Looper.myLooper() == handler.looper) block() else handler.post { block() }
    }

    companion object {
        /**
         * 系统连接端口的 mDNS 类型。
         *
         * mDNS type for the system connection port.
         */
        const val TLS_CONNECT = "_adb-tls-connect._tcp"
        /**
         * 系统配对端口的 mDNS 类型。
         *
         * mDNS type for the system pairing port.
         */
        const val TLS_PAIRING = "_adb-tls-pairing._tcp"
    }
}
