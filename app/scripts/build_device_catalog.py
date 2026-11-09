"""把 Google 公开机型表压缩成供 App 离线查询的目录。

Compresses Google's public device catalog for offline lookup in the app.
"""

import argparse
import csv
import gzip
import io
from pathlib import Path
import urllib.request

SOURCE_URL = "https://storage.googleapis.com/play_public/supported_devices.csv"
MAX_SOURCE_BYTES = 16 * 1024 * 1024


def build_catalog(source: bytes) -> bytes:
    """验证字段并生成去重的 UTF-8 目录。 / Validates columns and deduplicates UTF-8 entries."""
    if len(source) > MAX_SOURCE_BYTES:
        raise ValueError("Device catalog exceeds the size limit")
    encoding = "utf-16" if source.startswith((b"\xff\xfe", b"\xfe\xff")) else "utf-8-sig"
    reader = csv.DictReader(io.StringIO(source.decode(encoding)))
    columns = ("Retail Branding", "Model", "Device", "Marketing Name")
    if reader.fieldnames is None or not all(key in reader.fieldnames for key in columns):
        raise ValueError("Unexpected device catalog columns")
    rows = set()
    for row in reader:
        values = [" ".join((row.get(key) or "").split()) for key in columns]
        if not values[0] or not values[1] or not values[3] or any(len(v) > 256 for v in values):
            continue
        rows.add("\t".join([v.lower() for v in values[:3]] + [values[3]]))
    if not rows:
        raise ValueError("Device catalog is empty")
    return ("\n".join(sorted(rows)) + "\n").encode("utf-8")


def main() -> None:
    """下载或读取官方 CSV，再生成确定性的 gzip 资产。 / Builds a deterministic gzip asset."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, help="Use a previously downloaded CSV")
    # 使用独立扩展名，避免 Android 资产合并器自动解压并去掉 .gz 后缀。
    parser.add_argument("--output", type=Path,
                        default=Path(".tmp/device-catalog/device-report/device-names.catalog"))
    args = parser.parse_args()
    if args.source:
        source = args.source.read_bytes()
    else:
        with urllib.request.urlopen(SOURCE_URL, timeout=30) as response:
            source = response.read(MAX_SOURCE_BYTES + 1)
    catalog = build_catalog(source)
    compressed = gzip.compress(catalog, compresslevel=9, mtime=0)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_bytes(compressed)
    print(f"Device catalog: {catalog.count(chr(10).encode())} entries, {len(compressed)} bytes")


if __name__ == "__main__":
    main()
