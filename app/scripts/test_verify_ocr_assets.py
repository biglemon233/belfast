#!/usr/bin/env python3
"""测试 OCR 打包质量门，不依赖大型模型或设备。

Tests the OCR packaging gate without large models or devices.
"""

import hashlib
import io
import json
import unittest
import zipfile

from verify_ocr_assets import (HIAI_LIBRARIES, HIAI_SOURCE_COMMIT, LITERT_VERSION,
                               MEDIATEK_SYSTEM_LIBRARIES, MNN_SOURCE_COMMIT, REQUIRED_LIBRARIES, verify)


class OcrAssetsTest(unittest.TestCase):
    """覆盖正确包、损坏、混版和 ABI 缺失。 / Covers valid, corrupt, mismatched, and missing-ABI packages."""

    def archive(self, arm64=True, change=None):
        """生成小型合成 APK。 / Generates a small synthetic APK."""
        data = b"test weights"
        checksum = hashlib.sha256(data).hexdigest()
        files = {
            "AndroidManifest.xml": " ".join(sorted(MEDIATEK_SYSTEM_LIBRARIES)).encode(),
            "assets/ocr/manifest.json": json.dumps({"version": 2, "models": [{"source_weight_bundled": False, "npu_supported": True, "asset": "models/test.onnx", "sha256": checksum,
                "litert": {"asset": "litert/test.tflite", "sha256": checksum,
                    "cpu_shape_patches": [{"offset": 0, "size": 1, "expected": 116, "value": 1}]},
                "mnn": {"asset": "mnn/test.mnn", "sha256": checksum}}]}),
            "assets/ocr/runtime.json": json.dumps({"litert": LITERT_VERSION, "libraries": {
                name: checksum for name in REQUIRED_LIBRARIES}}),
            "assets/ocr/test/sample.png": b"\x89PNG\r\n\x1a\n" + bytes(8) + (320).to_bytes(4, "big") + (48).to_bytes(4, "big"),
            "assets/ocr/litert/test.tflite": data,
            "assets/ocr/mnn/test.mnn": data,
            "assets/ocr/licenses/neuropilot-license.pdf": b"license",
            "assets/ocr/licenses/hiai-LICENSE.txt": b"license",
            "assets/ocr/licenses/mnn-LICENSE.txt": b"license",
            "assets/ocr/licenses/mnn-flatbuffers-LICENSE.txt": b"license",
            "assets/ocr/licenses/mnn-half-LICENSE.txt": b"license",
            "assets/ocr/hiai-runtime.json": json.dumps({"mnn_source_commit": MNN_SOURCE_COMMIT,
                "hiai_source_commit": HIAI_SOURCE_COMMIT, "libraries": {
                    name: checksum for name in HIAI_LIBRARIES}}),
        }
        files.update({f"assets/overlays/{name}": b"overlay" for name in
                      ["android_host.py", "android_ocr.py", "sitecustomize.py"]})
        files.update({f"lib/arm64-v8a/{name}": data for name in REQUIRED_LIBRARIES | HIAI_LIBRARIES |
                      {"libocrhiai.so", "libMNN.so", "libMNN_Backend_HiAI.so", "libLiteRtDispatch_MediaTek.so"}} if arm64 else
                     {"lib/x86_64/libbridge.so": b"bridge"})
        if change:
            change(files)
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, "w") as archive:
            for name, value in files.items():
                archive.writestr(name, value)
        buffer.seek(0)
        return zipfile.ZipFile(buffer)

    def test_original_weights_rejected(self):
        with self.archive(change=lambda f: f.update({"assets/ocr/models/old.onnx": b"weights"})) as archive:
            with self.assertRaisesRegex(ValueError, "Original OCR weights"):
                verify(archive)

    def test_obsolete_onnx_android_runtime_rejected(self):
        with self.archive(change=lambda f: f.update({"lib/arm64-v8a/libonnxruntime.so": b"runtime"})) as archive:
            with self.assertRaisesRegex(ValueError, "Obsolete Android ONNX"):
                verify(archive)

    def test_corrupt_cpu_shape_descriptor_rejected(self):
        def change(files):
            manifest = json.loads(files["assets/ocr/manifest.json"])
            manifest["models"][0]["litert"]["cpu_shape_patches"][0]["expected"] = 0
            files["assets/ocr/manifest.json"] = json.dumps(manifest)
        with self.archive(change=change) as archive:
            with self.assertRaisesRegex(ValueError, "CPU shape descriptor differs"):
                verify(archive)

    def test_valid_vendor_package(self):
        with self.archive() as archive:
            verify(archive)

    def test_x86_cpu_package(self):
        with self.archive(arm64=False) as archive:
            verify(archive)

    def test_missing_apusys_declaration(self):
        for encoding in ["utf-8", "utf-16le"]:
            with self.subTest(encoding=encoding):
                manifest = " ".join(sorted(MEDIATEK_SYSTEM_LIBRARIES - {"libapuwareapusys_v2.mtk.so"}))
                with self.archive(change=lambda f: f.update({
                        "AndroidManifest.xml": manifest.encode(encoding)})) as archive:
                    with self.assertRaisesRegex(ValueError, "Missing MediaTek system library declaration"):
                        verify(archive)

    def test_legacy_mgvi_declaration_rejected(self):
        for encoding in ["utf-8", "utf-16le"]:
            with self.subTest(encoding=encoding):
                with self.archive(change=lambda f: f.update({
                        "AndroidManifest.xml": "libneuron_adapter_mgvi.so".encode(encoding)})) as archive:
                    with self.assertRaisesRegex(ValueError, "Legacy MGVI"):
                        verify(archive)

    def test_corrupt_model(self):
        with self.archive(change=lambda f: f.update({"assets/ocr/litert/test.tflite": b"test bad"})) as archive:
            with self.assertRaisesRegex(ValueError, "model checksum"):
                verify(archive)

    def test_unknown_output_postprocess_rejected(self):
        def change(files):
            manifest = json.loads(files["assets/ocr/manifest.json"])
            manifest["models"][0]["litert"]["output_postprocess"] = "unknown"
            files["assets/ocr/manifest.json"] = json.dumps(manifest)
        with self.archive(change=change) as archive:
            with self.assertRaisesRegex(ValueError, "Unknown OCR output postprocess"):
                verify(archive)

    def test_large_dictionary_postprocess_contract(self):
        def change(shape):
            def update(files):
                manifest = json.loads(files["assets/ocr/manifest.json"])
                manifest["models"][0]["litert"].update(output_postprocess="softmax", output_shape=shape)
                files["assets/ocr/manifest.json"] = json.dumps(manifest)
            return update
        with self.archive(change=change([1, 40, 18385])) as archive:
            verify(archive)
        for shape in [[1, 40, 97], [2, 40, 18385], [1, 39, 18385], []]:
            with self.subTest(shape=shape), self.archive(change=change(shape)) as archive:
                with self.assertRaisesRegex(ValueError, "Terminal Softmax requires"):
                    verify(archive)

    def test_missing_vendor(self):
        with self.archive(change=lambda f: f.pop("lib/arm64-v8a/libLiteRtDispatch_MediaTek_Vendor.so")) as archive:
            with self.assertRaises(KeyError):
                verify(archive)

    def test_missing_hiai_bridge(self):
        with self.archive(change=lambda f: f.pop("lib/arm64-v8a/libocrhiai.so")) as archive:
            with self.assertRaisesRegex(ValueError, "Missing OCR bridge"):
                verify(archive)

    def test_missing_mtk_dispatch_wrapper(self):
        with self.archive(change=lambda f: f.pop("lib/arm64-v8a/libLiteRtDispatch_MediaTek.so")) as archive:
            with self.assertRaisesRegex(ValueError, "Missing OCR bridge"):
                verify(archive)

    def test_corrupt_hiai_client(self):
        with self.archive(change=lambda f: f.update({"lib/arm64-v8a/libhiai.so": b"wrong"})) as archive:
            with self.assertRaisesRegex(ValueError, "HiAI library checksum"):
                verify(archive)

    def test_missing_hiai_model_conversion(self):
        def change(files):
            manifest = json.loads(files["assets/ocr/manifest.json"])
            manifest["models"][0].pop("mnn")
            files["assets/ocr/manifest.json"] = json.dumps(manifest)
        with self.archive(change=change) as archive:
            with self.assertRaisesRegex(ValueError, "Missing HiAI OCR conversion"):
                verify(archive)

    def test_corrupt_hiai_model(self):
        with self.archive(change=lambda f: f.update({"assets/ocr/mnn/test.mnn": b"wrong"})) as archive:
            with self.assertRaisesRegex(ValueError, "model checksum"):
                verify(archive)

    def test_wrong_version(self):
        def change(files):
            runtime = json.loads(files["assets/ocr/runtime.json"])
            runtime["litert"] = "2.2.0"
            files["assets/ocr/runtime.json"] = json.dumps(runtime)
        with self.archive(change=change) as archive:
            with self.assertRaisesRegex(ValueError, "mismatched versions"):
                verify(archive)

    def test_missing_overlay(self):
        with self.archive(change=lambda f: f.pop("assets/overlays/sitecustomize.py")) as archive:
            with self.assertRaisesRegex(ValueError, "Missing runtime overlay"):
                verify(archive)

    def test_missing_test_image(self):
        with self.archive(change=lambda f: f.pop("assets/ocr/test/sample.png")) as archive:
            with self.assertRaisesRegex(ValueError, "Missing OCR test image"):
                verify(archive)

    def test_wrong_test_image_dimensions(self):
        with self.archive(change=lambda f: f.update({"assets/ocr/test/sample.png": b"not a PNG"})) as archive:
            with self.assertRaisesRegex(ValueError, "Invalid OCR test image"):
                verify(archive)


if __name__ == "__main__":
    unittest.main()
