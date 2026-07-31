#!/usr/bin/env bash
set -Eeuo pipefail

MANAGER_PORT="${LIGHTNINGOS_MANAGER_PORT:-8443}"
CONFIG_PATH="${LIGHTNINGOS_FIREWALL_CONFIG:-/etc/lightningos/manager-firewall.conf}"
INTERACTIVE=0

print_ok() { echo "[OK] $1"; }
print_warn() { echo "[WARN] $1" >&2; }

read_config_value() {
  local key="$1"
  [[ -r "$CONFIG_PATH" ]] || return 0
  awk -F= -v key="$key" '$1 == key { print substr($0, length(key) + 2) }' "$CONFIG_PATH" | tail -n1
}

valid_ipv4_cidr() {
  local value="$1" address prefix octet
  [[ "$value" == */* ]] || return 1
  address="${value%/*}"
  prefix="${value##*/}"
  [[ "$prefix" =~ ^[0-9]+$ ]] && (( prefix >= 0 && prefix <= 32 )) || return 1
  [[ "$address" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
  IFS=. read -r -a octets <<<"$address"
  for octet in "${octets[@]}"; do
    [[ "$octet" =~ ^[0-9]+$ ]] && (( 10#$octet <= 255 )) || return 1
  done
}

detect_lan_cidr() {
  command -v ip >/dev/null 2>&1 || return 0
  local default_device=""
  default_device="$(ip -4 route show default 2>/dev/null | awk 'NR == 1 { for (i = 1; i <= NF; i++) if ($i == "dev") { print $(i + 1); exit } }')"
  if [[ -n "$default_device" ]]; then
    ip -o -4 route show dev "$default_device" scope link 2>/dev/null |
      awk '$1 ~ /^[0-9]+\./ && $1 ~ /\// { print $1; exit }'
    return 0
  fi
  ip -o -4 route show scope link 2>/dev/null |
    awk '$1 ~ /^(10\.|172\.(1[6-9]|2[0-9]|3[01])\.|192\.168\.)/ && $1 ~ /\// { print $1; exit }'
}

choose_lan_cidr() {
  local configured detected selected
  configured="$(read_config_value LAN_CIDR)"
  detected="$(detect_lan_cidr)"
  selected="${LIGHTNINGOS_LAN_CIDR:-${configured:-$detected}}"
  if (( INTERACTIVE == 1 )) && [[ -t 0 ]]; then
    echo "LightningOS needs port ${MANAGER_PORT} only for devices on your local network and Tailscale."
    echo "Enter the allowed IPv4 network in CIDR format, or 'none' to allow only Tailscale."
    read -r -p "Allowed local network [${selected:-none}]: " reply
    selected="${reply:-${selected:-none}}"
  fi
  if [[ -z "$selected" || "${selected,,}" == "none" ]]; then
    echo "none"
    return 0
  fi
  if ! valid_ipv4_cidr "$selected"; then
    print_warn "Invalid local network '${selected}'. Expected CIDR such as 192.168.1.0/24."
    return 1
  fi
  echo "$selected"
}

save_config() {
  local lan_cidr="$1"
  mkdir -p "$(dirname "$CONFIG_PATH")"
  printf 'LAN_CIDR=%s\n' "$lan_cidr" >"$CONFIG_PATH"
  chmod 0644 "$CONFIG_PATH"
}

ufw_is_active() { LC_ALL=C ufw status 2>/dev/null | grep -qiE '^status:[[:space:]]+active'; }

configure_firewall() {
  local previous lan_cidr tailscale_available=0
  previous="$(read_config_value LAN_CIDR)"
  lan_cidr="$(choose_lan_cidr)"
  save_config "$lan_cidr"
  if ! command -v ufw >/dev/null 2>&1; then
    print_warn "UFW is not installed; saved the selected network but did not change firewall rules."
    return 0
  fi
  if ! ufw_is_active; then
    print_warn "UFW is inactive; saved the selected network but did not change firewall rules."
    return 0
  fi
  if command -v ip >/dev/null 2>&1 && ip link show tailscale0 >/dev/null 2>&1; then
    tailscale_available=1
  fi
  if [[ "$lan_cidr" == "none" && "$tailscale_available" == "0" && "$INTERACTIVE" == "0" ]]; then
    print_warn "No LAN or Tailscale network could be detected; keeping existing firewall rules to avoid lockout."
    return 0
  fi
  if [[ -n "$previous" && "$previous" != "none" ]]; then
    ufw --force delete allow from "$previous" to any port "$MANAGER_PORT" proto tcp >/dev/null 2>&1 || true
  fi
  ufw --force delete allow "${MANAGER_PORT}/tcp" >/dev/null 2>&1 || true
  if [[ "$lan_cidr" != "none" ]]; then
    ufw allow from "$lan_cidr" to any port "$MANAGER_PORT" proto tcp comment 'LightningOS LAN'
    print_ok "Port ${MANAGER_PORT} allowed only from ${lan_cidr}."
  fi
  if [[ "$tailscale_available" == "1" ]]; then
    ufw allow in on tailscale0 to any port "$MANAGER_PORT" proto tcp comment 'LightningOS Tailscale'
    print_ok "Port ${MANAGER_PORT} allowed through tailscale0."
  elif [[ "$lan_cidr" == "none" ]]; then
    print_warn "No LAN rule was added and tailscale0 is not available; remote access may be blocked."
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --interactive) INTERACTIVE=1 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  [[ "$(id -u)" -eq 0 ]] || { echo "This script must run as root." >&2; exit 1; }
  configure_firewall
fi
