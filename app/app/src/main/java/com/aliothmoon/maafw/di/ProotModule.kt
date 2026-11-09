package com.aliothmoon.maafw.di

import com.aliothmoon.maafw.proot.AlasRunController
import com.aliothmoon.maafw.proot.ProotHost
import com.aliothmoon.maafw.service.HostState
import org.koin.android.ext.koin.androidApplication
import org.koin.core.qualifier.named
import org.koin.dsl.module

val prootModule = module {
    single {
        val host: HostState = get()
        ProotHost(androidApplication(), get(named<AppCoroutineScope>()), get(), host::ensureEnvironmentStarted)
    }
    single { AlasRunController(androidApplication(), get(named<AppCoroutineScope>())) }
}
