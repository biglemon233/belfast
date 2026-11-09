"""验证无权重运行时的清理范围、版本门与字典保留。

Checks cleanup scope, version gates, and dictionary preservation for weight-free runtimes.
"""

import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('compact_ocr', Path(__file__).parents[1] / 'build/compact-ocr-models.py')
compact_ocr = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(compact_ocr)


class CompactOcrTest(unittest.TestCase):
    """真实文件验证删除和失败前的完整性。 / Checks deletion and pre-failure integrity with files."""

    def fixture(self, root):
        """创建模型、字典及旧 NCNN 权重。 / Creates models, dictionaries, and old NCNN weights."""
        folder = root / 'opt/azurpilot/bin/ocr_models'
        folder.mkdir(parents=True)
        models = []
        for index in range(6):
            data = f'weights-{index}'.encode()
            (folder / f'model{index}.onnx').write_bytes(data)
            models.append({'asset': f'models/model{index}.onnx', 'sha256': hashlib.sha256(data).hexdigest()})
        (folder / 'dict.txt').write_text('characters')
        (folder / 'unused.bin').write_bytes(b'NCNN')
        return {'version': 2, 'models': models}, folder

    def test_weights_removed_but_all_descriptors_and_dictionary_remain(self):
        with tempfile.TemporaryDirectory() as temp:
            manifest, folder = self.fixture(Path(temp))
            report = compact_ocr.compact(Path(temp), manifest)
            self.assertEqual(report['descriptor_count'], 6)
            self.assertEqual(report['removed_bytes'], 6 * len(b'weights-0') + 4)
            self.assertFalse((folder / 'unused.bin').exists())
            self.assertEqual((folder / 'dict.txt').read_text(), 'characters')
            for index, spec in enumerate(manifest['models']):
                self.assertEqual(json.loads((folder / f'model{index}.onnx').read_text()),
                                 {'android_ocr_model': 2, 'sha256': spec['sha256']})

    def test_changed_active_model_fails_before_deletion(self):
        with tempfile.TemporaryDirectory() as temp:
            manifest, folder = self.fixture(Path(temp))
            (folder / 'model5.onnx').write_bytes(b'changed')
            with self.assertRaisesRegex(ValueError, 'AP model changed'):
                compact_ocr.compact(Path(temp), manifest)
            self.assertEqual((folder / 'model0.onnx').read_bytes(), b'weights-0')
            self.assertTrue((folder / 'unused.bin').exists())

    def test_external_symlink_never_deleted(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            root = base / 'root'
            manifest, folder = self.fixture(root)
            outside = base / 'private.onnx'
            outside.write_bytes(b'private')
            try:
                (folder / 'external.onnx').symlink_to(outside)
            except OSError:
                self.skipTest('Symlink creation unavailable')
            with self.assertRaisesRegex(ValueError, 'escapes'):
                compact_ocr.compact(root, manifest)
            self.assertEqual(outside.read_bytes(), b'private')
            self.assertEqual((folder / 'model0.onnx').read_bytes(), b'weights-0')


if __name__ == '__main__':
    unittest.main()
