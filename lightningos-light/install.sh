#!/usr/bin/env bash
set -Eeuo pipefail
set -o errtrace

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$SCRIPT_DIR"

LND_VERSION="${LND_VERSION:-0.20.0-beta}"
LND_URL_DEFAULT="https://github.com/lightningnetwork/lnd/releases/download/v${LND_VERSION}/lnd-linux-amd64-v${LND_VERSION}.tar.gz"
LND_URL="${LND_URL:-$LND_URL_DEFAULT}"

GO_VERSION="${GO_VERSION:-1.22.7}"
GO_TARBALL_URL="https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"

LND_DIR="/data/lnd"
LND_CONF="${LND_DIR}/lnd.conf"

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

strip_crlf() {
  local target="$1"
  if [[ -f "$target" ]]; then
    sed -i 's/\r$//' "$target"
  fi
}

trap 'echo ""; echo "Installation failed during: ${CURRENT_STEP:-unknown}"; echo "Last command: $BASH_COMMAND"; echo "Check: systemctl status postgresql --no-pager"; echo "Also: journalctl -u postgresql -n 50 --no-pager"; exit 1' ERR

psql_as_postgres() {
  if command -v runuser >/dev/null 2>&1; then
    PGCONNECT_TIMEOUT=5 runuser -u postgres -- psql -X "$@"
  else
    PGCONNECT_TIMEOUT=5 sudo -u postgres psql -X "$@"
  fi
}

psql_exec() {
  local label="$1"
  shift
  local out
  if ! out=$(psql_as_postgres -v ON_ERROR_STOP=1 "$@" 2>&1); then
    print_warn "$label failed: $out"
    return 1
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
  if [[ -n "$home" && ! -d "$home" ]]; then
    mkdir -p "$home"
    chown "$user:$user" "$home"
    chmod 750 "$home"
  fi
}

ensure_group_member() {
  local user="$1"
  local group="$2"
  if ! getent group "$group" >/dev/null 2>&1; then
    groupadd --system "$group"
  fi
  if id -nG "$user" | tr ' ' '\n' | grep -qx "$group"; then
    return
  fi
  usermod -a -G "$group" "$user"
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
    if [[ "$major" -gt 1 || ( "$major" -eq 1 && "$minor" -ge 22 ) ]]; then
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
  mkdir -p /etc/lightningos /etc/lightningos/tls /opt/lightningos/manager /opt/lightningos/ui /var/lib/lightningos /var/log/lightningos /var/log/lnd
  chmod 750 /etc/lightningos
  chmod 750 /var/lib/lightningos
  print_ok "Directories ready"
}

prepare_lnd_data_dir() {
  print_step "Preparing LND data directory"
  mkdir -p /data "$LND_DIR" /var/log/lnd
  if [[ -d /var/lib/lnd && -n "$(ls -A /var/lib/lnd 2>/dev/null)" ]]; then
    if [[ -d "$LND_DIR" && -z "$(ls -A "$LND_DIR" 2>/dev/null)" ]]; then
      print_warn "Existing /var/lib/lnd detected; copying data to /data/lnd"
      cp -a /var/lib/lnd/. "$LND_DIR"/
    fi
  fi
  chown -R lnd:lnd "$LND_DIR" /var/log/lnd
  chmod 750 "$LND_DIR" /var/log/lnd
  if [[ ! -e /home/lnd/.lnd ]]; then
    ln -s "$LND_DIR" /home/lnd/.lnd
    chown -h lnd:lnd /home/lnd/.lnd
  fi
  if [[ ! -f "$LND_DIR/password.txt" ]]; then
    touch "$LND_DIR/password.txt"
    chown lnd:lnd "$LND_DIR/password.txt"
    chmod 660 "$LND_DIR/password.txt"
  fi
  print_ok "LND data directory ready"
}

