#!/usr/bin/env bash
set -Eeuo pipefail

SECRETS_PATH="/etc/lightningos/secrets.env"
read_env_value() {
  local key="$1"
  local file="${2:-$SECRETS_PATH}"
  [[ -r "$file" ]] || return 0
  awk -v key="$key" 'index($0, key "=") == 1 { print substr($0, length(key) + 2) }' "$file" | tail -n1
}

terminal_enabled="$(read_env_value TERMINAL_ENABLED)"
if [[ "${terminal_enabled:-0}" != "1" ]]; then
  exit 0
fi

terminal_credential="$(read_env_value TERMINAL_CREDENTIAL)"
if [[ -z "${terminal_credential:-}" ]]; then
  echo "TERMINAL_CREDENTIAL missing" >&2
  exit 1
fi

port="$(read_env_value TERMINAL_PORT)"
port="${port:-7681}"
allow_write="$(read_env_value TERMINAL_ALLOW_WRITE)"
allow_write="${allow_write:-0}"
ws_origin="$(read_env_value TERMINAL_WS_ORIGIN)"
ws_origin="${ws_origin:-.*}"
term_name="$(read_env_value TERMINAL_TERM)"
term_name="${term_name:-xterm}"
terminal_shell="$(read_env_value TERMINAL_SHELL)"
terminal_shell="${terminal_shell:-/bin/bash}"

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
