#!/bin/bash
# =============================================================================
# AlasAos · 环境自检修复（rootfs 内由 App 侧 ProotHost 经 proot 拉起，每次启动跑）
#
# 职责（全幂等，失败只告警不阻塞启动）：
# 1) imageio 钉回上游 requirements.txt 的 2.27.0：imageio 2.35+ 把 P 模式 GIF 统一
#    解码成 RGB 3 通道，campaign 选关模板匹配时 cv2 通道断言直接崩，因此需要按上游版本固定依赖。已部署 rootfs 靠本脚本就地降级，不必
#    重烘焙；烘焙侧 build-rootfs.sh 已同步钉版（'imageio==2.27.0'）。
# 2) 把被旧构建补丁盖过的 module/base/template.py 用 git 还原成钉版上游原版
#    （烘焙是深度 1 克隆，本地含 commit 对象，离线字节级还原；补丁机制此后不再
#    覆盖该文件）。只还原显式白名单路径——overlay 里的合法补丁（base.py /
#    connection.py / rpc.py 等）每次启动由 AlasOverlay 重铺，绝不整树 checkout。
#
# 输出：env_fix: 前缀行进 runGuest 回收输出（App 侧 Timber 落日志）；
# 并在 ./log/env_fix.txt 追加一行当次记录（wrapper /logs 按 mtime 可取到）。
# =============================================================================
set -u

ALAS_DIR="${ALASAOS_ALAS_ROOT:-/opt/alas}"
cd "$ALAS_DIR" || { echo "env_fix: WARN cd $ALAS_DIR failed"; exit 0; }

WANT="2.27.0"
MIRROR="${ALASAOS_PYPI_MIRROR:-https://mirrors.aliyun.com/pypi/simple}"

# 进度留痕到文件：proot 管道 stdout 可能被块缓冲（进程被杀时丢失），
# 设备侧排查以 log/env_fix.txt 为准（wrapper /logs 按 mtime 可取）
mkdir -p log
mark() { echo "$(date '+%F %T') $1" >> log/env_fix.txt; echo "env_fix: $1"; }

mark "start"
cur="$(python3 -c 'import imageio; print(imageio.__version__)' 2>/dev/null)"
if [ "$cur" = "$WANT" ]; then
  mark "imageio already $WANT"
  now="$cur"
else
  mark "imageio ${cur:-missing} -> $WANT, pip install..."
  # proot 下 pip 比原生慢一个量级，给 --timeout/--retries 快速失败而非死等
  python3 -m pip install -q --break-system-packages --no-cache-dir \
    --timeout 15 --retries 2 --disable-pip-version-check \
    -i "$MIRROR" "imageio==$WANT" \
    || mark "WARN pip install failed（保留现状不阻塞启动）"
  now="$(python3 -c 'import imageio; print(imageio.__version__)' 2>/dev/null)"
fi
[ "$now" = "$WANT" ] || mark "WARN imageio verify = ${now:-missing}"

# 白名单还原：仅 module/base/template.py（旧补丁从资产中删除后，设备上遗留的覆盖件）
tstate="skip(no .git)"
if [ -d .git ]; then
  if git -c safe.directory='*' diff --quiet HEAD -- module/base/template.py 2>/dev/null; then
    tstate="clean"
  else
    if git -c safe.directory='*' checkout -- module/base/template.py 2>/dev/null; then
      tstate="restored"
    else
      tstate="WARN restore-failed"
    fi
  fi
fi
mark "template.py $tstate"
# GPT-6 ALAS-AOS: retire the old whole-file WebUI overlay. It removes new
# upstream fields such as Icon.STOP after switching forks. Restore pristine
# source; the sole TaskHandler compatibility guard now lives in alasaos_gui.py.
webui_file="module/webui/utils.py"
if [ -d .git ] && git cat-file -e "HEAD:$webui_file" 2>/dev/null; then
  if ! git diff --quiet HEAD -- "$webui_file" 2>/dev/null; then
    git checkout HEAD -- "$webui_file" 2>/dev/null \
      && mark "webui/utils.py retired overlay restored" \
      || mark "WARN webui/utils.py restore failed"
  fi
fi
# Restore retired image overlays and their generated Button metadata from the
# configured ALAS source. Keep active AOS code overlays and user config intact.
for asset_file in \
  assets/cn/daily/DAILY_SKIP.png \
  assets/cn/handler/AUTO_SEARCH_TITLE.png \
  assets/cn/handler/CLEAR_MODE_TITLE.png \
  assets/cn/map/MAP_PREPARATION.png \
  assets/cn/retire/SORT_ASC.png \
  assets/cn/retire/SORT_DESC.png \
  assets/cn/storage/MATERIAL_CHECK.png \
  assets/cn/ui/SHOP_CHECK.png \
  module/daily/assets.py \
  module/map/assets.py \
  module/retire/assets.py \
  module/ui/assets.py; do
  if [ -d .git ] && git --git-dir=.git --work-tree=. cat-file -e "HEAD:$asset_file" 2>/dev/null; then
    if ! git --git-dir=.git --work-tree=. diff --quiet HEAD -- "$asset_file" 2>/dev/null; then
      git --git-dir=.git --work-tree=. checkout HEAD -- "$asset_file" 2>/dev/null \
        && mark "$asset_file retired overlay restored" \
        || mark "WARN $asset_file restore failed"
    fi
  fi
done
mark "done imageio=${now:-?} template=$tstate"
exit 0
