#!/usr/bin/env bash
set -Eeuo pipefail

terminal_src="$1"
terminal_password_src="$2"
firewall_src="$3"
tls_mdns_src="$4"
marker_path="$5"

terminal_changed=0
if ! cmp -s "$terminal_src" /usr/local/sbin/lightningos-terminal 2>/dev/null; then
  install -m 0755 "$terminal_src" /usr/local/sbin/lightningos-terminal
  terminal_changed=1
fi

install -m 0755 "$firewall_src" /usr/local/sbin/lightningos-manager-firewall
install -m 0755 "$terminal_password_src" /usr/local/sbin/lightningos-terminal-password
install -m 0755 "$tls_mdns_src" /usr/local/sbin/lightningos-setup-manager-tls-mdns

if systemctl show lnd.service >/dev/null 2>&1; then
  dropin_dir="/etc/systemd/system/lnd.service.d"
  mkdir -p "$dropin_dir"
  printf '%s\n' '[Service]' 'Restart=always' 'RestartSec=60' >"$dropin_dir/20-lightningos-restart.conf"
  systemctl daemon-reload
fi

if ! command -v avahi-daemon >/dev/null 2>&1 && command -v apt-get >/dev/null 2>&1; then
  if ! DEBIAN_FRONTEND=noninteractive apt-get update \
    || ! DEBIAN_FRONTEND=noninteractive apt-get install -y avahi-daemon libnss-mdns; then
    echo "[WARN] Could not install Avahi; trusted IP access will remain available" >&2
  fi
fi

manager_user="$(systemctl show -p User --value lightningos-manager 2>/dev/null | tr -d '[:space:]')"
[[ -n "$manager_user" ]] || manager_user="lightningos"
manager_group="$(systemctl show -p Group --value lightningos-manager 2>/dev/null | tr -d '[:space:]')"
if [[ -z "$manager_group" ]] && id "$manager_user" >/dev/null 2>&1; then
  manager_group="$(id -gn "$manager_user")"
fi
[[ -n "$manager_group" ]] || manager_group="lightningos"

cert_path="/etc/lightningos/tls/server.crt"
cert_before="$(sha256sum "$cert_path" 2>/dev/null | awk '{print $1}')"
LIGHTNINGOS_MANAGER_GROUP="$manager_group" \
  LIGHTNINGOS_MANAGER_PORT=8443 \
  /usr/local/sbin/lightningos-setup-manager-tls-mdns
cert_after="$(sha256sum "$cert_path" 2>/dev/null | awk '{print $1}')"

/usr/local/sbin/lightningos-manager-firewall

if (( terminal_changed == 1 )) && systemctl is-active --quiet lightningos-terminal; then
  systemctl restart lightningos-terminal
fi

mkdir -p "$(dirname "$marker_path")"
touch "$marker_path"
chmod 0644 "$marker_path"

if [[ -n "$cert_after" && "$cert_after" != "$cert_before" ]]; then
  systemctl restart --no-block lightningos-manager
fi
