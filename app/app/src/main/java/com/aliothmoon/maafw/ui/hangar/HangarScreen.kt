package com.aliothmoon.maafw.ui.hangar

import androidx.compose.foundation.background
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ArrowDropDown
import androidx.compose.material.icons.outlined.OndemandVideo
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalMinimumInteractiveComponentSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.aliothmoon.maafw.R
import com.aliothmoon.maafw.constant.DefaultDisplayConfig
import com.aliothmoon.maafw.proot.AlasRunController
import com.aliothmoon.maafw.proot.AlasRunState
import com.aliothmoon.maafw.service.HostState
import com.aliothmoon.maafw.theme.MaaDesignTokens
import com.aliothmoon.maafw.theme.MaaTheme
import com.aliothmoon.maafw.ui.components.AlasControlPanel
import com.aliothmoon.maafw.ui.components.ToolSlotButton
import com.aliothmoon.maafw.ui.components.MaaCard
import com.aliothmoon.maafw.ui.components.MaaOutlinedButton
import com.aliothmoon.maafw.ui.components.maaClickable
import org.koin.compose.koinInject

/**
 * 挂机 tab：虚拟屏实时画面 + 运行配置选择 + ALAS 控制面板
 *
 * 画面走 native bridge_preview 通道（AIDL setMonitorSurface，零拷贝）；
 * 预览面本体是 AppRoot 持有的 movableContent（见 HangarPreview.kt），点卡片进全屏时搬走，
 * 页面不可见即摘面，不给看不到的画面白烧帧。
 * 配置面（改任务参数）仍在 ALAS WebUI tab，本页只选「跑哪个配置」
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HangarScreen(
    active: Boolean,
    previewContent: (@Composable () -> Unit)?,
    onEnterFullscreen: () -> Unit,
    modifier: Modifier = Modifier,
    hostState: HostState = koinInject(),
    alasController: AlasRunController = koinInject(),
) {
    val snapshot by hostState.snapshot.collectAsStateWithLifecycle()
    val alas by alasController.state.collectAsStateWithLifecycle()
    val configuration = LocalConfiguration.current
    val landscape = configuration.screenWidthDp > configuration.screenHeightDp

    // 本页可见且特权连接就绪时自动补一次「开始」链路建虚拟屏（HostState 内幂等）
    LaunchedEffect(active, snapshot.privilegedConnected) {
        if (active && snapshot.privilegedConnected) {
            hostState.ensureEnvironmentStarted()
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        if (!landscape) TopAppBar(
            title = {
                Text(
                    text = stringResource(R.string.nav_hangar),
                    style = MaterialTheme.typography.headlineMedium,
                    fontWeight = FontWeight.SemiBold,
                )
            },
            // AppRoot 的 Scaffold 已吃掉状态栏顶部 inset，这里不能再加一次
            windowInsets = WindowInsets(0, 0, 0, 0),
            colors = TopAppBarDefaults.topAppBarColors(
                containerColor = MaterialTheme.colorScheme.background,
                titleContentColor = MaterialTheme.colorScheme.onBackground,
            ),
        )
        if (landscape) {
            Row(
                modifier = Modifier.weight(1f).fillMaxWidth().padding(MaaDesignTokens.Spacing.md),
                horizontalArrangement = Arrangement.spacedBy(MaaDesignTokens.Spacing.lg),
            ) {
                Column(
                    modifier = Modifier.weight(1f).fillMaxHeight(),
                    verticalArrangement = Arrangement.spacedBy(MaaDesignTokens.Spacing.sm),
                ) {
                    VdPreview(
                        envUp = snapshot.vdDisplayId != DefaultDisplayConfig.DISPLAY_NONE,
                        content = previewContent,
                        onEnterFullscreen = onEnterFullscreen,
                        modifier = Modifier.weight(1f).fillMaxWidth(),
                        maintainAspectRatio = false,
                    )
                    ConfigToolRow(
                        alas = alas,
                        onSelect = alasController::selectConfig,
                        onToolStart = { alasController.startTool(it) },
                        onToolStop = { alasController.stopTool() },
                    )
                }
                AlasControlPanel(
                    snapshot = snapshot,
                    alas = alas,
                    onAlasStart = { alasController.startAlas() },
                    onAlasStop = { alasController.stopAlas() },
                    onToolStart = { alasController.startTool(it) },
                    onToolStop = { alasController.stopTool() },
                    modifier = Modifier.weight(1f).fillMaxHeight(),
                    showTools = false,
                )
            }
        } else Column(
            modifier = Modifier
                .weight(1f)
                .fillMaxWidth()
                .padding(
                    start = MaaDesignTokens.Spacing.lg,
                    end = MaaDesignTokens.Spacing.lg,
                    bottom = MaaDesignTokens.Spacing.md,
                ),
            verticalArrangement = Arrangement.spacedBy(MaaDesignTokens.Spacing.md),
        ) {
            VdPreview(
                // 判据只看虚拟屏存在性：桥 ping 抖动不拆预览（预览不走桥 TCP，
                // 桥挂了画面照渲染）；屏真没了（特权断线快照清零）才给权限提示
                envUp = snapshot.vdDisplayId != DefaultDisplayConfig.DISPLAY_NONE,
                content = previewContent,
                onEnterFullscreen = onEnterFullscreen,
                modifier = Modifier.fillMaxWidth(),
            )
            ConfigToolRow(
                alas = alas,
                onSelect = alasController::selectConfig,
                onToolStart = { alasController.startTool(it) },
                onToolStop = { alasController.stopTool() },
            )
            AlasControlPanel(
                snapshot = snapshot,
                alas = alas,
                onAlasStart = { alasController.startAlas() },
                onAlasStop = { alasController.stopAlas() },
                onToolStart = { alasController.startTool(it) },
                onToolStop = { alasController.stopTool() },
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth(),
                showTools = false,
            )
        }
    }
}

/**
 * 虚拟屏画面卡：环境在跑显示实时预览，没跑给权限提示
 *
 * [envUp] 的判据是虚拟屏存在性（vdDisplayId），不含桥 ping——分支一切换
 * content 就离开 composition、SurfaceView 整体销毁重建，桥探测抖动会把
 * 亚秒级的重挂放大成秒级黑屏 + 权限提示闪现
 *
 * 内嵌画面只响应「单击进全屏」，不转发触摸（防误触）；
 * [content] 为 null 表示画面已搬去全屏宿主，显示占位
 */
