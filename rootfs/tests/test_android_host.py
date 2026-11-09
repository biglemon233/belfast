"""验证 Android 宿主从部署配置读取连接地址，且不泄露访问口令。

Verifies deployment-based Android endpoints without exposing the access password.
"""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


spec = importlib.util.spec_from_file_location(
    'android_host', Path(__file__).resolve().parents[1] / 'overlays/android_host.py')
host = importlib.util.module_from_spec(spec)
spec.loader.exec_module(host)


class ConnectionConfigTest(unittest.TestCase):
    """验证自定义端口、回环连接与无效配置的拒绝。

    Verifies custom ports, loopback access, and rejection of invalid configuration.
    """

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        (self.root / 'config').mkdir()

    def write_config(self, bind='0.0.0.0', port=25560, tls=False):
        """写入带特殊字符口令的真实 YAML 测试配置。

        Writes real YAML containing a password with special characters.
        """
        (self.root / 'config/deploy.yaml').write_text(
            f'Deploy:\n  Webui:\n    WebuiHost: "{bind}"\n    WebuiPort: {port}\n'
            '    Password: "test # password: with quotes"\n'
            + ('    WebuiSSLKey: key.pem\n    WebuiSSLCert: cert.pem\n' if tls else ''),
            encoding='utf-8')

    def test_custom_port_uses_loopback_and_does_not_expose_password(self):
        self.write_config()
        result = host.connection_config(self.root)
        self.assertEqual(result, {'host': '127.0.0.1', 'port': 25560, 'tls': False})
        self.assertNotIn('password', json.dumps(result).lower())

    def test_ipv6_and_tls(self):
        self.write_config('::', 25600, tls=True)
        self.assertEqual(host.connection_config(self.root),
                         {'host': '[::1]', 'port': 25600, 'tls': True})

    def test_password_comes_from_yaml_instead_of_stale_password_file(self):
        self.write_config()
        (self.root / 'password.txt').write_text('stale password', encoding='utf-8')
        self.assertEqual(host.read_password(self.root), 'test # password: with quotes')

    def test_legacy_numeric_and_quoted_passwords(self):
        for scalar, expected in (('12345', '12345'), ('"0012345"', '0012345'), ('null', None),
                                 ("'it''s # secret'", "it's # secret")):
            with self.subTest(scalar=scalar):
                (self.root / 'config/deploy.yaml').write_text(
                    f'Deploy:\n  Webui:\n    Password: {scalar}\n', encoding='utf-8')
                self.assertEqual(host.read_password(self.root), expected)

    def test_invalid_ports_are_rejected(self):
        for port in (0, -1, 65536):
            with self.subTest(port=port):
                self.write_config(port=port)
                with self.assertRaises(ValueError):
                    host.connection_config(self.root)

    def test_host_that_excludes_loopback_is_rejected(self):
        self.write_config('192.0.2.1')
        with self.assertRaises(ValueError):
            host.connection_config(self.root)


if __name__ == '__main__':
    unittest.main()
