#!/usr/bin/env python3
"""转换并校验 APK 内置 OCR 模型，保留 AP 的 ONNX 输入输出契约。

在 Linux 的 Python 3.12 环境中安装 onnx2tf==2.6.9 后运行。转换后的 FP32 模型
必须通过 ONNX 与 LiteRT CPU 数值对照；该检查不代替设备上的 NPU 精度测试。

Converts and verifies bundled OCR models while preserving AP's ONNX I/O contract.
Run on Linux with Python 3.12 and onnx2tf==2.6.9. Converted FP32 models must pass
ONNX/LiteRT CPU comparisons; this does not replace NPU accuracy tests on devices.
"""

import argparse
import copy
import hashlib
import json
import subprocess
import sys
from pathlib import Path

import flatbuffers
import numpy as np
import onnx
import onnxruntime as ort
from onnxsim import simplify
from ai_edge_litert.interpreter import Interpreter
from ai_edge_litert import schema_py_generated as schema


ROOT = Path(__file__).resolve().parents[2]
ASSETS = ROOT / "app/app/src/main/assets/ocr"
UPSTREAM = "1841cb1941751a81ab70668b4d2383c369c4506e"
SOURCES = {
    "ppocr-v6/PP-OCRv6_tiny_rec.onnx": "9ef676d6ed3c88256a2d92c640c44f25b0c40947e111b14b8be8f594091563e6",
    "ppocr-v6/PP-OCRv6_small_rec.onnx": "5435fd747c9e0efe15a96d0b378d5bd157e9492ed8fd80edf08f30d02fa24634",
    "azur_lane/alocr-en-us-v2.6.nvc.onnx": "35413805f63b5fb13a0060b1cf3d5fcefb2fba6b88a3e85a1e1ae236533af5f6",
    "zh-CN/alocr-zh-cn-v3.dtk.onnx": "d0cd4977b80b1138239f88ce6b5de8e421d508fdf6da63b744e6077a4e2af0f3",
    "det/PP-OCRv6_tiny_det.onnx": "193bab7a04fca699a6c82e6abb5b81bdb28177f0abd4062552b04908dafb19f8",
}


def sha256(path):
    """返回文件内容哈希。 / Returns the file's content hash."""
    return hashlib.sha256(path.read_bytes()).hexdigest()


def node_info(node):
    """导出供 Python 会话代理使用的元数据。 / Exports metadata for the Python session proxy."""
    return {
        "name": node.name,
        "type": "tensor(float)",
        "shape": [d.dim_value or d.dim_param or None for d in node.type.tensor_type.shape.dim],
    }


