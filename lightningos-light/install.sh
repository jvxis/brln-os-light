#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$SCRIPT_DIR"

LND_VERSION="${LND_VERSION:-0.20.0-beta}"
LND_URL_DEFAULT="https://github.com/lightningnetwork/lnd/releases/download/v${LND_VERSION}/lnd-linux-amd64-v${LND_VERSION}.tar.gz"
LND_URL="${LND_URL:-$LND_URL_DEFAULT}"

GO_VERSION="${GO_VERSION:-1.21.13}"
GO_TARBALL_URL="https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"

CURRENT_STEP=""
LOG_FILE="/var/log/lightningos-install.log"

mkdir -p /var/log
exec > >(tee -a "$LOG_FILE") 2>&1

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

trap 'echo ""; echo "Installation failed during: ${CURRENT_STEP:-unknown}"; echo "Last command: $BASH_COMMAND"; echo "Check: systemctl status postgresql --no-pager"; echo "Also: journalctl -u postgresql -n 50 --no-pager"; exit 1' ERR

psql_as_postgres() {
  if command -v runuser >/dev/null 2>&1; then
    PGCONNECT_TIMEOUT=5 runuser -u postgres -- psql "$@"
  else
    PGCONNECT_TIMEOUT=5 sudo -u postgres psql "$@"
  fi
}

