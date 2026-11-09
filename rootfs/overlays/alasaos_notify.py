"""AOS delivery and failure guards; upstream files stay intact."""
from contextvars import ContextVar
import functools
import json
import os
from pathlib import Path
import uuid

_delivery = ContextVar('aos_notification_delivery', default=None)


def local_notify(title, content):
    """Publish an atomic app-private event independently of remote delivery."""
    from module.logger import logger
    try:
        directory = Path('log/.alasaos-notifications')
        directory.mkdir(parents=True, exist_ok=True)
        name = uuid.uuid4().hex
        pending = directory / (name + '.tmp')
        pending.write_text(json.dumps({
            'title': str(title or 'ALAS')[:256],
            'content': str(content or '')[:2048],
        }, ensure_ascii=False), encoding='utf-8')
        os.replace(pending, directory / (name + '.json'))
    except Exception:
        logger.warning('AOS local notification unavailable')


def checked_notifier(factory, provider):
    from onepush.exceptions import OnePushException
    from requests import Response
    notifier = factory(provider)
    original = notifier.notify

    @functools.wraps(original)
    def notify(**kwargs):
        response = original(**kwargs)
        if response is None:
            raise OnePushException('No response from push service')
        if str(notifier.name).lower() == 'pushplus' and isinstance(response, Response) and response.status_code == 200:
            try:
                body = response.json()
                accepted = isinstance(body, dict) and body.get('code') == 200
            except ValueError:
                accepted = False
            if not accepted:
                # Response bodies may echo credentials.
                raise OnePushException('PushPlus rejected the message or returned an invalid response')
        return response

    notifier.notify = notify
    return notifier


def guard_task(method):
    @functools.wraps(method)
    def guarded(self, *args, **kwargs):
        parent = _delivery.get()
        state = {'attempted': False}
        token = _delivery.set(state)
        failed = False
        try:
            return method(self, *args, **kwargs)
        except (Exception, SystemExit) as error:
            failed = not isinstance(error, SystemExit) or error.code not in (None, 0)
            stop = getattr(self, 'stop_event', None)
            if failed and not state['attempted'] and not (stop is not None and stop.is_set()):
                from module.logger import logger
                try:
                    from module.notify import handle_notify
                    handle_notify(
                        self.config.Error_OnePushConfig,
                        title=f'Alas <{self.config_name}> crashed',
                        content='Task stopped before error notification; check AOS logs and device settings.',
                    )
                except Exception:
                    logger.warning('AOS failure notification unavailable')
            raise
        finally:
            _delivery.reset(token)
            if failed and state['attempted'] and parent is not None:
                parent['attempted'] = True

    return guarded


def install_task_guards():
    from alas import AzurLaneAutoScript
    if getattr(AzurLaneAutoScript, '_alasaos_notify', False):
        return
    AzurLaneAutoScript._alasaos_notify = True
    for name in ('run', 'loop'):
        setattr(AzurLaneAutoScript, name, guard_task(getattr(AzurLaneAutoScript, name)))
    descriptor = AzurLaneAutoScript.__dict__['device']
    descriptor.func = guard_task(descriptor.func)


def install():
    import module.notify.notify as upstream
    if getattr(upstream, '_alasaos_notify', False):
        return
    upstream._alasaos_notify = True
    factory = upstream.get_notifier
    upstream.get_notifier = lambda provider: checked_notifier(factory, provider)
    original = upstream.handle_notify

    @functools.wraps(original)
    def handle_notify(config, **kwargs):
        state = _delivery.get()
        if state is not None:
            state['attempted'] = True
        local_notify(kwargs.get('title'), kwargs.get('content'))
        return original(config, **kwargs)

    upstream.handle_notify = handle_notify
    import module.webui.process_manager as process_manager
    remove_fake_pil = process_manager.remove_fake_pil_module

    @functools.wraps(remove_fake_pil)
    def prepare_task():
        # This hook runs inside the native task child, after logger setup.
        # Restore Pillow exactly once before importing ALAS.
        remove_fake_pil()
        install_task_guards()

    process_manager.remove_fake_pil_module = prepare_task
