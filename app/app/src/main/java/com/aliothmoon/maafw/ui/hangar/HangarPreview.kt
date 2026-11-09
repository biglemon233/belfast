package com.aliothmoon.maafw.ui.hangar

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.pm.ActivityInfo
import android.graphics.PixelFormat
import android.os.Build
import android.os.SystemClock
import android.view.Surface
import android.view.SurfaceHolder
import android.view.SurfaceView
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.systemGestures
import androidx.compose.foundation.layout.mandatorySystemGestures
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.movableContentOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInWindow
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import com.aliothmoon.maafw.R
import com.aliothmoon.maafw.constant.DefaultDisplayConfig
import com.aliothmoon.maafw.theme.MaaDesignTokens
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlin.math.roundToInt

/**
 * 全屏预览 + 触摸转发链（移植自 m0 TasksPreviewSection，分辨率从会话态简化为
 * DefaultDisplayConfig 常量——虚拟屏 1280×720 在 ALAS 桥配置里钉死）
 *
 * 三段链：
 * - [rememberMovablePreview]：预览面做成 movableContent，在内嵌卡片与全屏宿主之间搬家
 * - [FullscreenPreview]：全屏宿主（挂 AppRoot 顶层），隐藏系统栏、强制横屏、黑底 contain、
 *   可拖动返回按钮、BackHandler 退出、触摸 Down/Move/Up 换算后注入虚拟屏
 * - 内嵌卡片（HangarScreen.VdPreview）：单击进全屏，内嵌画面本身不转发触摸（防误触）
 */

/**
 * `setFixedSize` 必须延后一拍（m0 MaaPreviewSurface 同款处理）：
 * surfaceCreated 里同步调会被 attach 流程吞掉，画面按错误比例贴上来
 */
private const val FIXED_SIZE_DELAY_MS = 50L

/**
 * 预览面做成 movableContent：在内嵌卡片与全屏宿主之间搬家时复用同一份组合状态
 *
 * 「已挂上的 Surface」与「画面就绪」放在 movableContent **外层**：
 * SurfaceView 搬家时必然走一轮 detach/attach，surfaceDestroyed 与 surfaceCreated 成对触发，
 * 判重状态若放在里面会跟着一起搬，拿不准新旧 Surface 的对应关系（m0 同款布局）
 *
 * 闭包里读到的值必须走 rememberUpdatedState：movableContentOf 只创建一次，直接捕获会永远停在首帧
 *
 * [active] 是挂机 tab 可见性：tab 切走时 SurfaceView 多半还活着（pager 预组合），
 * surfaceChanged 不会再发，靠外层的 DisposableEffect 补一次摘/挂
 */
@Composable
fun rememberMovablePreview(
    active: Boolean,
    onSurfaceAvailable: (Surface) -> Unit,
    onSurfaceDestroyed: () -> Unit,
): @Composable () -> Unit {
    val currentActive by rememberUpdatedState(active)
    val currentAvailable by rememberUpdatedState(onSurfaceAvailable)
    val currentDestroyed by rememberUpdatedState(onSurfaceDestroyed)
    var attachedSurface by remember { mutableStateOf<Surface?>(null) }
    var surfaceReady by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    DisposableEffect(active) {
        if (active) {
            attachedSurface?.let { currentAvailable(it) }
        } else {
            currentDestroyed()
        }
        onDispose { }
    }

    return remember {
        movableContentOf {
            Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                // contain：画面按虚拟屏比例居中缩放，两侧/上下的黑边由外层底色兜住
                Box(
                    Modifier.aspectRatio(
                        DefaultDisplayConfig.WIDTH.toFloat() / DefaultDisplayConfig.HEIGHT
                    )
                ) {
                    AndroidView(
                        modifier = Modifier.fillMaxSize(),
                        factory = { context ->
                            SurfaceView(context).apply {
                                holder.setFormat(PixelFormat.RGBA_8888)
                                holder.addCallback(object : SurfaceHolder.Callback {
                                    override fun surfaceCreated(holder: SurfaceHolder) {
                                        scope.launch {
                                            delay(FIXED_SIZE_DELAY_MS)
                                            holder.setFixedSize(
                                                DefaultDisplayConfig.WIDTH,
                                                DefaultDisplayConfig.HEIGHT,
                                            )
                                        }
                                    }

                                    override fun surfaceChanged(
                                        holder: SurfaceHolder,
                                        format: Int,
                                        width: Int,
                                        height: Int,
                                    ) {
                                        // setFixedSize 是异步的：只在尺寸对齐虚拟屏后才上报，
                                        // 提前交出还是布局尺寸的 Surface 会按错误比例贴画面
                                        if (width == DefaultDisplayConfig.WIDTH &&
                                            height == DefaultDisplayConfig.HEIGHT
                                        ) {
                                            attachedSurface = holder.surface
                                            surfaceReady = true
                                            if (currentActive) {
                                                currentAvailable(holder.surface)
                                            }
                                        }
                                    }

                                    override fun surfaceDestroyed(holder: SurfaceHolder) {
                                        attachedSurface = null
                                        surfaceReady = false
                                        currentDestroyed()
                                    }
                                })
                            }
                        },
                    )
                    if (!surfaceReady) {
                        Column(
                            modifier = Modifier.align(Alignment.Center),
                            horizontalAlignment = Alignment.CenterHorizontally,
                            verticalArrangement = Arrangement.spacedBy(MaaDesignTokens.Spacing.sm),
                        ) {
                            CircularProgressIndicator(
                                modifier = Modifier.size(MaaDesignTokens.IconSize.md),
                                strokeWidth = MaaDesignTokens.Border.marker,
                                color = Color.LightGray,
                            )
                            Text(
                                text = stringResource(R.string.hangar_preview_waiting),
                                style = MaterialTheme.typography.bodySmall,
                                color = Color.LightGray,
                            )
                        }
                    }
                }
            }
        }
    }
}

