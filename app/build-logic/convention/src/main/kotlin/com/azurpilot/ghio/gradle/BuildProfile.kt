package com.azurpilot.ghio.gradle

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.add
import kotlinx.serialization.json.addJsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import kotlinx.serialization.json.putJsonObject
import org.gradle.api.Project
import org.snakeyaml.engine.v2.api.Load
import org.snakeyaml.engine.v2.api.LoadSettings
import java.io.File

/**
 * profile 未给出自己的清单时，一个 PI 默认对包贡献的文件集
 *
 * What a PI contributes to the package when the profile does not spell out its own list.
 */
private val DEFAULT_PI_INCLUDE = listOf(
    "interface.json",
    "tasks/**",
    "resource/**",
    "resource_*/**",
    "data/**",
    "locales/**",
    "CONTACT",
    "LICENSE",
)

/**
 * 可执行文件的合法落点；这两个取值与 app 侧 AgentRuntimeLocation 的反序列化一一对应
 *
 * Where an executable may land; these two values are exactly what AgentRuntimeLocation
 * deserializes.
 */
private val AGENT_LOCATIONS = setOf("nativeLibs", "bundle")

/**
 * 输出为 pretty print：这份 JSON 会进 APK，排查 agent 问题的人会直接读它
 *
 * Pretty printed because it ends up in the APK, where anyone debugging an agent will read it.
 */
private val descriptorJson = Json { prettyPrint = true }

/**
 * agent 运行时描述符的一项，app 侧以 AgentRuntimeDescriptor.runtimes 读回
 *
 * 声明在 profile 里而非打进 agent 发行包：发行包由不了解 PI 想如何启动 agent 的构建脚本
 * 产出，这份文件反正得手写，不如与其余配方放在一起。
 *
 * One entry of the descriptor the app reads back as AgentRuntimeDescriptor.runtimes.
 *
 * Declared in the profile rather than shipped inside the agent dist: the dist is produced by
 * a build script that knows nothing about how a PI wants its agents launched, so the file was
 * hand written anyway and belongs with the rest of the recipe.
 */
internal data class AgentRuntime(
    val location: String,
    val executable: String,
    val args: List<String>,
    val env: Map<String, String>,
)

/**
 * 一份打包配方：放入哪个 PI、从它取哪些文件、随行哪个 agent 运行时，以及产出应用
 * 携带的身份信息
 *
 * fork 适配另一个 PI 只需写一份这样的配方并把 pi.profile 指过去，因此本仓永远不出现
 * 项目专属的 id 或目录名。
 *
 * One packaging recipe: which PI goes in, what to take out of it, which agent runtime rides
 * along, and the identity the resulting app carries.
 *
 * A fork adapts to another PI by writing one of these and pointing pi.profile at it, so no
 * project specific id or directory name ever enters this repo.
 */
internal data class BuildProfile(
    /**
     * PI 在磁盘上的根目录；上游项目通常放在 assets 目录里，profile 键名由此而来
     * / Root of the PI on disk; upstream projects usually keep it in an assets directory,
     * hence the profile key.
     */
    val assetsDir: String?,
    val piInclude: List<String>,
    /**
     * 叠加在 include 列表之上，profile 借此从纳入的目录树里剔出少数大文件
     * / Applied on top of the include list, which is how a profile carves a few heavy files
     * out of an included tree.
     */
    val piExclude: List<String>,
    val agentSourceDir: String?,
    val agentAbi: List<String>,
    /**
     * 顺序即配对语义：第 n 项启动 PI 的 agent[n]，app 拒绝自行猜测配对
     * / Order matters: entry n launches the PI's agent[n], the app refuses to guess a pairing.
     */
    val agentRuntimes: List<AgentRuntime>,
    /**
     * 仅包名后缀，绝不是完整 applicationId；基础包名归本仓决定
     * / Package suffix only, never a whole applicationId; the base package is this repo's
     * to decide.
     */
    val appId: String?,
    val appLabel: String?,
    val appIcon: File?,
)

/**
 * 完全未配置 profile 时的默认形态：包里不带 PI
 *
 * Nothing configured at all: the package ships without a PI.
 */
private val NO_PROFILE = BuildProfile(
    assetsDir = null,
    piInclude = DEFAULT_PI_INCLUDE,
    piExclude = emptyList(),
    agentSourceDir = null,
    agentAbi = listOf("*"),
    agentRuntimes = emptyList(),
    appId = null,
    appLabel = null,
    appIcon = null,
)

/**
 * 解析 pi.profile（local.properties）或环境变量 PI_PROFILE，这是把构建指向某个 PI 的
 * 唯一途径；未配置时返回 [NO_PROFILE]。
 * profile 内的相对路径以 profile 自身目录为基准解析，配方因此可以与它描述的 PI 毗邻存放。
 *
 * Reads pi.profile from local.properties, or PI_PROFILE from the environment — the only way
 * to point the build at a PI; returns [NO_PROFILE] when nothing is configured.
 *
 * Relative paths inside a profile resolve against the profile's own directory, which lets a
 * profile sit next to the PI it describes.
 */
internal fun Project.buildProfile(): BuildProfile {
    val profilePath = pathSetting("pi.profile", "PI_PROFILE") ?: return NO_PROFILE
    val file = rootProject.file(profilePath)
    require(file.isFile) { "pi.profile points at a missing file: ${file.absolutePath}" }
    return runCatching { file.readProfile() }
        .getOrElse { throw IllegalStateException("invalid profile ${file.absolutePath}: ${it.message}", it) }
}

