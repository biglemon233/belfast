package com.azurpilot.ghio.gradle

import com.android.build.api.artifact.SingleArtifact
import com.android.build.api.dsl.ApplicationExtension
import com.android.build.api.variant.ApplicationAndroidComponentsExtension
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.api.tasks.Copy
import org.gradle.kotlin.dsl.configure
import org.gradle.kotlin.dsl.getByType
import org.gradle.kotlin.dsl.register

/**
 * 随包发布的 ABI 列表；debug 构建可用 build.debugAbi 收窄到单个 ABI 以缩短构建时间
 *
 * 内置 rootfs 的架构必须与之匹配：release CI 用 -Pazurpilot.releaseAbi 按架构收窄，
 * 且只打包该架构的 rootfs（PRoot 不做指令模拟）。
 *
 * The ABIs that ship; a debug build can narrow to one via build.debugAbi to save build time.
 *
 * The bundled rootfs must match: release CI narrows per arch with -Pazurpilot.releaseAbi
 * and bundles that arch's rootfs (proot does not emulate).
 */
private val SHIPPED_ABIS = listOf("arm64-v8a", "x86_64")

/**
 * 所有构建共用的基础包名；profile 只在其后追加段，从不整体替换
 *
 * The package every build sits under; a profile only appends to it, it never replaces it.
 */
private const val BASE_APPLICATION_ID = "com.azurpilot.ghio"

/**
 * profile 的 app.id 会成为包名段，因此必须满足包名规则而不能是任意文本
 *
 * 在这里拒绝非法值，好过事后从 manifest 合并报错里、甚至从一个装在
 * 没人想发布的包名下的安装包里才发现问题。
 *
 * A profile's app.id becomes package segments, so it takes package rules rather than free text.
 *
 * Rejecting the rest here beats finding out from a manifest merger error or, worse, an
 * installed package under a name nobody meant to publish.
 */
private val APP_ID_PATTERN = Regex("""[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*""")

/**
 * 刻意独立的包名后缀：基准测试会执行 `pm clear` 来度量首启，不加后缀会清掉开发者
 * 正在使用的那个构建的全部配置
 *
 * A separate package on purpose: the benchmarks run `pm clear` to measure a first launch,
 * which would otherwise wipe the configurations of the build the developer is actually using.
 */
internal const val BENCHMARK_APP_ID_SUFFIX = ".benchmark"

/**
 * profile 图标落地的资源名，与仓库自带的 ic_launcher 分开
 *
 * Resource name the profile icon lands under, kept apart from the checked-in ic_launcher.
 */
private const val PROFILE_ICON_NAME = "ic_profile_launcher"

/**
 * 计算最终 applicationId；:app 与 :macrobenchmark 的唯一来源，两者必须指向同一个包
 *
 * Returns the final applicationId; the single source shared by :app and :macrobenchmark,
 * which have to name the same package.
 */
internal fun Project.azurPilotApplicationId(): String =
    buildProfile().appId?.let { "$BASE_APPLICATION_ID.${it.requireAppId()}" } ?: BASE_APPLICATION_ID

/**
 * Android 既不解码 ico 也不解码 svg；profile 指向这类文件属于配置错误，直接构建失败
 *
 * Android decodes neither ico nor svg; a profile pointing at one is a mistake worth failing on.
 */
private val ICON_EXTENSIONS = setOf("png", "webp")

/**
 * :app 模块的整体外壳：SDK 基线、git 版本号、打包、签名与构建类型都在这里收口，
 * 模块脚本只保留该应用特有的部分（namespace、native、buildFeatures）。
 * 身份信息（applicationId、启动器标签与图标）来自构建 profile，见 [BuildProfile]。
 *
 * The whole shell around the app module: SDK baseline, git version, packaging, signing, and
 * build types; the module script keeps only what this app is (namespace, native,
 * buildFeatures). Identity (applicationId, launcher label and icon) comes from the build
 * profile, see [BuildProfile].
 */
