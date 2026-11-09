package com.aliothmoon.maafw.constant

/**
 * 虚拟显示器的参数：ALAS 桥配置（screencap 1280×720）钉死这套尺寸，
 * 改分辨率要连桥协议与 guest 侧校验一起动，故不做用户可选项
 */
object DefaultDisplayConfig {
    /** 建屏时的名字，只在 dumpsys 里可见 */
    const val VD_NAME = "MaaFwVirtualDisplay"

    const val DISPLAY_NONE = -1

    const val WIDTH = 1280
    const val HEIGHT = 720
    const val DPI = 220
}
