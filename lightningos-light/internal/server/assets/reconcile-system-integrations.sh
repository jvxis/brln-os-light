#!/usr/bin/env bash
set -Eeuo pipefail

terminal_src="$1"
terminal_password_src="$2"
firewall_src="$3"
marker_path="$4"

terminal_changed=0
if ! cmp -s "$terminal_src" /usr/local/sbin/lightningos-terminal 2>/dev/null; then
  install -m 0755 "$terminal_src" /usr/local/sbin/lightningos-terminal
  terminal_changed=1
fi

install -m 0755 "$firewall_src" /usr/local/sbin/lightningos-manager-firewall
install -m 0755 "$terminal_password_src" /usr/local/sbin/lightningos-terminal-password

if systemctl show lnd.service >/dev/null 2>&1; then
  dropin_dir="/etc/systemd/system/lnd.service.d"
  mkdir -p "$dropin_dir"
  printf '%s\n' '[Service]' 'Restart=always' 'RestartSec=60' >"$dropin_dir/20-lightningos-restart.conf"
  systemctl daemon-reload
fi

/usr/local/sbin/lightningos-manager-firewall

if (( terminal_changed == 1 )) && systemctl is-active --quiet lightningos-terminal; then
  systemctl restart lightningos-terminal
fi

mkdir -p "$(dirname "$marker_path")"
touch "$marker_path"
chmod 0644 "$marker_path"
