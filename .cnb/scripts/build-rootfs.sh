#!/usr/bin/env bash
# 在原生架构的构建环境中用 PRoot 构建，再通过 CNB 附件传递已校验的产物。
#
# Builds with PRoot in the native build environment and transfers checked outputs via CNB.
set -euo pipefail
: "${AZURPILOT_REF:?Missing upstream commit}" "${BUILD_KEY:?Missing build key}"
[[ "$AZURPILOT_REF" =~ ^[0-9a-f]{40}$ && "$BUILD_KEY" =~ ^[A-Za-z0-9_-]+$ ]]
case "${AZURPILOT_ABI:?Missing ABI}:$(uname -m)" in
    arm64-v8a:aarch64|x86_64:x86_64) ;;
    *) echo "Native runner does not match $AZURPILOT_ABI" >&2; exit 1 ;;
esac
python3 rootfs/tests/test_compact_runtime.py
python3 rootfs/tests/test_compact_ocr_models.py
python3 rootfs/tests/test_guest_env.py
ROOTFS_EXECUTOR=proot bash rootfs/build/build-azurpilot.sh
(
    cd dist
    sha256sum rootfs.tar.xz BUILD_MANIFEST frontend-*.tar.xz frontend-*.tar.xz.sha256 > SHA256SUMS
    tar -cf "../cnb-rootfs-${BUILD_KEY}-${AZURPILOT_ABI}.tar" .
)
