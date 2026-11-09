package com.azurpilot.adb

import android.content.Context
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.lsposed.hiddenapibypass.HiddenApiBypass
import java.net.InetAddress
import java.util.concurrent.CompletableFuture
import java.util.concurrent.TimeUnit
import java.security.KeyStore
import javax.net.ssl.KeyManagerFactory
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLServerSocket
import javax.net.ssl.SSLSocket

/**
 * 在设备验证密钥持久化、系统 TLS 导出及原生 SPAKE2 交换；不授权真实 adbd。
 *
 * Verifies key persistence, platform TLS export, and native SPAKE2 exchange on-device without
 * authorizing a real adbd.
 */
@RunWith(AndroidJUnit4::class)
class AdbCryptoTest {
    @Test fun persistedIdentityAndPlatformTlsExporter() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val preferences = context.getSharedPreferences("adb_crypto_test", Context.MODE_PRIVATE)
        preferences.edit().clear().commit()
        try {
            val key = AdbKey(PreferenceAdbKeyStore(preferences), "test")
            val restored = AdbKey(PreferenceAdbKeyStore(preferences), "test")
            assertArrayEquals(key.adbPublicKey, restored.adbPublicKey)
            assertEquals(256, restored.sign(ByteArray(20)).size)
            val server = serverContext(key).serverSocketFactory.createServerSocket(
                0, 1, InetAddress.getByName("127.0.0.1"),
            ) as SSLServerSocket
            server.use {
                it.soTimeout = 5_000
                val exported = CompletableFuture.supplyAsync {
                    (server.accept() as SSLSocket).use { socket ->
                        socket.soTimeout = 5_000
                        socket.useClientMode = false
                        socket.startHandshake()
                        export(socket)
                    }
                }
                (restored.sslContext.socketFactory.createSocket("127.0.0.1", server.localPort) as SSLSocket).use { socket ->
                    socket.soTimeout = 5_000
                    socket.startHandshake()
                    assertArrayEquals(exported.get(6, TimeUnit.SECONDS), export(socket))
                }
            }
        } finally {
            preferences.edit().clear().commit()
        }
    }

    @Test fun nativePairingRoundTripAndWrongCodeRejection() {
        System.loadLibrary("azurpilot_adb")
        val client = NativePair(true, "123456")
        val server = NativePair(false, "123456")
        try {
            assertTrue(client.init(server.message()))
            assertTrue(server.init(client.message()))
            val payload = ByteArray(8192) { it.toByte() }
            assertArrayEquals(payload, server.decrypt(client.encrypt(payload)))
            assertArrayEquals(payload, client.decrypt(server.encrypt(payload)))
        } finally { client.close(); server.close() }
        val wrongClient = NativePair(true, "123456")
        val wrongServer = NativePair(false, "654321")
        try {
            assertTrue(wrongClient.init(wrongServer.message()))
            assertTrue(wrongServer.init(wrongClient.message()))
            assertNull(wrongServer.decrypt(wrongClient.encrypt(ByteArray(8192))))
        } finally { wrongClient.close(); wrongServer.close() }
    }

    private fun export(socket: SSLSocket): ByteArray = HiddenApiBypass.invoke(
        Class.forName("com.android.org.conscrypt.Conscrypt"), null,
        "exportKeyingMaterial", socket, "adb-label\u0000", null, 64,
    ) as ByteArray

    private fun serverContext(key: AdbKey): SSLContext {
        // 生产身份只选择客户端证书，测试服务器需单独装配服务端 KeyManager。
        val privateKey = AdbKey::class.java.getDeclaredField("privateKey")
            .apply { isAccessible = true }.get(key) as java.security.PrivateKey
        val certificate = AdbKey::class.java.getDeclaredField("certificate")
            .apply { isAccessible = true }.get(key) as java.security.cert.X509Certificate
        val store = KeyStore.getInstance(KeyStore.getDefaultType()).apply {
            load(null)
            setKeyEntry("test_server", privateKey, charArrayOf(), arrayOf(certificate))
        }
        val manager = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm()).apply {
            init(store, charArrayOf())
        }
        return SSLContext.getInstance("TLSv1.3").apply { init(manager.keyManagers, null, null) }
    }

    private class NativePair(client: Boolean, code: String) : AutoCloseable {
        private val clazz = Class.forName("com.azurpilot.adb.PairingContext")
        private val pointer = clazz.getDeclaredMethod(
            "nativeConstructor", Boolean::class.javaPrimitiveType, ByteArray::class.java,
        ).apply { isAccessible = true }.invoke(null, client, code.toByteArray()) as Long
        private val instance = clazz.getDeclaredConstructor(Long::class.javaPrimitiveType)
            .apply { isAccessible = true }.newInstance(pointer)
        fun message() = invoke("nativeMsg") as ByteArray
        fun init(message: ByteArray) = invoke("nativeInitCipher", message) as Boolean
        fun encrypt(message: ByteArray) = invoke("nativeEncrypt", message) as ByteArray
        fun decrypt(message: ByteArray) = invoke("nativeDecrypt", message) as ByteArray?
        override fun close() { invoke("nativeDestroy") }
        private fun invoke(name: String, data: ByteArray? = null): Any? {
            val types = if (data == null) arrayOf(Long::class.javaPrimitiveType)
                else arrayOf(Long::class.javaPrimitiveType, ByteArray::class.java)
            val args = if (data == null) arrayOf(pointer) else arrayOf(pointer, data)
            return clazz.getDeclaredMethod(name, *types).apply { isAccessible = true }
                .invoke(instance, *args)
        }
    }
}