/**
 * 全屏宿主；与内嵌卡片共用同一份 movableContent
 *
 * 必须挂在 AppRoot 顶层而不是 pager 里，否则盖不住底部 tab 栏。
 * 进出时接管系统栏与屏幕方向，退出时一律还原成进来前的值（m0 同款行为）
 */
@Composable
fun FullscreenPreview(
    onExit: () -> Unit,
    onTouch: (x: Int, y: Int, action: PreviewTouchAction) -> Unit,
    content: @Composable () -> Unit,
) {
    val activity = LocalContext.current.findActivity()

    DisposableEffect(activity) {
        val controller = activity?.window?.let {
            WindowCompat.getInsetsController(it, it.decorView)
        }
        controller?.hide(WindowInsetsCompat.Type.systemBars())
        controller?.systemBarsBehavior =
            WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        onDispose { controller?.show(WindowInsetsCompat.Type.systemBars()) }
    }

    // 虚拟屏是横的，竖着看只有中间一条；退出时还原用户原本的方向设置
    DisposableEffect(activity) {
        val original = activity?.requestedOrientation
        activity?.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
        onDispose { if (original != null) activity.requestedOrientation = original }
    }

    BackHandler(onBack = onExit)

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black),
        contentAlignment = Alignment.Center,
    ) {
        // Keep the button outside the game's pointer-input ancestry.
        Box(Modifier.fillMaxSize().previewTouchInput(onTouch)) {
            content()
        }
        PreviewExitButton(onExit)
    }
}

