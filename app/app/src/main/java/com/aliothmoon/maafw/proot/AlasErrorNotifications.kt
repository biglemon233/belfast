package com.aliothmoon.maafw.proot

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.FileObserver
import androidx.core.app.NotificationCompat
import com.aliothmoon.maafw.MainActivity
import com.aliothmoon.maafw.R
import org.json.JSONObject
import timber.log.Timber
import java.io.File

/** App-private events work even when the device bridge or remote push is unavailable. */
internal class AlasErrorNotifications(private val context: Context, private val enabled: () -> Boolean) {
    private var observer: FileObserver? = null

    @Synchronized
    fun start(alasDir: File) {
        if (observer != null) return
        val directory = File(alasDir, "log/.alasaos-notifications")
        if (!directory.isDirectory && !directory.mkdirs()) {
            Timber.w("Cannot create ALAS notification directory")
            return
        }
        @Suppress("DEPRECATION")
        val watcher = object : FileObserver(directory.absolutePath, MOVED_TO) {
            override fun onEvent(event: Int, path: String?) {
                if (path != null && eventName.matches(path)) consume(File(directory, path))
            }
        }
        observer = watcher
        watcher.startWatching()
        directory.listFiles()?.filter { eventName.matches(it.name) }?.forEach(::consume)
    }

    @Synchronized
    fun stop() {
        observer?.stopWatching()
        observer = null
    }

    @Synchronized
    private fun consume(file: File) {
        if (!file.isFile) return
        try {
            if (!enabled()) return
            require(file.length() <= 16 * 1024)
            val event = JSONObject(file.readText())
            val title = event.optString("title", "ALAS").take(256)
            val content = event.optString("content").take(2048)
            val manager = context.getSystemService(NotificationManager::class.java)
            manager.createNotificationChannel(NotificationChannel(
                CHANNEL_ID, context.getString(R.string.notification_channel_alas_error),
                NotificationManager.IMPORTANCE_HIGH,
            ))
            val intent = Intent(context, MainActivity::class.java).apply {
                flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
            }
            val pending = PendingIntent.getActivity(context, 1, intent,
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
            manager.notify(NOTIFICATION_ID, NotificationCompat.Builder(context, CHANNEL_ID)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle(title)
                .setContentText(content)
                .setStyle(NotificationCompat.BigTextStyle().bigText(content))
                .setContentIntent(pending)
                .setAutoCancel(true)
                .setCategory(NotificationCompat.CATEGORY_ERROR)
                .setVisibility(NotificationCompat.VISIBILITY_PRIVATE)
                .build())
        } catch (error: Exception) {
            Timber.w("ALAS local notification failed: %s", error.javaClass.simpleName)
        } finally {
            file.delete()
        }
    }

    companion object {
        private val eventName = Regex("[a-f0-9]{32}\\.json")
        private const val CHANNEL_ID = "alas_errors"
        private const val NOTIFICATION_ID = 1002
    }
}
