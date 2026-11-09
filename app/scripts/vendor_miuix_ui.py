"""导入固定版本的 Miuix 原生 UI，保持 Android JVM 17。

Imports pinned Miuix native UI sources for the Android JVM 17 baseline.
"""

import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

REVISION = "39c40f99844227b853f0049a0933b1f3ae6c00ba"
MODULES = ["miuix-core", "miuix-ui", "miuix-preference", "miuix-blur", "miuix-shader"]


def main():
    """校验上游并记录每个导入文件的哈希。 / Verifies upstream and records source hashes."""
    source = Path(sys.argv[1]).resolve()
    revision = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
    if revision != REVISION:
        raise ValueError(f"Expected {REVISION}, got {revision}")
    subprocess.run(["git", "-C", str(source), "diff", "--quiet", "HEAD", "--", *MODULES, "miuix-icons", "LICENSE"], check=True)
    target = Path(__file__).resolve().parents[1] / "miuix-runtime"
    provenance = {}
    for module in MODULES:
        for source_set in ["commonMain", "androidMain"]:
            base = source / module / "src" / source_set / "kotlin"
            for path in sorted(base.rglob("*.kt")):
                if path.name == "PagerGestureUtils.kt":
                    continue
                text = path.read_text(encoding="utf-8")
                provenance[path.relative_to(source).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
                changed = False
                if source_set == "commonMain":
                    if path.name in ["DynamicColors.kt", "WindowNavigationEventScope.kt", "RenderEffectCompat.kt", "RenderEffect.kt"]:
                        continue
                    if module == "miuix-core" and path.name == "Utils.kt":
                        text = text[:text.index("/**\n * Returns the current platform name.")]
                        changed = True
                    else:
                        merged = re.sub(r"(?:@Composable\n)?^(?:internal )?expect (?:fun|val) [^\n]+\n", "", text, flags=re.M)
                        changed = merged != text
                        text = merged
                else:
                    text = text.replace("actual fun ", "fun ").replace("actual val ", "val ")
                    if path.name == "DeviceTilt.android.kt":
                        text = text.replace("smoothing: Float)", "smoothing: Float = 0.15f)")
                    changed = True
                if changed:
                    text = "// Adapted for AzurPilot for Android: merged platform declarations for JVM 17.\n" + text.rstrip() + "\n"
                output = target / "src/main/kotlin" / path.relative_to(base)
                output.parent.mkdir(parents=True, exist_ok=True)
                output.write_text(text, encoding="utf-8")
    icon_root = source / "miuix-icons/src/commonMain/kotlin"
    for path in sorted(icon_root.rglob("*.kt")):
        if path.stem not in ["Back", "Cottage", "Home", "Settings", "Security", "Extension"]:
            continue
        output = target / "src/main/kotlin" / path.relative_to(icon_root)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_bytes(path.read_bytes())
        provenance[path.relative_to(source).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    test_root = source / "miuix-ui/src/commonTest/kotlin"
    for path in sorted(test_root.rglob("*.kt")):
        output = target / "src/test/kotlin" / path.relative_to(test_root)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_bytes(path.read_bytes())
        provenance[path.relative_to(source).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    (target / "LICENSE").write_bytes((source / "LICENSE").read_bytes())
    (target / "UPSTREAM.json").write_text(json.dumps({
        "repository": "https://github.com/compose-miuix-ui/miuix",
        "version": "0.9.4", "revision": REVISION,
        "adaptations": ["Android expect/actual merged for JVM 17; pager supplied by navigation-runtime."],
        "source_sha256": provenance,
    }, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
