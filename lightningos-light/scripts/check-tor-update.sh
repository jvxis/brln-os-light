#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ " $* " != *" --verify-only "* && " $* " != *" --help "* && " $* " != *" -h "* ]]; then
  bash "$SCRIPT_DIR/install-lnd-onion-recovery.sh"
fi
exec bash "$SCRIPT_DIR/../internal/server/assets/check-tor-update.sh" "$@"
