#!/bin/bash
# ALAS source-only update. Repository/Branch are read ONLY from config/deploy.yaml.
# Legacy argv / ALASAOS_UPDATE_REPO / ALASAOS_UPDATE_BRANCH never override deploy.
# Host injects static system proxy env into this process only; no pip/TLS/global changes.
# A real HEAD, origin refs and same-SHA branch checkout are retained.
# Public output is limited to phase labels and fixed verdicts; never echo raw tool output.
set -uo pipefail
umask 077

ALAS_DIR="${ALASAOS_ALAS_ROOT:-/opt/alas}"
DEPTH="${ALASAOS_UPDATE_DEPTH:-50}"
TIMEOUT="${ALASAOS_UPDATE_TIMEOUT:-240}"
STATE_FILE="$ALAS_DIR/.alasaos_alas_commit"
FAIL_FILE="$ALAS_DIR/.alasaos_update_fail_date"
SOURCE_FILE="$ALAS_DIR/.alasaos_update_source"
DEPLOY_YAML="$ALAS_DIR/config/deploy.yaml"
DEFAULT_REPO="git://git.lyoko.io/AzurLaneAutoScript"

fail() { echo "FAILED $1"; exit 1; }
# Do not expose shell-generated paths or tool diagnostics (including credentials).
exec 2>/dev/null
cd "$ALAS_DIR" || fail "workdir"
# These helpers use only stdlib; avoid loading the runtime's NumPy site hook.
config="$(python3 -S seeds/sync_deploy.py --read "$DEPLOY_YAML" 2>/dev/null)" || { echo "FAILED deploy-config"; exit 1; }
REPO="${config%%$'\n'*}"
BRANCH="${config#*$'\n'}"
unset config
export GIT_TERMINAL_PROMPT=0

# Manifest/state files cannot prove checkout; only real Git objects and HEAD do.
current="$(git --git-dir=.git rev-parse --verify 'HEAD^{commit}' 2>/dev/null || true)"
current_branch="$(git --git-dir=.git symbolic-ref --short HEAD 2>/dev/null || true)"
git check-ref-format --branch "$BRANCH" >/dev/null 2>&1 || { echo "FAILED invalid-branch"; exit 1; }

want_source="$(printf '%s\n%s' "$REPO" "$BRANCH" | sha256sum | cut -d' ' -f1)"
last_source=""
[[ -f "$SOURCE_FILE" ]] && last_source="$(cat "$SOURCE_FILE" 2>/dev/null)"
# A baked clone has a real base but no previous update attempt. Bootstrap only
# when its recorded origin and branch already identify the selected source.
# Official GitHub/master and domestic/master share that base; arbitrary forks,
# branch changes and existing source markers retain normal checks.
if [[ ! -e "$SOURCE_FILE" && -n "$current" && "$current_branch" == "$BRANCH" ]]; then
  baked_repo="$(git --git-dir=.git remote get-url origin 2>/dev/null || true)"
  official_github='https://github.com/LmeSzinc/AzurLaneAutoScript'
  if [[ "$baked_repo" == "$REPO" ]] ||
     { [[ "$BRANCH" == master ]] &&
       [[ "$baked_repo" == "$DEFAULT_REPO" || "$baked_repo" == "$official_github" || "$baked_repo" == "$official_github.git" ]] &&
       [[ "$REPO" == "$DEFAULT_REPO" || "$REPO" == "$official_github" || "$REPO" == "$official_github.git" ]]; }; then
    last_source="$want_source"
  fi
  unset baked_repo official_github
fi
source_changed=0
[[ "$want_source" != "$last_source" ]] && source_changed=1

# Old versions wrote a daily failure marker. A failed update must never suppress
# the next explicit AOS startup, including after an APK upgrade.
rm -f "$FAIL_FILE"
if [[ "$source_changed" -eq 1 ]]; then
  echo "PHASE source-changed"
  rm -f "$FAIL_FILE"
fi
# Persist attempted source fingerprint before network I/O; the URL is never persisted here.
printf '%s\n' "$want_source" > "$SOURCE_FILE" || fail "source-marker"

if [[ ! -d .git ]]; then
  git init -q . >/dev/null 2>&1 || fail "git-init"
