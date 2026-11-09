#!/usr/bin/env python3
"""在 Linux 编译生产 MTK dispatch 包装库，验证公开 ABI 及内存布局处理。

需要 C++17 编译器，不执行 Android 驱动，不能代替真机数值验证。

Compiles the production MTK dispatch wrapper on Linux to check public ABI and tensor layouts.
Requires a C++17 compiler; does not execute Android drivers or establish device accuracy.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]


def main():
    """验证无填充、真实填充、拒绝条件和句柄所有权。

    Checks unpadded and padded tensors, rejection cases, and handle ownership.
    """
    compiler = shutil.which("c++")
    if os.name != "posix" or not compiler:
        raise RuntimeError("Run on Linux with a C++17 compiler")
    native = ROOT / "app/app/src/main/native"
    headers = native / "third_party/litert"
    provenance = json.loads((headers / "provenance.json").read_text())
    if provenance["version"] != "2.1.0rc1":
        raise ValueError("Dispatch headers must match the pinned LiteRT version")
    for name, checksum in provenance["files"].items():
        if hashlib.sha256((headers / name).read_bytes()).hexdigest() != checksum:
            raise ValueError(f"Modified public ABI header: {name}")
    fixture = ROOT / "app/scripts/ocr_mtk_dispatch_fixture.cpp"
    with tempfile.TemporaryDirectory(prefix="ocr-mtk-dispatch-") as temporary:
        directory = Path(temporary)
        flags = [compiler, "-std=c++17", "-O2", "-pthread", f"-I{native / 'third_party/litert'}"]
        shared = flags + ["-shared", "-fPIC"]
        subprocess.run(shared + ["-DFIXTURE_CORE", str(fixture), "-o", str(directory / "libLiteRt.so")], check=True)
        subprocess.run(shared + ["-DFIXTURE_VENDOR", str(fixture), f"-L{directory}", "-lLiteRt",
                       "-Wl,-soname=libLiteRtDispatch_MediaTek.so", "-o",
                       str(directory / "libLiteRtDispatch_MediaTek_Vendor.so")], check=True)
        subprocess.run(shared + [str(native / "ocr_mtk_dispatch.cpp"), "-ldl",
                       "-Wl,-soname=libLiteRtDispatch_MediaTek.so", "-o",
                       str(directory / "libWrapper.so")], check=True)
        executable = directory / "fixture"
        subprocess.run(flags + [str(fixture), f"-L{directory}", "-lLiteRt", "-ldl",
                       "-o", str(executable)], check=True)
        env = os.environ | {"LD_LIBRARY_PATH": str(directory)}
        subprocess.run([str(executable)], env=env, check=True)
        subprocess.run([str(executable), "bad_version"],
                       env=env | {"FIXTURE_BAD_VERSION": "1"}, check=True)
        (directory / "libLiteRtDispatch_MediaTek_Vendor.so").rename(directory / "vendor.disabled")
        subprocess.run([str(executable), "missing_vendor"], env=env, check=True)
        print("MTK dispatch wrapper: wrong ABI and missing vendor rejected")


if __name__ == "__main__":
    main()
