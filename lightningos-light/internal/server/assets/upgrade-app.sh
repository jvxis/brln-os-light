#!/usr/bin/env bash
set -Eeuo pipefail
set -o errtrace

LOG_FILE="/var/log/lightningos-app-upgrade.log"
mkdir -p /var/log /var/lib/lightningos /opt/lightningos/manager /opt/lightningos/ui
exec > >(tee -a "$LOG_FILE") 2>&1

print_step() {
  echo ""
  echo "==> $1"
}

print_ok() {
  echo "[OK] $1"
}

print_warn() {
  echo "[WARN] $1"
}

die() {
  echo "[ERROR] $1" >&2
  exit 1
}

require_root() {
  if [[ "$(id -u)" -ne 0 ]]; then
    die "This script must run as root."
  fi
}

resolve_bin() {
  local name="$1"
  shift

  local candidate=""
  candidate="$(command -v "$name" 2>/dev/null || true)"
  if [[ -n "$candidate" ]]; then
    echo "$candidate"
    return 0
  fi

  for candidate in "$@"; do
    if [[ -x "$candidate" ]]; then
      echo "$candidate"
      return 0
    fi
  done

  return 1
}

VERSION=""
TAG=""
REPO_URL="https://github.com/jvxis/brln-os-light.git"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --version=*)
      VERSION="${1#*=}"
      shift
      ;;
    --tag)
      TAG="${2:-}"
      shift 2
      ;;
    --tag=*)
      TAG="${1#*=}"
      shift
      ;;
    --repo-url)
      REPO_URL="${2:-}"
      shift 2
      ;;
    --repo-url=*)
      REPO_URL="${1#*=}"
      shift
      ;;
    *)
      die "Unknown argument: $1"
      ;;
  esac
done

require_root

export PATH="/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

VERSION="${VERSION#v}"
VERSION="$(echo "${VERSION}" | tr -s '[:space:]' ' ' | sed 's/^ *//;s/ *$//;s/ /-/g')"
if [[ -z "$VERSION" ]]; then
  die "Missing --version."
fi
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([\-][0-9A-Za-z][0-9A-Za-z\.-]*)?$ ]]; then
  die "Invalid version format: ${VERSION}"
fi

if [[ -z "$TAG" ]]; then
  TAG="v${VERSION}"
fi
if [[ "$TAG" =~ [[:space:]] ]]; then
  die "Invalid tag format."
fi

LOCK_FILE="/var/lib/lightningos/app-upgrade.lock"
GIT_BIN="$(resolve_bin git /usr/bin/git /bin/git)" || die "Required command missing: git"
GO_BIN="$(resolve_bin go /usr/local/go/bin/go /usr/bin/go /bin/go)" || die "Required command missing: go"
NPM_BIN="$(resolve_bin npm /usr/bin/npm /usr/local/bin/npm /bin/npm)" || die "Required command missing: npm"
SYSTEMCTL_BIN="$(resolve_bin systemctl /usr/bin/systemctl /bin/systemctl)" || die "Required command missing: systemctl"
SYSTEMD_RUN_BIN="$(resolve_bin systemd-run /usr/bin/systemd-run /bin/systemd-run)" || die "Required command missing: systemd-run"
INSTALL_BIN="$(resolve_bin install /usr/bin/install /bin/install)" || die "Required command missing: install"
CP_BIN="$(resolve_bin cp /usr/bin/cp /bin/cp)" || die "Required command missing: cp"
RM_BIN="$(resolve_bin rm /usr/bin/rm /bin/rm)" || die "Required command missing: rm"
FLOCK_BIN="$(resolve_bin flock /usr/bin/flock /bin/flock)" || die "Required command missing: flock"
DATE_BIN="$(resolve_bin date /usr/bin/date /bin/date)" || die "Required command missing: date"
STAT_BIN="$(resolve_bin stat /usr/bin/stat /bin/stat)" || die "Required command missing: stat"
TEE_BIN="$(resolve_bin tee /usr/bin/tee /bin/tee)" || die "Required command missing: tee"
VISUDO_BIN="$(resolve_bin visudo /usr/sbin/visudo /usr/bin/visudo /sbin/visudo)" || true
APT_GET_BIN="$(resolve_bin apt-get /usr/bin/apt-get /bin/apt-get)" || true
APT_BIN="$(resolve_bin apt /usr/bin/apt /bin/apt)" || true
DPKG_BIN="$(resolve_bin dpkg /usr/bin/dpkg /bin/dpkg)" || true
DOCKER_BIN="$(resolve_bin docker /usr/bin/docker /usr/local/bin/docker)" || true
DOCKER_COMPOSE_BIN="$(resolve_bin docker-compose /usr/bin/docker-compose /usr/local/bin/docker-compose)" || true
SMARTCTL_BIN="$(resolve_bin smartctl /usr/sbin/smartctl /usr/bin/smartctl /sbin/smartctl)" || SMARTCTL_BIN="/usr/sbin/smartctl"
UFW_BIN="$(resolve_bin ufw /usr/sbin/ufw /usr/bin/ufw)" || true
exec 9>"$LOCK_FILE"
if ! "$FLOCK_BIN" -n 9; then
  die "Another app upgrade is already running."
