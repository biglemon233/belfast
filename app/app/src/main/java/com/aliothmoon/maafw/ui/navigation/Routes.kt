package com.aliothmoon.maafw.ui.navigation

import android.net.Uri

/**
 * 二级页面路由
 *
 * 主 tab（Hangar/Alas/Settings）由 AppRoot 的 HorizontalPager 承载，
 * 不进 NavHost；NavHost 只承载推入式子页面，主 tab 路由仅作空占位
 */
object Routes {
    const val HANGAR = "hangar"
    const val ALAS = "alas"
    const val SETTINGS = "settings"

    /** 主 tab 路由集合，用来判断当前是否停在主界面 */
    val mainTabs: Set<String> = setOf(HANGAR, ALAS, SETTINGS)

    /** 启动器日志（`log/` 目录递归：app.log 系列 / session.log / crash） */
    const val APP_LOG = "app_log"

    /** 某一份启动器日志的正文；file 是相对 `log/` 目录的路径（含 `/`，必须 URL 编码） */
    const val APP_LOG_DETAIL = "app_log_detail/{file}"
    const val APP_LOG_DETAIL_ARG = "file"
    fun appLogDetail(path: String) = "app_log_detail/${Uri.encode(path)}"

    /** ALAS 日志（错误现场 + 按天日志两区） */
    const val ALAS_LOG = "alas_log"

    /** 某一份 ALAS 按天日志的正文；file 是 txt 文件名 */
    const val ALAS_LOG_DETAIL = "alas_log_detail/{file}"
    const val ALAS_LOG_DETAIL_ARG = "file"
    fun alasLogDetail(fileName: String) = "alas_log_detail/${Uri.encode(fileName)}"

    /** 一个 ALAS 错误现场（error/<毫秒时间戳>/）：log.txt + 截图 */
    const val ALAS_ERROR_DETAIL = "alas_error_detail/{dir}"
    const val ALAS_ERROR_DETAIL_ARG = "dir"
    fun alasErrorDetail(dirName: String) = "alas_error_detail/${Uri.encode(dirName)}"
}
