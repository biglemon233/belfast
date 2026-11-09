package com.aliothmoon.maafw.remote.internal

import android.accessibilityservice.AccessibilityServiceInfo
import android.app.UiAutomation
import android.graphics.Rect
import android.os.Bundle
import android.os.DeadObjectException
import android.os.HandlerThread
import android.os.Looper
import android.os.SystemClock
import android.util.SparseArray
import android.util.Xml
import android.view.accessibility.AccessibilityNodeInfo
import android.view.accessibility.AccessibilityWindowInfo
import com.aliothmoon.maafw.bridge.InputControlUtils
import org.json.JSONObject
import org.xmlpull.v1.XmlSerializer
import java.io.StringWriter
import java.lang.reflect.InvocationTargetException

/** UiAutomator2-compatible UI access over the privileged bridge; initialized lazily. */
object BridgeUiAutomation {
    private var automation: UiAutomation? = null
    private var handlerThread: HandlerThread? = null

    private fun connect(): UiAutomation {
        automation?.let { return it }
        val thread = HandlerThread("alasaos-ui-automation").apply { start() }
        var ui: UiAutomation? = null
        try {
            val connectionType = Class.forName("android.app.IUiAutomationConnection")
            val connection = Class.forName("android.app.UiAutomationConnection")
                .getDeclaredConstructor().newInstance()
            ui = UiAutomation::class.java.getConstructor(Looper::class.java, connectionType)
                .newInstance(thread.looper, connection)
            // Do not disable the user's accessibility services while using selectors.
            UiAutomation::class.java.getMethod("connect", Int::class.javaPrimitiveType)
                .invoke(ui, UiAutomation.FLAG_DONT_SUPPRESS_ACCESSIBILITY_SERVICES)
            val info = ui.serviceInfo
            info.flags = info.flags or AccessibilityServiceInfo.FLAG_RETRIEVE_INTERACTIVE_WINDOWS or
                AccessibilityServiceInfo.FLAG_INCLUDE_NOT_IMPORTANT_VIEWS or
                AccessibilityServiceInfo.FLAG_REPORT_VIEW_IDS
            ui.serviceInfo = info
            handlerThread = thread
            automation = ui
            return ui
        } catch (error: Exception) {
            runCatching { ui?.let { UiAutomation::class.java.getMethod("disconnect").invoke(it) } }
            thread.quitSafely()
            throw unwrap(error)
        }
    }

    private fun unwrap(error: Throwable): Throwable =
        if (error is InvocationTargetException && error.targetException != null) unwrap(error.targetException) else error

    internal fun isDisconnected(error: Throwable): Boolean {
        val cause = unwrap(error)
        return cause is DeadObjectException || (cause is IllegalStateException &&
            cause.message?.startsWith("UiAutomation not connected") == true)
    }

    @Synchronized
    fun close() {
        val ui = automation
        automation = null
        try {
            ui?.let { UiAutomation::class.java.getMethod("disconnect").invoke(it) }
        } finally {
            handlerThread?.quitSafely()
            handlerThread = null
        }
    }

    @Suppress("UNCHECKED_CAST")
    private fun windows(ui: UiAutomation): List<Pair<Int, AccessibilityWindowInfo>> {
        if (android.os.Build.VERSION.SDK_INT >= 30) {
            val displays = UiAutomation::class.java.getMethod("getWindowsOnAllDisplays")
                .invoke(ui) as SparseArray<List<AccessibilityWindowInfo>>
            return (0 until displays.size()).flatMap { index ->
                displays.valueAt(index).map { displays.keyAt(index) to it }
            }
        }
        return ui.windows.map { 0 to it }
    }

    @Synchronized
    fun handle(request: JSONObject): JSONObject {
        if (request.getString("action") == "reset") {
            close()
            return JSONObject().put("ok", true)
        }
        try {
            return handleConnected(request)
        } catch (error: Exception) {
            // Discard only our stale session. Never replay a click or text action.
            if (isDisconnected(error)) {
                runCatching { close() }.exceptionOrNull()?.let(error::addSuppressed)
            }
            throw unwrap(error)
        }
    }

