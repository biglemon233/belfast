# Android 后台校时 / Android background NTP

## 中文

此问题在 Redmi K60 的 Android PRoot 环境中遇到：主流程读取时间时同步访问 NTP，
DNS 解析及多个地址和服务器的重试可能导致截图、操作与调度长时间停顿。
UDP socket 的超时不能约束此前的 DNS 解析。这里不声称只有 Android 会受影响。

独立的 `android_ntp.py` overlay 沿用手机已使用的后台校时方案，复用原核心的
服务器列表、环境变量、NTP 协议、超时、校时周期和退避间隔，不关闭 NTP：

- 首次读取立即使用本机时间；成功后使用校时缓存和单调时钟推进。
- 每个时间源实例同时最多一个 daemon 校时线程，网络查询不持有缓存锁。
- 失败保留上次成功缓存；未成功过则用本机时间，按原间隔退避。
- `refresh(force=True)` 只请求刷新，返回已有缓存是否成功，不等待本轮网络结果。
- `timestamp()`、`now()`、`utcnow()`、`status()` 仍使用原模块和同一个全局时间源。
- fork 后重建锁、清除父进程的线程占用标记，避免 worker 继承不存在的校时线程。
- 已提供 `_refresh_in_background` 的新版核心不再覆盖，避免重复修复。

`sitecustomize` 仅在 `AZURPILOT_ANDROID=1` 安装延迟加载钩子。新 Runtime 将 overlay
装入 venv；APK 打包同一文件，ProotHost 启动前同步到既有 PYTHONPATH 目录。
因此轻量 APK 更新可覆盖已有 Runtime，核心热更不会删除宿主 overlay。
构建质量门真实导入时间源并检查适配标记。旧安装需要更新 APK 或 Runtime 才能得到它。
非 Android 的普通 Python 启动不启用适配。

本 PR 不包含 NTP 服务器或设备配置，不修改大世界逻辑，也不依赖事件 PR #29。
首次同步前仍依赖手机本机时间，故手机时间应正确；这不是阻塞等待的替代品。
DNS 卡住时后台线程可能仍然等待，但不会阻塞读取或无上限创建新线程。

测试使用固定核心真实时间源，复用原后台校时的 6 项单元测试，新增 7 项 Android
接线、进程状态、线程启动失败和公共接口测试，共 13 项。测试用受控事件模拟慢 DNS，
不访问真实 NTP。历史手机 Python 文件的部署不等于新 overlay 的 APK/真机验证；
本轮未重新部署手机，也未证明当前网络能连上任一 NTP 服务器。

```bash
AZURPILOT_SOURCE=/path/to/AzurPilot python3 rootfs/tests/test_android_ntp.py -v
```

## English

This issue was encountered on Redmi K60 under Android PRoot: reading time synchronously queried NTP,
and DNS resolution plus retries across addresses and servers could stall screenshots and scheduling.
The UDP socket timeout does not bound preceding DNS resolution. The issue is not claimed to be
exclusive to Android.

The standalone overlay ports the phone's background-sync approach while retaining upstream servers,
environment variables, protocol, socket timeout, refresh period, and retry backoff. It does not
disable NTP. First reads return device time immediately; successful reads advance cached calibration
with the monotonic clock. Each source has at most one daemon worker. Network queries run outside
the cache lock. Failures preserve successful calibration or use device time before initial success.
Forced refresh requests return cached sync status, not the outcome of the pending network request.
Existing public functions continue using the same singleton. Child processes reset inherited locks
and in-flight state. Already-asynchronous upstream implementations are left unchanged.

Android-gated sitecustomize installs a lazy hook. The overlay is installed in the venv, packaged in
APK assets, and synchronized to the existing PYTHONPATH directory before runtime startup. Slim APK
updates can therefore deliver it to installed runtimes; core hot updates do not delete host overlays.
A rootfs build gate imports the actual core and requires its adapter marker. Existing installations
need the updated APK or Runtime; non-Android Python startup does not activate the adapter.

No server/device configuration or Operation Siren changes are included, and event PR #29 is not a
dependency. Device time must be correct before initial calibration. Hung DNS may retain one worker,
but cannot block readers or spawn unbounded workers.

Tests use the actual pinned core: six reused background-time regressions plus seven new Android
wiring, process-state, thread-start failure, and public-API tests. Controlled events simulate DNS;
no real NTP server is contacted. Historical phone deployment is distinct from APK/overlay validation.
This work does not deploy to the phone or establish current connectivity to any NTP server.
