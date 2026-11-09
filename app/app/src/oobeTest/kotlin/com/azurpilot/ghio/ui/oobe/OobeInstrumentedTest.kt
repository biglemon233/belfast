package com.azurpilot.ghio.ui.oobe

import android.graphics.Bitmap
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.requiredSize
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.graphics.toPixelMap
import androidx.compose.ui.unit.dp
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.StateRestorationTester
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.azurpilot.ghio.R
import com.azurpilot.ghio.i18n.AppLocales
import android.content.res.Configuration
import android.os.LocaleList
import com.azurpilot.ghio.domain.RemoteBackend
import com.azurpilot.ghio.privileged.RemoteAccessState
import com.azurpilot.ghio.settings.AppSettings
import com.azurpilot.ghio.settings.AppSettingsManager
import com.azurpilot.ghio.theme.AzurPilotTheme
import com.azurpilot.ghio.ui.ksu.AppRootScaffold
import java.io.File
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.koin.core.context.GlobalContext

/**
 * 在 ADB 设备上验证 OOBE 的确认门、步骤恢复、两套主题、权限分支和持久化。
 * 测试结束恢复偏好，截图保存在应用外部文件目录的 oobe-tests 下。
 *
 * Verifies the OOBE acknowledgement gate, restoration, both themes, privilege branches,
 * and persistence on an ADB device. Restores preferences and exports screenshots to oobe-tests
 * in the app's external files directory.
 */
@RunWith(AndroidJUnit4::class)
class OobeInstrumentedTest {
    @get:Rule val compose = createComposeRule()
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext
    private lateinit var manager: AppSettingsManager
    private lateinit var original: AppSettings

    @Before
    fun loadSettings() {
        manager = GlobalContext.get().get()
        runBlocking {
            withTimeout(10_000) { manager.loaded.first { it } }
            original = manager.settings.first()
        }
    }

    @After
    fun restoreSettings() {
        runBlocking {
            manager.setAppearance { original }
            manager.setOobeCompleted(original.oobeCompleted.toBoolean())
            manager.setCommunityNoticeAcknowledged(original.communityNoticeAcknowledged.toBoolean())
            manager.setLegalNoticeVersion(original.legalNoticeVersion)
            manager.setSkipShizukuCheck(original.skipShizukuCheck.toBoolean())
        }
    }

    @Test
    fun confirmationSurvivesBackNavigationAndSavedStateRestoration() {
        var completed = 0
        val restoration = StateRestorationTester(compose)
        restoration.setContent {
            AzurPilotTheme { AppRootScaffold {
                OobeScreen(alreadyAcknowledged = false, onComplete = { completed++ })
            } }
        }
        compose.waitUntil(5000) { runCatching {
            compose.onAllNodes(hasText(text(R.string.oobe_action_next)) or
                hasContentDescription(text(R.string.oobe_action_next))).fetchSemanticsNodes().isNotEmpty()
        }.getOrDefault(false) }
        node(R.string.oobe_action_next).assertIsEnabled().performClick()
        node(R.string.oobe_pref_lang_title).assertIsDisplayed()
        forward()
        node(R.string.oobe_action_next).assertIsNotEnabled()
        node(R.string.oobe_welcome_guidelines_accept).performScrollTo().assertIsEnabled()
        node(R.string.oobe_action_next).assertIsNotEnabled()
        node(R.string.oobe_welcome_guidelines_accept).performClick()
        node(R.string.oobe_action_next).assertIsEnabled().performClick()
        node(R.string.oobe_pref_title).assertIsDisplayed()
        restoration.emulateSavedInstanceStateRestore()
        node(R.string.oobe_pref_title).assertIsDisplayed()
        node(R.string.oobe_action_prev).performClick()
        node(R.string.oobe_welcome_guidelines_accept).assertIsOn()
        node(R.string.oobe_action_next).assertIsEnabled()
        node(R.string.oobe_welcome_guidelines_accept).performClick()
        node(R.string.oobe_action_next).assertIsNotEnabled()
        node(R.string.oobe_welcome_guidelines_accept).performClick()
        node(R.string.oobe_action_next).performClick()
        node(R.string.oobe_action_next).performClick()
        node(R.string.oobe_priv_title).assertIsDisplayed()
        assertEquals(0, completed)
    }