wait_for_postgres() {
  if ! command -v pg_isready >/dev/null 2>&1; then
    sleep 2
    return 0
  fi

  local retries=20
  local i
  for i in $(seq 1 "$retries"); do
    if pg_isready -q; then
      return 0
    fi
    sleep 1
  done
  return 1
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

ensure_user() {
  local user="$1"
  local home="$2"
  if ! id "$user" >/dev/null 2>&1; then
    useradd --system --home "$home" --shell /usr/sbin/nologin "$user"
  fi
}

install_packages() {
  print_step "Installing base packages"
  apt-get update
  apt-get install -y postgresql smartmontools curl jq ca-certificates openssl build-essential git
  print_ok "Base packages installed"
}

install_go() {
  print_step "Installing Go ${GO_VERSION}"
  if command -v go >/dev/null 2>&1; then
    local current major minor
    current=$(go version | awk '{print $3}' | sed 's/go//')
    major=$(echo "$current" | cut -d. -f1)
    minor=$(echo "$current" | cut -d. -f2)
    if [[ "$major" -gt 1 || ( "$major" -eq 1 && "$minor" -ge 21 ) ]]; then
      export PATH="/usr/local/go/bin:$PATH"
      print_ok "Go already installed ($current)"
      return
    fi
  fi

  rm -rf /usr/local/go
  curl -L "$GO_TARBALL_URL" -o /tmp/go.tgz
  tar -C /usr/local -xzf /tmp/go.tgz
  rm -f /tmp/go.tgz
  export PATH="/usr/local/go/bin:$PATH"
  print_ok "Go installed"
}

install_node() {
  print_step "Installing Node.js 18.x"
  if command -v node >/dev/null 2>&1; then
    local major
    major=$(node -v | sed 's/v//' | cut -d. -f1)
    if [[ "$major" -ge 18 ]]; then
      print_ok "Node.js already installed ($(node -v))"
      return
    fi
  fi

  curl -fsSL https://deb.nodesource.com/setup_18.x | bash -
  apt-get install -y nodejs >/dev/null
  print_ok "Node.js installed"
}

ensure_dirs() {
  print_step "Preparing directories"
  mkdir -p /etc/lightningos /etc/lightningos/tls /etc/lnd /opt/lightningos/manager /opt/lightningos/ui /var/lib/lightningos /var/log/lightningos
  chmod 750 /etc/lightningos
  chmod 750 /var/lib/lightningos
  print_ok "Directories ready"
}

copy_templates() {
  print_step "Copying config templates"
  if [[ ! -f /etc/lightningos/config.yaml ]]; then
    cp "$REPO_ROOT/templates/lightningos.config.yaml" /etc/lightningos/config.yaml
  fi
  if [[ ! -f /etc/lightningos/secrets.env ]]; then
    cp "$REPO_ROOT/templates/secrets.env" /etc/lightningos/secrets.env
    chmod 600 /etc/lightningos/secrets.env
  fi
  if [[ ! -f /etc/lnd/lnd.conf ]]; then
    cp "$REPO_ROOT/templates/lnd.conf" /etc/lnd/lnd.conf
    chmod 640 /etc/lnd/lnd.conf
  fi
  if [[ ! -f /etc/lnd/lnd.user.conf ]]; then
    cp "$REPO_ROOT/templates/lnd.user.conf" /etc/lnd/lnd.user.conf
    chmod 640 /etc/lnd/lnd.user.conf
  fi
  print_ok "Templates copied"
}

postgres_setup() {
  print_step "Configuring PostgreSQL"
  systemctl enable --now postgresql >/dev/null 2>&1 || true
  print_ok "PostgreSQL service: $(systemctl is-active postgresql 2>/dev/null || echo unknown)"
  if command -v pg_isready >/dev/null 2>&1; then
    if pg_isready -q; then
      print_ok "pg_isready OK"
    else
      print_warn "pg_isready not ready: $(pg_isready 2>&1 || true)"
    fi
  fi
  if ! wait_for_postgres; then
    print_warn "PostgreSQL did not become ready"
    print_warn "Try: systemctl status postgresql --no-pager"
    exit 1
  fi
  local db_user="lndpg"
  local db_name="lnd"
  local role_exists db_exists
  if ! role_exists=$(psql_as_postgres -tAc "select 1 from pg_roles where rolname='${db_user}'" 2>&1); then
    print_warn "PostgreSQL access failed: $role_exists"
    print_warn "Try: systemctl status postgresql --no-pager"
    exit 1
  fi
  role_exists=$(echo "$role_exists" | tr -d '[:space:]')
  if ! db_exists=$(psql_as_postgres -tAc "select 1 from pg_database where datname='${db_name}'" 2>&1); then
    print_warn "PostgreSQL access failed: $db_exists"
    print_warn "Try: systemctl status postgresql --no-pager"
    exit 1
  fi
  db_exists=$(echo "$db_exists" | tr -d '[:space:]')

  print_ok "Role exists: ${role_exists:-0}"
  print_ok "Database exists: ${db_exists:-0}"

  if [[ "$role_exists" != "1" ]]; then
    local pw
    pw=$(tr -dc A-Za-z0-9 </dev/urandom | head -c 24)
    psql_as_postgres -c "create role ${db_user} with login password '${pw}'"
    update_dsn "$db_user" "$pw" "$db_name"
  fi
  if [[ "$db_exists" != "1" ]]; then
    psql_as_postgres -c "create database ${db_name} owner ${db_user}"
  fi
  if [[ "$role_exists" == "1" ]]; then
    ensure_dsn "$db_user" "$db_name"
  fi
  print_ok "PostgreSQL ready"
}

update_dsn() {
  local db_user="$1"
  local db_pass="$2"
  local db_name="$3"
  local dsn="postgres://${db_user}:${db_pass}@127.0.0.1:5432/${db_name}?sslmode=disable"
  if grep -q '^LND_PG_DSN=' /etc/lightningos/secrets.env; then
    sed -i "s|^LND_PG_DSN=.*|LND_PG_DSN=${dsn}|" /etc/lightningos/secrets.env
  else
    echo "LND_PG_DSN=${dsn}" >> /etc/lightningos/secrets.env
  fi
  chmod 600 /etc/lightningos/secrets.env

  if ! grep -q '^db.postgres.dsn=' /etc/lnd/lnd.conf; then
    echo "db.postgres.dsn=${dsn}" >> /etc/lnd/lnd.conf
  else
    sed -i "s|^db.postgres.dsn=.*|db.postgres.dsn=${dsn}|" /etc/lnd/lnd.conf
  fi
  chmod 640 /etc/lnd/lnd.conf
}

ensure_dsn() {
  local db_user="$1"
  local db_name="$2"
  local current
  current=$(grep '^LND_PG_DSN=' /etc/lightningos/secrets.env | cut -d= -f2- || true)
  if [[ -z "$current" || "$current" == *"CHANGE_ME"* ]]; then
    local pw
    pw=$(tr -dc A-Za-z0-9 </dev/urandom | head -c 24)
    psql_as_postgres -c "alter role ${db_user} with password '${pw}'"
    update_dsn "$db_user" "$pw" "$db_name"
  fi
}

install_lnd() {
  print_step "Installing LND ${LND_VERSION}"
  if [[ -x /usr/local/bin/lnd && -x /usr/local/bin/lncli ]]; then
    print_ok "LND already installed"
    return
  fi

  local tmp
  tmp=$(mktemp -d)
  curl -L "$LND_URL" -o "$tmp/lnd.tar.gz"
  tar -xzf "$tmp/lnd.tar.gz" -C "$tmp"
  local bin_dir
  bin_dir=$(find "$tmp" -maxdepth 2 -type d -name "lnd-*-linux-amd64" | head -n1)
  if [[ -z "$bin_dir" ]]; then
    echo "LND tarball structure unexpected" >&2
    exit 1
  fi
  install -m 0755 "$bin_dir/lnd" /usr/local/bin/lnd
  install -m 0755 "$bin_dir/lncli" /usr/local/bin/lncli
  rm -rf "$tmp"
  print_ok "LND installed"
}

build_ui() {
  print_step "Building UI"
  (cd "$REPO_ROOT/ui" && npm install && npm run build)
  print_ok "UI build complete"
}

install_manager() {
  print_step "Building LightningOS Manager"
  if [[ -x /opt/lightningos/manager/lightningos-manager ]]; then
    print_ok "Manager already installed"
    return
  fi

  if [[ -x "$REPO_ROOT/dist/lightningos-manager" ]]; then
    install -m 0755 "$REPO_ROOT/dist/lightningos-manager" /opt/lightningos/manager/lightningos-manager
    print_ok "Manager installed from dist/"
    return
  fi

  (cd "$REPO_ROOT" && go build -o /opt/lightningos/manager/lightningos-manager ./cmd/lightningos-manager)
  print_ok "Manager built and installed"
}

install_ui() {
  print_step "Installing UI"
  if [[ -d "$REPO_ROOT/ui/dist" ]]; then
    rm -rf /opt/lightningos/ui/*
    cp -a "$REPO_ROOT/ui/dist/." /opt/lightningos/ui/
  else
    build_ui
    rm -rf /opt/lightningos/ui/*
    cp -a "$REPO_ROOT/ui/dist/." /opt/lightningos/ui/
  fi
  print_ok "UI installed"
}

generate_tls() {
  print_step "Generating TLS certificate"
  if [[ -f /etc/lightningos/tls/server.crt && -f /etc/lightningos/tls/server.key ]]; then
    print_ok "TLS already exists"
    return
  fi
  openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
    -subj "/CN=localhost" \
    -keyout /etc/lightningos/tls/server.key \
    -out /etc/lightningos/tls/server.crt
  print_ok "TLS generated"
}

install_systemd() {
  print_step "Installing systemd services"
  cp "$REPO_ROOT/templates/systemd/lnd.service" /etc/systemd/system/lnd.service
  cp "$REPO_ROOT/templates/systemd/lightningos-manager.service" /etc/systemd/system/lightningos-manager.service
  systemctl daemon-reload
  systemctl enable --now postgresql
  systemctl enable --now lnd
  systemctl enable --now lightningos-manager
  print_ok "Services enabled and started"
}

service_status_summary() {
  print_step "Service status summary"
  for svc in postgresql lnd lightningos-manager; do
    if systemctl is-active --quiet "$svc"; then
      print_ok "$svc is active"
    else
      print_warn "$svc is not active"
    fi
  done
}

main() {
  require_root
  print_step "LightningOS Light installation starting"
  ensure_user lnd /var/lib/lnd
  ensure_user lightningos /var/lib/lightningos
  install_packages
  install_go
  install_node
  ensure_dirs
  copy_templates
  postgres_setup
  install_lnd
  install_manager
  install_ui
  generate_tls
  install_systemd
  service_status_summary
  local_ip=$(get_lan_ip)
  echo ""
  echo "Installation complete."
  echo "Open the UI from another machine on the same LAN:"
  if [[ -n "$local_ip" ]]; then
    echo "  https://$local_ip:8443"
  else
    echo "  https://<SERVER_LAN_IP>:8443"
  fi
  echo ""
  echo "Next steps:"
  echo "  1) Open the URL above"
  echo "  2) Complete the wizard (Bitcoin RPC + wallet)"
  echo "  3) Go to the dashboard"
}

main "$@"
