"""验证 AP Android 接线不会加载禁用组件或绕过宿主。

Checks that the AP Android adapter neither loads disabled components nor bypasses the host.
"""

import dataclasses
import hashlib
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace as Namespace
import unittest
import tempfile
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('android_ocr_adapter', Path(__file__).parents[1] / 'overlays/android_ocr.py')
ocr = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ocr)


class AdapterTest(unittest.TestCase):
    """覆盖 NCNN 配置迁移和检测组件初始化。 / Covers NCNN routing and detector initialization."""

    def fixture(self):
        """模拟上游不可变配置和 RapidOCR 组件。 / Models upstream immutable settings and components."""
        @dataclasses.dataclass(frozen=True)
        class Settings:
            backend: str
            device: str
            allow_vendor_execution_providers: bool
            model_version: str

            @classmethod
            def from_config(cls, config, name, *, device=None):
                return cls(config.backend, 'gpu', True, config.model_version)

        class Rapid:
            def _initialize(self, cfg):
                raise AssertionError('Unused classifier must never initialize')

        calls = []
        def component(name):
            def make(cfg):
                calls.append(name)
                return cfg
            return make
        # 函数全局表与 RapidOCR 导入模块相同；用于检查上游公开组件契约。
        Rapid._initialize.__globals__.update(TextDetector=component('det'), TextClassifier=component('cls'),
                                             TextRecognizer=component('rec'), LoadImage=lambda: 'image',
                                             CalRecBoxes=lambda: 'boxes')
        module = Namespace(OcrSettings=Settings, RapidOCR=Rapid)
        ocr.patch_ap_module(module)
        return module, calls

    def test_saved_ncnn_model_choice_routes_host_without_writing_config(self):
        module, calls = self.fixture()
        config = Namespace(backend='ncnn', model_version='pro')
        settings = module.OcrSettings.from_config(config, 'cn')
        self.assertEqual(dataclasses.asdict(settings), {'backend': 'onnxruntime', 'device': 'cpu',
                         'allow_vendor_execution_providers': False, 'model_version': 'pro'})
        self.assertEqual(config.backend, 'ncnn')
        ocr.patch_ap_module(module)
        self.assertEqual(settings, module.OcrSettings.from_config(config, 'cn'))

    def test_detector_and_recognizer_do_not_load_unused_classifier(self):
        module, calls = self.fixture()
        global_cfg = Namespace(text_score=0.5, min_height=30, width_height_ratio=8, use_det=True,
                               use_cls=False, use_rec=True, model_root_dir='/opt/azurpilot',
                               font_path=None, max_side_len=2000, min_side_len=30,
                               return_word_box=False, return_single_char_box=False)
        cfg = Namespace(Global=global_cfg, EngineConfig={'onnxruntime': {}},
                        Det=Namespace(engine_type=Namespace(value='onnxruntime')),
                        Rec=Namespace(engine_type=Namespace(value='onnxruntime')))
        engine = module.RapidOCR()
        engine._initialize(cfg)
        self.assertEqual(calls, ['det', 'rec'])
        self.assertIsNone(engine.text_cls)
        self.assertEqual(engine.cfg, cfg)
        global_cfg.use_rec = False
        calls.clear()
        engine._initialize(cfg)
        self.assertEqual(calls, ['det'])
        self.assertIsNone(engine.text_rec)

    def test_source_update_retires_known_weights_and_keeps_dictionary(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            install = root / 'opt/azurpilot'
            path = install / 'bin/ocr_models/model.onnx'
            path.parent.mkdir(parents=True)
            data = b'official weights'
            path.write_bytes(data)
            digest = hashlib.sha256(data).hexdigest()
            (path.parent / 'dict.txt').write_text('characters')
            (install / 'ocr-host-models.json').write_text(json.dumps({'version': 2, 'models': [
                {'asset': 'models/model.onnx', 'sha256': digest}]}))
            (install / 'ocr-retired-models.json').write_text(json.dumps([
                {'path': 'opt/azurpilot/bin/ocr_models/model.onnx', 'sha256': digest, 'size': len(data)}]))
            with patch.object(ocr, '_read_host_json', return_value={'status': {'model_format_version': 2}}):
                ocr.prepare_runtime_descriptors(install, root)
                self.assertEqual(json.loads(path.read_text()), {'android_ocr_model': 2, 'sha256': digest})
                self.assertEqual((path.parent / 'dict.txt').read_text(), 'characters')
                path.write_bytes(b'changed upstream')
                with self.assertRaisesRegex(RuntimeError, 'AP model changed'):
                    ocr.prepare_runtime_descriptors(install, root)
                self.assertEqual(path.read_bytes(), b'changed upstream')


if __name__ == '__main__':
    unittest.main()
