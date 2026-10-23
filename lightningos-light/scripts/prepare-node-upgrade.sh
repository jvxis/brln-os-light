#!/usr/bin/env bash
# Release-pinned global Node/npm for dedicated Debian/Ubuntu LightningOS hosts.
lightningos_node_error() { printf 'Node preparation: %s\n' "$*" >&2; return 1; }

lightningos_node_policy() {
  LOS_NODE_VERSION=24.22.1
  LOS_NPM_VERSION=11.19.1
  LOS_NODE_DEB_VERSION=24.22.1-1nodesource1
  case "$(dpkg --print-architecture)" in
    amd64) LOS_NODE_ARCH=amd64; LOS_NODE_SHA256=2f66fa3df76eb8c88de752e7f9f00fba5fa9e69707bda3345b4dac720a7639cb ;;
    arm64) LOS_NODE_ARCH=arm64; LOS_NODE_SHA256=1c31bf34801ccea3731b587a48c3f6a52ad9592d536f4af0f672d6c445570435 ;;
    *) lightningos_node_error 'unsupported CPU architecture'; return 1 ;;
  esac
}

lightningos_node_safe_directory() {
  local directory="$1" owner mode
  [[ "$directory" == /* && "$directory" != *'/../'* && "$directory" != */.. ]] || return 1
  if [[ "$directory" != / ]]; then
    lightningos_node_safe_directory "$(dirname -- "$directory")" || return 1
  fi
  [[ -d "$directory" && ! -L "$directory" ]] || { lightningos_node_error "unsafe directory: $directory"; return 1; }
  owner=$(stat -c '%u' -- "$directory") || return 1
  mode=$(stat -c '%a' -- "$directory") || return 1
  [[ "$owner" == 0 && "$mode" =~ ^[0-7]{3,4}$ ]] && (( (8#$mode & 0022) == 0 )) || {
    lightningos_node_error "directory is not root-controlled: $directory"; return 1;
  }
}

# Never delete an operator's nvm/n/manual installation or global npm packages.
lightningos_node_check_shadowing() {
  local name directory candidate
  for name in node npm npx; do
    for directory in /usr/local/sbin /usr/local/bin /usr/sbin /sbin /bin; do
      candidate="$directory/$name"
      if [[ -e "$candidate" || -L "$candidate" ]]; then
        [[ "$(readlink -f -- "$candidate")" == "$(readlink -f -- "/usr/bin/$name")" ]] || {
          lightningos_node_error "conflicting global executable $candidate; resolve the duplicate installation before retrying"; return 1;
        }
      fi
    done
  done
}

# Refuse APT plans which remove/change unrelated packages. Locale is forced by
# the caller; distro npm and development headers are replaced by nodejs itself.
lightningos_node_validate_plan() {
  local action package rest
  while read -r action package rest; do
    case "$action" in
      Remv)
        case "${package%%:*}" in
          npm|nodejs-doc|nodejs-dev|nodejs-legacy|libnode-dev) ;;
          *) lightningos_node_error "APT would remove unrelated package: $package"; return 1 ;;
        esac ;;
      Inst|Conf)
        case "${package%%:*}" in
          nodejs) ;;
          *) lightningos_node_error "APT would change unrelated package: $package"; return 1 ;;
        esac ;;
    esac
  done < "$1"
}

lightningos_node_check_global() {
  local actual version
  version=$(dpkg-query -W -f='${Version}' nodejs 2>/dev/null) || return 1
  [[ "$version" == "$LOS_NODE_DEB_VERSION" ]] || return 1
  lightningos_node_check_shadowing || return 1
  actual=$(env -u NODE_OPTIONS -u NODE_PATH /usr/bin/node --version) || return 1
  [[ "$actual" == "v$LOS_NODE_VERSION" ]] || return 1
  actual=$(env -u NODE_OPTIONS -u NODE_PATH PATH=/usr/bin:/bin /usr/bin/npm --version) || return 1
  [[ "$actual" == "$LOS_NPM_VERSION" ]]
}

