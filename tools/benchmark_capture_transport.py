"""在设备 PRoot 内对比共享内存和旧 TCP 截图的延迟分布，不注入输入。

旧通道保留上游逐字节读响应头的行为，计时包括接收和生成自有 RGB 数组。
重复读取静止帧也会计时，因此吞吐不代表游戏刷新率。共享通道失败会明确报错，不回退。

Compares shared-memory and legacy TCP capture latency inside device PRoot without input injection.

Legacy capture retains upstream's byte-at-a-time header reads. Timing includes reception and owned
RGB conversion. Repeated static frames count, so throughput is not game refresh rate. Shared-channel
failure raises an error instead of silently falling back.
"""

import argparse
import json
from pathlib import Path
import socket
import sys
import time

import numpy as np


OVERLAYS = Path(__file__).resolve().parents[1] / 'rootfs' / 'overlays'
if OVERLAYS.is_dir():
    sys.path.insert(0, str(OVERLAYS))
from android_capture import MAX_BYTES, SharedFrameClient


class TcpCapture:
    """复现旧帧接收和 RGB 转换，不复用共享内存适配器。

    Reproduces legacy frame reception and RGB conversion independently of the memory adapter.
    """

    def __init__(self, host, port):
        self.sock = socket.create_connection((host, port), timeout=10)
        self.sock.settimeout(10)
        self.sequence = 0
        self.last_metadata = None

    def close(self):
        """关闭测试连接。 / Closes the benchmark connection."""
        self.sock.close()

    def capture(self):
        """返回与上游约定相同的连续 RGB 图像。 / Returns a contiguous RGB image like upstream."""
        self.sequence += 1
        request = {'id': self.sequence, 'method': 'screencap'}
        self.sock.sendall(json.dumps(request).encode() + b'\n')
        header = bytearray()
        while not header.endswith(b'\n'):
            value = self.sock.recv(1)
            if not value:
                raise EOFError('connection closed in header')
            header.extend(value)
            if len(header) > 256 * 1024:
                raise ValueError('response line too long')
        response = json.loads(header)
        if not response.get('ok'):
            raise RuntimeError(response.get('error', 'capture failed'))
        width, height, length = response['width'], response['height'], response['length']
        if response['channels'] != 3 or not 0 < length == width * height * 3 <= MAX_BYTES:
            raise ValueError('invalid frame geometry')
        chunks = []
        remaining = length
        while remaining:
            data = self.sock.recv(min(1048576, remaining))
            if not data:
                raise EOFError('connection closed in frame')
            chunks.append(data)
            remaining -= len(data)
        self.last_metadata = response
        bgr = np.frombuffer(b''.join(chunks), dtype=np.uint8).reshape(height, width, 3)
        return np.ascontiguousarray(bgr[..., ::-1])


def measure(client, rounds, warmup):
    """报告完整截图耗时和帧序号进度，始终关闭连接。

    Reports full capture latency and frame-sequence progress, always closing the connection.
    """
    try:
        for _ in range(warmup):
            client.capture()
        times = []
        sequences = []
        for _ in range(rounds):
            started = time.perf_counter()
            image = client.capture()
            times.append((time.perf_counter() - started) * 1000)
            sequences.append(client.last_metadata.get('frames'))
        return {
            'rounds': rounds, 'size': [image.shape[1], image.shape[0]],
            'mean_ms': round(float(np.mean(times)), 3),
            'p50_ms': round(float(np.percentile(times, 50)), 3),
            'p95_ms': round(float(np.percentile(times, 95)), 3),
            'p99_ms': round(float(np.percentile(times, 99)), 3),
            'max_ms': round(max(times), 3),
            'frame_sequence_changes': sum(a != b for a, b in zip(sequences, sequences[1:])),
        }
    finally:
        client.close()


def main():
    """输出可存档的 JSON 对比，不包含像素或控制口令。

    Prints an archivable JSON comparison without pixels or control tokens.
    """
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--transport', choices=('compare', 'shared_memory', 'tcp'), default='compare')
    parser.add_argument('--rounds', type=int, default=100)
    parser.add_argument('--warmup', type=int, default=10)
    parser.add_argument('--host', default='127.0.0.1')
    parser.add_argument('--port', type=int, default=22301)
    args = parser.parse_args()
    if not 1 <= args.rounds <= 10000 or not 0 <= args.warmup <= 1000:
        parser.error('rounds must be 1..10000 and warmup 0..1000')
    result = {}
    if args.transport in ('compare', 'shared_memory'):
        if args.host != '127.0.0.1' or args.port != 22301:
            parser.error('shared memory requires the on-device local bridge')
        result['shared_memory'] = measure(SharedFrameClient(), args.rounds, args.warmup)
    if args.transport in ('compare', 'tcp'):
        result['legacy_tcp'] = measure(TcpCapture(args.host, args.port), args.rounds, args.warmup)
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
