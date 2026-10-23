#!/usr/bin/env bash
set -Eeuo pipefail
project=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$project/scripts/prepare-node-upgrade.sh"
fixture=$(mktemp -d)
trap '[[ "$fixture" == /tmp/tmp.* && -d "$fixture" && ! -L "$fixture" ]] && rm -rf -- "$fixture"' EXIT
printf 'Remv npm [9.0]\nRemv libnode-dev [18.0]\nInst nodejs [18.0] (24.22.1 localhost [amd64])\n' > "$fixture/allowed"
lightningos_node_validate_plan "$fixture/allowed"
for package in lnd bitcoind thunderhub balanceofsatoshis openssl libc6; do
  printf 'Remv %s [1.0]\n' "$package" > "$fixture/rejected"
  if lightningos_node_validate_plan "$fixture/rejected"; then echo "FAIL: removal $package allowed"; exit 1; fi
  printf 'Inst %s [1.0] (2.0 localhost [amd64])\n' "$package" > "$fixture/rejected"
  if lightningos_node_validate_plan "$fixture/rejected"; then echo "FAIL: change $package allowed"; exit 1; fi
  printf 'Conf %s (2.0 localhost [amd64])\n' "$package" > "$fixture/rejected"
  if lightningos_node_validate_plan "$fixture/rejected"; then echo "FAIL: pending configuration $package allowed"; exit 1; fi
done
dpkg-query() {
  printf 'npm\tinstalled\tnode-loader-alias, libnode-dev, libc6\nnode-css-loader\tinstalled\tnode-gyp | node-fallback\tnode-loader-alias\nnode-gyp\tinstalled\tlibnode-dev\nlibnode-dev\tinstalled\tlibnode109:any\nlibnode109:amd64\tinstalled\tlibc6\nlibc6\tinstalled\t\nwebpack\tinstalled\t\n'
  printf 'node-fallback\tinstalled\twebpack\nnode-external\tinstalled\t\nlnd\tinstalled\t\n'
}
apt-mark() { printf 'node-css-loader\nnode-gyp\nlibnode109\nlibc6\nnode-external\nlnd\n'; }
lightningos_node_packaging_allowlist "$fixture/automatic-stack"
for package in node-css-loader node-gyp libnode109; do
  grep -Fxq "$package" "$fixture/automatic-stack"
  printf 'Remv %s [1.0]\n' "$package" > "$fixture/allowed-auto"
  lightningos_node_validate_plan "$fixture/allowed-auto" "$fixture/automatic-stack"
done
for package in webpack node-fallback node-external libc6 lnd; do
  if grep -Fxq "$package" "$fixture/automatic-stack"; then echo "FAIL: unrelated/manual $package classified as npm packaging"; exit 1; fi
  printf 'Remv %s [1.0]\n' "$package" > "$fixture/rejected-auto"
  if lightningos_node_validate_plan "$fixture/rejected-auto" "$fixture/automatic-stack"; then echo "FAIL: unrelated/manual $package removal accepted"; exit 1; fi
done
dpkg-query() { printf 'libc6\tinstalled\t\n'; }
apt-mark() { echo libc6; }
lightningos_node_packaging_allowlist "$fixture/fresh-stack"
[[ ! -s "$fixture/fresh-stack" ]] || { echo 'FAIL: clean install classified unrelated packages'; exit 1; }
apt-mark() { return 1; }
if lightningos_node_packaging_allowlist "$fixture/missing-metadata"; then echo 'FAIL: missing APT automatic markers ignored'; exit 1; fi
unset -f dpkg-query apt-mark
for prefix in '' /usr /usr/local; do lightningos_node_validate_prefix "$prefix"; done
for prefix in /tmp/npm /home/admin/.npm relative /usr/local/../bin; do
  if lightningos_node_validate_prefix "$prefix"; then echo 'FAIL: custom or unsafe npm prefix accepted'; exit 1; fi
done
if lightningos_node_safe_directory relative; then echo 'FAIL: relative path accepted'; exit 1; fi
if lightningos_node_safe_directory /tmp; then echo 'FAIL: writable ancestor accepted'; exit 1; fi
dpkg() { [[ "$1" == --print-architecture ]] && echo amd64; }
lightningos_node_policy
[[ "$LOS_NODE_ARCH" == amd64 && "$LOS_NODE_SHA256" == 2f66fa3df76eb8c88de752e7f9f00fba5fa9e69707bda3345b4dac720a7639cb ]]
dpkg() { [[ "$1" == --print-architecture ]] && echo arm64; }
lightningos_node_policy
[[ "$LOS_NODE_ARCH" == arm64 && "$LOS_NODE_SHA256" == 1c31bf34801ccea3731b587a48c3f6a52ad9592d536f4af0f672d6c445570435 ]]
dpkg() { [[ "$1" == --print-architecture ]] && echo riscv64; }
if lightningos_node_policy; then echo 'FAIL: unsupported architecture accepted'; exit 1; fi
echo 'PASS: allowed packaging transition, unrelated removal/change rejection, unsafe paths, architecture/checksum policy'
if [[ "$EUID" == 0 && -d /usr/etc && ! -L /usr/etc ]] && command -v unshare >/dev/null && unshare --mount true 2>/dev/null; then
  # Never mutate the host npm config: mask its directory in a private mount
  # namespace. Mount teardown also owns all fixture links/files below it.
  unshare --mount --propagation private bash -s -- "$project/scripts/prepare-node-upgrade.sh" <<'SH'
set -euo pipefail
source "$1"
mount -t tmpfs -o size=1m,mode=0755 tmpfs /usr/etc
printf 'fixture\n' > /usr/etc/target
ln -s /usr/etc/target /usr/etc/npmrc
if lightningos_node_check_npm_config; then echo 'FAIL: npmrc symlink accepted'; exit 1; fi
[[ -L /usr/etc/npmrc ]] && rm -- /usr/etc/npmrc
printf 'prefix=/usr/local\n' > /usr/etc/npmrc
chmod 0666 /usr/etc/npmrc
if lightningos_node_check_npm_config; then echo 'FAIL: writable npmrc accepted'; exit 1; fi
[[ "$(stat -c '%a' /usr/etc/npmrc)" == 666 ]] || { echo 'FAIL: silently repaired npmrc'; exit 1; }
chmod 0644 /usr/etc/npmrc
lightningos_node_check_npm_config
ln /usr/etc/npmrc /usr/etc/npmrc-alias
if lightningos_node_check_npm_config; then echo 'FAIL: hardlinked npmrc accepted'; exit 1; fi
rm -- /usr/etc/npmrc-alias
lightningos_node_npm() {
  local config="${1#--globalconfig=}"
  [[ "$(stat -c '%a' "$(dirname "$config")")" == 700 ]] || { echo 'FAIL: npm writes outside private staging'; return 1; }
  printf 'prefix=/usr/local\n' > "$config"
  chmod 0666 "$config"
}
lightningos_node_prepare_npm_prefix /usr/local
[[ "$(stat -c '%u:%g:%a:%h' /usr/etc/npmrc)" == 0:0:644:1 ]]
[[ "$(cat /usr/etc/npmrc)" == prefix=/usr/local ]]
lightningos_node_prepare_npm_prefix /usr/local
echo 'PASS: global npm config symlinks, write permissions and hardlinks checked in private namespace'
echo 'PASS: npm-created writable config staged privately and published safely, including repeat'
SH
else
  echo 'SKIP: npmrc namespace fixtures require root, an existing /usr/etc directory and mount capability'
fi
