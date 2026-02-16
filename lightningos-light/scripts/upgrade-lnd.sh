#!/usr/bin/env bash
set -Eeuo pipefail
set -o errtrace

LOG_FILE="/var/log/lightningos-lnd-upgrade.log"
mkdir -p /var/log
exec > >(tee -a "$LOG_FILE") 2>&1

print_step() {
  echo ""
  echo "==> $1"
}

print_ok() {
  echo "[OK] $1"
}

print_warn() {
  echo "[WARN] $1"
}

die() {
  echo "[ERROR] $1" >&2
  exit 1
}

require_root() {
  if [[ "$(id -u)" -ne 0 ]]; then
    die "This script must run as root."
  fi
}

parse_version_from_output() {
  local output="$1"
  echo "$output" | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+([\-\.][0-9A-Za-z\.-]+)?' | head -n1 || true
}

base_version() {
  local value="$1"
  value="${value#v}"
  echo "${value%%-*}"
}

parse_commit_from_output() {
  local output="$1"
  local commit
  commit=$(echo "$output" | grep -Eo 'commit=[^ ]+' | head -n1 | cut -d= -f2- || true)
  commit="${commit#v}"
  echo "$commit"
}

VERSION=""
URL=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --version=*)
      VERSION="${1#*=}"
      shift
      ;;
    --url)
      URL="${2:-}"
      shift 2
      ;;
    --url=*)
      URL="${1#*=}"
      shift
      ;;
    *)
      die "Unknown argument: $1"
      ;;
  esac
done

require_root

if [[ -z "$VERSION" && -n "${LND_UPGRADE_VERSION:-}" ]]; then
  VERSION="${LND_UPGRADE_VERSION}"
fi
if [[ -z "$URL" && -n "${LND_UPGRADE_URL:-}" ]]; then
  URL="${LND_UPGRADE_URL}"
fi

VERSION="${VERSION#v}"
if [[ -z "$VERSION" && -n "$URL" ]]; then
  VERSION=$(echo "$URL" | grep -Eo 'v[0-9]+\.[0-9]+\.[0-9]+([\-\.][0-9A-Za-z\.-]+)?' | head -n1 || true)
  VERSION="${VERSION#v}"
fi
if [[ -z "$VERSION" ]]; then
  die "Missing --version. Example: --version 0.20.1-beta.rc1"
fi

if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([\-\.][0-9A-Za-z\.-]+)?$ ]]; then
  die "Invalid version format: ${VERSION}"
fi

if [[ -z "$URL" ]]; then
  URL="https://github.com/lightningnetwork/lnd/releases/download/v${VERSION}/lnd-linux-amd64-v${VERSION}.tar.gz"
fi

print_step "Starting LND upgrade to v${VERSION}"
echo "Download URL: ${URL}"

if [[ ! -x /usr/local/bin/lnd ]]; then
  die "LND binary not found at /usr/local/bin/lnd"
fi

current_raw=$(/usr/local/bin/lnd --version 2>/dev/null || true)
current_version=$(parse_version_from_output "$current_raw")
if [[ -n "$current_version" ]]; then
  echo "Current LND version: v${current_version}"
else
  print_warn "Could not parse current LND version."
fi

if [[ -n "$current_version" && "$current_version" == "$VERSION" ]]; then
  print_ok "Already running v${VERSION}. No upgrade needed."
  exit 0
fi

if ! command -v curl >/dev/null 2>&1; then
  die "curl is required but not installed."
fi
if ! command -v tar >/dev/null 2>&1; then
  die "tar is required but not installed."
fi

tmp_dir=""
backup_lnd=""
backup_lncli=""
rollback_ready=0

cleanup() {
  if [[ -n "$tmp_dir" && -d "$tmp_dir" ]]; then
    rm -rf "$tmp_dir"
  fi
}

rollback() {
  print_warn "Attempting rollback to previous binaries."
  if [[ -n "$backup_lnd" && -f "$backup_lnd" ]]; then
    install -m 0755 "$backup_lnd" /usr/local/bin/lnd
  fi
  if [[ -n "$backup_lncli" && -f "$backup_lncli" ]]; then
    install -m 0755 "$backup_lncli" /usr/local/bin/lncli
  fi
  if systemctl start lnd >/dev/null 2>&1; then
    print_ok "LND restarted after rollback."
  else
    print_warn "Failed to restart LND after rollback."
  fi
}

on_exit() {
  local code=$?
  trap - EXIT
  if [[ $code -ne 0 ]]; then
    print_warn "Upgrade failed. Check ${LOG_FILE} for details."
    if [[ $rollback_ready -eq 1 ]]; then
      rollback
    fi
  fi
  cleanup
}

trap on_exit EXIT

print_step "Downloading LND tarball"
tmp_dir=$(mktemp -d)
curl -fsSL "$URL" -o "$tmp_dir/lnd.tar.gz"
tar -xzf "$tmp_dir/lnd.tar.gz" -C "$tmp_dir"

lnd_bin=$(find "$tmp_dir" -type f -name "lnd" | head -n1)
lncli_bin=$(find "$tmp_dir" -type f -name "lncli" | head -n1)
if [[ -z "$lnd_bin" || -z "$lncli_bin" ]]; then
  die "Unexpected tarball contents. Missing lnd or lncli binaries."
fi

print_step "Stopping LND service"
systemctl stop lnd >/dev/null 2>&1 || true

print_step "Backing up existing binaries"
timestamp=$(date +%Y%m%d%H%M%S)
backup_lnd="/usr/local/bin/lnd.bak-${timestamp}"
backup_lncli="/usr/local/bin/lncli.bak-${timestamp}"
cp -f /usr/local/bin/lnd "$backup_lnd"
if [[ -f /usr/local/bin/lncli ]]; then
  cp -f /usr/local/bin/lncli "$backup_lncli"
else
  print_warn "lncli not found; skipping backup."
  backup_lncli=""
fi
print_ok "Backups created: ${backup_lnd}${backup_lncli:+, ${backup_lncli}}"
rollback_ready=1

print_step "Installing new binaries"
install -m 0755 "$lnd_bin" /usr/local/bin/lnd
install -m 0755 "$lncli_bin" /usr/local/bin/lncli

print_step "Verifying installed version"
new_raw=$(/usr/local/bin/lnd --version 2>/dev/null || true)
new_version=$(parse_version_from_output "$new_raw")
new_commit=$(parse_commit_from_output "$new_raw")
if [[ -z "$new_version" ]]; then
  die "Failed to detect new LND version."
fi
if [[ "$new_version" != "$VERSION" ]]; then
  target_base=$(base_version "$VERSION")
  if [[ -n "$new_commit" && "$new_commit" == "$VERSION" ]]; then
    print_warn "Installed version reports v${new_version} (commit v${new_commit}). Accepting commit match."
  elif [[ "$VERSION" == *-* && "$new_version" == "$target_base" ]]; then
    print_warn "Installed version reports v${new_version} (target v${VERSION}). Accepting pre-release normalization."
  else
    die "Version mismatch. Expected v${VERSION}, got v${new_version}"
  fi
fi
print_ok "Installed LND v${new_version}"

print_step "Starting LND service"
systemctl start lnd >/dev/null 2>&1 || die "Failed to start LND."

print_step "Waiting for LND to become active"
for i in $(seq 1 20); do
  if systemctl is-active --quiet lnd; then
    print_ok "LND is active."
    cleanup
    print_ok "Upgrade complete."
    print_ok "Upgrade job finished; systemd will mark unit complete."
    exit 0
  fi
  echo "Waiting for LND... (${i}/20)"
  sleep 1
done

die "LND did not become active in time."
