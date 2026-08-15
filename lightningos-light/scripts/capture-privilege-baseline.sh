#!/usr/bin/env bash
set -euo pipefail

# Read-only Phase 0 baseline collector. Run as root on a LightningOS node.
# Output is sectioned JSON: one "<section>\t<json>" record per line.
# It deliberately excludes hostnames, addresses, node identity, credentials,
# configuration contents, logs, environment variables, and macaroon contents.

if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  echo "capture-privilege-baseline.sh must run as root" >&2
  exit 1
fi

for command in jq systemctl stat sha256sum timeout; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "required command not found: $command" >&2
    exit 1
  fi
done

emit() {
  printf '%s\t%s\n' "$1" "$2"
}

redact_addresses() {
  sed -E 's/(^|[^0-9])([0-9]{1,3}\.){3}[0-9]{1,3}([^0-9]|$)/\1<redacted-ipv4>\3/g'
}

captured_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
os_pretty=$(awk -F= '$1=="PRETTY_NAME" {gsub(/^"|"$/, "", $2); print $2}' /etc/os-release 2>/dev/null || true)
arch=$(dpkg --print-architecture 2>/dev/null || uname -m)
kernel=$(uname -r)
version=$(tr -d '\r\n' </opt/lightningos/ui/version.txt 2>/dev/null || true)
stamp=$(tr -d '\r\n' </opt/lightningos/manager/.build_stamp 2>/dev/null || true)
commit=$(printf '%s' "$stamp" | grep -Eo '^[0-9a-f]{40}' || true)
if [[ -f /var/log/lightningos-install-existing.log ]]; then
  install_type=install_existing
elif [[ -f /var/log/lightningos-install.log ]]; then
  install_type=install
else
  install_type=unknown
fi
health_code=$(curl -k -sS --max-time 10 -o /dev/null -w '%{http_code}' https://127.0.0.1:8443/api/health 2>/dev/null || printf '000')
meta=$(jq -cn \
  --arg captured_at "$captured_at" \
  --arg version "$version" \
  --arg commit "$commit" \
  --arg install_type "$install_type" \
  --arg os "$os_pretty" \
  --arg arch "$arch" \
  --arg kernel "$kernel" \
  --arg health_code "$health_code" \
  '{captured_at:$captured_at,lightningos_version:$version,deployed_commit:$commit,install_type:$install_type,os:$os,architecture:$arch,kernel:$kernel,manager_health_http_code:$health_code}')
emit meta "$meta"

active_units=$(systemctl list-units --state=active --no-legend --plain 2>/dev/null \
  | awk '$1 ~ /\.(service|timer|socket|target|path)$/ && $1 !~ /^systemd-fsck@dev-disk-by/ {print $1}' \
  | jq -Rsc 'split("\n") | map(select(length>0)) | sort')
emit active_units "$active_units"

service_json='[]'
services=(
  lightningos-manager lnd lightningos-terminal lightningos-reports.timer
  docker postgresql ufw tailscaled bitcoin bitcoind elementsd tor tor@default
)
for unit in "${services[@]}"; do
  active=$(systemctl is-active "$unit" 2>/dev/null || true)
  enabled=$(systemctl is-enabled "$unit" 2>/dev/null || true)
  service_json=$(jq -cn \
    --argjson current "$service_json" \
    --arg unit "$unit" \
    --arg active "$active" \
    --arg enabled "$enabled" \
    '$current + [{unit:$unit,active:$active,enabled:$enabled}]')
done
emit services "$service_json"

manager_groups=$(id -nG lightningos 2>/dev/null \
  | tr ' ' '\n' \
  | jq -Rsc 'split("\n") | map(select(length>0)) | sort')
emit manager_groups "$manager_groups"

manager_sudo_commands=$(sudo -n -l -U lightningos 2>/dev/null \
  | sed -n -E 's/^[[:space:]]*\([^)]*\)[[:space:]]*NOPASSWD:[[:space:]]*/NOPASSWD: /p' \
  | redact_addresses \
  | jq -Rsc 'split("\n") | map(select(length>0))')
emit manager_sudo_commands "$manager_sudo_commands"

ufw_first=$(ufw status 2>/dev/null | sed -n '1p' | tr '[:upper:]' '[:lower:]' || true)
case "$ufw_first" in
  *inactive*) ufw_state=inactive ;;
  *active*) ufw_state=active ;;
  *) ufw_state=unknown ;;
esac
ufw_count=$(ufw status numbered 2>/dev/null | grep -Ec '^\[[[:space:]]*[0-9]+\]' || true)
ufw_hash=$(ufw status numbered 2>/dev/null | sha256sum | awk '{print $1}')
firewall=$(jq -cn \
  --arg state "$ufw_state" \
  --argjson rule_count "${ufw_count:-0}" \
  --arg rules_sha256 "$ufw_hash" \
  '{state:$state,rule_count:$rule_count,rules_sha256:$rules_sha256}')
