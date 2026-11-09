# 系统架构 / System architecture

> 本文说明 AzurPilot for Android 的分层结构：App 壳层、proot Runtime、网关协议与两条更新通道。
> This document describes the layered structure of AzurPilot for Android: the app shell, the
> proot runtime, the gateway protocol, and the two update channels.

## 中文

### 分层总览

```
┌─────────────────────────────────────────────────┐
│ App 壳层（Kotlin / Compose，进程 com.azurpilot.ghio）│
│  Provisioner → ProotHost → AzurPilotGateway      │
│  OCR 工作进程（:ocr）：127.0.0.1:22302 推理 API    │
│  特权服务（Shizuku / root）：虚拟屏 + 桥接输入捕获   │
└───────────────────┬─────────────────────────────┘
                    │ proot（nativeLibraryDir 内九件套）
┌───────────────────▼─────────────────────────────┐
│ Runtime（Ubuntu 24.04 rootfs + Python 3.14）      │
│  上游 AzurPilot：WebUI + /android/* 薄接口         │
│  监听 127.0.0.1:25548                             │
└─────────────────────────────────────────────────┘
```

### App 壳层

- **RootfsProvisioner**（`app/app/src/main/kotlin/com/azurpilot/ghio/provision/RootfsProvisioner.kt`）：
  负责 Runtime 的首次部署与升级，见 [runtime-provisioning.md](runtime-provisioning.md)。
- **ProotHost**（`proot/ProotHost.kt`）：以 App 进程为父拉起 proot 会话，
  崩溃退避重拉、stdin 保活约定、清理残留锁。
- **AzurPilotGateway**（`proot/`）：对 Runtime 的 WebSocket 富接口订阅方，
  全局唯一订阅，承载配置、日志、统计的实时推送。
- **远程客户端**（`remoteaccess/`）：从欢迎页、主页及本机部署页独立进入配对，复用原生控制台；
  `remoteClientMode` 将本机部署、自动启动与权限引导限制在本机模式，见 [remote-access.md](remote-access.md)。
- **OCR 工作进程**（`ocr/`）：私有绑定服务在 `:ocr` 进程托管推理库，
  AP 和 App 测试通过带口令的 `127.0.0.1:22302` 张量 API 调用。
  原生退出后主进程重新绑定并禁用失败模型的 NPU，见 [ocr-acceleration.md](ocr-acceleration.md)。
- **特权服务**（`privileged/`、`remote/`）：经内置 ADB、外部 Shizuku 或 root 以 `app_process`
  拉起独立进程，承载虚拟屏管理、桥接截屏与注入（`bridge/` 下的 native 代码）；
  游戏运行期间由 `SdkTaskRepatriator` 把厂商/渠道 SDK 弹在主屏的登录、实名、
  支付页搬回虚拟屏（部分定制 ROM 会把这类 Activity 重定向回默认显示器）。

### proot Runtime

- rootfs 由 CI 构建（见 [multi-arch.md](multi-arch.md)），按设备 ABI 对应。
- proot 九件套以 `lib*.so` 形式打进 APK 的 `jniLibs`，安装后落在
  `nativeLibraryDir`——这是 targetSdk 35 下唯一保证可 `execve` 的位置。
- `PROOT_LOADER` 指向 `nativeLibraryDir` 中的 loader，绕开
  「App 不得 execve 私有目录文件」的限制（Spike A 实证结论）。
- 会话约定：**stdin 管道保持敞开**。App 进程退出导致 EOF 时，wrapper
  自行杀掉进程组，防孤儿链路。
- 取消 `ProotHost` 的守望协程只停止等待，必须继续抛出 `CancellationException`；
  不能把它当作进程退出并清空 `session`。停机链仍需要该句柄关闭 stdin、等待并回收进程。

### 网关协议

Runtime 在 `127.0.0.1:25548` 同时暴露三类接口：

这是默认入口。部署设置的 `WebuiPort` 修改后，App 在下次启动 Runtime 时读取
`deploy.yaml`，将网关、控制与更新接口一起切换到新端口。`WebuiHost` 必须保留本机
回环访问：`127.0.0.1` / `localhost` / `::1` 仅本机，`0.0.0.0` / `::` 允许局域网。
局域网与远程入口显示的口令都从 `Deploy.Webui.Password` 读取；`password.txt`
只可能是上游曾经生成的旧值，不用于 App 回显。部署字段确认保存后直接写入配置，
修改通常需要重启 Runtime 生效。

| 接口 | 鉴权 | 用途 |
|---|---|---|
| `/api/v1/ws`（WebSocket） | 会话令牌 | 实例/总览/配置/日志/统计的富订阅 |
| `/android/*` | `X-AzurPilot-Android-Token` | App 专用的状态、日志、配置薄接口 |
| `/healthz` | 无 | 就绪探测 |

### 两条更新通道

- **App 更新**：轻量 APK（双 ABI、无 rootfs），由 `AppUpdateManager` 走
  Release 清单的 `apkUrl` 下载安装，见 [release-channel.md](release-channel.md)。
