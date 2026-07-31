#!/usr/bin/env bash
set -Eeuo pipefail

terminal_enabled="${TERMINAL_ENABLED:-0}"
if [[ "${terminal_enabled:-0}" != "1" ]]; then
  exit 0
fi

terminal_credential="${TERMINAL_CREDENTIAL:-}"
if [[ -z "${terminal_credential:-}" ]]; then
  echo "TERMINAL_CREDENTIAL missing" >&2
  exit 1
fi

port="${TERMINAL_PORT:-7681}"
allow_write="${TERMINAL_ALLOW_WRITE:-0}"
ws_origin="${TERMINAL_WS_ORIGIN:-.*}"
term_name="${TERMINAL_TERM:-xterm}"
terminal_shell="${TERMINAL_SHELL:-/bin/bash}"

export TERM="$term_name"
export SHELL="$terminal_shell"

args=(/usr/local/bin/gotty --address 127.0.0.1 --port "$port" --credential "$terminal_credential" --reconnect)

if [[ "$allow_write" == "1" ]]; then
  args+=(--permit-write)
fi

if /usr/local/bin/gotty --help 2>&1 | grep -q -- '--term'; then
  args+=(--term "$term_name")
fi

if /usr/local/bin/gotty --help 2>&1 | grep -q -- '--ws-origin'; then
  args+=(--ws-origin "$ws_origin")
fi

args+=(tmux new -A -s lightningos "$terminal_shell")

exec "${args[@]}"
