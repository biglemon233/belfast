# ADB 调试入口 / ADB diagnostics

## 中文

### 无界面调用

App 内置 `content://<applicationId>.debug` 诊断入口，Debug 和 Release 包均可用。
标准包名为 `com.azurpilot.ghio`。连接并授权 ADB 后，命令会启动 App 主进程，不打开
Activity，也不需要读取控制口令、root 或 Shizuku。OCR 命令沿用应用和 AP 同用的
认证回环接口，推理仍在独立 `:ocr` 进程中执行。

```bash
adb shell content call --uri content://com.azurpilot.ghio.debug --method help
adb shell content call --uri content://com.azurpilot.ghio.debug --method ocr-status
adb shell content call --uri content://com.azurpilot.ghio.debug --method ocr-test-all
```

命令同步等待结果。JSON 位于返回 Bundle 的 `json` 字段；`ok=true` 只表示诊断命令
完成，实际后端以 `result.backend` 或模型状态中的 `backend` 为准。CPU 测试成功不代表
NPU 可用，硬件性能验证字段仍需单独检查。

| 命令 | 参数 | 行为 |
|---|---|---|
| `help` | 无 | 列出支持的命令 |
| `ocr-status` | 无 | 查询 SoC、版本、实际后端、回退原因和调用计数 |
| `ocr-hardware-acceleration` | `on` / `off` | 保存并应用 App 硬件加速开关，不重启 AP |
| `ocr-test` | 原 ONNX SHA-256 | 测试单个内置识别器，包含首次和稳定耗时、CPU 对照 |
| `ocr-test-all` | 无 | 串行测试五个内置识别器，每个完成后保存进度 |
| `ocr-test-cpu` | 原 ONNX SHA-256 | 独立 LiteRT CPU 会话：只请求 CPU、无 NPU 分区，预热 3 次、测量 20 次 |
| `ocr-test-gpu-softmax` | 源模型 SHA-256 | 单测 GPU 末尾 Softmax，含上传、同步和读回，不依赖 NPU |
| `ocr-test-mixed` | 原 ONNX SHA-256 | 对照 NPU+GPU 联合委派，报告实际 NPU 分区；不改业务策略 |
| `ocr-ap-test` | 原 ONNX SHA-256 | 在已运行 AP 中调用实际识别器并检查 `12345` |
| `ocr-ap-config-test` | 无 | 读取当前运行或选中实例的保存配置，走正常 AlOcr 入口测试四种语言 |
| `runtime-status` | 无 | 查询运行时阶段和启动意图 |
| `storage-status` | 无 | 统计 APK、原生库、运行环境、依赖和缓存；软链接不跟随、硬链接不重复计数 |
| `runtime-start` | 无 | 启动已安装运行时，最多等待 120 秒；不启动游戏任务 |
| `debug-last` | 无 | 读取最近命令的进度或最终结果；无需等待测试锁 |
| `debug-export` | 无 | 导出 OCR 阶段、原生日志和最近结果，返回 ZIP 路径 |

模型哈希来自 `ocr-status`。直接调用时使用 `--arg <sha256>`。AP 接线测试需要先
`runtime-start`，运行时尚未安装时会报告失败，不在诊断入口下载或部署 rootfs。
长命令串行，重叠请求返回忙；`debug-last` 可在另一终端查询进度。

### 本地命令行工具

仓库内的 `tools/adb-debug.py` 只依赖 Python 标准库和 Android SDK 的 adb，自动解析
Bundle 并输出 JSON，支持模型简称 `tiny`、`small`、`en`、`zh`、`pro`、`det`。
检测器不能使用识别图片单测；它通过 AP 检测流程调用宿主。

```bash
python tools/adb-debug.py ocr-status
python tools/adb-debug.py storage-status --output storage.json
python tools/adb-debug.py ocr-hardware-acceleration off
python tools/adb-debug.py ocr-hardware-acceleration on
python tools/adb-debug.py ocr-test-all --output results.json --pull-logs ocr-debug.zip
python tools/adb-debug.py runtime-start
python tools/adb-debug.py ocr-ap-test en --output ap-en.json
python tools/adb-debug.py ocr-ap-config-test --output ap-config.json
python tools/adb-debug.py ocr-test-cpu tiny --output cpu-tiny.json
python tools/adb-debug.py ocr-test-gpu-softmax zh --output gpu-softmax.json
python tools/adb-debug.py ocr-test-mixed en --output mixed-en.json
python tools/adb-debug.py ocr-test zh
python tools/adb-debug.py debug-last
```

多台设备时加 `--serial <设备序列号>`；可用 `--adb <路径>` 和 `--package <包名>`
指定 SDK 或其他构建变体。失败返回非零退出码，仍输出结构化原因；读取运行中进度
不视为命令失败。

