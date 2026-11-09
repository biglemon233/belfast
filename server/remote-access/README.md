# AP 远程注册服务 / AP remote registry

## 中文

Go + SQLite 服务为设备 ID 保存当前 Quick Tunnel URL，提供固定入口与单次配对。
不保存 WebUI 主密码，不代理 AP 业务流量。

```powershell
go test ./...
$env:GOOS='linux'
$env:GOARCH='amd64'
$env:CGO_ENABLED='0'
go build -trimpath -ldflags '-s -w' -o ../../.tmp/remote-access-linux-amd64 .
```

默认本地运行：`go run .`，绑定 `127.0.0.1:18882`。
生产部署必须使用 TLS。`TLS_CERT` / `TLS_KEY` 可指定本服务自己的证书；
`ACME_HOST` 启用自动申请与续期，证书缓存只写 `ACME_CACHE`。
不要使用或替换现有业务的证书文件。

### 独立部署

示例服务入口：`https://remote.azurpilot.com`。
域名通过 Cloudflare 连接本服务器；客户端使用标准 HTTPS 443，不再携带 8443 端口。
根路径 `/` 提供介绍页，`/error` 提供离线说明；页面和样式随 Go 二进制内嵌部署。

新增文件：

| 路径 | 用途 |
| --- | --- |
| `/opt/azurpilot-remote/remote-access` | 静态 Linux 服务二进制 |
| `/opt/azurpilot-remote/service.env` | 本服务环境配置，参考 `deploy/service.env.example` |
| `/etc/systemd/system/azurpilot-remote.service` | 独立进程单元，参考 `deploy/azurpilot-remote.service` |
| `/etc/nginx/conf.d/azurpilot-remote-acme.conf` | 只匹配新域名的 HTTP-01 验证入口 |
| `/etc/nginx/conf.d/azurpilot-remote-domain.conf` | 只匹配 `remote.azurpilot.com` 的独立虚拟主机 |
| `/var/lib/azurpilot-remote/` | systemd 创建的私有 SQLite 与证书目录 |

首次安装前确认这些路径未被其他业务占用。只新增上述文件，不改写已有 Nginx 站点、全局设置、
证书、业务目录或已有 systemd 单元。保存所有原 Nginx 文件的 SHA-256，执行 `nginx -t` 后
仅 reload 加载新增虚拟主机；部署后逐文件比较原有摘要。`daemon-reload` 后启动新增单元。

新域名虚拟主机将请求转给本机已有的 Go HTTPS 8443 监听；上游开启 SNI、正常证书校验及
三级证书链校验深度，避免因默认深度不足产生 502。旧试验域名自己的 ACME 入口保持原样。
`deploy/azurpilot-remote-domain.conf` 引用本服务已有的独立域名证书；不自动申请证书或改写其他站点。
本次没有新增证书续期定时器。用正常 TLS 验证请求 `/api/healthz` 确认成功。
服务运行于 DynamicUser，数据库、ACME 账户私钥和 SSH 凭据均不得放入仓库。

### 协议

| 接口 | 行为 |
| --- | --- |
| `GET /` | 远程访问介绍页 |
| `GET /error` | 设备离线、失效或链接错误时的说明页 |
| `POST /api/v1/devices/register` | Bearer 设备密钥；提交 `device_id` / `name` / `tunnel_url`，首次占有并后续更新 |
| `DELETE /api/v1/devices/{id}` | Bearer 设备密钥；标记离线，不删除身份 |
| `GET /d/{id}/{path...}` | 180 秒租约内返回禁止缓存的 301，并保留路径与查询；设备不存在、离线、租约过期或隧道失效时 302 到 `/error` |
| `POST /api/v1/pairings` | Bearer 设备密钥；提交 `device_id` / `access_token`，返回六位 `code`、`id`、128 位 `particle_secret`、`expires_at` |
| `POST /api/v1/pairings/claim` | 提交六位 `code` 或 32 位十六进制 `scan_secret`；五分钟内共享一次领取状态 |
| `GET /api/v1/pairings/{id}` | Bearer 设备密钥；返回 pending / claimed / expired |
| `GET /api/healthz` | 健康检查 |

