"""Bootstrap preserves upstream main/argv and preloads against real dependencies."""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class GuiBootstrapTests(unittest.TestCase):
    def test_preload_before_fake_pil_and_preserve_upstream_main(self):
        base = ROOT / '.tmp/gui-bootstrap-tests'
        base.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=base) as tmp:
            target = Path(tmp)
            shutil.copyfile(ROOT / 'rootfs/overlays/alasaos_gui.py', target / 'alasaos_gui.py')
            (target / 'alasaos_control.py').write_text('def install(): pass\n')
            (target / 'alasaos_u2.py').write_text('def install(): pass\n')
            (target / 'alasaos_notify.py').write_text('def install(): pass\n')
            (target / 'PIL.py').write_text('class UnidentifiedImageError(Exception): pass\n')
            (target / 'adbutils.py').write_text('from PIL import UnidentifiedImageError\n')
            utils = target / 'module/webui'
            utils.mkdir(parents=True)
            (utils / 'utils.py').write_text(
                'class TaskHandler:\n'
                ' _thread = None\n _alive = True\n'
                ' def remove_pending_task(self): self.cleared = True\n'
                ' def stop(self): self._thread.join()\n'
                'class Icon:\n STOP = "fork-icon"\n')
            (target / 'gui.py').write_text(
                'import sys\nfrom types import ModuleType\n'
                'sys.modules["PIL"] = ModuleType("PIL")\n'
                'import adbutils\n'
                'assert issubclass(adbutils.UnidentifiedImageError, Exception)\n'
                'from module.webui.utils import TaskHandler, Icon\n'
                'handler = TaskHandler(); handler.stop()\n'
                'assert handler.cleared and not handler._alive\n'
                'assert Icon.STOP == "fork-icon"\n'
                'assert __name__ == "__main__"\n'
                'assert sys.argv[1:] == ["--port", "22268"]\n'
                'print("UPSTREAM_MAIN_OK")\n')
            env = dict(os.environ, PYTHONDONTWRITEBYTECODE='1')
            direct = subprocess.run([sys.executable, str(target / 'gui.py'), '--port', '22268'],
                                    env=env, capture_output=True, text=True, timeout=10)
            self.assertNotEqual(direct.returncode, 0)
            self.assertIn('UnidentifiedImageError', direct.stderr)
            wrapped = subprocess.run([sys.executable, str(target / 'alasaos_gui.py'), '--port', '22268'],
                                     env=env, capture_output=True, text=True, timeout=10)
            self.assertEqual(wrapped.returncode, 0, wrapped.stderr)
            self.assertIn('UPSTREAM_MAIN_OK', wrapped.stdout)


if __name__ == '__main__':
    unittest.main()
