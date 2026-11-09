#!/usr/bin/env bash
# 校验完整历史并解析构建输入，避免浅克隆改变应用版本。
# Checks full history and resolves build inputs so shallow clones cannot change app versions.
set -euo pipefail
git config --global --add safe.directory "$PWD"
if [[ $(git rev-parse --is-shallow-repository) == true ]]; then
    git fetch --unshallow origin
fi
python3 .cnb/scripts/artifacts.py resolve
python3 .cnb/scripts/artifacts.py credentials
if [[ -s .tmp/report-credentials/client-cert.pem ]]; then
    openssl x509 -in .tmp/report-credentials/client-cert.pem -checkend 0 -noout
    openssl x509 -in .tmp/report-credentials/client-cert.pem -pubkey -noout |
        openssl pkey -pubin -outform DER > .tmp/report-credentials/cert-public.der
    openssl pkey -in .tmp/report-credentials/client-key.pem -pubout -outform DER \
        > .tmp/report-credentials/key-public.der
    cmp .tmp/report-credentials/cert-public.der .tmp/report-credentials/key-public.der
fi
