package com.aliothmoon.maafw.constant

/**
 * 采集与注入的目标屏：只有 BACKGROUND（虚拟屏）。
 * 前台/主屏 PRIMARY 模式已移除。
 * BACKGROUND 建虚拟屏：尺寸由 PI controller 的 display_* 推导，目标应用被拉到该屏上
 */
object DisplayMode {
    const val BACKGROUND = 2
}
