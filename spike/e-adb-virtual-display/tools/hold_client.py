import socket
import sys
import time

host = "127.0.0.1"
port = int(sys.argv[1]) if len(sys.argv) > 1 else 27183
dump_path = sys.argv[2] if len(sys.argv) > 2 else None
cap = int(sys.argv[3]) if len(sys.argv) > 3 else 2_000_000

s = socket.create_connection((host, port), timeout=20)
s.settimeout(30)
f = open(dump_path, "wb") if dump_path else None
total = 0
t0 = time.time()
last_report = 0.0
print(f"[hold] connected to {host}:{port}", flush=True)
while True:
    try:
        data = s.recv(65536)
    except socket.timeout:
        if time.time() - last_report > 30:
            print(f"[hold] idle {time.time()-t0:.0f}s, total={total}", flush=True)
            last_report = time.time()
        continue
    if not data:
        print(f"[hold] server closed after {total} bytes / {time.time()-t0:.1f}s", flush=True)
        break
    total += len(data)
    if f and f.tell() < cap:
        f.write(data[: max(0, cap - f.tell())])
    if time.time() - last_report > 15:
        print(f"[hold] {total} bytes, {time.time()-t0:.1f}s", flush=True)
        last_report = time.time()
if f:
    f.flush()
    f.close()
