#!/usr/bin/env bash
set -Eeuo pipefail
export LC_ALL=C

# apt-cache output is parsed below. Force stable field names regardless of the
# locale configured on the node (for example, Candidate instead of Candidato).
export LC_ALL=C
export LANG=C

TOR_REPO_URL="https://deb.torproject.org/torproject.org"
TOR_REPO_KEY_URL="${TOR_REPO_URL}/A3C4F0F979CAA22CDBA8F512EE8CBC9E886DDD89.asc"
TOR_KEYRING="/usr/share/keyrings/deb.torproject.org-keyring.gpg"
TOR_SOURCES="/etc/apt/sources.list.d/tor.sources"
ASSUME_YES=0
AUTO_CONFIGURE_REPO=0
AUTO_RESTART=0

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

on_error() {
  local code=$?
  echo "[ERROR] Tor update failed with exit code ${code} while running: ${BASH_COMMAND}" >&2
  exit "$code"
}

trap on_error ERR

usage() {
  cat <<'EOF'
Usage: sudo bash ./scripts/check-tor-update.sh [options]

Checks the installed Tor package against the current APT candidate. If the
official Tor Project repository is missing, the script offers to configure it.
Package updates and the Tor service restart require separate confirmations.

Options:
  --yes             Accept update/install confirmations without a terminal.
  --configure-repo  Configure the official repository when it is missing.
  --restart         Restart Tor automatically after a successful update.
  -h, --help        Show this help.
EOF
}

confirm() {
  local prompt="$1"
  local reply=""

  if [[ "$ASSUME_YES" -eq 1 ]]; then
    return 0
  fi

  if [[ ! -t 0 ]]; then
    print_warn "An interactive terminal is required for confirmation."
    return 1
  fi

  read -r -p "${prompt} [y/N]: " reply
  case "${reply:-}" in
    y|Y|yes|YES) return 0 ;;
    *) return 1 ;;
  esac
}

require_root() {
  if [[ "$(id -u)" -ne 0 ]]; then
    die "This script must run as root. Use: sudo bash $0"
  fi
}

require_command() {
  local command_name="$1"
  command -v "$command_name" >/dev/null 2>&1 || die "Required command not found: ${command_name}"
}

