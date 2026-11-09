"""Loopback adapter to the running ALAS WebUI's own ProcessManager.

No second scheduler, task process, retry policy or independent log selection.
Loaded by the AOS entrypoint; upstream files remain unchanged.
"""
import functools
import json
import io
from pathlib import Path
import re
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse


def normalize_config_target(config_name, mod_name='alas'):
    """Treat uploaded/new-config .json suffixes as filenames, not submodules."""
    if mod_name == 'json':
        mod_name = 'alas'
    if mod_name == 'alas':
        while config_name.endswith('.json'):
            config_name = config_name[:-5]
    if not config_name or any(char in config_name for char in '.\\/:*?"\'<>|\x00'):
        raise ValueError('Invalid configuration name')
    return config_name, mod_name


def install_config_writer():
    from module.config.config_updater import ConfigUpdater

    original_write = ConfigUpdater.write_file

    @functools.wraps(original_write)
    def write(config_name, data, mod_name='alas'):
        name, mod = normalize_config_target(config_name, mod_name)
        if (name, mod) != (config_name, mod_name):
            from module.config.utils import filepath_config
            # An invalid filename must not silently overwrite another instance.
            if Path(filepath_config(name, mod)).exists():
                raise ValueError('Configuration already exists')
        return original_write(name, data, mod)

    ConfigUpdater.write_file = staticmethod(write)


def install_config_import():
    """Use the native add dialog's cancellation flow for configuration imports."""
    from importlib import import_module
    webui = import_module('module.webui.app')
    original_upload = webui.file_upload
    if getattr(original_upload, '_alasaos_cancelable', False):
        return

    @functools.wraps(original_upload)
    def upload(*args, **kwargs):
        if ('name' in kwargs or kwargs.get('accept') != '.json' or
                kwargs.get('label') != webui.t('Gui.AppManage.Import')):
            return original_upload(*args, **kwargs)
        field = original_upload(*args, name='alasaos_config_file', **kwargs)
        result = webui.input_group(label=kwargs['label'], inputs=[field], cancelable=True)
        # The upstream callback already returns without writing on None.
        return result.get('alasaos_config_file') if result else None

    upload._alasaos_cancelable = True
    webui.file_upload = upload


