#!/usr/bin/env bash
# 临时签名和上报文件不能留给下一轮构建。
# Temporary signing and reporting files must not survive into the next build.
set -euo pipefail
rm -f .tmp/signing/azurpilot.jks .tmp/report-credentials/client-cert.pem \
    .tmp/report-credentials/client-key.pem .tmp/report-credentials/cert-public.der \
    .tmp/report-credentials/key-public.der \
    app/app/build/generated/deviceReportCredentials/device-report/client-cert.pem \
    app/app/build/generated/deviceReportCredentials/device-report/client-key.pem
