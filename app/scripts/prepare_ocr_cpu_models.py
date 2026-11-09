#!/usr/bin/env python3
"""校验同权重 CPU 图视图，补齐检测与 pro，生成只含形状字段的修改描述。

原始 ONNX 仅位于开发缓存，不进入 APK。形状描述引用 LiteRT 中的字节位置，
不得修改训练权重；每个描述必须通过多尺寸原 ONNX 数值对照。

Verifies CPU graph views sharing existing weights, adding detection and pro support.
Original ONNX references stay in the developer cache. Descriptors reference shape-field
byte offsets, never trained weights, and must pass multi-shape ONNX numerical comparisons.
"""

import argparse
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
from ai_edge_litert import schema_py_generated as schema
from ai_edge_litert.interpreter import Interpreter
from prepare_ocr_models import (ASSETS, ROOT, node_info, normalize_batch_matmul_ranks,
                                normalize_singleton_ranks, normalize_clamp_activations,
                                split_large_output_softmax, softmax)

SOURCES = ROOT / '.tmp/ocr-sources'
EXTRA = {'ppocr-v6/PP-OCRv6_medium_rec.onnx':
         '9c09abf0957f7968c7586464b7397b84ad2387a0497a351af40e9acc71b673ba'}


def pack(model):
    """显式保存默认字段，允许 CPU 在不重打包权重的情况下调整池化规则。

    Stores default fields explicitly so CPU views can adjust pooling without repacking weights.
    """
    builder = flatbuffers.Builder(1024)
    builder.ForceDefaults(True)
    builder.Finish(model.Pack(builder), file_identifier=b'TFL3')
    return bytes(builder.Output())


