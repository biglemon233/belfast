#!/usr/bin/env python3
"""校验 APK 中的模型、来源清单及多厂商 NPU 库。

只读取 ZIP 条目，不解压 APK；x86_64 专用包允许不包含 ARM64 厂商库。

Verifies APK model checksums, source manifests, and vendor NPU libraries.
Reads ZIP entries without extraction; x86_64-only APKs may omit ARM64 vendor libraries.
"""

import argparse
import hashlib
import json
import zipfile
from pathlib import Path

LITERT_VERSION = "2.1.0rc1"
MEDIATEK_SYSTEM_LIBRARIES = {
    "libapuwareutils.mtk.so", "libapuwareutils_v2.mtk.so", "libapuwareapusys_v2.mtk.so",
    "libapuwarexrp.mtk.so", "libapuwarexrp_v2.mtk.so", "libapuwarehmp.mtk.so",
    "libcmdl_ndk.mtk.so", "libcmdl_ndk.mtk.vndk.so",
    "libnir_neon_driver_ndk.mtk.so", "libnir_neon_driver_ndk.mtk.vndk.so",
}
REQUIRED_LIBRARIES = {
    "libLiteRtCompilerPlugin_Qualcomm.so", "libLiteRtDispatch_Qualcomm.so",
    "libLiteRtCompilerPlugin_MediaTek.so", "libLiteRtDispatch_MediaTek_Vendor.so",
    "libQnnHtp.so", "libQnnHtpPrepare.so", "libQnnSystem.so",
    "libneuronusdk_adapter.mtk.so", "libneuronusdk_adapter.9.mtk.so",
} | {f"libQnnHtpV{version}{kind}.so"
     for version in [68, 69, 73, 75, 79, 81] for kind in ["Skel", "Stub"]}
HIAI_LIBRARIES = {"libhiai.so", "libhiai_ir.so", "libhiai_ir_build.so",
                  "libhiai_model_compatible.so", "libhiai_enhance.so"}
MNN_SOURCE_COMMIT = "024a946b0b8fcf87c8a418229fadd4cd7858ffba"
HIAI_SOURCE_COMMIT = "220e2070f1fad9005d6ed06d33520e559cdac7ec"


