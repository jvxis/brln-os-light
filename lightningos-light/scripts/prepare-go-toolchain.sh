#!/usr/bin/env bash

# Sourced only from the authenticated release checkout by root-run installers
# and upgrade-app.sh. No network code is sourced. The policy is parsed as data.

lightningos_go_error() {
  echo "Go preparation: $*" >&2
  return 1
}

lightningos_go_version_valid() {
  [[ "$1" =~ ^[1-9][0-9]*\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
}

lightningos_go_version_at_least() {
  local actual="$1" required="$2" i
  local -a actual_parts required_parts
  lightningos_go_version_valid "$actual" && lightningos_go_version_valid "$required" || return 1
  IFS=. read -r -a actual_parts <<<"$actual"
  IFS=. read -r -a required_parts <<<"$required"
  for i in 0 1 2; do
    (( ${actual_parts[i]} > ${required_parts[i]} )) && return 0
    (( ${actual_parts[i]} < ${required_parts[i]} )) && return 1
  done
  return 0
}

lightningos_go_read_policy() {
  local policy="$1" key value
  LOS_GO_VERSION=""
  LOS_GO_AMD64_SHA256=""
  LOS_GO_ARM64_SHA256=""
  [[ -f "$policy" && ! -L "$policy" ]] || { lightningos_go_error "policy is missing or unsafe"; return 1; }
  while IFS='=' read -r key value || [[ -n "$key" ]]; do
    key="${key%$'\r'}"
    value="${value%$'\r'}"
    case "$key" in
      ''|\#*) continue ;;
      version)
        [[ -z "$LOS_GO_VERSION" ]] && lightningos_go_version_valid "$value" || { lightningos_go_error "invalid or duplicate version"; return 1; }
        LOS_GO_VERSION="$value" ;;
      linux_amd64_sha256)
        [[ -z "$LOS_GO_AMD64_SHA256" && "$value" =~ ^[0-9a-f]{64}$ ]] || { lightningos_go_error "invalid or duplicate amd64 checksum"; return 1; }
        LOS_GO_AMD64_SHA256="$value" ;;
      linux_arm64_sha256)
        [[ -z "$LOS_GO_ARM64_SHA256" && "$value" =~ ^[0-9a-f]{64}$ ]] || { lightningos_go_error "invalid or duplicate arm64 checksum"; return 1; }
        LOS_GO_ARM64_SHA256="$value" ;;
      *) lightningos_go_error "unknown policy field"; return 1 ;;
    esac
  done <"$policy"
  [[ -n "$LOS_GO_VERSION" && -n "$LOS_GO_AMD64_SHA256" && -n "$LOS_GO_ARM64_SHA256" ]] || { lightningos_go_error "incomplete policy"; return 1; }
}

lightningos_go_check_module() {
  local module="$1" version="$2" directive required rest found=0
  [[ -f "$module" && ! -L "$module" ]] || { lightningos_go_error "go.mod is missing or unsafe"; return 1; }
  while read -r directive required rest || [[ -n "$directive" ]]; do
    required="${required%$'\r'}"
    case "$directive" in
      go) found=1 ;;
      toolchain)
        [[ "$required" == default ]] && continue
        required="${required#go}" ;;
      *) continue ;;
    esac
    [[ "$required" =~ ^[1-9][0-9]*\.[0-9]+$ ]] && required="${required}.0"
    lightningos_go_version_at_least "$version" "$required" || {
      lightningos_go_error "release requires ${directive} ${required}, but pins Go ${version}"; return 1;
    }
  done <"$module"
  [[ "$found" == 1 ]] || { lightningos_go_error "go.mod has no Go requirement"; return 1; }
}

