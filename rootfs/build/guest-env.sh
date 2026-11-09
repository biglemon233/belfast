#!/usr/bin/env bash
# 为 rootfs 构建提供一致的 guest 环境：chroot 使用内核挂载，PRoot 使用用户态绑定。
# 调用方须设置 ROOTFS_DIR、WORK_DIR 和 ROOTFS_EXECUTOR，并在退出时调用 cleanup_guest。
#
# Provides the same guest environment through chroot mounts or PRoot user-space bindings.
# Callers set ROOTFS_DIR, WORK_DIR, and ROOTFS_EXECUTOR and call cleanup_guest on exit.

MOUNTS=()
GUEST_BINDINGS=()
GUEST_RUNNER=()

bind_guest() {
    local host=$1 guest_path=$2 target="$ROOTFS_DIR$2"
    if [[ -d $host ]]; then
        mkdir -p "$target"
    else
        mkdir -p "$(dirname "$target")"
        touch "$target"
    fi
    if [[ $ROOTFS_EXECUTOR == proot ]]; then
        GUEST_BINDINGS+=(-b "$host:$guest_path")
    else
        mount --bind "$host" "$target"
        MOUNTS+=("$target")
    fi
}

cleanup_guest() {
    local i
    for ((i=${#MOUNTS[@]}-1;i>=0;i--)); do umount -lf "${MOUNTS[i]}" || true; done
    MOUNTS=()
    GUEST_BINDINGS=()
    GUEST_RUNNER=()
}

setup_guest() {
    case "$ROOTFS_EXECUTOR" in
        chroot) command -v chroot >/dev/null || return 1 ;;
        proot) command -v proot >/dev/null || {
            echo 'PRoot executor requires proot in PATH' >&2; return 1;
        } ;;
        *) echo "Unsupported rootfs executor: $ROOTFS_EXECUTOR" >&2; return 1 ;;
    esac
    # DNS 和下载缓存只在 guest 执行期间可见，不能进入发布的 rootfs。
    rm -f "$ROOTFS_DIR/etc/resolv.conf"
    if [[ $ROOTFS_EXECUTOR == proot ]]; then
        cp -L /etc/resolv.conf "$WORK_DIR/resolv.conf"
    else
        printf 'nameserver 1.1.1.1\nnameserver 8.8.8.8\n' > "$WORK_DIR/resolv.conf"
    fi
    bind_guest "$WORK_DIR/resolv.conf" /etc/resolv.conf
    local d
    for d in dev dev/pts proc sys; do bind_guest "/$d" "/$d"; done
    mkdir -p "$WORK_DIR/uv-cache"
    bind_guest "$WORK_DIR/uv-cache" /opt/uv-cache
    mkdir -p "$WORK_DIR/npm-cache"
    bind_guest "$WORK_DIR/npm-cache" /opt/npm-cache
    if [[ $ROOTFS_EXECUTOR == proot ]]; then
        # 只绑定构建依赖的路径；-R/-S 还会暴露宿主 HOME、tmp 等目录。
        GUEST_RUNNER=(proot --kill-on-exit -0 -r "$ROOTFS_DIR" -w / "${GUEST_BINDINGS[@]}")
        # 旧容器策略的 EPERM 会压过 seccomp 跟踪事件；改用 ptrace 让兼容补丁能处理返回值。
        if ! python3 -c 'import os; raise SystemExit(not os.access("/", os.R_OK, effective_ids=True))'; then
            GUEST_RUNNER=(env PROOT_NO_SECCOMP=1 "${GUEST_RUNNER[@]}")
            echo 'PRoot: using ptrace for container permission-check compatibility'
        fi
    else
        GUEST_RUNNER=(chroot "$ROOTFS_DIR")
    fi
}

guest() {
    # env -i 防止签名材料及 runner 变量泄入 guest，路径策略与两种执行器共用。
    "${GUEST_RUNNER[@]}" /usr/bin/env -i HOME=/root LANG=C.UTF-8 LC_ALL=C.UTF-8 \
        DEBIAN_FRONTEND=noninteractive GIT_TERMINAL_PROMPT=0 \
        UV_PYTHON_INSTALL_DIR=/opt/uv-python UV_CACHE_DIR=/opt/uv-cache \
        UV_PYTHON_PREFERENCE=only-managed UV_NO_PROGRESS=1 \
        PATH=/usr/local/bin:/usr/bin:/bin "$@"
}
