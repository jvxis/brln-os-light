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