@Composable
private fun PreviewExitButton(onExit: () -> Unit) {
    val context = LocalContext.current
    val windowView = LocalView.current.rootView
    DisposableEffect(windowView) {
        val original = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q)
            windowView.systemGestureExclusionRects.toList() else emptyList()
        onDispose {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) windowView.systemGestureExclusionRects = original
        }
    }
    val preferences = remember(context) {
        context.getSharedPreferences("preview_controls", Context.MODE_PRIVATE)
    }
    var fraction by remember {
        mutableStateOf(Offset(
            preferences.getFloat("exit_x", 0f).coerceIn(0f, 1f),
            preferences.getFloat("exit_y", 0.3f).coerceIn(0f, 1f),
        ))
    }
    var dock by remember {
        mutableStateOf(runCatching {
            PreviewExitDock.valueOf(preferences.getString("exit_dock", "NONE") ?: "NONE")
        }.getOrDefault(PreviewExitDock.NONE))
    }
    var dragging by remember { mutableStateOf(false) }
    var pressed by remember { mutableStateOf(false) }
    var autoHideDock by remember { mutableStateOf(PreviewExitDock.NONE) }
    val currentExit by rememberUpdatedState(onExit)
    val description = stringResource(R.string.hangar_fullscreen_exit)
    val density = LocalDensity.current
    val layoutDirection = LocalLayoutDirection.current
    val gestures = WindowInsets.systemGestures
    val mandatoryGestures = WindowInsets.mandatorySystemGestures

    fun savePosition() {
        preferences.edit().putFloat("exit_x", fraction.x).putFloat("exit_y", fraction.y)
            .putString("exit_dock", dock.name).apply()
    }

    LaunchedEffect(dock, pressed, dragging, autoHideDock) {
        if (dock == PreviewExitDock.NONE && !pressed && !dragging && autoHideDock != PreviewExitDock.NONE) {
            delay(3_000L)
            dock = autoHideDock
            autoHideDock = PreviewExitDock.NONE
            savePosition()
        }
    }

    fun activate() {
        if (dock != PreviewExitDock.NONE) {
            autoHideDock = dock
            dock = PreviewExitDock.NONE
            savePosition()
        } else {
            currentExit()
        }
    }

    // Use the physical preview bounds: cutout/padding insets leave a visible gap at the edge.
    BoxWithConstraints(Modifier.fillMaxSize()) {
        val diameter = with(density) { 30.dp.toPx() }
        val range = with(density) {
            Offset((maxWidth - 30.dp).toPx().coerceAtLeast(0f),
                (maxHeight - 30.dp).toPx().coerceAtLeast(0f))
        }
        val threshold = with(density) { 32.dp.toPx() }
        val revealMargin = with(density) { 32.dp.toPx() }
        val safeStart = Offset(
            maxOf(revealMargin, gestures.getLeft(density, layoutDirection).toFloat(),
                mandatoryGestures.getLeft(density, layoutDirection).toFloat()).coerceAtMost(range.x / 2f),
            maxOf(revealMargin, gestures.getTop(density).toFloat(),
                mandatoryGestures.getTop(density).toFloat()).coerceAtMost(range.y / 2f),
        )
        val safeEnd = Offset(
            maxOf(revealMargin, gestures.getRight(density, layoutDirection).toFloat(),
                mandatoryGestures.getRight(density, layoutDirection).toFloat()).coerceAtMost(range.x / 2f),
            maxOf(revealMargin, gestures.getBottom(density).toFloat(),
                mandatoryGestures.getBottom(density).toFloat()).coerceAtMost(range.y / 2f),
        )
        val hidden = diameter / 2f
        Box(
            Modifier
                .offset {
                    val position = previewExitVisibleOffset(fraction, range, dock, hidden, safeStart, safeEnd)
                    IntOffset(position.x.roundToInt(), position.y.roundToInt())
                }
                .size(30.dp)
                .drawWithContent {
                    // Hide half at the touchable edge, not beneath an OEM gesture window.
                    clipRect(
                        left = if (dock == PreviewExitDock.LEFT) size.width / 2f else 0f,
                        top = if (dock == PreviewExitDock.TOP) size.height / 2f else 0f,
                        right = if (dock == PreviewExitDock.RIGHT) size.width / 2f else size.width,
                        bottom = if (dock == PreviewExitDock.BOTTOM) size.height / 2f else size.height,
                    ) { this@drawWithContent.drawContent() }
                }
                .onGloballyPositioned { coordinates ->
                    // Compose's clipped bounds can stop short of a cutout/gesture inset.
                    // Request the actual window position so edge drags reach the control.
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                        val origin = coordinates.positionInWindow()
                        windowView.systemGestureExclusionRects = listOf(android.graphics.Rect(
                            origin.x.roundToInt(), origin.y.roundToInt(),
                            (origin.x + coordinates.size.width).roundToInt(),
                            (origin.y + coordinates.size.height).roundToInt(),
                        ))
                    }
                }
                .clip(CircleShape)
                .background(Color.Black.copy(alpha = if (dragging) 0.55f else 0.18f))
                .border(1.dp, Color.White.copy(alpha = if (dragging) 0.8f else 0.25f), CircleShape)
                .semantics {
                    contentDescription = description
                    role = Role.Button
                    onClick { activate(); true }
                }
                .pointerInput(range, threshold, safeStart, safeEnd) {
                    awaitEachGesture {
                        val down = awaitFirstDown()
                        // Match the circular visible control, including when only a sliver is shown.
                        if (!previewExitHit(down.position, Offset(size.width.toFloat(), size.height.toFloat()), dock)) {
                            return@awaitEachGesture
                        }
                        down.consume()
                        pressed = true
                        // Reveal on press: the OS may cancel an edge gesture before its release.
                        // That same press must never also close the preview.
                        val revealedOnPress = dock != PreviewExitDock.NONE
                        if (revealedOnPress) {
                            autoHideDock = dock
                            dock = PreviewExitDock.NONE
                            savePosition()
                        }
                        var motion = Offset.Zero
                        var movingPosition = previewExitVisibleOffset(fraction, range, dock, hidden, safeStart, safeEnd)
                        // A thumb can drift across the narrow edge tab during a tap.
                        val dragSlop = if (revealedOnPress) maxOf(viewConfiguration.touchSlop, diameter / 2f)
                            else viewConfiguration.touchSlop
                        var released = false
                        try {
                            while (true) {
                                val event = awaitPointerEvent()
                                val change = event.changes.firstOrNull { it.id == down.id } ?: break
                                if (change.isConsumed) break
                                val delta = change.position - change.previousPosition
                                motion += delta
                                if (!dragging && motion.getDistance() > dragSlop) {
                                    dragging = true
                                    autoHideDock = PreviewExitDock.NONE
                                    dock = PreviewExitDock.NONE
                                    movingPosition += motion
                                } else if (dragging) {
                                    movingPosition += delta
                                }
                                if (dragging) {
                                    movingPosition = Offset(movingPosition.x.coerceIn(0f, range.x),
                                        movingPosition.y.coerceIn(0f, range.y))
                                    fraction = Offset(
                                        if (range.x > 0f) movingPosition.x / range.x else 0f,
                                        if (range.y > 0f) movingPosition.y / range.y else 0f,
                                    )
                                }
                                change.consume()
                                if (!change.pressed) {
                                    released = true
                                    break
                                }
                            }
                            if (released) {
                                if (dragging) {
                                    dock = previewExitDock(fraction, range, threshold)
                                    fraction = when (dock) {
                                        PreviewExitDock.LEFT -> fraction.copy(x = 0f)
                                        PreviewExitDock.RIGHT -> fraction.copy(x = 1f)
                                        PreviewExitDock.TOP -> fraction.copy(y = 0f)
                                        PreviewExitDock.BOTTOM -> fraction.copy(y = 1f)
                                        PreviewExitDock.NONE -> fraction
                                    }
                                    savePosition()
                                } else if (!revealedOnPress) {
                                    activate()
                                }
                            }
                        } finally {
                            dragging = false
                            pressed = false
                        }
                    }
                },
            contentAlignment = Alignment.Center,
        ) {
            Icon(Icons.Outlined.Close, contentDescription = null,
                tint = Color.White.copy(alpha = if (dragging) 1f else 0.5f),
                modifier = Modifier.size(16.dp))
        }
    }
}

