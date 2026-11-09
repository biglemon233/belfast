# 注释规范 / Comment conventions

> 本文定义本仓库全部源码的注释标准。规则以 Google 官方风格指南为基准，
> 并叠加本仓库的双语惯例；所有新增与改写注释必须遵守本文。
>
> This document defines the comment standard for all source code in this repository.
> The rules are based on Google's official style guides, extended with this repo's
> bilingual convention; every new or rewritten comment must follow it.

## 中文

### 参考基准

- [Google Kotlin 风格指南](https://developer.android.com/kotlin/style-guide)（KDoc 规则）
- [Google Java 风格指南](https://google.github.io/styleguide/javaguide.html#s7-javadoc)（第 7 节 Javadoc）
- [Google C++ 风格指南](https://google.github.io/styleguide/cppguide.html#Comments)（第 7 节注释）
- [Google Python 风格指南](https://google.github.io/styleguide/pyguide.html#38-comments-and-docstrings)（第 3.8 节文档字符串）

### 总则

1. **注释解释「为什么」，不解释「做什么」**。代码本身应当自解释；注释只承载代码读不出来的信息：
   平台怪癖、性能考量、变通手段、不变量、线程与生命周期契约、踩坑记录。
   示例：`// 设置标题` 这类复述代码的注释一律删除。
2. **每个源文件的主声明必须有文档注释**（类 / object / interface / 顶层函数 / Composable）。
   公开成员（public / internal）需要 KDoc；私有成员在行为不明显时也要写。
3. **文档注释描述契约**：职责、保证、参数语义、返回值、异常、线程归属、生命周期，
   不逐行复述实现。
4. **信息必须真实**。不确定的行为不要编造；写之前先读函数体。数字、路径、坑点等既有事实保留，
   只按本文格式重写。
5. **双语规则（本仓库特有）**：文档注释（KDoc / Javadoc / 模块 docstring / 文件头注释）先中文、
   空一行、再英文镜像；两段信息等价。行内注释用中文，不要求双语。
   第三方移植代码（如 scrcpy）保留来源说明。
6. **注释排版每行不超过 100 列**，完整句子，句首大写（英文）、中文用全角标点。
7. **禁止空洞注释**：不允许空的 `/** */`、`@author` / `@version` 等署名噪音、
   分节线注释（`// ==== Setup ====`）。
8. **删除代码请连注释一起删**，不留注释掉的死代码。

### Kotlin：KDoc

- 使用 `/** ... */` KDoc，配合 Dokka 渲染；`//` 只用于行内说明。
- 首行是一句话摘要，用第三人称动词开头（返回 / 加载 / 拉起；Returns / Loads / Spawns）。
- 契约要素按需给出：职责 → 行为 → 约束。线程归属（主线程 / IO / 特权进程）必须写清。
- `@param`、`@return`、`@throws` 各占一行并附说明；一目了然的签名不必机械补全。
  `@throws` 写明何时抛出。
- 用 `[方括号]` 链接符号（如 `[AppPaths.ROOT]`），不用 HTML 标签。
- 数据类 / 枚举：每个承载语义的字段或枚举值加注释说明业务含义。
- Composable 函数：说明该组件在界面层级中的角色、关键状态与参数的行为，
  以及重组注意事项（如避免的重组开销）。
- object 单例：说明生命周期与线程模型（如「进程级单例，`init` 早于 Koin 就绪」）。

示例（节选自 `proot/ProotHost.kt` 的既有风格）：

```kotlin
/**
 * 亮屏 / 解锁 / 上锁息屏；整段在特权进程内完成
 *
 * 全程在特权进程里做而不是 app 侧分几步 IPC：息屏之后 app 侧的协程会被系统挂起，
 * 分步做会卡在中间。
 *
 * Wakes the screen, unlocks, and locks it back to sleep; runs entirely inside
 * the privileged process.
 *
 * Doing it all in one privileged call instead of step-by-step IPC from the app
 * side: after the screen goes off the system may suspend the app-side
 * coroutines, wedging the sequence halfway.
 *
 * @param credential 纯数字 PIN（图案与密码暂不支持）/ numeric PIN only (pattern
 *   and password are unsupported)
 * @return [WakeUnlockResult.OK] 或失败码 / [WakeUnlockResult.OK] or a failure code
 */
```

### 行内注释

- 只写「为什么」：平台怪癖、绕坑、性能权衡、防御性原因。
- 中文、完整句子；放在所解释代码的上方（尾随注释仅限单行短说明）。
- 魔法数字 / 阈值：在常量 KDoc 里说明取值依据，而非在使用处复述。

### 测试代码

- 测试类 KDoc：一句话说明钉住的行为面。
- 单个测试函数：命名已自解释的可省略 KDoc；否则写一行说明前置条件与断言意图。

### Kotlin（hidden-api 编译期桩）

按上述 Kotlin KDoc 规则：所有类与方法遵守双语规则，按需说明参数、返回值与异常。
桩代码注明「编译期签名镜像，运行时由框架提供」，并说明保持框架 JVM 签名所需的声明约束。

### C++（native 桥接库）

按 Google C++ 风格指南第 7 节：

- 每个文件头部写文件注释：职责、归属进程、与 Kotlin 侧的对应关系。
- 类 / 函数：`//` 注释，说明用途、参数、返回值、线程约束；不使用 Doxygen `@` 标签。
- 行内注释解释实现取舍，中文即可。

### Python（rootfs 覆盖层与构建脚本）

按 Google Python 风格指南 3.8 节：

- 每个模块、类、公开函数写 docstring。
- 函数 docstring 按需含 `Args:` / `Returns:` / `Raises:` 小节；双语规则：中文段在前、
  英文段在后（各小节镜像）。
- `# ` 行内注释解释「为什么」。

### 构建脚本与配置（Gradle KTS / TOML / CI YAML）

- 只注释非显而易见的配置块：说明「为什么这样配」（如 R8 keep 规则的对应类、
  jniLibs 命名约束的原因），不复述配置项字面含义。
- 版本目录（`libs.versions.toml`）不为每个条目写注释；仅在升级需谨慎处注明。

### TODO 与废弃

- TODO 格式：`// TODO(负责人): 描述`，描述写清要做的事与触发条件。
- 废弃 API：`@Deprecated`（Kotlin）/ `@deprecated`（Python）注解，
  并在文档注释中写明替代方案。

## English

### Reference standards

- [Google Kotlin style guide](https://developer.android.com/kotlin/style-guide) (KDoc rules)
- [Google Java Style Guide](https://google.github.io/styleguide/javaguide.html#s7-javadoc) (section 7, Javadoc)
- [Google C++ Style Guide](https://google.github.io/styleguide/cppguide.html#Comments) (section 7, Comments)
- [Google Python Style Guide](https://google.github.io/styleguide/pyguide.html#38-comments-and-docstrings) (section 3.8, docstrings)

### General principles

1. **Comments explain why, not what.** Code should be self-explanatory; comments carry only
   what the code cannot express: platform quirks, performance trade-offs, workarounds,
   invariants, threading and lifecycle contracts, hard-won lessons. Delete comments that
   narrate the code, such as `// set the title`.
2. **Every file's primary declaration is documented** (class / object / interface /
   top-level function / composable). All public and internal members get KDoc; private
   members get it when their behavior is not obvious.
3. **Doc comments describe the contract**: responsibility, guarantees, parameter semantics,
   return value, exceptions, thread affinity, lifecycle — never a line-by-line narration
   of the implementation.
4. **Comments must be true.** Do not invent behavior; read the function body first. Keep
   existing facts (numbers, paths, gotchas) and reformat them to this standard.
5. **Bilingual rule (repo-specific)**: doc comments (KDoc / Javadoc / module docstrings /
   file header comments) carry Chinese first, then a blank line, then the English mirror,
   with equivalent information. Inline comments are Chinese only. Ported third-party code
   (for example scrcpy) keeps its provenance note.
6. **Wrap comments at 100 columns**; complete sentences; sentence-case headings in English.
7. **No noise**: no empty `/** */` blocks, no `@author` / `@version` tags, no section
   divider comments (`// ==== Setup ====`).
8. **Delete dead code together with its comments**; never leave commented-out code.

### Kotlin: KDoc

- Use `/** ... */` KDoc (rendered by Dokka); use `//` only for inline notes.
- The first line is a one-sentence summary starting with a third-person verb
  (Returns / Loads / Spawns).
- Give contract elements as needed: responsibility → behavior → constraints. Thread
  affinity (main / IO / privileged process) is mandatory.
- Put `@param`, `@return`, `@throws` each on their own line with a description; skip them
  for trivially obvious signatures. `@throws` states when the exception occurs.
- Link symbols with `[brackets]` (for example `[AppPaths.ROOT]`); no HTML tags.
- Data classes and enums: document the business meaning of each semantic field or value.
- Composables: document the component's role in the UI tree, key state and parameter
  behavior, and recomposition caveats.
- Object singletons: document lifecycle and threading (for example "process-level
  singleton; `init` runs before Koin is ready").

### Inline comments

- Explain why only: platform quirks, workarounds, performance trade-offs, defensive reasons.
- Chinese, complete sentences, placed above the explained line (trailing comments only for
  short single-line notes).
- Magic numbers and thresholds: explain the rationale in the constant's KDoc, not at the
  use site.

### Test code

- Test class KDoc: one sentence naming the behavior it pins down.
- Individual test functions: omit KDoc when the name is self-explanatory; otherwise one
  line stating the setup and the assertion intent.

### Kotlin (hidden-api compile-time stubs)

Follow the Kotlin KDoc rules above: classes and methods use the same bilingual rule,
documenting parameters, return values, and exceptions as needed. Note that stubs mirror
framework signatures at compile time and are provided by the platform at runtime, and
explain declaration constraints needed to preserve the framework's JVM signatures.

### C++ (native bridge library)

Follow Google C++ Style Guide section 7:

- Every file starts with a file comment: responsibility, owning process, and the
  correspondence to the Kotlin side.
- Classes and functions: `//` comments covering purpose, arguments, return value, and
  thread constraints; no Doxygen `@` tags.
- Inline comments explain implementation trade-offs, in Chinese.

### Python (rootfs overlays and build scripts)

Follow Google Python Style Guide section 3.8:

- Every module, class, and public function gets a docstring.
- Function docstrings include `Args:` / `Returns:` / `Raises:` sections as needed; the
  bilingual rule applies (Chinese sections first, English sections after, mirrored).
- `# ` inline comments explain why.

### Build scripts and configuration (Gradle KTS / TOML / CI YAML)

- Comment only non-obvious blocks: state why the configuration is what it is (for example
  which class an R8 keep rule protects, why `jniLibs` naming is required), never restate
  the literal meaning of the option.
- Do not comment every entry in the version catalog (`libs.versions.toml`); note only
  entries that require care when upgrading.

### TODO and deprecation

- TODO format: `// TODO(owner): description`, stating what to do and when it is triggered.
- Deprecated APIs: use the `@Deprecated` annotation (Kotlin) or `@deprecated` (Python) and
  document the replacement in the doc comment.
