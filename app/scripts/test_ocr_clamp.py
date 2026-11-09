#!/usr/bin/env python3
"""通过真实 LiteRT 执行检查裁剪激活改写，不改变共享输出或量化语义。

Checks clamp rewriting with actual LiteRT execution and preserves shared outputs and
quantization boundaries.
"""
import unittest

import numpy as np
from ai_edge_litert import schema_py_generated as schema

from prepare_ocr_models import normalize_clamp_activations
from test_ocr_model_ranks import pack, run


def fixture(shape):
    """构造保留输入与激活输出的多输出图。 / Builds a graph exposing input and activation."""
    model = schema.ModelT()
    model.version, model.buffers = 3, [schema.BufferT()]
    code = schema.OperatorCodeT()
    code.builtinCode, code.deprecatedBuiltinCode, code.version = schema.BuiltinOperator.RELU_0_TO_1, 127, 1
    model.operatorCodes = [code]
    graph = schema.SubGraphT()
    graph.tensors = []
    for index in range(2):
        tensor = schema.TensorT()
        tensor.name, tensor.shape, tensor.type, tensor.buffer = f"t{index}".encode(), shape, schema.TensorType.FLOAT32, 0
        graph.tensors.append(tensor)
    op = schema.OperatorT()
    op.opcodeIndex, op.inputs, op.outputs = 0, [0], [1]
    graph.inputs, graph.outputs, graph.operators = [0], [0, 1], [op]
    model.subgraphs = [graph]
    return model


class ClampTest(unittest.TestCase):
    """覆盖不同秩、边界数值和拒绝条件。 / Covers ranks, numerical boundaries, and rejection."""

    def test_real_execution_preserves_boundary_values_and_shared_outputs(self):
        values = np.array([-1e38, -3, -0.0, 0.0, 0.1, 0.999, 1, 1e38], np.float32)
        for shape in ([8], [1, 2, 4], [1, 2, 2, 2]):
            with self.subTest(shape=shape):
                original = pack(fixture(shape))
                transformed, count = normalize_clamp_activations(original)
                self.assertEqual(count, 1)
                for expected, actual in zip(run(original, values.reshape(shape)),
                                            run(transformed, values.reshape(shape)), strict=True):
                    np.testing.assert_array_equal(actual, expected)
                self.assertEqual(normalize_clamp_activations(transformed), (transformed, 0))

    def test_external_buffers_are_rejected(self):
        model = fixture([8])
        model.buffers[0].offset, model.buffers[0].size = 1234, 8
        with self.assertRaisesRegex(ValueError, "External LiteRT"):
            normalize_clamp_activations(pack(model))

    def test_nonfloat_and_dynamic_tensors_are_rejected(self):
        for dtype, shape in ((schema.TensorType.INT8, [8]), (schema.TensorType.FLOAT32, [-1])):
            model = fixture(shape)
            for tensor in model.subgraphs[0].tensors:
                tensor.type = dtype
            with self.assertRaisesRegex(ValueError, "static unquantized FP32"):
                normalize_clamp_activations(pack(model))

    def test_quantized_float_is_rejected(self):
        model = fixture([8])
        quant = schema.QuantizationParametersT()
        quant.scale = [0.1]
        model.subgraphs[0].tensors[0].quantization = quant
        with self.assertRaisesRegex(ValueError, "static unquantized FP32"):
            normalize_clamp_activations(pack(model))


if __name__ == "__main__":
    unittest.main()
