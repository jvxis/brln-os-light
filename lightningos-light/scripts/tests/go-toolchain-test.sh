#!/usr/bin/env bash
set -Eeuo pipefail

# Unprivileged Linux fixtures: no writes to /opt, no services, no network.
project=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$project/scripts/install-artifact-verification.sh"
source "$project/scripts/prepare-go-toolchain.sh"
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -m 0755 "$fixture/store" "$fixture/payload"
printf 'preserve me\n' >"$fixture/system-go"

fail() { echo "FAIL: $*" >&2; exit 1; }
reject() { if "$@"; then fail "unexpected success: $*"; fi; }

lightningos_go_read_policy "$project/scripts/go-toolchain.conf"
[[ "$LOS_GO_VERSION" == 1.26.8 ]] || fail policy
lightningos_go_check_module "$project/go.mod" "$LOS_GO_VERSION"
for pair in '1.26.8 1.24.12' '1.26.8 1.26.8' '1.27.0 1.26.99' '1.26.10 1.26.9'; do
  read -r actual required <<<"$pair"
  lightningos_go_version_at_least "$actual" "$required" || fail "version comparison $pair"
done
for pair in '1.26.7 1.26.8' '1.9.9 1.26.0' '1.26.8rc1 1.26.8' '1.026.8 1.26.8'; do
  read -r actual required <<<"$pair"
  reject lightningos_go_version_at_least "$actual" "$required"
done
cp "$project/scripts/go-toolchain.conf" "$fixture/policy"
printf 'version=1.26.8\n' >>"$fixture/policy"
reject lightningos_go_read_policy "$fixture/policy"
printf 'version=$(touch %s/executed)\n' "$fixture" >"$fixture/policy"
reject lightningos_go_read_policy "$fixture/policy"
[[ ! -e "$fixture/executed" ]] || fail 'policy executed shell code'
printf 'module fixture\ngo 1.27.0\n' >"$fixture/go.mod"
reject lightningos_go_check_module "$fixture/go.mod" 1.26.8
printf 'module fixture\ngo 1.24\ntoolchain go1.27.1' >"$fixture/go.mod"
reject lightningos_go_check_module "$fixture/go.mod" 1.26.8
printf 'module fixture\ngo 1.24\ntoolchain default\n' >"$fixture/go.mod"
lightningos_go_check_module "$fixture/go.mod" 1.26.8
echo 'PASS: strict policy and module requirements'

# Diagnostic failures must identify the actual rejected ancestor without
# changing its ownership/mode or emitting unescaped path control characters.
directory_error() {
  local path="$1" offending="$2" reason="$3" output quoted
  if output=$(lightningos_go_safe_directory "$path" 2>&1); then
    fail "unsafe directory accepted: $path"
  fi
  printf -v quoted '%q' "$offending"
  [[ "$output" == *"directory ${quoted}: ${reason}"* ]] || fail "missing directory diagnostic: $output"
  [[ "$output" != *$'\n'* ]] || fail 'multiline directory diagnostic'
}
lightningos_go_safe_directory "$fixture/store"
directory_error relative relative 'expected an absolute path without parent traversal'
directory_error "$fixture/../invalid" "$fixture/../invalid" 'expected an absolute path without parent traversal'
directory_error "$fixture/missing" "$fixture/missing" 'directory is missing or is not a directory'
directory_error "$fixture/system-go" "$fixture/system-go" 'directory is missing or is not a directory'
ln -s "$fixture/store" "$fixture/directory-link"
directory_error "$fixture/directory-link" "$fixture/directory-link" 'symbolic links are not allowed'
ln -s "$fixture/missing" "$fixture/broken-link"
directory_error "$fixture/broken-link" "$fixture/broken-link" 'symbolic links are not allowed'
mkdir -p "$fixture/unsafe-parent/child"
chmod 0775 "$fixture/unsafe-parent"
directory_error "$fixture/unsafe-parent/child" "$fixture/unsafe-parent" 'mode 775 permits group or other users to write'
[[ "$(stat -c '%a' "$fixture/unsafe-parent")" == 775 ]] || fail 'permissions were repaired'
chmod 0755 "$fixture/unsafe-parent"
lightningos_go_safe_directory "$fixture/unsafe-parent/child"
directory_error "$fixture/line"$'\n'"break" "$fixture/line"$'\n'"break" 'directory is missing or is not a directory'
if [[ "$EUID" == 0 ]]; then
  mkdir -p "$fixture/foreign-parent/child"
  chown 65534:65534 "$fixture/foreign-parent"
  for attempt in 1 2; do
    directory_error "$fixture/foreign-parent/child" "$fixture/foreign-parent" 'owner UID 65534 is not allowed (expected UID 0)'
  done
  (
    lightningos_download_verified_artifact() { fail 'download reached with unsafe ancestor'; }
    reject lightningos_go_install "$fixture/foreign-parent/child" 1.26.8 amd64 "$(printf '%064d' 0)"
    [[ -z "$(find "$fixture/foreign-parent/child" -mindepth 1 -print -quit)" ]] || fail 'unsafe ancestor allowed store writes'
  )
  [[ "$(stat -c '%u:%g:%a' "$fixture/foreign-parent")" == 65534:65534:755 ]] || fail 'ownership was repaired'
  chown 0:0 "$fixture/foreign-parent"
  lightningos_go_safe_directory "$fixture/foreign-parent/child"