def normalize_batch_matmul_ranks(data):
    """为常量矩阵补前导单维，使 BatchMatMul 两输入具有相同秩。

    只克隆静态 FP32 常量的张量描述，复用原缓冲区，不修改其他消费者或乘法选项。
    动态输入、不支持的类型和外置缓冲区拒绝改写；已经规范化的模型保持原字节。

    Prepends singleton dimensions to constant matrices so BatchMatMul input ranks match.
    Clones static FP32 tensor descriptors and shares buffers without changing other consumers
    or multiplication options. Rejects dynamic inputs, unsupported types, and external buffers;
    already normalized models retain their original bytes.
    """
    model = schema.ModelT.InitFromObj(schema.Model.GetRootAsModel(data, 0))
    changed = 0
    for graph in model.subgraphs:
        for op in graph.operators:
            code = model.operatorCodes[op.opcodeIndex]
            if max(code.builtinCode, code.deprecatedBuiltinCode) != schema.BuiltinOperator.BATCH_MATMUL:
                continue
            inputs = [graph.tensors[int(index)] for index in op.inputs]
            ranks = [len(tensor.shape) for tensor in inputs]
            if len(ranks) != 2 or not all(2 <= rank <= 4 for rank in ranks):
                raise ValueError(f"Unsupported BatchMatMul input ranks: {ranks}")
            if ranks[0] == ranks[1]:
                continue
            slot = int(ranks[1] < ranks[0])
            tensor = inputs[slot]
            buffer = model.buffers[tensor.buffer]
            if (tensor.type != schema.TensorType.FLOAT32 or tensor.isVariable or
                    buffer.data is None or len(buffer.data) == 0 or any(d <= 0 for d in tensor.shape)):
                raise ValueError("Mismatched BatchMatMul ranks require a static FP32 constant")
            aligned = copy.deepcopy(tensor)
            padding = [1] * (max(ranks) - ranks[slot])
            aligned.shape = padding + list(tensor.shape)
            if aligned.shapeSignature is not None:
                aligned.shapeSignature = padding + list(tensor.shapeSignature)
            aligned.name = (tensor.name or b"constant") + b".npu_batch_rank"
            op.inputs = list(op.inputs)
            op.inputs[slot] = len(graph.tensors)
            graph.tensors.append(aligned)
            changed += 1
    if not changed:
        return data, 0
    # 重打包会移动偏移量，不能悄悄损坏未嵌入 FlatBuffer 的权重。
    if any(getattr(buffer, "offset", 0) or getattr(buffer, "size", 0) for buffer in model.buffers):
        raise ValueError("External LiteRT buffers cannot be repacked")
    builder = flatbuffers.Builder(len(data))
    builder.Finish(model.Pack(builder), file_identifier=b"TFL3")
    return bytes(builder.Output()), changed


def normalize_clamp_activations(data):
    """把 FP32 RELU_0_TO_1 换成等价的 max(x,0) 再 min(x,1)，减少委派断点。

    原输出索引保持不变；常量按输入秩广播。只处理静态、无量化的单输入输出算子，
    不改公开输入输出、共享消费者或原始 ONNX 权重。外置缓冲区拒绝重打包。

    Replaces FP32 RELU_0_TO_1 with equivalent max(x,0), then min(x,1), reducing delegation
    barriers. Keeps original output indices and broadcasts constants at input rank. Accepts
    only static, unquantized, single-input/output operators; preserves public tensors, shared
    consumers, and original ONNX weights. Rejects repacking external buffers.
    """
    model = schema.ModelT.InitFromObj(schema.Model.GetRootAsModel(data, 0))
    changed = 0
    codes = {}
    def opcode(builtin):
        if builtin not in codes:
            code = schema.OperatorCodeT()
            code.builtinCode, code.deprecatedBuiltinCode, code.version = builtin, min(builtin, 127), 1
            codes[builtin] = len(model.operatorCodes)
            model.operatorCodes.append(code)
        return codes[builtin]

    for graph in model.subgraphs:
        operators = []
        constants = {}
        def constant(rank, value):
            key = (rank, value)
            if key not in constants:
                buffer = schema.BufferT()
                buffer.data = np.frombuffer(np.float32(value).tobytes(), dtype=np.uint8)
                tensor = schema.TensorT()
                tensor.name, tensor.shape, tensor.type = f"clamp_{rank}_{value}".encode(), [1] * rank, schema.TensorType.FLOAT32
                tensor.buffer = len(model.buffers)
                model.buffers.append(buffer)
                constants[key] = len(graph.tensors)
                graph.tensors.append(tensor)
            return constants[key]

        for op in graph.operators:
            code = model.operatorCodes[op.opcodeIndex]
            if max(code.builtinCode, code.deprecatedBuiltinCode) != schema.BuiltinOperator.RELU_0_TO_1:
                operators.append(op)
                continue
            if len(op.inputs) != 1 or len(op.outputs) != 1:
                raise ValueError("Clamp normalization requires one input and output")
            tensors = [graph.tensors[int(i)] for i in [op.inputs[0], op.outputs[0]]]
            if any(t.type != schema.TensorType.FLOAT32 or t.isVariable or not len(t.shape) or
                   any(d <= 0 for d in t.shape) or
                   (t.quantization is not None and t.quantization.scale is not None and len(t.quantization.scale))
                   for t in tensors) or list(tensors[0].shape) != list(tensors[1].shape):
                raise ValueError("Clamp normalization requires matching static unquantized FP32 tensors")
            intermediate = copy.deepcopy(tensors[1])
            intermediate.name = (intermediate.name or b"activation") + b".clamp_lower"
            intermediate.buffer = 0
            index = len(graph.tensors)
            graph.tensors.append(intermediate)
            zero, one = constant(len(tensors[0].shape), 0), constant(len(tensors[0].shape), 1)
            for builtin, inputs, outputs in [
                (schema.BuiltinOperator.MAXIMUM, [int(op.inputs[0]), zero], [index]),
                (schema.BuiltinOperator.MINIMUM, [index, one], list(op.outputs)),
            ]:
                replacement = schema.OperatorT()
                replacement.opcodeIndex, replacement.inputs, replacement.outputs = opcode(builtin), inputs, outputs
                operators.append(replacement)
            changed += 1
        graph.operators = operators
    if not changed:
        return data, 0
    if any(getattr(buffer, "offset", 0) or getattr(buffer, "size", 0) for buffer in model.buffers):
        raise ValueError("External LiteRT buffers cannot be repacked")
    builder = flatbuffers.Builder(len(data))
    builder.Finish(model.Pack(builder), file_identifier=b"TFL3")
    return bytes(builder.Output()), changed