emit firewall "$firewall"

if command -v docker >/dev/null 2>&1 && timeout 10s docker info >/dev/null 2>&1; then
  containers=$(docker ps -a --format '{{json .}}' \
    | redact_addresses \
    | jq -cs 'map({name:.Names,image:.Image,state:.State,status:.Status}) | sort_by(.name)')
  images=$(docker image ls --format '{{json .}}' \
    | redact_addresses \
    | jq -cs 'map({repository:.Repository,tag:.Tag,id:.ID,size:.Size}) | sort_by(.repository,.tag)')
  networks=$(docker network ls --format '{{json .}}' \
    | redact_addresses \
    | jq -cs 'map({name:.Name,driver:.Driver,scope:.Scope}) | sort_by(.name)')
  volumes=$(docker volume ls --format '{{json .}}' \
    | redact_addresses \
    | jq -cs 'map({name:.Name,driver:.Driver}) | sort_by(.name)')
  docker_available=true
else
  containers='[]'
  images='[]'
  networks='[]'
  volumes='[]'
  docker_available=false
fi
docker_json=$(jq -cn \
  --argjson available "$docker_available" \
  --argjson containers "$containers" \
  --argjson images "$images" \
  --argjson networks "$networks" \
  --argjson volumes "$volumes" \
  '{available:$available,containers:$containers,images:$images,networks:$networks,volumes:$volumes}')
emit docker "$docker_json"

lncli=''
for candidate in /usr/local/bin/lncli /usr/bin/lncli; do
  if [[ -x "$candidate" ]]; then
    lncli="$candidate"
    break
  fi
done
if [[ -n "$lncli" ]]; then
  info_raw=$(timeout 15s sudo -n -u lnd "$lncli" getinfo 2>/dev/null || true)
  wallet_raw=$(timeout 15s sudo -n -u lnd "$lncli" walletbalance 2>/dev/null || true)
  channel_raw=$(timeout 15s sudo -n -u lnd "$lncli" channelbalance 2>/dev/null || true)
  list_raw=$(timeout 15s sudo -n -u lnd "$lncli" listchannels 2>/dev/null || true)
else
  info_raw=''
  wallet_raw=''
  channel_raw=''
  list_raw=''
fi
if printf '%s' "$info_raw" | jq -e . >/dev/null 2>&1; then
  info=$(printf '%s' "$info_raw" | jq -c '{available:true,version:(.version // ""),synced_to_chain:(.synced_to_chain // false),synced_to_graph:(.synced_to_graph // false),block_height:(.block_height // 0),num_active_channels:(.num_active_channels // 0),num_inactive_channels:(.num_inactive_channels // 0),num_pending_channels:(.num_pending_channels // 0),num_peers:(.num_peers // 0)}')
else
  info='{"available":false}'
fi
if printf '%s' "$wallet_raw" | jq -e . >/dev/null 2>&1; then
  wallet=$(printf '%s' "$wallet_raw" | jq -c '{available:true,total_balance:(.total_balance // "0"),confirmed_balance:(.confirmed_balance // "0"),unconfirmed_balance:(.unconfirmed_balance // "0"),locked_balance:(.locked_balance // "0")}')
else
  wallet='{"available":false}'
fi
if printf '%s' "$channel_raw" | jq -e . >/dev/null 2>&1; then
  channel_balance=$(printf '%s' "$channel_raw" | jq -c '{available:true,balance:(.balance // "0"),pending_open_balance:(.pending_open_balance // "0"),local_balance:(.local_balance // {}),remote_balance:(.remote_balance // {}),unsettled_local_balance:(.unsettled_local_balance // {}),unsettled_remote_balance:(.unsettled_remote_balance // {})}')
else
  channel_balance='{"available":false}'
fi
if printf '%s' "$list_raw" | jq -e . >/dev/null 2>&1; then
  channels=$(printf '%s' "$list_raw" | jq -c '{available:true,count:(.channels|length),active_count:([.channels[]|select(.active==true)]|length),private_count:([.channels[]|select(.private==true)]|length),capacity_sat:([.channels[].capacity|tonumber]|add//0),local_balance_sat:([.channels[].local_balance|tonumber]|add//0),remote_balance_sat:([.channels[].remote_balance|tonumber]|add//0)}')
else
  channels='{"available":false}'
fi
lnd=$(jq -cn \
  --argjson info "$info" \
  --argjson wallet "$wallet" \
  --argjson channel_balance "$channel_balance" \
  --argjson channels "$channels" \
  '{info:$info,wallet:$wallet,channel_balance:$channel_balance,channels:$channels}')
emit lnd "$lnd"

