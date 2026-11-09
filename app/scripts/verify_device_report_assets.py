"""验证最终 APK 中的机型目录及 CI 凭据，避免资产合并静默改变文件。

Verifies device catalog and CI credentials in final APKs against silent asset transformations.
"""

import argparse
import gzip
import io
from pathlib import Path
from typing import Optional
import zipfile


def verify(apk: Path, credentials_dir: Optional[Path]) -> None:
    """检查最终资产；本地验证同时确保私钥未进入 APK。 / Checks assets and local key absence."""
    with zipfile.ZipFile(apk) as archive:
        with gzip.GzipFile(fileobj=io.BytesIO(archive.read("assets/device-report/device-names.catalog"))) as catalog:
            data = catalog.read(16 * 1024 * 1024 + 1)
        if len(data) > 16 * 1024 * 1024 or not data or any(
            line.count("\t") != 3 for line in data.decode("utf-8").splitlines()
        ):
            raise ValueError("Invalid packaged device catalog")
        for name in ("client-cert.pem", "client-key.pem"):
            asset = "assets/device-report/" + name
            if credentials_dir is None:
                if asset in archive.namelist():
                    raise ValueError("Local APK unexpectedly contains reporting credentials")
            elif archive.read(asset) != (credentials_dir / name).read_bytes():
                raise ValueError("Packaged reporting credentials differ from CI inputs")
    print(f"{apk.name}: catalog verified; credentials {'verified' if credentials_dir else 'absent'}")


def main() -> None:
    """验证指定构建产物，不输出凭据内容。 / Verifies artifacts without printing credentials."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--credentials-dir", type=Path)
    parser.add_argument("apks", nargs="+", type=Path)
    args = parser.parse_args()
    for apk in args.apks:
        verify(apk, args.credentials_dir)


if __name__ == "__main__":
    main()