    @Test
    fun legacyAcknowledgementCanContinueImmediatelyAfterConfirmation() {
        var acknowledged = 0
        compose.setContent { AzurPilotTheme { AppRootScaffold {
            com.azurpilot.ghio.ui.components.CommunityWarningDialog { acknowledged++ }
        } } }
        // 原生 Dialog 窗口的入场动画不由 Compose 测试时钟推进。
        android.os.SystemClock.sleep(350)
        node(R.string.community_warning_continue).assertIsNotEnabled()
        node(R.string.community_warning_confirm_read).performScrollTo().assertIsEnabled().performClick()
        node(R.string.community_warning_continue).assertIsDisplayed().assertIsEnabled().performClick()
        compose.runOnIdle { assertEquals(1, acknowledged) }
    }

    @Test
    fun allActivationStepsRenderInBothThemesAndColorModes() {
        var completed = 0
        compose.setContent { AzurPilotTheme { AppRootScaffold {
            OobeScreen(alreadyAcknowledged = true, onComplete = { completed++ })
        } } }
        for (theme in listOf("material", "miuix")) {
            for (mode in listOf("1", "2")) {
                runBlocking { manager.setAppearance { it.copy(uiMode = theme, colorMode = mode) } }
                compose.waitUntil(10_000) { manager.appearance.value.uiMode == theme &&
                    manager.appearance.value.colorMode == mode }
                compose.waitForIdle()
                for ((index, step) in OobeStep.entries.withIndex()) {
                    val progress = hasStateDescription(context.createConfigurationContext(Configuration(context.resources.configuration).apply {
                        AppLocales.currentTag()?.let { setLocales(LocaleList.forLanguageTags(it)) }
                    }).getString(R.string.oobe_progress_format, index + 1, OobeStep.entries.size))
                    compose.waitUntil(5000) { runCatching {
                        compose.onAllNodes(progress).fetchSemanticsNodes().isNotEmpty()
                    }.getOrDefault(false) }
                    compose.onNode(progress).assertIsDisplayed()
                    screenshot("${theme}_${mode}_${step.name.lowercase()}")
                    if (index < OobeStep.entries.lastIndex) forward()
                }
                node(R.string.oobe_action_start).assertIsDisplayed().assertIsEnabled()
                    .assertHeightIsAtLeast(48.dp).performClick()
                repeat(OobeStep.entries.lastIndex) { node(R.string.oobe_action_prev).performClick() }
            }
        }
        assertEquals(4, completed)
    }

    @Test
    fun themeSwitchPreservesStepAndMonetUsesActiveFrameworkPreference() {
        runBlocking { manager.setAppearance { it.copy(uiMode = "miuix", colorMode = "1", miuixMonet = "false") } }
        val restoration = StateRestorationTester(compose)
        restoration.setContent { AzurPilotTheme { AppRootScaffold {
            OobeScreen(alreadyAcknowledged = true, onComplete = {})
        } } }
        forward()
        forward()
        forward()
        node(R.string.oobe_pref_monet_title).performScrollTo().performClick()
        compose.waitUntil(10_000) { manager.appearance.value.miuixMonet == "true" }
        assertEquals("1", manager.appearance.value.colorMode)
        node(R.string.oobe_pref_theme_dark).performScrollTo().performClick()
        compose.waitUntil(10_000) { manager.appearance.value.colorMode == "2" }
        assertEquals("true", manager.appearance.value.miuixMonet)
        runBlocking { manager.setAppearance { it.copy(uiMode = "material") } }
        node(R.string.oobe_pref_title).performScrollTo().assertIsDisplayed()
        node(R.string.oobe_pref_monet_title).performScrollTo().assertIsOff().performClick()
        compose.waitUntil(10_000) { manager.appearance.value.colorMode == "5" }
        restoration.emulateSavedInstanceStateRestore()
        node(R.string.oobe_pref_monet_title).performScrollTo().assertIsOn()
        repeat(3) { node(R.string.oobe_action_prev).performClick() }
        node(R.string.oobe_welcome_title).assertIsDisplayed()
    }