def check_conversion(source, converted):
    """对照空白和随机张量，拒绝布局、算子或输出顺序错误。

    Compares blank and random tensors, rejecting layout, operator, or output-order errors.
    """
    original = ort.InferenceSession(str(source), providers=["CPUExecutionProvider"])
    lite = Interpreter(model_path=str(converted), num_threads=2)
    lite.allocate_tensors()
    _, unnormalized = normalize_batch_matmul_ranks(converted.read_bytes())
    if unnormalized:
        raise ValueError("OCR BatchMatMul input ranks must be normalized before packaging")
    _, high_rank = normalize_singleton_ranks(converted.read_bytes())
    if high_rank:
        raise ValueError("OCR temporary singleton ranks must be normalized before packaging")
    _, unclamped = normalize_clamp_activations(converted.read_bytes())
    if unclamped:
        raise ValueError("OCR clamp activations must be normalized before packaging")
    normalized, postprocess = split_large_output_softmax(converted.read_bytes())
    if normalized != converted.read_bytes():
        raise ValueError("Large OCR output Softmax must be split before packaging")
    if any(op["op_name"] == "CUSTOM" for op in lite._get_ops_details()):
        raise ValueError("OCR conversions must not contain custom operators before NPU compilation")
    inp = lite.get_input_details()[0]
    out = lite.get_output_details()[0]
    shape = tuple(inp["shape"].tolist())
    if shape == (1, 48, 320, 3):
        layout = "nhwc"
    elif shape == (1, 3, 48, 320):
        layout = "nchw"
    else:
        raise ValueError(f"Unexpected LiteRT input: {shape}")
    rng = np.random.default_rng(20261007)
    worst_error = 0.0
    for values in [np.zeros((1, 3, 48, 320), np.float32),
                   np.full((1, 3, 48, 320), -1.0, np.float32),
                   rng.uniform(-1, 1, (1, 3, 48, 320)).astype(np.float32)]:
        expected = original.run(None, {original.get_inputs()[0].name: values})[0]
        lite.set_tensor(inp["index"], values.transpose(0, 2, 3, 1) if layout == "nhwc" else values)
        lite.invoke()
        actual = lite.get_tensor(out["index"])
        if postprocess:
            actual = softmax(actual)
        np.testing.assert_allclose(actual, expected, rtol=1e-3, atol=1e-4)
        np.testing.assert_array_equal(actual.argmax(-1), expected.argmax(-1))
        worst_error = max(worst_error, float(np.max(np.abs(actual - expected))))
    result = {
        "asset": converted.relative_to(ASSETS).as_posix(),
        "sha256": sha256(converted),
        "input_layout": layout,
        "input_shape": list(shape),
        "output_shape": out["shape"].tolist(),
        "input_name": inp["name"],
        "output_name": out["name"],
        "cpu_max_abs_error": worst_error,
    }
    if postprocess:
        result["output_postprocess"] = postprocess
    return result