class AndroidApplicationConventionPlugin : Plugin<Project> {
    override fun apply(target: Project) {
        with(target) {
            pluginManager.apply("com.android.application")

            val android = extensions.getByType<ApplicationExtension>()
            configureAndroidCommon(android)

            val profile = buildProfile()
            val icon = profile.appIcon?.also { configureProfileIcon(it) }

            android.defaultConfig {
                applicationId = azurPilotApplicationId()
                targetSdk = TARGET_SDK
                versionCode = gitVersionCode()
                versionName = gitVersionName()
                println("Build version: applicationId=$applicationId, versionCode=$versionCode, versionName=$versionName")

                // 用 manifestPlaceholders 而非 resValue：无 profile 时值仍是资源引用，
                // 仓内默认的标签与图标无需改动即可继续生效
                manifestPlaceholders["appLabel"] = profile.appLabel ?: "@string/app_name"
                manifestPlaceholders["appIcon"] =
                    if (icon != null) "@mipmap/$PROFILE_ICON_NAME" else "@mipmap/ic_launcher"
                manifestPlaceholders["appRoundIcon"] =
                    if (icon != null) "@mipmap/$PROFILE_ICON_NAME" else "@mipmap/ic_launcher_round"

                testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
            }

            android.packaging {
                jniLibs {
                    useLegacyPackaging = true
                }
                resources {
                    pickFirsts += setOf(
                        "META-INF/LICENSE.md",
                        "META-INF/NOTICE.md",
                    )
                }
            }

            // 没有 keystore 时 release 保持未签名，本地构建不会仅因缺签名材料而失败
            val keystorePath = signingSetting("KEYSTORE_PATH", "KEYSTORE_PATH")
            val releaseSigning = android.signingConfigs.create("release").apply {
                if (keystorePath.isNotEmpty()) {
                    storeFile = file(keystorePath)
                    storePassword = signingSetting("KEYSTORE_PASSWORD", "KEYSTORE_PASSWORD")
                    keyAlias = signingSetting("KEY_ALIAS", "KEY_ALIAS")
                    keyPassword = signingSetting("KEY_PASSWORD", "KEY_PASSWORD")
                    enableV1Signing = true
                    enableV2Signing = true
                    enableV3Signing = true
                }
            }

            // Debug 也走仓内固定的 keystore。AGP 默认用 ~/.android/debug.keystore——本地由 SDK 生成、
            // CI runner 上每次都是新生成的，于是每个 CI debug APK 签名都不同，互相覆盖安装会报
            // INSTALL_FAILED_UPDATE_INCOMPATIBLE。仓内这份是 Android 通用的公开调试密钥
            // （androiddebugkey / android），提交它正是为了让所有构建产物签名一致。
            val debugKeystore = file(
                signingSetting("DEBUG_KEYSTORE_PATH", "DEBUG_KEYSTORE_PATH").ifEmpty { "debug.keystore" },
            )
            if (debugKeystore.exists()) {
                android.signingConfigs.maybeCreate("debug").apply {
                    storeFile = debugKeystore
                    storePassword = signingSetting("DEBUG_KEYSTORE_PASSWORD", "DEBUG_KEYSTORE_PASSWORD")
                        .ifEmpty { "android" }
                    keyAlias = signingSetting("DEBUG_KEY_ALIAS", "DEBUG_KEY_ALIAS")
                        .ifEmpty { "androiddebugkey" }
                    keyPassword = signingSetting("DEBUG_KEY_PASSWORD", "DEBUG_KEY_PASSWORD")
                        .ifEmpty { "android" }
                }
            }

            extensions.configure<ApplicationAndroidComponentsExtension> {
                onVariants { variant ->
                    // 在这里设置而非在 buildType 上加 applicationIdSuffix：baselineprofile 插件
                    // 会把自己的 action 传给 buildTypes.create，其执行晚于 configureEach，
                    // 会把后缀再丢掉。不这样做的话 generation 构建会覆盖开发者设备上
                    // 正在使用的安装
                    val generated = variant.buildType.orEmpty().let {
                        it.startsWith("nonMinified") || it.startsWith("benchmarkRelease")
                    }
                    if (generated) {
                        variant.applicationId.set(azurPilotApplicationId() + BENCHMARK_APP_ID_SUFFIX)
                    }
                    // debug / nonMinified / benchmark 变体不产出 OBFUSCATION_MAPPING_FILE，
                    // 把校验任务挂上去会因 mapping 未配置而直接 configure 失败
                    if (variant.name == "release") {
                        val verify = tasks.register<VerifyR8KeepsTask>("verifyReleaseR8Keeps") {
                            mapping.set(
                                variant.artifacts.get(SingleArtifact.OBFUSCATION_MAPPING_FILE),
                            )
                            criticalClasses.set(R8_CRITICAL_CLASSES)
                            criticalNoArgConstructors.set(R8_CRITICAL_NO_ARG_CONSTRUCTORS)
                        }
                        tasks.matching { it.name == "assembleRelease" }
                            .configureEach { finalizedBy(verify) }
                    }
                }
            }

            val debugAbis = listSetting("build.debugAbi").ifEmpty { SHIPPED_ABIS }
            // 完整包与轻量更新包共用架构参数；无签名 CI 的 debug 包也必须按同一规则收窄。
            val releaseAbi = providers.gradleProperty("azurpilot.releaseAbi").orNull?.trim().orEmpty()
            require(releaseAbi.isEmpty() || releaseAbi in SHIPPED_ABIS) {
                "azurpilot.releaseAbi must be one of $SHIPPED_ABIS, got \"$releaseAbi\""
            }
            val releaseAbis = if (releaseAbi.isEmpty()) SHIPPED_ABIS else listOf(releaseAbi)
            android.buildTypes {
                getByName("debug") {
                    ndk {
                        abiFilters += if (releaseAbi.isEmpty()) debugAbis else releaseAbis
                    }
                }
                getByName("release") {
                    ndk {
                        abiFilters += releaseAbis
                    }
                    // 资源收缩保持关闭：它是另一个有独立失败模式的开关，目前没有做过
                    // 任何收益度量
                    isMinifyEnabled = true
                    proguardFiles(
                        android.getDefaultProguardFile("proguard-android-optimize.txt"),
                        "proguard-rules.pro",
                    )
                    if (keystorePath.isNotEmpty()) {
                        signingConfig = releaseSigning
                    }
                }
            }

            // androidx.baselineprofile 会自带一对构建类型：nonMinifiedRelease 用于采集 profile，
            // benchmarkRelease 用于度量发布形态。此前手写的 `benchmark` 类型与后者职责重叠，
            // 两者并存会让测试模块变体翻倍，还会漏放一个未加后缀的变体，把开发者设备上
            // 的应用卸掉
            //
            // 这两类保持 release 的完整 ABI 集：采集 profile 需要 API 33+ 或 root adb 会话，
            // 往往只能在模拟器上跑，而模拟器通常是 x86_64。
            // 用 configureEach 而非 getByName：执行到这里时它们尚未创建
            android.buildTypes.configureEach {
                if (!name.startsWith("nonMinified") && !name.startsWith("benchmarkRelease")) {
                    return@configureEach
                }
                // 用 debug 签名，无需 release 签名材料即可安装；profileable 让 macrobenchmark
                // 能对非 debuggable 构建挂 tracer。
                // applicationId 后缀在上面 onVariants 里设置，不在这里
                signingConfig = android.signingConfigs.getByName("debug")
                isProfileable = true
            }
        }
    }
}

