# 包体积与部署占用 / Package and deployed size

## 中文

### 测量范围

2026-10-08 的基线为本地 `1.2.116-mtk-ocr-test` APK 和滚动发布的 `1.2.109`
ARM64 运行环境。运行环境包 SHA-256 与 `latest.json` 一致：
`8cfda788d9aae50a62c3f3831c139cebf8367cda52d011a4ad30afddc3079325`。
完整 ARM64 APK 基线为 1,153,571,667 字节，约 1.07 GiB。

| 项目 | 调整前 | 调整后 | 减少 |
|---|---:|---:|---:|
| 通用轻量测试 APK | 251.92 MiB | 239.98 MiB | 11.94 MiB |
| ARM64 运行环境压缩包 | 864.98 MiB | 735.43 MiB | 129.56 MiB |
| 运行环境部署文件 | 2,298.51 MiB | 1,971.31 MiB | 327.20 MiB |

运行环境“调整后”来自同一份发布包的本地重打包：清除 APT 缓存，裁剪 Python 原生
库，保留其余文件与 tar 权限。部署文件计入三个硬链接在当前提取器中物化成副本的
大小，未计目录块、文件系统额外开销、配置和日志增长。正式 CI 使用原生 ARM64 /
x86_64 runner、GNU binutils，并执行真实导入和 OCR 推理，因此最终发布字节数可能
不同。本地重打包文件只用于测量，未作为新运行环境发布或安装到设备。

ARM64 原生库解压后另占 225.94 MiB。按 APK、原生库、部署文件和约 97 MiB 的
ONNX + LiteRT 模型缓存估算，旧轻量包完整部署约 2.8 GiB；旧完整包约 3.6 GiB。
模型缓存按已测试全部四个识别器估计，实际取决于使用情况；驱动缓存和日志另计。
完整 APK 内保留压缩归档，部署后还会生成解压文件，因此下载体积不等于安装占用。

### 实现

- OCR 模型资源允许 APK 压缩。加载继续通过 `assets.open()` 解压、校验 SHA-256，
  再从模型缓存推理。权重、原始 CPU 路径和各厂商运行库保持原样。
- CI 安装系统依赖后执行 `apt-get clean` 并移除包索引缓存。基线缓存共
  208,141,232 字节，其中下载包 86,397,398 字节，两个索引共 121,743,834 字节。
- `compact-runtime.py` 复制再裁剪 Python ELF，避免修改 uv 硬链接缓存。每个文件
  裁剪前后比较完整动态符号表，改变则构建失败。本地用 NDK LLVM 工具检查了
  468 个 ELF，320 个文件共减少 134,948,937 字节；报告写入构建清单。
- CI 裁剪后检查 Numba 计算、SciPy 计算、uvloop 事件循环、AP 导入，并对四个
  原始 ONNX 识别器执行 CPU 前向，拒绝空输出和 NaN/Inf。
- 已安装运行环境在 App 启动 AP 会话前清理固定 APT 缓存名与 `.deb` 文件；
  路径必须留在 rootfs 内，不跟随文件软链接。升级 APK 即可回收旧镜像安装缓存。
- OCR 工作进程首次加载模型前回收不在当前清单中的旧哈希缓存和遗留临时文件。
  当前 ONNX、LiteRT、MNN 哈希均保留，AP 配置与模型目录不参与清理。

### 验证和诊断

Release 编译、R8 保留检查、签名校验和 APK OCR 资源校验均通过。模型资源校验
回归 17 项、OCR overlay 回归 15 项、真实 ELF 回归 2 项均通过；四个原始识别器
在 WSL CPU 上输出有限数值，重打包归档通过 xz 完整性检查。
本次 ADB 无连接设备，尚未对新测试 APK 做设备端推理和缓存清理回归；ARM64
裁剪库的动态接口已检查，真实执行还需原生 CI / 设备验证。

```bash
python tools/adb-debug.py storage-status --output storage-before.json
python tools/adb-debug.py runtime-start
python tools/adb-debug.py ocr-test-all
python tools/adb-debug.py storage-status --output storage-after.json
```

`storage-status` 只读取文件元数据，分别报告逻辑字节和分配块字节；固定分类覆盖
APK、原生库、运行环境、APT 缓存、Python 依赖、OCR 缓存与 App 缓存。软链接
不跟随、硬链接只计一次，失败条目单独计数。数据库、偏好文件和目录块不在扫描范围。