def softmax(logits):
    """以有限 FP32 分数恢复最后一轴的概率，用于 CPU 数值对照。

    Restores probabilities along the final axis from finite FP32 logits for CPU comparisons.
    """
    if not np.isfinite(logits).all():
        raise ValueError("OCR logits must be finite")
    values = logits.astype(np.float64)
    values = np.exp(values - values.max(axis=-1, keepdims=True))
    return (values / values.sum(axis=-1, keepdims=True)).astype(np.float32)


def split_large_output_softmax(data):
    """将大字符字典的末尾 Softmax 移到宿主 CPU，保留主体图与公开概率契约。

    仅接受单图、单输出、静态 FP32 和 beta=1 的末尾 Softmax；内部 Softmax 不改写。
    自描述元数据使重复处理保持原字节，外置权重和未知契约拒绝改写。

    Moves terminal Softmax for large character dictionaries to the host CPU while preserving
    the backbone and public probability contract. Accepts only a single graph/output, static
    FP32 tensors, and terminal beta=1 Softmax; internal Softmax stays unchanged. Metadata makes
    repeated processing byte-identical. External weights and unknown contracts are rejected.
    """
    model = schema.ModelT.InitFromObj(schema.Model.GetRootAsModel(data, 0))
    marker = b"azurpilot.cpu_softmax"
    metadata = [item for item in (model.metadata or []) if item.name == marker]
    graph = model.subgraphs[0]
    if len(graph.outputs) != 1:
        if metadata:
            raise ValueError("CPU Softmax metadata requires one graph output")
        return data, None
    output = int(graph.outputs[0])
    tensor = graph.tensors[output]
    # 此阈值只筛出当前两个大字典模型，源于 MT6985 对照测试，并非厂商通用上限。
    if tensor.shape[-1] <= 16384:
        if metadata:
            raise ValueError("CPU Softmax metadata is reserved for large dictionaries")
        return data, None
    if len(model.subgraphs) != 1 or tensor.type != schema.TensorType.FLOAT32 or any(d <= 0 for d in tensor.shape):
        raise ValueError("CPU Softmax split requires static FP32 output in a single graph")
    terminal = graph.operators[-1]
    code = model.operatorCodes[terminal.opcodeIndex]
    if metadata:
        if len(metadata) != 1 or bytes(model.buffers[metadata[0].buffer].data) != b"beta=1;axis=-1":
            raise ValueError("Invalid CPU Softmax metadata")
        if max(code.builtinCode, code.deprecatedBuiltinCode) == schema.BuiltinOperator.SOFTMAX:
            raise ValueError("CPU Softmax metadata cannot retain terminal Softmax")
        return data, "softmax"
    if (max(code.builtinCode, code.deprecatedBuiltinCode) != schema.BuiltinOperator.SOFTMAX or
            list(terminal.outputs) != [output] or len(terminal.inputs) != 1 or
            terminal.builtinOptions.beta != 1.0):
        raise ValueError("Large OCR output must end with a single beta=1 Softmax")
    logits = int(terminal.inputs[0])
    source = graph.tensors[logits]
    if source.type != schema.TensorType.FLOAT32 or list(source.shape) != list(tensor.shape):
        raise ValueError("Softmax input/output contract changed")
    if any(output in op.inputs for op in graph.operators[:-1]):
        raise ValueError("Terminal Softmax output has other consumers")
    if any(getattr(buffer, "offset", 0) or getattr(buffer, "size", 0) for buffer in model.buffers):
        raise ValueError("External LiteRT buffers cannot be repacked")
    graph.outputs = [logits]
    graph.operators.pop()
    for signature in model.signatureDefs or []:
        for item in signature.outputs or []:
            if signature.subgraphIndex == 0 and item.tensorIndex == output:
                item.tensorIndex = logits
    item = schema.MetadataT()
    item.name, item.buffer = marker, len(model.buffers)
    buffer = schema.BufferT()
    buffer.data = np.frombuffer(b"beta=1;axis=-1", dtype=np.uint8)
    model.buffers.append(buffer)
    model.metadata = list(model.metadata or []) + [item]
    builder = flatbuffers.Builder(len(data))
    builder.Finish(model.Pack(builder), file_identifier=b"TFL3")
    return bytes(builder.Output()), "softmax"


