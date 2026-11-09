# MT6985 OCR 真机记录 / MT6985 OCR device validation

## 中文

### 环境与范围

2026-10-08，连接的 corot / MT6985 设备，Android 16，包名 `com.azurpilot.ghio`。
最新测试包为 1.2.116-ocr-test，versionCode `1791463529`，R8 Release，使用本机测试签名。
轻量 APK 沿用设备已安装的 rootfs。LiteRT 2.1.0rc1、NeuroPilot 8.0.10 / 9.0.3；未使用 NNAPI。

这是该设备的软件调用和测试图片记录。没有 NPU 硬件性能分析、完整游戏图片准确率或功耗验收，
不能推广为全部芯片均可用或已经证明低功耗。

### 已修复的问题

- MTK dispatch 的全局 adapter 不适合同时存活的多模型会话；切换模型前释放旧 NPU 会话。
- small / 中文的末尾大字典 Softmax 路径返回 NaN。将末尾 Softmax 拆到宿主 CPU 后，
  两模型返回有限概率并正确解码测试图片。此对照定位到该路径，未证明厂商内部具体溢出原因。
- 把静态 FP32 的 `RELU_0_TO_1` 等价改写为 MAXIMUM / MINIMUM，四模型的 dispatch
  分区从原来的 6 / 31 个降至 1 个。原始 ONNX 权重和公开输入输出不变。
- 大字典 Softmax 使用原生稳定计算：逐行减最大值、FP32 指数、FP64 分母累加。
- R8 保留 ORT 和 LiteRT JNI 回调类，避免异常处理期间因类被移除而原生退出。
- CPU 基准使用独立原 ONNX 会话；先验证所有执行节点为 `CPUExecutionProvider`，
  结束分析后再计时。AP 业务和诊断调用分别记录，诊断不覆盖 `last_ap_call`。

### 1.2.116 测试结果

输入为内置数字图片预处理后的 `[1,3,48,320]`，预热 3 次、计时 20 次。
耗时包含模型推理、张量复制和有限值检查，不包含 AP 图像处理、回环通信、CTC 解码或冷编译。
CPU 使用两个推理线程，每个模型保留逐次样本；设备温度、负载和频率会影响结果。

| 模型 | 加速平均 ms | 独立 ONNX CPU 平均 ms | 最大分数差 | 时间步字符一致 | dispatch 分区 |
|---|---:|---:|---:|---|---:|
| PP-OCRv6 tiny | 10.15 | 15.59 | 0.0565013 | 是 | 1 |
| PP-OCRv6 small | 33.58 | 71.07 | 0.0008625984 | 是 | 1 |
| AlOCR 英文 v2.6 | 15.93 | 52.93 | 0.022403985 | 是 | 1 |
| AlOCR 中文 v3 | 39.25 | 67.58 | 0.008241594 | 是 | 1 |

CPU 分析节点数分别为 70、164、299、299，均仅有 CPUExecutionProvider。
NPU 成功依据实际 dispatch 分区与成功返回；`npu_hardware_profile_verified=false`。
small / 中文的终端 Softmax 仍在 CPU；不能把单个分区解释为整个 OCR 流程都在 NPU。
MTK 同时仅保留一个 NPU 模型，跨模型切换有秒级冷编译成本。

`ocr-ap-config-test` 使用保存的 `ap` 实例配置，经正常 `AlOcr.ocr` 入口测试
azur_lane / cn / jp / tw：开启加速时均走 AndroidSession → 宿主 NPU 路径；关闭时均走
AndroidSession → 宿主原 ONNX CPU。两种选择都解码出 `12345`，实例配置未修改，未启动游戏任务。
这证明配置与 AP 接线，测试标记仍为诊断，不冒充正在运行的游戏业务。

### 用户控制与分数差

**设置 → OCR 加速 → 使用硬件加速** 默认开启。关闭后释放硬件会话，后续调用直接走 CPU，
状态记录 `cpu_reason=user_disabled`。ADB 使用相同入口：

