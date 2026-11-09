#!/usr/bin/env bash
# =============================================================================
# ALAS-AOS · build-rootfs.sh
# 烘焙 Ubuntu ARM64 rootfs：ubuntu-base 24.04 + ALAS（钉版）+ 原版 cnocr/MXNet + wrapper
#
# 运行环境：GitHub Actions `ubuntu-24.04-arm` runner（原生 aarch64，chroot 无需 qemu）。
# 本机（Windows + Git Bash）不可执行：核心动作是 chroot / mount --bind / GNU tar，
# Windows 无这些语义；本机只做 `bash -n` 语法检查与 rootfs/ 资产 curated。
#
# 环境变量（均可 export 覆盖，冒号后为默认值）：
#   ALAS_REF        master                          ALAS 分支/tag；传 40 位 commit sha 则按 commit 浅 fetch
#   ALAS_REPO       https://github.com/LmeSzinc/AzurLaneAutoScript.git
#   ROOTFS_VERSION  0.1.0                           写入 BUILD_MANIFEST.rootfs_version
#   UBUNTU_BASE     https://cdimage.ubuntu.com/ubuntu-base/releases/24.04/release/ubuntu-base-24.04.5-base-arm64.tar.gz
#   WORK_DIR        $GITHUB_WORKSPACE/work          构建工作区（runner 工作区内）
#
# 产物：
#   $GITHUB_WORKSPACE/dist/rootfs.tar.xz            rootfs 包（含 /opt/alas）
#   $GITHUB_WORKSPACE/dist/BUILD_MANIFEST           构建清单（同时装入镜像 /opt/alas/BUILD_MANIFEST，App 可读）
# =============================================================================
set -euo pipefail

# ---------- 0. 变量、提权、前置检查 ----------
ALAS_REF="${ALAS_REF:-master}"
ALAS_REPO="${ALAS_REPO:-https://github.com/LmeSzinc/AzurLaneAutoScript.git}"
# 注：GHA runner 在海外，GitHub 原生最快；gitee 同名镜像对匿名克隆要凭证（401→挂凭证提示），勿用。
# 国内本地复现构建时可 export ALAS_REPO=<可达镜像>；runtime 更新镜像由 deploy.yaml 的 fullcn 配置管，与此无关。
ROOTFS_VERSION="${ROOTFS_VERSION:-0.2.0-ocr}"
ROOTFS_ARCH="${ROOTFS_ARCH:-arm64}"
case "$ROOTFS_ARCH" in
  arm64) HOST_ARCH=aarch64; GNU_ARCH=aarch64-linux-gnu ;;
  amd64) HOST_ARCH=x86_64; GNU_ARCH=x86_64-linux-gnu ;;
  *) echo "Unsupported ROOTFS_ARCH: $ROOTFS_ARCH" >&2; exit 1 ;;
esac
export ROOTFS_ARCH
UBUNTU_BASE="${UBUNTU_BASE:-https://cdimage.ubuntu.com/ubuntu-base/releases/24.04/release/ubuntu-base-24.04.5-base-${ROOTFS_ARCH}.tar.gz}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GITHUB_WORKSPACE="${GITHUB_WORKSPACE:-$REPO_ROOT}"
WORK_DIR="${WORK_DIR:-$GITHUB_WORKSPACE/work}"
ROOTFS_DIR="$WORK_DIR/rootfs"
ASSETS="$REPO_ROOT/rootfs"          # 本仓 curated 资产（任务 A 产物）
DIST_DIR="$GITHUB_WORKSPACE/dist"
# 构建期 pip 源：默认 PyPI 官方（GHA runner 在海外，直连最快最稳）；
# 与设备运行时无关（InstallDependencies:false 已锁，rootfs 永不在设备上装包）。
# 国内本地复现构建时可 export PYPI_MIRROR=https://mirrors.aliyun.com/pypi/simple
PYPI_MIRROR="${PYPI_MIRROR:-https://pypi.org/simple}"
# ALAS 原版 OCR 依赖 mxnet 的 aarch64 轮子（PyPI 无 aarch64 mxnet whl，仓库内置一份）；
# 可 export MXNET_WHL=<路径> 覆盖。whl 内 libmxnet.so 未 strip，构建末尾会 strip 瘦身。
MXNET_WHL="${MXNET_WHL:-$REPO_ROOT/rootfs/wheels/mxnet-1.9.1-py3-none-any.whl}"

log() { echo "[build-rootfs] $*"; }

