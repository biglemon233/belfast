package com.aliothmoon.maafw.log

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.aliothmoon.maafw.MaaDispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

data class AlasErrorDirInfo(
    /** 目录名（毫秒时间戳），路由参数 */
    val name: String,
    val timestamp: Long,
    val fileCount: Int,
)

data class AlasDailyLogInfo(
    val name: String,
    val sizeBytes: Long,
    val lastModified: Long,
)

data class AlasLogUiState(
    val errorDirs: List<AlasErrorDirInfo> = emptyList(),
    val dailyLogs: List<AlasDailyLogInfo> = emptyList(),
    val loading: Boolean = true,
)

/** ALAS 日志列表（`rootfs/opt/alas/log`）：错误现场 + 按天日志两区；正文归 [LogTailViewModel] */
class AlasLogViewModel(
    private val source: AlasLogSource,
) : ViewModel() {

    private val _uiState = MutableStateFlow(AlasLogUiState())
    val uiState: StateFlow<AlasLogUiState> = _uiState.asStateFlow()

    init {
        viewModelScope.launch { reload() }
    }

    private suspend fun reload() {
        val state = withContext(MaaDispatchers.IO) {
            AlasLogUiState(
                errorDirs = source.errorDirs().map { dir ->
                    AlasErrorDirInfo(
                        name = dir.name,
                        timestamp = dir.name.toLongOrNull() ?: 0L,
                        fileCount = dir.listFiles()?.count { it.isFile } ?: 0,
                    )
                },
                dailyLogs = source.dailyLogs().map {
                    AlasDailyLogInfo(
                        name = it.name,
                        sizeBytes = it.length(),
                        lastModified = it.lastModified(),
                    )
                },
                loading = false,
            )
        }
        _uiState.value = state
    }
}
