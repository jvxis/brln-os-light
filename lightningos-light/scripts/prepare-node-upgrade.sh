#!/usr/bin/env bash
# Shared, release-pinned build toolchain. May also run standalone via sudo bash.
# Never replaces system Node/npm or edits an application's configuration.

lightningos_node_error() { printf 'Node preparation: %s\n' "$*" >&2; return 1; }

lightningos_node_policy() {
  LOS_NODE_VERSION=24.22.1
  LOS_NPM_VERSION=11.19.1
  case "$(uname -m)" in
    x86_64) LOS_NODE_ARCH=x64; LOS_NODE_SHA256=eb6728e77c576a35162c122c45ccadf224d89ff00abc8dae46cc2897c6002d7c ;;
    aarch64|arm64) LOS_NODE_ARCH=arm64; LOS_NODE_SHA256=33c37be234ba8bee041961047b19160f4d7d0f164e8e246f78bdd10feff148b3 ;;
    *) lightningos_node_error 'unsupported CPU architecture'; return 1 ;;
  esac
  LOS_NODE_STORE=/opt/lightningos/toolchains
  LOS_NODE_ROOT="$LOS_NODE_STORE/node-v${LOS_NODE_VERSION}-linux-${LOS_NODE_ARCH}"
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

lightningos_node_validate_archive() {
  python3 - "$1" "node-v${LOS_NODE_VERSION}-linux-${LOS_NODE_ARCH}" <<'PY'
import posixpath, sys, tarfile
archive, root = sys.argv[1:]
links = {
    root + '/bin/npm': '../lib/node_modules/npm/bin/npm-cli.js',
    root + '/bin/npx': '../lib/node_modules/npm/bin/npx-cli.js',
    root + '/bin/corepack': '../lib/node_modules/corepack/dist/corepack.js',
}
with tarfile.open(archive, 'r:gz') as tar:
    members = tar.getmembers()
    names = set()
    for entry in members:
        name = entry.name.rstrip('/')
        if (name in names or not (name == root or name.startswith(root + '/'))
                or posixpath.normpath(name) != name or '..' in name.split('/') or '\\' in name
                or any(ord(c) < 32 for c in name)
                or not (entry.isfile() or entry.isdir() or entry.issym())
                or (not entry.issym() and entry.mode & 0o7022)):
            sys.exit('Node preparation: unsafe archive entry')
        names.add(name)
        if entry.issym() and links.get(name) != entry.linkname:
            sys.exit('Node preparation: unexpected archive symlink')
    # No extraction through a link, even one with an approved destination.
    symlinks = {e.name.rstrip('/') for e in members if e.issym()}
    for name in names:
        parent = posixpath.dirname(name)
        while parent:
            if parent in symlinks:
                sys.exit('Node preparation: archive traverses a symlink')
            parent = posixpath.dirname(parent)
PY
}

lightningos_node_check_tree() {
  local root="$1" actual
  lightningos_node_safe_directory "$root" || return 1
  [[ -f "$root/.lightningos-sha256" && ! -L "$root/.lightningos-sha256" &&
     "$(<"$root/.lightningos-sha256")" == "$LOS_NODE_SHA256" ]] || return 1
  python3 - "$root" <<'PY'
import os, stat, sys
root = os.path.realpath(sys.argv[1])
for current, directories, files in os.walk(root, followlinks=False):
    for name in directories + files:
        path = os.path.join(current, name)
        info = os.lstat(path)
        if info.st_uid != 0:
            sys.exit('Node preparation: toolchain is not root-owned')
        if stat.S_ISLNK(info.st_mode):
            expected = {
                'bin/npm': '../lib/node_modules/npm/bin/npm-cli.js',
                'bin/npx': '../lib/node_modules/npm/bin/npx-cli.js',
                'bin/corepack': '../lib/node_modules/corepack/dist/corepack.js',
            }.get(os.path.relpath(path, root))
            if os.readlink(path) != expected or not os.path.isfile(path) or not os.path.realpath(path).startswith(root + os.sep):
                sys.exit('Node preparation: unsafe toolchain link')
        elif not (stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode)) or info.st_mode & 0o7022:
            sys.exit('Node preparation: unsafe toolchain permissions/type')