fix_permissions() {
  print_step "Fixing permissions"
  chown root:lightningos /etc/lightningos /etc/lightningos/tls
  chmod 750 /etc/lightningos /etc/lightningos/tls
  if [[ -f /etc/lightningos/config.yaml ]]; then
    chown root:lightningos /etc/lightningos/config.yaml
    chmod 640 /etc/lightningos/config.yaml
  fi
  if [[ -f /etc/lightningos/tls/server.crt ]]; then
    chown root:lightningos /etc/lightningos/tls/server.crt
    chmod 640 /etc/lightningos/tls/server.crt
  fi
  if [[ -f /etc/lightningos/tls/server.key ]]; then
    chown root:lightningos /etc/lightningos/tls/server.key
    chmod 640 /etc/lightningos/tls/server.key
  fi
  if [[ -f /etc/lightningos/secrets.env ]]; then
    chown root:lightningos /etc/lightningos/secrets.env
    chmod 660 /etc/lightningos/secrets.env
  fi
  if [[ -f "$LND_CONF" ]]; then
    chown lnd:lnd "$LND_CONF"
    chmod 660 "$LND_CONF"
  fi
  if [[ -f "$LND_DIR/tls.cert" ]]; then
    chown lnd:lnd "$LND_DIR/tls.cert"
    chmod 640 "$LND_DIR/tls.cert"
  fi
  if [[ -f "$LND_DIR/password.txt" ]]; then
    chown lnd:lnd "$LND_DIR/password.txt"
    chmod 660 "$LND_DIR/password.txt"
  fi
  if [[ -f "$LND_DIR/data/chain/bitcoin/mainnet/admin.macaroon" ]]; then
    chown lnd:lnd "$LND_DIR/data/chain/bitcoin/mainnet/admin.macaroon"
    chmod 640 "$LND_DIR/data/chain/bitcoin/mainnet/admin.macaroon"
  fi
  chown -R lightningos:lightningos /var/lib/lightningos /var/log/lightningos
  print_ok "Permissions updated"
}

copy_templates() {
  print_step "Copying config templates"
  if [[ ! -f /etc/lightningos/config.yaml ]]; then
    cp "$REPO_ROOT/templates/lightningos.config.yaml" /etc/lightningos/config.yaml
  fi
  if [[ ! -f /etc/lightningos/secrets.env ]]; then
    cp "$REPO_ROOT/templates/secrets.env" /etc/lightningos/secrets.env
    chown root:lightningos /etc/lightningos/secrets.env
    chmod 660 /etc/lightningos/secrets.env
  fi
  if [[ ! -s "$LND_CONF" ]]; then
    cp "$REPO_ROOT/templates/lnd.conf" "$LND_CONF"
    chown lnd:lnd "$LND_CONF"
    chmod 660 "$LND_CONF"
  fi
  strip_crlf /etc/lightningos/config.yaml
  strip_crlf "$LND_CONF"
  print_ok "Templates copied"
}

migrate_lnd_paths() {
  if [[ -f /etc/lightningos/config.yaml ]]; then
    if grep -q "/var/lib/lnd" /etc/lightningos/config.yaml; then
      sed -i 's#/var/lib/lnd#/data/lnd#g' /etc/lightningos/config.yaml
      print_ok "Updated LND paths in /etc/lightningos/config.yaml"
    fi
  fi
}

validate_lnd_conf() {
  if [[ ! -f "$LND_CONF" ]]; then
    print_warn "$LND_CONF missing"
    return
  fi
  if command -v runuser >/dev/null 2>&1; then
    if ! runuser -u lnd -- test -r "$LND_CONF"; then
      print_warn "lnd.conf not readable by user lnd"
    fi
  fi
  if ! grep -q '^bitcoin.mainnet=1' "$LND_CONF"; then
    print_warn "lnd.conf missing bitcoin.mainnet=1"
  fi
  if ! grep -q '^bitcoin.active=1' "$LND_CONF"; then
    print_warn "lnd.conf missing bitcoin.active=1"
  fi
  if ! grep -q '^bitcoin.node=bitcoind' "$LND_CONF"; then
    print_warn "lnd.conf missing bitcoin.node=bitcoind"
  fi
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
    pw=$( (set +o pipefail; tr -dc A-Za-z0-9 </dev/urandom | head -c 24) )
    if [[ -z "$pw" ]]; then
      pw=$( (set +o pipefail; tr -dc A-Za-z0-9 </dev/urandom | head -c 32) )
    fi
    psql_exec "Create role" -c "create role ${db_user} with login password '${pw}'"
    update_dsn "$db_user" "$pw" "$db_name"
    print_ok "Role created"
  fi
  if [[ "$db_exists" != "1" ]]; then
    psql_exec "Create database" -c "create database ${db_name} owner ${db_user}"
    print_ok "Database created"
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
  chmod 660 /etc/lightningos/secrets.env

  if ! grep -q '^db.postgres.dsn=' "$LND_CONF"; then
    echo "db.postgres.dsn=${dsn}" >> "$LND_CONF"
  else
    sed -i "s|^db.postgres.dsn=.*|db.postgres.dsn=${dsn}|" "$LND_CONF"
  fi
  chmod 660 "$LND_CONF"
}

