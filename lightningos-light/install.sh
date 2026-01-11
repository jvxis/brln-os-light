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

NODE_VERSION="${NODE_VERSION:-current}"

POSTGRES_VERSION="${POSTGRES_VERSION:-latest}"

ALLOW_STOP_UNATTENDED_UPGRADES="${ALLOW_STOP_UNATTENDED_UPGRADES:-1}"
UNATTENDED_NOTICE_SHOWN=0

LND_DIR="/data/lnd"
LND_CONF="${LND_DIR}/lnd.conf"
LND_FIX_PERMS_SCRIPT="/usr/local/sbin/lightningos-fix-lnd-perms"

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

resolve_postgres_version() {
  if [[ "$POSTGRES_VERSION" =~ ^[0-9]+$ ]]; then
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

wait_for_apt_locks() {
  local retries=60
  local i
  for i in $(seq 1 "$retries"); do
    local has_lock="false"
    if pgrep -x unattended-upgrades >/dev/null 2>&1 || pgrep -x unattended-upgr >/dev/null 2>&1; then
      has_lock="true"
      if [[ "$ALLOW_STOP_UNATTENDED_UPGRADES" == "1" ]]; then
        stop_unattended_upgrades
      elif [[ "$UNATTENDED_NOTICE_SHOWN" -eq 0 ]]; then
        print_warn "unattended-upgrades is running; set ALLOW_STOP_UNATTENDED_UPGRADES=1 to stop it automatically"
        UNATTENDED_NOTICE_SHOWN=1
      fi
    fi
    if pgrep -x apt-get >/dev/null 2>&1 || pgrep -x dpkg >/dev/null 2>&1; then
      has_lock="true"
    fi
    if [[ "$has_lock" == "true" ]]; then
      print_warn "Waiting for apt/dpkg lock... (attempt ${i}/${retries})"
      if command -v ps >/dev/null 2>&1; then
        ps -eo pid,comm,args | awk '/(apt-get|apt|dpkg|unattended)/ && !/awk/ {print "  " $0}'
      fi
      sleep 5
      continue
    fi
    return 0
  done
  return 1
}

apt_get() {
  local attempt
  local log
  log=$(mktemp)
  for attempt in $(seq 1 5); do
    if ! wait_for_apt_locks; then
      print_warn "apt/dpkg lock wait timed out"
    fi
    if apt-get "$@" 2>&1 | tee "$log"; then
      rm -f "$log"
      return 0
    fi
    if grep -q "Could not get lock" "$log"; then
      print_warn "apt lock busy; waiting before retry"
      sleep 5
      continue
    fi
    cat "$log" >&2
    rm -f "$log"
    return 1
  done
  cat "$log" >&2
  rm -f "$log"
  return 1
}

stop_unattended_upgrades() {
  print_warn "Stopping unattended-upgrades to release apt locks"
  systemctl stop unattended-upgrades >/dev/null 2>&1 || true
  systemctl stop apt-daily.service apt-daily-upgrade.service >/dev/null 2>&1 || true
  systemctl stop apt-daily.timer apt-daily-upgrade.timer >/dev/null 2>&1 || true
}

setup_postgres_repo() {
  print_step "Configuring PostgreSQL repository"
  local codename
  codename=$(get_os_codename)
  if [[ -z "$codename" ]]; then
    print_warn "Could not detect OS codename; skipping PGDG repo"
    return
  fi
  apt_get install -y ca-certificates curl gnupg
  curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc | gpg --dearmor -o /usr/share/keyrings/postgresql.gpg
  echo "deb [signed-by=/usr/share/keyrings/postgresql.gpg] http://apt.postgresql.org/pub/repos/apt ${codename}-pgdg main" \
    > /etc/apt/sources.list.d/pgdg.list
  print_ok "PostgreSQL repo ready (${codename}-pgdg)"
}

setup_tor_repo() {
  print_step "Configuring Tor repository"
  local codename
  codename=$(get_os_codename)
  if [[ -z "$codename" ]]; then
    print_warn "Could not detect OS codename; skipping Tor repo"
    return
  fi
  apt_get install -y ca-certificates curl gnupg
  if ! curl -fsI "https://deb.torproject.org/torproject.org/dists/${codename}/InRelease" >/dev/null 2>&1; then
    print_warn "Tor repo not available for ${codename}, falling back to jammy"
    codename="jammy"
  fi
  curl -fsSL https://deb.torproject.org/torproject.org/A3C4F0F979CAA22CDBA8F512EE8CBC9E886DDD89.asc \
    | gpg --dearmor \
    | tee /usr/share/keyrings/tor-archive-keyring.gpg >/dev/null
  cat > /etc/apt/sources.list.d/tor.list <<EOF
deb     [arch=amd64 signed-by=/usr/share/keyrings/tor-archive-keyring.gpg] https://deb.torproject.org/torproject.org ${codename} main
deb-src [arch=amd64 signed-by=/usr/share/keyrings/tor-archive-keyring.gpg] https://deb.torproject.org/torproject.org ${codename} main
EOF
  print_ok "Tor repo ready (${codename})"
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

create_lnd_user() {
  print_step "Ensuring user lnd"
  if id lnd >/dev/null 2>&1; then
    local home shell
    home=$(getent passwd lnd | cut -d: -f6)
    shell=$(getent passwd lnd | cut -d: -f7)
    if [[ "$home" != "/home/lnd" ]]; then
      usermod -d /home/lnd -m lnd
    fi
    if [[ "$shell" != "/bin/bash" ]]; then
      usermod -s /bin/bash lnd
    fi
  else
    if command -v adduser >/dev/null 2>&1; then
      adduser --disabled-password --gecos "" lnd
    else
      useradd -m -d /home/lnd -s /bin/bash lnd
    fi
  fi
  ensure_group_member lnd bitcoin
  print_ok "User lnd ready"
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

configure_sudoers() {
  print_step "Configuring sudoers"
  local systemctl_path apt_get_path apt_path dpkg_path docker_path docker_compose_path systemd_run_path smartctl_path
  systemctl_path=$(command -v systemctl || true)
  apt_get_path=$(command -v apt-get || true)
  apt_path=$(command -v apt || true)
  dpkg_path=$(command -v dpkg || true)
  docker_path=$(command -v docker || true)
  docker_compose_path=$(command -v docker-compose || true)
  systemd_run_path=$(command -v systemd-run || true)
  smartctl_path=$(command -v smartctl || true)
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
  system_cmds="${systemctl_path} restart lnd, ${systemctl_path} restart lightningos-manager, ${systemctl_path} restart postgresql, ${LND_FIX_PERMS_SCRIPT}, ${smartctl_path} *"
  local app_cmds=()
  [[ -n "$apt_get_path" ]] && app_cmds+=("${apt_get_path} *")
  [[ -n "$apt_path" ]] && app_cmds+=("${apt_path} *")
  [[ -n "$dpkg_path" ]] && app_cmds+=("${dpkg_path} *")
  [[ -n "$docker_path" ]] && app_cmds+=("${docker_path} *")
  [[ -n "$docker_compose_path" ]] && app_cmds+=("${docker_compose_path} *")
  [[ -n "$systemd_run_path" ]] && app_cmds+=("${systemd_run_path} *")
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

install_packages() {
  print_step "Installing base packages"
  apt_get update
  setup_postgres_repo
  setup_tor_repo
  apt_get update
  resolve_postgres_version
  apt_get install -y \
    postgresql-common \
    postgresql-client-common \
    postgresql-"${POSTGRES_VERSION}" \
    postgresql-client-"${POSTGRES_VERSION}" \
    smartmontools curl jq ca-certificates openssl build-essential git sudo tor deb.torproject.org-keyring apt-transport-https
  print_ok "Base packages installed"
}

ensure_tor_setting() {
  local key="$1"
  local value="$2"
  local torrc="/etc/tor/torrc"
  if [[ ! -f "$torrc" ]]; then
    return
  fi
  local tmp
  tmp=$(mktemp)
  grep -Ev "^[[:space:]]*#?[[:space:]]*${key}[[:space:]]+" "$torrc" > "$tmp"
  echo "${key} ${value}" >> "$tmp"
  mv "$tmp" "$torrc"
}

configure_tor() {
  print_step "Configuring Tor"
  local torrc="/etc/tor/torrc"
  if ! command -v tor >/dev/null 2>&1; then
    print_warn "tor not installed; skipping"
    return
  fi
  if [[ ! -f "$torrc" ]]; then
    print_warn "$torrc not found; skipping"
    return
  fi
  ensure_tor_setting "ControlPort" "127.0.0.1:9051"
  ensure_tor_setting "SocksPort" "127.0.0.1:9050"
  ensure_tor_setting "CookieAuthentication" "1"
  ensure_tor_setting "CookieAuthFileGroupReadable" "1"

  strip_crlf "$torrc"
  start_tor_service
  if systemctl list-unit-files | grep -q '^tor@default\.service'; then
    systemctl restart tor@default >/dev/null 2>&1 || true
  else
    systemctl restart tor >/dev/null 2>&1 || true
  fi
  if ! wait_for_tor_control; then
    print_warn "Tor control port 9051 not ready yet"
    if systemctl list-unit-files | grep -q '^tor@default\.service'; then
      systemctl status tor@default --no-pager || true
      journalctl -u tor@default -n 50 --no-pager || true
    else
      systemctl status tor --no-pager || true
      journalctl -u tor -n 50 --no-pager || true
    fi
    if command -v ss >/dev/null 2>&1; then
      ss -ltnp | grep -E '9050|9051' || true
    fi
  fi
  print_ok "Tor configured"
}

install_i2pd() {
  print_step "Installing i2pd"
  if ! command -v i2pd >/dev/null 2>&1; then
    curl -fsSL https://repo.i2pd.xyz/.help/add_repo | bash -s -
    apt_get update
    apt_get install -y i2pd
  fi
  systemctl enable --now i2pd >/dev/null 2>&1 || true
  print_ok "i2pd installed"
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

  curl -fsSL "https://deb.nodesource.com/setup_${NODE_VERSION}.x" | bash -
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

install_helper_scripts() {
  print_step "Installing helper scripts"
  local src="$REPO_ROOT/scripts/fix-lnd-perms.sh"
  if [[ -f "$src" ]]; then
    mkdir -p "$(dirname "$LND_FIX_PERMS_SCRIPT")"
    install -m 0755 "$src" "$LND_FIX_PERMS_SCRIPT"
    print_ok "Helper scripts installed"
  else
    print_warn "Missing helper script: $src"
  fi
}

prepare_lnd_data_dir() {
  print_step "Preparing LND data directory"
  mkdir -p /data "$LND_DIR" /var/log/lnd
  chown -R lnd:lnd /data
  chmod 750 /data
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
  for dir in "$LND_DIR/data" "$LND_DIR/data/chain" "$LND_DIR/data/chain/bitcoin" "$LND_DIR/data/chain/bitcoin/mainnet"; do
    if [[ -d "$dir" ]]; then
      chown lnd:lnd "$dir"
      chmod 750 "$dir"
    fi
  done
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
  if grep -Eq '^[[:space:]]*bitcoin\.active[[:space:]]*=' "$LND_CONF"; then
    print_warn "lnd.conf has deprecated bitcoin.active (remove it)"
  fi
  if ! grep -Eq '^[[:space:]]*bitcoin\.mainnet[[:space:]]*=[[:space:]]*(1|true)[[:space:]]*$' "$LND_CONF"; then
    print_warn "lnd.conf missing bitcoin.mainnet=1"
  fi
  if ! grep -Eq '^[[:space:]]*bitcoin\.node[[:space:]]*=[[:space:]]*bitcoind[[:space:]]*$' "$LND_CONF"; then
    print_warn "lnd.conf missing bitcoin.node=bitcoind"
  fi
  if ! grep -q '^db.postgres.dsn=' "$LND_CONF"; then
    print_warn "lnd.conf missing db.postgres.dsn"
  elif grep -q '^db.postgres.dsn=.*CHANGE_ME' "$LND_CONF"; then
    print_warn "lnd.conf has placeholder db.postgres.dsn"
  fi
}

warn_existing_wallet() {
  local wallet_db="$LND_DIR/data/chain/bitcoin/mainnet/wallet.db"
  if [[ -f "$wallet_db" ]]; then
    print_warn "Wallet database already exists at $wallet_db"
    print_warn "Wizard 'Create new' will fail. Use Unlock, or move /data/lnd for a clean install."
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

sync_lnd_dsn_from_secrets() {
  local dsn
  dsn=$(grep '^LND_PG_DSN=' /etc/lightningos/secrets.env | cut -d= -f2- || true)
  if [[ -z "$dsn" ]]; then
    return
  fi
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
  sync_lnd_dsn_from_secrets
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
  start_tor_service
  if ! wait_for_tor_control; then
    print_warn "Tor control port 9051 not ready; LND may fail to start"
  fi
  systemctl enable --now lnd
  systemctl enable --now lightningos-manager
  systemctl restart lnd >/dev/null 2>&1 || true
  systemctl restart lightningos-manager >/dev/null 2>&1 || true
  print_ok "Services enabled and started"
}

start_tor_service() {
  local unit="tor"
  if systemctl list-unit-files | grep -q '^tor@default\.service'; then
    unit="tor@default"
  fi
  systemctl stop tor@default >/dev/null 2>&1 || true
  systemctl stop tor >/dev/null 2>&1 || true
  if [[ "$unit" == "tor@default" ]]; then
    systemctl enable --now tor@default >/dev/null 2>&1 || true
  else
    systemctl enable --now tor >/dev/null 2>&1 || true
  fi
}

wait_for_tor_control() {
  local retries=60
  local i
  for i in $(seq 1 "$retries"); do
    if command -v ss >/dev/null 2>&1; then
      if ss -ltn | grep -q '127.0.0.1:9051'; then
        return 0
      fi
    else
      if (echo > /dev/tcp/127.0.0.1/9051) >/dev/null 2>&1; then
        return 0
      fi
    fi
    sleep 1
  done
  return 1
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
  configure_sudoers
  install_packages
  configure_tor
  create_lnd_user
  ensure_group_member lnd debian-tor
  ensure_user lightningos /var/lib/lightningos
  ensure_group_member lightningos lnd
  ensure_group_member lightningos systemd-journal
  ensure_group_member lightningos docker
  install_i2pd
  install_go
  install_node
  ensure_dirs
  install_helper_scripts
  prepare_lnd_data_dir
  copy_templates
  validate_lnd_conf
  warn_existing_wallet
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
