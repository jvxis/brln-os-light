#!/usr/bin/env bash
set -Eeuo pipefail

password_file="${1:-}"
operator_user="${2:-}"

cleanup() {
  [[ -n "$password_file" ]] && rm -f -- "$password_file"
}
trap cleanup EXIT

if [[ ! "$password_file" =~ ^/var/lib/lightningos/terminal-password-[A-Za-z0-9._-]+$ ]]; then
  echo "invalid terminal password staging path" >&2
  exit 2
fi
if [[ ! "$operator_user" =~ ^[a-z_][a-z0-9_-]{0,31}$ ]]; then
  echo "invalid terminal operator user" >&2
  exit 2
fi
if [[ ! -f "$password_file" || -L "$password_file" ]]; then
  echo "terminal password staging file is invalid" >&2
  exit 2
fi
if ! id "$operator_user" >/dev/null 2>&1; then
  echo "terminal operator user does not exist" >&2
  exit 2
fi

IFS= read -r password <"$password_file"
if [[ ! "$password" =~ ^[A-Za-z0-9]{16,128}$ ]]; then
  echo "terminal password format is invalid" >&2
  exit 2
fi

printf '%s:%s\n' "$operator_user" "$password" | /usr/sbin/chpasswd
