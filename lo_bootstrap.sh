#!/usr/bin/env bash
set -Eeuo pipefail

REPO_URL_DEFAULT="https://github.com/jvxis/brln-os-light"
REPO_URL="${REPO_URL:-$REPO_URL_DEFAULT}"
TARGET_DIR="${BRLN_DIR:-/opt/brln-os-light}"
REF_OVERRIDE="${BRLN_REF:-${BRLN_BRANCH:-}}"
RELEASE_API_URL="${BRLN_RELEASE_API_URL:-https://api.github.com/repos/jvxis/brln-os-light/releases?per_page=10}"
RELEASE_TAG_API_BASE="https://api.github.com/repos/jvxis/brln-os-light/releases/tags"
INSTALLER="${BRLN_INSTALLER:-install.sh}"
GIT_USER="${BRLN_GIT_USER:-}"

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

repo_git() {
  if [[ "$(id -u)" -eq 0 && -n "$GIT_USER" ]]; then
    local git_uid=""
    local target_uid=""
    git_uid="$(id -u "$GIT_USER" 2>/dev/null || true)"
    target_uid="$(stat -c '%u' "$TARGET_DIR" 2>/dev/null || true)"
    if [[ -n "$git_uid" && "$target_uid" == "$git_uid" ]]; then
      sudo -H -u "$GIT_USER" git -C "$TARGET_DIR" "$@"
      return
    fi
  fi
  git -c safe.directory="$TARGET_DIR" -C "$TARGET_DIR" "$@"
}

resolve_latest_release() {
  require_cmd curl

  local response=""
  local tag=""
  response=$(curl -fsSL \
    -H "Accept: application/vnd.github+json" \
    -H "User-Agent: lightningos-bootstrap" \
    "$RELEASE_API_URL") || die "Failed to query published LightningOS releases."
  while IFS= read -r line; do
    if [[ "$line" =~ \"tag_name\"[[:space:]]*:[[:space:]]*\"([^\"]+)\" ]]; then
      tag="${BASH_REMATCH[1]}"
      break
    fi
  done <<< "$response"

  [[ -n "$tag" ]] || die "No published LightningOS release was found."
  echo "$tag"
}

resolve_ref() {
  if [[ -n "$REF_OVERRIDE" && "$REF_OVERRIDE" != "latest" ]]; then
    echo "$REF_OVERRIDE"
    return
  fi

  resolve_latest_release
}

verify_immutable_release() {
  local tag="$1"
  local response=""
  response=$(curl --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 15 --max-time 45 --max-filesize 1048576 -fsSL \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2026-03-10" \
    -H "User-Agent: lightningos-bootstrap" \
    "${RELEASE_TAG_API_BASE}/${tag}") \
    || die "Failed to retrieve the LightningOS release attestation."
  printf '%s' "$response" | grep -Eq '"immutable"[[:space:]]*:[[:space:]]*true([,}])' \
    || die "LightningOS release is not immutable and attested."
  printf '%s' "$response" | grep -Eq '"draft"[[:space:]]*:[[:space:]]*false([,}])' \
    || die "LightningOS release is not published."
  printf '%s' "$response" | grep -Fq "\"tag_name\":\"${tag}\"" \
    || die "LightningOS release attestation tag mismatch."
}

ensure_repo() {
  local ref="$1"
  if [[ -d "$TARGET_DIR/.git" ]]; then
    local origin
    origin=$(repo_git remote get-url origin 2>/dev/null || true)
    if [[ -n "$origin" && "$origin" != "$REPO_URL" ]]; then
      die "Existing repo at $TARGET_DIR has origin '$origin' (expected '$REPO_URL'). Refusing to overwrite."
    fi
    if [[ -n "$(repo_git status --porcelain)" ]]; then
      die "Local changes detected in $TARGET_DIR. Commit/stash them before running the bootstrap."
    fi
    log "Updating repo in $TARGET_DIR"
    repo_git fetch --tags --prune origin
    if repo_git show-ref --verify --quiet "refs/tags/$ref"; then
      repo_git checkout --detach "refs/tags/$ref"
    elif repo_git show-ref --verify --quiet "refs/remotes/origin/$ref"; then
      repo_git checkout -B "$ref" "origin/$ref"
    else
      die "LightningOS ref not found after fetch: $ref"
    fi
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
  git clone --branch "$ref" --single-branch "$REPO_URL" "$TARGET_DIR"
}

main() {
  require_cmd git
  if [[ -n "${BRLN_REF:-}" && -n "${BRLN_BRANCH:-}" ]]; then
    die "Set only one of BRLN_REF or the legacy BRLN_BRANCH override."
  fi
  case "$INSTALLER" in
    install.sh|install_existing.sh|install_existing_pi.sh) ;;
    *) die "Invalid BRLN_INSTALLER: $INSTALLER" ;;
  esac

  local ref
  ref=$(resolve_ref)
  log "Using LightningOS release/ref: $ref"
  if [[ -z "$REF_OVERRIDE" || "$REF_OVERRIDE" == "latest" ]]; then
    verify_immutable_release "$ref"
    log "Immutable LightningOS release attestation verified"
  fi
  ensure_repo "$ref"

  local install_dir="$TARGET_DIR/lightningos-light"
  local install_script="$install_dir/$INSTALLER"
  if [[ ! -f "$install_script" ]]; then
    die "$INSTALLER not found at $install_script"
  fi
  chmod +x "$install_script" 2>/dev/null || true
  log "Running installer"
  export LIGHTNINGOS_RELEASE_BOOTSTRAPPED=1
  exec "$install_script" "$@"
}

main "$@"