ensure_dsn() {
  local db_user="$1"
  local db_name="$2"
  local current
  current=$(grep '^LND_PG_DSN=' /etc/lightningos/secrets.env | cut -d= -f2- || true)
  if [[ -z "$current" || "$current" == *"CHANGE_ME"* ]]; then
    local pw
    pw=$( (set +o pipefail; tr -dc A-Za-z0-9 </dev/urandom | head -c 24) )
    if [[ -z "$pw" ]]; then
      pw=$( (set +o pipefail; tr -dc A-Za-z0-9 </dev/urandom | head -c 32) )
    fi
    psql_exec "Alter role password" -c "alter role ${db_user} with password '${pw}'"
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
  local lnd_bin lncli_bin
  lnd_bin=$(find "$tmp" -type f -name "lnd" | head -n1)
  lncli_bin=$(find "$tmp" -type f -name "lncli" | head -n1)
  if [[ -z "$lnd_bin" || -z "$lncli_bin" ]]; then
    echo "LND tarball structure unexpected" >&2
    echo "Contents:" >&2
    find "$tmp" -maxdepth 3 -type f >&2
    exit 1
  fi
  install -m 0755 "$lnd_bin" /usr/local/bin/lnd
  install -m 0755 "$lncli_bin" /usr/local/bin/lncli
  rm -rf "$tmp"
  print_ok "LND installed"
}

build_ui() {
  print_step "Building UI"
  (cd "$REPO_ROOT/ui" && npm install && npm run build)
  print_ok "UI build complete"
}

manager_build_stamp() {
  if command -v git >/dev/null 2>&1 && git -C "$REPO_ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    local rev dirty
    rev=$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo "nogit")
    dirty=$(git -C "$REPO_ROOT" status --porcelain 2>/dev/null | sha256sum | awk '{print $1}')
    echo "${rev}-${dirty}"
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    find "$REPO_ROOT" -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) -print0 \
      | sort -z \
      | xargs -0 sha256sum \
      | sha256sum \
      | awk '{print $1}'
    return
  fi
  date +%s
}

ui_build_stamp() {
  if command -v git >/dev/null 2>&1 && git -C "$REPO_ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    local rev dirty
    rev=$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo "nogit")
    dirty=$(git -C "$REPO_ROOT" status --porcelain -- ui 2>/dev/null | sha256sum | awk '{print $1}')
    echo "${rev}-${dirty}"
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    find "$REPO_ROOT/ui" -type f ! -path "*/node_modules/*" -print0 \
      | sort -z \
      | xargs -0 sha256sum \
      | sha256sum \
      | awk '{print $1}'
    return
  fi
  date +%s
}