```bash
python tools/adb-debug.py ocr-hardware-acceleration off
python tools/adb-debug.py ocr-hardware-acceleration on
```

已验证关闭后四个模型均为 ONNX CPU、零 dispatch 分区；强制停止并由 ADB 冷启动后，
关闭选择仍保留，英文测试继续使用 CPU。重新开启后英文恢复 NPU dispatch 推理。
最终 APK 再次通过正常 AP 接线接口验证关闭 / 开启的热切换；关闭时联合 NPU+GPU
诊断拒绝加载硬件，重新开启后英文识别恢复 NPU，文字均为 `12345`。

1.2.115 的真实 AP 英文调用曾出现最大分数差 `0.21335483`，但 40 个时间步的字符选择
全部一致。当时因 `0.15` 门槛回退；此数值不是字符错误率，也不足以断言认错字。
按用户要求，1.2.116 移除业务 CPU 对照和分数、字符差异回退门槛。诊断仍展示对照信息，
由用户自行决定是否关闭加速。驱动、库加载、编译、推理及无效输出等实际错误保留自动 CPU 回退，
不支持的尺寸或芯片保留 CPU。原生崩溃禁用记录仍保留，开关不能绕过已知崩溃保护。

### 软件验证与限制

Debug / Release 打包、16 类 R8 保留检查、APK 模型与运行库哈希及签名校验均通过。
文案检查为中文 / 英文各 612 项，零错误、零警告。15 项协议与配置测试、17 项打包测试、
20 项秩 / Softmax / clamp 转换测试通过；生产 JNI 的 Linux JVM 回归包括稳定 Softmax、
句柄、缺失驱动、APUSys 和 adapter 选择检查。四模型的转换 CPU 对照最大误差约 `4.6e-5`，
这项转换验证的严格门槛保留，与运行时用户选择分开。

NPU+GPU 联合选项仅提供实验诊断，`gpu_requested` 不表示 GPU 确实执行。1.2.114 在旧图上的
联合委派曾失败；没有因此默认开启 GPU。当前没有 GPU 实际分区或硬件能耗验收。
高通、HiAI 尚未在本次连接设备上验证，玄戒等缺失运行库的平台限制见
[OCR 加速说明](ocr-acceleration.md)。

## English

### Environment and scope

On 2026-10-08, tests used the connected corot / MT6985 phone running Android 16, package
`com.azurpilot.ghio`. The latest APK is 1.2.116-ocr-test, versionCode `1791463529`, an R8 Release
build with the local test signing key. The slim APK reuses the installed rootfs. Runtime versions
are LiteRT 2.1.0rc1 and NeuroPilot 8.0.10 / 9.0.3; NNAPI is not used.

These are software execution and fixture results on this device. There is no NPU hardware
profiling, complete game-image accuracy evaluation, or energy validation. Results do not
establish support for every chip or demonstrate low power consumption.

### Resolved issues

- Close the previous MTK NPU session before switching models because dispatch uses a global adapter.
- The large-dictionary terminal Softmax path produced NaN for small and Chinese. Moving terminal
  Softmax to host CPU produced finite probabilities and correct fixture text. This isolates the
  failing path without establishing the vendor's internal overflow mechanism.
- Replace static FP32 `RELU_0_TO_1` with equivalent MAXIMUM / MINIMUM operations, reducing
  dispatch partitions from 6 / 31 to 1 across the four models without changing original ONNX
  weights or public input/output contracts.
- Stable native Softmax subtracts each row maximum, uses FP32 exponentials and FP64 summation.
- Preserve ORT and LiteRT JNI callback classes through R8 to prevent native exits in exception handling.
- Use isolated original ONNX CPU baselines, verify all execution nodes use CPUExecutionProvider,
  and end profiling before timing. Keep AP and diagnostic records separate, preserving `last_ap_call`.

### Version 1.2.116 results

