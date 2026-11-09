package com.azurpilot.adb

import com.azurpilot.adb.AdbProtocol.A_AUTH
import com.azurpilot.adb.AdbProtocol.A_CNXN
import com.azurpilot.adb.AdbProtocol.A_OPEN
import com.azurpilot.adb.AdbProtocol.A_OKAY
import com.azurpilot.adb.AdbProtocol.A_WRTE
import com.azurpilot.adb.AdbProtocol.A_CLSE
import com.azurpilot.adb.AdbProtocol.A_VERSION
import com.azurpilot.adb.AdbProtocol.A_MAXDATA
import com.azurpilot.adb.AdbProtocol.ADB_AUTH_TOKEN
import com.azurpilot.adb.AdbProtocol.ADB_AUTH_SIGNATURE
import org.junit.Assert.*
import org.junit.Test
import java.io.DataInputStream
import java.io.DataOutputStream
import java.net.ServerSocket
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.util.concurrent.CompletableFuture
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext

/**
 * 用本机假 adbd 验证认证、拒绝、超长帧及 shell 握手；不访问真实调试服务。
 *
 * Verifies authentication, rejection, oversized frames, and shell handshakes against a local
 * fake adbd without accessing a real debugging service.
 */
class AdbClientTest {
    private val identity = object : AdbIdentity {
        override val adbPublicKey = "test-key\u0000".toByteArray()
        override val sslContext: SSLContext get() = SSLContext.getDefault()
        override fun sign(data: ByteArray?) = ByteArray(256)
    }

    @Test fun shellHandshakeAndOutput() = withServer({ input, output ->
        assertEquals(A_CNXN, read(input).command)
        write(output, AdbMessage(A_CNXN, A_VERSION, A_MAXDATA, "device::"))
        val open = read(input)
        assertEquals(A_OPEN, open.command)
        assertEquals("shell:id -u\u0000", open.data!!.toString(Charsets.UTF_8))
        write(output, AdbMessage(A_OKAY, 7, 1))
        write(output, AdbMessage(A_WRTE, 7, 1, "2000\n".toByteArray()))
        assertEquals(A_OKAY, read(input).command)
        write(output, AdbMessage(A_CLSE, 7, 1))
        assertEquals(A_CLSE, read(input).command)
    }) { client ->
        client.connect()
        val result = StringBuilder()
        client.shellCommand("id -u") { result.append(it.toString(Charsets.UTF_8)) }
        assertEquals("2000\n", result.toString())
    }

    @Test fun authenticationRequiresTokenType() = withServer({ input, output ->
        read(input)
        write(output, AdbMessage(A_AUTH, ADB_AUTH_SIGNATURE, 0, ByteArray(20)))
    }) { client -> assertThrows(IllegalStateException::class.java) { client.connect() }; Unit }

    @Test fun silentReconnectDoesNotSendPublicKey() = withServer({ input, output ->
        read(input)
        write(output, AdbMessage(A_AUTH, ADB_AUTH_TOKEN, 0, ByteArray(20)))
        assertEquals(ADB_AUTH_SIGNATURE, read(input).arg0)
        write(output, AdbMessage(A_AUTH, ADB_AUTH_TOKEN, 0, ByteArray(20)))
        assertEquals(-1, input.read())
    }, allowPublicKey = false) { client ->
        assertThrows(IllegalStateException::class.java) { client.connect() }
        client.close()
    }

    @Test fun rejectedShellServiceFails() = withServer({ input, output ->
        read(input)
        write(output, AdbMessage(A_CNXN, A_VERSION, A_MAXDATA, "device::"))
        read(input)
        write(output, AdbMessage(A_CLSE, 0, 1))
        assertEquals(A_CLSE, read(input).command)
    }) { client ->
        client.connect()
        assertThrows(IllegalStateException::class.java) { client.shellCommand("id -u", null) }
        Unit
    }

    @Test fun oversizedPayloadRejectedBeforeReadingBody() = withServer({ input, output ->
        read(input)
        val header = ByteBuffer.allocate(24).order(ByteOrder.LITTLE_ENDIAN)
            .putInt(A_CNXN).putInt(0).putInt(0).putInt(Int.MAX_VALUE).putInt(0).putInt(A_CNXN.inv())
        output.write(header.array())
        output.flush()
    }) { client -> assertThrows(IllegalArgumentException::class.java) { client.connect() }; Unit }

    @Test fun badChecksumRejected() {
        val bytes = byteArrayOf(1, 2, 3)
        assertFalse(AdbMessage(A_WRTE, 1, 1, 3, 0, A_WRTE.inv(), bytes).validate())
        assertFalse(AdbMessage(A_WRTE, 1, 1, -1, 0, A_WRTE.inv(), null).validate())
    }

    @Test fun remoteHostRejected() {
        AdbClient("192.0.2.1", 5555, identity).use { client ->
            assertThrows(IllegalArgumentException::class.java) { client.connect() }
        }
    }

    private fun withServer(
        server: (DataInputStream, DataOutputStream) -> Unit,
        allowPublicKey: Boolean = true,
        action: (AdbClient) -> Unit,
    ) {
        ServerSocket(0, 1, java.net.InetAddress.getByName("127.0.0.1")).use { listener ->
            listener.soTimeout = 3_000
            val result = CompletableFuture.runAsync {
                listener.accept().use { socket ->
                    socket.soTimeout = 3_000
                    server(DataInputStream(socket.inputStream), DataOutputStream(socket.outputStream))
                }
            }
            AdbClient("127.0.0.1", listener.localPort, identity, 2_000, allowPublicKey).use(action)
            result.get(4, TimeUnit.SECONDS)
        }
    }

    private fun read(input: DataInputStream): AdbMessage {
        val header = ByteBuffer.allocate(24).order(ByteOrder.LITTLE_ENDIAN)
        input.readFully(header.array())
        val command = header.int
        val arg0 = header.int
        val arg1 = header.int
        val length = header.int
        val checksum = header.int
        val magic = header.int
        require(length in 0..4096)
        val data = ByteArray(length)
        input.readFully(data)
        return AdbMessage(command, arg0, arg1, length, checksum, magic, data)
    }

    private fun write(output: DataOutputStream, message: AdbMessage) {
        output.write(message.toByteArray())
        output.flush()
    }

    private fun AdbMessage(command: Int, arg0: Int, arg1: Int) =
        AdbMessage(command, arg0, arg1, null as ByteArray?)
}
