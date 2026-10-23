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
