#!/bin/bash
# =============================================================================
# AlasAos · ALAS 工作区离线还原（rootfs 内由 App 侧 ProotHost 经 proot 拉起，按需执行）
#
# 背景：rootfs 镜像只带浅克隆 .git（瘦身）——git 跟踪的工作区文件在打包前已按
# ls-files 精确剥离，pack（~70MB）里仍含全部内容；untracked 的 AOS 自有资产
# （seeds/、config/deploy.yaml、BUILD_MANIFEST、wrapper*.py 等）不受影响。
# 首次启动（或上次还原中断）时由本脚本 `git reset --hard` 从 pack 离线物化整树，
# 无需网络；剥离时同时写入 .alasaos_restore_pending，明确要求完成 Git 还原。
#
# 幂等：成功后落 marker（.alasaos_restored，untracked，reset --hard 不会清掉），
# App 侧见 marker 直接跳过本步；旧版完整工作区的 rootfs 升级上来时同样补 marker。
# 失败：非零退出，ProotHost 按 fatal 处理——树不全 ALAS 必然起不来，早失败早暴露。
# 输出由 runGuest 回收。
# =============================================================================
set -u

ALAS_DIR="${ALASAOS_ALAS_ROOT:-/opt/alas}"
MARK=".alasaos_restored"
PENDING=".alasaos_restore_pending"
SENTINEL="alas.py"   # 仓库根主入口，上游 tracked；物化完成的判据

cd "$ALAS_DIR" || { echo "restore: FATAL ALAS directory missing"; exit 1; }
# Explicit paths prevent Git from discovering a parent repository in legacy trees.
git_repo() { git -c safe.directory='*' --git-dir=.git --work-tree=. "$@"; }

if [ -f "$MARK" ] && [ ! -f "$PENDING" ]; then
  echo "restore: already materialized, skip"
  exit 0
fi

mark_complete() {
  touch "$MARK" && rm -f "$PENDING" || { echo "restore: FATAL cannot save completion"; exit 1; }
}

if ! git_repo rev-parse --verify 'HEAD^{commit}' >/dev/null 2>&1; then
  # git init creates objects/pack even when a failed first fetch leaves no HEAD.
  # Legacy images still have their entry files; explicitly stripped images must
  # never use this fallback, including after an interrupted restore.
  if [ ! -f "$PENDING" ] && [ -f "$SENTINEL" ] && [ -f gui.py ] && [ -f module/config/config.py ]; then
    mark_complete
    echo "restore: legacy full tree (no valid HEAD), marked"
    exit 0
  fi
  echo "restore: FATAL no valid HEAD for required restore"
  exit 1
fi

echo "restore: materializing ALAS tree from pack (one-time)..."
git_repo reset --hard HEAD || { echo "restore: FATAL git reset failed"; exit 1; }
if [ ! -f "$SENTINEL" ]; then
  echo "restore: FATAL sentinel missing after reset"
  exit 1
fi
mark_complete
echo "restore: OK ($(git_repo ls-files 2>/dev/null | wc -l) tracked files)"
exit 0
