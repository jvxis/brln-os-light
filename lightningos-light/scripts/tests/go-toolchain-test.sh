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