测试包：`AzurPilot-1.2.117-size-test.apk`，251,636,245 字节，SHA-256：
`98e3be48285fa16bd3cb13219d9cbc5ffb03be989784009d40159b9115eeda3d`。

## English

### Measurement scope

The 2026-10-08 baseline uses the local `1.2.116-mtk-ocr-test` APK and the rolling release's
`1.2.109` ARM64 runtime. Its SHA-256 matches `latest.json`:
`8cfda788d9aae50a62c3f3831c139cebf8367cda52d011a4ad30afddc3079325`.
The baseline full ARM64 APK is 1,153,571,667 bytes, about 1.07 GiB.

| Item | Before | After | Reduction |
|---|---:|---:|---:|
| Universal slim test APK | 251.92 MiB | 239.98 MiB | 11.94 MiB |
| Compressed ARM64 runtime | 864.98 MiB | 735.43 MiB | 129.56 MiB |
| Deployed runtime files | 2,298.51 MiB | 1,971.31 MiB | 327.20 MiB |

The runtime's after values come from repacking the same published archive locally: removing
APT caches and compacting Python libraries while retaining other files and tar permissions.
Deployed bytes include the three hard links materialized as copies by the current extractor,
excluding directory blocks, filesystem overhead, and configuration/log growth. Production CI
uses native ARM64/x86_64 runners and GNU binutils and performs real imports and OCR inference;
final published bytes may differ. The benchmark archive was neither published nor installed.

Extracted ARM64 native libraries add 225.94 MiB. APK, libraries, runtime files and approximately
97 MiB of ONNX/LiteRT model caches yield an estimated 2.8 GiB for the old slim installation or
3.6 GiB for the old full installation. Model caches assume all four recognizers were tested;
actual usage varies, and driver caches/logs are additional. The full APK retains its compressed
archive after deployment creates extracted files, so download size differs from disk usage.

### Implementation

- APK compression is enabled for OCR resources. Models still stream through `assets.open()`,
  undergo SHA-256 verification, and run from their cache. Weights, original CPU inference,
  and vendor runtimes remain intact.
- CI runs `apt-get clean` and removes package index caches after installing dependencies.
  Baseline caches total 208,141,232 bytes: 86,397,398 bytes of packages and 121,743,834 of indexes.
- `compact-runtime.py` copies before stripping Python ELF files, protecting uv hard links.
  Full dynamic symbol tables must remain identical or the build fails. Local NDK LLVM checks
  covered 468 ELF files; 320 files saved 134,948,937 bytes. Reports enter the build manifest.
- After compaction, CI checks Numba/SciPy computations, a uvloop event loop, AP imports and
  real CPU inference for four original ONNX recognizers, rejecting empty or non-finite outputs.
- Before starting AP sessions, the app removes fixed APT cache names and `.deb` files from
  existing runtimes. Paths must stay within rootfs and file symlinks are skipped. APK upgrades
  can reclaim old installation caches.
- Before loading its first model, the OCR worker removes obsolete hash caches and temporary
  files. Current ONNX, LiteRT and MNN hashes remain; AP configuration/model directories are excluded.

### Validation and diagnostics

Release compilation, R8 keep checks, signing checks and APK OCR asset verification passed.
Asset regression tests (17), OCR overlay tests (15), and real ELF tests (2) passed. All four
original recognizers produced finite outputs on WSL CPU, and xz integrity checks passed for
the benchmark archive. ADB had no connected device, so phone inference and cache cleanup
have not been retested with this APK. ARM64 compacted libraries passed dynamic interface
checks; native CI/device execution remains required.

```bash
python tools/adb-debug.py storage-status --output storage-before.json
python tools/adb-debug.py runtime-start
python tools/adb-debug.py ocr-test-all
python tools/adb-debug.py storage-status --output storage-after.json
```

`storage-status` reads metadata only, reporting logical bytes and allocated block bytes for
fixed APK, library, runtime, APT cache, dependency, OCR cache and app cache categories. It skips
symlinks, counts hard links once and records unreadable entries. Databases, preferences and
directory blocks are excluded.

Test APK: `AzurPilot-1.2.117-size-test.apk`, 251,636,245 bytes, SHA-256:
`98e3be48285fa16bd3cb13219d9cbc5ffb03be989784009d40159b9115eeda3d`.
