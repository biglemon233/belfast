"""导入固定版本的 Miuix 导航源码，保持 JVM 17 构建基线。

Imports pinned Miuix navigation sources while preserving the JVM 17 build baseline.
"""

import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

REVISION = "39c40f99844227b853f0049a0933b1f3ae6c00ba"


def main():
    """校验上游提交并导入 Android 所需源码与测试。

    Verifies the upstream revision and imports Android sources and tests.
    """
    source = Path(sys.argv[1]).resolve()
    revision = subprocess.check_output(
        ["git", "-C", str(source), "rev-parse", "HEAD"], text=True
    ).strip()
    if revision != REVISION:
        raise ValueError(f"Expected {REVISION}, got {revision}")
    # HEAD 相同不代表工作副本未修改，拒绝导入没有记录的上游补丁。
    subprocess.run(
        ["git", "-C", str(source), "diff", "--quiet", "HEAD", "--", "miuix-nav", "LICENSE"],
        check=True,
    )
    target = Path(__file__).resolve().parents[1] / "navigation-runtime"
    provenance = {}
    for module, source_set, destination in [
        ("miuix-nav", "commonMain", "main"),
        ("miuix-nav", "commonTest", "test"),
    ]:
        root = source / module / "src" / source_set / "kotlin"
        for path in sorted(root.rglob("*.kt")):
            relative = path.relative_to(source).as_posix()
            provenance[relative] = hashlib.sha256(path.read_bytes()).hexdigest()
            if path.name == "NavSystemCornerRadius.kt":
                continue
            output = target / "src" / destination / "kotlin" / path.relative_to(root)
            output.parent.mkdir(parents=True, exist_ok=True)
            content = path.read_text(encoding="utf-8")
            if path.name == "WindowNavigationEventBridge.kt":
                # 普通 Android 模块不需要 expect/actual；保留相同实现与事件转发类。
                android_path = source / "miuix-nav/src/androidMain/kotlin" / path.relative_to(root)
                android_path = android_path.with_name("WindowNavigationEventBridge.android.kt")
                android = android_path.read_text(encoding="utf-8")
                implementation = android[android.index("actual fun"):].replace("actual fun", "fun", 1)
                content = content.replace("expect fun WindowNavigationEventBridge()", implementation)
                extra_imports = sorted(set(re.findall(r"^import .+$", android, re.M)) -
                                       set(re.findall(r"^import .+$", content, re.M)))
                position = content.index("\nimport ")
                content = content[:position] + "\n" + "\n".join(extra_imports) + content[position:]
                provenance[android_path.relative_to(source).as_posix()] = hashlib.sha256(
                    android_path.read_bytes()
                ).hexdigest()
            output.write_text(content, encoding="utf-8")

    android_corner = source / (
        "miuix-nav/src/androidMain/kotlin/top/yukonga/miuix/kmp/nav/core/"
        "NavSystemCornerRadius.android.kt"
    )
    output = target / "src/main/kotlin/top/yukonga/miuix/kmp/nav/core/NavSystemCornerRadius.kt"
    output.write_text(android_corner.read_text(encoding="utf-8").replace("actual fun", "fun", 1),
                      encoding="utf-8")
    provenance[android_corner.relative_to(source).as_posix()] = hashlib.sha256(
        android_corner.read_bytes()
    ).hexdigest()
    pager = source / "miuix-ui/src/commonMain/kotlin/top/yukonga/miuix/kmp/utils/PagerGestureUtils.kt"
    subprocess.run(["git", "-C", str(source), "diff", "--quiet", "HEAD", "--", str(pager)], check=True)
    pager_output = target / "src/main/kotlin/top/yukonga/miuix/kmp/utils/PagerGestureUtils.kt"
    pager_output.parent.mkdir(parents=True, exist_ok=True)
    pager_output.write_bytes(pager.read_bytes())
    provenance[pager.relative_to(source).as_posix()] = hashlib.sha256(pager.read_bytes()).hexdigest()
    (target / "LICENSE").write_bytes((source / "LICENSE").read_bytes())
    (target / "UPSTREAM.json").write_text(
        json.dumps({"repository": "https://github.com/compose-miuix-ui/miuix",
                    "version": "0.9.4", "revision": REVISION, "source_sha256": provenance},
                   ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )


if __name__ == "__main__":
    main()