PY
  [[ $? == 0 ]] || return 1
  actual=$(env -u NODE_OPTIONS -u NODE_PATH "$root/bin/node" --version) || return 1
  [[ "$actual" == "v$LOS_NODE_VERSION" ]] || return 1
  actual=$(env -u NODE_OPTIONS -u NODE_PATH "$root/bin/node" "$root/lib/node_modules/npm/bin/npm-cli.js" --version) || return 1
  [[ "$actual" == "$LOS_NPM_VERSION" ]]
}

lightningos_node_install() (
  set -Eeuo pipefail
  local stage='' archive actual directory
  umask 022
  for directory in /opt/lightningos "$LOS_NODE_STORE"; do
    if [[ ! -e "$directory" && ! -L "$directory" ]]; then
      lightningos_node_safe_directory "$(dirname -- "$directory")" || exit 1
      install -d -o root -g root -m 0755 "$directory" || exit 1
    fi
    lightningos_node_safe_directory "$directory" || exit 1
  done
  [[ ! -L "$LOS_NODE_STORE/.node-install.lock" ]] || exit 1
  if [[ -e "$LOS_NODE_STORE/.node-install.lock" ]]; then
    [[ -f "$LOS_NODE_STORE/.node-install.lock" && "$(stat -c '%u' "$LOS_NODE_STORE/.node-install.lock")" == 0 &&
       -z "$(find "$LOS_NODE_STORE/.node-install.lock" -perm /022 -print -quit)" ]] || exit 1
  fi
  exec 9>"$LOS_NODE_STORE/.node-install.lock" || exit 1
  flock -x 9 || exit 1
  if [[ -e "$LOS_NODE_ROOT" || -L "$LOS_NODE_ROOT" ]]; then
    lightningos_node_check_tree "$LOS_NODE_ROOT" || { lightningos_node_error 'existing managed Node failed validation; refusing replacement'; exit 1; }
    return 0
  fi
  stage=$(mktemp -d "$LOS_NODE_STORE/.node-stage.XXXXXX") || exit 1
  # Only this freshly allocated, fixed-store stage is eligible for cleanup.
  trap 'if [[ "$stage" == "$LOS_NODE_STORE"/.node-stage.* && -d "$stage" && ! -L "$stage" ]]; then rm -rf -- "$stage"; fi' EXIT
  archive="$stage/node.tar.gz"
  curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 20 --max-time 600 \
    "https://nodejs.org/dist/v${LOS_NODE_VERSION}/node-v${LOS_NODE_VERSION}-linux-${LOS_NODE_ARCH}.tar.gz" --output "$archive" || exit 1
  actual=$(sha256sum -- "$archive") || exit 1
  actual="${actual%% *}"
  [[ "$actual" == "$LOS_NODE_SHA256" ]] || { lightningos_node_error 'archive checksum mismatch'; exit 1; }
  lightningos_node_validate_archive "$archive" || exit 1
  tar --no-same-owner -xzf "$archive" -C "$stage" || exit 1
  printf '%s\n' "$LOS_NODE_SHA256" >"$stage/$(basename "$LOS_NODE_ROOT")/.lightningos-sha256" || exit 1
  lightningos_node_check_tree "$stage/$(basename "$LOS_NODE_ROOT")" || exit 1
  mv -T -- "$stage/$(basename "$LOS_NODE_ROOT")" "$LOS_NODE_ROOT" || exit 1
)

lightningos_prepare_node() {
  local required
  [[ "$EUID" == 0 && "$(uname -s)" == Linux ]] || { lightningos_node_error 'root on Linux is required'; return 1; }
  for required in curl sha256sum python3 tar flock stat find install mv; do
    command -v "$required" >/dev/null || { lightningos_node_error "missing prerequisite: $required"; return 1; }
  done
  lightningos_node_policy || return 1
  lightningos_node_install || return 1
  export NODE_BIN="$LOS_NODE_ROOT/bin/node" NPM_BIN="$LOS_NODE_ROOT/bin/npm"
  export PATH="$LOS_NODE_ROOT/bin:$PATH"
  # Restrict these changes to the installer/upgrader process, never the host.
  unset NODE_OPTIONS NODE_PATH NODE_ENV npm_config_prefix NPM_CONFIG_PREFIX npm_config_omit NPM_CONFIG_OMIT
  printf '[OK] LOS build toolchain: Node %s / npm %s (%s)\n' "$LOS_NODE_VERSION" "$LOS_NPM_VERSION" "$LOS_NODE_ARCH"
}

