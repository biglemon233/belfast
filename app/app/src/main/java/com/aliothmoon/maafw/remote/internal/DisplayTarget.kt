package com.aliothmoon.maafw.remote.internal

import com.aliothmoon.maafw.constant.DisplayMode

/**
 * 桥侧采集/注入目标的唯一来源（桥与 RemoteServiceImpl 同进程）。
 *
 * BACKGROUND：虚拟屏承载游戏，注入目标 = 虚拟屏 id，帧尺寸钉 1280x720。
 * PRIMARY：主屏承载游戏（虚拟屏不被 ROM 放行时的全屏回退），采集 = 主屏镜像，
 *          注入目标 = display 0（InputControlUtils 对 0 有直通分支），帧尺寸随旋转走。
 *
 * 写入方：RemoteServiceImpl.setVirtualDisplayMode；读取方：BridgeServer。
 */
object DisplayTarget {

    @Volatile
    var mode: Int = DisplayMode.BACKGROUND

    /** 触摸注入目标 displayId；虚拟屏未起返回 [VirtualDisplayManager.DISPLAY_NONE] */
    fun injectDisplayId(): Int = VirtualDisplayManager.getDisplayId()

    /** 采集帧尺寸校验基准（width, height）；未就绪返回 null */
    fun captureSize(): Pair<Int, Int>? =
        VirtualDisplayManager.getConfig().let { it.width to it.height }

    /** 协议侧回报的模式名，供 ALAS 客户端决定启动语义 */
    fun modeName(): String = "BACKGROUND"
}
