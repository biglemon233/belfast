package com.aliothmoon.maafw.service

import android.content.Context
import android.os.SystemClock
import android.view.Surface
import com.aliothmoon.maafw.BuildConfig
import com.aliothmoon.maafw.MaaDispatchers
import com.aliothmoon.maafw.constant.DefaultDisplayConfig
import com.aliothmoon.maafw.privileged.PrivilegedServicePort
import com.aliothmoon.maafw.privileged.PrivilegedServiceState
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import org.json.JSONObject
import timber.log.Timber
import java.io.BufferedInputStream
import java.io.ByteArrayOutputStream
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

/**
 * 外壳真实状态的唯一来源：特权进程连接态 + 桥可达性 + 虚拟屏 displayId
 *
 * 桥活在特权进程里（BridgeServer 随 RemoteServiceImpl 自起），app 侧够不到它的
 * isRunning()，可达性只能自己周期 ping——协议与 m0 桥同款（行分隔 JSON），
 * 与 ALAS 客户端打的是同一个端口
 *
 * displayId 只是「最近一次 startVirtualDisplay 的返回值」：特权进程一断，
 * 屏与桥随之作废，快照整份清零，等下次连接/探测重建
 */
class HostState(
    private val context: Context,
    private val servicePort: PrivilegedServicePort,
    private val scope: CoroutineScope,
    private val settings: com.aliothmoon.maafw.settings.AppSettingsGateway,
) {

    private val _snapshot = MutableStateFlow(HostSnapshot())
    val snapshot: StateFlow<HostSnapshot> = _snapshot.asStateFlow()

    /** 周期与按需探测共用一把锁，防并发探测挤在同一端口上 */
    private val probeMutex = Mutex()
    private val envMutex = Mutex()
    private val previewClickMutex = Mutex()
    private val pingSeq = AtomicInteger(0)

    private val started = AtomicBoolean(false)

    /** Both periodic and explicit probes hold probeMutex when touching this throttle. */
    private val probeFailures = PollFailureThrottle()

    fun start() {
        if (!started.compareAndSet(false, true)) return
        scope.launch {
            servicePort.serviceState.collect { state ->
                _snapshot.update {
                    if (state == PrivilegedServiceState.Connected) {
                        it.copy(privilegedConnected = true)
                    } else {
                        HostSnapshot()
                    }
                }
            }
        }
        scope.launch(MaaDispatchers.Default) {
            combine(
                BackgroundPolling.foreground,
                BackgroundPolling.taskActive,
                snapshot.map { it.vdDisplayId != DefaultDisplayConfig.DISPLAY_NONE }
                    .distinctUntilChanged(),
            ) { foreground, taskActive, displayActive ->
                // Keep the bridge responsive while a VD exists, even between tasks.
                foreground to (taskActive || displayActive)
            }.distinctUntilChanged().collectLatest { (foreground, active) ->
                // Visibility is part of the key even if both intervals are 4s: resume probes now.
                while (true) {
                    probeBridgeNow()
                    delay(BackgroundPolling.intervalMs(foreground, active))
                }
            }
        }
    }

    // Page activation and overlay actions also call this from Main. Keep the IO
    // boundary here so those probes cannot turn NetworkOnMainThread into an outage.
    suspend fun probeBridgeNow(): Boolean = withContext(MaaDispatchers.IO) {
        probeMutex.withLock {
            currentCoroutineContext().ensureActive()
            val result = runCatching { pingBridge() }
            // Socket IO is blocking; a cancelled old poll must not publish its late result.
            currentCoroutineContext().ensureActive()
            val reachable = result.getOrDefault(false)
            if (reachable) {
                val recovered = probeFailures.recovered()
                if (recovered > 0) Timber.d("bridge probe recovered after %d failures", recovered)
                _snapshot.update { it.copy(bridgeReachable = true) }
            } else {
                if (probeFailures.failed(SystemClock.elapsedRealtime())) {
                    Timber.d("bridge probe failed (streak=%d): %s", probeFailures.failures,
                        result.exceptionOrNull()?.toString() ?: "no pong")
                }
                // 挂机满负荷（ALAS 每帧 2.7MB 打 screencap）时单次 ping 超时是常态，
                // 连续 BRIDGE_FAIL_THRESHOLD 次失败才判不可达，与 FGS「桥抖动不撤保活」对齐
                if (probeFailures.failures >= BRIDGE_FAIL_THRESHOLD) {
                    _snapshot.update { it.copy(bridgeReachable = false) }
                }
            }
            reachable
        }
    }

    /**
     * 「开始」链路：确保特权连接就绪 → setup() → startVirtualDisplay()
     * 桥随特权进程自起，无需显式操作
     *
     * 幂等：屏已存在直接成功；断线重连后（快照已清零）可再次触发
     */
    suspend fun ensureEnvironmentStarted(): Boolean {
        envMutex.withLock {
            if (_snapshot.value.vdDisplayId != DefaultDisplayConfig.DISPLAY_NONE) return@withLock
            val service = servicePort.serviceOrNull() ?: run {
                // 已断就拉一次：自动路径进入前已判 Connected，这里基本是空转；
                // 未授权时 bind 会落成 Error，等完照旧拿不到服务面
                servicePort.bind()
                runCatching {
                    withTimeout(CONNECT_WAIT_MS) {
                        servicePort.serviceState.first { it != PrivilegedServiceState.Connecting }
                    }
                }
                servicePort.serviceOrNull() ?: run {
                    Timber.w("ensureEnvironmentStarted: privileged not connected")
                    return@withLock
                }
            }
            runCatching { service.setup(null, null, BuildConfig.DEBUG) }
                .onFailure { Timber.w(it, "setup failed") }
            // 只有后台（虚拟屏）模式；前台/主屏 PRIMARY 已移除
            val displayMode = settings.runMode.value.displayMode
            runCatching { service.setVirtualDisplayMode(displayMode) }
                .onFailure { Timber.w(it, "setVirtualDisplayMode(%s) failed", displayMode) }
            val displayId = runCatching { service.startVirtualDisplay() }
                .getOrElse {
                    Timber.e(it, "startVirtualDisplay failed")
                    return@withLock
                }
            if (displayId == DefaultDisplayConfig.DISPLAY_NONE) {
                Timber.w("startVirtualDisplay returned DISPLAY_NONE")
                return@withLock
            }
            Timber.i("Environment started, displayId=%s", displayId)
            _snapshot.update { it.copy(vdDisplayId = displayId) }
            RunForegroundService.start(context)
        }
        // 建完屏（或本来就有屏）顺手刷一次桥态，UI 不必干等下个探测周期
        probeBridgeNow()
        return _snapshot.value.vdDisplayId != DefaultDisplayConfig.DISPLAY_NONE
    }

    /** 「停止」链路：只停虚拟屏；特权进程与桥留着，下次开始不用重连 */
    suspend fun stopEnvironment() {
        envMutex.withLock {
            servicePort.serviceOrNull()?.let { service ->
                runCatching { service.stopVirtualDisplay() }
                    .onFailure { Timber.w(it, "stopVirtualDisplay failed") }
            }
            _snapshot.update { it.copy(vdDisplayId = DefaultDisplayConfig.DISPLAY_NONE) }
        }
    }

    /**
     * 预览面挂载/摘除：挂机页 SurfaceView 的 Surface 交给特权进程渲染虚拟屏画面
     * （native bridge_preview 通道，零拷贝）。特权断线时静默失败——
     * 页面侧显示占位，连接恢复后随页面 active 翻转会重挂
     */
    fun attachPreviewSurface(surface: Surface) {
        runCatching { servicePort.serviceOrNull()?.setMonitorSurface(surface) }
            .onFailure { Timber.w(it, "attachPreviewSurface failed") }
    }

    fun detachPreviewSurface() {
        runCatching { servicePort.serviceOrNull()?.setMonitorSurface(null) }
            .onFailure { Timber.w(it, "detachPreviewSurface failed") }
    }

    /**
     * 全屏预览上的手动操作：坐标由 UI 换算到虚拟屏坐标系后传入，
     * 直通 AIDL 同名方法（oneway，内部带虚拟屏 displayId 注入，见 RemoteServiceImpl）。
     * 高频（一次滑动几十条），失败静默——特权断线时快照清零，注入也随之失去目标
     */
    fun touchClick(x: Int, y: Int) {
        // The host gesture is complete before the bridge focuses the game and
        // injects its down/up pair. Serialize taps without blocking the UI.
        scope.launch(MaaDispatchers.IO) {
            previewClickMutex.withLock {
                runCatching {
                    Socket().use { socket ->
                        socket.connect(InetSocketAddress(BRIDGE_HOST, BRIDGE_PORT), BRIDGE_CONNECT_TIMEOUT_MS)
                        socket.soTimeout = 6_000
                        val request = JSONObject()
                            .put("id", pingSeq.incrementAndGet())
                            .put("method", "click")
                            .put("x", x)
                            .put("y", y)
                        socket.getOutputStream().write((request.toString() + "\n").toByteArray(Charsets.UTF_8))
                        val reply = JSONObject(readLine(BufferedInputStream(socket.getInputStream())))
                        check(reply.optBoolean("ok", false)) { "Preview click injection failed" }
                    }
                }.onFailure { Timber.w(it, "touchClick failed") }
            }
        }
    }

    fun touchDown(x: Int, y: Int) {
        runCatching { servicePort.serviceOrNull()?.touchDown(x, y) }
            .onFailure { Timber.w(it, "touchDown failed") }
    }

    fun touchMove(x: Int, y: Int) {
        runCatching { servicePort.serviceOrNull()?.touchMove(x, y) }
            .onFailure { Timber.w(it, "touchMove failed") }
    }

    fun touchUp(x: Int, y: Int) {
        runCatching { servicePort.serviceOrNull()?.touchUp(x, y) }
            .onFailure { Timber.w(it, "touchUp failed") }
    }

    /** m0 桥协议最小客户端：一行请求一行响应，判 "pong":true */
    private fun pingBridge(): Boolean {
        Socket().use { socket ->
            socket.connect(
                InetSocketAddress(InetAddress.getByName(BRIDGE_HOST), BRIDGE_PORT),
                BRIDGE_CONNECT_TIMEOUT_MS,
            )
            socket.soTimeout = BRIDGE_READ_TIMEOUT_MS
            val request = """{"id":${pingSeq.incrementAndGet()},"method":"ping"}"""
            socket.getOutputStream().write((request + "\n").toByteArray(Charsets.UTF_8))
            val line = readLine(BufferedInputStream(socket.getInputStream()))
            return JSONObject(line).optBoolean("pong", false)
        }
    }

    private fun readLine(input: BufferedInputStream): String {
        val line = ByteArrayOutputStream()
        while (true) {
            val b = input.read()
            check(b >= 0) { "bridge closed connection" }
            if (b == '\n'.code) break
            line.write(b)
            check(line.size() <= MAX_REPLY_LINE) { "reply line too long" }
        }
        return line.toString(Charsets.UTF_8.name())
    }

    private companion object {
        const val BRIDGE_HOST = "127.0.0.1"
        const val BRIDGE_PORT = 22300
        const val BRIDGE_FAIL_THRESHOLD = 2
        const val BRIDGE_CONNECT_TIMEOUT_MS = 1_500
        const val BRIDGE_READ_TIMEOUT_MS = 2_000
        const val CONNECT_WAIT_MS = 12_000L
        const val MAX_REPLY_LINE = 4 * 1024
    }
}

data class HostSnapshot(
    val privilegedConnected: Boolean = false,
    val bridgeReachable: Boolean = false,
    val vdDisplayId: Int = DefaultDisplayConfig.DISPLAY_NONE,
) {
    /** 环境整体活着：屏在且桥通；悬浮球与 FGS 的「活着」判据 */
    val environmentUp: Boolean
        get() = bridgeReachable && vdDisplayId != DefaultDisplayConfig.DISPLAY_NONE
}
