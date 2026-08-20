#!/usr/bin/env bash
set -Eeuo pipefail

chat_user="${1:-lightningos}"
chat_group="${2:-lightningos}"
chat_parent="/var/lib/lightningos"
chat_dir="${chat_parent}/chat"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "[ERROR] Chat storage permission repair must run as root." >&2
  exit 1
fi
if ! id "$chat_user" >/dev/null 2>&1; then
  echo "[ERROR] Chat storage user does not exist: ${chat_user}" >&2
  exit 1
fi
if ! getent group "$chat_group" >/dev/null 2>&1; then
  echo "[ERROR] Chat storage group does not exist: ${chat_group}" >&2
  exit 1
fi
if [[ ! -d "$chat_parent" || -L "$chat_parent" ]]; then
  echo "[ERROR] Chat storage parent is missing or unsafe: ${chat_parent}" >&2
  exit 1
fi
if [[ -e "$chat_dir" && ( ! -d "$chat_dir" || -L "$chat_dir" ) ]]; then
  echo "[ERROR] Chat storage path is not a safe directory: ${chat_dir}" >&2
  exit 1
fi

install -d -o "$chat_user" -g "$chat_group" -m 0750 "$chat_dir"

for name in \
  messages.jsonl \
  messages.jsonl.tmp \
  cursor.txt \
  read-state.json \
  read-state.json.tmp; do
  path="${chat_dir}/${name}"
  [[ -e "$path" ]] || continue
  if [[ -L "$path" || ! -f "$path" ]]; then
    echo "[ERROR] Refusing unsafe Chat storage entry: ${path}" >&2
    exit 1
  fi
  chown --no-dereference "$chat_user:$chat_group" "$path"
  chmod 0640 "$path"
done

echo "[OK] Chat storage permissions are ready for ${chat_user}:${chat_group}."
