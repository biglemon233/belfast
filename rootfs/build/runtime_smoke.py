#!/usr/bin/env python3
"""Run inside an isolated ARM64 rootfs; write only to the supplied work directory."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import sys


def notification_smoke(alas_root, work):
    """Exercise installed OnePush offline on both ISAs, outside the ALAS tree."""
    import importlib.util
    import threading
    from http.server import BaseHTTPRequestHandler, HTTPServer
    from onepush.__version__ import __version__
    from onepush.providers.pushplus import PushPlus
    from onepush import get_notifier
    from onepush.exceptions import OnePushException

    work.mkdir(parents=True, exist_ok=True)
    import_root = work / 'imports'
    if not (import_root / 'module').exists():
        shutil.copytree(alas_root / 'module', import_root / 'module', symlinks=True)
    sys.path.insert(0, str(import_root))
    # The upstream logger changes cwd on import; give it a disposable module copy.
    from module.logger import logger
    spec = importlib.util.spec_from_file_location('aos_notify_smoke', alas_root / 'alasaos_notify.py')
    adapter = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(adapter)
    os.chdir(work)
    received = []
    reply = {'code': 200}

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            received.append(json.loads(self.rfile.read(int(self.headers['Content-Length']))))
            self.send_response(200)
            self.end_headers()
            self.wfile.write(json.dumps(reply).encode())

        def log_message(self, *args):
            pass

    server = HTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    original_url = PushPlus.base_url
    custom = dict(url=f'http://127.0.0.1:{server.server_port}/send', method='post', datatype='json',
                  data={'title': 'AOS smoke', 'content': 'Local delivery'})
    try:
        PushPlus.base_url = f'http://127.0.0.1:{server.server_port}/send'
        response = adapter.checked_notifier(get_notifier, 'custom').notify(**custom)
        assert response.status_code == 200
        assert received == [{'title': 'AOS smoke', 'content': 'Local delivery'}]
        for code in (200, 400, 500):
            reply['code'] = code
            try:
                adapter.checked_notifier(get_notifier, 'pushplus').notify(token='local-dummy',
                    title='AOS smoke', content='Local status check')
                assert code == 200
            except OnePushException:
                assert code != 200
        events = work / 'log/.alasaos-notifications'
        before = set(events.glob('*.json'))
        adapter.local_notify('AOS local only', 'No remote provider required')
        created = set(events.glob('*.json')) - before
        assert len(created) == 1
        event = created.pop()
        assert json.loads(event.read_text(encoding='utf-8')) == {
            'title': 'AOS local only', 'content': 'No remote provider required'}
        event.unlink()
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)
        PushPlus.base_url = original_url
    try:
        adapter.checked_notifier(get_notifier, 'custom').notify(**custom)
        raise AssertionError('Connection failure must not be reported as success')
    except OnePushException:
        pass
    return {'onepush_version': __version__, 'custom_received': True,
            'pushplus_business_status': True, 'connection_failure': True, 'local_without_provider': True}


def smoke(alas_root, work, basic_codecs=False):
    os.environ['PYTHONDONTWRITEBYTECODE'] = '1'
    sys.dont_write_bytecode = True
    work.mkdir(parents=True, exist_ok=True)
    os.chdir(work)
    # ALAS's original logger changes cwd to its own module parent on import.
    # Import a byte-identical disposable module copy so that its normal logs
    # stay outside the protected ALAS tree. Models are read from the original.
    import_root = work / 'imports'
    if not (import_root / 'module').exists():
        shutil.copytree(alas_root / 'module', import_root / 'module', symlinks=True)
    sys.path.insert(0, str(import_root))
    import numpy as np
    import scipy
    from scipy import linalg, ndimage, signal
    import cv2
    from PIL import Image
    import imageio
    import mxnet as mx
    import cnocr
    import pywebio, uvicorn, fastapi, pydantic, yaml, requests
    import adbutils, uiautomator2, uiautomator2cache
    import ssl
    from zoneinfo import ZoneInfo

    matrix = np.array([[4., 1., 2.], [1., 3., 0.], [2., 0., 5.]])
    assert np.allclose(matrix @ linalg.inv(matrix), np.eye(3))
    numeric = [float(np.linalg.det(matrix)), float(linalg.det(matrix)),
               float(ndimage.gaussian_filter(matrix, 1).sum()),
               float(signal.convolve2d(matrix, np.ones((2, 2))).sum())]
    pixels = np.arange(32 * 48 * 3, dtype=np.uint8).reshape((32, 48, 3))
    ok, encoded = cv2.imencode('.png', pixels)
    assert ok and np.array_equal(cv2.imdecode(encoded, cv2.IMREAD_COLOR), pixels)
    assert cv2.imdecode(cv2.imencode('.jpg', pixels)[1], cv2.IMREAD_COLOR).shape == pixels.shape
    # Exercise MXNet's separate apt OpenCV chain as well as the Python wheel.
    assert tuple(mx.image.imdecode(encoded.tobytes()).shape) == pixels.shape
    # Compare the actual imgcodecs consumer across a replacement, not just cv2
    # (the Python wheel carries a separate OpenCV implementation).
    codec_pixels = np.arange(96 * 128 * 3, dtype=np.uint8).reshape((96, 128, 3))
    native_codecs = {}
    formats = ('.png', '.jpg', '.bmp') if basic_codecs else ('.png', '.jpg', '.bmp', '.tiff', '.webp', '.jp2', '.ppm')
    for extension in formats:
        print('NATIVE_DECODE', extension, flush=True)
        ok, buffer = cv2.imencode(extension, codec_pixels)
        assert ok, f'Cannot encode test input: {extension}'
        frame = mx.image.imdecode(buffer.tobytes()).asnumpy()
        assert frame.shape == codec_pixels.shape, (extension, frame.shape)
        native_codecs[extension] = hashlib.sha256(frame.tobytes()).hexdigest()
    gif = work / 'palette.gif'
    Image.fromarray(pixels).convert('P').save(gif)
    decoded = imageio.v2.imread(gif)
    assert decoded.shape[:2] == pixels.shape[:2]
    assert ssl.create_default_context().cert_store_stats()['x509_ca'] > 0
    assert str(ZoneInfo('Asia/Shanghai')) == 'Asia/Shanghai'

    from module.ocr.al_ocr import AlOcr
    # Synthetic inputs contain no user screenshots/configuration. Compare the
    # original models' exact predictions before and after each trim group.
    # Epochs match the upstream module/ocr/models.py definitions.
    ocr_models = {}
    for name, epoch in (('azur_lane', 15), ('azur_lane_jp', 20), ('cnocr', 39), ('jp', 125), ('tw', 63)):
        model_root = alas_root / 'bin/cnocr_models' / name
        labels = set((model_root / 'label_cn.txt').read_text(encoding='utf-8').splitlines())
        assert set('0123456789') <= labels, f'{name} has no numeric OCR alphabet'
        alphabet = ''.join(char for char in '0123456789/' if char in labels)
        model = AlOcr(model_name='densenet-lite-gru', model_epoch=epoch,
                      root=str(model_root), name='trim_validation_' + name)
        predictions = []
        for text in ('12345', '67890', '2026', '100/200'):
            frame = np.full((40, 200), 255, dtype=np.uint8)
            cv2.putText(frame, text, (2, 29), cv2.FONT_HERSHEY_SIMPLEX, 0.9, 0, 2, cv2.LINE_AA)
            prediction = model.atomic_ocr_for_single_line(frame, cand_alphabet=alphabet)
            predictions.append(''.join(prediction))
        assert any(predictions), f'OCR must load the {name} model and recognize text'
        params = model_root / f'cnocr-v1.2.0-densenet-lite-gru-{epoch:04d}.params'
        ocr_models[name] = {'predictions': predictions, 'alphabet': alphabet,
                            'params_sha256': hashlib.sha256(params.read_bytes()).hexdigest()}
    notifications = notification_smoke(alas_root, work / 'notifications')
    return {'notifications': notifications, 'numeric': numeric, 'ocr_predictions': ocr_models['azur_lane']['predictions'],
            'ocr_model_sha256': ocr_models['azur_lane']['params_sha256'], 'ocr_models': ocr_models,
            'versions': {'numpy': np.__version__, 'scipy': scipy.__version__,
                         'cv2': cv2.__version__, 'mxnet': mx.__version__,
                         'imageio': imageio.__version__},
            'png_sha256': hashlib.sha256(encoded.tobytes()).hexdigest(),
            'native_imgcodecs_sha256': native_codecs,
            'native_codecs_profile': 'basic' if basic_codecs else 'full',
            'gif_shape': list(decoded.shape), 'imports_ok': True}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--alas-root', type=Path, default=Path('/opt/alas'))
    p.add_argument('--work', type=Path, default=Path('/aos-validation/work'))
    p.add_argument('--output', type=Path, required=True)
    p.add_argument('--basic-codecs', action='store_true',
                   help='Unmodified official MXNet wheel: common ALAS input formats; no native codec replacement')
    args = p.parse_args()
    report = smoke(args.alas_root.resolve(), args.work.resolve(), args.basic_codecs)
    args.output.write_text(json.dumps(report, indent=2), encoding='utf-8')
    print('RUNTIME_SMOKE_OK', json.dumps(report))


if __name__ == '__main__':
    main()