/**
 * 校验 profile 的 app.id 为合法包名段：非法值让构建失败，合法时原样返回
 *
 * Validates a profile's app.id as package segments, failing the build otherwise, then
 * returns it unchanged.
 */
private fun String.requireAppId(): String {
    require(APP_ID_PATTERN.matches(this)) {
        "app.id must be lowercase package segments such as m9a or azurpilot.end, got \"$this\""
    }
    return this
}

/**
 * 把 profile 图标以单张 xxxhdpi 位图写入生成的 res 目录
 *
 * 只提供这一个密度：API 26+ 的启动器会不加遮罩直接显示位图而非自适应图标；
 * 配齐一整套自适应图标意味着 profile 要携带前景、背景与多密度文件。
 *
 * Drops the profile icon into a generated res tree as a single xxxhdpi bitmap.
 *
 * One density only, so launchers on API 26+ show the bitmap unmasked instead of an adaptive
 * icon; shipping a full adaptive set would mean the profile carrying foreground, background
 * and densities.
 */
private fun Project.configureProfileIcon(icon: java.io.File) {
    require(icon.isFile) { "app.icon points at a missing file: ${icon.absolutePath}" }
    val extension = icon.extension.lowercase()
    require(extension in ICON_EXTENSIONS) {
        "app.icon must be one of $ICON_EXTENSIONS, got .$extension (${icon.absolutePath})"
    }

    val profileResDir = layout.buildDirectory.dir("generated/profileRes")
    val syncProfileIcon = tasks.register<Copy>("syncProfileIcon") {
        group = "build"
        description = "Copy the profile launcher icon into the generated resources"
        from(icon) { rename { "$PROFILE_ICON_NAME.$extension" } }
        into(profileResDir.map { it.dir("mipmap-xxxhdpi") })
    }
    val writeProfileSplash = tasks.register("writeProfileSplash") {
        group = "build"
        description = "Override the splash theme so it uses the profile icon"
        val themeOut = profileResDir.map { it.file("values/profile_splash.xml") }
        val iconOut = profileResDir.map { it.file("drawable/ic_profile_splash.xml") }
        outputs.files(themeOut, iconOut)
        doLast {
            // SplashScreen 总会把图标圆形裁切；内缩 48dp 让整张位图落在圆内，
            // 透明像素透出的才是 colorBackground 而非黑色
            iconOut.get().asFile.apply {
                parentFile.mkdirs()
                writeText(
                    """
                    <layer-list xmlns:android="http://schemas.android.com/apk/res/android">
                        <item
                            android:bottom="48dp"
                            android:left="48dp"
                            android:right="48dp"
                            android:top="48dp">
                            <bitmap
                                android:gravity="fill"
                                android:src="@mipmap/$PROFILE_ICON_NAME" />
                        </item>
                    </layer-list>
                    """.trimIndent() + "\n",
                )
            }
            themeOut.get().asFile.apply {
                parentFile.mkdirs()
                writeText(
                    """
                    <resources>
                        <style name="Theme.AzurPilotApp.Starting" parent="Theme.SplashScreen">
                            <item name="windowSplashScreenBackground">?android:attr/colorBackground</item>
                            <item name="windowSplashScreenAnimatedIcon">@drawable/ic_profile_splash</item>
                            <item name="postSplashScreenTheme">@style/Theme.AzurPilotApp</item>
                        </style>
                    </resources>
                    """.trimIndent() + "\n",
                )
            }
        }
    }

    tasks.named("preBuild") {
        dependsOn(syncProfileIcon, writeProfileSplash)
    }

    extensions.configure<ApplicationAndroidComponentsExtension> {
        onVariants { variant ->
            variant.sources.res?.addStaticSourceDirectory(profileResDir.get().asFile.absolutePath)
        }
    }
}
