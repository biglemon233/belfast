#!/usr/bin/env python3
"""转换并对照 HiAI 路径使用的 MNN OCR 模型。

保持 AP 的 NCHW 输入与原始输出，使用 MNN 3.6.1 的 FP32 转换。卷积和归一化融合
会改变舍入顺序，因此转换验收使用独立的 0.01 最大绝对误差及逐时间步相同字符。
此检查只验证 CPU 转换，设备首次请求还需对照原始 ONNX，并另做 NPU 性能分析。

Converts FP32 OCR models with MNN 3.6.1 while preserving AP's NCHW input and output contract.
Fused convolution/normalization changes rounding, so conversion checks use a separate
0.01 maximum absolute error and identical per-step character predictions. CPU conversion
checks do not replace first-request ONNX comparisons and hardware profiling on devices.
"""

import argparse
import hashlib
import json
from pathlib import Path

import MNN
import _tools
import numpy as np
import onnx
import onnxruntime as ort

ROOT = Path(__file__).resolve().parents[2]
ASSETS = ROOT / "app/app/src/main/assets/ocr"


def compare(source, converted):
    """对照完整概率张量和字符序列。 / Compares full probability tensors and character sequences."""
    interpreter = MNN.Interpreter(str(converted))
    session = interpreter.createSession({"backend": "CPU", "numThread": 2, "precision": "high"})
    inp = interpreter.getSessionInput(session)
    original = ort.InferenceSession(str(source), providers=["CPUExecutionProvider"])
    maximum = 0.0
    shape = (1, 3, 48, 320)
    rng = np.random.default_rng(20261007)
    for values in [np.zeros(shape, np.float32), np.full(shape, -1.0, np.float32),
                   rng.uniform(-1, 1, shape).astype(np.float32)]:
        host_input = MNN.Tensor(shape, MNN.Halide_Type_Float, values, MNN.Tensor_DimensionType_Caffe)
        inp.copyFrom(host_input)
        interpreter.runSession(session)
        result = interpreter.getSessionOutput(session)
        host_output = MNN.Tensor(result.getShape(), MNN.Halide_Type_Float,
                                 np.zeros(result.getShape(), np.float32), MNN.Tensor_DimensionType_Caffe)
        result.copyToHostTensor(host_output)
        actual = np.array(host_output.getData(), np.float32).reshape(result.getShape())
        expected = original.run(None, {original.get_inputs()[0].name: values})[0]
        if actual.shape != expected.shape or not np.isfinite(actual).all():
            raise ValueError("MNN OCR output shape or values changed")
        np.testing.assert_allclose(actual, expected, rtol=0, atol=0.01)
        np.testing.assert_array_equal(actual.argmax(-1), expected.argmax(-1))
        maximum = max(maximum, float(np.max(np.abs(actual - expected))))
    return {"asset": converted.relative_to(ASSETS).as_posix(),
            "sha256": hashlib.sha256(converted.read_bytes()).hexdigest(),
            "converter": "MNN==3.6.1", "input_layout": "nchw", "input_shape": list(shape),
            "output_shape": list(actual.shape), "cpu_max_abs_error": maximum}


def main():
    """生成资产和转换清单，或复验现有资产。 / Generates assets and manifests or verifies existing assets."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify-only", action="store_true")
    args = parser.parse_args()
    manifest_path = ASSETS / "manifest.json"
    manifest = json.loads(manifest_path.read_text())
    for spec in manifest["models"]:
        if not spec.get("npu_supported"):
            continue
        source = ROOT / ".tmp/ocr-sources" / spec["asset"].removeprefix("models/")
        if hashlib.sha256(source.read_bytes()).hexdigest() != spec["sha256"]:
            raise ValueError("Original OCR model checksum changed")
        converted = ASSETS / "mnn" / (source.stem + ".mnn")
        if not args.verify_only:
            work = ROOT / ".tmp/ocr-mnn-convert"
            work.mkdir(parents=True, exist_ok=True)
            model = onnx.load(source)
            for dimension, size in zip(model.graph.input[0].type.tensor_type.shape.dim,
                                       (1, 3, 48, 320), strict=True):
                dimension.ClearField("dim_param")
                dimension.dim_value = size
            del model.graph.value_info[:]
            static = work / source.name
            onnx.save(model, static)
            converted.parent.mkdir(parents=True, exist_ok=True)
            # 直接调用转换器，避免 Python CLI 的遥测和自动安装依赖行为。
            _tools.mnnconvert(["MNNConvert", "-f", "ONNX", "--modelFile", str(static),
                               "--MNNModel", str(converted), "--bizCode", "AzurPilot-OCR"])
        conversion = compare(source, converted)
        if args.verify_only:
            if spec["mnn"]["sha256"] != conversion["sha256"]:
                raise ValueError("MNN OCR checksum changed")
        else:
            spec["mnn"] = conversion
        print(f"HIAI_CONVERSION_OK {converted.name}: {conversion['cpu_max_abs_error']:.8g}", flush=True)
    if not args.verify_only:
        manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
