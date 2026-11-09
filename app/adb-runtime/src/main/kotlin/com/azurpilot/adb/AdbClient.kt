package com.azurpilot.adb

import timber.log.Timber
import com.azurpilot.adb.AdbProtocol.ADB_AUTH_RSAPUBLICKEY
import com.azurpilot.adb.AdbProtocol.ADB_AUTH_SIGNATURE
import com.azurpilot.adb.AdbProtocol.ADB_AUTH_TOKEN
import com.azurpilot.adb.AdbProtocol.A_AUTH
import com.azurpilot.adb.AdbProtocol.A_CLSE
import com.azurpilot.adb.AdbProtocol.A_CNXN
import com.azurpilot.adb.AdbProtocol.A_MAXDATA
import com.azurpilot.adb.AdbProtocol.A_OKAY
import com.azurpilot.adb.AdbProtocol.A_OPEN
import com.azurpilot.adb.AdbProtocol.A_STLS
import com.azurpilot.adb.AdbProtocol.A_STLS_VERSION
import com.azurpilot.adb.AdbProtocol.A_VERSION
import com.azurpilot.adb.AdbProtocol.A_WRTE
import java.io.Closeable
import java.io.DataInputStream
import java.io.DataOutputStream
import java.net.Socket
import java.net.InetSocketAddress
import java.nio.ByteBuffer
import java.nio.ByteOrder
import javax.net.ssl.SSLSocket

private const val TAG = "AdbClient"

/**
 * 在 IO 线程连接本机 ADB 并运行 shell 或 tcpip 服务。
 *
 * Connects to local ADB and runs shell or tcpip services on an IO thread.
 *
 * 移植自 wess09/shizuku-m bcb2b62（Apache-2.0），保留协议实现。
 * Ported from wess09/shizuku-m bcb2b62 (Apache-2.0), preserving its protocol implementation.
 */
class AdbClient(
    private val host: String,
    private val port: Int,
    private val key: AdbIdentity,
    private val timeoutMs: Int = 5_000,
    private val allowPublicKey: Boolean = true,
) : Closeable {

    private lateinit var socket: Socket
    private lateinit var plainInputStream: DataInputStream
    private lateinit var plainOutputStream: DataOutputStream

    private var useTls = false

    private lateinit var tlsSocket: SSLSocket
    private lateinit var tlsInputStream: DataInputStream
    private lateinit var tlsOutputStream: DataOutputStream

    private val inputStream get() = if (useTls) tlsInputStream else plainInputStream
    private val outputStream get() = if (useTls) tlsOutputStream else plainOutputStream

    fun connect() {
        require(host == "127.0.0.1") { "Only local ADB is supported" }
        socket = Socket()
        socket.connect(InetSocketAddress(host, port), timeoutMs)
        socket.tcpNoDelay = true
        if (timeoutMs > 0) socket.soTimeout = timeoutMs
        plainInputStream = DataInputStream(socket.getInputStream())
        plainOutputStream = DataOutputStream(socket.getOutputStream())

        write(A_CNXN, A_VERSION, A_MAXDATA, "host::")

        var message = read()
        if (message.command == A_STLS) {
            if (android.os.Build.VERSION.SDK_INT < 29) {
                error("Connect to adb with TLS is not supported before Android 9")
            }
            write(A_STLS, A_STLS_VERSION, 0)

            val sslContext = key.sslContext
            tlsSocket = sslContext.socketFactory.createSocket(socket, host, port, true) as SSLSocket
            tlsSocket.startHandshake()
            Timber.d("Handshake succeeded.")

            tlsInputStream = DataInputStream(tlsSocket.inputStream)
            tlsOutputStream = DataOutputStream(tlsSocket.outputStream)
            useTls = true

            message = read()
        } else if (message.command == A_AUTH) {
            if (message.command != A_AUTH || message.arg0 != ADB_AUTH_TOKEN) error("not A_AUTH ADB_AUTH_TOKEN")
            write(A_AUTH, ADB_AUTH_SIGNATURE, 0, key.sign(message.data))

            message = read()
            if (message.command != A_CNXN) {
                check(allowPublicKey) { "ADB identity requires system approval" }
                write(A_AUTH, ADB_AUTH_RSAPUBLICKEY, 0, key.adbPublicKey)
                message = read()
            }
        }

        if (message.command != A_CNXN) error("not A_CNXN")
    }

    fun shellCommand(command: String, listener: ((ByteArray) -> Unit)?) = runService("shell:$command", listener)

    // [shizuku-m] adbd's built-in tcpip service pins the TCP listener without shell setprop
    // (SELinux denies shell writing service.adb.tcp.port = adbd_config_prop on modern devices)
    fun tcpipCommand(port: Int) = runService("tcpip:$port", null)

    private fun runService(service: String, listener: ((ByteArray) -> Unit)?) {
        val localId = 1
        write(A_OPEN, localId, 0, service)

        var message = read()
        when (message.command) {
            A_OKAY -> {
                while (true) {
                    message = read()
                    val remoteId = message.arg0
                    if (message.command == A_WRTE) {
                        if (message.data_length > 0) {
                            listener?.invoke(message.data!!)
                        }
                        write(A_OKAY, localId, remoteId)
                    } else if (message.command == A_CLSE) {
                        write(A_CLSE, localId, remoteId)
                        break
                    } else {
                        error("not A_WRTE or A_CLSE")
                    }
                }
            }
            A_CLSE -> {
                val remoteId = message.arg0
                write(A_CLSE, localId, remoteId)
                error("ADB service rejected: $service")
            }
            else -> {
                error("not A_OKAY or A_CLSE")
            }
        }
    }

    private fun write(command: Int, arg0: Int, arg1: Int, data: ByteArray? = null) = write(AdbMessage(command, arg0, arg1, data))

    private fun write(command: Int, arg0: Int, arg1: Int, data: String) = write(AdbMessage(command, arg0, arg1, data))

    private fun write(message: AdbMessage) {
        outputStream.write(message.toByteArray())
        outputStream.flush()
    }

    private fun read(): AdbMessage {
        val buffer = ByteBuffer.allocate(AdbMessage.HEADER_LENGTH).order(ByteOrder.LITTLE_ENDIAN)

        inputStream.readFully(buffer.array(), 0, 24)

        val command = buffer.int
        val arg0 = buffer.int
        val arg1 = buffer.int
        val dataLength = buffer.int
        val checksum = buffer.int
        val magic = buffer.int
        val data: ByteArray?
        require(dataLength in 0..A_MAXDATA) { "Invalid ADB payload length" }
        if (dataLength >= 0) {
            data = ByteArray(dataLength)
            inputStream.readFully(data, 0, dataLength)
        } else {
            data = null
        }
        val message = AdbMessage(command, arg0, arg1, dataLength, checksum, magic, data)
        message.validateOrThrow()
        return message
    }

    override fun close() {
        try {
            plainInputStream.close()
        } catch (e: Throwable) {
        }
        try {
            plainOutputStream.close()
        } catch (e: Throwable) {
        }
        try {
            socket.close()
        } catch (e: Exception) {
        }

        if (useTls) {
            try {
                tlsInputStream.close()
            } catch (e: Throwable) {
            }
            try {
                tlsOutputStream.close()
            } catch (e: Throwable) {
            }
            try {
                tlsSocket.close()
            } catch (e: Exception) {
            }
        }
    }
}