fi
if git remote get-url origin >/dev/null 2>&1; then
  git remote set-url origin "$REPO" 2>/dev/null || fail "remote"
else
  git remote add origin "$REPO" 2>/dev/null || fail "remote"
fi
find .git -name '*.lock' -delete 2>/dev/null

# CDN pack is valid only for the unchanged official domestic/master source with a real base.
if [[ -z "${ALASAOS_UPDATE_NO_CDN:-}" && "$REPO" == "$DEFAULT_REPO" && "$BRANCH" == master && -n "$current" && "$source_changed" -eq 0 && "$current_branch" == "$BRANCH" ]]; then
  cdn_out="$(python3 -S seeds/cdn_update.py "$ALAS_DIR" "$current" 2>&1)"; cdn_rc=$?
  cdn_last="${cdn_out##*$'\n'}"
  if [[ $cdn_rc -eq 0 && "$cdn_last" == "UPTODATE" ]]; then
    git update-ref "refs/remotes/origin/$BRANCH" "$current" >/dev/null 2>&1 || fail "remote-ref"
    echo "$current" > "$STATE_FILE"
    rm -f "$FAIL_FILE"
    echo "UNCHANGED $current (cdn)"
    exit 0
  elif [[ $cdn_rc -eq 0 && "$cdn_last" =~ ^PACK_READY\ ([0-9a-f]{40}|[0-9a-f]{64})$ ]]; then
    new="${BASH_REMATCH[1]}"
    git reset --hard "origin/$BRANCH" >/dev/null 2>&1 || fail "cdn-reset"
    # Check the actual resulting checkout, not only the CDN's claimed SHA.
    new="$(git rev-parse --verify HEAD 2>/dev/null)" || fail "revision"
    echo "$new" > "$STATE_FILE"
    rm -f "$FAIL_FILE"
    echo "UPDATED $new (cdn)"
    exit 0
  fi
  unset cdn_out cdn_last
  echo "PHASE cdn-fallback"
fi

# Probe failures fall through to a bounded fetch; only terminal failures create backoff.
remote_head="$(timeout 60 git ls-remote --heads origin "refs/heads/$BRANCH" 2>/dev/null | head -1 | cut -f1)"
if [[ -n "$remote_head" && "$remote_head" == "$current" && "$source_changed" -eq 0 && "$current_branch" == "$BRANCH" ]]; then
  git update-ref "refs/remotes/origin/$BRANCH" "$remote_head" >/dev/null 2>&1 || fail "remote-ref"
  echo "$remote_head" > "$STATE_FILE"
  rm -f "$FAIL_FILE"
  echo "UNCHANGED $remote_head"
  exit 0
fi
[[ -z "$remote_head" ]] && echo "PHASE probe-fallback"
if [[ ! -f .git/FETCH_HEAD && ! -f .git/shallow ]]; then DEPTH=1; fi

echo "PHASE fetch"
fetch_options=(--progress --depth "$DEPTH")
# A shallow base from another source/branch may negotiate unusable thin deltas.
# Refetch without negotiating that old base; keep the existing repository/config.
if [[ "$source_changed" -eq 1 || "$current_branch" != "$BRANCH" ]]; then
  fetch_options+=(--refetch)
fi
timeout "$TIMEOUT" python3 -S seeds/update_progress.py -- git fetch "${fetch_options[@]}" origin "+refs/heads/$BRANCH:refs/remotes/origin/$BRANCH" >/dev/null 2>&1 || fail "fetch"
new="$(git rev-parse --verify 'FETCH_HEAD^{commit}' 2>/dev/null)" || fail "revision"
if [[ "$new" == "$current" && "$source_changed" -eq 0 && "$current_branch" == "$BRANCH" ]]; then
  echo "$new" > "$STATE_FILE"
  rm -f "$FAIL_FILE"
  echo "UNCHANGED $new"
  exit 0
fi

# Even a same-SHA source/branch change must align HEAD's branch and replay the overlay.
git checkout -f -B "$BRANCH" "$new" -- >/dev/null 2>&1 || fail "checkout"
git reset --hard "$new" >/dev/null 2>&1 || fail "reset"
echo "$new" > "$STATE_FILE"
rm -f "$FAIL_FILE"
echo "UPDATED $new"
exit 0