The bundled digit image produces `[1,3,48,320]` input. Each model receives three warmups and
twenty timed runs. Timings include model inference, tensor copies, and finite-value checks,
excluding AP image processing, loopback transport, CTC decoding, and cold compilation.
CPU uses two inference threads and retains raw samples. Temperature, load, and frequency affect results.

| Model | Accelerated mean ms | Isolated ONNX CPU mean ms | Maximum score difference | Timestep agreement | Dispatch partitions |
|---|---:|---:|---:|---|---:|
| PP-OCRv6 tiny | 10.15 | 15.59 | 0.0565013 | Yes | 1 |
| PP-OCRv6 small | 33.58 | 71.07 | 0.0008625984 | Yes | 1 |
| AlOCR English v2.6 | 15.93 | 52.93 | 0.022403985 | Yes | 1 |
| AlOCR Chinese v3 | 39.25 | 67.58 | 0.008241594 | Yes | 1 |

CPU profiling reported 70, 164, 299, and 299 nodes respectively, all using CPUExecutionProvider.
NPU evidence is actual dispatch partitions plus successful inference;
`npu_hardware_profile_verified=false`. Small and Chinese terminal Softmax remains on CPU.
One dispatch partition does not mean the entire OCR pipeline runs on NPU. MTK retains only
one NPU model at a time, so model switches incur seconds of cold compilation.

`ocr-ap-config-test` uses the saved `ap` instance and normal `AlOcr.ocr` for azur_lane / cn / jp / tw.
With acceleration on, all use AndroidSession → host NPU; with it off, all use AndroidSession →
original host ONNX CPU. Both choices decode `12345` without modifying instance settings or
starting game tasks. This verifies configuration and integration, remaining diagnostics rather
than live game business evidence.

### User control and score differences

**Settings → OCR acceleration → Use hardware acceleration** defaults to on. Turning it off
releases hardware sessions and sends subsequent calls directly to CPU, with
`cpu_reason=user_disabled`. ADB uses the same setting:

```bash
python tools/adb-debug.py ocr-hardware-acceleration off
python tools/adb-debug.py ocr-hardware-acceleration on
```

All four model tests used ONNX CPU with zero dispatch partitions after disabling. Following
force-stop and headless cold startup, the choice remained off and English still used CPU.
Re-enabling restored English NPU dispatch inference.
The final APK also passed off/on hot switching through the normal AP integration interface.
Mixed NPU/GPU diagnostics refused hardware while off; re-enabling restored English NPU
inference, with `12345` text in both modes.

A real AP English call on 1.2.115 differed by `0.21335483` at most, with identical character
choices for all forty timesteps. The old `0.15` gate caused fallback; the number is not a
character error rate and does not establish misrecognition. At the user's request, 1.2.116
removes business CPU comparisons and score/character difference gates. Diagnostics still
report comparisons so users decide whether to disable acceleration. Actual driver, library,
compilation, inference, and invalid-output errors retain automatic CPU fallback, as do unsupported
dimensions or chips. Native crash gates remain; the switch cannot bypass known crash protection.

### Software checks and limitations

Debug / Release assembly, the sixteen-class R8 guard, APK model/runtime hashes, and signature
verification passed. String checks found 612 Chinese and English keys each, with no errors or
warnings. Fifteen protocol/configuration tests, seventeen packaging tests, and twenty rank /
Softmax / clamp tests passed. Production JNI Linux JVM regression covers stable Softmax,
handles, missing drivers, APUSys, and adapter selection. Converted-model CPU comparisons
have maximum error about `4.6e-5`; strict conversion checks remain separate from runtime choice.

NPU+GPU options remain experimental diagnostics. `gpu_requested` is not proof of execution;
combined delegation failed on the older graph in 1.2.114 and GPU was not enabled by default.
Actual GPU partition and hardware energy validation are absent. Qualcomm and HiAI were not
tested on the connected device; Xring and other missing-runtime limitations are documented in
[OCR acceleration](ocr-acceleration.md).