official_repo_configured() {
  local sources=""
  local paths=()

  [[ -f /etc/apt/sources.list ]] && paths+=(/etc/apt/sources.list)
  [[ -d /etc/apt/sources.list.d ]] && paths+=(/etc/apt/sources.list.d)
  [[ ${#paths[@]} -gt 0 ]] || return 1

  sources=$(grep -RhsE \
    '(^|[[:space:]])(https://)?deb\.torproject\.org/torproject\.org([/[:space:]]|$)|^URIs:[[:space:]]+https://deb\.torproject\.org/torproject\.org/?$' \
    "${paths[@]}" 2>/dev/null || true)
  grep -qvE '^[[:space:]]*#' <<<"$sources"
}

get_os_codename() {
  local codename=""

  if [[ -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    codename="${VERSION_CODENAME:-${UBUNTU_CODENAME:-}}"
  fi

  if [[ -z "$codename" ]] && command -v lsb_release >/dev/null 2>&1; then
    codename=$(lsb_release -sc 2>/dev/null || true)
  fi

  echo "$codename"
}

configure_official_repo() {
  local architecture codename tmp_key

  architecture=$(dpkg --print-architecture)
  case "$architecture" in
    amd64|arm64) ;;
    *) die "The Tor Project APT repository does not support architecture: ${architecture}" ;;
  esac

  codename=$(get_os_codename)
  [[ -n "$codename" ]] || die "Could not detect the Debian/Ubuntu codename."

  apt-get update
  apt-get install -y ca-certificates curl gnupg

  print_step "Checking Tor Project repository for ${codename}/${architecture}"
  curl -fsI "${TOR_REPO_URL}/dists/${codename}/InRelease" >/dev/null \
    || die "The Tor Project repository is unavailable for codename: ${codename}"

  tmp_key=$(mktemp)
  trap 'rm -f "${tmp_key:-}"' RETURN
  curl -fsSL "$TOR_REPO_KEY_URL" | gpg --dearmor > "$tmp_key"
  install -m 0644 "$tmp_key" "$TOR_KEYRING"
  rm -f "$tmp_key"
  trap - RETURN

  cat > "$TOR_SOURCES" <<EOF
Types: deb
URIs: ${TOR_REPO_URL}/
Suites: ${codename}
Components: main
Architectures: ${architecture}
Signed-By: ${TOR_KEYRING}
EOF

  print_ok "Official Tor Project repository configured (${codename}/${architecture})."
}

installed_package_version() {
  local status version

  status=$(dpkg-query -W -f='${db:Status-Abbrev}' tor 2>/dev/null || true)
  [[ "$status" == "ii " ]] || return 0
  version=$(dpkg-query -W -f='${Version}' tor 2>/dev/null || true)
  echo "$version"
}

candidate_package_version() {
  apt-cache policy tor | awk '
    $1 ~ /^Candidate:$/ { candidate = $2 }
    END { if (candidate) print candidate }
  '
}

runtime_tor_version() {
  tor --version 2>/dev/null \
    | sed -n 's/^Tor version \([^ .][^ ]*\)\.$/\1/p'
}

detect_tor_unit() {
  if systemctl list-unit-files tor@default.service --no-legend 2>/dev/null | grep -q '^tor@default\.service'; then
    echo "tor@default.service"
    return
  fi
  if systemctl list-unit-files tor.service --no-legend 2>/dev/null | grep -q '^tor\.service'; then
    echo "tor.service"
    return
  fi
  echo ""
}

wait_for_service() {
  local unit="$1"
  local attempt

  for attempt in $(seq 1 30); do
    if systemctl is-active --quiet "$unit"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_for_bootstrap() {
  local unit="$1"
  local since="$2"
  local attempt logs

  for attempt in $(seq 1 60); do
    logs=$(journalctl -u "$unit" --since "$since" --no-pager 2>/dev/null || true)
    if grep -q 'Bootstrapped 100% (done)' <<<"$logs"; then
      print_ok "Tor bootstrapped to 100%."
      return 0
    fi
    sleep 2
  done

  print_warn "Tor did not report 100% bootstrap in the journal within 120 seconds."
  journalctl -u "$unit" --since "$since" --no-pager 2>/dev/null \
    | grep -E 'Bootstrapped|\[warn\]|\[err\]|warn|error|fail|consensus|authority' \
    | tail -n 30 || true
  return 1
}

main() {
  local repo_ready=0
  local installed candidate runtime unit restart_since
  local update_performed=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --yes)
        ASSUME_YES=1
        ;;
      --configure-repo)
        AUTO_CONFIGURE_REPO=1
        ;;
      --restart)
        AUTO_RESTART=1
        ;;
      --help|-h)
        usage
        exit 0
        ;;
      *)
        die "Unknown argument: $1"
        ;;
    esac
    shift
  done

  require_root
  require_command apt-get
  require_command apt-cache
  require_command dpkg
  require_command dpkg-query
  require_command systemctl

  print_step "Checking Tor package source"
  if official_repo_configured; then
    repo_ready=1
    print_ok "Official Tor Project repository is configured."
  else
    print_warn "Official Tor Project repository is not configured."
    print_warn "The distribution candidate may be older than the current Tor release."
    if [[ "$AUTO_CONFIGURE_REPO" -eq 1 ]] || confirm "Configure the official Tor Project repository now?"; then
      configure_official_repo
      repo_ready=1
    else
      print_warn "Continuing with the currently configured APT repositories."
    fi
  fi

  print_step "Refreshing APT package metadata"
  apt-get update

  installed=$(installed_package_version)
  candidate=$(candidate_package_version)
  [[ "$candidate" != "(none)" ]] || candidate=""

  echo "Installed package: ${installed:-not installed}"
  echo "APT candidate:     ${candidate:-not available}"
  if command -v tor >/dev/null 2>&1; then
    runtime=$(runtime_tor_version)
    echo "Runtime binary:    ${runtime:-unknown}"
  else
    echo "Runtime binary:    not found"
  fi
  echo ""
  apt-cache policy tor

  [[ -n "$candidate" ]] || die "APT does not provide a Tor candidate."

  if [[ -z "$installed" ]]; then
    if confirm "Tor is not installed. Install ${candidate}?"; then
      if [[ "$repo_ready" -eq 1 ]]; then
        apt-get install -y tor tor-geoipdb deb.torproject.org-keyring
      else
        apt-get install -y tor tor-geoipdb
      fi
      update_performed=1
    else
      print_warn "Tor installation declined."
      exit 0
    fi
  elif dpkg --compare-versions "$installed" lt "$candidate"; then
    if confirm "Update Tor from ${installed} to ${candidate}?"; then
      if [[ "$repo_ready" -eq 1 ]]; then
        apt-get install -y tor tor-geoipdb deb.torproject.org-keyring
      else
        apt-get install -y tor tor-geoipdb
      fi
      update_performed=1
    else
      print_warn "Tor update declined."
      exit 0
    fi
  elif dpkg --compare-versions "$installed" gt "$candidate"; then
    print_warn "Installed Tor ${installed} is newer than the APT candidate ${candidate}."
    print_warn "No downgrade will be attempted."
    exit 0
  else
    print_ok "Tor is already at the current APT candidate (${installed})."
    print_ok "Tor update check complete."
    exit 0
  fi

  installed=$(installed_package_version)
  runtime=$(runtime_tor_version)
  print_ok "Installed package version: ${installed:-unknown}"
  print_ok "Tor binary version: ${runtime:-unknown}"

  if [[ -z "$installed" ]] || dpkg --compare-versions "$installed" lt "$candidate"; then
    die "Tor did not reach the expected package version ${candidate}."
  fi

  if [[ "$update_performed" -ne 1 ]]; then
    exit 0
  fi

  unit=$(detect_tor_unit)
  if [[ -z "$unit" ]]; then
    print_warn "Tor systemd unit was not found. Restart it manually if required."
    exit 0
  fi

  if [[ "$AUTO_RESTART" -ne 1 ]] && ! confirm "Restart ${unit} now? Existing Tor connections will be interrupted briefly."; then
    print_warn "Tor was updated but not restarted. Restart ${unit} to load the new binary."
    exit 0
  fi

  restart_since=$(date --iso-8601=seconds)
  systemctl restart "$unit"

  if ! wait_for_service "$unit"; then
    systemctl status "$unit" --no-pager -l || true
    die "${unit} did not become active after the restart."
  fi
  print_ok "${unit} is active."

  wait_for_bootstrap "$unit" "$restart_since" || true
  print_ok "Tor update complete."
}

main "$@"
