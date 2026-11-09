package com.azurpilot.ghio.privileged

import com.azurpilot.ghio.domain.RemoteBackend
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * 在真实设备上验证 Shizuku 与 root 特权服务能建立连接并加载 native bridge。
 *
 * 该测试无法在纯 JVM 或没有授权后端的模拟器上成立：未安装/未授权 Shizuku 且无 root 时使用
 * JUnit assume 跳过，不视为失败。测试在 instrumentation 线程运行，等待 Shizuku 异步 Binder
 * 抵达后再重绑服务。
 *
 * Verifies on a real device that Shizuku and root privileged services can connect and load the
 * native bridge.
 *
 * This cannot pass on a plain JVM or an emulator without an authorized backend: when Shizuku is
 * unavailable or unauthorized and root is absent, JUnit assumptions skip it rather than fail it.
 * The test runs on an instrumentation thread, waits for the asynchronous Shizuku Binder, then
 * rebinds the service.
 */
@RunWith(AndroidJUnit4::class)
class RemoteServiceConnectionTest {

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    /**
     * 初始化指定 [backend]、等待其可用并返回已连接服务的版本信息。
     *
     * Shizuku provider 异步交付 Binder，故最多轮询 10 秒；成功后断言 bridge 标记与 pid，避免
     * 仅验证了 Binder 连接而漏掉 native 库加载失败。
     *
     * Initializes [backend], waits until available, and returns the connected service version.
     *
     * The Shizuku provider delivers its Binder asynchronously, so this polls for at most 10 seconds.
     * On success it asserts the bridge marker and pid, avoiding a Binder-only test that misses a
     * native-library loading failure.
     */
    private fun connectOrSkip(backend: RemoteBackend) = runBlocking {
        RemoteServiceManager.initialize(context) { backend }
        // Shizuku 的 Binder 由 provider 异步送达，初始化后不能假设立即可用。
        var available = false
        repeat(20) {
            if (RemoteAccessCoordinator.refresh().isAvailable(backend)) {
                available = true
                return@repeat
            }
            Thread.sleep(500)
        }
        assumeTrue("$backend 不可用", available)
        assumeTrue("$backend 未授权", RemoteAccessCoordinator.request(backend))

        RemoteServiceManager.unbind()
        val service = RemoteServiceManager.getInstance(timeoutMs = 30_000)
        val version = service.version()
        val pid = service.pid()
        // 版本字符串包含 bridge ping，证明特权进程实际加载了 libbridge.so。
        assertTrue("version 应包含 bridge 信息: $version", version.contains("bridge="))
        assertTrue("特权进程 pid 应有效: $pid", pid > 0)
        assertTrue("bridge 未加载: $version", !version.contains("not loaded"))
        version
    }

    /** 验证已授权 Shizuku 后端的端到端连接。 / Verifies end-to-end connection through an authorized Shizuku backend. */
    @Test
    fun shizukuBackendConnects() {
        val version = connectOrSkip(RemoteBackend.SHIZUKU)
        println("[shizuku] $version")
    }

    /** 验证已授权 root 后端的端到端连接。 / Verifies end-to-end connection through an authorized root backend. */
    @Test
    fun rootBackendConnects() {
        val version = connectOrSkip(RemoteBackend.ROOT)
        println("[root] $version")
    }
}
