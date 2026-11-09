#!/usr/bin/env python3
"""通过受保护的 ADB 入口调试 App，直接输出 JSON；无需 GUI、root 或读取控制口令。

Uses the protected ADB app entry to print JSON without GUI, root, or reading control tokens.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

METHODS = ["help", "debug-last", "debug-export", "ocr-status", "ocr-test", "ocr-test-all",
           "ocr-test-cpu", "ocr-test-mixed", "ocr-test-gpu-softmax", "ocr-hardware-acceleration", "ocr-ap-test",
           "ocr-ap-config-test", "runtime-status", "runtime-start", "storage-status"]
MODEL_NAMES = {"tiny": "PP-OCRv6_tiny_rec.onnx", "small": "PP-OCRv6_small_rec.onnx",
               "pro": "PP-OCRv6_medium_rec.onnx", "det": "PP-OCRv6_tiny_det.onnx",
               "en": "alocr-en-us-v2.6.nvc.onnx", "zh": "alocr-zh-cn-v3.dtk.onnx"}


def find_adb():
    """优先使用 PATH 或 Android SDK 中的 adb。 / Finds adb in PATH or the Android SDK."""
    found = shutil.which("adb")
    if found:
        return found
    roots = [os.environ.get("ANDROID_HOME"), os.environ.get("ANDROID_SDK_ROOT")]
    if os.environ.get("LOCALAPPDATA"):
        roots.append(str(Path(os.environ["LOCALAPPDATA"]) / "Android/Sdk"))
    for root in filter(None, roots):
        path = Path(root) / "platform-tools" / ("adb.exe" if os.name == "nt" else "adb")
        if path.is_file():
            return str(path)
    raise RuntimeError("adb unavailable; specify --adb or configure Android SDK")


def invoke(adb_args, package, method, model=None):
    """执行固定命令并解析 Android Bundle 中的 JSON。 / Runs a fixed command and parses Bundle JSON."""
    command = adb_args + ["shell", "content", "call", "--uri", f"content://{package}.debug",
                          "--method", method]
    if model:
        command += ["--arg", model]
    process = subprocess.run(command, text=True, capture_output=True, encoding="utf-8", errors="replace")
    marker = "Bundle[{json="
    if process.returncode or marker not in process.stdout or not process.stdout.strip().endswith("}]"):
        raise RuntimeError((process.stderr or process.stdout or "ADB debug command failed").strip())
    return json.loads(process.stdout.split(marker, 1)[1].strip()[:-2])


def main():
    """运行诊断、保存结果，按需导出并拉取 OCR 日志。 / Runs diagnostics and optionally pulls OCR logs."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=METHODS)
    parser.add_argument("model", nargs="?", help="Model alias / ONNX SHA-256, or on / off for the acceleration setting")
    parser.add_argument("--serial", help="ADB device serial")
    parser.add_argument("--adb", help="Path to adb executable")
    parser.add_argument("--package", default="com.azurpilot.ghio")
    parser.add_argument("--output", type=Path, help="Save JSON locally")
    parser.add_argument("--pull-logs", type=Path, help="Export and pull OCR debug ZIP")
    args = parser.parse_args()
    adb_args = [args.adb or find_adb()]
    if args.serial:
        adb_args += ["-s", args.serial]
    model = args.model
    if args.command == "ocr-hardware-acceleration" and model not in ("on", "off"):
        parser.error("ocr-hardware-acceleration requires on or off")
    if args.command != "ocr-hardware-acceleration" and model in MODEL_NAMES:
        status = invoke(adb_args, args.package, "ocr-status")
        if not status.get("ok"):
            raise RuntimeError(status.get("error", "OCR status failed"))
        model = next(item["model_sha256"] for item in status["result"]["model_status"]
                     if item["name"] == MODEL_NAMES[model])
    result = invoke(adb_args, args.package, args.command, model)
    text = json.dumps(result, ensure_ascii=False, indent=2)
    print(text)
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(text + "\n", encoding="utf-8")
    if args.pull_logs:
        export = invoke(adb_args, args.package, "debug-export")
        if not export.get("ok"):
            raise RuntimeError(export.get("error", "OCR log export failed"))
        args.pull_logs.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(adb_args + ["pull", export["result"]["archive_path"], str(args.pull_logs)], check=True)
    return 0 if result.get("ok") or (args.command == "debug-last" and result.get("state") == "running") else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (RuntimeError, ValueError, StopIteration) as error:
        print(str(error) or "Unknown bundled model", file=sys.stderr)
        sys.exit(1)