internal enum class PreviewExitDock { NONE, LEFT, RIGHT, TOP, BOTTOM }

/** Only nearby edges dock. At a corner use the closest edge, so one half stays visible. */
internal fun previewExitDock(fraction: Offset, range: Offset, threshold: Float): PreviewExitDock {
    val distances = listOf(
        PreviewExitDock.LEFT to fraction.x * range.x,
        PreviewExitDock.RIGHT to (1f - fraction.x) * range.x,
        PreviewExitDock.TOP to fraction.y * range.y,
        PreviewExitDock.BOTTOM to (1f - fraction.y) * range.y,
    ).filter { (edge, _) ->
        if (edge == PreviewExitDock.LEFT || edge == PreviewExitDock.RIGHT) range.x > 0f else range.y > 0f
    }
    return distances.minByOrNull { it.second }?.takeIf { it.second <= threshold }?.first
        ?: PreviewExitDock.NONE
}

internal fun previewExitOffset(fraction: Offset, range: Offset, dock: PreviewExitDock, hidden: Float): Offset =
    Offset(fraction.x * range.x, fraction.y * range.y) + when (dock) {
        PreviewExitDock.LEFT -> Offset(-hidden, 0f)
        PreviewExitDock.RIGHT -> Offset(hidden, 0f)
        PreviewExitDock.TOP -> Offset(0f, -hidden)
        PreviewExitDock.BOTTOM -> Offset(0f, hidden)
        PreviewExitDock.NONE -> Offset.Zero
    }

