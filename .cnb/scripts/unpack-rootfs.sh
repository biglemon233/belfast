#!/usr/bin/env bash
# 按构建编号解包，防止并发或重试时拿到同提交的旧运行时。
# Unpacks by build key so concurrent builds or retries cannot consume stale runtimes.
set -euo pipefail
for abi in arm64-v8a x86_64; do
    target=".tmp/azurpilot-artifact/$abi"
    mkdir -p "$target"
    tar -xf "cnb-rootfs-${BUILD_KEY:?Missing build key}-$abi.tar" -C "$target"
    (cd "$target" && sha256sum -c SHA256SUMS && sha256sum -c frontend-*.tar.xz.sha256)
done
python3 .cnb/scripts/artifacts.py verify-rootfs
