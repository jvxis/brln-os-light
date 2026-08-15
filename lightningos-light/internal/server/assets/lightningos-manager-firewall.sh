#!/usr/bin/env bash
set -Eeuo pipefail

MANAGER_PORT="${LIGHTNINGOS_MANAGER_PORT:-8443}"
CONFIG_PATH="${LIGHTNINGOS_FIREWALL_CONFIG:-/etc/lightningos/manager-firewall.conf}"
INTERACTIVE=0
REQUESTED_MODE=""
REQUESTED_LAN_CIDR=""
ACKNOWLEDGE_UNPROTECTED=0

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
  if [[ -n "$configured" && "${configured,,}" != "none" ]] && ! valid_ipv4_cidr "$configured"; then
    print_warn "Ignoring invalid saved local network '${configured}' and detecting it again."
    configured=""
  fi
  detected="$(detect_lan_cidr)"
  selected="${LIGHTNINGOS_LAN_CIDR:-${configured:-$detected}}"
  if (( INTERACTIVE == 1 )) && [[ -t 0 ]]; then
    # choose_lan_cidr is called through command substitution. Keep guidance on
    # stderr so stdout contains only the selected CIDR.
    echo "LightningOS needs port ${MANAGER_PORT} only for devices on your local network and Tailscale." >&2
    echo "Enter the allowed IPv4 network in CIDR format, or 'none' to allow only Tailscale." >&2
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
  local mode="$1" lan_cidr="$2" acknowledged="${3:-0}" config_dir tmp
  config_dir="$(dirname "$CONFIG_PATH")"
  [[ -d "$config_dir" && ! -L "$config_dir" ]] || return 1
  if [[ -e "$CONFIG_PATH" || -L "$CONFIG_PATH" ]]; then
    [[ -f "$CONFIG_PATH" && ! -L "$CONFIG_PATH" && "$(stat -c '%u' "$CONFIG_PATH")" == "0" ]] || return 1
  fi
  tmp="$(mktemp "${config_dir}/.manager-firewall.conf.XXXXXX")"
  if ! {
    printf 'ACCESS_MODE=%s\n' "$mode"
    printf 'LAN_CIDR=%s\n' "$lan_cidr"
    printf 'ACKNOWLEDGED_UNPROTECTED=%s\n' "$acknowledged"
  } >"$tmp"; then
    rm -f -- "$tmp"
    return 1
  fi
  chown root:root "$tmp" || { rm -f -- "$tmp"; return 1; }
  chmod 0644 "$tmp" || { rm -f -- "$tmp"; return 1; }
  mv -f -- "$tmp" "$CONFIG_PATH" || { rm -f -- "$tmp"; return 1; }
}

ufw_is_active() { LC_ALL=C ufw status 2>/dev/null | grep -qiE '^status:[[:space:]]+active'; }

tailscale_is_available() {
  command -v ip >/dev/null 2>&1 && ip link show tailscale0 >/dev/null 2>&1
}

verify_firewall() {
  local mode="$1" lan_cidr="$2" status
  status="$(LC_ALL=C ufw status 2>/dev/null)" || return 1
  grep -qiE '^status:[[:space:]]+active' <<<"$status" || return 1
  if grep -Fi "$MANAGER_PORT" <<<"$status" | grep -i 'allow' | grep -i 'anywhere' | grep -qiv 'tailscale0'; then
    print_warn "Port ${MANAGER_PORT} still has a broad allow rule."
    return 1
  fi
  case "$mode" in
    lan)
      grep -Fi "$MANAGER_PORT" <<<"$status" | grep -i 'allow' | grep -Fqi "$lan_cidr" || return 1
      ! grep -Fi "$MANAGER_PORT" <<<"$status" | grep -i 'allow' | grep -qi 'tailscale0'
      ;;
    vpn)
      grep -Fi "$MANAGER_PORT" <<<"$status" | grep -i 'allow' | grep -qi 'tailscale0'
      ;;
    *) return 1 ;;
  esac
}

