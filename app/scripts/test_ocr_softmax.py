#!/usr/bin/env python3
"""验证大字典输出拆分保留原概率、签名和重复处理语义。

Tests that large-dictionary output splitting preserves probabilities, signatures,
and idempotence using the real LiteRT CPU interpreter in the Linux conversion environment.
"""

import unittest

import numpy as np
from ai_edge_litert import schema_py_generated as schema

from prepare_ocr_models import softmax, split_large_output_softmax
from test_ocr_model_ranks import pack, run


def fixture(classes=18385, beta=1.0):
    """构造加法和末尾 Softmax 图，并保留命名输出签名。

    Builds an addition and terminal Softmax graph with a named output signature.
    """
    model = schema.ModelT()
    model.version = 3
    model.buffers = [schema.BufferT(), schema.BufferT()]
    model.buffers[1].data = np.frombuffer(np.float32(0).tobytes(), dtype=np.uint8)
    model.operatorCodes = []
    for builtin in [schema.BuiltinOperator.ADD, schema.BuiltinOperator.SOFTMAX]:
        code = schema.OperatorCodeT()
        code.builtinCode, code.deprecatedBuiltinCode, code.version = builtin, builtin, 1
        model.operatorCodes.append(code)
    graph = schema.SubGraphT()
    graph.tensors = []
    for index, shape in enumerate([[1, 2, classes], [], [1, 2, classes], [1, 2, classes]]):
        tensor = schema.TensorT()
        tensor.name, tensor.shape, tensor.type = f"t{index}".encode(), shape, schema.TensorType.FLOAT32
        tensor.buffer = int(index == 1)
        graph.tensors.append(tensor)
    add, final = schema.OperatorT(), schema.OperatorT()
    add.opcodeIndex, add.inputs, add.outputs = 0, [0, 1], [2]
    add.builtinOptionsType, add.builtinOptions = schema.BuiltinOptions.AddOptions, schema.AddOptionsT()
    final.opcodeIndex, final.inputs, final.outputs = 1, [2], [3]
    final.builtinOptionsType = schema.BuiltinOptions.SoftmaxOptions
    final.builtinOptions = schema.SoftmaxOptionsT()
    final.builtinOptions.beta = beta
    graph.operators, graph.inputs, graph.outputs = [add, final], [0], [3]
    model.subgraphs = [graph]
    signature = schema.SignatureDefT()
    signature.signatureKey, signature.subgraphIndex = b"serving_default", 0
    input_map = schema.TensorMapT()
    input_map.name, input_map.tensorIndex = b"logits", 0
    signature.inputs = [input_map]
    output = schema.TensorMapT()
    output.name, output.tensorIndex = b"probabilities", 3
    signature.outputs = [output]
    model.signatureDefs = [signature]
    return model


class OutputSoftmaxTest(unittest.TestCase):
    """用真实执行及拒绝条件检查拆分契约。

    Checks the split contract through real execution and rejection cases.
    """

    def test_probabilities_signature_and_idempotence(self):
        original = pack(fixture())
        split, postprocess = split_large_output_softmax(original)
        self.assertEqual(postprocess, "softmax")
        rng = np.random.default_rng(20261008)
        values = rng.uniform(-25, 25, (1, 2, 18385)).astype(np.float32)
        np.testing.assert_allclose(softmax(run(split, values)[0]), run(original, values)[0],
                                   rtol=1e-5, atol=1e-7)
        graph = schema.ModelT.InitFromObj(schema.Model.GetRootAsModel(split, 0))
        self.assertEqual(graph.signatureDefs[0].outputs[0].tensorIndex, 2)
        self.assertEqual(split_large_output_softmax(split), (split, "softmax"))

    def test_small_dictionary_is_unchanged(self):
        original = pack(fixture(classes=97))
        self.assertEqual(split_large_output_softmax(original), (original, None))

    def test_unknown_beta_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "beta=1"):
            split_large_output_softmax(pack(fixture(beta=0.5)))

    def test_external_buffers_are_rejected(self):
        model = fixture()
        model.buffers[1].offset, model.buffers[1].size = 1024, 4
        with self.assertRaisesRegex(ValueError, "External LiteRT"):
            split_large_output_softmax(pack(model))

    def test_softmax_handles_extreme_finite_logits_and_rejects_nan(self):
        np.testing.assert_array_equal(softmax(np.array([[1e30, -1e30]], np.float32)), [[1, 0]])
        with self.assertRaisesRegex(ValueError, "finite"):
            softmax(np.array([[np.nan, 0]], np.float32))


if __name__ == "__main__":
    unittest.main()
