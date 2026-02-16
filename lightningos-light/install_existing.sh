#!/usr/bin/env bash
set -Eeuo pipefail
set -o errtrace

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$SCRIPT_DIR"

GO_VERSION="${GO_VERSION:-1.24.12}"
GO_TARBALL_URL="https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
NODE_VERSION="${NODE_VERSION:-current}"
GOTTY_VERSION="${GOTTY_VERSION:-1.0.1}"
GOTTY_URL="https://github.com/yudai/gotty/releases/download/v${GOTTY_VERSION}/gotty_linux_amd64.tar.gz"
POSTGRES_VERSION="${POSTGRES_VERSION:-latest}"
LND_FIX_PERMS_SCRIPT="/usr/local/sbin/lightningos-fix-lnd-perms"
LND_UPGRADE_SCRIPT="/usr/local/sbin/lightningos-upgrade-lnd"

CURRENT_STEP=""
LOG_FILE="/var/log/lightningos-install-existing.log"

mkdir -p /var/log
exec > >(tee -a "$LOG_FILE") 2>&1
trap 'echo ""; echo "Installation failed during: ${CURRENT_STEP:-unknown}"; echo "Last command: $BASH_COMMAND"; echo "Check: systemctl status lightningos-manager --no-pager"; echo "Also: journalctl -u lightningos-manager -n 50 --no-pager"; exit 1' ERR

DEFAULT_LND_DIR="/data/lnd"
DEFAULT_BITCOIN_DIR="/data/bitcoin"
CONFIG_PATH="/etc/lightningos/config.yaml"
SECRETS_PATH="/etc/lightningos/secrets.env"
NOTIFICATIONS_DB_NAME="lightningos"
NOTIFICATIONS_APP_USER="losapp"
NOTIFICATIONS_ADMIN_USER="losadmin"
LND_SERVICE=""
LND_USER=""
LND_GROUP=""
BITCOIN_SERVICE=""
BITCOIN_USER=""
BITCOIN_GROUP=""

print_step() {
  CURRENT_STEP="$1"
  echo ""
  echo "==> $1"
}

print_ok() {
  echo "[OK] $1"
}

print_warn() {
  echo "[WARN] $1"
}

get_lightningos_version() {
  local version_file="$REPO_ROOT/ui/public/version.txt"
  local version="unknown"
  if [[ -f "$version_file" ]]; then
    version=$(head -n1 "$version_file" | tr -d '\r' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
  fi
  if [[ -z "$version" ]]; then
    version="unknown"
  fi
  echo "$version"
}

print_lightningos_banner() {
  cat <<'EOF'
■     ■■■■■ ■■■■■ ■   ■ ■■■■■ ■   ■ ■■■■■ ■   ■ ■■■■■ □□□□□ □□□□□
■       ■   ■     ■   ■   ■   ■■  ■   ■   ■■  ■ ■     □   □ □
■       ■   ■     ■   ■   ■   ■ ■ ■   ■   ■ ■ ■ ■     □   □ □
■       ■   ■ ■■■ ■■■■■   ■   ■  ■■   ■   ■  ■■ ■ ■■■ □   □ □□□□□
■       ■   ■   ■ ■   ■   ■   ■   ■   ■   ■   ■ ■   ■ □   □     □
■       ■   ■   ■ ■   ■   ■   ■   ■   ■   ■   ■ ■   ■ □   □     □
■■■■■ ■■■■■ ■■■■■ ■   ■   ■   ■   ■ ■■■■■ ■   ■ ■■■■■ □□□□□ □□□□□
EOF
}

get_mit_terms() {
  local license_file="$REPO_ROOT/../LICENSE"
  if [[ -f "$license_file" ]]; then
    awk 'BEGIN{p=0} /^MIT License \\(English\\)/{p=1} /^MIT License \\(Portuguese\\)/{p=0} p{print}' "$license_file"
    return
  fi
  cat <<'EOF'
MIT License (English)

Copyright (c) 2026 BR⚡LN

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
EOF
}

confirm_mit_license() {
  local accepted="${ACCEPT_MIT_LICENSE:-}"
  if [[ -n "$accepted" ]]; then
    case "$accepted" in
      1|y|Y|yes|YES|true|TRUE) return 0 ;;
      0|n|N|no|NO|false|FALSE) echo "MIT License not accepted."; exit 1 ;;
    esac
  fi
  if [[ ! -t 0 ]]; then
    echo "Non-interactive mode detected."
    echo "Set ACCEPT_MIT_LICENSE=1 to proceed."
    exit 1
  fi
  while true; do
    read -r -p "Do you want to continue with the installation? [y/N]: " reply
    reply="${reply:-N}"
    case "$reply" in
      [Yy]*) return 0 ;;
      [Nn]*) echo "Installation cancelled."; exit 1 ;;
    esac
  done
}

show_welcome_and_license() {
  local version
  version=$(get_lightningos_version)
  print_lightningos_banner
  echo "Version: ${version}"
  echo "Created by BR⚡LN - https://br-ln.com"
  echo ""
  get_mit_terms
  echo ""
  confirm_mit_license
}

get_lan_ip() {
  local ip
  ip=$(hostname -I 2>/dev/null | awk '{print $1}')
  echo "$ip"
}

require_root() {
  if [[ "$(id -u)" -ne 0 ]]; then
    echo "This script must run as root. Use sudo." >&2
    exit 1
  fi
}

prompt_yes_no() {
  local prompt="$1"
  local default="${2:-y}"
  local suffix
  if [[ "$default" == "y" ]]; then
    suffix="[Y/n]"
  else
    suffix="[y/N]"
  fi
  while true; do
    read -r -p "${prompt} ${suffix} " reply
    reply="${reply:-$default}"
    case "$reply" in
      [Yy]*) return 0 ;;
      [Nn]*) return 1 ;;
    esac
  done
}

prompt_value() {
  local prompt="$1"
  local default="${2:-}"
  local value
  if [[ -n "$default" ]]; then
    read -r -p "${prompt} [${default}] " value
    value="${value:-$default}"
  else
    read -r -p "${prompt} " value
  fi
  echo "$value"
}

escape_sed() {
  printf '%s' "$1" | sed -e 's/[\\/&]/\\&/g'
}