configure_firewall() {
  local previous previous_mode mode lan_cidr
  previous="$(read_config_value LAN_CIDR)"
  previous_mode="$(read_config_value ACCESS_MODE)"
  mode="${REQUESTED_MODE:-$previous_mode}"
  if [[ -z "$mode" ]]; then
    mode="lan"
  fi
  case "$mode" in
    lan|vpn|unprotected) ;;
    *) print_warn "Invalid access mode '${mode}'."; return 2 ;;
  esac

  if [[ "$mode" == "unprotected" ]]; then
    if [[ "$ACKNOWLEDGE_UNPROTECTED" != "1" && "$(read_config_value ACKNOWLEDGED_UNPROTECTED)" != "1" ]]; then
      print_warn "Continuing without verified firewall protection requires explicit acknowledgement."
      return 4
    fi
    lan_cidr="${REQUESTED_LAN_CIDR:-${previous:-none}}"
    save_config "$mode" "$lan_cidr" 1
    print_warn "Manager network exposure is explicitly acknowledged as unprotected."
    return 0
  fi

  if ! command -v ufw >/dev/null 2>&1; then
    print_warn "UFW is not installed; Manager exposure is not protected."
    return 3
  fi
  if ! ufw_is_active; then
    print_warn "UFW is inactive; Manager exposure is not protected."
    return 3
  fi

  if [[ "$mode" == "lan" ]]; then
    if [[ -n "$REQUESTED_LAN_CIDR" ]]; then
      lan_cidr="$REQUESTED_LAN_CIDR"
      valid_ipv4_cidr "$lan_cidr" || { print_warn "Invalid LAN CIDR '${lan_cidr}'."; return 2; }
    else
      lan_cidr="$(choose_lan_cidr)"
    fi
    if [[ "$lan_cidr" == "none" ]]; then
      print_warn "LAN mode requires a valid local IPv4 CIDR."
      return 2
    fi
  else
    lan_cidr="none"
    if ! tailscale_is_available; then
      print_warn "VPN-only mode requires the tailscale0 interface."
      return 3
    fi
  fi

  # Add the requested access path before removing the previous LightningOS
  # rule. This preserves access if a later command fails.
  if [[ "$mode" == "lan" ]]; then
    ufw allow from "$lan_cidr" to any port "$MANAGER_PORT" proto tcp comment 'LightningOS LAN'
    ufw allow from "$lan_cidr" to 224.0.0.251 port 5353 proto udp comment 'LightningOS mDNS'
  else
    ufw allow in on tailscale0 to any port "$MANAGER_PORT" proto tcp comment 'LightningOS Tailscale'
  fi

  if [[ -n "$previous" && "$previous" != "none" && "$previous" != "$lan_cidr" ]]; then
    ufw --force delete allow from "$previous" to any port "$MANAGER_PORT" proto tcp >/dev/null 2>&1 || true
    ufw --force delete allow from "$previous" to 224.0.0.251 port 5353 proto udp >/dev/null 2>&1 || true
  fi
  if [[ "$mode" == "lan" ]]; then
    ufw --force delete allow in on tailscale0 to any port "$MANAGER_PORT" proto tcp >/dev/null 2>&1 || true
  fi
  ufw --force delete allow "${MANAGER_PORT}/tcp" >/dev/null 2>&1 || true

  if ! verify_firewall "$mode" "$lan_cidr"; then
    print_warn "The requested Manager access policy could not be verified."
    return 5
  fi
  save_config "$mode" "$lan_cidr" 0
  print_ok "Manager access mode '${mode}' is active and verified."
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --interactive) INTERACTIVE=1 ;;
    --mode) shift; [[ $# -gt 0 ]] || { echo "Missing --mode value" >&2; exit 2; }; REQUESTED_MODE="$1" ;;
    --lan-cidr) shift; [[ $# -gt 0 ]] || { echo "Missing --lan-cidr value" >&2; exit 2; }; REQUESTED_LAN_CIDR="$1" ;;
    --acknowledge-unprotected) ACKNOWLEDGE_UNPROTECTED=1 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  [[ "$(id -u)" -eq 0 ]] || { echo "This script must run as root." >&2; exit 1; }
  configure_firewall
fi
