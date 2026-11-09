package com.azurpilot.adb

/**
 * 定义 ADB 客户端使用的线协议常量。
 *
 * Defines wire protocol constants used by the ADB client.
 *
 * 移植自 wess09/shizuku-m bcb2b62（Apache-2.0），保留协议实现。
 * Ported from wess09/shizuku-m bcb2b62 (Apache-2.0), preserving its protocol implementation.
 */
object AdbProtocol {

    const val A_SYNC = 0x434e5953
    const val A_CNXN = 0x4e584e43
    const val A_AUTH = 0x48545541
    const val A_OPEN = 0x4e45504f
    const val A_OKAY = 0x59414b4f
    const val A_CLSE = 0x45534c43
    const val A_WRTE = 0x45545257
    const val A_STLS = 0x534C5453

    const val A_VERSION = 0x01000000
    const val A_MAXDATA = 4096

    const val A_STLS_VERSION = 0x01000000

    const val ADB_AUTH_TOKEN = 1
    const val ADB_AUTH_SIGNATURE = 2
    const val ADB_AUTH_RSAPUBLICKEY = 3
}