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

private_lan_cidr() {
  local value="$1" first second prefix
  valid_ipv4_cidr "$value" || return 1
  IFS=. read -r first second _ <<<"${value%/*}"
  first=$((10#$first)); second=$((10#$second)); prefix="${value##*/}"; prefix=$((10#$prefix))
  (( (first == 10 && prefix >= 8) ||
     (first == 172 && second >= 16 && second <= 31 && prefix >= 12) ||
     (first == 192 && second == 168 && prefix >= 16) ))
}

default_interface() {
  command -v ip >/dev/null 2>&1 || return 0
  ip -4 route show default 2>/dev/null |
    awk 'NR == 1 { for (i = 1; i <= NF; i++) if ($i == "dev") { print $(i + 1); exit } }'
}

detect_lan_cidr() {
  command -v ip >/dev/null 2>&1 || return 0
  local default_device candidate route device
  default_device="$(default_interface)"
  if [[ -n "$default_device" ]]; then
    while read -r candidate _; do
      if private_lan_cidr "$candidate"; then echo "$candidate"; return 0; fi
    done < <(ip -o -4 route show dev "$default_device" scope link 2>/dev/null)
    return 0
  fi
  # Preserve isolated private LANs without a default route, but never pick an
  # unrelated container/VPN interface as their replacement.
  while read -r candidate route; do
    private_lan_cidr "$candidate" || continue
    device="$(awk '{ for (i=1; i<NF; i++) if ($i=="dev") { print $(i+1); exit } }' <<<"$route")"
    case "$device" in ""|lo|tailscale*|docker*|br-*|veth*) continue ;; esac
    echo "$candidate"; return 0
  done < <(ip -o -4 route show scope link 2>/dev/null)
  return 0
}

choose_lan_cidr() {
  local configured detected selected
  configured="$(read_config_value LAN_CIDR)"
  if [[ -n "$configured" && "${configured,,}" != "none" ]] && ! private_lan_cidr "$configured"; then
    print_warn "Saved LAN is not a private IPv4 network; selecting private LAN or Tailscale-only access."
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
  if ! private_lan_cidr "$selected"; then
    print_warn "LAN must be entirely within 10/8, 172.16/12 or 192.168/16. Use VPN-only access on a VPS."
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
  local mode="$1" lan_cidr="$2" status source line device
  status="$(LC_ALL=C ufw status 2>/dev/null)" || return 1
  grep -qiE '^status:[[:space:]]+active' <<<"$status" || return 1
  while IFS= read -r line; do
    line="${line%%#*}"
    [[ "$line" == *"$MANAGER_PORT"* && "${line,,}" == *allow* ]] || continue
    [[ "$line" == *" on tailscale0 "* ]] && continue
    source="$(awk '{ for (i=1; i<=NF; i++) if (toupper($i)=="ALLOW") { i++; if (toupper($i)=="IN") i++; print $i; exit } }' <<<"$line")"
    [[ "$source" == */* ]] || source="${source}/32"
    if ! private_lan_cidr "$source"; then
      print_warn "Port ${MANAGER_PORT} still has an allow rule outside private LAN/Tailscale."
      return 1
    fi
  done <<<"$status"
  case "$mode" in
    lan)
      grep -Fi "$MANAGER_PORT" <<<"$status" | grep -i 'allow' | grep -Fqi "$lan_cidr" || return 1
      ! grep -Fi "$MANAGER_PORT" <<<"$status" | grep -i 'allow' | grep -qi 'tailscale0'
      ;;
    vpn)
      grep -Fi "$MANAGER_PORT" <<<"$status" | grep -i 'allow' | grep -qi 'tailscale0' || return 1
      device="$(default_interface)"
      grep -Fi "$MANAGER_PORT" <<<"$status" | grep -F " on ${device} " | grep -qi 'deny'
      ;;
    *) return 1 ;;
  esac
}

configure_firewall() {
  local previous previous_mode mode lan_cidr default_device vpn_available=0
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
      private_lan_cidr "$lan_cidr" || { print_warn "LAN CIDR must be entirely private IPv4."; return 2; }
    else
      lan_cidr="$(choose_lan_cidr)" || return 2
    fi
    if [[ "$lan_cidr" == "none" ]]; then
      mode="vpn"
      print_warn "No private LAN selected; restricting Manager access to Tailscale."
    fi
  fi
  if [[ "$mode" == "vpn" ]]; then
    lan_cidr="none"
    if tailscale_is_available; then vpn_available=1; fi
  fi

  # Add the requested access path before removing the previous LightningOS
  # rule. This preserves access if a later command fails.
  if [[ "$mode" == "lan" ]]; then
    ufw allow from "$lan_cidr" to any port "$MANAGER_PORT" proto tcp comment 'LightningOS LAN' || return 5
    ufw allow from "$lan_cidr" to 224.0.0.251 port 5353 proto udp comment 'LightningOS mDNS' || return 5
  else
    if (( vpn_available == 1 )); then
      ufw allow in on tailscale0 to any port "$MANAGER_PORT" proto tcp comment 'LightningOS Tailscale' || return 5
    fi
    default_device="$(default_interface)"
    if [[ -z "$default_device" || "$default_device" == "tailscale0" || "$default_device" == "lo" ]]; then
      print_warn "Cannot identify the uplink to block public Manager access; firewall policy was not verified."
      return 3
    fi
    # Block this port on the physical uplink (IPv4 and IPv6), not on Docker
    # bridges. Prepend so a provider-wide legacy ALLOW cannot override it.
    ufw insert 1 deny in on "$default_device" to any port "$MANAGER_PORT" proto tcp comment 'LightningOS Manager uplink block' || return 5
  fi

  if [[ -n "$previous" && "$previous" != "none" && "$previous" != "$lan_cidr" ]]; then
    ufw --force delete allow from "$previous" to any port "$MANAGER_PORT" proto tcp >/dev/null 2>&1 || return 5
    ufw --force delete allow from "$previous" to 224.0.0.251 port 5353 proto udp >/dev/null 2>&1 || return 5
  fi
  if [[ "$mode" == "lan" ]]; then
    default_device="$(default_interface)"
    if [[ -n "$default_device" && "$default_device" != "tailscale0" && "$default_device" != "lo" ]]; then
      ufw --force delete deny in on "$default_device" to any port "$MANAGER_PORT" proto tcp comment 'LightningOS Manager uplink block' >/dev/null 2>&1 || return 5
    fi
    ufw --force delete allow in on tailscale0 to any port "$MANAGER_PORT" proto tcp >/dev/null 2>&1 || true
  fi
  ufw --force delete allow "${MANAGER_PORT}/tcp" >/dev/null 2>&1 || true

  if [[ "$mode" == "vpn" && "$vpn_available" == "0" ]]; then
    save_config "$mode" "$lan_cidr" 0
    print_warn "Manager port blocked on the uplink, but tailscale0 is unavailable. Configure Tailscale through SSH/console; protection is not verified."
    return 3
  fi
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