# chroot / mount 需要 root；GHA runner 有免密 sudo，自提权（-E 保留上面的环境变量），用绝对路径防 CWD 漂移
if [[ "$(id -u)" -ne 0 ]]; then
  exec sudo -E bash "$REPO_ROOT/rootfs/build/build-rootfs.sh" "$@"
fi

if [[ "$(uname -m)" != "$HOST_ARCH" ]]; then
  log "ERROR: host $(uname -m) cannot build native $ROOTFS_ARCH rootfs"
  exit 1
fi

# fail-fast：检查构建所需的运行资产，
# 缺任何一个都不许开构建——在下载与 apt 之前先验，省一次白跑
require_file() {
  if [[ ! -f "$1" ]]; then
    echo "::error::必需资产缺失: $1（请先准备该文件再触发构建）"
    exit 1
  fi
}
require_file "$ASSETS/overlays/wrapper.py"
require_file "$ASSETS/overlays/alasaos_gui.py"
require_file "$ASSETS/overlays/alasaos_control.py"
require_file "$ASSETS/overlays/alasaos_u2.py"
require_file "$ASSETS/overlays/alasaos_notify.py"
require_file "$ASSETS/overlays/runner.py"
require_file "$ASSETS/seeds/deploy.yaml"
require_file "$ASSETS/seeds/alasaos_update.sh"
require_file "$ASSETS/seeds/update_progress.py"
require_file "$ASSETS/seeds/cdn_update.py"
require_file "$ASSETS/seeds/regen_args.py"
require_file "$ASSETS/seeds/alasaos_restore.sh"
require_file "$ASSETS/shims/jellyfish.py"
require_file "$ASSETS/shims/numpy_shim.py"
require_file "$ASSETS/shims/zzz_alas_shim.pth"
require_file "$ASSETS/build/trim_rootfs.py"
require_file "$ASSETS/build/validate_trim.py"
require_file "$ASSETS/build/runtime_smoke.py"
if [[ "$ROOTFS_ARCH" == arm64 ]]; then require_file "$MXNET_WHL"; fi

# chroot 内统一环境：干净 env + 非交互 + C.UTF-8（免 perl locale 警告）
chroot_run() {
  chroot "$ROOTFS_DIR" /usr/bin/env -i \
    HOME=/root LANG=C.UTF-8 LC_ALL=C.UTF-8 DEBIAN_FRONTEND=noninteractive \
    GIT_TERMINAL_PROMPT=0 \
    PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    "$@"
}

# ---------- 1. 下载并解包 ubuntu-base ----------
mkdir -p "$WORK_DIR" "$DIST_DIR"
BASE_TAR="$WORK_DIR/ubuntu-base.tar.gz"
if [[ ! -f "$BASE_TAR" ]]; then
  log "下载 ubuntu-base: $UBUNTU_BASE"
  curl -fL --retry 3 -o "$BASE_TAR" "$UBUNTU_BASE"
fi
rm -rf -- "${ROOTFS_DIR:?}/"
mkdir -p "$ROOTFS_DIR"
tar -xzf "$BASE_TAR" -C "$ROOTFS_DIR"

