#!/usr/bin/env bash
set -euo pipefail
project=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "${1:-$project/scripts/prepare-node-upgrade.sh}"
LOS_NODE_VERSION=24.22.1
LOS_NPM_VERSION=11.19.1
for installed in 25.9.0-1nodesource1 1:25.9.0-1nodesource1; do
  if output=$(lightningos_node_newer_version_error "$installed" 2>&1); then
    echo 'FAIL: EOL newer Node was accepted'; exit 1
  fi
  [[ "$output" == *'Node.js 25 is end-of-life (EOL)'* ]]
  [[ "$output" == *'24.22.1 LTS / npm 11.19.1'* ]]
  [[ "$output" == *'not proof of security fixes'* ]]
  [[ "$output" == *'No Node/npm packages or app configurations were changed'* ]]
  [[ "$output" == *'Review other Node apps'* ]]
  [[ "$output" == *'refusing automatic downgrade'* ]]
  [[ "$output" != *'[OK]'* ]]
done
for installed in 24.22.2-1nodesource1 26.12.1-1nodesource1 27.0.0-1nodesource1; do
  if output=$(lightningos_node_newer_version_error "$installed" 2>&1); then
    echo 'FAIL: untested newer Node was accepted'; exit 1
  fi
  [[ "$output" == *'newer than this release'* ]]
  [[ "$output" == *'not necessarily insecure or unsupported'* ]]
  [[ "$output" != *'is end-of-life'* ]]
  [[ "$output" == *'refusing automatic downgrade'* ]]
  [[ "$output" == *'No Node/npm packages or app configurations were changed'* ]]
  [[ "$output" != *'[OK]'* ]]
done
echo 'PASS: EOL 25/epoch and newer patch/current/future diagnostics retain failure status'

# Local metadata is advisory only: no apt update, installation or credentials
# from policy output may be included in the diagnostic.
apt-cache() {
  [[ "$*" == 'policy nodejs' && "$LC_ALL" == C ]] || return 90
  printf 'nodejs:\n  Installed: 26.5.1-1nodesource1\n  Candidate: 26.12.1-1nodesource1\n  Version table:\n     https://user:DO_NOT_PRINT@example.invalid/node_26.x\n'
}
apt-get() { echo 'FAIL: diagnostic invoked apt-get'; return 91; }
if output=$(lightningos_node_newer_version_error 26.5.1-1nodesource1 2>&1); then exit 1; fi
[[ "$output" == *'APT candidate (local metadata): 26.12.1-1nodesource1'* ]]
[[ "$output" == *'Node.js 26 can receive security fixes within its own major line'* ]]
[[ "$output" == *'security-patched runtime is not the same as LOS compatibility validation'* ]]
[[ "$output" == *'already-running Node apps keep their previous runtime'* ]]
[[ "$output" != *DO_NOT_PRINT* && "$output" != *'FAIL:'* && "$output" != *'[OK]'* ]]
apt-cache() { printf 'nodejs:\n  Candidate: 25.9.0-1nodesource1\n'; }
if output=$(lightningos_node_newer_version_error 25.9.0-1nodesource1 2>&1); then exit 1; fi
[[ "$output" == *'APT candidate (local metadata): 25.9.0-1nodesource1'* ]]
[[ "$output" == *'An EOL repository may offer no newer candidate'* ]]
[[ "$output" != *'Node.js 26 can receive'* ]]
for scenario in none failed malformed; do
  apt-cache() {
    case "$scenario" in
      none) printf '  Candidate: (none)\n' ;;
      failed) return 100 ;;
      malformed) printf '  Candidate: invalid version DO_NOT_PRINT\n' ;;
    esac
  }
  if output=$(lightningos_node_newer_version_error 26.5.1-1nodesource1 2>&1); then exit 1; fi
  [[ "$output" == *'APT candidate: unavailable'* ]]
  [[ "$output" == *'refusing automatic downgrade'* && "$output" != *DO_NOT_PRINT* ]]
done
unset -f apt-cache apt-get
command() {
  if [[ "$*" == '-v apt-cache' ]]; then return 1; fi
  builtin command "$@"
}
if output=$(lightningos_node_newer_version_error 26.5.1-1nodesource1 2>&1); then exit 1; fi
[[ "$output" == *'APT candidate: unavailable'* && "$output" == *'refusing automatic downgrade'* ]]
unset -f command
output=$(lightningos_node_runtime_notice 2>&1)
[[ "$output" == *'already-running Node apps keep their previous runtime'* ]]
[[ "$output" == *'Plan their restart'* ]]
[[ "$output" == *'does not restart services'* ]]
echo 'PASS: advisory APT candidate, unavailable metadata, Node26 guidance and restart notice'
