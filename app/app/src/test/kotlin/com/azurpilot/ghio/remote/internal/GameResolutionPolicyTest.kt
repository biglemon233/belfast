package com.azurpilot.ghio.remote.internal

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 验证原生分辨率策略只修改目标包，处理静态、动态 APS 和标准兼容模式的拒绝与回读。
 *
 * Verifies package-scoped native resolution, including persistent and dynamic APS, standard
 * compatibility mode, rejection, and readback. Runs on the JVM without Android framework stubs.
 */
class GameResolutionPolicyTest {
    private val game = "com.bilibili.azurlane"

    @Test
    fun noApsServiceDoesNotChangeConfiguration() {
        assertFalse(GameResolutionPolicy.disableApsScaling(null, game))
    }

    @Test
    fun staticMagicOsRatioIsReplacedWithOneWithoutChangingFps() {
        val aps = Aps(0.84375f)
        assertTrue(GameResolutionPolicy.disableApsScaling(aps, game))
        assertEquals(1f, aps.info.ratio, 0f)
        assertFalse(aps.switchable)
        assertEquals(120, aps.fps)
        assertEquals(listOf(game), aps.writes)
    }

    @Test
    fun inactivePersistentRatioIsDisabledBeforeItCanBecomeActive() {
        val aps = Aps(0.84375f).apply { effectiveOverride = -1f }
        assertTrue(GameResolutionPolicy.disableApsScaling(aps, game))
        assertEquals(1f, aps.info.ratio, 0f)
    }

    @Test
    fun nativeRatioDoesNotWriteConfiguration() {
        val aps = Aps(1f)
        assertFalse(GameResolutionPolicy.disableApsScaling(aps, game))
        assertTrue(aps.writes.isEmpty())
    }

    @Test
    fun missingPackageDoesNotCreateAnApsRecord() {
        val aps = Aps(1f).apply { hasInfo = false; effectiveOverride = -1f }
        assertFalse(GameResolutionPolicy.disableApsScaling(aps, game))
        assertTrue(aps.writes.isEmpty())
    }

    @Test
    fun dynamicRatioIsDisabledIndependently() {
        val aps = DynamicAps(1f).apply { dynamic = 0.84375f }
        assertTrue(GameResolutionPolicy.disableApsScaling(aps, game))
        assertEquals(1f, aps.dynamic, 0f)
        assertEquals(listOf(game), aps.dynamicWrites)
        assertTrue(aps.writes.isEmpty())
    }

    @Test
    fun missingDynamicCallbackIsAcceptedOnlyAfterSuccessfulReadback() {
        val aps = DynamicAps(1f).apply { dynamic = 0.84375f; dynamicStatus = -5 }
        assertTrue(GameResolutionPolicy.disableApsScaling(aps, game))
        assertEquals(1f, aps.dynamic, 0f)
    }

    @Test
    fun staticPermissionDenialIsAnError() {
        val aps = Aps(0.84375f).apply { status = -3 }
        val error = assertThrows(IllegalStateException::class.java) {
            GameResolutionPolicy.disableApsScaling(aps, game)
        }
        assertTrue(error.message!!.contains("status=-3"))
        assertEquals(0.84375f, aps.info.ratio, 0f)
    }

    @Test
    fun binderPermissionExceptionIsUnwrappedForDiagnostics() {
        val aps = Aps(0.84375f).apply { rejectRead = true }
        val error = assertThrows(SecurityException::class.java) {
            GameResolutionPolicy.disableApsScaling(aps, game)
        }
        assertEquals("UPDATE_APS_INFO denied", error.message)
    }

    @Test
    fun invalidResolutionReadbackIsAnError() {
        val aps = Aps(1f).apply { effectiveOverride = Float.NaN }
        assertThrows(IllegalStateException::class.java) {
            GameResolutionPolicy.disableApsScaling(aps, game)
        }
    }

    @Test
    fun successfulReturnWithoutStaticReadbackIsAnError() {
        val aps = Aps(0.84375f).apply { ignoreWrite = true }
        assertThrows(IllegalStateException::class.java) {
            GameResolutionPolicy.disableApsScaling(aps, game)
        }
    }

    @Test
    fun dynamicPermissionDenialIsAnError() {
        val aps = DynamicAps(1f).apply { dynamic = 0.84375f; dynamicStatus = -3 }
        assertThrows(IllegalStateException::class.java) {
            GameResolutionPolicy.disableApsScaling(aps, game)
        }
    }

    @Test
    fun missingDynamicCallbackWithUnchangedRatioIsAnError() {
        val aps = DynamicAps(1f).apply {
            dynamic = 0.84375f; dynamicStatus = -5; ignoreDynamicWrite = true
        }
        assertThrows(IllegalStateException::class.java) {
            GameResolutionPolicy.disableApsScaling(aps, game)
        }
    }

    @Test
    fun standardCompatIsDisabledAndVerified() {
        val compat = Compat(1)
        assertTrue(GameResolutionPolicy.disableScreenCompat(compat, game))
        assertEquals(0, compat.mode)
        assertEquals(listOf(game), compat.writes)
    }

    @Test
    fun disabledAndNeverNeededModesDoNotWrite() {
        for (mode in listOf(0, -2)) {
            val compat = Compat(mode)
            assertFalse(GameResolutionPolicy.disableScreenCompat(compat, game))
            assertTrue(compat.writes.isEmpty())
        }
    }

    @Test
    fun mandatoryUnknownAndRejectedCompatModesAreErrors() {
        for (mode in listOf(-1, -3, 1)) {
            val compat = Compat(mode).apply { ignoreWrite = true }
            assertThrows(IllegalStateException::class.java) {
                GameResolutionPolicy.disableScreenCompat(compat, game)
            }
        }
    }

    private class Info(var ratio: Float) {
        fun getResolutionRatio(): Float = ratio
    }

    private open class Aps(ratio: Float) {
        val info = Info(ratio)
        var effectiveOverride: Float? = null
        var hasInfo = true
        var switchable = true
        val fps = 120
        var status = 0
        var ignoreWrite = false
        var rejectRead = false
        val writes = mutableListOf<String>()

        fun getPackageApsInfo(packageName: String): Info? = info.takeIf { hasInfo }
        fun getResolution(packageName: String): Float {
            if (rejectRead) throw SecurityException("UPDATE_APS_INFO denied")
            return effectiveOverride ?: info.ratio
        }
        fun setResolution(packageName: String, ratio: Float, isSwitchable: Boolean): Int {
            writes += packageName
            if (status == 0 && !ignoreWrite) {
                info.ratio = ratio
                switchable = isSwitchable
            }
            return status
        }
    }

    private class DynamicAps(ratio: Float) : Aps(ratio) {
        var dynamic = 1f
        var dynamicStatus = 0
        var ignoreDynamicWrite = false
        val dynamicWrites = mutableListOf<String>()

        fun getDynamicResolutionRatio(packageName: String): Float = dynamic
        fun setDynamicResolutionRatio(packageName: String, ratio: Float): Int {
            dynamicWrites += packageName
            if ((dynamicStatus == 0 || dynamicStatus == -5) && !ignoreDynamicWrite) dynamic = ratio
            return dynamicStatus
        }
    }

    private class Compat(var mode: Int) {
        var ignoreWrite = false
        val writes = mutableListOf<String>()

        fun getPackageScreenCompatMode(packageName: String): Int = mode
        fun setPackageScreenCompatMode(packageName: String, requested: Int) {
            writes += packageName
            if (!ignoreWrite) mode = requested
        }
    }
}
