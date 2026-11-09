package com.azurpilot.adb

import javax.net.ssl.SSLContext

/**
 * 提供 ADB RSA 和 TLS 认证材料；实现应持久保存身份，避免每次触发系统授权。
 *
 * Provides ADB RSA and TLS authentication material. Implementations persist the identity to
 * avoid requesting system approval on every connection. Used only on IO threads.
 */
interface AdbIdentity {
    /**
     * Android ADB 格式的公钥。
     *
     * Public key in Android ADB format.
     */
    val adbPublicKey: ByteArray
    /**
     * 配对与连接共用的客户端 TLS 身份。
     *
     * Client TLS identity shared by pairing and connection.
     */
    val sslContext: SSLContext
    /**
     * 对 adbd 的 SHA-1 token 进行 PKCS#1 RSA 签名。
     *
     * Signs an adbd SHA-1 token with PKCS#1 RSA.
     */
    fun sign(data: ByteArray?): ByteArray
}
