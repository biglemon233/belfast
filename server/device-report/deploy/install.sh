#!/bin/sh
# 安装已交叉编译的二进制与公钥；共享客户端私钥仅由 GitHub CI 打包。
#
# Installs a cross-compiled binary and public certificate; only GitHub CI bundles the client key.
set -eu
if [ "$#" -ne 3 ]; then
    echo "Usage: install.sh BINARY CLIENT_CERT NGINX_VHOST_DIR" >&2
    exit 1
fi
binary=$1
client_cert=$2
vhost_dir=$3
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
test -d "$vhost_dir"
test -f /etc/azurpilot-device-report/origin-cert.pem
test -f /etc/azurpilot-device-report/origin-key.pem
test -f /etc/azurpilot-device-report/service.env
id azurpilot-report >/dev/null 2>&1 ||
    useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin azurpilot-report
install -d -m 0755 /opt/azurpilot-device-report /etc/azurpilot-device-report
install -d -m 0700 -o azurpilot-report -g azurpilot-report /var/lib/azurpilot-device-report
install -m 0755 "$binary" /opt/azurpilot-device-report/device-report.new
mv /opt/azurpilot-device-report/device-report.new /opt/azurpilot-device-report/device-report
install -m 0644 "$client_cert" /etc/azurpilot-device-report/client-cert.pem
install -m 0644 "$script_dir/azurpilot-device-report.service" /etc/systemd/system/azurpilot-device-report.service
chmod 0600 /etc/azurpilot-device-report/service.env /etc/azurpilot-device-report/origin-key.pem
fingerprint=$(openssl x509 -in "$client_cert" -noout -fingerprint -sha1 | cut -d= -f2 | tr -d ':' | tr 'A-F' 'a-f')
target="$vhost_dir/azurpilot-device-report.conf"
if [ -f "$target" ]; then cp "$target" "$target.previous"; fi
sed "s/CLIENT_CERT_SHA1/$fingerprint/g" "$script_dir/nginx.conf" > "$target"
if ! nginx -t; then
    if [ -f "$target.previous" ]; then mv "$target.previous" "$target"; else rm -f "$target"; fi
    exit 1
fi
systemctl daemon-reload
systemctl enable azurpilot-device-report
systemctl restart azurpilot-device-report
systemctl reload nginx
curl --retry 10 --retry-connrefused --retry-delay 1 --max-time 5 --fail http://127.0.0.1:18882/healthz
