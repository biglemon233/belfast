package com.azurpilot.ghio.i18n

import androidx.annotation.StringRes

/**
 * 纯 JVM 单测拿不到 Context，解析不出文案，只能比资源 id 与参数
 *
 * [args] 省略即只比 id（「出没出这条诊断」），给了就连参数一起比（「诊断指向哪个对象」）
 *
 * Pure-JVM unit tests have no Context and cannot resolve text, so only the
 * resource id and the args are compared.
 *
 * Omitting [args] compares the id only ("did this diagnostic appear"); passing
 * it also compares the args ("which object the diagnostic points at").
 */
fun UiText?.isResource(@StringRes resId: Int, vararg args: Any?): Boolean {
    val resource = this as? UiText.Resource ?: return false
    if (resource.resId != resId) return false
    if (args.isEmpty()) return true
    return resource.args == args.toList()
}

/**
 * 断言失败时能看出实际是哪条资源，比 `assertTrue(false)` 有用
 *
 * Shows which resource a [UiText] actually holds when an assertion fails —
 * more useful than `assertTrue(false)`.
 */
fun UiText?.describe(): String = when (this) {
    null -> "null"
    UiText.Empty -> "Empty"
    is UiText.Verbatim -> "Verbatim($value)"
    is UiText.Resource -> "Resource(id=$resId, args=$args)"
    is UiText.Plural -> "Plural(id=$resId, count=$count, args=$args)"
    is UiText.Joined -> "Joined(${parts.joinToString { it.describe() }})"
}

/**
 * [isResource] 的复数版；[args] 省略即只比 id 与选形数
 *
 * The plural counterpart of [isResource]; omitting [args] compares only the id
 * and the plural count.
 */
fun UiText?.isPlural(resId: Int, count: Int, vararg args: Any?): Boolean {
    val plural = this as? UiText.Plural ?: return false
    if (plural.resId != resId || plural.count != count) return false
    if (args.isEmpty()) return true
    return plural.args == args.toList()
}