class Control:
    def __init__(self, managers, state, console_factory):
        self.managers = managers
        self.state = state
        self.console_factory = console_factory
        self.last_config = 'alas'
        self.lock = threading.RLock()
        self.stop_event = lambda: None
        self._state_cache = {}
        self.theme_setter = None

    def configs(self):
        from pathlib import Path
        return sorted(p.stem for p in Path('config').glob('*.json')
                      if not p.stem.startswith('template'))

    def current(self):
        running = self.managers.running_instances()
        if running:
            self.last_config = running[0].config_name
        return self.managers.get_manager(self.last_config)

    def usable_configs(self):
        """Count configurations the GUI can open, not arbitrary *.json files."""
        out = []
        names = self.configs()
        for name in names:
            path = Path('config') / (name + '.json')
            if path.is_symlink() or path.resolve().parent != Path('config').resolve():
                continue
            if '.' in name:
                instance, mod = name.rsplit('.', 1)
                from module.submodule.utils import get_available_mod, get_mod_dir
                if mod not in get_available_mod() or not Path('submodule', get_mod_dir(mod)).is_dir():
                    continue
            else:
                instance, mod = name, 'alas'
            # Upstream indexes mod instances by the base name alone; a malformed
            # sibling can shadow an otherwise valid ordinary configuration.
            if sum(other.split('.', 1)[0] == instance for other in names) != 1:
                continue
            try:
                if normalize_config_target(instance, mod) != (instance, mod):
                    continue
                if not isinstance(json.loads(path.read_text(encoding='utf-8')), dict):
                    continue
            except (OSError, ValueError):
                continue
            out.append(name)
        return out

    def delete_config(self, config):
        # Shared with native WebUI starts/stops: a running configuration cannot
        # disappear between the alive check and the atomic move.
        with self.lock:
            if (not config or '/' in config or '\\' in config or '\x00' in config
                    or config not in self.configs()):
                raise ValueError('unknown_configuration')
            path = Path('config') / (config + '.json')
            if path.is_symlink() or path.resolve().parent != Path('config').resolve():
                raise ValueError('unknown_configuration')
            instance = config.rsplit('.', 1)[0] if '.' in config else config
            if any(m.config_name in (config, instance) for m in self.managers.running_instances()):
                raise ValueError('configuration_running')
            remaining = [name for name in self.usable_configs() if name != config]
            if not remaining:
                raise ValueError('last_configuration')
            # Keep a private recovery copy, outside the list of selectable JSONs.
            backup = Path('config/.alasaos-deleted') / uuid.uuid4().hex
            backup.mkdir(parents=True)
            path.rename(backup / path.name)
            if self.last_config in (config, instance):
                self.last_config = remaining[0]
            self._state_cache.pop(instance, None)
            return {'deleted': config}

    def logs(self, count=200):
        # Native run_process sets this instance's file logger before any imports.
        # Restrict selection to that instance; GUI diagnostics can never win.
        manager = self.current()
        pattern = re.compile(r'^\d{4}-\d{2}-\d{2}_' + re.escape(manager.config_name) + r'\.txt$')
        candidates = [p for p in Path('log').glob('*.txt') if pattern.fullmatch(p.name)]
        if not candidates:
            return ''
        path = max(candidates, key=lambda p: p.stat().st_mtime)
        with path.open('rb') as stream:
            stream.seek(0, 2)
            stream.seek(max(0, stream.tell() - 256 * 1024))
            text = stream.read().decode('utf-8', errors='replace')
        return '\n'.join(text.splitlines()[-count:])

    def status(self):
        manager = self.current()
        alive = manager.alive
        latest = manager.renderables[-1] if manager.renderables else None
        key = (alive, id(latest), len(manager.renderables))
        cached = self._state_cache.get(manager.config_name)
        if cached is None or cached[0] != key:
            cached = (key, manager.state)
            self._state_cache[manager.config_name] = cached
        func = getattr(manager, '_alasaos_func', 'alas')
        tool = func in ('Daemon', 'EventStory')
        theme = getattr(self.state, 'theme', None)
        if theme is None:
            deploy_config = getattr(self.state, 'deploy_config', None)
            theme = getattr(deploy_config, 'Theme', 'default') if deploy_config else 'default'
        return dict(runner_alive=alive and not tool,
                    runner_wanted=alive and not tool, runner_respawns=0,
                    runner_state=cached[1],
                    pid=manager._process.pid if alive else None,
                    config=manager.config_name, gui_alive=True,
                    theme=theme,
                    log_lines=len(manager.renderables), log_file=None,
                    tool_alive=alive and tool,
                    tool_name={'Daemon': 'daemon', 'EventStory': 'event_story'}.get(func),
                    tool_pid=manager._process.pid if alive and tool else None)

    def set_theme(self, theme):
        if theme not in ('dark', 'default'):
            raise ValueError('Invalid theme')
        with self.lock:
            if callable(self.theme_setter):
                self.theme_setter(theme)
            else:
                from module.webui.app import AlasGUI
                AlasGUI.set_theme(theme)
            return {'theme': theme}

    def start(self, config, func=None):
        if (not config or any(c in config for c in '.\\/:*?"\'<>|\x00') or config.startswith('-')
                or config not in self.configs()):
            raise ValueError('Unknown configuration')
        manager = self.managers.get_manager(config)
        manager.start(func=func, ev=self.stop_event())
        self.last_config = config
        return self.status()

    def stop(self):
        # AOS has a global stop button; use the same stop as WebUI stop-all.
        for manager in list(self.managers.running_instances()):
            manager.stop()
        return self.status()


