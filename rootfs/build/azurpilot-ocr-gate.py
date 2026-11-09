#!/usr/bin/env python3
"""对 AzurPilot 使用的 ONNX OCR 模型做真实 CPU 推理，充当 rootfs 的构建质量门。

由 build-azurpilot.sh 装入 rootfs（/opt/azurpilot/azurpilot-ocr-gate.py），须在
rootfs 内部用其 venv Python 执行（模型与 onnxruntime 都只存在于 rootfs 内）：
原始权重仅用于构建阶段；逐一加载六个模型并喂入零张量，任一模型缺失、无输出或含非有限值
即抛错、非零退出。

Runs a real CPU inference pass over the ONNX OCR models used by AzurPilot as a
quality gate for the rootfs build.

Installed into the rootfs by build-azurpilot.sh
(/opt/azurpilot/azurpilot-ocr-gate.py); it must run inside the rootfs with its
venv Python (both the models and onnxruntime only exist there). Each model is
loaded and fed a zero tensor through one forward pass; a missing model, empty
output, or non-finite values raise and exit non-zero.
"""

from pathlib import Path

import numpy as np
import onnxruntime as ort


ROOT = Path(__file__).resolve().parent
# 在删除源权重前检查五个识别器和检测器；发布镜像不再执行此开发对照。
MODELS = (
    'bin/ocr_models/ppocr-v6/PP-OCRv6_tiny_rec.onnx',
    'bin/ocr_models/ppocr-v6/PP-OCRv6_small_rec.onnx',
    'bin/ocr_models/ppocr-v6/PP-OCRv6_medium_rec.onnx',
    'bin/ocr_models/azur_lane/alocr-en-us-v2.6.nvc.onnx',
    'bin/ocr_models/zh-CN/alocr-zh-cn-v3.dtk.onnx',
    'bin/ocr_models/det/PP-OCRv6_tiny_det.onnx',
)


def main():
    """逐一推理 MODELS 中的模型，校验输出非空且全部元素有限。

    Runs every model in MODELS in turn and checks its outputs are non-empty and
    finite.

    Raises:
        FileNotFoundError: 模型文件不存在。/ A model file is missing.
        RuntimeError: 推理无输出或输出含 NaN/Inf。/ Inference produced no output
            or contains NaN/Inf.
    """
    for relative in MODELS:
        model = ROOT / relative
        if not model.is_file():
            raise FileNotFoundError(model)
        options = ort.SessionOptions()
        # 3 = 只报错误：冒烟推理时静默 ONNX Runtime 的 INFO/WARNING 日志。
        options.log_severity_level = 3
        options.intra_op_num_threads = 2
        session = ort.InferenceSession(str(model), sess_options=options,
                                       providers=['CPUExecutionProvider'])
        source = session.get_inputs()[0]
        # 检测器的空间尺寸按 32 对齐，不能把动态高度设成 1。
        baseline = (1, 3, 64, 64) if '_det.onnx' in relative else (1, 3, 48, 320)
        shape = tuple(d if isinstance(d, int) else baseline[index]
                      for index, d in enumerate(source.shape))
        inputs = np.zeros(shape, dtype=np.float32)
        output = session.run(None, {source.name: inputs})
        if not output or not all(np.isfinite(value).all() and value.size for value in output):
            raise RuntimeError(f'OCR model inference invalid: {relative}')
        print(f'OCR_OK {relative}: {[value.shape for value in output]}')


if __name__ == '__main__':
    main()