def verify(archive):
    """拒绝缺失、混版或损坏的 OCR 包。 / Rejects incomplete, mismatched, or corrupt OCR packages."""
    names = set(archive.namelist())
    # Android 二进制 XML 的字符串池可使用两种编码；打包后仍含旧 MGVI 时必须拒绝。
    android_manifest = archive.read("AndroidManifest.xml")
    if any("libneuron_adapter_mgvi.so".encode(encoding) in android_manifest
           for encoding in ["utf-8", "utf-16le"]):
        raise ValueError("Legacy MGVI remains in APK manifest and can override bundled NeuroPilot")
    manifest = json.loads(archive.read("assets/ocr/manifest.json"))
    runtime = json.loads(archive.read("assets/ocr/runtime.json"))
    hiai = json.loads(archive.read("assets/ocr/hiai-runtime.json"))
    if runtime["litert"] != LITERT_VERSION:
        raise ValueError("LiteRT runtime and vendor plugins have mismatched versions")
    if manifest.get("version") != 2:
        raise ValueError("OCR model format 2 required")
    if any(name.startswith("assets/ocr/") and name.endswith(".onnx") for name in names):
        raise ValueError("Original OCR weights must not be bundled")
    if any("onnxruntime" in name for name in names if name.startswith("lib/")):
        raise ValueError("Obsolete Android ONNX runtime must not be bundled")
    for model in manifest["models"]:
        if model.get("source_weight_bundled") is not False or "litert" not in model:
            raise ValueError("All OCR models require a weight-free source identity and LiteRT CPU conversion")
        if model.get("npu_supported") and "mnn" not in model:
            raise ValueError(f"Missing HiAI OCR conversion: {model['asset']}")
        conversion = model["litert"]
        patches = conversion.get("cpu_shape_patches", [])
        data = archive.read("assets/ocr/" + conversion["asset"])
        if not patches:
            raise ValueError("Missing CPU shape descriptors")
        for patch in patches:
            offset, size = patch["offset"], patch["size"]
            if size not in (1, 4) or offset < 0 or offset + size > len(data):
                raise ValueError("Invalid CPU shape descriptor bounds")
            if int.from_bytes(data[offset:offset + size], "little", signed=True) != patch["expected"]:
                raise ValueError("CPU shape descriptor differs from model bytes")
        postprocess = conversion.get("output_postprocess")
        if postprocess not in (None, "softmax"):
            raise ValueError("Unknown OCR output postprocess")
        if postprocess == "softmax":
            shape = model["litert"].get("output_shape", [])
            if len(shape) != 3 or shape[:2] != [1, 40] or shape[-1] <= 16384:
                raise ValueError("Terminal Softmax requires the verified large-dictionary output contract")
        for spec in [model[key] for key in ["litert", "mnn"] if key in model]:
            actual = hashlib.sha256(archive.read(f"assets/ocr/{spec['asset']}")).hexdigest()
            if actual != spec["sha256"]:
                raise ValueError(f"OCR model checksum mismatch: {spec['asset']}")
    if any(name.startswith("lib/arm64-v8a/") for name in names):
        for name in MEDIATEK_SYSTEM_LIBRARIES:
            if not any(name.encode(encoding) in android_manifest for encoding in ["utf-8", "utf-16le"]):
                raise ValueError(f"Missing MediaTek system library declaration: {name}")
        if set(runtime["libraries"]) != REQUIRED_LIBRARIES:
            raise ValueError("Incomplete OCR vendor runtime manifest")
        for name, checksum in runtime["libraries"].items():
            actual = hashlib.sha256(archive.read(f"lib/arm64-v8a/{name}")).hexdigest()
            if actual != checksum:
                raise ValueError(f"NPU library checksum mismatch: {name}")
        if hiai["mnn_source_commit"] != MNN_SOURCE_COMMIT or hiai["hiai_source_commit"] != HIAI_SOURCE_COMMIT:
            raise ValueError("Mismatched HiAI/MNN source versions")
        if set(hiai["libraries"]) != HIAI_LIBRARIES:
            raise ValueError("Incomplete HiAI runtime manifest")
        for name, checksum in hiai["libraries"].items():
            if hashlib.sha256(archive.read(f"lib/arm64-v8a/{name}")).hexdigest() != checksum:
                raise ValueError(f"HiAI library checksum mismatch: {name}")
        for name in ["libocrhiai.so", "libMNN.so", "libMNN_Backend_HiAI.so", "libLiteRtDispatch_MediaTek.so"]:
            if f"lib/arm64-v8a/{name}" not in names:
                raise ValueError(f"Missing OCR bridge library: {name}")
    for name in ["android_host.py", "android_ocr.py", "sitecustomize.py"]:
        if f"assets/overlays/{name}" not in names:
            raise ValueError(f"Missing runtime overlay: {name}")
    if "assets/ocr/test/sample.png" not in names:
        raise ValueError("Missing OCR test image")
    sample = archive.read("assets/ocr/test/sample.png")
    if not sample.startswith(b"\x89PNG\r\n\x1a\n") or sample[16:24] != (320).to_bytes(4, "big") + (48).to_bytes(4, "big"):
        raise ValueError("Invalid OCR test image dimensions")
    if "assets/ocr/licenses/neuropilot-license.pdf" not in names:
        raise ValueError("Missing NeuroPilot license")
    for name in ["hiai-LICENSE.txt", "mnn-LICENSE.txt", "mnn-flatbuffers-LICENSE.txt", "mnn-half-LICENSE.txt"]:
        if f"assets/ocr/licenses/{name}" not in names:
            raise ValueError(f"Missing NPU license: {name}")


def main():
    """校验命令行指定的 APK。 / Verifies APKs specified on the command line."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("apks", nargs="+", type=Path)
    args = parser.parse_args()
    for path in args.apks:
        with zipfile.ZipFile(path) as archive:
            verify(archive)
        print(f"OCR assets verified: {path.name}")


if __name__ == "__main__":
    main()