    @Test
    fun privilegeBranchesShowCorrectActionsAndFailureFeedback() {
        val state = mutableStateOf(RemoteAccessState())
        val requesting = mutableStateOf(false)
        val failed = mutableStateOf(false)
        var requests = 0
        var activations = 0
        compose.setContent { AzurPilotTheme { AppRootScaffold {
            Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
                OobePrivilegedContent(state.value, requesting.value, failed.value,
                    onSelectBackend = { state.value = state.value.copy(configuredBackend = it) },
                    onRequestAccess = { requests++ }, onOpenActivation = { activations++ })
            }
        } } }
        for (theme in listOf("material", "miuix")) {
            runBlocking { manager.setAppearance { it.copy(uiMode = theme) } }
            compose.runOnIdle { state.value = RemoteAccessState() }
            compose.waitUntil(5000) { runCatching {
                compose.onAllNodesWithText(text(R.string.oobe_priv_status_not_running))
                    .fetchSemanticsNodes().isNotEmpty()
            }.getOrDefault(false) }
            node(R.string.oobe_priv_status_not_running).performScrollTo().assertIsDisplayed()
            node(R.string.oobe_priv_action_pair).performScrollTo().performClick()
            compose.runOnIdle { state.value = RemoteAccessState(shizukuAvailable = true) }
            node(R.string.oobe_priv_status_need_auth).performScrollTo().assertIsDisplayed()
            node(R.string.oobe_priv_action_auth).performScrollTo().performClick()
            compose.runOnIdle { requesting.value = true }
            node(R.string.oobe_priv_action_auth).assertIsNotEnabled()
            node(R.string.oobe_priv_requesting).performScrollTo().assertIsDisplayed()
            compose.runOnIdle { requesting.value = false; failed.value = true }
            node(R.string.oobe_priv_grant_failed).performScrollTo().assertIsDisplayed()
            screenshot("${theme}_grant_failed")
            compose.runOnIdle { state.value = RemoteAccessState(shizukuAvailable = true, shizukuGranted = true) }
            node(R.string.oobe_priv_status_ready).performScrollTo().assertIsDisplayed()
            node(R.string.oobe_priv_action_auth).assertDoesNotExist()
            compose.runOnIdle { state.value = RemoteAccessState(configuredBackend = RemoteBackend.ROOT); failed.value = false }
            node(R.string.oobe_priv_status_root_unavailable).performScrollTo().assertIsDisplayed()
            node(R.string.oobe_priv_action_root).performScrollTo().performClick()
            compose.runOnIdle { state.value = state.value.copy(rootAvailable = true) }
            node(R.string.oobe_priv_status_need_auth).performScrollTo().assertIsDisplayed()
            compose.runOnIdle { state.value = state.value.copy(rootGranted = true) }
            node(R.string.oobe_priv_status_ready).performScrollTo().assertIsDisplayed()
            node(R.string.oobe_priv_action_root).assertDoesNotExist()
            screenshot("${theme}_root_ready")
        }
        assertEquals(4, requests)
        assertEquals(2, activations)
    }

    @Test
    fun previewDismissesAndCompletionPersistsBothFlags() {
        var dismissed = 0
        compose.setContent { AzurPilotTheme { AppRootScaffold {
            OobeScreen(alreadyAcknowledged = true, onComplete = {}, onDismiss = { dismissed++ })
        } } }
        node(R.string.dialog_cancel).assertIsDisplayed().performClick()
        assertEquals(1, dismissed)
        runBlocking {
            manager.setCommunityNoticeAcknowledged(false)
            manager.setOobeCompleted(false)
            manager.completeOobe()
            val saved = withTimeout(10_000) { manager.settings.first {
                it.oobeCompleted.toBoolean() && it.communityNoticeAcknowledged.toBoolean()
            } }
            assertEquals("true", saved.oobeCompleted)
            assertEquals("true", saved.communityNoticeAcknowledged)
            assertEquals(com.azurpilot.ghio.settings.CURRENT_LEGAL_NOTICE_VERSION, saved.legalNoticeVersion)
        }
    }

    @Test
    fun originalPhoneGlyphsRetainTheirAssetBytes() {
        for ((resource, expected) in listOf(
            R.raw.oobe_os3_language to "2eaef15f5e131cd7caaa0490cbae6677cfa0628a8ad32bfc457ef527aa548aad",
            R.raw.oobe_os3_terms to "e7ea44afd84710d8d33fff278bdb8131dc74252739950372a49208bc80abfd2a",
            R.raw.oobe_os3_settings to "74f20d5bcaa97cacf3a282f18c54c99b3b19c9c3c7c5b2fecc6c415f94cda377",
            R.raw.oobe_os3_password to "18ce6242fa285395eb84166b5a3cadc0132b09aef85a9b9b7e824b0d60515641"
        )) {
            val bytes = context.resources.openRawResource(resource).use { it.readBytes() }
            val digest = java.security.MessageDigest.getInstance("SHA-256").digest(bytes)
                .joinToString("") { "%02x".format(it) }
            assertEquals(expected, digest)
            val composition = com.airbnb.lottie.LottieCompositionFactory.fromRawResSync(context, resource)
            assertNotNull("Original glyph must load: ${composition.exception}", composition.value)
        }
    }

    @Test
    fun glyphsWaitForEntryAndResumeFromTheirPausedFrame() {
        val composition = mutableStateOf<com.airbnb.lottie.LottieComposition?>(null)
        var progress: androidx.compose.runtime.State<Float>? = null
        val entered = mutableStateOf(false)
        lateinit var owner: GlyphLifecycleOwner
        compose.runOnUiThread {
            owner = GlyphLifecycleOwner()
            owner.registry.currentState = androidx.lifecycle.Lifecycle.State.STARTED
        }
        val resolver = context.contentResolver
        val originalScale = android.provider.Settings.Global.getString(resolver,
            android.provider.Settings.Global.ANIMATOR_DURATION_SCALE)
        fun shell(command: String) = android.os.ParcelFileDescriptor.AutoCloseInputStream(
            InstrumentationRegistry.getInstrumentation().uiAutomation.executeShellCommand(command),
        ).use { it.bufferedReader().readText() }
        fun frame() = checkNotNull(progress).value
        try {
            shell("settings put global animator_duration_scale 1")
            compose.mainClock.autoAdvance = false
            compose.setContent { AzurPilotTheme {
                androidx.compose.runtime.CompositionLocalProvider(
                    androidx.lifecycle.compose.LocalLifecycleOwner provides owner,
                    LocalOobePageActive provides entered.value,
                ) {
                    progress = rememberOobeGlyphProgress(composition.value)
                }
            } }
            for (asset in OobeGlyph.entries) {
                val loaded = checkNotNull(com.airbnb.lottie.LottieCompositionFactory
                    .fromRawResSync(context, asset.resource).value)
                compose.runOnUiThread {
                    entered.value = false
                    owner.registry.currentState = androidx.lifecycle.Lifecycle.State.STARTED
                    composition.value = loaded
                }
                compose.mainClock.advanceTimeBy(100)
                assertEquals("$asset must wait for page entry", 0f, frame(), .001f)
                compose.runOnUiThread { entered.value = true }
                compose.mainClock.advanceTimeBy(250)
                assertEquals("$asset must not jump to the end before resume", 0f,
                    frame(), .001f)
                compose.runOnUiThread {
                    owner.registry.currentState = androidx.lifecycle.Lifecycle.State.RESUMED
                }
                compose.mainClock.advanceTimeBy(220)
                val playing = frame()
                assertTrue("$asset must animate after entry: $playing", playing > 0f && playing < .9f)
                compose.runOnUiThread {
                    owner.registry.currentState = androidx.lifecycle.Lifecycle.State.STARTED
                }
                compose.mainClock.advanceTimeByFrame()
                val paused = frame()
                compose.mainClock.advanceTimeBy(250)
                assertEquals("$asset must retain its frame in the background", paused, frame(), .001f)
                compose.runOnUiThread {
                    owner.registry.currentState = androidx.lifecycle.Lifecycle.State.RESUMED
                }
                compose.mainClock.advanceTimeBy(120)
                val resumed = frame()
                compose.mainClock.advanceTimeBy(2000)
                val complete = frame()
                assertTrue("$asset must resume without skipping to the end",
                    resumed > paused && resumed < complete)
                assertTrue("$asset must reach the final frame", complete > .95f)
                compose.mainClock.advanceTimeBy(500)
                assertEquals("$asset must settle after one complete playback", complete, frame(), .001f)
            }
        } finally {
            compose.mainClock.autoAdvance = true
            shell(if (originalScale == null) "settings delete global animator_duration_scale"
                else "settings put global animator_duration_scale ${originalScale.toFloat()}")
        }
    }

    /** 在主线程驱动图标的前后台状态。 / Drives glyph foreground state on the main thread. */
    private class GlyphLifecycleOwner : androidx.lifecycle.LifecycleOwner {
        val registry = androidx.lifecycle.LifecycleRegistry(this)
        override val lifecycle: androidx.lifecycle.Lifecycle get() = registry
    }

    @Test
    fun legalDocumentsReadOfflineAndRetainConsentWhenClosed() {
        for (language in listOf("zh-CN", "zh-TW", "en", "ja")) {
            for (document in listOf(OobeLegalDocument.TERMS, OobeLegalDocument.PRIVACY)) {
                val body = document.read(context, language)
                assertTrue("$document/$language must be a full document", body.length > 1200)
                assertTrue(body.contains("2026-10-14") || body.contains("2026年10月14日"))
                assertTrue(body.contains("9. "))
                assertTrue(body.contains("https://github.com/wess09/AzurPilot-for-Android"))
            }
        }
        val license = OobeLegalDocument.AGPL.read(context, "zh-CN")
        val digest = java.security.MessageDigest.getInstance("SHA-256").digest(license.toByteArray())
            .joinToString("") { "%02x".format(it) }
        assertEquals("0f1981d1a64e5d43a85ae08eedc779170dfbc38c5d65d95811f347bd7c65b370", digest)
        assertTrue(license.contains("How to Apply These Terms to Your New Programs"))
        var acknowledged = false
        compose.setContent { AzurPilotTheme { AppRootScaffold {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                OobeStepAgreement(true, { acknowledged = it })
            }
        } } }
        for (theme in listOf("material", "miuix")) {
        runBlocking { manager.setAppearance { it.copy(uiMode = theme, colorMode = "1") } }
        for (document in OobeLegalDocument.entries) {
            node(document.title).performScrollTo().performClick()
            compose.waitUntil(5000) {
                compose.onAllNodesWithText(if (document == OobeLegalDocument.AGPL)
                    "GNU AFFERO GENERAL PUBLIC LICENSE" else "2026", substring = true)
                    .fetchSemanticsNodes().isNotEmpty()
            }
            // 原生 Dialog 窗口动画不受 Compose 测试时钟控制，等待窗口稳定再取图。
            android.os.SystemClock.sleep(350)
            screenshot("legal_${theme}_${document.name.lowercase()}")
            val language = AppLocales.currentTag() ?: context.resources.configuration.locales[0].toLanguageTag()
            val last = if (document == OobeLegalDocument.AGPL) 1
                else document.read(context, language).trim().split("\n\n").lastIndex
            val documentScroll = compose.onNode(hasScrollAction() and hasAnyAncestor(isDialog()))
            documentScroll.performScrollToIndex(last)
            compose.waitForIdle()
            val raw = document.read(context, language).trim()
            val finalText = if (document == OobeLegalDocument.AGPL) raw
                else raw.split("\n\n").last().substringAfter('\n')
            val finalNode = compose.onNodeWithText(finalText)
            repeat(5) {
                if (finalNode.getUnclippedBoundsInRoot().bottom > documentScroll.getUnclippedBoundsInRoot().bottom) {
                    documentScroll.performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.ScrollBy) {
                        it(0f, 100_000f)
                    }
                    compose.waitForIdle()
                }
            }
            finalNode.assertIsDisplayed()
            assertTrue("The final paragraph must fit fully into the viewport",
                finalNode.getUnclippedBoundsInRoot().bottom <= documentScroll.getUnclippedBoundsInRoot().bottom + 1.dp)
            screenshot("legal_${theme}_${document.name.lowercase()}_end")
            node(R.string.oobe_action_prev).performClick()
            node(R.string.oobe_welcome_guidelines_acknowledged).performScrollTo().assertIsDisplayed()
            assertTrue(acknowledged)
        }
        }
    }

    @Test
    fun compactWelcomeKeepsBrandAndNextButtonApart() {
        runBlocking { manager.setAppearance { it.copy(uiMode = "miuix") } }
        var next = 0
        compose.setContent { AzurPilotTheme { AppRootScaffold {
            OobeStepWelcome(Modifier.requiredSize(320.dp, 240.dp), { next++ }, text(R.string.oobe_action_next),
                true, null, text(R.string.dialog_cancel))
        } } }
        android.os.SystemClock.sleep(2000)
        val brand = compose.onNode(hasContentDescription(text(R.string.oobe_welcome_title)))
            .getUnclippedBoundsInRoot()
        val button = node(R.string.oobe_action_next).getUnclippedBoundsInRoot()
        assertTrue("The brand must not overlap the next button in short windows", brand.bottom <= button.top)
        node(R.string.oobe_action_next).performClick()
        assertEquals(1, next)
        screenshot("miuix_compact_welcome")
    }

    @OptIn(androidx.compose.ui.test.ExperimentalTestApi::class)
    @Test
    fun miuixWelcomeEnablesContinueAfterTheOriginalButtonEntrance() {
        runBlocking { manager.setAppearance { it.copy(uiMode = "miuix", colorMode = "1") } }
        compose.waitUntil(10_000) {
            manager.appearance.value.uiMode == "miuix" && manager.appearance.value.colorMode == "1"
        }
        var next = 0
        compose.mainClock.autoAdvance = false
        try {
            compose.setContent { AzurPilotTheme { AppRootScaffold {
                OobeStepWelcome(Modifier.fillMaxSize(), { next++ }, text(R.string.oobe_action_next),
                    true, null, text(R.string.dialog_cancel))
            } } }
            val button = node(R.string.oobe_action_next).assertIsNotEnabled()
            button.performTouchInput { click() }
            assertEquals(0, next)
            compose.mainClock.advanceTimeBy(2500)
            button.assertIsDisplayed().assertIsEnabled()
            val pixels = button.captureToImage().toPixelMap()
            var darkest = 1f
            var lightest = 0f
            // 只看圆形按钮内部，避免把边缘与动态背景的差异误判为箭头。
            for (y in pixels.height / 4 until pixels.height * 3 / 4) {
                for (x in pixels.width / 4 until pixels.width * 3 / 4) {
                    val color = pixels[x, y]
                    val brightness = (color.red + color.green + color.blue) / 3f
                    darkest = minOf(darkest, brightness)
                    lightest = maxOf(lightest, brightness)
                }
            }
            assertTrue("The entered button must contain a visible arrow",
                lightest - darkest > .3f)
            button.performTouchInput { click() }
            compose.runOnIdle { assertEquals(1, next) }
        } finally {
            compose.mainClock.autoAdvance = true
        }
    }

    @Test
    fun welcomeSpringMatches101SamplesFromTheOriginalOs3Framework() {
        assertEquals(497, OobeWelcomeRevealDuration)
        val rows = InstrumentationRegistry.getInstrumentation().context.assets
            .open("oobe/os3-welcome-spring.csv").bufferedReader().use { reader ->
                reader.readLines().filterNot { it.startsWith("#") || it.isBlank() }
            }
        assertEquals(101, rows.size)
        for (row in rows) {
            val (fraction, expected) = row.split(',').map(String::toFloat)
            assertEquals("Native OS3 interpolation at $fraction", expected,
                OobeWelcomeEasing.transform(fraction), .000001f)
        }
    }

    @Test
    fun welcomeGeometryMatchesOriginalSystemUiInBothDirections() {
        val rows = InstrumentationRegistry.getInstrumentation().context.assets
            .open("oobe/os3-welcome-geometry.csv").bufferedReader().use { reader ->
                reader.readLines().filterNot { it.startsWith("#") || it.isBlank() }
            }
        assertEquals(22, rows.size)
        for (row in rows) {
            val cells = row.split(',')
            val closing = cells[0].toBoolean()
            val fraction = cells[1].toFloat()
            val frame = oobeWelcomeFrame(androidx.compose.ui.geometry.Rect(435f, 1746f, 645f, 1956f),
                androidx.compose.ui.geometry.Size(1080f, 2400f),
                if (closing) 1f - fraction else fraction, closing)
            assertEquals("Native OS3 x at $row", cells[2].toFloat(), frame.bounds.left, .001f)
            assertEquals("Native OS3 y at $row", cells[3].toFloat(), frame.bounds.top, .001f)
            assertEquals("Native OS3 scale at $row", cells[4].toFloat(), frame.scale, .000001f)
            assertEquals(cells[5].toFloat(), frame.crop.width, 0f)
            assertEquals(cells[6].toFloat(), frame.crop.height, 0f)
            val outline = frame.createOutline(androidx.compose.ui.geometry.Size(1080f, 2400f),
                androidx.compose.ui.unit.LayoutDirection.Ltr, androidx.compose.ui.unit.Density(1f))
                as androidx.compose.ui.graphics.Outline.Rounded
            assertEquals("Native OS3 physical radius at $row", cells[7].toFloat(),
                outline.roundRect.topLeftCornerRadius.x * frame.scale, .001f)
        }
    }

    @Test
    fun welcomeLaunchExpandsInPlaceAndReturnsToTheSameButton() {
        runBlocking { manager.setAppearance { it.copy(uiMode = "miuix", colorMode = "1") } }
        compose.mainClock.autoAdvance = false
        try {
            compose.setContent { AzurPilotTheme { AppRootScaffold {
                OobeScreen(true, {})
            } } }
            compose.mainClock.advanceTimeBy(2500)
            screenshot("welcome_launch_000")
            node(R.string.oobe_action_next).assertIsEnabled().performClick()
            compose.mainClock.advanceTimeBy(64)
            // 布局尺寸不含 graphicsLayer 的缩放，必须比较变换后的实际坐标。
            val title = node(R.string.oobe_pref_lang_title).fetchSemanticsNode().layoutInfo.coordinates
            val root = compose.onRoot().fetchSemanticsNode().layoutInfo.coordinates
            val start = title.localToRoot(androidx.compose.ui.geometry.Offset.Zero)
            val end = title.localToRoot(androidx.compose.ui.geometry.Offset(title.size.width.toFloat(), 0f))
            val center = root.localToRoot(androidx.compose.ui.geometry.Offset(root.size.width * .5f, 0f))
            assertEquals("Language content must remain centered inside the scaled surface",
                center.x, (start.x + end.x) * .5f, 1f)
            screenshot("welcome_launch_064")
            for (time in listOf(160L, 320L, 640L)) {
                compose.mainClock.advanceTimeBy(if (time == 160L) 96L else time / 2)
                screenshot("welcome_launch_$time")
            }
            node(R.string.oobe_pref_lang_title).assertIsDisplayed()
            node(R.string.oobe_action_prev).performClick()
            compose.mainClock.advanceTimeBy(700)
            node(R.string.oobe_action_next).assertIsDisplayed().assertIsEnabled()
        } finally {
            compose.mainClock.autoAdvance = true
        }
    }

    @Test
    fun previewBlocksBackgroundActionsAndKeepsNavigationClickable() {
        var backgroundActions = 0
        compose.setContent { AzurPilotTheme { AppRootScaffold {
            androidx.compose.foundation.layout.Box(Modifier.fillMaxSize()) {
                com.azurpilot.ghio.ui.ksu.AppButton({ backgroundActions++ }, Modifier.fillMaxSize()) {
                    com.azurpilot.ghio.ui.ksu.AppText("background action")
                }
                OobeScreen(true, {}, onDismiss = {})
            }
        } } }
        for (theme in listOf("material", "miuix")) {
            runBlocking { manager.setAppearance { it.copy(uiMode = theme) } }
            compose.waitForIdle()
            compose.onRoot().performTouchInput { click(androidx.compose.ui.geometry.Offset(width * .9f, height * .1f)) }
            assertEquals(0, backgroundActions)
            node(R.string.oobe_action_next).performClick()
            node(R.string.oobe_pref_lang_title).assertIsDisplayed()
            node(R.string.oobe_action_prev).performClick()
        }
    }

    @OptIn(androidx.compose.ui.test.ExperimentalTestApi::class)
    @Test
    fun transferIconsMatchOriginalLottieRendering() {
        val resolver = context.contentResolver
        val originalScale = android.provider.Settings.Global.getString(resolver,
            android.provider.Settings.Global.ANIMATOR_DURATION_SCALE)
        fun shell(command: String) = android.os.ParcelFileDescriptor.AutoCloseInputStream(
            InstrumentationRegistry.getInstrumentation().uiAutomation.executeShellCommand(command),
        ).use { it.bufferedReader().readText() }
        val assets = listOf(
            com.azurpilot.ghio.ui.components.TransferStage.DOWNLOAD to R.raw.transfer_download,
            com.azurpilot.ghio.ui.components.TransferStage.EXTRACT to R.raw.transfer_extract,
            com.azurpilot.ghio.ui.components.TransferStage.VERIFY to R.raw.transfer_verify,
            com.azurpilot.ghio.ui.components.TransferStage.APPLY to R.raw.transfer_apply,
        )
        val stage = mutableStateOf(assets.first())
        try {
            shell("settings put global animator_duration_scale 0")
            compose.setContent { AzurPilotTheme { AppRootScaffold {
                val selected = stage.value
                val composition = androidx.compose.runtime.remember(selected) {
                    checkNotNull(com.airbnb.lottie.LottieCompositionFactory.fromRawResSync(context, selected.second).value)
                }
                val tint = androidx.compose.ui.graphics.Color(0xFF3388FF)
                val properties = com.airbnb.lottie.compose.rememberLottieDynamicProperties(
                    com.airbnb.lottie.compose.rememberLottieDynamicProperty(com.airbnb.lottie.LottieProperty.STROKE_COLOR, 0xFF3388FF.toInt(), "**"),
                    com.airbnb.lottie.compose.rememberLottieDynamicProperty(com.airbnb.lottie.LottieProperty.COLOR, 0xFF3388FF.toInt(), "**"),
                )
                androidx.compose.foundation.layout.Row {
                    com.azurpilot.ghio.ui.components.AnimatedTransferIcon(selected.first,
                        Modifier.testTag("transfer-actual"), tint)
                    com.airbnb.lottie.compose.LottieAnimation(composition, progress = { 0f },
                        dynamicProperties = properties, modifier = Modifier.size(48.dp).testTag("transfer-original"))
                }
            } } }
            for (theme in listOf("material", "miuix")) {
                runBlocking { manager.setAppearance { it.copy(uiMode = theme, colorMode = "1") } }
                for (asset in assets) {
                    compose.runOnIdle { stage.value = asset }
                    compose.waitUntil(5000) {
                        runCatching {
                            val actual = compose.onNodeWithTag("transfer-actual").captureToImage().toPixelMap()
                            val original = compose.onNodeWithTag("transfer-original").captureToImage().toPixelMap()
                            if (actual.width != original.width || actual.height != original.height) false else {
                                var difference = 0f
                                for (y in 0 until actual.height) for (x in 0 until actual.width) {
                                    difference += kotlin.math.abs(actual[x, y].red - original[x, y].red) +
                                        kotlin.math.abs(actual[x, y].green - original[x, y].green) +
                                        kotlin.math.abs(actual[x, y].blue - original[x, y].blue)
                                }
                                difference / (actual.width * actual.height * 3) < .001f
                            }
                        }.getOrDefault(false)
                    }
                    screenshot("transfer_${theme}_${asset.first.name.lowercase()}")
                }
            }
        } finally {
            shell(if (originalScale == null) "settings delete global animator_duration_scale"
                else "settings put global animator_duration_scale ${originalScale.toFloat()}")
        }
    }

    private fun text(resource: Int): String {
        val tag = AppLocales.currentTag() ?: return context.getString(resource)
        val config = Configuration(context.resources.configuration)
        config.setLocales(LocaleList.forLanguageTags(tag))
        return context.createConfigurationContext(config).getString(resource)
    }
    private fun node(resource: Int) = compose.onNode(hasText(text(resource)) or hasContentDescription(text(resource)))
    private fun forward() {
        val skip = node(R.string.oobe_action_skip)
        if (compose.onAllNodes(hasText(text(R.string.oobe_action_skip)) or hasContentDescription(text(R.string.oobe_action_skip)))
                .fetchSemanticsNodes().isNotEmpty()) skip.performClick()
        else node(R.string.oobe_action_next).performClick()
        compose.waitForIdle()
        android.os.SystemClock.sleep(250)
    }

    private fun screenshot(name: String) {
        if (name.endsWith("_welcome")) android.os.SystemClock.sleep(2000)
        compose.waitForIdle()
        val output = File(context.getExternalFilesDir("oobe-tests"), "$name.png")
        output.parentFile?.mkdirs()
        InstrumentationRegistry.getInstrumentation().uiAutomation.takeScreenshot()!!.let { bitmap ->
            output.outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            bitmap.recycle()
        }
    }
}