fi

mirror_root="/var/lib/lightningos/src/brln-os-light"
worktree_root="/var/lib/lightningos/worktrees"
worktree_dir=""
project_dir=""

owned_by_root() {
  local path=""
  local owner_uid=""
  for path in "$@"; do
    [[ -e "$path" ]] || continue
    owner_uid="$("$STAT_BIN" -c '%u' "$path" 2>/dev/null || true)"
    if [[ "$owner_uid" != "0" ]]; then
      return 1
    fi
  done
  return 0
}

cleanup() {
  if [[ -n "$worktree_dir" && -d "$worktree_dir" ]]; then
    "$GIT_BIN" -C "$mirror_root" worktree remove --force "$worktree_dir" >/dev/null 2>&1 || true
    "$RM_BIN" -rf "$worktree_dir" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

join_by_comma() {
  local first=1
  local item=""
  for item in "$@"; do
    [[ -n "$item" ]] || continue
    if [[ $first -eq 1 ]]; then
      printf '%s' "$item"
      first=0
      continue
    fi
    printf ', %s' "$item"
  done
}

sudoers_no_requiretty_line() {
  local user="$1"
  [[ -n "${VISUDO_BIN:-}" ]] || return 0
  local tmp
  tmp="$(mktemp)"
  printf 'Defaults:%s !requiretty\n' "$user" > "$tmp"
  if "$VISUDO_BIN" -cf "$tmp" >/dev/null 2>&1; then
    printf 'Defaults:%s !requiretty\n' "$user"
  fi
  rm -f "$tmp"
}

configure_manager_sudoers() {
  local manager_user=""
  manager_user="$("$SYSTEMCTL_BIN" show -p User --value lightningos-manager 2>/dev/null | tr -d '[:space:]')"
  if [[ -z "$manager_user" ]]; then
    manager_user="lightningos"
  fi

  local system_cmds=""
  local app_cmds=()
  local app_cmds_line=""
  local sudoers_path="/etc/sudoers.d/lightningos"
  local alias_suffix=""
  local system_alias=""
  local app_alias=""
  local no_requiretty_line=""

  if [[ "$manager_user" != "lightningos" ]]; then
    sudoers_path="/etc/sudoers.d/lightningos-${manager_user}"
  fi

  system_cmds="${SYSTEMCTL_BIN} restart lnd, ${SYSTEMCTL_BIN} restart lightningos-manager, ${SYSTEMCTL_BIN} restart postgresql, ${SYSTEMCTL_BIN} is-active lightningos-lnd-upgrade, ${SYSTEMCTL_BIN} is-active lightningos-app-upgrade, ${SYSTEMCTL_BIN} reboot, ${SYSTEMCTL_BIN} poweroff, /usr/local/sbin/lightningos-fix-lnd-perms, /usr/local/sbin/lightningos-upgrade-lnd, /usr/local/sbin/lightningos-upgrade-app, ${TEE_BIN} /etc/lightningos/config.yaml, ${SMARTCTL_BIN} *"

  [[ -n "${APT_GET_BIN:-}" ]] && app_cmds+=("${APT_GET_BIN} *")
  [[ -n "${APT_BIN:-}" ]] && app_cmds+=("${APT_BIN} *")
  [[ -n "${DPKG_BIN:-}" ]] && app_cmds+=("${DPKG_BIN} *")
  [[ -n "${DOCKER_BIN:-}" ]] && app_cmds+=("${DOCKER_BIN} *")
  [[ -n "${DOCKER_COMPOSE_BIN:-}" ]] && app_cmds+=("${DOCKER_COMPOSE_BIN} *")
  [[ -n "${SYSTEMD_RUN_BIN:-}" ]] && app_cmds+=("${SYSTEMD_RUN_BIN} *")
  [[ -n "${UFW_BIN:-}" ]] && app_cmds+=("${UFW_BIN} *")
  app_cmds_line="$(join_by_comma "${app_cmds[@]}")"
  if [[ -z "$app_cmds_line" ]]; then
    app_cmds_line="/bin/true"
  fi
  alias_suffix="$(printf '%s' "$manager_user" | tr '[:lower:]-' '[:upper:]_' | tr -cd 'A-Z0-9_')"
  if [[ -z "$alias_suffix" ]]; then
    alias_suffix="LIGHTNINGOS"
  fi
  system_alias="LIGHTNINGOS_SYSTEM_${alias_suffix}"
  app_alias="LIGHTNINGOS_APPS_${alias_suffix}"
  no_requiretty_line="$(sudoers_no_requiretty_line "$manager_user")"

  cat > "$sudoers_path" <<EOF
${no_requiretty_line}
Cmnd_Alias ${system_alias} = ${system_cmds}
Cmnd_Alias ${app_alias} = ${app_cmds_line}
${manager_user} ALL=NOPASSWD: ${system_alias}, ${app_alias}
EOF
  chmod 440 "$sudoers_path"
  if [[ -n "${VISUDO_BIN:-}" ]]; then
    "$VISUDO_BIN" -cf "$sudoers_path" >/dev/null
  fi
  print_ok "Sudoers refreshed for manager user: ${manager_user}"
}

stage_peerswap_assets() {
  local src="$project_dir/assets/binaries/peerswap/version_5_0/amd64"
  local dest="/opt/lightningos/manager/assets/binaries/peerswap/version_5_0/amd64"
  local bin=""

  if [[ ! -d "$src" ]]; then
    print_warn "Peerswap assets not found at $src; skipping"
    return 0
  fi

  print_step "Staging Peerswap assets"
  mkdir -p "$dest"
  for bin in peerswapd pscli psweb; do
    if [[ -f "$src/$bin" ]]; then
      "$INSTALL_BIN" -m 0755 "$src/$bin" "$dest/$bin"
    else
      print_warn "Peerswap binary missing: $src/$bin"
    fi
  done
  print_ok "Peerswap assets staged"
}

print_step "Preparing repository mirror"
mkdir -p "$(dirname "$mirror_root")" "$worktree_root"
if [[ -d "$mirror_root" && ! -d "$mirror_root/.git" ]]; then
  print_warn "Repository mirror path is not a git repository. Recreating it."
  "$RM_BIN" -rf "$mirror_root"
fi
if [[ -d "$mirror_root" ]] && ! owned_by_root "$mirror_root" "$mirror_root/.git"; then
  print_warn "Repository mirror ownership is incompatible with root. Recreating it."
  "$RM_BIN" -rf "$mirror_root"
fi
if [[ ! -d "$mirror_root/.git" ]]; then
  "$RM_BIN" -rf "$mirror_root"
  "$GIT_BIN" clone --no-checkout "$REPO_URL" "$mirror_root"
else
  "$GIT_BIN" -C "$mirror_root" remote set-url origin "$REPO_URL" || true
fi
"$GIT_BIN" -C "$mirror_root" fetch --tags --prune origin

if ! "$GIT_BIN" -C "$mirror_root" rev-parse -q --verify "refs/tags/$TAG^{}" >/dev/null 2>&1; then
  alt_tag=""
  while IFS= read -r candidate; do
    if [[ "${candidate,,}" == "${TAG,,}" ]]; then
      alt_tag="$candidate"
      break
    fi
  done < <("$GIT_BIN" -C "$mirror_root" tag --list)
  if [[ -n "$alt_tag" ]]; then
    TAG="$alt_tag"
  fi
fi

if ! "$GIT_BIN" -C "$mirror_root" rev-parse -q --verify "refs/tags/$TAG^{}" >/dev/null 2>&1; then
  die "Tag not found in repository: ${TAG}"
fi

print_step "Creating temporary worktree for ${TAG}"
safe_tag="$(echo "$TAG" | tr '/\\' '__')"
worktree_dir="${worktree_root}/app-upgrade-${safe_tag}-$("$DATE_BIN" +%Y%m%d%H%M%S)"
"$GIT_BIN" -C "$mirror_root" worktree add --detach "$worktree_dir" "$TAG"

if [[ -f "$worktree_dir/go.mod" ]]; then
  project_dir="$worktree_dir"
elif [[ -f "$worktree_dir/lightningos-light/go.mod" ]]; then
  project_dir="$worktree_dir/lightningos-light"
else
  die "Could not find go.mod in worktree."
fi

if [[ ! -d "$project_dir/ui" ]]; then
  die "Could not find UI directory in worktree."
fi

print_ok "Using project directory: $project_dir"

go_env="GOPATH=/opt/lightningos/go GOCACHE=/opt/lightningos/go-cache GOMODCACHE=/opt/lightningos/go/pkg/mod"
mkdir -p /opt/lightningos/go /opt/lightningos/go-cache /opt/lightningos/go/pkg/mod

print_step "Building manager binary"
mkdir -p "$project_dir/dist"
(cd "$project_dir" && env $go_env GOFLAGS=-mod=mod "$GO_BIN" build -o dist/lightningos-manager ./cmd/lightningos-manager)
"$INSTALL_BIN" -m 0755 "$project_dir/dist/lightningos-manager" /opt/lightningos/manager/lightningos-manager
print_ok "Manager installed"

print_step "Building UI"
(cd "$project_dir/ui" && "$NPM_BIN" install && "$NPM_BIN" run build)
"$RM_BIN" -rf /opt/lightningos/ui/*
"$CP_BIN" -a "$project_dir/ui/dist/." /opt/lightningos/ui/
print_ok "UI installed"

stage_peerswap_assets

print_step "Refreshing manager sudoers"
configure_manager_sudoers

print_step "Restarting lightningos-manager"
"$SYSTEMCTL_BIN" restart lightningos-manager
if "$SYSTEMCTL_BIN" is-active --quiet lightningos-manager; then
  print_ok "lightningos-manager is active"
else
  die "lightningos-manager failed to start"
fi

print_ok "App upgrade complete to ${VERSION} (${TAG})"
