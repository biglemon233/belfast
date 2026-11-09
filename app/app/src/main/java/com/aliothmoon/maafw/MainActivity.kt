package com.aliothmoon.maafw

import android.graphics.Color
import android.os.Build
import android.os.Bundle
import android.view.WindowManager
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.appcompat.app.AppCompatActivity
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import com.aliothmoon.maafw.proot.AlasRunController
import com.aliothmoon.maafw.service.BackgroundPolling
import com.aliothmoon.maafw.settings.AppSettingsManager
import com.aliothmoon.maafw.ui.AppRoot
import com.aliothmoon.maafw.ui.alas.AlasWebViewHolder
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.first
import com.aliothmoon.maafw.ui.components.showArchitectureReadinessDialog
import org.koin.android.ext.android.inject

class MainActivity : AppCompatActivity() {

    private val appSettings: AppSettingsManager by inject()
    private val alasController: AlasRunController by inject()

    override fun onCreate(savedInstanceState: Bundle?) {
        val splash = installSplashScreen()
        splash.setKeepOnScreenCondition { !appSettings.loaded.value }
        super.onCreate(savedInstanceState)
        // 挂机/工具运行期间保持屏幕唤醒（App 退到后台或用户手动息屏时仍允许锁屏）
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.STARTED) {
                alasController.state.collect { s ->
                    if (s.runnerAlive || s.toolAlive) {
                        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
                    } else {
                        window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
                    }
                }
            }
        }

        lifecycleScope.launch {
            appSettings.loaded.first { it }
            val deviceAbi = com.aliothmoon.maafw.provision.RuntimeArchitecture.deviceAbi(
                java.io.File("/system/bin/app_process64"), Build.SUPPORTED_ABIS.toList())
            if (!appSettings.skipArchitectureCheck.value &&
                !com.aliothmoon.maafw.provision.RuntimeArchitecture.supportsPackage(BuildConfig.RUNTIME_ABI, deviceAbi)) {
                showArchitectureReadinessDialog(this@MainActivity, BuildConfig.RUNTIME_ABI, deviceAbi,
                    onNeverShowAgain = { lifecycleScope.launch { appSettings.setSkipArchitectureCheck(true) } },
                    onContinue = ::showApp)
            } else showApp()
        }
    }

    private fun showApp() {
        setContent { AppRoot(onDarkThemeChanged = ::applyEdgeToEdge) }
    }

    override fun onStart() {
        super.onStart()
        AlasWebViewHolder.resumeAll()
        // StateFlow restarts both polling loops immediately, not after the idle delay.
        BackgroundPolling.setForeground(true)
    }

    override fun onStop() {
        // Language/orientation recreation is not a real trip into the background.
        if (!isChangingConfigurations) {
            BackgroundPolling.setForeground(false)
            AlasWebViewHolder.pauseAll()
        }
        super.onStop()
    }

    private fun applyEdgeToEdge(darkMode: Boolean) {
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT) { darkMode },
            navigationBarStyle = SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT) { darkMode },
        )
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            window.isNavigationBarContrastEnforced = false
        }
    }
}
