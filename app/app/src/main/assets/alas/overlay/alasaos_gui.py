"""AOS bootstrap for the unmodified upstream GUI.

Some forks import adbutils after the WebUI replaces PIL with a lightweight
placeholder. Load adbutils against real Pillow first, before running gui.py.
The upstream multiprocessing reload loop and CLI arguments remain intact.
"""
from pathlib import Path
import runpy

import adbutils  # noqa: F401 - preload before the WebUI installs its PIL stub

from module.webui.utils import TaskHandler


# Preserve the old AOS guard without freezing the entire upstream utils.py.
_upstream_stop = TaskHandler.stop


def _stop_task_handler(self):
    if self._thread is None:
        self.remove_pending_task()
        self._alive = False
        return
    return _upstream_stop(self)


TaskHandler.stop = _stop_task_handler


if __name__ == '__main__':
    from alasaos_notify import install as install_notify
    install_notify()
    from alasaos_u2 import install as install_u2
    install_u2()
    from alasaos_control import install
    install()
    runpy.run_path(str(Path(__file__).with_name('gui.py')), run_name='__main__')