set_env_value() {
  local key="$1"
  local value="$2"
  local escaped
  escaped=$(escape_sed "$value")
  if grep -q "^${key}=" "$SECRETS_PATH"; then
    sed -i "s|^${key}=.*|${key}=${escaped}|" "$SECRETS_PATH"
  else
    echo "${key}=${value}" >> "$SECRETS_PATH"
  fi
}

ensure_secrets_file() {
  mkdir -p /etc/lightningos
  if [[ ! -f "$SECRETS_PATH" ]]; then
    cp "$REPO_ROOT/templates/secrets.env" "$SECRETS_PATH"
  fi
}

ensure_dirs() {
  print_step "Preparing directories"
  mkdir -p /etc/lightningos /etc/lightningos/tls /opt/lightningos/manager /opt/lightningos/ui \
    /var/lib/lightningos /var/log/lightningos
  chmod 750 /etc/lightningos /etc/lightningos/tls /var/lib/lightningos
  print_ok "Directories ready"
}

fix_lightningos_permissions() {
  local group="$1"
  if ! getent group "$group" >/dev/null 2>&1; then
    print_warn "Group ${group} not found; skipping /etc/lightningos ownership"
    return
  fi
  chown root:"$group" /etc/lightningos /etc/lightningos/tls
  chmod 750 /etc/lightningos /etc/lightningos/tls
  if [[ -f "$CONFIG_PATH" ]]; then
    chown root:"$group" "$CONFIG_PATH"
    chmod 640 "$CONFIG_PATH"
  fi
  if [[ -f "$SECRETS_PATH" ]]; then
    chown root:"$group" "$SECRETS_PATH"
    chmod 660 "$SECRETS_PATH"
  fi
  if [[ -f /etc/lightningos/tls/server.crt ]]; then
    chown root:"$group" /etc/lightningos/tls/server.crt
    chmod 640 /etc/lightningos/tls/server.crt
  fi
  if [[ -f /etc/lightningos/tls/server.key ]]; then
    chown root:"$group" /etc/lightningos/tls/server.key
    chmod 640 /etc/lightningos/tls/server.key
  fi
  print_ok "Permissions updated for /etc/lightningos"
}

fix_lightningos_storage_permissions() {
  local user="$1"
  local group="$2"
  if ! id "$user" >/dev/null 2>&1; then
    print_warn "User ${user} not found; skipping /var/lib/lightningos ownership"
    return
  fi
  if ! getent group "$group" >/dev/null 2>&1; then
    print_warn "Group ${group} not found; skipping /var/lib/lightningos ownership"
    return
  fi
  chown -R "$user:$group" /var/lib/lightningos /var/log/lightningos
  chmod 750 /var/lib/lightningos /var/log/lightningos
  print_ok "Permissions updated for /var/lib/lightningos"
}

install_go() {
  print_step "Installing Go ${GO_VERSION}"
  local go_bin
  go_bin=$(detect_go_binary || true)
  if [[ -n "$go_bin" ]]; then
    local current major minor
    current=$("$go_bin" version | awk '{print $3}' | sed 's/go//')
    major=$(echo "$current" | cut -d. -f1)
    minor=$(echo "$current" | cut -d. -f2)
    if [[ "$major" -gt 1 || ( "$major" -eq 1 && "$minor" -ge 24 ) ]]; then
      export PATH="/usr/local/go/bin:$PATH"
      print_ok "Go already installed ($current)"
      return
    fi
  fi

  rm -rf /usr/local/go
  local tmp=""
  if tmp=$(mktemp /tmp/go.tgz.XXXXXX 2>/dev/null); then
    :
  elif tmp=$(mktemp /var/tmp/go.tgz.XXXXXX 2>/dev/null); then
    :
  elif tmp=$(mktemp /root/go.tgz.XXXXXX 2>/dev/null); then
    :
  else
    print_warn "No writable temp directory for Go download"
    exit 1
  fi
  local primary_url="$GO_TARBALL_URL"
  local fallback_url="https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz"
  if [[ "$primary_url" == "$fallback_url" ]]; then
    fallback_url="https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
  fi

  if ! curl -fL "$primary_url" -o "$tmp" || ! tar -tzf "$tmp" >/dev/null 2>&1; then
    print_warn "Go download failed from ${primary_url}"
    if ! curl -fL "$fallback_url" -o "$tmp" || ! tar -tzf "$tmp" >/dev/null 2>&1; then
      print_warn "Go download failed from ${fallback_url}"
      exit 1
    fi
  fi

  tar -C /usr/local -xzf "$tmp"
  rm -f "$tmp"
  export PATH="/usr/local/go/bin:$PATH"
  print_ok "Go installed"
}

install_node() {
  resolve_node_version
  print_step "Installing Node.js ${NODE_VERSION}.x"
  if command -v node >/dev/null 2>&1; then
    local major
    major=$(node -v | sed 's/v//' | cut -d. -f1)
    if [[ "$major" -ge "$NODE_VERSION" ]]; then
      print_ok "Node.js already installed ($(node -v))"
      return
    fi
  fi
  if ! command -v apt-get >/dev/null 2>&1; then
    print_warn "apt-get not found; install Node.js manually and re-run."
    return 1
  fi
  curl -fsSL "https://deb.nodesource.com/setup_${NODE_VERSION}.x" | bash -
  apt-get install -y nodejs >/dev/null
  print_ok "Node.js installed"
}

resolve_node_version() {
  if [[ "$NODE_VERSION" =~ ^[0-9]+$ ]]; then
    return 0
  fi
  if command -v jq >/dev/null 2>&1 && command -v curl >/dev/null 2>&1; then
    local major
    major=$(curl -fsSL https://nodejs.org/dist/index.json \
      | jq -r '.[].version' \
      | sed 's/^v//' \
      | cut -d. -f1 \
      | sort -nr \
      | head -n1)
    if [[ -n "$major" ]]; then
      NODE_VERSION="$major"
      print_ok "Using Node.js ${NODE_VERSION}.x"
      return 0
    fi
  fi
  print_warn "Could not resolve latest Node.js version; falling back to 20"
  NODE_VERSION="20"
}

