#!/usr/bin/env bash
set -Eeuo pipefail
set -o errtrace

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$SCRIPT_DIR"

GO_VERSION="${GO_VERSION:-1.22.7}"
GO_TARBALL_URL="https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
NODE_VERSION="${NODE_VERSION:-20}"
GOTTY_VERSION="${GOTTY_VERSION:-1.0.1}"
GOTTY_URL="https://github.com/yudai/gotty/releases/download/v${GOTTY_VERSION}/gotty_linux_amd64.tar.gz"

DEFAULT_LND_DIR="/data/lnd"
DEFAULT_BITCOIN_DIR="/data/bitcoin"
CONFIG_PATH="/etc/lightningos/config.yaml"
SECRETS_PATH="/etc/lightningos/secrets.env"

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

install_go() {
  print_step "Installing Go ${GO_VERSION}"
  rm -rf /usr/local/go
  curl -fsSL "$GO_TARBALL_URL" -o /tmp/go.tgz
  tar -C /usr/local -xzf /tmp/go.tgz
  rm -f /tmp/go.tgz
  export PATH="/usr/local/go/bin:$PATH"
  print_ok "Go installed"
}

install_node() {
  print_step "Installing Node.js ${NODE_VERSION}.x"
  if ! command -v apt-get >/dev/null 2>&1; then
    print_warn "apt-get not found; install Node.js manually and re-run."
    return 1
  fi
  curl -fsSL "https://deb.nodesource.com/setup_${NODE_VERSION}.x" | bash -
  apt-get install -y nodejs >/dev/null
  print_ok "Node.js installed"
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

ensure_tools() {
  if ! command -v go >/dev/null 2>&1; then
    print_warn "Go not found"
    if prompt_yes_no "Install Go now?" "y"; then
      install_go
    else
      print_warn "Go is required to build the manager"
      exit 1
    fi
  fi
  if ! command -v npm >/dev/null 2>&1; then
    print_warn "npm not found"
    if prompt_yes_no "Install Node.js (npm) now?" "y"; then
      install_node
    else
      print_warn "npm is required to build the UI"
      exit 1
    fi
  fi
}

build_manager() {
  print_step "Building manager"
  (cd "$REPO_ROOT" && go build -o dist/lightningos-manager ./cmd/lightningos-manager)
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
  getent group lnd >/dev/null 2>&1 && groups+=("lnd")
  getent group bitcoin >/dev/null 2>&1 && groups+=("bitcoin")
  getent group docker >/dev/null 2>&1 && groups+=("docker")
  local group_line
  group_line=$(IFS=' '; echo "${groups[*]}")
  sed -i "s|^SupplementaryGroups=.*|SupplementaryGroups=${group_line}|" "$dst"
}

ensure_reports_services() {
  cp "$REPO_ROOT/templates/systemd/lightningos-reports.service" /etc/systemd/system/lightningos-reports.service
  cp "$REPO_ROOT/templates/systemd/lightningos-reports.timer" /etc/systemd/system/lightningos-reports.timer
}

ensure_terminal_service() {
  local user="$1"
  local group="$2"
  cp "$REPO_ROOT/templates/systemd/lightningos-terminal.service" /etc/systemd/system/lightningos-terminal.service
  sed -i "s|^User=.*|User=${user}|" /etc/systemd/system/lightningos-terminal.service
  sed -i "s|^Group=.*|Group=${group}|" /etc/systemd/system/lightningos-terminal.service
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

main() {
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

  local rpc_user rpc_pass
  rpc_user=$(read_conf_value "$btc_conf" "rpcuser" || true)
  rpc_pass=$(read_conf_value "$btc_conf" "rpcpassword" || true)
  if [[ -z "$rpc_user" ]]; then
    rpc_user=$(prompt_value "Enter bitcoin RPC user")
  fi
  if [[ -z "$rpc_pass" ]]; then
    rpc_pass=$(prompt_value "Enter bitcoin RPC password")
  fi

  ensure_dirs
  ensure_secrets_file
  set_env_value "BITCOIN_RPC_USER" "$rpc_user"
  set_env_value "BITCOIN_RPC_PASS" "$rpc_pass"

  if [[ ! -f "$CONFIG_PATH" ]]; then
    cp "$REPO_ROOT/templates/lightningos.config.yaml" "$CONFIG_PATH"
  fi

  ensure_tls

  local lnd_backend
  lnd_backend=$(detect_lnd_backend "$lnd_conf")
  if [[ "$lnd_backend" == "postgres" ]]; then
    print_ok "Detected LND backend: postgres"
  elif [[ "$lnd_backend" == "bolt" ]]; then
    print_ok "Detected LND backend: bolt/sqlite"
  else
    print_warn "Could not detect LND backend"
  fi

  if [[ "$lnd_backend" != "postgres" ]]; then
    if prompt_yes_no "Install Postgres for reports/notifications?" "y"; then
      if command -v apt-get >/dev/null 2>&1; then
        print_step "Installing Postgres"
        apt-get update
        apt-get install -y postgresql postgresql-client
        systemctl enable --now postgresql
      else
        print_warn "apt-get not found; install Postgres manually"
      fi
    fi
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
    terminal_user=$(prompt_value "Terminal service user" "admin")
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
    ensure_terminal_service "$terminal_user" "$terminal_user"
  fi

  local manager_user
  manager_user=$(prompt_value "Manager service user" "admin")
  local manager_group
  manager_group=$(prompt_value "Manager service group" "$manager_user")
  if ! id "$manager_user" >/dev/null 2>&1; then
    print_warn "User ${manager_user} does not exist; edit systemd unit manually later"
  fi
  if prompt_yes_no "Add ${manager_user} to lnd/bitcoin/docker groups when available?" "y"; then
    ensure_group_membership "$manager_user" lnd bitcoin docker systemd-journal
  fi
  ensure_manager_service "$manager_user" "$manager_group"

  if prompt_yes_no "Install reports timer (requires Postgres)?" "y"; then
    ensure_reports_services
  fi

  if prompt_yes_no "Build and install manager binary now?" "y"; then
    ensure_tools
    build_manager
  fi
  if prompt_yes_no "Build and install UI now?" "y"; then
    ensure_tools
    build_ui
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

  print_step "Done"
  echo "Check: systemctl status lightningos-manager --no-pager"
  echo "Health: curl -k https://127.0.0.1:8443/api/health"
}

main "$@"