# ---------- 2. 挂载准备（trap 兜底卸载；打包前还会显式卸载并校验） ----------
# chroot 老规矩：/dev /proc /sys bind 进去；/dev/pts 单独 bind（bind 不携带子挂载点，
# 而 apt 部分 postinst 需要 pts）。挂载失败 → set -e 中止 → EXIT trap 卸掉已挂部分。
MOUNTED=()
mount_bind() {
  mount --bind "$1" "$2"
  MOUNTED+=("$2")
}
cleanup_mounts() {
  local i
  for (( i=${#MOUNTED[@]}-1; i>=0; i-- )); do
    if ! umount -lf "${MOUNTED[i]}" 2>/dev/null; then
      echo "::warning::umount 失败: ${MOUNTED[i]}（runner 为一次性环境，影响有限，但需记录）"
    fi
  done
  MOUNTED=()
}
trap cleanup_mounts EXIT

# DNS：不能 bind 宿主 /etc/resolv.conf——GHA runner 是 systemd-resolved stub
# （127.0.0.53），chroot 内没有 resolved 监听，解析必挂。写静态 resolv.conf
# （AliDNS + Cloudflare）bind 进去；ubuntu-base 自带的是指向 /run/systemd 的悬空软链，先删
rm -f "$ROOTFS_DIR/etc/resolv.conf"
touch "$ROOTFS_DIR/etc/resolv.conf"
printf 'nameserver 223.5.5.5\nnameserver 1.1.1.1\n' > "$WORK_DIR/resolv.conf"
mount_bind "$WORK_DIR/resolv.conf" "$ROOTFS_DIR/etc/resolv.conf"
# ubuntu-base 的 /dev 下可能没有 pts/ 子目录（mount --bind 要求挂载点已存在），先补齐
mkdir -p "$ROOTFS_DIR/dev/pts" "$ROOTFS_DIR/proc" "$ROOTFS_DIR/sys"
mount_bind /dev "$ROOTFS_DIR/dev"
mount_bind /dev/pts "$ROOTFS_DIR/dev/pts"
mount_bind /proc "$ROOTFS_DIR/proc"
mount_bind /sys "$ROOTFS_DIR/sys"

# ---------- 3. chroot 内 apt：最小系统依赖 ----------
# opencv-headless 运行只需 glib/gomp 级系统库；不装 Qt/X11
# 原版 OCR：mxnet 的 libmxnet.so 链 libopenblas.so.0 + OpenCV4.6(so.406)，用 apt 补运行时库；
# binutils 供构建末尾 strip libmxnet.so 瘦身
chroot_run apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=3 update
OCR_APT=()
if [[ "$ROOTFS_ARCH" == arm64 ]]; then
  OCR_APT=(libopenblas0-pthread libopencv-core406t64 libopencv-imgproc406t64 libopencv-imgcodecs406t64)
else
  # The manylinux wheel bundles BLAS/Fortran, but its Fortran library still
  # loads the system GCC quad-precision runtime.
  OCR_APT=(libquadmath0)
fi
chroot_run apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=3 install -y --no-install-recommends \
  python3 python3-pip python3-venv git ca-certificates \
  libglib2.0-0t64 libgomp1 curl xz-utils binutils \
  "${OCR_APT[@]}"
chroot_run /bin/bash -c 'apt-get clean; rm -rf /var/lib/apt/lists/* /var/cache/apt/archives/* /var/cache/apt/*.bin'

# deploy.yaml 里 PythonExecutable: python；ubuntu-base 只有 python3，补软链对齐
chroot_run ln -sf /usr/bin/python3 /usr/local/bin/python

# ---------- 4. chroot 内建 /opt/alas：浅克隆并钉版 ----------
if [[ "$ALAS_REF" =~ ^[0-9a-fA-F]{40}$ ]]; then
  # 钉 commit：浅 fetch 指定 sha。注意 gitee 若未开 allow-any-sha1-in-want 会拒绝——
  # 那时请改用 branch/tag；此处失败即构建失败，钉版语义不允许静默回退
  chroot_run git init /opt/alas
  chroot_run git -C /opt/alas remote add origin "$ALAS_REPO"
  chroot_run git -C /opt/alas fetch --depth 1 origin "$ALAS_REF"
  chroot_run git -C /opt/alas checkout --detach FETCH_HEAD
else
  chroot_run git clone --depth 1 --branch "$ALAS_REF" "$ALAS_REPO" /opt/alas
fi
PINNED_COMMIT="$(chroot_run git -C /opt/alas rev-parse HEAD)"
log "ALAS 钉版: $PINNED_COMMIT (ref: $ALAS_REF)"

# ---------- 5. chroot 内 pip（系统级安装，Ubuntu 24.04 PEP 668 需 --break-system-packages） ----------
pip_install() {
  chroot_run python3 -m pip install --break-system-packages --no-cache-dir -i "$PYPI_MIRROR" "$@"
}

# 依赖层：现代 py3.12 集，但为 ALAS 原版 OCR 链（cnocr 1.2.2 + mxnet 1.9.1）钉住 native 三件套——
# numpy 必须 <2 且用 mxnet1.9.1/cnocr 实测可用的 1.26.4（2.x 与 mxnet 冲突，删掉的别名由 numpy_shim 补回）；
# scipy 1.13.1 / opencv-python-headless 4.10.0.84 与之匹配。
# 不装：onnxruntime（PP-OCR 已退役）、jellyfish（shim 顶替）、zerorpc/pyzmq/gevent（rpc.py 惰性
# import，UseOcrServer:false 不触发）、av/lz4（死链且用不上）。imageio 钉 2.27.0（P 模式 GIF 解码
# 回 2D，campaign 模板匹配不崩）；pydantic <2 对齐 ALAS v1；cached-property 显式装；uvicorn 不带 [standard]。
pip_install \
  'numpy==1.26.4' 'scipy==1.13.1' pillow lxml 'opencv-python-headless==4.10.0.84' \
  pywebio uvicorn fastapi aiofiles inflection pyyaml requests tqdm rich 'imageio==2.27.0' \
  'pydantic<2' adbutils uiautomator2 uiautomator2cache websockets pypresence onepush \
  cached-property 'graphviz<0.9'

# ALAS 原版 OCR 引擎：mxnet aarch64 轮子（仓库内置，需先拷进 chroot 才能 pip 装）+ cnocr 1.2.2。
# cnocr 用 --no-deps：其声明依赖会拉 mxnet1.6/gluoncv/matplotlib/pandas 一大坨老死链，
# 而 AlOcr 推理路径只用 mxnet+numpy，gluoncv 等一概不需要。
if [[ "$ROOTFS_ARCH" == amd64 ]]; then
  # The official Linux wheel carries its own native dependencies. Windows DLLs
  # from a desktop toolkit cannot execute in a glibc rootfs.
  MXNET_WHL="$WORK_DIR/mxnet-1.9.1-py3-none-manylinux2014_x86_64.whl"
  python3 - "$MXNET_WHL" <<'PY'
import hashlib, json, sys, urllib.request
from pathlib import Path
metadata = json.load(urllib.request.urlopen('https://pypi.org/pypi/mxnet/1.9.1/json'))
target = Path(sys.argv[1])
wheel = next(item for item in metadata['urls'] if item['filename'] == target.name)
data = urllib.request.urlopen(wheel['url'], timeout=120).read()
assert hashlib.sha256(data).hexdigest() == '65d5dac162c87a14d138d888b54494d515036d9047ae804ff51f770bd02197a6'
target.write_bytes(data)
PY
fi
WHL_BASE="$(basename "$MXNET_WHL")"   # 必须保留合法 wheel 文件名，否则 pip 报 "not a valid wheel filename"
install -D -m 0644 "$MXNET_WHL" "$ROOTFS_DIR/tmp/$WHL_BASE"
pip_install --no-deps "/tmp/$WHL_BASE"
pip_install --no-deps cnocr==1.2.2
rm -f "$ROOTFS_DIR/tmp/$WHL_BASE"

# 该 whl 把 libmxnet.so 装进 data scheme（如 /usr/local/mxnet/），mxnet 包目录里没有它，
# `import mxnet` 会报 "Cannot find the MXNet library"（find_lib_path 先查包目录）。
# 把真实 .so 软链进包目录，避免重复占用约 102MB；strip 仍作用于真实文件。
REAL_SO="$(find "$ROOTFS_DIR" -xdev -type f -name libmxnet.so | head -1)"
MX_PKG_DIR="$(find "$ROOTFS_DIR" -xdev -type d -path '*/dist-packages/mxnet' | head -1)"
if [[ -n "$REAL_SO" && -n "$MX_PKG_DIR" && ! -e "$MX_PKG_DIR/libmxnet.so" ]]; then
  ln -sf "${REAL_SO#"$ROOTFS_DIR"}" "$MX_PKG_DIR/libmxnet.so"
  log "symlink libmxnet.so → $MX_PKG_DIR/libmxnet.so -> ${REAL_SO#"$ROOTFS_DIR"}"
else
  log "libmxnet.so 包目录链接跳过：REAL_SO=$REAL_SO MX_PKG_DIR=$MX_PKG_DIR"
fi

# ---------- 6. 应用本仓资产（宿主侧拷入 $ROOTFS_DIR/opt/alas） ----------
# AOS runtime code overlays; image templates and Button metadata stay upstream.
cp -rf "$ASSETS/patches/module/." "$ROOTFS_DIR/opt/alas/module/"

# OCR：保留 ALAS 上游自带的 module/ocr/rpc.py（zerorpc 客户端；zerorpc 为惰性 import，
# UseOcrServer:false 时不触发）。原版 OCR 走 models.OCR_MODEL=cnocr+mxnet，不覆盖 rpc.py。

# jellyfish shim：现代 jellyfish（1.x）是 Rust/maturin 构建，目标环境装不了，未入依赖清单；
# 把纯 Python shim 放到 site-packages 顶替模块名（ALAS 只调 levenshtein_distance）。
# 模块路径在 chroot 内用 sysconfig 查实，不猜前缀
PY_PURELIB="$(chroot_run python3 -c 'import sysconfig; print(sysconfig.get_path("purelib"))')"
install -D -m 0644 "$ASSETS/shims/jellyfish.py" "$ROOTFS_DIR$PY_PURELIB/jellyfish.py"

# numpy 兼容垫片：补回 numpy>=1.24 删掉的别名（np.long/PZERO/... mxnet1.9.1/cnocr1.2.2 要用）。
# zzz_alas_shim.pth 让 site 启动时自动 `import numpy_shim`（zzz 前缀确保在其它 .pth 之后跑）。
install -D -m 0644 "$ASSETS/shims/numpy_shim.py" "$ROOTFS_DIR$PY_PURELIB/numpy_shim.py"
install -D -m 0644 "$ASSETS/shims/zzz_alas_shim.pth" "$ROOTFS_DIR$PY_PURELIB/zzz_alas_shim.pth"

# deploy.yaml：AutoUpdate:false 是保住钉版 commit 的唯一闸门（详见文件头注释）；
# EnableReload:true 放开 WebUI「Force restart」。运行期策略键由
# seeds/seed_deploy.py 幂等校正（改策略键不必重烘焙 rootfs），Repository/Branch 跟随
# 用户换源由宿主 AlasSourceRepository 原子维护，sync_deploy.py 只读。
install -D -m 0644 "$ASSETS/seeds/deploy.yaml" "$ROOTFS_DIR/opt/alas/config/deploy.yaml"

# 实例配置生成器：运行时实例播种由宿主调用（ALAS CWD=仓库根；脚本内 ALAS 根取
# ALASAOS_ALAS_ROOT 环境变量，调用方需 export ALASAOS_ALAS_ROOT=/opt/alas）
install -D -m 0644 "$ASSETS/seeds/seed_config.py" "$ROOTFS_DIR/opt/alas/seeds/seed_config.py"

# deploy.yaml 校正器：seed_deploy 每次启动幂等校正静态策略键(EnableReload 等)；
# sync_deploy 由 alasaos_update.sh 调用，只读 Repository/Branch，不回写用户配置。
# 两者亦随 overlay 资产每次启动刷新（改它们不必重烘焙 rootfs），这里 install 保 rootfs 自洽。
install -D -m 0644 "$ASSETS/seeds/seed_deploy.py" "$ROOTFS_DIR/opt/alas/seeds/seed_deploy.py"
install -D -m 0644 "$ASSETS/seeds/sync_deploy.py" "$ROOTFS_DIR/opt/alas/seeds/sync_deploy.py"

# ALAS 热更新脚本：设备端唯一更新通道（内置更新器已被 AutoUpdate:false 锁死），
# App 侧 AlasUpdater 经 proot 拉起；协议见脚本头注释
install -D -m 0755 "$ASSETS/seeds/alasaos_update.sh" "$ROOTFS_DIR/opt/alas/seeds/alasaos_update.sh"
install -D -m 0755 "$ASSETS/seeds/update_progress.py" "$ROOTFS_DIR/opt/alas/seeds/update_progress.py"
install -D -m 0755 "$ASSETS/seeds/cdn_update.py" "$ROOTFS_DIR/opt/alas/seeds/cdn_update.py"

# args 现场再生器：args.json/argument.yaml 不补丁化，每次启动重跑 ALAS 生成链
# 并补回 alasaos 桥选项（活动列表永不冻结）；App 侧 ProotHost 经 proot 拉起
install -D -m 0755 "$ASSETS/seeds/regen_args.py" "$ROOTFS_DIR/opt/alas/seeds/regen_args.py"

# 环境自检修复：每次启动幂等跑（App 侧 ProotHost 经 proot 拉起）——把已部署 rootfs 的
# imageio 钉回上游 2.27.0，并 git 还原被旧构建补丁盖过的上游跟踪文件；协议见脚本头注释
install -D -m 0755 "$ASSETS/seeds/env_fix.sh" "$ROOTFS_DIR/opt/alas/seeds/env_fix.sh"

# 工作区离线还原：rootfs 只带 .git（瘦身），git 跟踪的工作区文件由本脚本在打包前
# 剥离、真机首启经 proot 拉起本 seed 从 pack 物化（协议见脚本头注释）
install -D -m 0755 "$ASSETS/seeds/alasaos_restore.sh" "$ROOTFS_DIR/opt/alas/seeds/alasaos_restore.sh"

# 原版 OCR 模型随上游 ALAS 自带（bin/cnocr_models/{azur_lane,azur_lane_jp,cnocr,jp,tw}，
# 共约 31MB），无需另装；PP-OCR 的 det/rec/keys.onnx 已随自研 OCR 层退役，不再装入。

# strip libmxnet.so 瘦身：whl 内是未 strip 的 aarch64 .so（~102MB）；--strip-unneeded 去掉调试/
# 符号表但保留动态导出符号（mxnet 靠动态符号加载，安全）。site-packages 内是符号链接，取真实文件。
MXNET_SO="$(find "$ROOTFS_DIR" -xdev -type f -name libmxnet.so | head -1)"
if [[ "$ROOTFS_ARCH" == arm64 ]]; then
if [[ -n "$MXNET_SO" ]]; then
  before=$(stat -c %s "$MXNET_SO")
  chroot_run strip --strip-unneeded "${MXNET_SO#"$ROOTFS_DIR"}"
  after=$(stat -c %s "$MXNET_SO")
  log "strip libmxnet.so: $before -> $after bytes"
else
  echo "::warning::libmxnet.so 未找到，跳过 strip"
fi

# 瘦身诊断（只记录、不改动）：查 mxnet 到底链哪些 opencv/blas 库（决定能否砍 imgcodecs→gdal→mesa/LLVM），
# 以及镜像里最占地的 apt 包 top20。据此在后续迭代精准裁剪。
if [[ -n "$MXNET_SO" ]]; then
  log "libmxnet.so DT_NEEDED (opencv/blas/gfortran/lapack):"
  chroot_run bash -c "readelf -d '${MXNET_SO#"$ROOTFS_DIR"}' 2>/dev/null | grep NEEDED | grep -iE 'opencv|blas|gfortran|gomp|lapack' | sed 's/^/  /' || echo '  (none matched)'"
fi
log "最大的 20 个已装 apt 包（Installed-Size KB）:"
chroot_run bash -c "dpkg-query -Wf '\${Installed-Size}\t\${Package}\n' 2>/dev/null | sort -rn | head -20 | sed 's/^/  /'"

# 依赖链诊断（定瘦身手术方案）：谁硬依赖 gdal/mesa/llvm/openblas；模拟移除会连带删什么；imgcodecs 直链什么。
log "谁硬依赖 gdal/mesa/llvm/openblas（rdepends --installed）:"
chroot_run bash -c "for p in libgdal34t64 mesa-libgallium libllvm20 libopenblas0-pthread; do echo \"  [\$p] <- \$(apt-cache rdepends --installed --no-recommends \$p 2>/dev/null | tail -n +3 | tr -d ' ' | tr '\n' ',')\"; done"
log "模拟移除 gdal/mesa/llvm（--simulate 不真删），看是否连带 opencv:"
chroot_run bash -c "apt-get remove --purge --auto-remove --simulate libgdal34t64 mesa-libgallium libllvm20 2>&1 | grep -iE 'Remv|opencv|mxnet|imgcodecs' | sed 's/^/  /' | head -40 || echo '  (simulate 无输出)'"
IMGC="$(find "$ROOTFS_DIR" -xdev -name 'libopencv_imgcodecs.so.406' | head -1)"
if [[ -n "$IMGC" ]]; then
  log "libopencv_imgcodecs.so.406 DT_NEEDED（全部，勿删这些）:"
  chroot_run bash -c "readelf -d '${IMGC#"$ROOTFS_DIR"}' 2>/dev/null | grep NEEDED | sed 's/^/  /'"
fi

# ---------- 瘦身手术：删 mxnet 用不到的 LLVM + mesa GL 软栈（~182MB） ----------
# imgcodecs 硬链并**真用** gdal 符号（GDALRasterBand::RasterIO），
# 空壳 gdal 会 "undefined symbol" 载入失败；gdcm/OpenEXR 也是 imgcodecs 直接 DT_NEEDED。
# → 放弃空壳，保留真 gdal + proj + gdcm + openexr 等全部编解码依赖；只删 **LLVM(136MB)+mesa(46MB)**——
# 它们是 GL 软件渲染栈，只被 gdal 的 GL 驱动惰性 dlopen，mxnet 数值 OCR 链永不触发，且 gdal.so 不直链它们。
# 仅删文件不 apt-remove（免连锁删 opencv）。import 硬门禁验证 mxnet 仍载入；挂则回滚本步。
GLIBDIR="$ROOTFS_DIR/usr/lib/$GNU_ARCH"
GDALSO="$(find "$GLIBDIR" -name 'libgdal.so.34*' -type f | head -1)"
[[ -n "$GDALSO" ]] && { log "libgdal.so.34 DT_NEEDED 里的 mesa/llvm（应为空才安全）:"; chroot_run bash -c "readelf -d '${GDALSO#"$ROOTFS_DIR"}' 2>/dev/null | grep NEEDED | grep -iE 'mesa|LLVM|gallium' | sed 's/^/  /' || echo '  (无，删除安全)'"; }
rm -f "$GLIBDIR"/libLLVM*.so* "$GLIBDIR"/libgallium*.so* "$GLIBDIR"/libgbm.so* \
      "$GLIBDIR"/libglapi.so* "$GLIBDIR"/libGLX_mesa.so* "$GLIBDIR"/libEGL_mesa.so* 2>/dev/null
rm -rf "$GLIBDIR/dri"
chroot_run ldconfig 2>/dev/null || true
log "瘦身手术：删 LLVM+mesa GL 软栈（约 -182MB 解压）；gdal/proj/gdcm/openexr 编解码依赖全保留"
fi

# ---------- 7. wrapper / runner（所需文件已在开头检查） ----------
cp "$ASSETS/overlays/wrapper.py" "$ASSETS/overlays/runner.py" "$ASSETS/overlays/alasaos_gui.py" "$ASSETS/overlays/alasaos_control.py" "$ASSETS/overlays/alasaos_u2.py" "$ASSETS/overlays/alasaos_notify.py" "$ROOTFS_DIR/opt/alas/"

# ---------- 8. import 硬门禁 + BUILD_MANIFEST（决策 #10：App 要可读） ----------
PY_VER="$(chroot_run python3 -c 'import platform; print(platform.python_version())')"
CV_VER="$(chroot_run python3 -c 'import cv2; print(cv2.__version__)')"
MX_VER="$(chroot_run python3 -c 'import mxnet; print(mxnet.__version__)')"
CN_VER="$(chroot_run python3 -c 'import cnocr; print(cnocr.__version__)')"

# import 硬门禁（fail-fast）：现代依赖 + 原版 OCR 链。import mxnet 会 dlopen libmxnet.so，
# 顺带验 openblas/opencv apt 库齐全 + strip 未破坏；numpy_shim 由 .pth 自启，先验别名已补回。
# 任一失败 → ::error:: 退出码 1 中止构建（set -e 捕获）。azur_lane 实推理留真机首启复验。
chroot_run python3 - <<'PY'
try:
    import numpy
    assert hasattr(numpy, 'long'), 'numpy_shim 未生效（缺 numpy.long）'
    import cv2, scipy, PIL, lxml.etree, yaml
    import pywebio, uvicorn, fastapi, pydantic, imageio, rich, requests, jellyfish
    import adbutils, uiautomator2, cached_property
    import mxnet, cnocr
except (ImportError, AssertionError) as e:
    print(f'::error::import 硬门禁失败: {e}')
    raise SystemExit(1)
print('cv2', cv2.__version__, '| numpy', numpy.__version__, '| scipy', scipy.__version__)
print('mxnet', mxnet.__version__, '| cnocr', cnocr.__version__, '| pydantic', pydantic.VERSION)
print('jellyfish shim check:', jellyfish.levenshtein_distance('abc', 'abd') == 1)
print('ALL_IMPORTS_OK')
PY

# 优先用 GITHUB_SHA（checkout 的那个 commit）；本地兜底走 git——脚本已 sudo 提权为 root，
# 直接 git 会撞 "dubious ownership"（仓属 runner 用户），故带 -c safe.directory
REPO_COMMIT="${GITHUB_SHA:-$(git -c safe.directory='*' -C "$REPO_ROOT" rev-parse HEAD)}"
BUILD_TIME_UTC="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
AZ_SHA="$(sha256sum "$ROOTFS_DIR/opt/alas/bin/cnocr_models/azur_lane/cnocr-v1.2.0-densenet-lite-gru-0015.params" | awk '{print $1}')"

ROOTFS_VERSION="$ROOTFS_VERSION" BUILD_TIME_UTC="$BUILD_TIME_UTC" \
ALAS_REPO="$ALAS_REPO" PINNED_COMMIT="$PINNED_COMMIT" REPO_COMMIT="$REPO_COMMIT" \
AZ_SHA="$AZ_SHA" MX_VER="$MX_VER" CN_VER="$CN_VER" \
PY_VER="$PY_VER" CV_VER="$CV_VER" \
python3 - <<'PY' > "$ROOTFS_DIR/opt/alas/BUILD_MANIFEST"
import json, os
e = os.environ
manifest = {
    "rootfs_version": e["ROOTFS_VERSION"],
    "architecture": e["ROOTFS_ARCH"],
    "build_time_utc": e["BUILD_TIME_UTC"],
    "alas_repo": e["ALAS_REPO"],
    "alas_commit": e["PINNED_COMMIT"],
    "patches_source": f"rootfs/patches @ repo commit {e['REPO_COMMIT']}",
    "ocr": {
        "engine": "cnocr+mxnet (ALAS original, UseOcrServer=false)",
        "mxnet_version": e["MX_VER"],
        "cnocr_version": e["CN_VER"],
        "azur_lane_params_sha256": e["AZ_SHA"],
    },
    "python_version": e["PY_VER"],
    "opencv_version": e["CV_VER"],
}
print(json.dumps(manifest, indent=2, ensure_ascii=False))
PY
cp "$ROOTFS_DIR/opt/alas/BUILD_MANIFEST" "$DIST_DIR/BUILD_MANIFEST"

# ---------- 9. 瘦身 + 打包 ----------
# 先显式卸载全部 bind mount（trap 只是兜底）：否则下面 find/rm 会爬进宿主 /proc /sys /dev，
# 打包也会把宿主文件系统打进 tar。卸载后再做一切 rootfs 内部清理
cleanup_mounts
for mp in dev dev/pts proc sys etc/resolv.conf; do
  if mountpoint -q "$ROOTFS_DIR/$mp"; then
    echo "::error::$ROOTFS_DIR/$mp 仍处于挂载状态，拒绝清理与打包（trap 已兜底，此处为显式防线）"
    exit 1
  fi
done

# 保留浅克隆的 /opt/alas/.git（含钉版 commit pack），仅移除 hooks、logs 与锁。
# pack 是 ALAS 树在镜像里的唯一载体：体积≈整树 xz 压缩（67MB↔70MB 基本等价），
# 真机首启由 seeds/alasaos_restore.sh 离线物化整树（官方一键包同款方案），
# 后续热更新以 pack 为基线做增量拉取。
rm -rf "$ROOTFS_DIR/opt/alas/.git/hooks" "$ROOTFS_DIR/opt/alas/.git/logs"
find "$ROOTFS_DIR/opt/alas/.git" -name '*.lock' -delete 2>/dev/null
# 与 native-slim 重打包共用剥离入口：补齐恢复 seed、pending 标记后才移除跟踪文件。
# index、pack 与 config/deploy.yaml、BUILD_MANIFEST、wrapper 等 AOS 自有资产保留。
if [[ "$ROOTFS_ARCH" == arm64 ]]; then
python3 - "$ASSETS/build" "$ROOTFS_DIR" <<'PY'
import sys
from pathlib import Path
sys.path.insert(0, sys.argv[1])
from validate_trim import strip_alas_tree
strip_alas_tree(Path(sys.argv[2]))
PY
log "ALAS 跟踪工作区已剥离，镜像只带 .git pack + AOS untracked 资产"
fi
find "$ROOTFS_DIR" -xdev -type d -name __pycache__ -prune -exec rm -rf {} +
rm -rf "$ROOTFS_DIR/root/.cache" "$ROOTFS_DIR/var/lib/apt/lists"/*

OUT="$DIST_DIR/rootfs.tar.xz"
# 原始tar只作本次构建基线；验证器在独立副本逐组移除文档/构建工具/
# NumPy SciPy测试/非ARM64缓存，每组核原版OCR与运行依赖，且ALAS整树必须不变。
# 最后直接从原始tar过滤生成XZ；不修改当前构建rootfs，不把tar→XZ差值当裁剪收益。
RAW_TAR="$WORK_DIR/rootfs-before-trim.tar"
tar --one-file-system -C "$ROOTFS_DIR" -cf "$RAW_TAR" .
RAW_SHA="$(sha256sum "$RAW_TAR" | awk '{print $1}')"
python3 "$ASSETS/build/validate_trim.py" --input "$RAW_TAR" --expected-sha256 "$RAW_SHA" \
  --work "$WORK_DIR/trim-validation" --dist "$DIST_DIR" ${ROOTFS_PLAIN_TAR:+--plain-tar} \
  $([[ "$ROOTFS_ARCH" == amd64 ]] && echo --no-trim)
if [[ -n "${ROOTFS_PLAIN_TAR:-}" ]]; then
  log "Validated intermediate: $DIST_DIR/rootfs.tar (final XZ follows native validation)"
  exit 0
fi
SIZE="$(stat -c %s "$OUT")"
SHA="$(sha256sum "$OUT" | awk '{print $1}')"
log "rootfs.tar.xz: $SIZE bytes"
log "rootfs.tar.xz sha256: $SHA"
# 目标 ~250MB；超 400MB 报警（不 fail，留人审）
if [[ "$ROOTFS_ARCH" == arm64 ]] && (( SIZE > 400*1024*1024 )); then
  echo "::warning::rootfs.tar.xz 超 400MB（$SIZE bytes，目标 ~250MB），需要瘦身"
fi
log "完成：$OUT"
