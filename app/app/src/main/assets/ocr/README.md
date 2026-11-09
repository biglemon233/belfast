# 内置 OCR 权重 / Bundled OCR weights

权重来自 [AzurPilot](https://github.com/wess09/AzurPilot/tree/1841cb1941751a81ab70668b4d2383c369c4506e/bin/ocr_models)，
固定提交 `1841cb1941751a81ab70668b4d2383c369c4506e`。来源仓库使用 AGPL-3.0；
`upstream-LICENSE.txt` 保留其许可文本。原始权重的 SHA-256、张量元数据、转换产物的
SHA-256 及 CPU 对照误差在 `manifest.json` 中。

`litert/` 是用 `app/scripts/prepare_ocr_models.py` 从原始 ONNX 转出的 FP32 模型。
识别器固定批次为 1、输入高度 48、宽度 320，宿主逐张处理批次；其他宽度和检测模型
使用原始 ONNX。字符字典和 CTC 解码仍由 AzurPilot 提供。

`mnn/` 是用 `app/scripts/prepare_hiai_models.py` 转出的 FP32 海思识别模型，输入输出
契约与 LiteRT 转换一致。MNN CPU 对照验证与首次 NPU 请求均要求最大绝对误差不超过
`0.01` 且每个时间步字符分类一致。

厂商运行库在构建时下载，版本与校验和在 `runtime.json` 中；QNN、NeuroPilot 的许可
文本位于 `licenses/`。`hiai-runtime.json` 记录 HiAI 客户端和 MNN 源码来源，
对应许可及 MNN 使用的 FlatBuffers、half 许可也随 APK 打包。LiteRT、MNN 使用
Apache-2.0，ONNX Runtime 使用 MIT。

`test/sample.png` 是本项目生成的 320×48 白底数字「12345」，用于模型对照和 AP 接线测试。
图片使用系统 Arial 字体绘制，不包含字体文件；测试结果不代替实际游戏识别验证。

Weights originate from [AzurPilot](https://github.com/wess09/AzurPilot/tree/1841cb1941751a81ab70668b4d2383c369c4506e/bin/ocr_models)
at commit `1841cb1941751a81ab70668b4d2383c369c4506e`. The source repository uses AGPL-3.0;
`upstream-LICENSE.txt` preserves its license. `manifest.json` records original checksums,
tensor metadata, converted checksums, and CPU comparison errors.

`litert/` contains FP32 models converted from ONNX by `app/scripts/prepare_ocr_models.py`.
Recognizers use batch 1, height 48, and width 320; the host splits batches. Other widths and
the detector use original ONNX. AzurPilot retains character dictionaries and CTC decoding.

`mnn/` contains FP32 HiAI recognition models produced by `app/scripts/prepare_hiai_models.py`
with the same IO contract. MNN CPU comparisons and the first NPU request require absolute
error at most `0.01` and identical character predictions at every timestep.

Vendor libraries download during builds; `runtime.json` records versions and checksums.
QNN and NeuroPilot licenses reside in `licenses/`. `hiai-runtime.json` records HiAI clients
and MNN source provenance. Corresponding licenses and MNN's FlatBuffers and half licenses
ship in the APK. LiteRT and MNN use Apache-2.0; ONNX Runtime uses MIT.

`test/sample.png` is a project-generated 320×48 white image containing "12345", used for
model comparisons and AP integration tests. It was drawn with the system Arial font;
no font file is bundled. Test results do not replace game-image recognition validation.
