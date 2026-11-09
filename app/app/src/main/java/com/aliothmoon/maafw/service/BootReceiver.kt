package com.aliothmoon.maafw.service

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/** BOOT_COMPLETED is sent after credential-encrypted settings become available. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED) RunForegroundService.startOnBoot(context)
    }
}
