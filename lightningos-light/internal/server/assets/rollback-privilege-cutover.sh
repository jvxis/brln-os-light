#!/usr/bin/env bash
set -euo pipefail

STATE_ROOT="/var/lib/lightningos/rollback/0.5.3-privilege-cutover"
CONFIG_PATH="/etc/lightningos/config.yaml"
SERVICE_PATH="/etc/systemd/system/lightningos-manager.service"
DROPIN_PATH="/etc/systemd/system/lightningos-manager.service.d/30-privilege-hardening.conf"
MANAGER_BIN="/opt/lightningos/manager/lightningos-manager"
BROKER_BIN="/usr/local/libexec/lightningos-privileged"
TMPFILES_PATH="/etc/tmpfiles.d/lightningos-privileged.conf"
SOCKET_UNIT="/etc/systemd/system/lightningos-privileged.socket"
BROKER_UNIT="/etc/systemd/system/lightningos-privileged@.service"
AUTH_SUDOERS_PATH="/etc/sudoers.d/lightningos-auth-enable"

if [[ "${EUID}" -ne 0 ]]; then
  echo "Run this rollback as root." >&2
  exit 1
fi
if [[ ! -d "$STATE_ROOT" || -L "$STATE_ROOT" || ! -f "$STATE_ROOT/prepared" || -L "$STATE_ROOT/prepared" || ! -f "$STATE_ROOT/schema-v2" || -L "$STATE_ROOT/schema-v2" ]]; then
  echo "No trusted LightningOS privilege-cutover rollback state is available." >&2
  exit 1
fi
state_owner="$(stat -c '%u:%g:%a' "$STATE_ROOT" 2>/dev/null || true)"
if [[ "$state_owner" != "0:0:700" ]]; then
  echo "LightningOS privilege-cutover rollback state has unsafe ownership or permissions." >&2
  exit 1
fi

restore_file() {
  local backup="$1"
  local target="$2"
  if [[ -f "$backup" && ! -L "$backup" ]]; then
    cp -a --remove-destination -- "$backup" "$target"
  fi
}

restore_or_remove() {
  local name="$1"
  local target="$2"
  if [[ -f "$STATE_ROOT/${name}.existed" && ! -L "$STATE_ROOT/${name}.existed" ]]; then
    restore_file "$STATE_ROOT/$name" "$target"
  else
    rm -f -- "$target"
  fi
}

systemctl stop lightningos-privileged.socket >/dev/null 2>&1 || true

restore_file "$STATE_ROOT/config.yaml" "$CONFIG_PATH"
restore_or_remove "lightningos-manager.service" "$SERVICE_PATH"
restore_or_remove "30-privilege-hardening.conf" "$DROPIN_PATH"
restore_or_remove "lightningos-manager" "$MANAGER_BIN"
restore_or_remove "lightningos-privileged" "$BROKER_BIN"
restore_or_remove "lightningos-privileged.conf" "$TMPFILES_PATH"
restore_or_remove "lightningos-privileged.socket" "$SOCKET_UNIT"
restore_or_remove "lightningos-privileged@.service" "$BROKER_UNIT"
restore_or_remove "auth-enable-sudoers" "$AUTH_SUDOERS_PATH"

if [[ -f "$STATE_ROOT/sudoers.path" && ! -L "$STATE_ROOT/sudoers.path" ]]; then
  sudoers_path="$(<"$STATE_ROOT/sudoers.path")"
  case "$sudoers_path" in
    /etc/sudoers.d/lightningos|/etc/sudoers.d/lightningos-*) ;;
    *) echo "Rollback sudoers target is invalid." >&2; exit 1 ;;
  esac
  if [[ -f "$STATE_ROOT/sudoers.existed" && ! -L "$STATE_ROOT/sudoers.existed" ]]; then
    restore_file "$STATE_ROOT/sudoers" "$sudoers_path"
  else
    rm -f -- "$sudoers_path"
  fi
fi

manager_user="lightningos"
if [[ -f "$STATE_ROOT/manager.user" && ! -L "$STATE_ROOT/manager.user" ]]; then
  manager_user="$(<"$STATE_ROOT/manager.user")"
fi
if [[ ! "$manager_user" =~ ^[a-z_][a-z0-9_-]*[$]?$ ]]; then
  echo "Rollback manager user is invalid." >&2
  exit 1
fi
if [[ -f "$STATE_ROOT/had-docker-group" ]] && getent group docker >/dev/null 2>&1; then
  usermod -a -G docker "$manager_user"
fi

systemctl daemon-reload
if [[ -f "$STATE_ROOT/socket-enabled" && ! -L "$STATE_ROOT/socket-enabled" ]]; then
  systemctl enable lightningos-privileged.socket >/dev/null 2>&1 || true
else
  systemctl disable lightningos-privileged.socket >/dev/null 2>&1 || true
fi
if [[ -f "$STATE_ROOT/socket-active" && ! -L "$STATE_ROOT/socket-active" ]]; then
  systemctl start lightningos-privileged.socket
else
  systemctl stop lightningos-privileged.socket >/dev/null 2>&1 || true
fi
systemctl restart lightningos-manager
systemctl is-active --quiet lightningos-manager
echo "LightningOS privilege cutover rolled back. Bitcoin, LND, and app data were not modified."