bitcoin='{"available":false,"source":"unavailable"}'
if command -v bitcoin-cli >/dev/null 2>&1 && id bitcoin >/dev/null 2>&1; then
  raw=$(timeout 10s sudo -n -u bitcoin bitcoin-cli getblockchaininfo 2>/dev/null || true)
  if printf '%s' "$raw" | jq -e . >/dev/null 2>&1; then
    bitcoin=$(printf '%s' "$raw" | jq -c '{available:true,source:"native",chain:(.chain//""),blocks:(.blocks//0),headers:(.headers//0),verification_progress:(.verificationprogress//0),initial_block_download:(.initialblockdownload//false)}')
  fi
fi
if [[ $(printf '%s' "$bitcoin" | jq -r .available) != true ]] && command -v docker >/dev/null 2>&1; then
  while IFS= read -r container_id; do
    [[ -n "$container_id" ]] || continue
    raw=$(timeout 10s docker exec "$container_id" bitcoin-cli -datadir=/home/bitcoin/.bitcoin getblockchaininfo 2>/dev/null || true)
    if ! printf '%s' "$raw" | jq -e . >/dev/null 2>&1; then
      raw=$(timeout 10s docker exec "$container_id" bitcoin-cli getblockchaininfo 2>/dev/null || true)
    fi
    if printf '%s' "$raw" | jq -e . >/dev/null 2>&1; then
      bitcoin=$(printf '%s' "$raw" | jq -c '{available:true,source:"docker",chain:(.chain//""),blocks:(.blocks//0),headers:(.headers//0),verification_progress:(.verificationprogress//0),initial_block_download:(.initialblockdownload//false)}')
      break
    fi
  done < <(docker ps --format '{{.ID}} {{.Image}} {{.Names}}' 2>/dev/null | awk 'tolower($0) ~ /bitcoin/ {print $1}')
fi
if [[ $(printf '%s' "$bitcoin" | jq -r .available) != true ]] && printf '%s' "$info_raw" | jq -e . >/dev/null 2>&1; then
  bitcoin=$(printf '%s' "$info_raw" | jq -c '{available:true,source:"lnd_getinfo",chain:"bitcoin-mainnet",blocks:(.block_height//0),headers:(.block_height//0),verification_progress:(if .synced_to_chain then 1 else 0 end),initial_block_download:(.synced_to_chain|not)}')
fi
emit bitcoin "$bitcoin"

managed_paths=(
  /opt/lightningos/manager
  /opt/lightningos/manager/lightningos-manager
  /opt/lightningos/manager/.build_stamp
  /opt/lightningos/ui
  /opt/lightningos/ui/version.txt
  /etc/lightningos
  /etc/lightningos/config.yaml
  /etc/lightningos/secrets.env
  /etc/lightningos/tls
  /etc/lightningos/tls/server.crt
  /etc/lightningos/tls/server.key
  /etc/lightningos/tls/local-ca.crt
  /etc/lightningos/tls/local-ca.key
  /etc/lightningos/tls/access.env
  /etc/sudoers.d/lightningos
  /etc/sudoers.d/lightningos-auth-enable
  /etc/systemd/system/lightningos-manager.service
  /etc/systemd/system/lnd.service
  /etc/systemd/system/lightningos-terminal.service
  /etc/systemd/system/lightningos-reports.service
  /etc/systemd/system/lightningos-reports.timer
  /usr/local/sbin/lightningos-fix-lnd-perms
  /usr/local/sbin/lightningos-upgrade-lnd
  /usr/local/sbin/lightningos-upgrade-app
  /usr/local/sbin/lightningos-terminal
  /usr/local/sbin/lightningos-terminal-password
  /usr/local/sbin/lightningos-manager-firewall
  /data/lnd
  /home/lnd/.lnd
  /data/lnd/tls.cert
  /data/lnd/data/chain/bitcoin/mainnet/admin.macaroon
  /var/lib/lightningos
  /var/lib/lightningos/apps
  /var/lib/lightningos/apps-data
  /var/log/lightningos
  /var/run/docker.sock
)
managed_path_metadata='[]'
for path in "${managed_paths[@]}"; do
  if [[ -e "$path" || -L "$path" ]]; then
    stat_line=$(stat -c '%n|%F|%U|%G|%a|%s' -- "$path" 2>/dev/null || true)
    row=$(printf '%s' "$stat_line" | jq -Rcs 'split("|") | {path:.[0],type:.[1],owner:.[2],group:.[3],mode:.[4],size:(.[5]|tonumber)}')
    managed_path_metadata=$(jq -cn --argjson current "$managed_path_metadata" --argjson row "$row" '$current + [$row]')
  else
    managed_path_metadata=$(jq -cn --argjson current "$managed_path_metadata" --arg path "$path" '$current + [{path:$path,present:false}]')
  fi
done
emit managed_path_metadata "$managed_path_metadata"