detect_go_binary() {
  if command -v go >/dev/null 2>&1; then
    command -v go
    return 0
  fi
  if [[ -x /usr/local/go/bin/go ]]; then
    echo "/usr/local/go/bin/go"
    return 0
  fi
  return 1
}

install_gotty() {
  print_step "Installing GoTTY ${GOTTY_VERSION}"
  if command -v gotty >/dev/null 2>&1; then
    if gotty --version 2>/dev/null | grep -q "${GOTTY_VERSION}"; then
      print_ok "GoTTY already installed"
      return
    fi
  fi
  local tmp
  tmp=$(mktemp -d)
  curl -fsSL "$GOTTY_URL" -o "$tmp/gotty.tar.gz"
  tar -xzf "$tmp/gotty.tar.gz" -C "$tmp"
  install -m 0755 "$tmp/gotty" /usr/local/bin/gotty
  rm -rf "$tmp"
  print_ok "GoTTY installed"
}

ensure_smartmontools() {
  if command -v smartctl >/dev/null 2>&1; then
    print_ok "smartctl already installed"
    return 0
  fi
  if ! command -v apt-get >/dev/null 2>&1; then
    print_warn "smartctl not found and apt-get unavailable; install smartmontools manually"
    return 1
  fi
  print_step "Installing smartmontools"
  apt-get update
  apt-get install -y smartmontools >/dev/null
  print_ok "smartmontools installed"
}

configure_smartctl_sudoers() {
  local user="$1"
  if [[ "$user" == "root" ]]; then
    print_ok "Manager user is root; smartctl sudoers not needed"
    return 0
  fi
  if ! command -v sudo >/dev/null 2>&1; then
    print_warn "sudo not found; smartctl access may fail"
    return 1
  fi
  local smartctl_path
  smartctl_path=$(command -v smartctl || true)
  if [[ -z "$smartctl_path" ]]; then
    smartctl_path="/usr/sbin/smartctl"
  fi
  local sudoers="/etc/sudoers.d/lightningos-smartctl"
  cat > "$sudoers" <<EOF
Defaults:${user} !requiretty
${user} ALL=NOPASSWD: ${smartctl_path} *
EOF
  chmod 440 "$sudoers"
  print_ok "Sudoers updated for smartctl"
}

install_lnd_fix_perms_script() {
  local src="$REPO_ROOT/scripts/fix-lnd-perms.sh"
  if [[ -f "$src" ]]; then
    mkdir -p "$(dirname "$LND_FIX_PERMS_SCRIPT")"
    install -m 0755 "$src" "$LND_FIX_PERMS_SCRIPT"
    print_ok "LND permissions helper installed"
  else
    print_warn "Missing helper script: $src"
  fi
}

install_lnd_upgrade_script() {
  local src="$REPO_ROOT/scripts/upgrade-lnd.sh"
  if [[ -f "$src" ]]; then
    mkdir -p "$(dirname "$LND_UPGRADE_SCRIPT")"
    install -m 0755 "$src" "$LND_UPGRADE_SCRIPT"
    print_ok "LND upgrade helper installed"
  else
    print_warn "Missing helper script: $src"
  fi
}

configure_sudoers() {
  print_step "Configuring sudoers"
  local systemctl_path apt_get_path apt_path dpkg_path docker_path docker_compose_path systemd_run_path smartctl_path ufw_path
  systemctl_path=$(command -v systemctl || true)
  apt_get_path=$(command -v apt-get || true)
  apt_path=$(command -v apt || true)
  dpkg_path=$(command -v dpkg || true)
  docker_path=$(command -v docker || true)
  docker_compose_path=$(command -v docker-compose || true)
  systemd_run_path=$(command -v systemd-run || true)
  smartctl_path=$(command -v smartctl || true)
  ufw_path=$(command -v ufw || true)
  if [[ -z "$docker_path" ]]; then
    docker_path="/usr/bin/docker"
  fi
  if [[ -z "$docker_compose_path" ]]; then
    docker_compose_path="/usr/bin/docker-compose"
  fi
  if [[ -z "$systemd_run_path" ]]; then
    systemd_run_path="/usr/bin/systemd-run"
  fi
  if [[ -z "$smartctl_path" ]]; then
    smartctl_path="/usr/sbin/smartctl"
  fi
  if [[ -z "$systemctl_path" ]]; then
    print_warn "systemctl not found; skipping sudoers setup"
    return
  fi
  local system_cmds
  system_cmds="${systemctl_path} restart lnd, ${systemctl_path} restart lightningos-manager, ${systemctl_path} restart postgresql, ${systemctl_path} is-active lightningos-lnd-upgrade, ${systemctl_path} reboot, ${systemctl_path} poweroff, ${LND_FIX_PERMS_SCRIPT}, ${LND_UPGRADE_SCRIPT}, ${smartctl_path} *"
  local app_cmds=()
  [[ -n "$apt_get_path" ]] && app_cmds+=("${apt_get_path} *")
  [[ -n "$apt_path" ]] && app_cmds+=("${apt_path} *")
  [[ -n "$dpkg_path" ]] && app_cmds+=("${dpkg_path} *")
  [[ -n "$docker_path" ]] && app_cmds+=("${docker_path} *")
  [[ -n "$docker_compose_path" ]] && app_cmds+=("${docker_compose_path} *")
  [[ -n "$systemd_run_path" ]] && app_cmds+=("${systemd_run_path} *")
  [[ -n "$ufw_path" ]] && app_cmds+=("${ufw_path} *")
  local app_cmds_line
  app_cmds_line=$(IFS=", "; echo "${app_cmds[*]}")
  if [[ -z "$app_cmds_line" ]]; then
    app_cmds_line="/bin/true"
  fi
  cat > /etc/sudoers.d/lightningos <<EOF
Defaults:lightningos !requiretty
Cmnd_Alias LIGHTNINGOS_SYSTEM = ${system_cmds}
Cmnd_Alias LIGHTNINGOS_APPS = ${app_cmds_line}
lightningos ALL=NOPASSWD: LIGHTNINGOS_SYSTEM, LIGHTNINGOS_APPS
EOF
  chmod 440 /etc/sudoers.d/lightningos
  print_ok "Sudoers configured"
}

