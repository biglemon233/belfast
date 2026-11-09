# 构建 Profile / Build profiles

> 本文说明可选的 PI 打包 profile。它面向维护者，不是终端用户配置。
> This document describes optional PI packaging profiles. It targets maintainers, not end-user configuration.

## 中文

### 用途

默认构建不携带 PI。配置 profile 后，Gradle 从指定 PI 目录选择资源和 agent 运行时，并生成与该 profile 对应的应用身份信息。profile 的解析入口为 `app/build-logic/convention/src/main/kotlin/com/azurpilot/ghio/gradle/BuildProfile.kt`。

配置路径的优先级是：`app/local.properties` 中的 `pi.profile` 优先于环境变量 `PI_PROFILE`。两者都未设置时，构建使用不携带 PI 的默认 profile。路径相对 Gradle 根 `app/` 解析；profile 文件内部的相对路径则相对该 profile 文件所在目录解析。

### 最小配置

在 `app/local.properties` 中指定 YAML 文件：

```properties
pi.profile=../profiles/example.yaml
```

或在 CI/终端中设置：

```bash
PI_PROFILE=../profiles/example.yaml ./gradlew assembleDebug
```

profile 顶层必须是 YAML mapping。可用字段如下：

| 字段 | 类型 | 效果 |
|---|---|---|
| `assets` | string | PI 资源根目录；未设置时不复制 PI 资源。 |
| `include` | string list | 从 `assets` 复制的 glob；未设置时使用构建逻辑中的默认 PI 文件集。 |
| `exclude` | string list | 在 `include` 的结果中排除的 glob。 |
| `agent.sourceDir` | string | agent 发行文件根目录。 |
| `agent.abi` | string list | agent 支持的 ABI；未设置为 `*`。 |
| `agent.runtimes` | list | 运行时描述符项；与 `agent.sourceDir` 必须同时配置。 |
| `agent.runtimes[].location` | `nativeLibs` 或 `bundle` | 可执行文件的安装位置。 |
| `agent.runtimes[].executable` | string | agent 可执行文件相对路径。 |
| `agent.runtimes[].args` | string list | 启动参数。 |
| `agent.runtimes[].env` | mapping | 启动环境变量。 |
| `app.id` | string | 追加到基础包名 `com.azurpilot.ghio` 的合法小写包名段。 |
| `app.label` | string | 启动器标签的 manifest placeholder。 |
| `app.icon` | string | PNG、JPG、JPEG 或 WebP 位图图标。 |

profile 输出的字符串支持 `${NAME}` 和 `${NAME:-fallback}` 环境变量展开。未设置且没有回退值的变量会使构建失败，避免生成不完整的包名、标签或路径。

### 身份与产物

profile 只能追加 `app.id`，不能替换基础 application ID。标签和图标通过 manifest placeholder 注入；配置图标时，构建生成 `mipmap-xxxhdpi` 资源和启动页图层。agent 描述符写入 APK 的 `assets/agent/agent-runtime.json`。

不要把个人机器路径、密钥或用户数据提交到 profile。若 profile 含环境变量占位符，记录所需变量名称和可安全公开的默认值。

### 验证

1. 使用 profile 构建：
   ```bash
   cd app && ./gradlew compileDebugKotlin -x verifyBundledAzurPilotRuntime
   ```
2. 对需要 APK 的验证使用：
   ```bash
   cd app && ./gradlew assembleDebug -Pazurpilot.slimApk=true
   ```
3. 安装前确认打印的 `applicationId`、标签和 ABI 与 profile 一致。
4. 变更 agent 配方时，在目标 ABI 设备上验证 descriptor、可执行文件落点和启动路径。

## English

### Purpose

The default build ships no PI. With a profile configured, Gradle selects assets and agent runtimes from a PI directory and generates matching application identity data. The parsing entry point is `app/build-logic/convention/src/main/kotlin/com/azurpilot/ghio/gradle/BuildProfile.kt`.

For the configuration path, `pi.profile` in `app/local.properties` takes precedence over the `PI_PROFILE` environment variable. If neither is set, the build uses the no-PI default profile. Paths are relative to the Gradle root, `app/`; relative paths inside the profile are relative to the profile file itself.

### Minimal configuration

Set a YAML file in `app/local.properties`:

```properties
pi.profile=../profiles/example.yaml
```

Or set it in CI or a terminal:

```bash
PI_PROFILE=../profiles/example.yaml ./gradlew assembleDebug
```

A profile must have a YAML mapping at its top level. Available fields follow:

| Field | Type | Effect |
|---|---|---|
| `assets` | string | PI asset root; no PI assets are copied when absent. |
| `include` | string list | Globs copied from `assets`; the build logic uses its default PI file set when absent. |
| `exclude` | string list | Globs excluded from the `include` result. |
| `agent.sourceDir` | string | Root of agent distribution files. |
| `agent.abi` | string list | ABIs supported by the agent; defaults to `*`. |
| `agent.runtimes` | list | Runtime descriptor entries; it must be configured together with `agent.sourceDir`. |
| `agent.runtimes[].location` | `nativeLibs` or `bundle` | Installation location for the executable. |
| `agent.runtimes[].executable` | string | Relative agent executable path. |
| `agent.runtimes[].args` | string list | Launch arguments. |
| `agent.runtimes[].env` | mapping | Launch environment variables. |
| `app.id` | string | Valid lowercase package segments appended to `com.azurpilot.ghio`. |
| `app.label` | string | Launcher-label manifest placeholder. |
| `app.icon` | string | PNG, JPG, JPEG, or WebP bitmap icon. |

Strings produced by a profile support `${NAME}` and `${NAME:-fallback}` environment expansion. An unset variable without a fallback fails the build, preventing incomplete package IDs, labels, or paths.

### Identity and artifacts

A profile can append `app.id`; it cannot replace the base application ID. Label and icon values are injected through manifest placeholders. When an icon is configured, the build generates a `mipmap-xxxhdpi` resource and a splash-screen layer. The agent descriptor is written to `assets/agent/agent-runtime.json` in the APK.

Do not commit personal-machine paths, secrets, or user data in a profile. When a profile contains environment placeholders, document required variable names and safe public defaults.

### Verification

1. Build with the profile:
   ```bash
   cd app && ./gradlew compileDebugKotlin -x verifyBundledAzurPilotRuntime
   ```
2. For APK-required validation, run:
   ```bash
   cd app && ./gradlew assembleDebug -Pazurpilot.slimApk=true
   ```
3. Before installing, confirm the printed `applicationId`, label, and ABIs match the profile.
4. After changing an agent recipe, verify the descriptor, executable location, and launch path on a device for each target ABI.