private fun File.readProfile(): BuildProfile {
    val loaded = Load(LoadSettings.builder().build()).loadFromString(readText())
    val root = loaded as? Map<*, *>
        ?: throw IllegalStateException("the top level of a profile must be a mapping")
    val base = parentFile
    val agent = root.child("agent")
    val app = root.child("app")

    val agentSourceDir = agent?.text("sourceDir")?.let { base.resolvePath(it).absolutePath }
    val agentRuntimes = agent?.children("runtimes")?.map { it.toAgentRuntime() }.orEmpty()
    // 只配其一永远产不出可启动的 agent，而这两处失败都离这里很远：缺描述符要到运行期
    // prepare() 才暴露，缺发行包要到真正执行时才暴露
    require(agentSourceDir == null || agentRuntimes.isNotEmpty()) {
        "agent.sourceDir is set but agent.runtimes is empty, nothing could be launched from it"
    }
    require(agentRuntimes.isEmpty() || agentSourceDir != null) {
        "agent.runtimes is declared but agent.sourceDir is not, there would be no executables to launch"
    }

    return BuildProfile(
        assetsDir = root.text("assets")?.let { base.resolvePath(it).absolutePath },
        piInclude = root.textList("include") ?: DEFAULT_PI_INCLUDE,
        piExclude = root.textList("exclude").orEmpty(),
        agentSourceDir = agentSourceDir,
        agentAbi = agent?.textList("abi") ?: listOf("*"),
        agentRuntimes = agentRuntimes,
        appId = app?.text("id"),
        appLabel = app?.text("label"),
        appIcon = app?.text("icon")?.let { base.resolvePath(it) },
    )
}

private fun Map<*, *>.toAgentRuntime(): AgentRuntime {
    val location = text("location")
    require(location in AGENT_LOCATIONS) {
        "agent.runtimes[].location must be one of $AGENT_LOCATIONS, got ${location ?: "nothing"}"
    }
    return AgentRuntime(
        location = location!!,
        executable = requireNotNull(text("executable")) { "agent.runtimes[].executable is required" },
        args = textList("args").orEmpty(),
        env = child("env")?.entries
            ?.associate { (key, value) -> key.toString() to value.scalar().orEmpty() }
            .orEmpty(),
    )
}

/**
 * 生成写进 assets/agent/agent-runtime.json 的字节内容，app 侧对应 AgentRuntimeDescriptor
 *
 * Produces the exact bytes that land in assets/agent/agent-runtime.json, see
 * AgentRuntimeDescriptor.
 */
internal fun List<AgentRuntime>.toDescriptorJson(): String = descriptorJson.encodeToString(
    JsonObject.serializer(),
    buildJsonObject {
        putJsonArray("runtimes") {
            this@toDescriptorJson.forEach { runtime ->
                addJsonObject {
                    put("location", runtime.location)
                    put("executable", runtime.executable)
                    if (runtime.args.isNotEmpty()) {
                        putJsonArray("args") { runtime.args.forEach { add(it) } }
                    }
                    if (runtime.env.isNotEmpty()) {
                        putJsonObject("env") { runtime.env.forEach { (key, value) -> put(key, value) } }
                    }
                }
            }
        }
    },
)

private fun File.resolvePath(path: String): File =
    File(path).let { if (it.isAbsolute) it else File(this, path) }

/**
 * YAML 会把不带引号的标量还原成 Int、Boolean 等；profile 只想要文本，
 * 因此凡非集合的值都按其字面形式读出
 *
 * YAML hands back Int, Boolean and friends for unquoted scalars; a profile only ever wants
 * text out of them, so anything that is not a collection is read as its literal form.
 */
private fun Any?.scalar(): String? = when (this) {
    null, is Map<*, *>, is List<*> -> null
    else -> toString().expandEnv()
}

private fun Map<*, *>.text(key: String): String? = this[key].scalar()?.takeIf { it.isNotBlank() }

private fun Map<*, *>.textList(key: String): List<String>? =
    (this[key] as? List<*>)?.mapNotNull { it.scalar() }?.filter { it.isNotBlank() }

private fun Map<*, *>.child(key: String): Map<*, *>? = this[key] as? Map<*, *>

private fun Map<*, *>.children(key: String): List<Map<*, *>>? =
    (this[key] as? List<*>)?.filterIsInstance<Map<*, *>>()

private val ENV_PLACEHOLDER = Regex("""\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}""")

/**
 * profile 产出的每个字符串都过这里：检入仓库的 profile 可把机器相关的部分（路径、标签、
 * id）交给环境变量，而不写死在文件里
 *
 * 未设且无 `:-` 回退的 ${NAME} 会让构建失败而不是展开为空：带洞的包名或标签能正常安装
 * 发布，问题因此极易被漏掉。
 *
 * Every string a profile yields goes through this, so a checked-in profile can leave the
 * machine specific parts (paths, labels, ids) to the environment instead of baking them in.
 *
 * An unset ${NAME} without a :-fallback fails the build rather than expanding to nothing: a
 * package name or label with a hole in it installs and ships just fine, which is how it goes
 * unnoticed.
 */
private fun String.expandEnv(): String = ENV_PLACEHOLDER.replace(this) { match ->
    val name = match.groupValues[1]
    System.getenv(name)?.takeIf { it.isNotEmpty() }
        ?: match.groups[2]?.value
        ?: throw IllegalStateException("\${$name} is referenced but that environment variable is not set")
}