read_conf_value() {
  local path="$1"
  local key="$2"
  if [[ ! -f "$path" ]]; then
    return
  fi
  local line
  line=$(grep -E "^[[:space:]]*${key}[[:space:]]*=" "$path" | grep -v '^[[:space:]]*[#;]' | tail -n1 || true)
  line="${line#*=}"
  line="$(echo "$line" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
  if [[ -n "$line" ]]; then
    echo "$line"
  fi
}

resolve_data_dir() {
  local label="$1"
  local default="$2"
  local dir="$default"
  if [[ -d "$default" ]]; then
    echo "$default"
    return
  fi

  local admin_link=""
  if [[ "$label" == "LND" && -e /home/admin/.lnd ]]; then
    admin_link="/home/admin/.lnd"
  elif [[ "$label" == "Bitcoin" && -e /home/admin/.bitcoin ]]; then
    admin_link="/home/admin/.bitcoin"
  fi

  if [[ -n "$admin_link" ]]; then
    local admin_target=""
    if command -v readlink >/dev/null 2>&1; then
      admin_target=$(readlink -f "$admin_link" || true)
    fi
    if [[ -z "$admin_target" && -d "$admin_link" ]]; then
      admin_target="$admin_link"
    fi
    if [[ -n "$admin_target" && -d "$admin_target" ]]; then
      if prompt_yes_no "Found ${admin_link} -> ${admin_target}. Create symlink ${default} -> ${admin_target}?" "y"; then
        if [[ -e "$default" ]]; then
          print_warn "Path ${default} already exists; skipping symlink"
        else
          mkdir -p "$(dirname "$default")"
          ln -s "$admin_target" "$default"
          print_ok "Symlink created: ${default} -> ${admin_target}"
          echo "$default"
          return
        fi
      fi
    fi
  fi

  print_warn "${label} directory not found at ${default}"
  if prompt_yes_no "Use a different ${label} directory?" "y"; then
    dir=$(prompt_value "Enter ${label} directory")
    if [[ -z "$dir" || ! -d "$dir" ]]; then
      print_warn "Directory not found: ${dir}"
      exit 1
    fi
  else
    exit 1
  fi
  if [[ "$default" != "$dir" ]]; then
    if prompt_yes_no "Create symlink ${default} -> ${dir}?" "n"; then
      if [[ -e "$default" ]]; then
        print_warn "Path ${default} already exists; skipping symlink"
      else
        mkdir -p "$(dirname "$default")"
        ln -s "$dir" "$default"
        print_ok "Symlink created: ${default} -> ${dir}"
      fi
    fi
  fi
  echo "$dir"
}

ensure_go() {
  local go_ok="0"
  local go_bin
  go_bin=$(detect_go_binary || true)
  if [[ -n "$go_bin" ]]; then
    export PATH="$(dirname "$go_bin"):$PATH"
    local current major minor
    current=$("$go_bin" version | awk '{print $3}' | sed 's/go//')
    major=$(echo "$current" | cut -d. -f1)
    minor=$(echo "$current" | cut -d. -f2)
    if [[ "$major" -gt 1 || ( "$major" -eq 1 && "$minor" -ge 24 ) ]]; then
      go_ok="1"
    fi
  fi
  if [[ "$go_ok" != "1" ]]; then
    print_warn "Go 1.24+ required"
    if prompt_yes_no "Install Go ${GO_VERSION} now?" "y"; then
      install_go
    else
      print_warn "Go is required to build the manager"
      exit 1
    fi
  fi
}

ensure_node() {
  local node_ok="0"
  local npm_ok="0"
  if command -v node >/dev/null 2>&1; then
    resolve_node_version
    local major
    major=$(node -v | sed 's/v//' | cut -d. -f1)
    if [[ "$major" -ge "$NODE_VERSION" ]]; then
      node_ok="1"
    fi
  fi
  if command -v npm >/dev/null 2>&1; then
    npm_ok="1"
  fi
  if [[ "$node_ok" != "1" || "$npm_ok" != "1" ]]; then
    print_warn "Node.js (npm) required"
    if prompt_yes_no "Install Node.js ${NODE_VERSION}.x now?" "y"; then
      install_node
    else
      print_warn "npm is required to build the UI"
      exit 1
    fi
  fi
}

build_manager() {
  print_step "Building manager"
  (cd "$REPO_ROOT" && \
    GOFLAGS="-mod=mod" go mod download && \
    GOFLAGS="-mod=mod" go build -o dist/lightningos-manager ./cmd/lightningos-manager)
  install -m 0755 "$REPO_ROOT/dist/lightningos-manager" /opt/lightningos/manager/lightningos-manager
  print_ok "Manager built and installed"
}