install_manager() {
  print_step "Building LightningOS Manager"
  local stamp_file="/opt/lightningos/manager/.build_stamp"
  local current_stamp
  current_stamp=$(manager_build_stamp)
  if [[ -x /opt/lightningos/manager/lightningos-manager && -f "$stamp_file" ]]; then
    local existing_stamp
    existing_stamp=$(cat "$stamp_file" 2>/dev/null || true)
    if [[ -n "$current_stamp" && "$existing_stamp" == "$current_stamp" ]]; then
      print_ok "Manager already installed (up-to-date)"
      return
    fi
  fi

  if [[ -x "$REPO_ROOT/dist/lightningos-manager" ]]; then
    install -m 0755 "$REPO_ROOT/dist/lightningos-manager" /opt/lightningos/manager/lightningos-manager
    if [[ -n "$current_stamp" ]]; then
      echo "$current_stamp" > "$stamp_file"
      chmod 0644 "$stamp_file"
    fi
    print_ok "Manager installed from dist/"
    return
  fi

  local go_env
  go_env="GOPATH=/opt/lightningos/go GOCACHE=/opt/lightningos/go-cache GOMODCACHE=/opt/lightningos/go/pkg/mod"
  mkdir -p /opt/lightningos/go /opt/lightningos/go-cache /opt/lightningos/go/pkg/mod

  print_step "Downloading Go modules"
  (cd "$REPO_ROOT" && env $go_env GOFLAGS=-mod=mod go mod download)
  print_ok "Go modules ready"

  print_step "Compiling LightningOS Manager"
  (cd "$REPO_ROOT" && env $go_env GOFLAGS=-mod=mod go build -o /opt/lightningos/manager/lightningos-manager ./cmd/lightningos-manager)
  if [[ -n "$current_stamp" ]]; then
    echo "$current_stamp" > "$stamp_file"
    chmod 0644 "$stamp_file"
  fi
  print_ok "Manager built and installed"
}

install_ui() {
  print_step "Installing UI"
  local stamp_file="/opt/lightningos/ui/.build_stamp"
  local current_stamp
  local need_build="true"
  current_stamp=$(ui_build_stamp)
  if [[ -d "$REPO_ROOT/ui/dist" && -f "$stamp_file" ]]; then
    local existing_stamp
    existing_stamp=$(cat "$stamp_file" 2>/dev/null || true)
    if [[ -n "$current_stamp" && "$existing_stamp" == "$current_stamp" ]]; then
      need_build="false"
    fi
  fi
  if [[ "$need_build" == "true" ]]; then
    build_ui
  fi
  rm -rf /opt/lightningos/ui/*
  cp -a "$REPO_ROOT/ui/dist/." /opt/lightningos/ui/
  if [[ -n "$current_stamp" ]]; then
    echo "$current_stamp" > "$stamp_file"
    chmod 0644 "$stamp_file"
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
  strip_crlf /etc/systemd/system/lnd.service
  strip_crlf /etc/systemd/system/lightningos-manager.service
  systemctl daemon-reload
  systemctl enable --now postgresql
  systemctl enable --now lnd
  systemctl enable --now lightningos-manager
  systemctl restart lnd >/dev/null 2>&1 || true
  systemctl restart lightningos-manager >/dev/null 2>&1 || true
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

show_manager_logs() {
  if command -v journalctl >/dev/null 2>&1; then
    print_warn "Recent lightningos-manager logs:"
    journalctl -u lightningos-manager -n 200 --no-pager || true
  else
    print_warn "journalctl not available; skipping logs"
  fi
}

verify_manager_listener() {
  print_step "Verifying manager listener"
  if ! systemctl is-active --quiet lightningos-manager; then
    print_warn "lightningos-manager is not active"
    if command -v systemctl >/dev/null 2>&1; then
      systemctl status lightningos-manager --no-pager || true
    fi
    show_manager_logs
    return
  fi

  if command -v ss >/dev/null 2>&1; then
    if ss -ltn | grep -q ':8443'; then
      print_ok "Port 8443 is listening"
    else
      print_warn "Port 8443 is not listening"
      show_manager_logs
    fi
  else
    print_warn "ss not available; skipping port check"
  fi

  if command -v curl >/dev/null 2>&1; then
    if curl -sk --max-time 3 https://127.0.0.1:8443/api/health >/dev/null; then
      print_ok "Health endpoint reachable"
    else
      print_warn "Health endpoint not reachable"
    fi
  fi
}

main() {
  require_root
  print_step "LightningOS Light installation starting"
  ensure_user lnd /home/lnd
  ensure_user lightningos /var/lib/lightningos
  ensure_group_member lightningos lnd
  install_packages
  install_go
  install_node
  ensure_dirs
  prepare_lnd_data_dir
  copy_templates
  migrate_lnd_paths
  validate_lnd_conf
  fix_permissions
  postgres_setup
  install_lnd
  install_manager
  install_ui
  generate_tls
  fix_permissions
  install_systemd
  service_status_summary
  verify_manager_listener
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
