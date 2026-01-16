#!/usr/bin/env bash
set -Eeuo pipefail

SECRETS_PATH="/etc/lightningos/secrets.env"
if [[ -f "$SECRETS_PATH" ]]; then
  # shellcheck disable=SC1091
  source "$SECRETS_PATH"
fi

if [[ "${TERMINAL_ENABLED:-0}" != "1" ]]; then
  exit 0
fi

if [[ -z "${TERMINAL_CREDENTIAL:-}" ]]; then
  echo "TERMINAL_CREDENTIAL missing" >&2
  exit 1
fi

port="${TERMINAL_PORT:-7681}"
ws_origin="${TERMINAL_WS_ORIGIN:-.*}"
term_name="${TERMINAL_TERM:-xterm}"

export TERM="$term_name"

args=(/usr/local/bin/gotty --address 127.0.0.1 --port "$port" --credential "$TERMINAL_CREDENTIAL" --reconnect)

if [[ "${TERMINAL_ALLOW_WRITE:-0}" == "1" ]]; then
  args+=(--permit-write)
fi

if /usr/local/bin/gotty --help 2>&1 | grep -q -- '--term'; then
  args+=(--term "$term_name")
fi

if /usr/local/bin/gotty --help 2>&1 | grep -q -- '--ws-origin'; then
  args+=(--ws-origin "$ws_origin")
fi

args+=(tmux new -A -s lightningos)

exec "${args[@]}"