def normalize_singleton_ranks(data):
    """从 reshape、transpose、split 的内部 5 维张量消去一个单维轴。

    保持图输入输出和数据顺序，克隆控制常量以保留共享消费者；未知算子或动态维度拒绝。

    Removes one singleton axis from internal rank-five reshape/transpose/split tensors.
    Preserves graph I/O and data order, cloning controls to protect shared consumers.
    Rejects unknown operators or dynamic dimensions.
    """
    model = schema.ModelT.InitFromObj(schema.Model.GetRootAsModel(data, 0))
    changed = 0
    for graph in model.subgraphs:
        axes = {}
        for index in list(graph.inputs) + list(graph.outputs):
            if len(graph.tensors[int(index)].shape) > 4:
                raise ValueError("High-rank graph I/O cannot be normalized")

        def control(index, values):
            original = graph.tensors[int(index)]
            if original.type != schema.TensorType.INT32 or original.isVariable:
                raise ValueError("Rank normalization requires static INT32 controls")
            old = model.buffers[original.buffer].data
            if old is None or len(old) == 0:
                raise ValueError("Rank normalization requires static INT32 controls")
            tensor = copy.deepcopy(original)
            tensor.name = (tensor.name or b"control") + b".npu_rank4"
            tensor.shape = [] if len(original.shape) == 0 and len(values) == 1 else [len(values)]
            if tensor.shapeSignature is not None:
                tensor.shapeSignature = list(tensor.shape)
            tensor.buffer = len(model.buffers)
            buffer = schema.BufferT()
            buffer.data = np.frombuffer(np.asarray(values, dtype="<i4").tobytes(), dtype=np.uint8)
            model.buffers.append(buffer)
            graph.tensors.append(tensor)
            return len(graph.tensors) - 1

        for op in graph.operators:
            high_inputs = [int(x) for x in op.inputs if x >= 0 and
                           (int(x) in axes or len(graph.tensors[int(x)].shape) > 4)]
            high_outputs = [int(x) for x in op.outputs if len(graph.tensors[int(x)].shape) > 4]
            if not high_inputs and not high_outputs:
                continue
            code = model.operatorCodes[op.opcodeIndex]
            builtin = max(code.builtinCode, code.deprecatedBuiltinCode)
            op.inputs = list(op.inputs)
            if any(x not in axes for x in high_inputs):
                raise ValueError("High-rank tensors must be produced by a supported static reshape")
            if builtin == schema.BuiltinOperator.RESHAPE:
                for index in high_outputs:
                    shape = list(graph.tensors[index].shape)
                    if len(shape) != 5 or 1 not in shape:
                        raise ValueError("High-rank reshape needs a singleton axis")
                    axes[index] = shape.index(1)
                if high_outputs:
                    if len(op.outputs) != 1 or len(op.inputs) != 2:
                        raise ValueError("Rank normalization requires a two-input reshape")
                    output = high_outputs[0]
                    reduced = list(graph.tensors[output].shape)
                    del reduced[axes[output]]
                    op.inputs[1] = control(op.inputs[1], reduced)
                    op.builtinOptions.newShape = reduced
            elif builtin == schema.BuiltinOperator.TRANSPOSE:
                if len(high_inputs) != 1 or len(high_outputs) != 1:
                    raise ValueError("Unsupported high-rank transpose")
                tensor = graph.tensors[int(op.inputs[1])]
                raw = model.buffers[tensor.buffer].data
                if tensor.type != schema.TensorType.INT32 or raw is None:
                    raise ValueError("Rank normalization requires static INT32 controls")
                perm = list(np.frombuffer(bytes(raw), dtype="<i4"))
                if sorted(perm) != list(range(5)):
                    raise ValueError("Invalid high-rank transpose permutation")
                removed = axes[high_inputs[0]]
                axes[high_outputs[0]] = perm.index(removed)
                op.inputs[1] = control(op.inputs[1], [int(x - (x > removed)) for x in perm if x != removed])
            elif builtin == schema.BuiltinOperator.SPLIT:
                if len(high_inputs) != 1 or len(high_outputs) != len(op.outputs):
                    raise ValueError("Unsupported high-rank split")
                tensor = graph.tensors[int(op.inputs[0])]
                raw = model.buffers[tensor.buffer].data
                if tensor.type != schema.TensorType.INT32 or raw is None or len(raw) != 4:
                    raise ValueError("Rank normalization requires a static split axis")
                split_axis = int(np.frombuffer(bytes(raw), dtype="<i4")[0])
                if not -5 <= split_axis < 5:
                    raise ValueError("Invalid high-rank split axis")
                split_axis %= 5
                removed = axes[high_inputs[0]]
                if split_axis == removed:
                    raise ValueError("Cannot split the removed singleton axis")
                op.inputs[0] = control(op.inputs[0], [split_axis - int(split_axis > removed)])
                axes.update({x: removed for x in high_outputs})
            else:
                raise ValueError(f"Unsupported high-rank operator: {builtin}")
            for index in high_outputs:
                tensor = graph.tensors[index]
                shape = list(tensor.shape)
                axis = axes[index]
                if len(shape) != 5 or shape[axis] != 1 or any(d <= 0 for d in shape):
                    raise ValueError("Rank normalization requires a static singleton axis")
                if tensor.shapeSignature is not None:
                    if list(tensor.shapeSignature) != shape:
                        raise ValueError("Dynamic high-rank tensor cannot be normalized")
                    tensor.shapeSignature = shape[:axis] + shape[axis + 1:]
                tensor.shape = shape[:axis] + shape[axis + 1:]
                changed += 1
    if not changed:
        return data, 0
    if any(getattr(buffer, "offset", 0) or getattr(buffer, "size", 0) for buffer in model.buffers):
        raise ValueError("External LiteRT buffers cannot be repacked")
    builder = flatbuffers.Builder(len(data))
    builder.Finish(model.Pack(builder), file_identifier=b"TFL3")
    return bytes(builder.Output()), changed


