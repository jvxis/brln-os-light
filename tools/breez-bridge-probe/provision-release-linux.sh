#!/usr/bin/env bash
set -euo pipefail

if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  exit 0
fi
if [[ $(id -u) != 0 || $# != 2 || $1 != /* || $2 != /* || ! -x $1 || ! -x $2 ]]; then
  echo 'Expected root and the verified absolute Go and npm paths.' >&2
  exit 1
fi
source_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
export PATH="$(dirname -- "$1"):$(dirname -- "$2"):/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export GOTOOLCHAIN=local
export CI=1

[[ ! -L /opt/lightningos ]]
install -d -o root -g root -m 0755 -- /opt/lightningos
stage=$(mktemp -d /opt/lightningos/treasury-build.XXXXXXXX)
cleanup() {
  if [[ "$stage" == /opt/lightningos/treasury-build.* && -d "$stage" && ! -L "$stage" ]]; then
    rm -rf -- "$stage"
  fi
}
trap cleanup EXIT

if ! command -v gcc >/dev/null; then
  [[ -z "$(dpkg --audit)" ]] || { echo 'Treasury cannot prepare a compiler with pending dpkg changes.' >&2; exit 1; }
  plan="$(LC_ALL=C apt-get --simulate --no-remove install --no-install-recommends build-essential)"
  if grep -Eq '^Remv |^Inst [^ ]+ \[' <<<"$plan"; then
    # The Go bridge uses CGO. A private compiler avoids upgrading host libraries and restarting services.
    echo 'Preparing an isolated Treasury compiler.'
    zig_archive=zig-x86_64-linux-0.17.1.tar.xz
    zig_sha256=3581863e3cbcfd0fce0efc682ce7e9e93da96668a977791d356a26d2a7e9b17a
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
      --connect-timeout 15 --max-time 600 --retry 2 "https://ziglang.org/download/0.17.1/$zig_archive" -o "$stage/$zig_archive"
    printf '%s  %s\n' "$zig_sha256" "$stage/$zig_archive" | sha256sum --strict --check
    tar --no-same-owner --no-same-permissions -xf "$stage/$zig_archive" -C "$stage"
    export CC="$stage/zig-x86_64-linux-0.17.1/zig cc -target x86_64-linux-gnu.2.34"
    export ZIG_GLOBAL_CACHE_DIR="$stage/zig-global" ZIG_LOCAL_CACHE_DIR="$stage/zig-local"
  else
    DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l apt-get install -y --no-remove --no-install-recommends build-essential
  fi
fi
if ! command -v patchelf >/dev/null; then
  # Reuse APT's signed distribution metadata without installing a host package.
  mkdir -- "$stage/dependency"
  (
    cd -- "$stage/dependency"
    apt-get download patchelf
    shopt -s nullglob
    packages=(patchelf_*.deb)
    [[ "${#packages[@]}" == 1 ]]
    dpkg-deb -x -- "${packages[0]}" unpacked
  )
  export PATH="$stage/dependency/unpacked/usr/bin:$PATH"
fi
# A non-interactive installer may run without HOME. Keep the build caches
# inside the private staging directory so Go and npm have writable paths.
GOMODCACHE="$stage/go-mod" GOCACHE="$stage/go-build" npm_config_cache="$stage/npm-cache" \
  bash "$source_dir/package-linux.sh" "$stage/bundle"
bash "$source_dir/install-linux.sh" "$stage/bundle"