build_ui() {
  print_step "Building UI"
  (cd "$REPO_ROOT/ui" && npm install && npm run build)
  rm -rf /opt/lightningos/ui/*
  cp -a "$REPO_ROOT/ui/dist/." /opt/lightningos/ui/
  print_ok "UI built and installed"
}

ensure_tls() {
  local crt="/etc/lightningos/tls/server.crt"
  local key="/etc/lightningos/tls/server.key"
  if [[ -f "$crt" && -f "$key" ]]; then
    return
  fi
  if ! prompt_yes_no "Generate self-signed TLS cert for the manager?" "y"; then
    print_warn "TLS certs missing; manager may not start without them"
    return
  fi
  openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
    -subj "/CN=$(hostname -f)" \
    -keyout "$key" \
    -out "$crt"
  print_ok "TLS certificates created"
}

detect_lnd_backend() {
  local lnd_conf="$1"
  if [[ ! -f "$lnd_conf" ]]; then
    echo "unknown"
    return
  fi
  local backend
  backend=$(read_conf_value "$lnd_conf" "db.backend")
  if [[ "$backend" == "postgres" ]]; then
    echo "postgres"
    return
  fi
  local dsn
  dsn=$(read_conf_value "$lnd_conf" "db.postgres.dsn")
  if [[ -n "$dsn" ]]; then
    echo "postgres"
    return
  fi
  echo "bolt"
}

ensure_manager_service() {
  local user="$1"
  local group="$2"
  local dst="/etc/systemd/system/lightningos-manager.service"
  cp "$REPO_ROOT/templates/systemd/lightningos-manager.service" "$dst"
  sed -i "s|^User=.*|User=${user}|" "$dst"
  sed -i "s|^Group=.*|Group=${group}|" "$dst"
  local groups=("systemd-journal")
  if [[ -n "$LND_GROUP" ]]; then
    getent group "$LND_GROUP" >/dev/null 2>&1 && groups+=("$LND_GROUP")
  else
    getent group lnd >/dev/null 2>&1 && groups+=("lnd")
  fi
  if [[ -n "$BITCOIN_GROUP" ]]; then
    getent group "$BITCOIN_GROUP" >/dev/null 2>&1 && groups+=("$BITCOIN_GROUP")
  else
    getent group bitcoin >/dev/null 2>&1 && groups+=("bitcoin")
  fi
  getent group docker >/dev/null 2>&1 && groups+=("docker")
  local group_line
  group_line=$(IFS=' '; echo "${groups[*]}")
  sed -i "s|^SupplementaryGroups=.*|SupplementaryGroups=${group_line}|" "$dst"
}

ensure_reports_services() {
  local user="$1"
  local group="$2"
  local svc="/etc/systemd/system/lightningos-reports.service"
  cp "$REPO_ROOT/templates/systemd/lightningos-reports.service" "$svc"
  cp "$REPO_ROOT/templates/systemd/lightningos-reports.timer" /etc/systemd/system/lightningos-reports.timer
  sed -i "s|^User=.*|User=${user}|" "$svc"
  sed -i "s|^Group=.*|Group=${group}|" "$svc"
  if getent group systemd-journal >/dev/null 2>&1; then
    sed -i "s|^SupplementaryGroups=.*|SupplementaryGroups=systemd-journal|" "$svc"
  else
    sed -i "/^SupplementaryGroups=/d" "$svc"
  fi
}

ensure_terminal_service() {
  local user="$1"
  local group="$2"
  cp "$REPO_ROOT/templates/systemd/lightningos-terminal.service" /etc/systemd/system/lightningos-terminal.service
  sed -i "s|^User=.*|User=${user}|" /etc/systemd/system/lightningos-terminal.service
  sed -i "s|^Group=.*|Group=${group}|" /etc/systemd/system/lightningos-terminal.service
  if getent group lightningos >/dev/null 2>&1; then
    sed -i "s|^SupplementaryGroups=.*|SupplementaryGroups=lightningos|" /etc/systemd/system/lightningos-terminal.service
  else
    sed -i "/^SupplementaryGroups=/d" /etc/systemd/system/lightningos-terminal.service
  fi
}

ensure_terminal_helper() {
  local src="$REPO_ROOT/scripts/lightningos-terminal.sh"
  if [[ -f "$src" ]]; then
    install -m 0755 "$src" /usr/local/sbin/lightningos-terminal
    print_ok "Terminal helper installed"
  else
    print_warn "Missing helper script: $src"
  fi
}

ensure_terminal_user() {
  local user="$1"
  if id "$user" >/dev/null 2>&1; then
    return
  fi
  if prompt_yes_no "User ${user} does not exist. Create it?" "y"; then
    if command -v adduser >/dev/null 2>&1; then
      adduser --disabled-password --gecos "" "$user"
    else
      useradd -m -d "/home/${user}" -s /bin/bash "$user"
    fi
  else
    print_warn "Terminal service requires a valid user"
    exit 1
  fi
}

ensure_lightningos_user() {
  if ! getent group lightningos >/dev/null 2>&1; then
    groupadd --system lightningos
  fi
  if ! id lightningos >/dev/null 2>&1; then
    useradd --system --home /var/lib/lightningos --shell /usr/sbin/nologin -g lightningos lightningos
  fi
  mkdir -p /var/lib/lightningos
  chown lightningos:lightningos /var/lib/lightningos
  chmod 750 /var/lib/lightningos
}

ensure_group_membership() {
  local user="$1"
  shift
  local group
  for group in "$@"; do
    if ! getent group "$group" >/dev/null 2>&1; then
      continue
    fi
    if id -nG "$user" | tr ' ' '\n' | grep -qx "$group"; then
      continue
    fi
    usermod -a -G "$group" "$user"
  done
}

check_service() {
  local svc="$1"
  if systemctl is-active --quiet "$svc"; then
    echo "active"
  elif systemctl is-enabled --quiet "$svc" 2>/dev/null; then
    echo "enabled"
  else
    echo "missing"
  fi
}

ensure_user_exists() {
  local user="$1"
  if id "$user" >/dev/null 2>&1; then
    return 0
  fi
  if prompt_yes_no "User ${user} does not exist. Create it?" "y"; then
    if command -v adduser >/dev/null 2>&1; then
      adduser --disabled-password --gecos "" "$user"
    else
      useradd -m -d "/home/${user}" -s /bin/bash "$user"
    fi
    return 0
  fi
  return 1
}

ensure_group_exists() {
  local group="$1"
  if getent group "$group" >/dev/null 2>&1; then
    return 0
  fi
  if prompt_yes_no "Group ${group} does not exist. Create it?" "y"; then
    groupadd --system "$group"
    return 0
  fi
  return 1
}

service_exists() {
  systemctl list-unit-files --type=service --no-pager 2>/dev/null | awk '{print $1}' | grep -qx "${1}.service"
}

ensure_ufw_manager_port() {
  if ! command -v ufw >/dev/null 2>&1; then
    return 0
  fi
  local status
  status=$(ufw status 2>/dev/null || true)
  if ! echo "$status" | grep -qi "Status: active"; then
    return 0
  fi
  if echo "$status" | grep -Eq '(^|[[:space:]])8443/tcp([[:space:]]|$)'; then
    return 0
  fi
  ufw allow 8443/tcp || print_warn "Failed to open UFW port 8443/tcp"
}

detect_first_service() {
  local svc
  for svc in "$@"; do
    if service_exists "$svc"; then
      echo "$svc"
      return 0
    fi
  done
  return 1
}

detect_service_user() {
  local svc="$1"
  if [[ -z "$svc" ]]; then
    return 1
  fi
  local user
  user=$(systemctl show -p User --value "$svc" 2>/dev/null || true)
  user=$(echo "$user" | tr -d ' ')
  if [[ -n "$user" ]]; then
    echo "$user"
    return 0
  fi
  return 1
}

detect_service_group() {
  local svc="$1"
  local fallback="$2"
  if [[ -z "$svc" ]]; then
    return 1
  fi
  local group
  group=$(systemctl show -p Group --value "$svc" 2>/dev/null || true)
  group=$(echo "$group" | tr -d ' ')
  if [[ -z "$group" ]]; then
    group="$fallback"
  fi
  if [[ -n "$group" ]]; then
    echo "$group"
    return 0
  fi
  return 1
}

detect_core_service_users() {
  LND_SERVICE=$(detect_first_service lnd lnd@default || true)
  if [[ -n "$LND_SERVICE" ]]; then
    LND_USER=$(detect_service_user "$LND_SERVICE" || true)
    LND_GROUP=$(detect_service_group "$LND_SERVICE" "$LND_USER" || true)
  fi

  BITCOIN_SERVICE=$(detect_first_service bitcoind bitcoin bitcoind@default bitcoin@default || true)
  if [[ -n "$BITCOIN_SERVICE" ]]; then
    BITCOIN_USER=$(detect_service_user "$BITCOIN_SERVICE" || true)
    BITCOIN_GROUP=$(detect_service_group "$BITCOIN_SERVICE" "$BITCOIN_USER" || true)
  fi
}

fix_lnd_permissions() {
  local lnd_dir="$1"
  local lnd_user="$2"
  local lnd_group="$3"
  if [[ -z "$lnd_dir" || -z "$lnd_user" || -z "$lnd_group" ]]; then
    print_warn "Missing LND user/group; skipping LND permissions fix"
    return 0
  fi

  local chain_dir="${lnd_dir}/data/chain/bitcoin/mainnet"
  local lnd_conf="${lnd_dir}/lnd.conf"
  if [[ -d "$lnd_dir" ]]; then
    chown "$lnd_user:$lnd_group" "$lnd_dir"
    chmod 750 "$lnd_dir"
  fi
  if [[ -f "$lnd_conf" ]]; then
    chown "$lnd_user:$lnd_group" "$lnd_conf"
    chmod 660 "$lnd_conf"
  fi
  for dir in "$lnd_dir/data" "$lnd_dir/data/chain" "$lnd_dir/data/chain/bitcoin" "$chain_dir"; do
    if [[ -d "$dir" ]]; then
      chown "$lnd_user:$lnd_group" "$dir"
      chmod 750 "$dir"
    fi
  done
  if [[ -f "$lnd_dir/tls.cert" ]]; then
    chown "$lnd_user:$lnd_group" "$lnd_dir/tls.cert"
    chmod 640 "$lnd_dir/tls.cert"
  fi
  if [[ -d "$chain_dir" ]]; then
    shopt -s nullglob
    for mac in "$chain_dir"/*.macaroon; do
      chown "$lnd_user:$lnd_group" "$mac"
      chmod 640 "$mac"
    done
    shopt -u nullglob
  fi
}

ensure_postgres_service() {
  if ! service_exists "postgresql"; then
    print_warn "PostgreSQL service not found"
    if prompt_yes_no "Install PostgreSQL now (required for reports/notifications)?" "y"; then
      if command -v apt-get >/dev/null 2>&1; then
        install_postgres_packages
      else
        print_warn "apt-get not found; install PostgreSQL manually"
        return
      fi
    else
      return
    fi
  fi
  if ! systemctl is-active --quiet postgresql; then
    if prompt_yes_no "PostgreSQL is inactive. Enable and start it now?" "y"; then
      systemctl enable --now postgresql
      print_ok "PostgreSQL started"
    else
      print_warn "PostgreSQL is required for reports/notifications"
    fi
  fi
}

get_os_codename() {
  local codename=""
  if [[ -f /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    codename="${VERSION_CODENAME:-}"
  fi
  if [[ -z "$codename" ]]; then
    codename=$(lsb_release -cs 2>/dev/null || true)
  fi
  echo "$codename"
}

detect_installed_postgres_major() {
  local version=""
  if command -v pg_lsclusters >/dev/null 2>&1; then
    version=$(pg_lsclusters 2>/dev/null | awk 'NR>1 && $4=="online" {print $1}' | sort -nr | head -n1)
    if [[ -z "$version" ]]; then
      version=$(pg_lsclusters 2>/dev/null | awk 'NR>1 {print $1}' | sort -nr | head -n1)
    fi
  fi
  if [[ -z "$version" ]]; then
    version=$(dpkg-query -W -f='${Package}\n' 'postgresql-[0-9]*' 2>/dev/null | sed 's/postgresql-//' | sort -nr | head -n1)
  fi
  echo "$version"
}

setup_postgres_repo() {
  print_step "Configuring PostgreSQL repository"
  local codename
  codename=$(get_os_codename)
  if [[ -z "$codename" ]]; then
    print_warn "Could not detect OS codename; skipping PGDG repo"
    return
  fi
  apt-get install -y ca-certificates curl gnupg
  curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc | gpg --dearmor -o /usr/share/keyrings/postgresql.gpg
  echo "deb [signed-by=/usr/share/keyrings/postgresql.gpg] http://apt.postgresql.org/pub/repos/apt ${codename}-pgdg main" \
    > /etc/apt/sources.list.d/pgdg.list
  print_ok "PostgreSQL repo ready (${codename}-pgdg)"
}

resolve_postgres_version() {
  if [[ "$POSTGRES_VERSION" =~ ^[0-9]+$ ]]; then
    return 0
  fi
  local installed
  installed=$(detect_installed_postgres_major)
  if [[ -n "$installed" ]]; then
    POSTGRES_VERSION="$installed"
    print_ok "Using installed PostgreSQL ${POSTGRES_VERSION} (no major upgrade)"
    return 0
  fi
  local versions
  versions=$(apt-cache search --names-only '^postgresql-[0-9]+$' 2>/dev/null | awk '{print $1}' | sed 's/postgresql-//' | sort -nr)
  if [[ -z "$versions" ]]; then
    print_warn "Could not detect PostgreSQL versions; falling back to 17"
    POSTGRES_VERSION="17"
    return 0
  fi
  POSTGRES_VERSION=$(echo "$versions" | head -n1)
  print_ok "Using PostgreSQL ${POSTGRES_VERSION}"
}

install_postgres_packages() {
  print_step "Installing PostgreSQL"
  apt-get update
  setup_postgres_repo
  apt-get update
  resolve_postgres_version
  apt-get install -y \
    postgresql-common \
    postgresql-client-common \
    postgresql-"${POSTGRES_VERSION}" \
    postgresql-client-"${POSTGRES_VERSION}"
  print_ok "PostgreSQL installed"
}

psql_as_postgres() {
  if command -v runuser >/dev/null 2>&1; then
    runuser -u postgres -- psql -X "$@"
  else
    sudo -u postgres psql -X "$@"
  fi
}

escape_pg_password() {
  printf '%s' "$1" | sed "s/'/''/g"
}

ensure_pg_role() {
  local role="$1"
  local options="$2"
  local password="$3"
  local exists
  exists=$(psql_as_postgres -tAc "select 1 from pg_roles where rolname='${role}'" 2>/dev/null | tr -d '[:space:]')
  if [[ "$exists" == "1" ]]; then
    psql_as_postgres -v ON_ERROR_STOP=1 -c "alter role ${role} with ${options} password '${password}'"
  else
    psql_as_postgres -v ON_ERROR_STOP=1 -c "create role ${role} with login ${options} password '${password}'"
  fi
}

ensure_pg_database() {
  local db="$1"
  local owner="$2"
  local exists
  exists=$(psql_as_postgres -tAc "select 1 from pg_database where datname='${db}'" 2>/dev/null | tr -d '[:space:]')
  if [[ "$exists" == "1" ]]; then
    psql_as_postgres -v ON_ERROR_STOP=1 -c "alter database ${db} owner to ${owner}"
  else
    psql_as_postgres -v ON_ERROR_STOP=1 -c "create database ${db} owner ${owner}"
  fi
}

provision_notifications_db() {
  if ! command -v psql >/dev/null 2>&1; then
    print_warn "psql not found; cannot provision database"
    return 1
  fi
  if ! systemctl is-active --quiet postgresql; then
    print_warn "PostgreSQL is not active; cannot provision database"
    return 1
  fi

  local admin_pass app_pass
  admin_pass=$(prompt_value "Password for ${NOTIFICATIONS_ADMIN_USER} (blank to auto-generate)")
  if [[ -z "$admin_pass" ]]; then
    admin_pass=$(openssl rand -hex 12)
  fi
  app_pass=$(prompt_value "Password for ${NOTIFICATIONS_APP_USER} (blank to auto-generate)")
  if [[ -z "$app_pass" ]]; then
    app_pass=$(openssl rand -hex 12)
  fi

  local admin_pass_esc app_pass_esc
  admin_pass_esc=$(escape_pg_password "$admin_pass")
  app_pass_esc=$(escape_pg_password "$app_pass")

  ensure_pg_role "$NOTIFICATIONS_ADMIN_USER" "createrole createdb" "$admin_pass_esc"
  ensure_pg_role "$NOTIFICATIONS_APP_USER" "" "$app_pass_esc"
  ensure_pg_database "$NOTIFICATIONS_DB_NAME" "$NOTIFICATIONS_APP_USER"

  set_env_value "NOTIFICATIONS_PG_DSN" "postgres://${NOTIFICATIONS_APP_USER}:${app_pass}@127.0.0.1:5432/${NOTIFICATIONS_DB_NAME}?sslmode=disable"
  set_env_value "NOTIFICATIONS_PG_ADMIN_DSN" "postgres://${NOTIFICATIONS_ADMIN_USER}:${admin_pass}@127.0.0.1:5432/postgres?sslmode=disable"
  print_ok "Notifications database ready (${NOTIFICATIONS_DB_NAME})"
}

run_reports_backfill() {
  local from
  local to
  from=$(prompt_value "Reports backfill FROM date (YYYY-MM-DD, blank to skip)")
  if [[ -z "$from" ]]; then
    return 0
  fi
  to=$(prompt_value "Reports backfill TO date (YYYY-MM-DD)")
  if [[ -z "$to" ]]; then
    print_warn "Missing TO date; skipping backfill"
    return 0
  fi
  if [[ ! -x /opt/lightningos/manager/lightningos-manager ]]; then
    print_warn "Manager binary not found; skipping backfill"
    return 0
  fi
  print_step "Running reports backfill (${from} -> ${to})"
  /opt/lightningos/manager/lightningos-manager reports-backfill --from "$from" --to "$to" || \
    print_warn "Reports backfill failed"
}

main() {
  show_welcome_and_license
  require_root
  print_step "LightningOS existing node setup"

  local lnd_dir
  local bitcoin_dir
  lnd_dir=$(resolve_data_dir "LND" "$DEFAULT_LND_DIR")
  bitcoin_dir=$(resolve_data_dir "Bitcoin" "$DEFAULT_BITCOIN_DIR")

  local lnd_conf="${lnd_dir}/lnd.conf"
  local btc_conf="${bitcoin_dir}/bitcoin.conf"

  if [[ -f "$btc_conf" ]]; then
    print_ok "Found bitcoin.conf at ${btc_conf}"
  else
    print_warn "bitcoin.conf not found at ${btc_conf}"
  fi

  ensure_dirs
  ensure_secrets_file
  ensure_lightningos_user

  local manager_user="lightningos"
  local manager_group="lightningos"
  print_ok "Manager service user: ${manager_user}"

  if [[ ! -f "$CONFIG_PATH" ]]; then
    cp "$REPO_ROOT/templates/lightningos.config.yaml" "$CONFIG_PATH"
  fi

  ensure_tls

  if prompt_yes_no "Install smartmontools (smartctl) for disk health?" "y"; then
    ensure_smartmontools || print_warn "SMART data may be unavailable"
  fi

  local lnd_backend
  lnd_backend=$(detect_lnd_backend "$lnd_conf")
  if [[ "$lnd_backend" == "postgres" ]]; then
    print_ok "Detected LND backend: postgres"
  elif [[ "$lnd_backend" == "bolt" ]]; then
    print_ok "Detected LND backend: bolt/sqlite"
  else
    print_warn "Could not detect LND backend"
  fi

  detect_core_service_users
  if [[ -n "$LND_USER" ]]; then
    print_ok "Detected LND service user: ${LND_USER}"
  else
    print_warn "LND service user not detected"
  fi
  if [[ -n "$BITCOIN_USER" ]]; then
    print_ok "Detected Bitcoin service user: ${BITCOIN_USER}"
  fi

  if [[ "$lnd_backend" != "postgres" ]]; then
    if prompt_yes_no "Install/enable Postgres for reports/notifications?" "y"; then
      ensure_postgres_service
    fi
  else
    ensure_postgres_service
  fi

  if prompt_yes_no "Create/ensure LightningOS database and users now?" "y"; then
    provision_notifications_db || print_warn "Database provisioning skipped"
  fi

  local notifications_dsn
  notifications_dsn=$(grep '^NOTIFICATIONS_PG_DSN=' "$SECRETS_PATH" | cut -d= -f2- || true)
  if [[ -z "$notifications_dsn" || "$notifications_dsn" == *CHANGE_ME* ]]; then
    notifications_dsn=$(prompt_value "Enter NOTIFICATIONS_PG_DSN")
    if [[ -n "$notifications_dsn" ]]; then
      set_env_value "NOTIFICATIONS_PG_DSN" "$notifications_dsn"
    fi
  fi
  local notifications_admin_dsn
  notifications_admin_dsn=$(grep '^NOTIFICATIONS_PG_ADMIN_DSN=' "$SECRETS_PATH" | cut -d= -f2- || true)
  if [[ -z "$notifications_admin_dsn" || "$notifications_admin_dsn" == *CHANGE_ME* ]]; then
    notifications_admin_dsn=$(prompt_value "Enter NOTIFICATIONS_PG_ADMIN_DSN")
    if [[ -n "$notifications_admin_dsn" ]]; then
      set_env_value "NOTIFICATIONS_PG_ADMIN_DSN" "$notifications_admin_dsn"
    fi
  fi

  if prompt_yes_no "Enable LightningOS terminal service (GoTTY)?" "n"; then
    if ! command -v tmux >/dev/null 2>&1; then
      if prompt_yes_no "tmux not found. Install it now?" "y"; then
        if command -v apt-get >/dev/null 2>&1; then
          apt-get update
          apt-get install -y tmux
        else
          print_warn "apt-get not found; install tmux manually"
        fi
      fi
    fi
    if ! command -v gotty >/dev/null 2>&1; then
      if prompt_yes_no "GoTTY not found. Install it now?" "y"; then
        install_gotty
      else
        print_warn "Terminal service requires GoTTY"
      fi
    fi
    local terminal_user
    terminal_user=$(prompt_value "Terminal operator user" "admin")
    ensure_terminal_user "$terminal_user"
    local terminal_pass
    terminal_pass=$(prompt_value "Terminal password (leave blank to auto-generate)")
    if [[ -z "$terminal_pass" ]]; then
      terminal_pass=$(openssl rand -hex 12)
    fi
    set_env_value "TERMINAL_ENABLED" "1"
    set_env_value "TERMINAL_OPERATOR_USER" "$terminal_user"
    set_env_value "TERMINAL_OPERATOR_PASSWORD" "$terminal_pass"
    set_env_value "TERMINAL_CREDENTIAL" "${terminal_user}:${terminal_pass}"
    ensure_terminal_helper
    ensure_terminal_service "$manager_user" "$manager_group"
  fi

  local membership_groups=()
  if [[ -n "$LND_GROUP" ]]; then
    membership_groups+=("$LND_GROUP")
  else
    membership_groups+=("lnd")
  fi
  if [[ -n "$BITCOIN_GROUP" ]]; then
    membership_groups+=("$BITCOIN_GROUP")
  else
    membership_groups+=("bitcoin")
  fi
  membership_groups+=("docker" "systemd-journal")
  ensure_group_membership "$manager_user" "${membership_groups[@]}"
  if [[ -n "$LND_USER" && -n "$BITCOIN_GROUP" ]]; then
    if ! id -nG "$LND_USER" | tr ' ' '\n' | grep -qx "$BITCOIN_GROUP"; then
      if prompt_yes_no "Add ${LND_USER} to ${BITCOIN_GROUP} group for Bitcoin RPC cookie access?" "y"; then
        ensure_group_membership "$LND_USER" "$BITCOIN_GROUP"
      fi
    fi
  fi
  install_lnd_fix_perms_script
  install_lnd_upgrade_script
  configure_sudoers
  ensure_manager_service "$manager_user" "$manager_group"
  if [[ -n "$LND_USER" && -n "$LND_GROUP" ]]; then
    fix_lnd_permissions "$lnd_dir" "$LND_USER" "$LND_GROUP"
  else
    print_warn "Skipping LND permissions fix (user/group not detected)"
  fi
  fix_lightningos_permissions "$manager_group"
  fix_lightningos_storage_permissions "$manager_user" "$manager_group"

  if prompt_yes_no "Install reports timer (requires Postgres)?" "y"; then
    ensure_reports_services "$manager_user" "$manager_group"
  fi

  if prompt_yes_no "Build and install manager binary now?" "y"; then
    ensure_go
    build_manager
  fi
  if prompt_yes_no "Build and install UI now?" "y"; then
    ensure_node
    build_ui
  fi

  if prompt_yes_no "Run reports backfill now?" "n"; then
    run_reports_backfill
  fi

  print_step "Enabling services"
  systemctl daemon-reload
  systemctl enable --now lightningos-manager
  if [[ -f /etc/systemd/system/lightningos-reports.timer ]]; then
    systemctl enable --now lightningos-reports.timer
  fi
  if [[ -f /etc/systemd/system/lightningos-terminal.service ]]; then
    systemctl enable --now lightningos-terminal || true
  fi

  ensure_ufw_manager_port

  print_step "Done"
  echo "Log: ${LOG_FILE}"
  echo "Check: systemctl status lightningos-manager --no-pager"
  local lan_ip
  lan_ip=$(get_lan_ip)
  if [[ -n "$lan_ip" ]]; then
    echo "Open: https://${lan_ip}:8443"
  else
    echo "Open: https://IP_DA_MAQUINA:8443"
  fi
}

main "$@"
