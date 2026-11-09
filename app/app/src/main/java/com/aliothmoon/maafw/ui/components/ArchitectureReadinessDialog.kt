package com.aliothmoon.maafw.ui.components

import android.app.Activity
import android.app.AlertDialog
import android.widget.CheckBox
import com.aliothmoon.maafw.R

/** An advisory gate: users can continue and persist dismissal for unusual devices. */
fun showArchitectureReadinessDialog(
    activity: Activity,
    packageAbi: String,
    deviceAbi: String,
    onNeverShowAgain: () -> Unit,
    onContinue: () -> Unit,
) {
    val checkbox = CheckBox(activity).apply {
        text = activity.getString(R.string.runtime_architecture_never_show)
    }
    val supported = if (packageAbi == "universal") "arm64-v8a / x86_64" else packageAbi
    AlertDialog.Builder(activity)
        .setTitle(R.string.runtime_architecture_title)
        .setMessage(activity.getString(R.string.runtime_architecture_message, supported, deviceAbi))
        .setView(checkbox)
        .setPositiveButton(R.string.common_close, null)
        .setOnDismissListener {
            if (checkbox.isChecked) onNeverShowAgain()
            onContinue()
        }
        .show()
}