### 结果与权限

模型计时是固定 `[1,3,48,320]` 输入的预热后推理，包含张量复制和有限数值检查，
不包含 AP 图像处理、回环传输或 CTC 解码。CPU 对照使用独立 LiteRT CPU 会话，
只请求 CPU、不提供厂商 provider，并校验 NPU 分区为零；逐次结果在 `cpu_samples_ms`。
GPU 末尾 Softmax 的耗时包含上传、同步和读回，不能把异步提交耗时当成推理耗时。

硬件加速默认开启。`ocr-hardware-acceleration off` 与设置页开关使用同一保存入口，
后续宿主请求直接走 CPU，重启仍生效。开启时只因实际硬件运行错误或不支持回退；
分数差与字符对照仅作诊断，不触发回退。`hardware_acceleration_enabled` 返回当前选择，
`cpu_reason=user_disabled` 与驱动故障分别报告。关闭时 `ocr-test-mixed` 拒绝加载硬件。

`ocr-ap-test` 显式选模型，证明接线；`ocr-ap-config-test` 保留实例的模型选择和配置文件，
证明保存配置能走哪条路径。两者都标记为诊断，不证明正在运行的游戏任务已调用 NPU。
业务证据以 `model_status.ap_requests`、`last_ap_call` 为准，测试不会覆盖最后业务记录。
NCNN 配置在 Android 转接宿主；动态尺寸使用 LiteRT CPU。未知模型和连接错误明确报告失败。

`ocr-test-mixed` 只请求联合委派；`gpu_requested=true` 不等于 GPU 实际执行。
在取得 GPU 分区证据和性能、功耗对照前，`gpu_execution_verified` 与
`power_efficiency_verified` 保持 `false`，该字段只描述联合委派实验。末尾 GPU Softmax 另记录 `terminal_softmax_backend`、
`gpu_renderer` 和包含同步的耗时；单测成功不等于 AP 业务调用。

最近进度写到 `externalFiles/debug/adb/latest.json`，最近最终结果写到 `result.json`。
写入采用同目录临时文件和原子替换。日志包 `ocr-debug.zip` 只收集已有 OCR 阶段文件、
有界原生快照和诊断结果，不包含用户配置、输入张量或控制口令。直接 `debug-export`
后可使用其 `archive_path` 执行 `adb pull`，命令行工具的 `--pull-logs` 自动完成此步骤。

