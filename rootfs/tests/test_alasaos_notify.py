import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
from types import ModuleType, SimpleNamespace
import unittest
from unittest.mock import Mock, patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('aos_notify', ROOT/'rootfs/overlays/alasaos_notify.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class PushError(Exception):
    pass


class Response:
    status_code = 200

    def __init__(self, body):
        self.body = body

    def json(self):
        if isinstance(self.body, Exception):
            raise self.body
        return self.body


class NotificationTests(unittest.TestCase):
    def setUp(self):
        self.logger = Mock()
        self.remote = Mock()
        self.modules = {
            'module.logger': SimpleNamespace(logger=self.logger),
            'module.notify': SimpleNamespace(handle_notify=self.deliver),
            'onepush.exceptions': SimpleNamespace(OnePushException=PushError),
            'requests': SimpleNamespace(Response=Response),
        }
        self.patch = patch.dict(sys.modules, self.modules)
        self.patch.start()
        self.addCleanup(self.patch.stop)
        self.instance = SimpleNamespace(config_name='isolated', stop_event=None,
            config=SimpleNamespace(Error_OnePushConfig='provider: null'))

    def deliver(self, *args, **kwargs):
        state = m._delivery.get()
        if state is not None:
            state['attempted'] = True
        return self.remote(*args, **kwargs)

    def checked(self, result, provider='pushplus'):
        notifier = SimpleNamespace(name=provider, notify=Mock(return_value=result))
        return m.checked_notifier(lambda name: notifier, provider).notify

    def test_pushplus_checks_business_status_not_only_http(self):
        for body in ({'code': 400}, {'code': 500}, {}, [], ValueError('invalid')):
            with self.subTest(body=body), self.assertRaises(PushError):
                self.checked(Response(body))()
        response = Response({'code': 200})
        self.assertIs(self.checked(response)(), response)

    def test_no_response_is_failure_for_every_provider(self):
        for name in ('pushplus', 'custom', 'telegram'):
            with self.subTest(name=name), self.assertRaises(PushError):
                self.checked(None, name)()

    def test_other_provider_response_is_preserved(self):
        response = Response({'other_provider_status': True})
        self.assertIs(self.checked(response, 'custom')(), response)

    def test_startup_exit_notifies_and_preserves_exit_code(self):
        @m.guard_task
        def task(self):
            raise SystemExit(1)
        with self.assertRaises(SystemExit) as error:
            task(self.instance)
        self.assertEqual(error.exception.code, 1)
        self.remote.assert_called_once()

    def test_nested_failure_and_existing_notify_do_not_duplicate(self):
        @m.guard_task
        def inner(self):
            self_notify = sys.modules['module.notify'].handle_notify
            self_notify('test', title='Original failure')
            raise SystemExit(1)
        @m.guard_task
        def outer(self):
            return inner(self)
        with self.assertRaises(SystemExit):
            outer(self.instance)
        self.remote.assert_called_once()

    def test_recovered_error_does_not_suppress_later_failure(self):
        @m.guard_task
        def recovered(self):
            self_notify = sys.modules['module.notify'].handle_notify
            self_notify('test', title='Recovered')
        @m.guard_task
        def outer(self):
            recovered(self)
            raise RuntimeError('later failure')
        with self.assertRaises(RuntimeError):
            outer(self.instance)
        self.assertEqual(self.remote.call_count, 2)

    def test_clean_exit_and_update_stop_do_not_notify(self):
        @m.guard_task
        def task(self):
            raise SystemExit(0)
        with self.assertRaises(SystemExit):
            task(self.instance)
        self.instance.stop_event = SimpleNamespace(is_set=lambda: True)
        @m.guard_task
        def stopped(self):
            raise SystemExit(1)
        with self.assertRaises(SystemExit):
            stopped(self.instance)
        self.remote.assert_not_called()

    def test_notification_failure_preserves_original_error(self):
        self.remote.side_effect = RuntimeError('delivery unavailable')
        @m.guard_task
        def task(self):
            raise ValueError('original failure')
        with self.assertRaisesRegex(ValueError, 'original failure'):
            task(self.instance)

    def test_atomic_local_event_is_bounded_and_contains_no_config(self):
        base = ROOT/'.tmp/notify-tests'
        base.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=base) as directory:
            before = Path.cwd()
            try:
                os.chdir(directory)
                m.local_notify('x'*300, 'y'*3000)
                events = list(Path('log/.alasaos-notifications').iterdir())
                self.assertEqual(len(events), 1)
                self.assertEqual(events[0].suffix, '.json')
                event = json.loads(events[0].read_text(encoding='utf-8'))
                self.assertEqual(len(event['title']), 256)
                self.assertEqual(len(event['content']), 2048)
                self.assertEqual(set(event), {'title', 'content'})
            finally:
                os.chdir(before)


if __name__ == '__main__':
    unittest.main()