def main():
    """生成转换产物和白名单；可仅修正常量秩，或只复验现有产物。

    Generates conversions and the allowlist; can normalize existing constants or only reverify.
    """
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--verify-only", action="store_true")
    mode.add_argument("--normalize-existing", action="store_true",
                      help="Normalize bundled LiteRT constants and revalidate without reconverting ONNX")
    args = parser.parse_args()
    if json.loads((ASSETS / "manifest.json").read_text()).get("version") == 2:
        # 形状描述必须与权重字节同时生成，避免旧导出器覆盖 v2 清单。
        from prepare_ocr_cpu_models import main as prepare_cpu
        if args.normalize_existing:
            parser.error("Use prepare_ocr_cpu_models.py for model format 2")
        return prepare_cpu()
    previous = {}
    if (ASSETS / "manifest.json").is_file():
        previous = {item["sha256"]: item for item in
                    json.loads((ASSETS / "manifest.json").read_text())["models"]}
    models = []
    for relative, expected_hash in SOURCES.items():
        source = ROOT / ".tmp/ocr-sources" / relative
        if sha256(source) != expected_hash:
            raise ValueError(f"Upstream model hash mismatch: {relative}")
        model = onnx.load(source)
        item = {
            "asset": "models/" + relative,
            "sha256": expected_hash,
            "inputs": [node_info(n) for n in model.graph.input],
            "outputs": [node_info(n) for n in model.graph.output],
            "custom_metadata_map": {p.key: p.value for p in model.metadata_props},
        }
        if "_rec.onnx" in relative or "alocr-" in relative:
            converted = ASSETS / "litert" / (source.stem + ".tflite")
            if not args.verify_only and not args.normalize_existing:
                work = ROOT / ".tmp/ocr-convert" / source.stem
                work.mkdir(parents=True, exist_ok=True)
                # 转换器可能原地简化 ONNX；原始资产的哈希必须与 AP 的模型保持一致。
                conversion_input = work / source.name
                conversion_model = onnx.load(source)
                for dim, value in zip(conversion_model.graph.input[0].type.tensor_type.shape.dim,
                                      (1, 3, 48, 320), strict=True):
                    dim.dim_value = value
                del conversion_model.graph.value_info[:]
                conversion_model, valid = simplify(conversion_model)
                if not valid:
                    raise ValueError(f"ONNX simplification changed outputs: {relative}")
                onnx.save(conversion_model, conversion_input)
                log = work / "conversion.log"
                with log.open("w") as output:
                    subprocess.run([
                        sys.executable, "-m", "onnx2tf", "-i", str(conversion_input), "-o", str(work),
                        "-ois", "x:1,3,48,320", "-coion", "-v", "error",
                    ], check=True, stdout=output, stderr=subprocess.STDOUT)
                generated = list(work.glob("*_float32.tflite"))
                if len(generated) != 1:
                    raise ValueError(f"Expected one FP32 conversion; inspect {log}")
                converted.parent.mkdir(parents=True, exist_ok=True)
                converted.write_bytes(generated[0].read_bytes())
            if not args.verify_only:
                normalized, count = normalize_batch_matmul_ranks(converted.read_bytes())
                if count:
                    converted.write_bytes(normalized)
                print(f"OCR_MATMUL_RANKS_NORMALIZED {source.name}: {count}")
                normalized, count = normalize_singleton_ranks(converted.read_bytes())
                if count:
                    converted.write_bytes(normalized)
                print(f"OCR_SINGLETON_RANKS_NORMALIZED {source.name}: {count}")
                normalized, count = normalize_clamp_activations(converted.read_bytes())
                if count:
                    converted.write_bytes(normalized)
                print(f"OCR_CLAMP_ACTIVATIONS_NORMALIZED {source.name}: {count}")
                normalized, postprocess = split_large_output_softmax(converted.read_bytes())
                converted.write_bytes(normalized)
                print(f"OCR_OUTPUT_POSTPROCESS {source.name}: {postprocess or 'none'}")
            item["litert"] = check_conversion(source, converted)
            if "mnn" in previous.get(expected_hash, {}):
                item["mnn"] = previous[expected_hash]["mnn"]
            print(f"OCR_CONVERSION_OK {source.name}: {item['litert']['cpu_max_abs_error']:.8g}")
        models.append(item)
    manifest = {"version": 1, "upstream_commit": UPSTREAM, "converter": "onnx2tf==2.6.9", "models": models}
    if args.verify_only:
        existing = json.loads((ASSETS / "manifest.json").read_text())
        for old, new in zip(existing["models"], models, strict=True):
            assert old["sha256"] == new["sha256"]
            assert old.get("litert", {}).get("sha256") == new.get("litert", {}).get("sha256")
            assert old.get("litert", {}).get("output_postprocess") == new.get("litert", {}).get("output_postprocess")
    else:
        (ASSETS / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")


if __name__ == "__main__":
    main()
