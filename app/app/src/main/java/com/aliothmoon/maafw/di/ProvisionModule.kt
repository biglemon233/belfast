package com.aliothmoon.maafw.di

import com.aliothmoon.maafw.provision.RootfsProvisioner
import com.aliothmoon.maafw.settings.AppSettingsManager
import org.koin.android.ext.koin.androidApplication
import org.koin.core.qualifier.named
import org.koin.dsl.module

val provisionModule = module {
    single {
        val settings = get<AppSettingsManager>()
        RootfsProvisioner(androidApplication(), get(named<AppCoroutineScope>())) { settings.rootfsRuntimeAbi.value }
    }
}
