#!/usr/bin/env bash
set -Eeuo pipefail

REPO_URL_DEFAULT="https://github.com/jvxis/brln-os-light"
REPO_URL="${REPO_URL:-$REPO_URL_DEFAULT}"
TARGET_DIR="${BRLN_DIR:-/opt/brln-os-light}"
REF_OVERRIDE="${BRLN_REF:-${BRLN_BRANCH:-}}"
RELEASE_API_URL="https://api.github.com/repos/jvxis/brln-os-light-updates/releases/latest"
RELEASE_TAG_API_BASE="https://api.github.com/repos/jvxis/brln-os-light-updates/releases/tags"
RELEASE_REPO_URL="https://github.com/jvxis/brln-os-light-updates.git"
EXPECTED_COMMIT=""
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

configure_runtime_sparse_checkout() {
  # GitHub metadata is required by repository automation, but it is not
  # application source and should not be materialized on managed nodes.
  repo_git sparse-checkout init --no-cone \
    || die "Failed to initialize the LightningOS runtime sparse checkout."
  repo_git sparse-checkout set --no-cone '/*' '!/.github/' \
    || die "Failed to exclude repository-only GitHub metadata from the node checkout."
}

release_request() {
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 15 --max-time 45 --max-filesize 1048576 -fsSL \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2026-03-10" \
    -H "User-Agent: lightningos-bootstrap" \
    "$1"
}

# Parse the actual JSON object, not nested fields or whitespace-dependent grep.
# Python 3 is a bootstrap prerequisite on the supported Ubuntu hosts.
release_tag_from_json() {
  python3 -c '
import json, re, sys
try:
    release = json.load(sys.stdin)
    if not isinstance(release, dict):
        raise ValueError("expected a release object")
    if release.get("immutable") is not True or release.get("draft") is not False or release.get("prerelease") is not False:
        raise ValueError("release must be immutable, published and not a prerelease")
    tag = release.get("tag_name", "")
    if not isinstance(tag, str) or not re.fullmatch(r"[vV]?[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9][A-Za-z0-9.-]*)?", tag):
        raise ValueError("invalid release tag")
    print(tag)
except (ValueError, TypeError) as error:
    print("[ERROR] Invalid LightningOS release metadata: " + str(error), file=sys.stderr)
    sys.exit(1)
'
}

resolve_latest_release() {
  local response=""
  response=$(release_request "$RELEASE_API_URL") \
    || die "Failed to query published LightningOS releases; no legacy catalog fallback."
  printf '%s' "$response" | release_tag_from_json
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
  local verified_tag=""
  local remote_refs=""
  response=$(release_request "${RELEASE_TAG_API_BASE}/${tag}") \
    || die "Failed to retrieve the LightningOS release attestation."
  verified_tag=$(printf '%s' "$response" | release_tag_from_json) \
    || die "LightningOS release is not immutable and attested."
  [[ "$verified_tag" == "$tag" ]] \
    || die "LightningOS release attestation tag mismatch."
  # The release target_commitish can be 'main'. Resolve the immutable tag itself,
  # including annotated tags, and compare it to the source checkout before exec.
  remote_refs=$(git ls-remote --exit-code "$RELEASE_REPO_URL" "refs/tags/$tag" "refs/tags/$tag^{}") \
    || die "Failed to resolve immutable release source tag."
  EXPECTED_COMMIT=$(printf '%s\n' "$remote_refs" | python3 -c '
import re, sys
tag = "refs/tags/" + sys.argv[1]
refs = dict((ref, sha) for sha, ref in (line.split() for line in sys.stdin if line.strip()))
commit = refs.get(tag + "^{}", refs.get(tag, ""))
if not re.fullmatch(r"[0-9a-f]{40}", commit):
    sys.exit("Invalid immutable release commit")
print(commit)
' "$tag") || die "Invalid immutable release source tag."
}

verify_release_checkout() {
  local tag="$1"
  local actual_commit=""
  local tag_commit=""
  local actual_version=""
  actual_commit=$(repo_git rev-parse HEAD) || die "Cannot resolve checked-out commit."
  tag_commit=$(repo_git rev-parse --verify "refs/tags/$tag^{commit}") \
    || die "Release tag is missing from the source checkout; installer was not run."
  [[ "$tag_commit" == "$EXPECTED_COMMIT" ]] \
    || die "Source tag does not match the immutable release commit; installer was not run."
  [[ "$actual_commit" == "$EXPECTED_COMMIT" ]] \
    || die "Source tag does not match the immutable release commit; installer was not run."
  actual_version=$(tr -d '\r\n' < "$TARGET_DIR/lightningos-light/ui/public/version.txt") \
    || die "Cannot read source version."
  actual_version="${actual_version#[vV]}"
  tag="${tag#[vV]}"
  [[ "${actual_version,,}" == "${tag,,}" ]] \
    || die "Source version does not match the release tag; installer was not run."
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
    elif [[ -n "$REF_OVERRIDE" && "$REF_OVERRIDE" != "latest" ]] && repo_git show-ref --verify --quiet "refs/remotes/origin/$ref"; then
      repo_git checkout -B "$ref" "origin/$ref"
    else
      die "LightningOS ref not found after fetch: $ref"
    fi
    configure_runtime_sparse_checkout
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
  configure_runtime_sparse_checkout
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

  if [[ -z "$REF_OVERRIDE" || "$REF_OVERRIDE" == "latest" ]]; then
    require_cmd curl
    require_cmd python3
    [[ -z "${BRLN_RELEASE_API_URL:-}" || "$BRLN_RELEASE_API_URL" == "$RELEASE_API_URL" ]] \
      || die "Custom release catalogs are not supported for verified installations."
    case "${REPO_URL%.git}" in
      https://github.com/jvxis/brln-os-light|git@github.com:jvxis/brln-os-light|ssh://git@github.com/jvxis/brln-os-light) ;;
      *) die "Verified installations require the official source repository." ;;
    esac
  else
    warn "Explicit BRLN_REF/BRLN_BRANCH: development checkout, not an attested latest-release installation."
  fi

  local ref
  ref=$(resolve_ref)
  log "Using LightningOS release/ref: $ref"
  if [[ -z "$REF_OVERRIDE" || "$REF_OVERRIDE" == "latest" ]]; then
    verify_immutable_release "$ref"
    log "Immutable LightningOS release attestation verified"
  fi
  ensure_repo "$ref"
  if [[ -n "$EXPECTED_COMMIT" ]]; then
    verify_release_checkout "$ref"
    log "Source commit and version match the immutable release"
  fi

  local install_dir="$TARGET_DIR/lightningos-light"
  local install_script="$install_dir/$INSTALLER"
  if [[ ! -f "$install_script" ]]; then
    die "$INSTALLER not found at $install_script"
  fi
  chmod +x "$install_script" 2>/dev/null || true
  if [[ -f "$install_dir/scripts/prepare-node-upgrade.sh" ]]; then
    log "The selected release installs/upgrades verified global Node/npm on this dedicated LOS host."
  fi
  log "Running installer"
  export LIGHTNINGOS_RELEASE_BOOTSTRAPPED=1
  exec "$install_script" "$@"
}

main "$@"
