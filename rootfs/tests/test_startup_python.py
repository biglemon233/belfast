"""Fast stdlib launchers must retain normal site initialization in ALAS children."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class StartupPythonTests(unittest.TestCase):
    def setUp(self):
        base = ROOT / '.tmp/startup-python-tests'
        base.mkdir(parents=True, exist_ok=True)
        tmp = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        (self.root / 'sitecustomize.py').write_text(
            'import os\nfrom pathlib import Path\n'
            'Path(os.environ["SITE_PROBE"]).write_text("loaded")\n')
        self.marker = self.root / 'site-loaded'
        self.env = dict(os.environ, PYTHONPATH=str(self.root), SITE_PROBE=str(self.marker),
                        PYTHONDONTWRITEBYTECODE='1')

    def test_fast_regenerator_still_runs_upstream_with_site_and_patches_options(self):
        seeds = self.root / 'seeds'
        seeds.mkdir()
        shutil.copyfile(ROOT / 'rootfs/seeds/regen_args.py', seeds / 'regen_args.py')
        module = self.root / 'module/config'
        module.mkdir(parents=True)
        (module / 'config_updater.py').write_text(
            'import json, sys\nfrom pathlib import Path\n'
            'assert not sys.flags.no_site\n'
            'assert Path("site-loaded").read_text() == "loaded"\n'
            'args={"Alas":{"Emulator":{name:{"option":["adb"]} for name in '
            '("ScreenshotMethod","ControlMethod")}}}\n'
            'Path("module/config/argument").mkdir()\n'
            'Path("module/config/argument/args.json").write_text(json.dumps(args))\n'
            'Path("module/config/i18n").mkdir()\n'
            'Path("module/config/i18n/zh-CN.json").write_text(json.dumps('
            '{"Emulator":{"ScreenshotMethod":{},"ControlMethod":{}}}))\n')
        subprocess.run([sys.executable, '-S', str(seeds / 'regen_args.py')],
                       env=self.env, check=True, capture_output=True, timeout=15)
        args = json.loads((module / 'argument/args.json').read_text(encoding='utf-8'))
        zh = json.loads((module / 'i18n/zh-CN.json').read_text(encoding='utf-8'))
        for name in ('ScreenshotMethod', 'ControlMethod'):
            self.assertEqual(args['Alas']['Emulator'][name]['option'], ['adb', 'alasaos'])
            self.assertEqual(zh['Emulator'][name]['alasaos'], 'ALAS-AOS 桥')

    @unittest.skipUnless(os.name == 'posix', 'The wrapper uses Linux fcntl and process groups')
    def test_fast_wrapper_still_starts_gui_with_normal_site_initialization(self):
        shutil.copyfile(ROOT / 'rootfs/overlays/wrapper.py', self.root / 'wrapper.py')
        (self.root / 'alasaos_gui.py').write_text(
            'import sys\nfrom pathlib import Path\n'
            'assert not sys.flags.no_site\n'
            'assert Path("site-loaded").read_text() == "loaded"\n'
            'Path("gui-started").write_text("ok")\n')
        driver = self.root / 'driver.py'
        driver.write_text(
            'import sys\nassert sys.flags.no_site\nimport wrapper\n'
            'wrapper._start_gui_once()\n'
            'assert wrapper._gui.wait(timeout=10) == 0\n')
        subprocess.run([sys.executable, '-S', str(driver)], cwd=self.root, env=self.env,
                       check=True, capture_output=True, timeout=15)
        self.assertEqual((self.root / 'gui-started').read_text(), 'ok')


if __name__ == '__main__':
    unittest.main()
