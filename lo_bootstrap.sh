#!/usr/bin/env bash
set -Eeuo pipefail

REPO_URL_DEFAULT="https://github.com/jvxis/brln-os-light"
REPO_URL="${REPO_URL:-$REPO_URL_DEFAULT}"
TARGET_DIR="${BRLN_DIR:-/opt/brln-os-light}"
BRANCH_OVERRIDE="${BRLN_BRANCH:-}"

log() {
  echo "==> $*"
}

warn() {
  echo "[WARN] $*" >&2
}

die() {
  echo "[ERROR] $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "Missing '$1'. Install it and try again."
}

resolve_branch() {
  if [[ -n "$BRANCH_OVERRIDE" ]]; then
    echo "$BRANCH_OVERRIDE"
    return
  fi
  local resolved=""
  resolved=$(git ls-remote --symref "$REPO_URL" HEAD 2>/dev/null | awk '/^ref:/ {sub("refs\\/heads\\/","",$2); print $2; exit}')
  echo "${resolved:-main}"
}

ensure_repo() {
  local branch="$1"
  if [[ -d "$TARGET_DIR/.git" ]]; then
    local origin
    origin=$(git -C "$TARGET_DIR" remote get-url origin 2>/dev/null || true)
    if [[ -n "$origin" && "$origin" != "$REPO_URL" ]]; then
      die "Existing repo at $TARGET_DIR has origin '$origin' (expected '$REPO_URL'). Refusing to overwrite."
    fi
    if [[ -n "$(git -C "$TARGET_DIR" status --porcelain)" ]]; then
      die "Local changes detected in $TARGET_DIR. Commit/stash them before running the bootstrap."
    fi
    log "Updating repo in $TARGET_DIR"
    git -C "$TARGET_DIR" fetch --prune origin
    git -C "$TARGET_DIR" checkout "$branch"
    git -C "$TARGET_DIR" pull --ff-only origin "$branch"
    return
  fi

  if [[ -e "$TARGET_DIR" && ! -d "$TARGET_DIR" ]]; then
    die "Path exists and is not a directory: $TARGET_DIR"
  fi
  if [[ -d "$TARGET_DIR" ]]; then
    if [[ -n "$(ls -A "$TARGET_DIR")" ]]; then
      die "Directory exists and is not a git repo: $TARGET_DIR"
    fi
    rmdir "$TARGET_DIR" 2>/dev/null || true
  fi

  log "Cloning $REPO_URL into $TARGET_DIR"
  git clone --branch "$branch" --single-branch "$REPO_URL" "$TARGET_DIR"
}

main() {
  require_cmd git
  local branch
  branch=$(resolve_branch)
  log "Using branch: $branch"
  ensure_repo "$branch"

  local install_dir="$TARGET_DIR/lightningos-light"
  local install_script="$install_dir/install.sh"
  if [[ ! -f "$install_script" ]]; then
    die "install.sh not found at $install_script"
  fi
  chmod +x "$install_script" 2>/dev/null || true
  log "Running installer"
  exec "$install_script" "$@"
}

main "$@"
