#!/usr/bin/env bash
set -Eeuo pipefail
project=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "${1:-$project/scripts/prepare-node-upgrade.sh}"
# Real Debian version comparisons; fixture only the executable responses.
env() {
  case "${*: -2}" in
    '/usr/bin/node --version') [[ ${node_failure:-0} == 0 ]] || return 1; printf '%s\n' "$fixture_node" ;;
    *) command env "$@" ;;
  esac
}
lightningos_node_npm() { [[ ${npm_failure:-0} == 0 ]] || return 1; printf '%s\n' "$fixture_npm"; }
for fixture_node in v22.12.0 v22.23.4 v24.19.0 v24.22.1 v24.23.0 v25.9.0 v26.5.0 v26.12.1 v27.0.0; do
  for fixture_npm in 10.0.0 11.17.0 11.19.1 11.20.0 12.0.0; do
    lightningos_node_check_compatible || { echo "FAIL compatible $fixture_node/$fixture_npm refused"; exit 1; }
    [[ $LOS_NODE_ACTUAL_VERSION == "${fixture_node#v}" && $LOS_NPM_ACTUAL_VERSION == "$fixture_npm" ]]
  done
done
for fixture_node in v18.20.0 v20.19.0 v22.11.0 v26.12.1-rc.1 garbage 'v26.12.1 injected'; do
  fixture_npm=11.19.1
  if lightningos_node_check_compatible; then echo "FAIL incompatible $fixture_node accepted"; exit 1; fi
done
fixture_node=v26.12.1
for fixture_npm in 9.9.0 invalid 11.1.0-beta; do
  if lightningos_node_check_compatible; then echo "FAIL incompatible npm $fixture_npm accepted"; exit 1; fi
done
fixture_npm=11.19.1
node_failure=1
if lightningos_node_check_compatible; then echo 'FAIL broken node accepted'; exit 1; fi
node_failure=0 npm_failure=1
if lightningos_node_check_compatible; then echo 'FAIL broken npm accepted'; exit 1; fi
npm_failure=0
for version in 22.12.0 24.19.0 25.9.0 26.5.0 27.0.0; do
  LOS_NODE_ACTUAL_VERSION=$version
  output=$(lightningos_node_security_notice 2>&1)
  [[ $output == *'[WARN]'* ]] || { echo 'FAIL security warning missing'; exit 1; }
done
for version in 22.23.4 24.22.1 26.12.1; do
  LOS_NODE_ACTUAL_VERSION=$version
  [[ -z $(lightningos_node_security_notice 2>&1) ]] || { echo 'FAIL patched baseline falsely warned'; exit 1; }
done
# Exercise full preparation: compatible runtime must never reach package install,
# regardless of Debian package version. Safety checks are covered separately.
lightningos_node_check_shadowing() { :; }
lightningos_node_check_npm_config() { :; }
lightningos_node_install() { echo 'FAIL unexpected package installation' >&2; return 99; }
fixture_node=v26.12.1 fixture_npm=11.20.0
if [[ $EUID == 0 ]]; then
  lightningos_prepare_node
  [[ $NODE_BIN == /usr/bin/node && $NPM_BIN == /usr/bin/npm ]]
else
  echo 'SKIP: full root preparation; version compatibility and warning fixtures ran above'
fi
echo 'PASS: version range, future patches, EOL/security warnings, malformed/broken runtimes and no package mutation for compatible runtime'
