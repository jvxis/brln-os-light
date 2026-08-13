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
PRIVILEGED_BROKER="/usr/local/libexec/lightningos-privileged"
PRIVILEGED_TMPFILES_CONFIG="/etc/tmpfiles.d/lightningos-privileged.conf"
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

privileged_broker_is_trusted() {
  [[ -f "$PRIVILEGED_BROKER" && ! -L "$PRIVILEGED_BROKER" && -x "$PRIVILEGED_BROKER" ]] || return 1
  local owner group mode
  owner=$("$STAT_BIN" -c '%u' "$PRIVILEGED_BROKER" 2>/dev/null) || return 1
  group=$("$STAT_BIN" -c '%g' "$PRIVILEGED_BROKER" 2>/dev/null) || return 1
  mode=$("$STAT_BIN" -c '%a' "$PRIVILEGED_BROKER" 2>/dev/null) || return 1
  [[ "$owner" == "0" && "$group" == "0" ]] || return 1
  (( (8#$mode & 8#022) == 0 ))
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
  local lnd_service="lnd"

  if [[ "$manager_user" != "lightningos" ]]; then
    sudoers_path="/etc/sudoers.d/lightningos-${manager_user}"
  fi

  system_cmds="${SYSTEMCTL_BIN} restart lnd, ${SYSTEMCTL_BIN} restart --no-block lnd, ${SYSTEMCTL_BIN} restart lightningos-manager, ${SYSTEMCTL_BIN} restart postgresql, ${SYSTEMCTL_BIN} is-active lightningos-lnd-upgrade, ${SYSTEMCTL_BIN} is-active lightningos-app-upgrade, ${SYSTEMCTL_BIN} reboot, ${SYSTEMCTL_BIN} poweroff, /usr/local/sbin/lightningos-fix-lnd-perms, /usr/local/sbin/lightningos-upgrade-lnd, /usr/local/sbin/lightningos-upgrade-app, ${TEE_BIN} /etc/lightningos/config.yaml, ${SMARTCTL_BIN} *"
  if [[ "$manager_user" == "lightningos" ]] && privileged_broker_is_trusted; then
    system_cmds+=", ${PRIVILEGED_BROKER} \"\""
  fi
  if ! "$SYSTEMCTL_BIN" is-active --quiet lnd && "$SYSTEMCTL_BIN" is-active --quiet lnd@default; then
    lnd_service="lnd@default"
    system_cmds+=", ${SYSTEMCTL_BIN} restart ${lnd_service}, ${SYSTEMCTL_BIN} restart --no-block ${lnd_service}"
  fi

  [[ -n "${APT_GET_BIN:-}" ]] && app_cmds+=("${APT_GET_BIN} *")
  [[ -n "${APT_BIN:-}" ]] && app_cmds+=("${APT_BIN} *")
  [[ -n "${DPKG_BIN:-}" ]] && app_cmds+=("${DPKG_BIN} *")
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

stage_privilege_cutover() {
  local state_root="/var/lib/lightningos/rollback/0.5.3-privilege-cutover"
  local config_path="/etc/lightningos/config.yaml"
  local service_path="/etc/systemd/system/lightningos-manager.service"
  local dropin_dir="/etc/systemd/system/lightningos-manager.service.d"
  local dropin_path="${dropin_dir}/30-privilege-hardening.conf"
  local rollback_src="$project_dir/internal/server/assets/rollback-privilege-cutover.sh"
  local rollback_bin="/usr/local/sbin/lightningos-rollback-privilege-cutover"
  local manager_user=""
  local sudoers_path=""
  local config_tmp=""
  local dropin_tmp=""
  local raw_groups=""
  local group=""
  local safe_groups=()

  manager_user="$($SYSTEMCTL_BIN show -p User --value lightningos-manager 2>/dev/null | tr -d '[:space:]')"
  [[ -n "$manager_user" ]] || manager_user="lightningos"
  [[ "$manager_user" =~ ^[a-z_][a-z0-9_-]*[$]?$ ]] || return 1
  sudoers_path="/etc/sudoers.d/lightningos"
  if [[ "$manager_user" != "lightningos" ]]; then
    sudoers_path="/etc/sudoers.d/lightningos-${manager_user}"
  fi

  [[ -f "$config_path" && ! -L "$config_path" ]] || return 1
  [[ -f "$rollback_src" && ! -L "$rollback_src" ]] || return 1
  for path in "$state_root" "$dropin_dir" "$dropin_path" "$rollback_bin"; do
    [[ ! -L "$path" ]] || return 1
  done
  "$INSTALL_BIN" -d -o root -g root -m 0700 "$state_root"
  "$INSTALL_BIN" -d -o root -g root -m 0755 "$dropin_dir"
  "$INSTALL_BIN" -o root -g root -m 0755 "$rollback_src" "$rollback_bin"

  if [[ ! -f "$state_root/prepared" ]]; then
    "$CP_BIN" -a -- "$config_path" "$state_root/config.yaml"
    if [[ -f "$service_path" && ! -L "$service_path" ]]; then
      "$CP_BIN" -a -- "$service_path" "$state_root/lightningos-manager.service"
    fi
    if [[ -f "$dropin_path" ]]; then
      [[ ! -L "$dropin_path" ]] || return 1
      "$CP_BIN" -a -- "$dropin_path" "$state_root/30-privilege-hardening.conf"
      : > "$state_root/dropin.existed"
    fi
    printf '%s\n' "$sudoers_path" > "$state_root/sudoers.path"
    if [[ -f "$sudoers_path" && ! -L "$sudoers_path" ]]; then
      "$CP_BIN" -a -- "$sudoers_path" "$state_root/sudoers"
      : > "$state_root/sudoers.existed"
    fi
    printf '%s\n' "$manager_user" > "$state_root/manager.user"
    if id -nG "$manager_user" | tr ' ' '\n' | grep -qx docker; then
      : > "$state_root/had-docker-group"
    fi
    : > "$state_root/prepared"
  fi

  config_tmp="$(mktemp /etc/lightningos/.config.yaml.privilege-cutover.XXXXXX)"
  if ! "$CP_BIN" -a -- "$config_path" "$config_tmp"; then
    "$RM_BIN" -f -- "$config_tmp"
    return 1
  fi
  if ! awk '/^privileged:[[:space:]]*$/ { count++ } END { exit(count > 1) }' "$config_path"; then
    "$RM_BIN" -f -- "$config_tmp"
    return 1
  fi
  if ! awk '
    BEGIN { in_privileged = 0; saw_privileged = 0; saw_mode = 0 }
    /^privileged:[[:space:]]*$/ { in_privileged = 1; saw_privileged = 1; print; next }
    in_privileged && /^[^[:space:]#]/ {
      if (!saw_mode) print "  mode: \"enforce\""
      in_privileged = 0
    }
    in_privileged && /^[[:space:]]+mode:[[:space:]]*/ { print "  mode: \"enforce\""; saw_mode = 1; next }
    { print }
    END {
      if (in_privileged && !saw_mode) print "  mode: \"enforce\""
      if (!saw_privileged) print "\nprivileged:\n  mode: \"enforce\""
    }
  ' "$config_path" > "$config_tmp"; then
    "$RM_BIN" -f -- "$config_tmp"
    return 1
  fi
  mv -f -- "$config_tmp" "$config_path"

  raw_groups="$($SYSTEMCTL_BIN show -p SupplementaryGroups --value lightningos-manager 2>/dev/null || true)"
  for group in $raw_groups; do
    [[ "$group" != "docker" ]] || continue
    [[ "$group" =~ ^[a-zA-Z0-9_.@-]+$ ]] || return 1
    safe_groups+=("$group")
  done
  dropin_tmp="$(mktemp "${dropin_dir}/.30-privilege-hardening.conf.XXXXXX")"
  {
    printf '%s\n' '[Service]' 'SupplementaryGroups='
    if [[ ${#safe_groups[@]} -gt 0 ]]; then
      printf 'SupplementaryGroups=%s\n' "${safe_groups[*]}"
    fi
  } > "$dropin_tmp"
  chmod 0644 "$dropin_tmp"
  chown root:root "$dropin_tmp"
  mv -f -- "$dropin_tmp" "$dropin_path"

  if id -nG "$manager_user" | tr ' ' '\n' | grep -qx docker; then
    gpasswd -d "$manager_user" docker >/dev/null
  fi
  "$SYSTEMCTL_BIN" daemon-reload
  print_ok "Privilege cutover staged with root-only rollback state"
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

refresh_terminal_helper() {
  local src="$project_dir/internal/server/assets/lightningos-terminal.sh"
  local password_src="$project_dir/internal/server/assets/lightningos-terminal-password.sh"
  if [[ ! -f "$src" ]]; then
    print_warn "Terminal helper not found at $src; skipping"
    return 0
  fi

  print_step "Refreshing terminal helper"
  "$INSTALL_BIN" -m 0755 "$src" /usr/local/sbin/lightningos-terminal
  if [[ -f "$password_src" ]]; then
    "$INSTALL_BIN" -m 0755 "$password_src" /usr/local/sbin/lightningos-terminal-password
  else
    print_warn "Terminal password helper not found at $password_src"
  fi
  if "$SYSTEMCTL_BIN" is-active --quiet lightningos-terminal; then
    "$SYSTEMCTL_BIN" restart lightningos-terminal
  fi
  print_ok "Terminal helper refreshed"
}

configure_lnd_restart_policy() {
  if ! "$SYSTEMCTL_BIN" show lnd.service >/dev/null 2>&1; then
    print_warn "lnd.service not found; skipping restart policy"
    return 0
  fi

  print_step "Configuring LND restart policy"
  local dropin_dir="/etc/systemd/system/lnd.service.d"
  mkdir -p "$dropin_dir"
  printf '%s\n' '[Service]' 'Restart=always' 'RestartSec=60' >"$dropin_dir/20-lightningos-restart.conf"
  "$SYSTEMCTL_BIN" daemon-reload
  print_ok "LND will restart after PostgreSQL or other unexpected interruptions"
}

configure_manager_tls_mdns() {
  local src="$project_dir/internal/server/assets/setup-manager-tls-mdns.sh"
  local dest="/usr/local/sbin/lightningos-setup-manager-tls-mdns"
  local manager_user=""
  local manager_group=""

  if [[ ! -f "$src" ]]; then
    print_warn "Manager TLS/mDNS helper not found at $src; skipping"
    return 0
  fi

  if ! command -v avahi-daemon >/dev/null 2>&1; then
    if [[ -n "${APT_GET_BIN:-}" ]]; then
      print_step "Installing local hostname discovery"
      if DEBIAN_FRONTEND=noninteractive "$APT_GET_BIN" update \
        && DEBIAN_FRONTEND=noninteractive "$APT_GET_BIN" install -y avahi-daemon libnss-mdns; then
        print_ok "mDNS packages installed"
      else
        print_warn "Could not install Avahi; trusted IP access will remain available"
      fi
    else
      print_warn "apt-get is unavailable; .local discovery cannot be installed automatically"
    fi
  fi

  manager_user="$("$SYSTEMCTL_BIN" show -p User --value lightningos-manager 2>/dev/null | tr -d '[:space:]')"
  [[ -n "$manager_user" ]] || manager_user="lightningos"
  manager_group="$("$SYSTEMCTL_BIN" show -p Group --value lightningos-manager 2>/dev/null | tr -d '[:space:]')"
  if [[ -z "$manager_group" ]] && id "$manager_user" >/dev/null 2>&1; then
    manager_group="$(id -gn "$manager_user")"
  fi
  [[ -n "$manager_group" ]] || manager_group="lightningos"

  print_step "Migrating manager TLS and local discovery"
  "$INSTALL_BIN" -m 0755 "$src" "$dest"
  if LIGHTNINGOS_MANAGER_GROUP="$manager_group" LIGHTNINGOS_MANAGER_PORT=8443 "$dest"; then
    print_ok "Manager TLS and local discovery are current"
  else
    print_warn "Manager TLS migration was not applied; the existing certificate was preserved"
  fi
}

configure_manager_firewall() {
  local src="$project_dir/internal/server/assets/lightningos-manager-firewall.sh"
  if [[ ! -f "$src" ]]; then
    print_warn "Manager firewall helper not found at $src; skipping"
    return 0
  fi

  print_step "Restricting manager firewall access"
  "$INSTALL_BIN" -m 0755 "$src" /usr/local/sbin/lightningos-manager-firewall
  if /usr/local/sbin/lightningos-manager-firewall; then
    touch /var/lib/lightningos/system-integrations-20260731-v1
    chmod 0644 /var/lib/lightningos/system-integrations-20260731-v1
  else
    print_warn "Manager firewall configuration could not be updated"
  fi
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

print_step "Installing privileged broker foundation"
(cd "$project_dir" && env $go_env GOFLAGS=-mod=mod "$GO_BIN" build -o dist/lightningos-privileged ./cmd/lightningos-privileged)
for broker_path in /usr/local/libexec /var/log/lightningos-privileged /run/lock/lightningos "$PRIVILEGED_BROKER" "$PRIVILEGED_TMPFILES_CONFIG"; do
  [[ ! -L "$broker_path" ]] || die "Refusing symlinked privileged broker path: $broker_path"
done
[[ -f "$project_dir/templates/lightningos-privileged.tmpfiles.conf" ]] || die "Privileged broker tmpfiles template is missing"
"$INSTALL_BIN" -d -o root -g root -m 0755 /usr/local/libexec
"$INSTALL_BIN" -d -o root -g root -m 0755 /etc/tmpfiles.d
"$INSTALL_BIN" -d -o root -g root -m 0750 /var/log/lightningos-privileged /run/lock/lightningos
"$INSTALL_BIN" -o root -g root -m 0644 "$project_dir/templates/lightningos-privileged.tmpfiles.conf" "$PRIVILEGED_TMPFILES_CONFIG"
/usr/bin/systemd-tmpfiles --create "$PRIVILEGED_TMPFILES_CONFIG"
"$INSTALL_BIN" -o root -g root -m 0755 "$project_dir/dist/lightningos-privileged" "$PRIVILEGED_BROKER"
broker_response="$(printf '%s\n' '{"version":1,"request_id":"upgrade_self_test","operation":"self_test","params":{}}' | env -u SUDO_UID -u SUDO_USER -u SUDO_COMMAND "$PRIVILEGED_BROKER")"
if [[ "$broker_response" != *'"request_id":"upgrade_self_test"'* || "$broker_response" != *'"ok":true'* || "$broker_response" != *'"ready":true'* ]]; then
  die "Privileged broker self-test failed"
fi
print_ok "Privileged broker installed and self-test passed"

print_step "Building UI"
(cd "$project_dir/ui" && "$NPM_BIN" install && "$NPM_BIN" run build)
"$RM_BIN" -rf /opt/lightningos/ui/*
"$CP_BIN" -a "$project_dir/ui/dist/." /opt/lightningos/ui/
print_ok "UI installed"

stage_peerswap_assets
refresh_terminal_helper
configure_lnd_restart_policy
configure_manager_tls_mdns
configure_manager_firewall

print_step "Staging privilege cutover"
if ! (stage_privilege_cutover && configure_manager_sudoers); then
  print_warn "Privilege cutover staging failed; restoring the previous boundary"
  /usr/local/sbin/lightningos-rollback-privilege-cutover || true
  die "Privilege cutover could not be staged safely"
fi

print_step "Restarting lightningos-manager"
if "$SYSTEMCTL_BIN" restart lightningos-manager && "$SYSTEMCTL_BIN" is-active --quiet lightningos-manager; then
  print_ok "lightningos-manager is active"
else
  print_warn "Manager failed after privilege cutover; restoring the previous boundary"
  /usr/local/sbin/lightningos-rollback-privilege-cutover || true
  die "lightningos-manager failed to start after cutover"
fi

print_ok "App upgrade complete to ${VERSION} (${TAG})"