@Composable
private fun VdPreview(
    envUp: Boolean,
    content: (@Composable () -> Unit)?,
    onEnterFullscreen: () -> Unit,
    modifier: Modifier = Modifier,
    maintainAspectRatio: Boolean = true,
) {
    Box(
        modifier = modifier
            .then(if (maintainAspectRatio) {
                Modifier.aspectRatio(DefaultDisplayConfig.WIDTH.toFloat() / DefaultDisplayConfig.HEIGHT)
            } else Modifier)
            .clip(MaterialTheme.shapes.medium)
            .background(Color.Black),
        contentAlignment = Alignment.Center,
    ) {
        when {
            envUp && content != null -> {
                Box(
                    modifier = Modifier
                        .fillMaxSize()
                        .maaClickable(onClick = onEnterFullscreen),
                ) {
                    content()
                }
            }

            envUp -> PreviewPlaceholder(
                textRes = R.string.hangar_preview_moved,
            )

            else -> PreviewPlaceholder(textRes = R.string.hangar_permission_required)
        }
    }
}

@Composable
private fun PreviewPlaceholder(textRes: Int) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(MaaDesignTokens.Spacing.sm),
    ) {
        Icon(
            imageVector = Icons.Outlined.OndemandVideo,
            contentDescription = null,
            tint = Color.White.copy(alpha = 0.7f),
            modifier = Modifier.size(MaaDesignTokens.IconSize.lg),
        )
        Text(
            text = stringResource(textRes),
            style = MaterialTheme.typography.bodySmall,
            color = Color.White.copy(alpha = 0.7f),
        )
    }
}

/**
 * 运行配置 + 工具 双模块行：左卡上「运行配置」标签、下配置下拉框；
 * 右列「半自动点击」「活动剧情」两个按钮上下排，与开始挂机同款实心形制。
 * 行高取左右最大固有高（IntrinsicSize），右列两按钮均分填满，大小随模块自适应。
 * 某工具在跑时对应槽位变「停止」（槽位即归属）；启停互斥归 wrapper，可用性只看可达/忙碌。
 * 调度器在跑时锁配置切换——生效配置以 /status 回报的 runningConfig 为准，选择下次启动生效
 */
