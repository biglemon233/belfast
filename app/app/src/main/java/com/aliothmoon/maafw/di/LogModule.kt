package com.aliothmoon.maafw.di

import com.aliothmoon.maafw.constant.AppPaths
import com.aliothmoon.maafw.log.AlasLogSource
import com.aliothmoon.maafw.log.AppLogWriter
import com.aliothmoon.maafw.log.LogCleaner
import com.aliothmoon.maafw.log.LogExportService
import org.koin.android.ext.koin.androidContext
import org.koin.dsl.module

val logModule = module {
    single { AppLogWriter() }
    single { AlasLogSource(androidContext()) }
    single { LogCleaner(get()) }
    single {
        LogExportService(
            context = androidContext(),
            baseDir = { AppPaths.ROOT },
            launcherRoots = { listOf(AppPaths.LOG_DIR, AppPaths.DEBUG_DIR) },
            alasLogDir = { get<AlasLogSource>().logDir() },
        )
    }
}