else
  echo 'SKIP: foreign-owner diagnostic requires root'
fi
# Simulate metadata read failures only; normal cases above use real Linux stat.
(
  stat() { return 1; }
  directory_error / / 'unable to read owner UID'
)
(
  stat() { [[ "$2" != '%a' ]] || return 1; command stat "$@"; }
  directory_error / / 'unable to read permission mode'
)
echo 'PASS: directory diagnostics, unchanged permissions and retry after operator repair'

# Only the downloader is replaced. Production checksum, tar, permissions,
# executable validation, locking and rename code all run unchanged.
curl() {
  local destination="" url="" arg
  while (( $# )); do
    arg="$1"; shift
    case "$arg" in
      --output) destination="$1"; shift ;;
      https://*) url="$arg" ;;
    esac
  done
  [[ "$url" == 'https://go.dev/dl/go1.26.8.linux-amd64.tar.gz' ]] || return 1
  printf 'download\n' >>"$fixture/downloads"
  [[ "${download_failure:-0}" == 0 ]] || return 1
  sleep 0.1
  cp "$fixture/archive.tar.gz" "$destination"
}

make_archive() {
  local platform="${1:-linux/amd64}"
  rm -rf -- "$fixture/payload/go"
  mkdir -p "$fixture/payload/go/bin"
  printf '#!/usr/bin/env bash\nprintf "go version go1.26.8 %s\\n"\n' "$platform" >"$fixture/payload/go/bin/go"
  chmod 0755 "$fixture/payload/go/bin/go"
  tar -czf "$fixture/archive.tar.gz" -C "$fixture/payload" go/
  checksum=$(sha256sum "$fixture/archive.tar.gz" | awk '{print $1}')
}

target="$fixture/store/go1.26.8.linux-amd64"
make_archive
download_failure=1
reject lightningos_go_install "$fixture/store" 1.26.8 amd64 "$checksum"
[[ ! -e "$target" ]] || fail 'download failure published toolchain'
download_failure=0
reject lightningos_go_install "$fixture/store" 1.26.8 amd64 "$(printf '%064d' 0)"
[[ ! -e "$target" ]] || fail 'checksum failure published toolchain'
make_archive linux/arm64
reject lightningos_go_install "$fixture/store" 1.26.8 amd64 "$checksum"
[[ ! -e "$target" ]] || fail 'wrong architecture published toolchain'
make_archive
ln -s "$fixture/system-go" "$fixture/payload/go/unsafe"
tar -czf "$fixture/archive.tar.gz" -C "$fixture/payload" go/
checksum=$(sha256sum "$fixture/archive.tar.gz" | awk '{print $1}')
reject lightningos_go_install "$fixture/store" 1.26.8 amd64 "$checksum"
[[ ! -e "$target" ]] || fail 'symlink archive published toolchain'
make_archive
ln -s "$fixture/payload/go" "$target"
reject lightningos_go_install "$fixture/store" 1.26.8 amd64 "$checksum"
rm -- "$target"
ln -s "$fixture/store" "$fixture/linked-store"
reject lightningos_go_install "$fixture/linked-store" 1.26.8 amd64 "$checksum"
chmod 0777 "$fixture/store"
reject lightningos_go_install "$fixture/store" 1.26.8 amd64 "$checksum"
chmod 0755 "$fixture/store"
[[ -z "$(find "$fixture/store" -name '.go-stage.*' -print -quit)" ]] || fail 'failed staging was retained'
echo 'PASS: failed downloads, integrity, architecture and unsafe paths preserve state'

: >"$fixture/downloads"
lightningos_go_install "$fixture/store" 1.26.8 amd64 "$checksum" &
first=$!
lightningos_go_install "$fixture/store" 1.26.8 amd64 "$checksum" &
second=$!
wait "$first" || fail 'first concurrent installer'
wait "$second" || fail 'second concurrent installer'
[[ "$(wc -l <"$fixture/downloads")" == 1 ]] || fail 'concurrent installs downloaded twice'
lightningos_go_check_tree "$target" 1.26.8 amd64 "$checksum"
lightningos_go_install "$fixture/store" 1.26.8 amd64 "$checksum"
[[ "$(wc -l <"$fixture/downloads")" == 1 ]] || fail 'repeat downloaded again'
printf 'invalid\n' >"$target/.lightningos-sha256"
reject lightningos_go_install "$fixture/store" 1.26.8 amd64 "$checksum"
[[ "$(cat "$fixture/system-go")" == 'preserve me' ]] || fail 'unrelated Go changed'
[[ -z "$(find "$fixture/store" -name '.go-stage.*' -print -quit)" ]] || fail 'staging was retained'
echo 'PASS: concurrent preparation, reuse and tampered cache rejection'