/** Both expanded controls and the visible docked half must avoid OS-owned touch strips. */
internal fun previewExitVisibleOffset(
    fraction: Offset, range: Offset, dock: PreviewExitDock, hidden: Float,
    safeStart: Offset, safeEnd: Offset,
): Offset {
    val position = previewExitOffset(fraction, range, dock, hidden)
    val safePosition = Offset(
        position.x.coerceIn(safeStart.x, range.x - safeEnd.x),
        position.y.coerceIn(safeStart.y, range.y - safeEnd.y),
    )
    return when (dock) {
        PreviewExitDock.LEFT -> safePosition.copy(x = safeStart.x - hidden)
        PreviewExitDock.RIGHT -> safePosition.copy(x = range.x - safeEnd.x + hidden)
        PreviewExitDock.TOP -> safePosition.copy(y = safeStart.y - hidden)
        PreviewExitDock.BOTTOM -> safePosition.copy(y = range.y - safeEnd.y + hidden)
        PreviewExitDock.NONE -> safePosition
    }
}

internal fun previewExitHit(position: Offset, size: Offset, dock: PreviewExitDock): Boolean {
    val center = size / 2f
    if ((position - center).getDistance() > minOf(size.x, size.y) / 2f) return false
    return when (dock) {
        PreviewExitDock.LEFT -> position.x >= center.x
        PreviewExitDock.RIGHT -> position.x <= center.x
        PreviewExitDock.TOP -> position.y >= center.y
        PreviewExitDock.BOTTOM -> position.y <= center.y
        PreviewExitDock.NONE -> true
    }
}

/**
 * 把手指位置换算到虚拟屏坐标再上报（m0 previewTouchInput 同款）
 *
 * 画面按 contain 方式居中缩放，两侧/上下可能有黑边，落在黑边上的点直接丢掉——
 * 那里没有对应的虚拟屏像素，硬算会得到越界坐标
 */
private fun Modifier.previewTouchInput(
    onTouch: (x: Int, y: Int, action: PreviewTouchAction) -> Unit,
): Modifier = pointerInput(Unit) {
    awaitEachGesture {
        val down = awaitFirstDown(requireUnconsumed = false)
        val scale = minOf(
            size.width / DefaultDisplayConfig.WIDTH.toFloat(),
            size.height / DefaultDisplayConfig.HEIGHT.toFloat(),
        )
        if (scale <= 0f) return@awaitEachGesture
        val offsetX = (size.width - DefaultDisplayConfig.WIDTH * scale) / 2f
        val offsetY = (size.height - DefaultDisplayConfig.HEIGHT * scale) / 2f
        fun virtualPosition(position: Offset) = Offset(
            (position.x - offsetX) / scale,
            (position.y - offsetY) / scale,
        )
        val origin = virtualPosition(down.position)
        if (origin.x < 0 || origin.x >= DefaultDisplayConfig.WIDTH ||
            origin.y < 0 || origin.y >= DefaultDisplayConfig.HEIGHT
        ) return@awaitEachGesture

        val gesture = PreviewTouchGesture(origin, viewConfiguration.touchSlop / scale) { position, action ->
            onTouch(
                position.x.toInt().coerceIn(0, DefaultDisplayConfig.WIDTH - 1),
                position.y.toInt().coerceIn(0, DefaultDisplayConfig.HEIGHT - 1),
                action,
            )
        }
        down.consume()
        var waitingForHold = true
        try {
            while (true) {
                val event = if (waitingForHold) {
                    val remaining = down.uptimeMillis + viewConfiguration.longPressTimeoutMillis - SystemClock.uptimeMillis()
                    withTimeoutOrNull(remaining.coerceAtLeast(1)) { awaitPointerEvent() }
                } else awaitPointerEvent()
                if (event == null) {
                    gesture.hold()
                    waitingForHold = false
                    continue
                }
                val change = event.changes.firstOrNull { it.id == down.id } ?: break
                val position = virtualPosition(change.position)
                if (!change.pressed) {
                    if (position.x < 0 || position.x >= DefaultDisplayConfig.WIDTH ||
                        position.y < 0 || position.y >= DefaultDisplayConfig.HEIGHT
                    ) gesture.cancel() else gesture.up(position)
                    change.consume()
                    break
                }
                gesture.move(position)
                change.consume()
            }
        } finally {
            gesture.cancel()
        }
    }
}

/** Compose 的 LocalContext 可能是 ContextWrapper，逐层剥到 Activity */
private tailrec fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.findActivity()
    else -> null
}
