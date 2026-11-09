"""UiAutomator2-compatible selectors backed by AOS's privileged UI bridge.

The AOS serial is not an ADB transport. Keep ordinary u2 connections intact and
use UiAutomation in the privileged process for UI queries and actions.
"""
import functools
import json
import os
import re
import shlex
import socket
import time


class BridgeU2:
    def __init__(self):
        self.wait_timeout = 20.0

    def _call(self, **payload):
        address = os.environ.get('ALASAOS_PROXY_ADDR', '127.0.0.1:22300')
        host, port = address.rsplit(':', 1)
        # Separate connections keep UI replies away from the screenshot stream.
        # Never replay a click/text action after a lost response.
        with socket.create_connection((host, int(port)), timeout=10) as sock:
            sock.settimeout(60)
            sock.sendall(json.dumps(dict(payload, id=1)).encode('utf-8') + b'\n')
            with sock.makefile('rb') as stream:
                line = stream.readline(4 * 1024 * 1024 + 1)
        if not line.endswith(b'\n') or len(line) > 4 * 1024 * 1024:
            raise RuntimeError('Invalid AOS UI bridge response')
        result = json.loads(line)
        if not result.get('ok'):
            raise RuntimeError(f'AOS UI bridge: {result.get("error", result.get("stderr", "request failed"))}')
        return result

    def dump_hierarchy(self):
        return self._call(method='ui', action='dump')['xml']

    def xpath(self, expression):
        return BridgeSelector(self, expression=expression)

    def __call__(self, **kwargs):
        return BridgeSelector(self, attributes=kwargs)

    def _shell(self, command):
        return self._call(method='shell', cmd=command)['stdout']

    def app_start(self, package_name, activity=None):
        package = shlex.quote(package_name)
        if activity is None:
            output = self._shell(f'cmd package resolve-activity --brief --user current {package}')
            component = output.strip().splitlines()[-1] if output.strip() else ''
            if '/' not in component or component.startswith('android/'):
                # Applications may expose multiple launcher activities; prefer a
                # concrete launcher rather than opening Android's resolver dialog.
                output = self._shell(
                    f'cmd package query-activities --brief --components --user current '
                    f'-a android.intent.action.MAIN -c android.intent.category.LAUNCHER {package}')
                component = next((line.strip() for line in output.splitlines()
                                  if line.strip().startswith(package_name + '/')), '')
            if not component:
                raise RuntimeError(f'No launcher activity for {package_name}')
        else:
            component = f'{package_name}/{activity}'
        output = self._shell(f'am start --user current -n {shlex.quote(component)}')
        if 'Error:' in output or 'Exception' in output:
            raise RuntimeError(output)

    def app_stop(self, package_name):
        self._shell(f'am force-stop --user current {shlex.quote(package_name)}')

    def reset_uiautomator(self):
        self._call(method='ui', action='reset')


class BridgeSelector:
    _ATTRIBUTES = {'resourceId': 'resource-id', 'text': 'text',
                   'description': 'content-desc', 'className': 'class',
                   'packageName': 'package', 'clickable': 'clickable',
                   'enabled': 'enabled', 'focused': 'focused', 'selected': 'selected'}

    def __init__(self, device, expression=None, attributes=None):
        self.device = device
        self.expression = expression
        self.attributes = attributes or {}
        unsupported = self.attributes.keys() - self._ATTRIBUTES.keys()
        if unsupported:
            raise NotImplementedError(f'Unsupported AOS UI selector: {sorted(unsupported)}')

    def _nodes(self):
        from lxml import etree
        root = etree.fromstring(self.device.dump_hierarchy().encode('utf-8'),
                                parser=etree.XMLParser(resolve_entities=False, no_network=True))
        # Match u2 XPath's class-name tags (e.g. /android.widget.ImageView[last()]).
        for node in root.iter('node'):
            class_name = node.get('class', '')
            node.tag = re.sub(r'[^\w.\-]', '_', class_name) or 'node'
        if self.expression is not None:
            return root.xpath(self.expression)
        return [node for node in root.iter() if node is not root and all(
            node.get(self._ATTRIBUTES[key]) == (str(value).lower() if isinstance(value, bool) else str(value))
            for key, value in self.attributes.items())]

    @property
    def exists(self):
        return bool(self._nodes())

    def _wait_node(self, timeout):
        deadline = time.monotonic() + max(0, timeout)
        while True:
            nodes = self._nodes()
            if nodes:
                return nodes[0]
            if time.monotonic() >= deadline:
                from uiautomator2.exceptions import UiObjectNotFoundError, XPathElementNotFoundError
                message = f'AOS UI selector not found: {self.expression or self.attributes}'
                if self.expression is not None:
                    raise XPathElementNotFoundError(message)
                raise UiObjectNotFoundError({'code': -32002, 'message': message,
                                             'data': self.attributes})
            time.sleep(min(0.2, max(0, deadline - time.monotonic())))

    def _act(self, action, timeout=None, **kwargs):
        node = self._wait_node(self.device.wait_timeout if timeout is None else timeout)
        return self.device._call(method='ui', action=action,
                                 display_id=int(node.get('display-id')),
                                 window_id=int(node.get('window-id')),
                                 node_path=node.get('node-path'),
                                 resource_id=node.get('resource-id', ''),
                                 class_name=node.get('class', ''), text=node.get('text', ''), **kwargs)

    def click(self, timeout=None):
        self._act('click', timeout=timeout)

    def get_text(self, timeout=None):
        node = self._wait_node(self.device.wait_timeout if timeout is None else timeout)
        return node.get('text', '')

    def send_keys(self, text):
        # ACTION_SET_TEXT replaces existing text and supports Unicode without an IME.
        self._act('set_text', value=str(text))


def install():
    import uiautomator2 as u2
    original = u2.connect
    if getattr(original, '_alasaos_bridge', False) is True:
        return

    @functools.wraps(original)
    def connect(serial=None, *args, **kwargs):
        if isinstance(serial, str) and serial.startswith('alasaos'):
            return BridgeU2()
        return original(serial, *args, **kwargs)

    connect._alasaos_bridge = True
    u2.connect = connect