@Composable
private fun ConfigToolRow(
    alas: AlasRunState,
    onSelect: (String) -> Unit,
    onToolStart: (String) -> Unit,
    onToolStop: () -> Unit,
) {
    var expanded by remember { mutableStateOf(false) }
    // 弹层宽度对齐锚按钮：DropdownMenu 默认按内容包宽，量出按钮宽显式喂给它
    var menuWidthPx by remember { mutableIntStateOf(0) }
    val density = LocalDensity.current
    val configSelectionEnabled = !alas.runnerAlive && alas.configs.isNotEmpty()
    // 模块整体压高 ~30%：关掉 M3 的 48dp 最小交互尺寸强制（本行按钮/下拉显式给矮高）
    CompositionLocalProvider(LocalMinimumInteractiveComponentSize provides 0.dp) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .height(IntrinsicSize.Max),
            horizontalArrangement = Arrangement.spacedBy(MaaDesignTokens.Spacing.md),
        ) {
        MaaCard(
            modifier = Modifier
                .weight(1f)
                .fillMaxHeight(),
            // 卡被行高拉伸后内容靠顶：上 padding 多给一档，标签下移、上下留白趋均
            contentPadding = PaddingValues(
                start = MaaDesignTokens.Card.innerPadding,
                top = MaaDesignTokens.Spacing.sm,
                end = MaaDesignTokens.Card.innerPadding,
                bottom = MaaDesignTokens.Spacing.xs,
            ),
        ) {
            Text(
                text = stringResource(R.string.hangar_config_label),
                style = MaterialTheme.typography.titleMedium,
            )
            Spacer(Modifier.height(MaaDesignTokens.Spacing.xxs))
            Box {
                MaaOutlinedButton(
                    onClick = { expanded = true },
                    enabled = configSelectionEnabled,
                    // This is a selector field, so use the same small radius as choice chips.
                    shape = if (MaaTheme.isMaterial) MaterialTheme.shapes.small
                        else RoundedCornerShape(MaaTheme.style.radii.button),
                    border = if (MaaTheme.isMaterial) BorderStroke(
                        1.dp,
                        MaterialTheme.colorScheme.outline.copy(alpha = if (configSelectionEnabled) 1f else 0.38f),
                    ) else ButtonDefaults.outlinedButtonBorder(configSelectionEnabled),
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(32.dp)
                        .onGloballyPositioned { menuWidthPx = it.size.width },
                    contentPadding = PaddingValues(horizontal = 16.dp, vertical = 2.dp),
                ) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(
                            text = if (alas.runnerAlive) {
                                alas.runningConfig ?: alas.selectedConfig
                            } else {
                                alas.selectedConfig
                            },
                        )
                        Icon(
                            imageVector = Icons.Outlined.ArrowDropDown,
                            contentDescription = null,
                            modifier = Modifier.size(MaaDesignTokens.IconSize.sm),
                        )
                    }
                }
                DropdownMenu(
                    expanded = expanded,
                    onDismissRequest = { expanded = false },
                    modifier = Modifier.width(with(density) { menuWidthPx.toDp() }),
                ) {
                    alas.configs.forEach { name ->
                        DropdownMenuItem(
                            text = { Text(name) },
                            onClick = {
                                onSelect(name)
                                expanded = false
                            },
                        )
                    }
                }
            }
        }
        Column(
            modifier = Modifier
                .weight(1f)
                .fillMaxHeight(),
            verticalArrangement = Arrangement.spacedBy(MaaDesignTokens.Spacing.xs),
        ) {
            ToolSlotButton(
                labelRes = R.string.hangar_tool_semi_auto,
                running = alas.toolAlive && alas.toolName == AlasRunState.TOOL_SEMI_AUTO,
                onStart = { onToolStart(AlasRunState.TOOL_SEMI_AUTO) },
                onStop = onToolStop,
                enabled = alas.reachable && !alas.busy,
                modifier = Modifier.weight(1f),
            )
            ToolSlotButton(
                labelRes = R.string.hangar_tool_event_story,
                running = alas.toolAlive && alas.toolName == AlasRunState.TOOL_EVENT_STORY,
                onStart = { onToolStart(AlasRunState.TOOL_EVENT_STORY) },
                onStop = onToolStop,
                enabled = alas.reachable && !alas.busy,
                modifier = Modifier.weight(1f),
            )
        }
    }
}
}