lightningos_node_install() (
  set -Eeuo pipefail
  local stage='' archive actual installed package plan
  local -a apt_options=(-o DPkg::Lock::Timeout=120 -o APT::Get::AutomaticRemove=false)
  umask 022
  lightningos_node_safe_directory /var/cache || exit 1
  if [[ -e /var/cache/lightningos-node.lock || -L /var/cache/lightningos-node.lock ]]; then
    [[ -f /var/cache/lightningos-node.lock && ! -L /var/cache/lightningos-node.lock &&
       "$(stat -c '%u' /var/cache/lightningos-node.lock)" == 0 &&
       -z "$(find /var/cache/lightningos-node.lock -perm /022 -print -quit)" ]] || exit 1
  fi
  exec 9>/var/cache/lightningos-node.lock || exit 1
  flock -x 9 || exit 1
  lightningos_node_check_shadowing || exit 1
  installed=$(dpkg-query -W -f='${Version}' nodejs 2>/dev/null || true)
  if [[ "$installed" == "$LOS_NODE_DEB_VERSION" ]]; then
    lightningos_node_check_global || { lightningos_node_error 'installed global Node/npm failed validation'; exit 1; }
    exit 0
  fi
  if [[ -n "$installed" ]] && dpkg --compare-versions "$installed" gt "$LOS_NODE_DEB_VERSION"; then
    lightningos_node_error "installed Node ($installed) is newer than this release's tested version; refusing downgrade"; exit 1
  fi
  lightningos_node_safe_directory /var/cache || exit 1
  stage=$(mktemp -d /var/cache/lightningos-node.XXXXXX) || exit 1
  trap 'if [[ "$stage" == /var/cache/lightningos-node.* && -d "$stage" && ! -L "$stage" ]]; then rm -rf -- "$stage"; fi' EXIT
  archive="$stage/nodejs_${LOS_NODE_DEB_VERSION}_${LOS_NODE_ARCH}.deb"
  curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 20 --max-time 600 \
    "https://deb.nodesource.com/node_24.x/pool/main/n/nodejs/$(basename "$archive")" --output "$archive" || exit 1
  actual=$(sha256sum -- "$archive") || exit 1
  [[ "${actual%% *}" == "$LOS_NODE_SHA256" ]] || { lightningos_node_error 'package checksum mismatch'; exit 1; }
  [[ "$(dpkg-deb -f "$archive" Package)" == nodejs &&
     "$(dpkg-deb -f "$archive" Version)" == "$LOS_NODE_DEB_VERSION" &&
     "$(dpkg-deb -f "$archive" Architecture)" == "$LOS_NODE_ARCH" ]] || {
    lightningos_node_error 'unexpected package identity'; exit 1;
  }
  local -a targets=("$archive")
  for package in npm nodejs-doc nodejs-dev nodejs-legacy libnode-dev; do
    if [[ "$(dpkg-query -W -f='${db:Status-Status}' "$package" 2>/dev/null || true)" == installed ]]; then
      targets+=("$package-")
    fi
  done
  plan="$stage/apt-plan"
  LC_ALL=C apt-get "${apt_options[@]}" --simulate install "${targets[@]}" >"$plan" || { lightningos_node_error 'APT simulation failed; global Node was not changed'; exit 1; }
  lightningos_node_validate_plan "$plan" || exit 1
  # No apt update, autoremove, source rewrites or app rebuilds. needrestart only
  # reports: upgrading a build prerequisite must not restart LND/Bitcoin/apps.
  DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l LC_ALL=C \
    apt-get "${apt_options[@]}" --yes install "${targets[@]}" || {
      lightningos_node_error 'APT installation failed; inspect dpkg status and retry before upgrading LOS'; exit 1;
    }
  lightningos_node_check_global || { lightningos_node_error 'global Node/npm post-install validation failed'; exit 1; }
)

lightningos_prepare_node() {
  local required
  [[ "$EUID" == 0 && "$(uname -s)" == Linux && -f /etc/debian_version ]] || { lightningos_node_error 'root on Debian/Ubuntu Linux is required'; return 1; }
  for required in curl sha256sum dpkg dpkg-deb dpkg-query apt-get stat readlink env flock find; do
    command -v "$required" >/dev/null || { lightningos_node_error "missing prerequisite: $required"; return 1; }
  done
  lightningos_node_policy || return 1
  lightningos_node_install || return 1
  export NODE_BIN=/usr/bin/node NPM_BIN=/usr/bin/npm
  export PATH="/usr/bin:/bin:$PATH"
  unset NODE_OPTIONS NODE_PATH NODE_ENV npm_config_prefix NPM_CONFIG_PREFIX npm_config_omit NPM_CONFIG_OMIT
  printf '[OK] Global Node %s / npm %s installed and verified (%s)\n' "$LOS_NODE_VERSION" "$LOS_NPM_VERSION" "$LOS_NODE_ARCH"
}

lightningos_node_upgrade_main() (
  set -Eeuo pipefail
  [[ "${1:-}" != --help ]] || { echo 'Usage: sudo bash prepare-node-upgrade.sh [--check]'; exit 0; }
  [[ $# == 0 || ( $# == 1 && "$1" == --check ) ]] || { lightningos_node_error 'unknown argument'; exit 1; }
  [[ "$EUID" == 0 && "$(uname -s)" == Linux && -f /etc/debian_version ]] || { lightningos_node_error 'root on Debian/Ubuntu Linux is required'; exit 1; }
  lightningos_node_policy
  if [[ "${1:-}" == --check ]]; then
    lightningos_node_check_global
    echo '[OK] Global Node/npm verified'
    exit 0
  fi
  [[ -f /opt/lightningos/manager/lightningos-manager && ! -L /opt/lightningos/manager/lightningos-manager ]] || {
    lightningos_node_error 'existing LightningOS Manager not found; use the normal installer for a new node'; exit 1;
  }
  if systemctl is-active --quiet lightningos-app-upgrade.service; then
    lightningos_node_error 'an upgrade is running; wait for it to finish'; exit 1
  fi
  lightningos_prepare_node
  echo '[OK] Ready: start the LightningOS upgrade through the UI. Global Node/npm were updated; app configurations were not changed.'
)

if [[ "${BASH_SOURCE[0]:-$0}" == "$0" ]]; then
  lightningos_node_upgrade_main "$@"
fi
