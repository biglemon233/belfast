# 共享内存截图 / Shared-memory capture

## 中文

### 数据路径

默认本机 `azurpilot_android` 截图使用匿名 `SharedMemory`，不将像素写到文件或送入 TCP。

1. App 创建抽象 Unix 监听端点 `com.azurpilot.ghio.frames.v1`。
2. App 接受本 UID 的 Runtime 连接，设置 60 秒读写超时，通过 Binder transaction 78
   `openSharedFrameChannel` 交付已连接 FD。每个连接仅交付一次。
3. 特权端创建独占内存区域，先保留可写映射，再将 FD 限制为只读，经 `SCM_RIGHTS`
   交给 Runtime。Python 持久 mmap，无路径绑定，不受 PRoot 文件路径转换影响。
4. 每次截图在 native 中将最新 BGR 帧 memcpy 到此映射，随后回传尺寸和同一帧的序号。
5. Python 在下一次请求前复制为自有、连续 RGB ndarray，旧截图不会被后续帧改写。

此实现仍有 native 快照复制和 RGB 转换复制，不能称为全链路零复制。
它消除了整帧 Java 数组分配、内核 TCP 像素缓冲及 Python 接收分块拼接。
截图通道不取得输入锁，其它连接的点击和长滑动不会阻塞取帧。

### Android 权限与生命周期

不能让 Runtime 直接连接 shell 创建的 socket。这台 Android 11 设备同时拒绝了两个方向的
跨域连接；将 App 的监听 FD 交给 shell 再调用 listen 也被拒绝。
因此 App 完成 connect/accept 和 socket 选项设置，特权端只使用已连接 FD 的读写能力。
旧系统用 `LocalSocket.createConnectedLocalSocket`，新系统用 FD 构造器包装借用句柄；
这部分只在特权域调用。借用包装器不拥有 FD，真正句柄由 `ParcelFileDescriptor` 关闭。

每个特权连接至多保留 32 MiB，最多 8 个连接。EOF、60 秒空闲超时或服务停止回收资源。
需要更大的分辨率时返回 `reopen=true`，客户端重连分配；元数据总是对应真实复制的槽位。
Runtime 重启、服务死亡及 fork 后都会重新建立连接，不复用父进程请求流。

APK 会在 Runtime 启动前同步 `android_capture.py`，rootfs 构建也将它装入 site-packages。
旧 APK 或通道故障重连一次后回退到 TCP，30 秒后再试共享内存。
非默认本机代理地址保留旧行为；设置 `AZURPILOT_ANDROID_CAPTURE=tcp` 可强制 TCP。
`device.android_capture_transport` 可确认本次实际使用的通道。

### 验证与测量

本地回归：

```powershell
uv run --with numpy python -m unittest discover -s rootfs/tests -p test_android_capture.py -v
cd app
.\gradlew.bat compileDebugKotlin -x verifyBundledAzurPilotRuntime
.\gradlew.bat assembleDebug '-Pazurpilot.slimApk=true' -x verifyBundledAzurPilotRuntime
```

新版 APK 启动并连接特权服务后，在设备 PRoot 内运行：

```bash
.venv/bin/python tools/benchmark_capture_transport.py --rounds 100
```

工具需复制进 Runtime 或使用仓库工作目录，报告平均值、P50、P95、P99、最大延迟和帧序号
变化次数。共享内存失败会报错，不会静默回退。TCP 基线保留旧客户端逐字节读响应头的方式；
结果包含这一优化与传输通道优化的共同收益。连续读取同一帧不能当作游戏刷新率。

2026-10-13 在 M2104K10AC、Android 11、设备 PRoot/Python 3.14 的隔离进程中验证：

| 通道 | 平均延迟 | P50 | P95 | 最大延迟 |
|---|---:|---:|---:|---:|
| 共享内存 | 15.617 ms | 14.921 ms | 25.065 ms | 33.516 ms |
| 旧 TCP | 47.196 ms | 41.861 ms | 81.664 ms | 105.294 ms |