def render_log(log, renderable):
    # Rich 15 capture consumes output before record/export_html sees it.
    # A private StringIO sink preserves recording through Rich's public API.
    from module.webui.utils import LOG_CODE_FORMAT
    previous = log.console.file
    try:
        log.console.file = io.StringIO()
        log.console.print(renderable)
        return log.console.export_html(theme=log.terminal_theme, clear=True,
                                       code_format=LOG_CODE_FORMAT, inline_styles=True)
    finally:
        log.console.file = previous


def install():
    from module.webui.setting import State
    from module.webui.process_manager import ProcessManager
    from rich.console import Console

    install_config_writer()

    from module.webui.widgets import RichLog
    RichLog.render = render_log

    control = Control(ProcessManager, State, Console)
    def stop_event():
        from module.webui.app import updater
        return updater.event

    control.stop_event = stop_event
    original_start = ProcessManager.start
    original_stop = ProcessManager.stop

    @functools.wraps(original_start)
    def start(manager, func, *args, **kwargs):
        with control.lock:
            # Old browser sessions may still hold a manager after deletion.
            from module.config.utils import filepath_config
            from module.submodule.utils import get_config_mod
            if not Path(filepath_config(manager.config_name, get_config_mod(manager.config_name))).is_file():
                raise ValueError('Unknown configuration')
            if not manager.alive:
                manager._alasaos_func = func or 'alas'
                control.last_config = manager.config_name
            return original_start(manager, func, *args, **kwargs)

    @functools.wraps(original_stop)
    def stop(manager, *args, **kwargs):
        with control.lock:
            return original_stop(manager, *args, **kwargs)

    ProcessManager.start = start
    ProcessManager.stop = stop
    original_init = State.init
    original_clearup = State.clearup
    server = None

    def init(cls):
        nonlocal server
        original_init()
        install_config_import()

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def handle_request(self):
                url = urlparse(self.path)
                query = parse_qs(url.query)
                code = 200
                content_type = 'application/json; charset=utf-8'
                try:
                    with control.lock:
                        if self.command == 'GET' and url.path == '/status':
                            result = control.status()
                        elif self.command == 'GET' and url.path == '/configs':
                            result = {'configs': control.configs()}
                        elif self.command == 'POST' and url.path == '/configs/delete':
                            result = control.delete_config(query.get('config', [''])[0])
                        elif self.command == 'GET' and url.path == '/logs':
                            count = max(1, min(int(query.get('tail', ['200'])[0]), 2000))
                            result = control.logs(count)
                            content_type = 'text/plain; charset=utf-8'
                        elif self.command == 'POST' and url.path in ('/start', '/tool/start'):
                            func = None
                            if url.path == '/tool/start':
                                func = {'daemon': 'Daemon', 'event_story': 'EventStory'}.get(
                                    query.get('name', [''])[0])
                                if func is None:
                                    raise ValueError('Unknown tool')
                            result = control.start(query.get('config', ['alas'])[0], func)
                        elif self.command == 'POST' and url.path in ('/stop', '/tool/stop'):
                            result = control.stop()
                        elif self.command == 'POST' and url.path == '/theme':
                            result = control.set_theme(query.get('theme', ['default'])[0])
                        else:
                            code, result = 404, {'error': 'not found'}
                except ValueError as exc:
                    code, result = 400, {'error': str(exc)}
                except Exception:
                    from module.logger import logger
                    logger.exception('AOS control request failed')
                    code, result = 503, {'error': 'ALAS control unavailable; see GUI diagnostic log'}
                body = (result if isinstance(result, str) else json.dumps(result)).encode('utf-8')
                self.send_response(code)
                self.send_header('Content-Type', content_type)
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            do_GET = do_POST = handle_request

        server = ThreadingHTTPServer(('127.0.0.1', 22401), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()

    def clearup(cls):
        nonlocal server
        if server is not None:
            server.shutdown()
            server.server_close()
            server = None
        original_clearup()

    State.init = classmethod(init)
    State.clearup = classmethod(clearup)
