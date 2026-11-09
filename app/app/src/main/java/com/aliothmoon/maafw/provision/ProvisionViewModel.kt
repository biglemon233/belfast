package com.aliothmoon.maafw.provision

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.aliothmoon.maafw.settings.AppSettingsGateway
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * 首启下载源选择的会话（lite 构建的 [ProvisionState.NotBundled] 页用）
 *
 * 输入期间的事实源是 VM 内的草稿（[RootfsDownloadUiState]），进 VM 而不是 composable，
 * 是为了「下载」点击时拿到的必然是最新输入；盘上 DataStore 只在离散动作（切源、开始下载）
 * 时写穿，逐键落盘纯属白跑 IO。种子从 DataStore 流的首个发射灌入（读到盘即真实值），
 * 之后盘上变化不再回灌——用户已接管输入
 */
class ProvisionViewModel(
    private val provisioner: RootfsProvisioner,
    private val appSettings: AppSettingsGateway,
) : ViewModel() {

    private val draft = MutableStateFlow(RootfsDownloadUiState(abi = provisioner.runtimeAbi))
    val downloadState: StateFlow<RootfsDownloadUiState> = draft.asStateFlow()

    init {
        viewModelScope.launch {
            val seed = combine(
                appSettings.rootfsSourceKind,
                appSettings.rootfsCustomUrl,
                appSettings.rootfsUseMirror,
                appSettings.rootfsMirrors,
            ) { kind, customUrl, useMirror, mirrors ->
                RootfsDownloadUiState(
                    sourceKind = kind,
                    customUrl = customUrl,
                    useMirror = useMirror,
                    mirrors = mirrors,
                    abi = draft.value.abi,
                )
            }.first()
            draft.value = seed
        }
    }

    fun onIntent(intent: ProvisionIntent) {
        when (intent) {
            is ProvisionIntent.SetArchitecture -> draft.update { it.copy(abi = intent.abi) }
            is ProvisionIntent.SetSourceKind -> draft.update {
                it.copy(sourceKind = intent.kind, rejection = null)
            }

            is ProvisionIntent.SetCustomUrl -> draft.update {
                it.copy(customUrl = intent.url, rejection = null)
            }

            is ProvisionIntent.SetUseMirror -> draft.update {
                it.copy(useMirror = intent.enabled)
            }

            is ProvisionIntent.SetMirror -> draft.update {
                val mirrors = it.mirrors.toMutableList()
                while (mirrors.size <= intent.index) mirrors.add("")
                mirrors[intent.index] = intent.prefix
                it.copy(mirrors = mirrors)
            }

            ProvisionIntent.StartDownload -> startDownload()
        }
    }

    private fun startDownload() {
        val state = draft.value
        val source = RootfsDownloadSource(state.sourceKind, state.customUrl.trim(), state.useMirror, state.mirrors, state.abi)
        // GitHub 源是仓库自有 Release，固定合法；自定义直链先过红线校验，拒绝就留在本页提示
        val validation = RootfsSourceValidator.validate(source.customUrl)
        if (source.kind == RootfsSourceKind.CUSTOM && validation is RootfsSourceValidator.Result.Rejected) {
            draft.update { it.copy(rejection = validation.reason) }
            return
        }
        // 持久化本次选择：下次首启（或清数据重装）自动带上
        viewModelScope.launch {
            appSettings.setRootfsSourceKind(state.sourceKind)
            appSettings.setRootfsCustomUrl(state.customUrl.trim())
            appSettings.setRootfsUseMirror(state.useMirror)
            appSettings.setRootfsMirrors(state.mirrors)
            appSettings.setRootfsRuntimeAbi(state.abi)
            provisioner.startDownload(source)
        }
    }
}

/** 下载源选择页的聚合态 */
data class RootfsDownloadUiState(
    val abi: String = "arm64-v8a",
    val sourceKind: RootfsSourceKind = RootfsSourceKind.GITHUB,
    val customUrl: String = "",
    val useMirror: Boolean = false,
    val mirrors: List<String> = RootfsSource.splitMirrors(RootfsSource.DEFAULT_MIRRORS_JOINED),
    /** 自定义直链被红线校验拒绝时的理由；null=无 */
    val rejection: RootfsSourceValidator.Reason? = null,
)

sealed interface ProvisionIntent {
    data class SetArchitecture(val abi: String) : ProvisionIntent
    data class SetSourceKind(val kind: RootfsSourceKind) : ProvisionIntent

    data class SetCustomUrl(val url: String) : ProvisionIntent

    data class SetUseMirror(val enabled: Boolean) : ProvisionIntent

    /** 编辑第 [index] 个镜像前缀（0 起） */
    data class SetMirror(val index: Int, val prefix: String) : ProvisionIntent

    data object StartDownload : ProvisionIntent
}