- **Runtime 更新**：按设备 ABI 从 `runtimes` 选择 `rootfs-<abi>.tar.xz`，
  由 RootfsProvisioner 下载解压，见 [runtime-provisioning.md](runtime-provisioning.md)。

内置 ADB 的配对、离线连接及 Binder 回传细节见 [内置启动](embedded-shizuku.md)。

## English

### Layer overview

```
┌──────────────────────────────────────────────────┐
│ App shell (Kotlin/Compose, process com.azurpilot.ghio)│
│  Provisioner → ProotHost → AzurPilotGateway       │
│  OCR worker (:ocr): 127.0.0.1:22302 inference API │
│  Privileged service (Shizuku/root): virtual display│
│  + bridge capture/input                           │
└───────────────────┬──────────────────────────────┘
                    │ proot (nine-lib set in nativeLibraryDir)
┌───────────────────▼──────────────────────────────┐
│ Runtime (Ubuntu 24.04 rootfs + Python 3.14)       │
│  Upstream AzurPilot: WebUI + /android/* thin API  │
│  Listening on 127.0.0.1:25548                     │
└──────────────────────────────────────────────────┘
```

### App shell

- **RootfsProvisioner** (`app/app/src/main/kotlin/com/azurpilot/ghio/provision/RootfsProvisioner.kt`):
  owns first-time Runtime deployment and upgrades. See [runtime-provisioning.md](runtime-provisioning.md).
- **ProotHost** (`proot/ProotHost.kt`): spawns the proot session with the app
  process as parent, restarts with backoff on crash, keeps stdin alive, and
  cleans stale locks.
- **AzurPilotGateway** (`proot/`): the single WebSocket subscriber to the
  runtime's rich API; carries live config, log, and statistics pushes.
- **Remote client** (`remoteaccess/`): pairing opens from welcome, home and local provisioning,
  sharing the native console. `remoteClientMode` scopes local provisioning, automatic startup and
  permission guidance to local mode. See [remote-access.md](remote-access.md).
- **OCR worker** (`ocr/`): a private bound service hosts inference libraries in
  `:ocr`. AP and in-app tests use the authenticated `127.0.0.1:22302` tensor API.
  After a native exit, the main process rebinds and gates the failed model's NPU.
  See [ocr-acceleration.md](ocr-acceleration.md).
- **Privileged service** (`privileged/`, `remote/`): a separate process started
  through embedded ADB, external Shizuku, or root via `app_process`. It owns virtual display
  management and the bridge capture/input native code (`bridge/`). While the
  game is running, `SdkTaskRepatriator` moves vendor/channel-SDK login,
  real-name, and payment pages popped onto the primary display back onto the
  virtual display (some customized ROMs redirect such activities to the default
  display).

### proot runtime

- CI builds the rootfs (see [multi-arch.md](multi-arch.md)) per device ABI.
- The proot nine-library set ships inside the APK as `jniLibs` named
  `lib*.so`; after install they land in `nativeLibraryDir` — the only
  location guaranteed `execve`-able at targetSdk 35.
- `PROOT_LOADER` points at the loader inside `nativeLibraryDir`, which
  bypasses the "apps must not `execve` private-dir files" restriction
  (verified in Spike A).
- Session contract: **keep the stdin pipe open**. When the app process dies,
  the wrapper sees EOF and kills its own process group to avoid orphans.
- Cancelling the `ProotHost` supervisor only stops waiting and must propagate `CancellationException`.
  It must not clear `session` as if the process exited; shutdown still needs the handle to close stdin,
  wait for completion, and reclaim the process.

### Gateway protocol

The runtime exposes three interfaces on `127.0.0.1:25548`:

This is the default endpoint. When `WebuiPort` changes, the app reads `deploy.yaml` at the
next Runtime startup and moves gateway, control, and update calls to the new port together.
`WebuiHost` must retain loopback access: `127.0.0.1` / `localhost` / `::1` are local only;
`0.0.0.0` / `::` allow LAN access. Both LAN and remote password displays read
`Deploy.Webui.Password`; `password.txt` may contain an older generated value and is not used
for app display. Confirming a deployment field writes it directly to config; changes usually
require a Runtime restart.

| Interface | Auth | Purpose |
|---|---|---|
| `/api/v1/ws` (WebSocket) | session token | Rich subscriptions: instances, overview, config, logs, statistics |
| `/android/*` | `X-AzurPilot-Android-Token` | App-only thin API for status, logs, and config |
| `/healthz` | none | Readiness probe |

### Two update channels

- **App update**: a slim APK (both ABIs, no rootfs). `AppUpdateManager`
  downloads `apkUrl` from the release manifest. See
  [release-channel.md](release-channel.md).
- **Runtime update**: `rootfs-<abi>.tar.xz` selected from `runtimes` by
  device ABI, downloaded and extracted by RootfsProvisioner. See
  [runtime-provisioning.md](runtime-provisioning.md).

For embedded ADB pairing, offline reconnection, and Binder handoff, see
[embedded activation](embedded-shizuku.md).
