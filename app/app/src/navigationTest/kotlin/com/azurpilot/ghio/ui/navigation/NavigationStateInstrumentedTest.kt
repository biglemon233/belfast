package com.azurpilot.ghio.ui.navigation

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.text.BasicText
import androidx.compose.material3.TextField
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.click
import androidx.compose.ui.test.junit4.StateRestorationTester
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performTouchInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.azurpilot.ghio.theme.AzurPilotTheme
import com.azurpilot.ghio.ui.ksu.component.FloatingBottomBarItem
import com.azurpilot.ghio.ui.motion.AppNavigation
import kotlinx.coroutines.flow.first
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * 验证真实导航宿主的状态恢复、参数保存及交互式返回取消和提交。
 *
 * Verifies state restoration, argument preservation, and interactive back cancellation and
 * completion through the real navigation host.
 */
@RunWith(AndroidJUnit4::class)
class NavigationStateInstrumentedTest {
    @get:Rule val compose = createComposeRule()

    /**
     * 验证真实悬浮底栏在旧 Android 上可绘制和点击。 / Verifies floating navigation rendering and selection on older
     * Android.
     */
    /** 切换两套主题时保留详情和输入草稿。 / Theme switching preserves the current detail and input draft. */
    @Test
    fun themeSwitchPreservesCurrentEntryAndDraft() {
        val manager =
            org.koin.core.context.GlobalContext.get()
                .get<com.azurpilot.ghio.settings.AppSettingsManager>()
        kotlinx.coroutines.runBlocking {
            kotlinx.coroutines.withTimeout(10000) { manager.loaded.first { it } }
        }
        val originalMode = manager.appearance.value.uiMode
        val nextMode = if (originalMode == "miuix") "material" else "miuix"
        lateinit var navigator: AppNavigator
        compose.setContent {
            AzurPilotTheme {
                navigator = rememberAppNavigator()
                com.azurpilot.ghio.ui.ksu.AppRootScaffold {
                    AppNavigation(navigator) { swipe ->
                        entry<AppRoute.Main> { BasicText("Home") }
                        entry<AppRoute.Display>(swipeDismiss = swipe) {
                            var draft by rememberSaveable { mutableStateOf("") }
                            Column {
                                BasicText(
                                    com.azurpilot.ghio.theme.ksu.LocalUiMode.current.value,
                                    Modifier.testTag("mode"),
                                )
                                com.azurpilot.ghio.ui.ksu.AppTextField(
                                    draft,
                                    { draft = it },
                                    Modifier.testTag("themeDraft"),
                                )
                            }
                        }
                    }
                }
            }
        }
        try {
            compose.runOnIdle { navigator.push(AppRoute.Display) }
            compose
                .onNode(androidx.compose.ui.test.hasSetTextAction())
                .performTextInput("Theme draft")
            kotlinx.coroutines.runBlocking { manager.setAppearance { it.copy(uiMode = nextMode) } }
            compose.waitUntil(10000) { manager.appearance.value.uiMode == nextMode }
            compose.onNodeWithTag("mode").assertTextEquals(nextMode)
            compose.onNodeWithText("Theme draft").assertIsDisplayed()
            compose.runOnIdle { assertEquals(AppRoute.Display, navigator.current) }
        } finally {
            kotlinx.coroutines.runBlocking {
                manager.setAppearance { it.copy(uiMode = originalMode) }
            }
        }
    }

    @Test
    fun floatingNavigationRendersAndSelectsWithoutRuntimeShader() {
        var selected = 0
        compose.setContent {
            AzurPilotTheme {
                top.yukonga.miuix.kmp.theme.MiuixTheme {
                    val backdrop =
                        top.yukonga.miuix.kmp.blur.rememberLayerBackdrop { drawContent() }
                    com.azurpilot.ghio.ui.ksu.component.FloatingBottomBar(
                        selectedIndex = selected,
                        onSelected = { selected = it },
                        backdrop = backdrop,
                        tabsCount = 4,
                        isBlurEnabled = true,
                    ) { activate ->
                        repeat(4) { index ->
                            FloatingBottomBarItem(
                                selected = selected == index,
                                onClick = { activate(index) },
                            ) {
                                BasicText("Tab $index")
                            }
                        }
                    }
                }
            }
        }
        compose.onNodeWithText("Tab 2").assertIsDisplayed().performTouchInput { click() }
        compose.runOnIdle { assertEquals(2, selected) }
    }

    @Test
    fun restoresTaskArgumentsAndParentDraft() {
        lateinit var navigator: AppNavigator
        val restoration = StateRestorationTester(compose)
        val taskName = "舰队/配置 ?mode=1#任务"
        restoration.setContent {
            navigator = rememberAppNavigator()
            AzurPilotTheme {
                AppNavigation(navigator) { swipe ->
                    entry<AppRoute.Main> {
                        var draft by rememberSaveable { mutableStateOf("") }
                        Column {
                            BasicText("Home")
                            TextField(
                                value = draft,
                                onValueChange = { draft = it },
                                modifier = Modifier.testTag("draft"),
                            )
                        }
                    }
                    entry<AppRoute.Task>(swipeDismiss = swipe) { BasicText(it.taskName) }
                }
            }
        }
        compose.onNodeWithTag("draft").performTextInput("Saved input")
        compose.runOnIdle { navigator.push(AppRoute.Task(taskName)) }
        compose.onNodeWithText(taskName).assertIsDisplayed()
        restoration.emulateSavedInstanceStateRestore()
        compose.onNodeWithText(taskName).assertIsDisplayed()
        compose.runOnIdle { navigator.pop() }
        compose.onNodeWithTag("draft").assertTextEquals("Saved input")
    }

    @Test
    fun swipeReturnCanCancelThenCommitWithoutDuplicatingEntries() {
        lateinit var navigator: AppNavigator
        compose.setContent {
            navigator = rememberAppNavigator()
            AzurPilotTheme {
                AppNavigation(navigator) { swipe ->
                    entry<AppRoute.Main> { BasicText("Home") }
                    entry<AppRoute.Display>(swipeDismiss = swipe) {
                        Box(Modifier.fillMaxSize().testTag("detail")) { BasicText("Detail") }
                    }
                }
            }
        }
        compose.runOnIdle {
            repeat(5) { navigator.push(AppRoute.Display) }
            assertEquals(2, navigator.backStack.size)
        }
        compose.onNodeWithTag("detail").performTouchInput {
            down(Offset(width * 0.1f, height * 0.5f))
            moveTo(Offset(width * 0.15f, height * 0.5f), delayMillis = 100)
            moveTo(Offset(width * 0.2f, height * 0.5f), delayMillis = 100)
            advanceEventTime(300)
            up()
        }
        compose.onNodeWithText("Detail").assertIsDisplayed()
        compose.runOnIdle { assertEquals(AppRoute.Display, navigator.current) }
        compose.onNodeWithTag("detail").performTouchInput {
            down(Offset(width * 0.1f, height * 0.5f))
            moveTo(Offset(width * 0.5f, height * 0.5f), delayMillis = 150)
            moveTo(Offset(width * 0.9f, height * 0.5f), delayMillis = 150)
            advanceEventTime(100)
            up()
        }
        compose.onNodeWithText("Home").assertIsDisplayed()
        compose.runOnIdle {
            assertEquals(1, navigator.backStack.size)
            navigator.pop()
            assertEquals(AppRoute.Main, navigator.current)
        }
    }
}
