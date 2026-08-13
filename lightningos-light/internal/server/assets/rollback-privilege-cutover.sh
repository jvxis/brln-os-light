#!/usr/bin/env bash
set -euo pipefail

STATE_ROOT="/var/lib/lightningos/rollback/0.5.3-privilege-cutover"
CONFIG_PATH="/etc/lightningos/config.yaml"
SERVICE_PATH="/etc/systemd/system/lightningos-manager.service"
DROPIN_PATH="/etc/systemd/system/lightningos-manager.service.d/30-privilege-hardening.conf"

if [[ "${EUID}" -ne 0 ]]; then
  echo "Run this rollback as root." >&2
  exit 1
fi
if [[ ! -d "$STATE_ROOT" || -L "$STATE_ROOT" || ! -f "$STATE_ROOT/prepared" || -L "$STATE_ROOT/prepared" ]]; then
  echo "No trusted LightningOS privilege-cutover rollback state is available." >&2
  exit 1
fi

restore_file() {
  local backup="$1"
  local target="$2"
  if [[ -f "$backup" && ! -L "$backup" ]]; then
    cp -a --remove-destination -- "$backup" "$target"
  fi
}

restore_file "$STATE_ROOT/config.yaml" "$CONFIG_PATH"
restore_file "$STATE_ROOT/lightningos-manager.service" "$SERVICE_PATH"

if [[ -f "$STATE_ROOT/dropin.existed" ]]; then
  restore_file "$STATE_ROOT/30-privilege-hardening.conf" "$DROPIN_PATH"
else
  rm -f -- "$DROPIN_PATH"
fi

if [[ -f "$STATE_ROOT/sudoers.path" && ! -L "$STATE_ROOT/sudoers.path" ]]; then
  sudoers_path="$(<"$STATE_ROOT/sudoers.path")"
  case "$sudoers_path" in
    /etc/sudoers.d/lightningos|/etc/sudoers.d/lightningos-*) ;;
    *) echo "Rollback sudoers target is invalid." >&2; exit 1 ;;
  esac
  if [[ -f "$STATE_ROOT/sudoers.existed" ]]; then
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
systemctl restart lightningos-manager
systemctl is-active --quiet lightningos-manager
echo "LightningOS privilege cutover rolled back. Bitcoin, LND, and app data were not modified."