# Check each existing ancestor before creating or using a managed path. This
# also refuses a symlink in /opt/lightningos, not just at the final directory.
lightningos_go_safe_directory() {
  local path="$1" mode owner display_path expected_owner
  # Quote paths for single-line diagnostics; never print configuration contents
  # or attempt to repair an existing installation's ownership/permissions.
  printf -v display_path '%q' "$path"
  [[ "$path" == /* && "$path" != *'/../'* && "$path" != */.. ]] || {
    lightningos_go_error "directory ${display_path}: expected an absolute path without parent traversal"; return 1;
  }
  if [[ "$path" != / ]]; then
    lightningos_go_safe_directory "$(dirname -- "$path")" || return 1
  fi
  [[ ! -L "$path" ]] || {
    lightningos_go_error "directory ${display_path}: symbolic links are not allowed"; return 1;
  }
  [[ -d "$path" ]] || {
    lightningos_go_error "directory ${display_path}: directory is missing or is not a directory"; return 1;
  }
  owner=$(stat -c '%u' -- "$path" 2>/dev/null) || {
    lightningos_go_error "directory ${display_path}: unable to read owner UID"; return 1;
  }
  mode=$(stat -c '%a' -- "$path" 2>/dev/null) || {
    lightningos_go_error "directory ${display_path}: unable to read permission mode"; return 1;
  }
  # Root-owned sticky /tmp is permitted as an ancestor for isolated fixtures.
  expected_owner=0
  [[ "$EUID" == 0 ]] || expected_owner="0 or ${EUID}"
  [[ "$owner" == "$EUID" || "$owner" == 0 ]] || {
    lightningos_go_error "directory ${display_path}: owner UID ${owner} is not allowed (expected UID ${expected_owner})"; return 1;
  }
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] || {
    lightningos_go_error "directory ${display_path}: unable to validate permission mode"; return 1;
  }
  (( (8#$mode & 0022) == 0 )) || [[ "$path" == /tmp && "$owner" == 0 && "$mode" == 1777 ]] || {
    lightningos_go_error "directory ${display_path}: mode ${mode} permits group or other users to write"; return 1;
  }
}

lightningos_go_check_tree() {
  local directory="$1" version="$2" arch="$3" expected="$4" unsafe output
  [[ -d "$directory" && ! -L "$directory" && -x "$directory/bin/go" && -f "$directory/.lightningos-sha256" ]] || return 1
  unsafe=$(find "$directory" \( -type l -o ! -user "$EUID" -o -perm /022 \) -print -quit) || return 1
  [[ -z "$unsafe" && "$(cat "$directory/.lightningos-sha256")" == "$expected" ]] || return 1
  output=$(env GOROOT="$directory" GOENV=off GOWORK=off GOTOOLCHAIN=local "$directory/bin/go" version) || return 1
  [[ "$output" == "go version go${version} linux/${arch}" ]]
}

# All mutations stay inside the root-owned toolchain store. Existing versions
# (including /usr/local/go) are never replaced. A failed build can leave a
# verified, reusable toolchain here without changing the running application.
lightningos_go_install() (
  local store="$1" version="$2" arch="$3" expected="$4" stage="" target archive entry
  lightningos_go_version_valid "$version" && [[ "$arch" == amd64 || "$arch" == arm64 ]] && [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || return 1
  lightningos_go_safe_directory "$store" || { lightningos_go_error "unsafe toolchain store"; return 1; }
  [[ ! -L "$store/.install.lock" ]] || return 1
  if [[ -e "$store/.install.lock" ]]; then
    [[ -f "$store/.install.lock" && "$(stat -c '%u' "$store/.install.lock")" == "$EUID" ]] || return 1
  fi
  umask 022
  exec 8>"$store/.install.lock" || return 1
  flock -x 8 || return 1
  target="$store/go${version}.linux-${arch}"
  if [[ -e "$target" || -L "$target" ]]; then
    lightningos_go_check_tree "$target" "$version" "$arch" "$expected" || { lightningos_go_error "existing managed toolchain failed validation"; return 1; }
    echo "Go ${version} (${arch}) is already prepared"
    return 0
  fi
  stage=$(mktemp -d "$store/.go-stage.XXXXXX") || return 1
  trap '[[ -z "$stage" ]] || rm -rf -- "$stage"' EXIT
  archive="$stage/go.tar.gz"
  echo "Downloading and verifying Go ${version} (${arch})"
  lightningos_download_verified_artifact "https://go.dev/dl/go${version}.linux-${arch}.tar.gz" "$archive" "$expected" "Go ${version} linux-${arch}" || return 1
  tar -tzf "$archive" >"$stage/entries" || return 1
  # The authenticated official archive must contain only the go/ subtree.
  while IFS= read -r entry; do
    [[ "$entry" == go/* && "$entry" != *'/../'* && "$entry" != */.. ]] || { lightningos_go_error "unsafe archive path"; return 1; }
  done <"$stage/entries"
  # Reject links/devices before extraction, rather than detecting them only
  # after a later archive entry could have traversed a newly created link.
  tar -tvzf "$archive" >"$stage/types" || return 1
  while IFS= read -r entry; do
    case "${entry:0:1}" in
      -|d) ;;
      *) lightningos_go_error "unsupported archive entry type"; return 1 ;;
    esac
  done <"$stage/types"
  tar --no-same-owner -xzf "$archive" -C "$stage" || return 1
  [[ -d "$stage/go" && ! -L "$stage/go" ]] || return 1
  printf '%s\n' "$expected" >"$stage/go/.lightningos-sha256" || return 1
  lightningos_go_check_tree "$stage/go" "$version" "$arch" "$expected" || { lightningos_go_error "downloaded toolchain failed validation"; return 1; }
  # Both paths are on the same filesystem; publish only a completely verified tree.
  mv -T -- "$stage/go" "$target" || return 1
  echo "Go ${version} (${arch}) prepared"
)

lightningos_prepare_go() {
  local project="$1" arch checksum store="/opt/lightningos/toolchains"
  [[ "$EUID" == 0 ]] || { lightningos_go_error "root is required"; return 1; }
  lightningos_go_read_policy "$project/scripts/go-toolchain.conf" || return 1
  lightningos_go_check_module "$project/go.mod" "$LOS_GO_VERSION" || return 1
  [[ "$(uname -s)" == Linux ]] || { lightningos_go_error "only Linux is supported"; return 1; }
  case "$(uname -m)" in
    x86_64) arch=amd64; checksum="$LOS_GO_AMD64_SHA256" ;;
    aarch64|arm64) arch=arm64; checksum="$LOS_GO_ARM64_SHA256" ;;
    *) lightningos_go_error "unsupported CPU architecture"; return 1 ;;
  esac
  lightningos_go_safe_directory /opt || return 1
  if [[ ! -e /opt/lightningos && ! -L /opt/lightningos ]]; then
    install -d -m 0755 /opt/lightningos || return 1
  fi
  lightningos_go_safe_directory /opt/lightningos || return 1
  if [[ ! -e "$store" && ! -L "$store" ]]; then
    install -d -m 0755 "$store" || return 1
  fi
  lightningos_go_install "$store" "$LOS_GO_VERSION" "$arch" "$checksum" || return 1
  GO_BIN="$store/go${LOS_GO_VERSION}.linux-${arch}/bin/go"
  export GOROOT="${GO_BIN%/bin/go}" GOTOOLCHAIN=local GOENV=off GOWORK=off
  export PATH="${GO_BIN%/go}:$PATH"
  echo "Using Go ${LOS_GO_VERSION} from ${GO_BIN}"
}