设备密钥只存 SHA-256；六码和扫描 secret 也只存摘要。配对的独立访问凭据在领取前暂存于私有
SQLite，领取后清空。只能跳转到根路径的 HTTPS `*.trycloudflare.com`，拒绝凭据、端口和任意域名。
每 IP 每分钟最多 5 次领取，服务整体每分钟最多 120 次；只信任回环反代写入的 `X-Real-IP`，
公网直连客户端不能通过转发头伪造来源。

固定入口先检查 180 秒心跳租约，再以四秒超时读取当前 Tunnel 的 `/healthz`，要求 AP 返回
`status=ok`、`protocolVersion=1`。探测不携带凭据、不跟随跳转。在线入口保留路径和查询并返回
禁止缓存的 301；不存在、主动离线、心跳过期或探测失败的设备以 302 跳到 `/error`。

测试包括设备身份防冒用、换址 301、失效租约、扫描 secret、并发单次领取和限流。数据库重启后保留
映射和密钥，AP 每次启动仍重新注册最新地址。Quick Tunnel 为试验功能，没有生产可用性承诺。

## English

This Go/SQLite service maps installation IDs to current Quick Tunnel URLs and supplies stable
redirects plus one-time pairing. It stores no primary WebUI password and proxies no AP traffic.
Run `go test ./...`; cross-build Linux/amd64 with CGO disabled. Local `go run .` binds to
`127.0.0.1:18882`. Production requires TLS, through service-owned `TLS_CERT` / `TLS_KEY` or
automatic certificates configured by `ACME_HOST` and `ACME_CACHE`.

The public URL is `https://remote.azurpilot.com` on standard HTTPS port 443. `/` is the introduction
page and `/error` explains offline devices. HTML and CSS are embedded in the Go binary. Add only the binary and
environment under `/opt/azurpilot-remote/`, the new `azurpilot-remote.service` unit, and the new
service-owned virtual hosts from `deploy/`, including `azurpilot-remote-domain.conf`. DynamicUser owns the private state under
`/var/lib/azurpilot-remote/`. Preserve all existing Nginx files, sites, certificates, systemd units
and business directories. Compare their SHA-256 values before and after deployment. Test Nginx
configuration, reload only to load the new host, and start the independent service.

The new hostname proxies to the existing loopback-accessible Go HTTPS listener on 8443, using SNI,
certificate verification and a verification depth of three. The old trial host and its ACME route remain
unchanged. The new virtual-host example references this service's existing independent certificate;
it does not request certificates or edit other sites. No renewal timer was added in this deployment.
Verify `/api/healthz` with normal TLS validation. Keep private state and credentials out of source control.

`POST /api/v1/devices/register` takes `device_id`, `name`, `tunnel_url` with a Bearer device secret.
The same secret owns subsequent updates; `DELETE /api/v1/devices/{id}` marks it offline without
deleting ownership. `GET /d/{id}/{path...}` returns a non-cacheable 301 preserving path/query
while the 180-second lease is valid and the tunnel health probe succeeds, otherwise a 302 to `/error`.
The four-second probe reads `/healthz` and requires AP's `status=ok` and `protocolVersion=1`, without
credentials or following redirects. Targets must be root HTTPS subdomains of
trycloudflare.com, with no credentials, custom ports or arbitrary hosts.

Authenticated `POST /api/v1/pairings` accepts `device_id` and an independent `access_token`, returning
an ID, six-digit code, 128-bit `particle_secret` and five-minute expiry. `POST /api/v1/pairings/claim` accepts
either a six-digit code or a 32-hex-digit `scan_secret`; both share one successful claim. Authenticated
`GET /api/v1/pairings/{id}` returns pending, claimed or expired. Device keys, codes and scan secrets
are hashed; an unclaimed access token is temporarily retained in private SQLite and cleared on claim.
Claims are limited to 5 per IP and 120 globally per minute. Only loopback proxies may supply `X-Real-IP`;
public clients cannot spoof their rate-limit source with forwarding headers.

Tests cover ownership, updated redirects, leases, scan secrets, concurrent claims and rate limits.
Database restarts preserve ownership and mappings; AP still re-registers its current URL at each start.
Quick Tunnel is experimental and has no production availability guarantee.