两路均预热 15 次，再各取 100 张 1280×720 图像，包含 RGB 转换。
原生 AImageReader 接收每 50 ms 改色的合成 Surface，验证整帧颜色一致、旧快照不变、
只读映射拒绝 PROT_WRITE，以及重复重连后客户端 FD 数不增长。
测试用隔离的 FD 转交器模拟 App 接受连接，将 App 域的已连接 socket 交给 shell，调用
生产 `SharedFrameServer` 和 `SharedFrameClient`；没有替换已安装 APK，也没有验证正式
App 的 Binder 初始化或实际游戏任务。这些测量不证明游戏渲染、识别或省电策略没有其它瓶颈。

## English

### Data path

Default local `azurpilot_android` capture uses anonymous `SharedMemory` without pixel files or TCP.

1. The app creates the abstract Unix listener `com.azurpilot.ghio.frames.v1`.
2. It accepts same-UID Runtime connections, sets 60-second read/write timeouts, and hands each
   connected FD over once through Binder transaction 78, `openSharedFrameChannel`.
3. The privileged service creates a connection-owned region, retains a writable mapping, and
   restricts the shared FD to read-only before sending it with `SCM_RIGHTS`. Python keeps a
   persistent mmap without path binds or PRoot path translation.
4. Native capture copies the newest BGR frame into this mapping, then returns dimensions and
   the sequence of that same frame.
5. Python copies into an owned contiguous RGB ndarray before requesting again, preserving old images.

Native snapshot copying and RGB conversion still occur; this is not end-to-end zero-copy.
It removes full-frame Java allocation, TCP pixel buffering, and Python receive-chunk assembly.
Capture does not take the input lock, so another connection's click or long swipe cannot block it.

### Android permissions and lifecycle

Runtime cannot directly connect to a shell-created socket. This Android 11 device rejected both
directions of cross-domain connect; passing an app-created listener to shell and calling listen
also failed. The app therefore handles connect/accept and socket options, while the privileged
service uses only connected-FD reads/writes. Older systems use
`LocalSocket.createConnectedLocalSocket`; newer systems use the FD constructor. These hidden
wrappers run only in the privileged domain and borrow the FD, whose owner is `ParcelFileDescriptor`.

Limits are 32 MiB per privileged connection and 8 connections. EOF, 60-second idle timeout, or service
shutdown reclaim resources. Growth beyond capacity returns `reopen=true`; metadata always describes
the copied slot. Runtime restart, service death, and fork create new connections without sharing
the parent's request stream.

The APK syncs `android_capture.py` before Runtime startup, and rootfs builds install it into
site-packages. Old APKs or channel failures reconnect once, fall back to TCP, and retry after 30 seconds.
Nondefault proxy addresses retain legacy behavior; `AZURPILOT_ANDROID_CAPTURE=tcp` forces TCP.
`device.android_capture_transport` reports the actual transport.

### Verification and measurement

Use the local regression and Gradle commands shown above. After launching the new APK and connecting
the privileged service, run `.venv/bin/python tools/benchmark_capture_transport.py --rounds 100`
inside device PRoot, copying the tool into Runtime or using a repository checkout. Reports include
mean, P50, P95, P99, maximum latency, and frame-sequence changes. Shared capture fails explicitly
instead of silently falling back. TCP retains legacy byte-at-a-time header reads, so comparison
includes both header and pixel-transport improvements. Reading one frame repeatedly is not game FPS.

On 2026-10-13, an isolated M2104K10AC/Android 11 device PRoot with Python 3.14 measured:

| Transport | Mean | P50 | P95 | Maximum |
|---|---:|---:|---:|---:|
| Shared memory | 15.617 ms | 14.921 ms | 25.065 ms | 33.516 ms |
| Legacy TCP | 47.196 ms | 41.861 ms | 81.664 ms | 105.294 ms |

Each path warmed up for 15 captures before 100 measured 1280×720 images, including RGB conversion.
Native AImageReader consumed a synthetic Surface changing color every 50 ms. Checks covered uniform
pixels, immutable old snapshots, rejection of PROT_WRITE mappings, and stable client FD counts across
reconnects. An isolated FD broker simulated app-side accept and handed connected app-domain sockets
to shell, using production `SharedFrameServer` and `SharedFrameClient`. The installed APK was not
replaced; production app Binder initialization and real game tasks were not verified. Measurements
do not rule out additional rendering, recognition, or power-management bottlenecks.