入口在 Manifest 声明系统 `android.permission.DUMP`，并在 `call` 内检查真实 Binder
调用者。[Android 的 call 接口说明](https://developer.android.com/reference/android/content/ContentProvider#call(java.lang.String,java.lang.String,android.os.Bundle))
要求提供者自行执行命令权限检查，不能仅依赖读写声明。入口不接受任意代码、路径或
文件访问，也不允许重置原生崩溃禁用记录；普通应用无此系统权限。

## English

### Headless calls

Both Debug and Release APKs include `content://<applicationId>.debug`; the standard package
is `com.azurpilot.ghio`. Once ADB is connected and authorized, commands start the main app
process without an Activity, reading control tokens, root, or Shizuku. OCR commands use the
same authenticated loopback API as the app and AP, with inference isolated in `:ocr`.

```bash
adb shell content call --uri content://com.azurpilot.ghio.debug --method help
adb shell content call --uri content://com.azurpilot.ghio.debug --method ocr-status
adb shell content call --uri content://com.azurpilot.ghio.debug --method ocr-test-all
```

Calls wait synchronously and return JSON in the Bundle's `json` field. `ok=true` means the
diagnostic command completed; inspect `result.backend` or model status for the actual backend.
A passing CPU test does not establish NPU availability or hardware profiling.

| Command | Argument | Behavior |
|---|---|---|
| `help` | None | Lists supported commands |
| `ocr-status` | None | Reports SoC, versions, actual backends, fallback reasons, and counts |
| `ocr-hardware-acceleration` | `on` / `off` | Persists and applies the app acceleration switch without restarting AP |
| `ocr-test` | Original ONNX SHA-256 | Tests one bundled recognizer with cold/steady timings and CPU comparison |
| `ocr-test-all` | None | Serially tests all five recognizers, saving progress after each |
| `ocr-test-cpu` | Original ONNX SHA-256 | Times an isolated CPU-only LiteRT session without NPU partitions; 3 warmups and 20 runs |
| `ocr-test-gpu-softmax` | Source SHA-256 | Tests GPU terminal Softmax with upload/sync/readback independently of NPU |
| `ocr-test-mixed` | Original ONNX SHA-256 | Compares combined NPU/GPU delegation with actual NPU partition evidence; keeps production policy |
| `ocr-ap-test` | Original ONNX SHA-256 | Invokes the real recognizer in running AP and checks `12345` |
| `ocr-ap-config-test` | None | Tests normal AlOcr for four languages using the running or selected instance's saved settings |
| `runtime-status` | None | Reports runtime phase and start intent |
| `storage-status` | None | Measures APK, native libraries, runtime, dependencies and caches; skips symlinks and counts hard links once |
| `runtime-start` | None | Starts the installed runtime, waiting up to 120 seconds without starting game tasks |
| `debug-last` | None | Reads progress or the last result without waiting for the test lock |
| `debug-export` | None | Exports OCR stages, native logs, and recent results, returning a ZIP path |

Use a model hash from `ocr-status` as `--arg <sha256>`. Run `runtime-start` before AP integration
tests. Missing runtimes report failure; diagnostics do not download or deploy rootfs. Long
commands serialize and overlapping requests report busy. Poll `debug-last` in another terminal.

### Local CLI

`tools/adb-debug.py` needs only Python's standard library and the Android SDK adb. It parses
Bundles into JSON and accepts model aliases `tiny`, `small`, `en`, `zh`, `pro`, and `det`.
The detector runs through AP detection rather than the recognizer image test.

```bash
python tools/adb-debug.py ocr-status
python tools/adb-debug.py storage-status --output storage.json
python tools/adb-debug.py ocr-test-all --output results.json --pull-logs ocr-debug.zip
python tools/adb-debug.py runtime-start
python tools/adb-debug.py ocr-ap-test en --output ap-en.json
python tools/adb-debug.py ocr-ap-config-test --output ap-config.json
python tools/adb-debug.py ocr-test-cpu tiny --output cpu-tiny.json
python tools/adb-debug.py ocr-test-gpu-softmax zh --output gpu-softmax.json
python tools/adb-debug.py ocr-test-mixed en --output mixed-en.json
python tools/adb-debug.py ocr-test zh
python tools/adb-debug.py debug-last
```

Select devices with `--serial <serial>`; override the SDK executable or package using `--adb`
and `--package`. Failures produce a nonzero exit code and structured reason. Polling a running
command does not count as failure.

### Results and access

Model timings use a fixed `[1,3,48,320]` warm input and include tensor copies and finite-value
checks. They exclude AP image processing, loopback transport, and CTC decoding.
CPU comparisons use an isolated LiteRT session requesting CPU without a vendor provider,
with zero NPU partitions checked; `cpu_samples_ms` retains each measurement.
Terminal GPU Softmax timings include upload, synchronization, and readback rather than
only asynchronous submission.

Hardware acceleration defaults to on. `ocr-hardware-acceleration off` uses the same persistent
setting as the UI switch: subsequent host requests go directly to CPU, including after restart.
When on, only actual runtime errors or unsupported requests fall back. Score differences and
character comparisons are diagnostics only. `hardware_acceleration_enabled` reports the choice;
`cpu_reason=user_disabled` distinguishes manual CPU use from driver failure. When off,
`ocr-test-mixed` refuses to load hardware.

```bash
python tools/adb-debug.py ocr-hardware-acceleration off
python tools/adb-debug.py ocr-hardware-acceleration on
```

`ocr-ap-test` explicitly selects a model to verify integration. `ocr-ap-config-test` preserves
saved backend/model choices to verify their actual path. Both remain diagnostics and do not
prove a live game task has called NPU. Business evidence is `model_status.ap_requests` and
`last_ap_call`; tests do not overwrite the last business record. Android routes saved NCNN
settings to the host. Dynamic shapes use LiteRT CPU; unknown models and connection errors
fail explicitly.

`ocr-test-mixed` requests combined delegation. `gpu_requested=true` does not establish GPU
execution. Until GPU partition evidence and latency/power comparisons exist,
`gpu_execution_verified` and `power_efficiency_verified` stay false for that experiment.
Terminal GPU Softmax separately reports `terminal_softmax_backend`, `gpu_renderer`, and
synchronized timings. Its standalone test does not establish AP business execution.

Progress is stored in `externalFiles/debug/adb/latest.json`, with the last terminal result in
`result.json`. Writes use same-directory temporary files and atomic replacement. `ocr-debug.zip`
includes existing OCR stage files, bounded native snapshots, and diagnostic results, without
user configuration, input tensors, or control tokens. After `debug-export`, run `adb pull` with
its `archive_path`, or use the CLI's `--pull-logs` option.

The manifest requires system `android.permission.DUMP`, and `call` explicitly checks its Binder
caller. [Android's call documentation](https://developer.android.com/reference/android/content/ContentProvider#call(java.lang.String,java.lang.String,android.os.Bundle))
requires providers to enforce command permissions themselves instead of relying on read/write
declarations alone. Commands accept no arbitrary code, paths, or file access and cannot clear
native-crash gates. Ordinary apps do not hold this system permission.