def descriptors(data, detector):
    """仅描述输入尺寸、时间轴 reshape 和检测尺寸控制常量。

    Describes only input dimensions, temporal reshapes, and detector size-control constants.
    """
    model = schema.Model.GetRootAsModel(data, 0)
    graph = model.Subgraphs(0)
    origin = np.frombuffer(data, dtype=np.uint8).ctypes.data
    patches = {}

    def add(array, index, **replacement):
        offset = int(array.ctypes.data - origin + 4 * index)
        value = {'offset': offset, 'size': 4, 'expected': int(array[index]), **replacement}
        if offset in patches and patches[offset] != value:
            raise ValueError('Shared shape constant has incompatible consumers')
        patches[offset] = value

    source = graph.Tensors(graph.Inputs(0))
    shape = source.ShapeAsNumpy()
    layout = 'nhwc' if shape[-1] == 3 else 'nchw'
    height_axis, width_axis = (1, 2) if layout == 'nhwc' else (2, 3)
    for dimensions in [shape, source.ShapeSignatureAsNumpy()]:
        if isinstance(dimensions, np.ndarray):
            add(dimensions, width_axis, dimension=3)
            if detector:
                add(dimensions, height_axis, dimension=2)
    output = graph.Tensors(graph.Outputs(0))
    output_shape = output.ShapeAsNumpy()
    axes = ((1, 2) if output_shape[-1] == 1 else (2, 3)) if detector else (1,)
    # 编译器的缓冲区仍会读取模型输出标注，因此与实际步长一起专门化。
    for dimensions in [output_shape, output.ShapeSignatureAsNumpy()]:
        if isinstance(dimensions, np.ndarray):
            for axis in axes:
                add(dimensions, axis, dimension=(2 if axis == axes[0] else 3) if detector else 3,
                    divisor=1 if detector else 8, floor=not detector, add=0 if detector else 3)
    for index in range(graph.OperatorsLength()):
        op = graph.Operators(index)
        code = model.OperatorCodes(op.OpcodeIndex()).BuiltinCode()
        if code == schema.BuiltinOperator.RESHAPE and not detector:
            tensor = graph.Tensors(op.Inputs(1))
            raw = model.Buffers(tensor.Buffer()).DataAsNumpy()
            if not isinstance(raw, np.ndarray) or len(raw) == 0:
                continue
            dims = raw.view(np.int32)
            if 40 in dims:
                if list(dims).count(40) != 1 or -1 in dims:
                    raise ValueError('Temporal reshape cannot infer a unique dimension')
                add(dims, list(dims).index(40), value=-1)
        if code == schema.BuiltinOperator.AVERAGE_POOL_2D and not detector:
            options = schema.Pool2DOptions()
            options.Init(op.BuiltinOptions().Bytes, op.BuiltinOptions().Pos)
            if (options.FilterHeight(), options.FilterWidth(), options.StrideH(), options.StrideW()) != (3, 2, 3, 2):
                raise ValueError('Unknown OCR pooling contract')
            offset = options._tab.Pos + options._tab.Offset(4)
            if not options._tab.Offset(4):
                raise ValueError('Pool padding default was not materialized')
            patches[offset] = {'offset': offset, 'size': 1, 'expected': options.Padding(),
                               'value': schema.Padding.VALID}
        if detector and code in (schema.BuiltinOperator.RESIZE_BILINEAR,
                                 schema.BuiltinOperator.RESIZE_NEAREST_NEIGHBOR,
                                 schema.BuiltinOperator.TRANSPOSE_CONV):
            slot = 0 if code == schema.BuiltinOperator.TRANSPOSE_CONV else 1
            raw = model.Buffers(graph.Tensors(op.Inputs(slot)).Buffer()).DataAsNumpy()
            if not isinstance(raw, np.ndarray) or len(raw) == 0:
                continue
            dims = raw.view(np.int32)
            axes = (1, 2) if slot == 0 else (0, 1)
            for axis, dimension, base in zip(axes, (2, 3), (shape[height_axis], shape[width_axis]), strict=True):
                if dims[axis] <= 0 or base % dims[axis]:
                    raise ValueError('Unknown detector resize ratio')
                add(dims, axis, dimension=dimension, divisor=int(base // dims[axis]))
    return sorted(patches.values(), key=lambda x: x['offset']), layout


def specialize(data, patches, shape):
    """复现 App 字节视图，拒绝错误原值。 / Mirrors App byte views and rejects wrong originals."""
    result = bytearray(data)
    for patch in patches:
        offset, size = patch['offset'], patch['size']
        old = int.from_bytes(result[offset:offset + size], 'little', signed=True)
        if old != patch['expected']:
            raise ValueError('CPU shape descriptor mismatches its model')
        value = patch.get('value')
        if value is None:
            divisor = patch.get('divisor', 1)
            if not patch.get('floor') and shape[patch['dimension']] % divisor:
                raise ValueError('Unsupported detector dimensions')
            value = (shape[patch['dimension']] + patch.get('add', 0)) // divisor
        result[offset:offset + size] = int(value).to_bytes(size, 'little', signed=True)
    return bytes(result)


def verify_view(source, data, patches, layout, detector, postprocess):
    """对照随机、空白、多尺寸输入，保持输出顺序及原始概率。

    Compares random and blank inputs at several shapes, preserving output order and probabilities.
    """
    options = ort.SessionOptions()
    options.intra_op_num_threads = 2
    options.log_severity_level = 3
    original = ort.InferenceSession(str(source), options, providers=['CPUExecutionProvider'])
    shapes = [(1, 3, 64, 64), (1, 3, 96, 160), (1, 3, 128, 96)] if detector else [
        (1, 3, 48, w) for w in [32, *range(317, 325), 328, 480, 640, 1024]]
    worst = 0.0
    output_layout = 'native'
    for shape in shapes:
        lite = Interpreter(model_content=specialize(data, patches, shape), num_threads=2)
        lite.allocate_tensors()
        for values in [np.zeros(shape, np.float32), np.random.default_rng(20261009).uniform(-1, 1, shape).astype(np.float32)]:
            lite.set_tensor(lite.get_input_details()[0]['index'], values.transpose(0, 2, 3, 1) if layout == 'nhwc' else values)
            lite.invoke()
            actual = lite.get_tensor(lite.get_output_details()[0]['index'])
            expected = original.run(None, {original.get_inputs()[0].name: values})[0]
            if detector and actual.shape != expected.shape:
                actual = actual.transpose(0, 3, 1, 2)
                output_layout = 'nhwc'
            if postprocess:
                actual = softmax(actual)
            np.testing.assert_allclose(actual, expected, rtol=1e-3, atol=1e-4)
            if not detector:
                np.testing.assert_array_equal(actual.argmax(-1), expected.argmax(-1))
            worst = max(worst, float(np.max(np.abs(actual - expected))))
        print(f'CPU_VIEW_OK {source.name} {shape}: {list(actual.shape)}', flush=True)
    return worst, output_layout


def convert(source, detector):
    """仅转换缺少的模型，强制静态基准图后校验。 / Converts missing models at a static baseline."""
    work = ROOT / '.tmp/ocr-cpu-convert' / source.stem
    work.mkdir(parents=True, exist_ok=True)
    model = onnx.load(source)
    dims = (1, 3, 64, 64) if detector else (1, 3, 48, 320)
    for dim, value in zip(model.graph.input[0].type.tensor_type.shape.dim, dims, strict=True):
        dim.dim_value = value
    del model.graph.value_info[:]
    model, valid = simplify(model)
    if not valid:
        raise ValueError('ONNX simplification failed')
    temporary = work / source.name
    onnx.save(model, temporary)
    with (work / 'conversion.log').open('w') as log:
        subprocess.run([sys.executable, '-m', 'onnx2tf', '-i', str(temporary), '-o', str(work),
                        '-ois', 'x:' + ','.join(map(str, dims)), '-coion', '-v', 'error'],
                       check=True, stdout=log, stderr=subprocess.STDOUT)
    converted = next(work.glob('*_float32.tflite')).read_bytes()
    for normalize in [normalize_batch_matmul_ranks, normalize_singleton_ranks, normalize_clamp_activations]:
        converted, _ = normalize(converted)
    return split_large_output_softmax(converted)[0] if not detector else converted


def main():
    """生成可重复构建的 CPU 视图描述；原始权重仅在开发缓存。

    Generates reproducible CPU view descriptors with original weights confined to the dev cache.
    """
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--verify-only', action='store_true')
    args = parser.parse_args()
    manifest = json.loads((ASSETS / 'manifest.json').read_text())
    models = manifest['models']
    for relative, digest in EXTRA.items():
        if any(m['sha256'] == digest for m in models):
            continue
        if args.verify_only:
            raise ValueError('Missing pro CPU conversion')
        source = SOURCES / relative
        model = onnx.load(source)
        models.append({'asset': 'models/' + relative, 'sha256': digest,
                       'inputs': [node_info(n) for n in model.graph.input],
                       'outputs': [node_info(n) for n in model.graph.output],
                       'custom_metadata_map': {p.key: p.value for p in model.metadata_props}})
    for item in models:
        source = SOURCES / item['asset'].removeprefix('models/')
        if hashlib.sha256(source.read_bytes()).hexdigest() != item['sha256']:
            raise ValueError('Developer reference hash mismatch')
        detector = '_det.onnx' in source.name
        target = ASSETS / 'litert' / (source.stem + '.tflite')
        if not target.exists():
            if args.verify_only:
                raise ValueError('Missing LiteRT conversion')
            target.write_bytes(convert(source, detector))
        model = schema.ModelT.InitFromObj(schema.Model.GetRootAsModel(target.read_bytes(), 0))
        graph = model.subgraphs[0]
        graph.tensors[graph.inputs[0]].shapeSignature = graph.tensors[graph.inputs[0]].shape
        data = pack(model)
        patches, layout = descriptors(data, detector)
        lite = Interpreter(model_content=data, num_threads=2)
        lite.allocate_tensors()
        postprocess = item.get('litert', {}).get('output_postprocess')
        if not detector and graph.tensors[graph.outputs[0]].shape[-1] > 16384:
            postprocess = 'softmax'
        error, output_layout = verify_view(source, data, patches, layout, detector, postprocess)
        conversion = {'asset': target.relative_to(ASSETS).as_posix(),
                      'sha256': hashlib.sha256(data).hexdigest(), 'input_layout': layout,
                      'input_shape': lite.get_input_details()[0]['shape'].tolist(),
                      'output_shape': lite.get_output_details()[0]['shape'].tolist(),
                      'output_layout': output_layout, 'cpu_shape_patches': patches,
                      'cpu_dynamic_max_abs_error': error}
        if postprocess:
            conversion['output_postprocess'] = postprocess
        if args.verify_only:
            if data != target.read_bytes() or conversion != item['litert']:
                raise ValueError('CPU view descriptor or model changed')
        else:
            target.write_bytes(data)
            item['litert'] = conversion
            item['kind'] = 'detector' if detector else 'recognition'
            item['npu_supported'] = 'mnn' in item
            item['source_weight_bundled'] = False
    if not args.verify_only:
        manifest['version'] = 2
        (ASSETS / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')


if __name__ == '__main__':
    main()
