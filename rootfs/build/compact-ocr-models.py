#!/usr/bin/env python3
"""用 APK 模型身份文件替换 rootfs 原权重，保留字典和 AP 配置。

在原始模型质量门之后执行；全部六个模型必须与 APK 清单匹配才开始删除。
仅裁剪构建目录中 AP 与 RapidOCR 的已知模型目录，不跟随符号链接。

Replaces rootfs source weights with APK identity descriptors while retaining dictionaries
and AP configuration. Runs after the source-model gate and validates all six identities
before deleting weights. Prunes only known AP/RapidOCR model folders without following links.
"""

import argparse
import hashlib
import json
from pathlib import Path

WEIGHT_SUFFIXES = {'.onnx', '.bin', '.param', '.params'}


def descriptor(model):
    """返回仅含版本和源模型身份的 JSON。 / Returns weight-free model identity JSON."""
    return (json.dumps({'android_ocr_model': 2, 'sha256': model['sha256']},
                       separators=(',', ':')) + '\n').encode()


def compact(root, manifest):
    """先验证，再删除并写入身份文件。 / Validates before pruning and writing descriptors."""
    root = root.resolve(strict=True)
    install = root / 'opt/azurpilot'
    active = []
    if manifest['version'] != 2 or len(manifest['models']) != 6:
        raise ValueError('Weight-free runtime requires the complete APK model format 2')
    for model in manifest['models']:
        relative = Path(model['asset']).relative_to('models')
        target = install / 'bin/ocr_models' / relative
        if not target.resolve().is_relative_to(root) or target.is_symlink():
            raise ValueError('OCR source escapes the build root')
        if hashlib.sha256(target.read_bytes()).hexdigest() != model['sha256']:
            raise ValueError(f'AP model changed; regenerate APK conversion: {relative}')
        active.append((target, descriptor(model)))
    folders = [install / 'bin/ocr_models', install / 'bin/cnocr_models']
    for venv in [install / '.venv', root / 'opt/azurpilot-venv']:
        folders.extend(venv.glob('lib/python*/site-packages/rapidocr/models'))
    retired = []
    for folder in folders:
        if not folder.exists():
            continue
        if not folder.resolve().is_relative_to(root):
            raise ValueError('OCR model folder escapes the build root')
        for path in folder.rglob('*'):
            if path.suffix not in WEIGHT_SUFFIXES or not path.is_file():
                continue
            if path.is_symlink() or not path.resolve().is_relative_to(root):
                raise ValueError('OCR weight escapes the build root')
            retired.append({'path': path.relative_to(root).as_posix(),
                            'size': path.stat().st_size,
                            'sha256': hashlib.sha256(path.read_bytes()).hexdigest()})
    for item in retired:
        (root / item['path']).unlink()
    for path, data in active:
        path.write_bytes(data)
    return {'model_format': 2, 'descriptor_count': len(active),
            'removed_bytes': sum(item['size'] for item in retired), 'retired': retired}


def main():
    """裁剪构建镜像并写入报告。 / Compacts a build root and writes its report."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('rootfs', type=Path)
    parser.add_argument('manifest', type=Path)
    args = parser.parse_args()
    report = compact(args.rootfs, json.loads(args.manifest.read_text()))
    install = args.rootfs / 'opt/azurpilot'
    (install / 'OCR_SIZE_REPORT.json').write_text(json.dumps(report, indent=2) + '\n')
    (install / 'ocr-host-models.json').write_bytes(args.manifest.read_bytes())
    (install / 'ocr-retired-models.json').write_bytes((args.manifest.parent / 'retired-models.json').read_bytes())
    print(f"OCR_WEIGHTS_REMOVED bytes={report['removed_bytes']} descriptors={report['descriptor_count']}")


if __name__ == '__main__':
    main()