    private fun handleConnected(request: JSONObject): JSONObject {
        val ui = connect()
        val windows = windows(ui)
        try {
            return when (val action = request.getString("action")) {
                "dump" -> {
                    val output = StringWriter()
                    val xml = Xml.newSerializer().apply { setOutput(output) }
                    xml.startDocument("UTF-8", true)
                    xml.startTag(null, "hierarchy")
                    windows.forEach { (display, window) ->
                        window.root?.let { root ->
                            try { dump(xml, root, display, window.id, "") }
                            finally { root.recycle() }
                        }
                    }
                    xml.endTag(null, "hierarchy")
                    xml.endDocument()
                    JSONObject().put("ok", true).put("xml", output.toString())
                }
                "click", "set_text" -> act(windows, request, action)
                else -> error("Unknown UI action: $action")
            }
        } finally {
            windows.forEach { (_, window) -> window.recycle() }
        }
    }

    private fun dump(xml: XmlSerializer, node: AccessibilityNodeInfo, display: Int, window: Int, path: String) {
        val bounds = Rect().also(node::getBoundsInScreen)
        // Match UiAutomator's visible hierarchy. Including off-screen toolbar
        // buttons would change XPath's last() result.
        if (!node.isVisibleToUser || bounds.isEmpty) return
        xml.startTag(null, "node")
        mapOf(
            "class" to node.className, "text" to node.text, "resource-id" to node.viewIdResourceName,
            "content-desc" to node.contentDescription, "package" to node.packageName,
            "clickable" to node.isClickable, "enabled" to node.isEnabled,
            "focused" to node.isFocused, "selected" to node.isSelected,
            "bounds" to "[${bounds.left},${bounds.top}][${bounds.right},${bounds.bottom}]",
            "display-id" to display, "window-id" to window, "node-path" to path
        ).forEach { (key, value) -> xml.attribute(null, key, value?.toString().orEmpty()) }
        for (index in 0 until node.childCount) {
            node.getChild(index)?.let { child ->
                try { dump(xml, child, display, window, if (path.isEmpty()) "$index" else "$path/$index") }
                finally { child.recycle() }
            }
        }
        xml.endTag(null, "node")
    }

    private fun act(windows: List<Pair<Int, AccessibilityWindowInfo>>, request: JSONObject, action: String): JSONObject {
        val (display, window) = windows.firstOrNull {
            it.first == request.getInt("display_id") && it.second.id == request.getInt("window_id")
        } ?: error("UI window disappeared; query the selector again")
        var node = window.root ?: error("UI window has no root")
        try {
            val path = request.getString("node_path")
            for (part in path.split('/').filter { it.isNotEmpty() }) {
                val child = node.getChild(part.toInt()) ?: error("UI node disappeared; query the selector again")
                node.recycle()
                node = child
            }
            // Refuse to act on a different node if the window changed after the query.
            check(node.viewIdResourceName.orEmpty() == request.getString("resource_id") &&
                node.className?.toString().orEmpty() == request.getString("class_name") &&
                node.text?.toString().orEmpty() == request.getString("text")) { "UI node changed; query the selector again" }
            check(node.isEnabled && node.isVisibleToUser) { "UI node is disabled or invisible" }
            if (action == "set_text") {
                val args = Bundle().apply {
                    putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, request.getString("value"))
                }
                check(node.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args)) { "UI node does not support setting text" }
            } else if (!node.performAction(AccessibilityNodeInfo.ACTION_CLICK)) {
                val bounds = Rect().also(node::getBoundsInScreen)
                check(!bounds.isEmpty) { "UI node has empty bounds" }
                BridgeServer.DEVICE_LOCK.lock()
                try {
                    val down = InputControlUtils.down(bounds.centerX(), bounds.centerY(), 0, display)
                    SystemClock.sleep(50)
                    val up = InputControlUtils.up(bounds.centerX(), bounds.centerY(), 0, display)
                    check(down && up) { "UI click injection failed" }
                } finally { BridgeServer.DEVICE_LOCK.unlock() }
            }
            return JSONObject().put("ok", true)
        } finally { node.recycle() }
    }
}