lightningos_node_dropin_content() {
  printf '%s\n' '# Managed by LightningOS prepare-node-upgrade.sh; upgrade service only.' \
    '[Service]' "Environment=\"PATH=$LOS_NODE_ROOT/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\"" \
    'UnsetEnvironment=NODE_OPTIONS NODE_PATH NODE_ENV npm_config_prefix NPM_CONFIG_PREFIX npm_config_omit NPM_CONFIG_OMIT'
}

lightningos_node_upgrade_main() (
  set -Eeuo pipefail
  local dropin_dir=/etc/systemd/system/lightningos-app-upgrade.service.d dropin temporary
  [[ "${1:-}" != --help ]] || { echo 'Usage: sudo bash prepare-node-upgrade.sh [--check]'; exit 0; }
  [[ $# == 0 || ( $# == 1 && "$1" == --check ) ]] || { lightningos_node_error 'unknown argument'; exit 1; }
  [[ "$EUID" == 0 && "$(uname -s)" == Linux ]] || { lightningos_node_error 'root on Linux is required'; exit 1; }
  lightningos_node_policy
  dropin="$dropin_dir/20-lightningos-node.conf"
  if [[ "${1:-}" == --check ]]; then
    lightningos_node_check_tree "$LOS_NODE_ROOT"
    lightningos_node_safe_directory "$dropin_dir"
    [[ -f "$dropin" && ! -L "$dropin" && "$(stat -c '%u' "$dropin")" == 0 &&
       -z "$(find "$dropin" -perm /022 -print -quit)" &&
       "$(<"$dropin")" == "$(lightningos_node_dropin_content)" ]]
    echo '[OK] Managed Node/npm and legacy upgrade preparation verified'
    exit 0
  fi
  [[ -f /opt/lightningos/manager/lightningos-manager && ! -L /opt/lightningos/manager/lightningos-manager ]] || {
    lightningos_node_error 'existing LightningOS Manager not found; use the normal installer for a new node'; exit 1;
  }
  command -v systemctl >/dev/null
  # Never race a running upgrade or overwrite another administrator's file.
  if systemctl is-active --quiet lightningos-app-upgrade.service; then
    lightningos_node_error 'an upgrade is running; wait for it to finish'; exit 1
  fi
  lightningos_node_safe_directory /etc/systemd/system
  if [[ ! -e "$dropin_dir" && ! -L "$dropin_dir" ]]; then install -d -o root -g root -m 0755 "$dropin_dir"; fi
  lightningos_node_safe_directory "$dropin_dir"
  if [[ -e "$dropin" || -L "$dropin" ]]; then
    [[ -f "$dropin" && ! -L "$dropin" && "$(stat -c '%u' "$dropin")" == 0 &&
       -z "$(find "$dropin" -perm /022 -print -quit)" &&
       "$(head -n 1 "$dropin")" == '# Managed by LightningOS prepare-node-upgrade.sh; upgrade service only.' ]] || {
      lightningos_node_error 'refusing an unmanaged or unsafe upgrade drop-in'; exit 1;
    }
  fi
  lightningos_prepare_node
  # Publish only after Node AND npm run successfully. No restart is required.
  temporary=$(mktemp "$dropin_dir/.node.XXXXXX")
  trap '[[ -z "$temporary" ]] || rm -f -- "$temporary"' EXIT
  lightningos_node_dropin_content >"$temporary"
  chmod 0644 "$temporary"
  if [[ -f "$dropin" ]] && ! cmp -s "$temporary" "$dropin"; then
    lightningos_node_error 'existing preparation differs; refusing automatic replacement'; exit 1
  fi
  mv -T -- "$temporary" "$dropin"
  temporary=''
  systemctl daemon-reload
  echo '[OK] Ready: start the LightningOS upgrade through the UI. No services were restarted.'
)

if [[ "${BASH_SOURCE[0]:-$0}" == "$0" ]]; then
  lightningos_node_upgrade_main "$@"
fi
