#!/usr/bin/env bash
set -Eeuo pipefail
set -o errtrace

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$SCRIPT_DIR"

if [[ -f "$REPO_ROOT/scripts/install-release-bootstrap.sh" ]]; then
  source "$REPO_ROOT/scripts/install-release-bootstrap.sh"
  lightningos_bootstrap_latest_release "install_existing.sh" "$@"
fi

ARTIFACT_VERIFY_SCRIPT="$REPO_ROOT/scripts/install-artifact-verification.sh"
if [[ ! -r "$ARTIFACT_VERIFY_SCRIPT" ]]; then
  echo "Missing artifact verification library: $ARTIFACT_VERIFY_SCRIPT" >&2
  exit 1
fi
source "$ARTIFACT_VERIFY_SCRIPT"

GO_VERSION="1.24.12"
GO_ARTIFACT="go${GO_VERSION}.linux-amd64.tar.gz"
GO_TARBALL_URL="https://go.dev/dl/${GO_ARTIFACT}"
GO_TARBALL_SHA256="bddf8e653c82429aea7aec2520774e79925d4bb929fe20e67ecc00dd5af44c50"
NODE_VERSION="24"
NODESOURCE_KEY_URL="https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key"
NODESOURCE_KEY_FINGERPRINT="6F71F525282841EEDAF851B42F59B5F99B1BE0B4"
NODESOURCE_KEY_SHA256="b42e0321dabdc24e892115da705cf061167eac12a317f23d329862d0aa0a271d"
NODESOURCE_INRELEASE_URL="https://deb.nodesource.com/node_${NODE_VERSION}.x/dists/nodistro/InRelease"
NODESOURCE_KEYRING="/usr/share/keyrings/nodesource.gpg"
NODESOURCE_SOURCE="/etc/apt/sources.list.d/nodesource.sources"
GOTTY_VERSION="1.8.0"
GOTTY_ARTIFACT="gotty_v${GOTTY_VERSION}_linux_amd64.tar.gz"
GOTTY_URL="https://github.com/sorenisanerd/gotty/releases/download/v${GOTTY_VERSION}/${GOTTY_ARTIFACT}"
GOTTY_SHA256="9cf032e1f3a49d33da3ba32c79f49892aad94e52edc6417524a76b623ced2f5f"
PGDG_KEY_URL="https://www.postgresql.org/media/keys/ACCC4CF8.asc"
PGDG_KEY_FINGERPRINT="B97B0AFCAA1A47F044F244A07FCC7D46ACCC4CF8"
PGDG_KEY_SHA256="0144068502a1eddd2a0280ede10ef607d1ec592ce819940991203941564e8e76"
PGDG_KEYRING="/usr/share/keyrings/postgresql.gpg"
PGDG_SOURCE="/etc/apt/sources.list.d/pgdg.sources"
POSTGRES_VERSION="${POSTGRES_VERSION:-latest}"
LND_FIX_PERMS_SCRIPT="/usr/local/sbin/lightningos-fix-lnd-perms"
LND_UPGRADE_SCRIPT="/usr/local/sbin/lightningos-upgrade-lnd"
MANAGER_FIREWALL_SCRIPT="/usr/local/sbin/lightningos-manager-firewall"
PRIVILEGED_BROKER="/usr/local/libexec/lightningos-privileged"
PRIVILEGED_TMPFILES_CONFIG="/etc/tmpfiles.d/lightningos-privileged.conf"
TERMINAL_ENV_PATH="/etc/lightningos/terminal.env"
SYSTEM_INTEGRATIONS_MARKER="/var/lib/lightningos/system-integrations-20260731-v2"
TERMINAL_OPERATOR_USER="${TERMINAL_OPERATOR_USER:-losop}"
MANAGER_BIN="/opt/lightningos/manager/lightningos-manager"

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

print_auth_setup_token() {
  if [[ ! -x "$MANAGER_BIN" ]]; then
    print_warn "Manager binary not found at $MANAGER_BIN"
    return
  fi

  local status token_output
  if ! status=$("$MANAGER_BIN" auth status 2>/dev/null); then
    print_warn "Could not read auth status"
    echo "Generate a setup token manually with:"
    echo "  sudo $MANAGER_BIN auth setup-token new"
    return
  fi

  if grep -q '^password_configured=true$' <<<"$status"; then
    echo "Admin password is already configured."
    return
  fi

  if [[ ! -t 0 || ! -t 1 || ! -w /dev/tty ]]; then
    echo "Admin setup token was not generated because no interactive terminal is attached."
    echo "Generate one later from an interactive terminal with:"
    echo "  sudo $MANAGER_BIN auth setup-token new"
    return
  fi

  if token_output=$("$MANAGER_BIN" auth setup-token new 2>/dev/null); then
    {
      echo ""
      echo "Admin setup token:"
      while IFS= read -r line; do
        echo "  $line"
      done <<<"$token_output"
    } > /dev/tty
  else
    print_warn "Automatic setup token generation failed"
  fi
  echo "Generate another setup token later with:"
  echo "  sudo $MANAGER_BIN auth setup-token new"
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
    cat "$license_file"
    return
  fi
  cat <<'EOF'
MIT License

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

read_env_value() {
  local key="$1"
  local file="${2:-$SECRETS_PATH}"
  [[ -r "$file" ]] || return 0
  awk -v key="$key" 'index($0, key "=") == 1 { print substr($0, length(key) + 2) }' "$file" | tail -n1
}

is_usable_dsn() {
  local dsn="${1:-}"
  [[ -n "$dsn" && "$dsn" != *CHANGE_ME* ]]
}

ensure_secrets_file() {
  mkdir -p /etc/lightningos
  if [[ ! -f "$SECRETS_PATH" ]]; then
    cp "$REPO_ROOT/templates/secrets.env" "$SECRETS_PATH"
  fi
}

ensure_default_bitcoin_source() {
  local btc_conf="$1"
  local current
  current=$(read_env_value "BITCOIN_SOURCE")
  if [[ -n "$current" ]]; then
    print_ok "Bitcoin source already configured (${current})"
    return 0
  fi
  if [[ -f "$btc_conf" ]]; then
    set_env_value "BITCOIN_SOURCE" "local"
    print_ok "Bitcoin source set to local (existing node)"
  else
    print_warn "bitcoin.conf not found; bitcoin source left unset (UI defaults to remote)"
  fi
}

ensure_dirs() {
  print_step "Preparing directories"
  mkdir -p /etc/lightningos /etc/lightningos/tls /opt/lightningos/manager /opt/lightningos/ui /var/log/lightningos
  mkdir -p -m 0750 /var/lib/lightningos
  mkdir -p -m 0750 /var/lib/lightningos/apps /var/lib/lightningos/apps-data
  chmod 750 /etc/lightningos /etc/lightningos/tls
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
  if [[ -f /etc/lightningos/tls/local-ca.crt ]]; then
    chown root:root /etc/lightningos/tls/local-ca.crt
    chmod 644 /etc/lightningos/tls/local-ca.crt
  fi
  if [[ -f /etc/lightningos/tls/local-ca.key ]]; then
    chown root:root /etc/lightningos/tls/local-ca.key
    chmod 600 /etc/lightningos/tls/local-ca.key
  fi
  if [[ -f /etc/lightningos/tls/access.env ]]; then
    chown root:root /etc/lightningos/tls/access.env
    chmod 644 /etc/lightningos/tls/access.env
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
  # Preserve app-specific ownership below apps/ and apps-data/. Native apps
  # such as Lightning Loop use isolated service accounts and private files.
  chown "$user:$group" /var/lib/lightningos /var/lib/lightningos/apps /var/lib/lightningos/apps-data
  chown -R "$user:$group" /var/log/lightningos
  chmod 750 /var/log/lightningos
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

  local tmp archive
  tmp=$(mktemp -d)
  archive="$tmp/$GO_ARTIFACT"
  if ! lightningos_download_verified_artifact "$GO_TARBALL_URL" "$archive" "$GO_TARBALL_SHA256" "Go ${GO_VERSION} linux-amd64"; then
    rm -rf -- "$tmp"
    return 1
  fi
  if ! tar -tzf "$archive" >/dev/null 2>&1; then
    rm -rf -- "$tmp"
    print_warn "Verified Go archive is not a valid gzip tarball"
    return 1
  fi
  rm -rf /usr/local/go
  if ! tar -C /usr/local -xzf "$archive"; then
    rm -rf -- "$tmp"
    return 1
  fi
  rm -rf -- "$tmp"
  export PATH="/usr/local/go/bin:$PATH"
  print_ok "Go installed"
}

install_node() {
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
  apt-get install -y ca-certificates curl gnupg
  local architecture source_tmp
  architecture=$(dpkg --print-architecture)
  if [[ "$architecture" != "amd64" && "$architecture" != "arm64" ]]; then
    print_warn "Unsupported NodeSource architecture: ${architecture}"
    return 1
  fi
  install -d -o root -g root -m 0755 /usr/share/keyrings /etc/apt/sources.list.d
  lightningos_install_authenticated_apt_key "$NODESOURCE_KEY_URL" "$NODESOURCE_KEY_FINGERPRINT" "$NODESOURCE_KEY_SHA256" \
    "$NODESOURCE_INRELEASE_URL" "$NODESOURCE_KEYRING" "NodeSource repository"
  source_tmp=$(mktemp)
  cat > "$source_tmp" <<EOF
Types: deb
URIs: https://deb.nodesource.com/node_${NODE_VERSION}.x
Suites: nodistro
Components: main
Architectures: ${architecture}
Signed-By: ${NODESOURCE_KEYRING}
EOF
  install -o root -g root -m 0644 "$source_tmp" "$NODESOURCE_SOURCE"
  rm -f -- "$source_tmp"
  rm -f -- /etc/apt/sources.list.d/nodesource.list
  apt-get update
  apt-get install -y nodejs >/dev/null
  print_ok "Node.js installed"
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
  if ! lightningos_download_verified_artifact "$GOTTY_URL" "$tmp/$GOTTY_ARTIFACT" "$GOTTY_SHA256" "GoTTY ${GOTTY_VERSION} linux-amd64"; then
    rm -rf -- "$tmp"
    return 1
  fi
  if ! tar -xzf "$tmp/$GOTTY_ARTIFACT" -C "$tmp"; then
    rm -rf -- "$tmp"
    return 1
  fi
  if [[ ! -f "$tmp/gotty" || -L "$tmp/gotty" ]]; then
    rm -rf -- "$tmp"
    print_warn "Verified GoTTY archive does not contain a regular gotty binary"
    return 1
  fi
  install -m 0755 "$tmp/gotty" /usr/local/bin/gotty
  rm -rf -- "$tmp"
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

ensure_acl_support() {
  if command -v setfacl >/dev/null 2>&1; then
    print_ok "Filesystem ACL support already installed"
    return 0
  fi
  if ! command -v apt-get >/dev/null 2>&1; then
    print_warn "apt-get not found; install the acl package before installing Lightning Loop"
    return 1
  fi
  print_step "Installing filesystem ACL support"
  apt-get update
  apt-get install -y acl >/dev/null
  print_ok "Filesystem ACL support installed"
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
  local src="$REPO_ROOT/internal/server/assets/upgrade-lnd.sh"
  if [[ -f "$src" ]]; then
    mkdir -p "$(dirname "$LND_UPGRADE_SCRIPT")"
    install -m 0755 "$src" "$LND_UPGRADE_SCRIPT"
    print_ok "LND upgrade helper installed"
  else
    print_warn "Missing helper script: $src"
  fi
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

read_lnd_postgres_dsn() {
  local lnd_conf="$1"
  local dsn
  dsn=$(read_conf_value "$lnd_conf" "db.postgres.dsn")
  if is_usable_dsn "$dsn"; then
    echo "$dsn"
  fi
}

persist_lnd_postgres_dsn() {
  local lnd_conf="$1"
  local dsn current
  dsn=$(read_lnd_postgres_dsn "$lnd_conf")
  if [[ -z "$dsn" ]]; then
    return 0
  fi
  current=$(read_env_value "LND_PG_DSN")
  if ! is_usable_dsn "$current"; then
    set_env_value "LND_PG_DSN" "$dsn"
    print_ok "Saved existing LND Postgres DSN to secrets.env"
  elif [[ "$current" != "$dsn" ]]; then
    print_warn "LND_PG_DSN already exists in secrets.env; keeping existing value"
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
          print_warn "Path ${default} already exists; skipping symlink" >&2
        else
          mkdir -p "$(dirname "$default")"
          ln -s "$admin_target" "$default"
          print_ok "Symlink created: ${default} -> ${admin_target}" >&2
          echo "$default"
          return
        fi
      fi
    fi
  fi

  print_warn "${label} directory not found at ${default}" >&2
  if prompt_yes_no "Use a different ${label} directory?" "y"; then
    dir=$(prompt_value "Enter ${label} directory")
    if [[ -z "$dir" || ! -d "$dir" ]]; then
      print_warn "Directory not found: ${dir}" >&2
      exit 1
    fi
  else
    exit 1
  fi
  if [[ "$default" != "$dir" ]]; then
    if prompt_yes_no "Create symlink ${default} -> ${dir}?" "n"; then
      if [[ -e "$default" ]]; then
        print_warn "Path ${default} already exists; skipping symlink" >&2
      else
        mkdir -p "$(dirname "$default")"
        ln -s "$dir" "$default"
        print_ok "Symlink created: ${default} -> ${dir}" >&2
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
    GOFLAGS="-mod=mod -buildvcs=false" go build -o dist/lightningos-manager ./cmd/lightningos-manager && \
    GOFLAGS="-mod=mod -buildvcs=false" go build -o dist/lightningos-privileged ./cmd/lightningos-privileged)
  install -m 0755 "$REPO_ROOT/dist/lightningos-manager" /opt/lightningos/manager/lightningos-manager
  local broker_path
  for broker_path in /usr/local/libexec /var/log/lightningos-privileged /run/lock/lightningos "$PRIVILEGED_BROKER" "$PRIVILEGED_TMPFILES_CONFIG"; do
    [[ ! -L "$broker_path" ]] || die "Refusing symlinked privileged broker path: $broker_path"
  done
  [[ -f "$REPO_ROOT/templates/lightningos-privileged.tmpfiles.conf" ]] || die "Privileged broker tmpfiles template is missing"
  install -d -o root -g root -m 0755 /usr/local/libexec
  install -d -o root -g root -m 0755 /etc/tmpfiles.d
  install -d -o root -g root -m 0750 /var/log/lightningos-privileged /run/lock/lightningos
  install -o root -g root -m 0644 "$REPO_ROOT/templates/lightningos-privileged.tmpfiles.conf" "$PRIVILEGED_TMPFILES_CONFIG"
  /usr/bin/systemd-tmpfiles --create "$PRIVILEGED_TMPFILES_CONFIG"
  install -o root -g root -m 0755 "$REPO_ROOT/dist/lightningos-privileged" "$PRIVILEGED_BROKER"
  local broker_response
  broker_response=$(printf '%s\n' '{"version":1,"request_id":"install_self_test","operation":"self_test","params":{}}' | env -u SUDO_UID -u SUDO_USER -u SUDO_COMMAND "$PRIVILEGED_BROKER")
  if ! jq -e '.version == 1 and .request_id == "install_self_test" and .ok == true and .result.ready == true' >/dev/null <<<"$broker_response"; then
    die "Privileged broker self-test failed"
  fi
  print_ok "Privileged broker installed and self-test passed"
  print_ok "Manager built and installed"
}

build_ui() {
  print_step "Building UI"
  (cd "$REPO_ROOT/ui" && npm install && npm run build)
  publish_ui_tree
  print_ok "UI built and installed"
}

publish_ui_tree() {
  local source="$REPO_ROOT/ui/dist"
  local target="/opt/lightningos/ui"
  [[ -d "$source" && ! -L "$source" ]] || die "UI build output is missing or unsafe"
  [[ -d "$target" && ! -L "$target" ]] || die "UI destination is missing or unsafe"
  [[ -z "$(find "$source" -type l -print -quit)" ]] || die "UI build output contains a symbolic link"
  [[ -z "$(find "$source" ! -type d ! -type f -print -quit)" ]] || die "UI build output contains an unsupported file type"
  find "$target" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
  cp -R --no-preserve=mode,ownership,timestamps -- "$source/." "$target/"
  find "$target" -type d -exec chmod 0755 {} +
  find "$target" -type f -exec chmod 0644 {} +
  chown -R root:root "$target"
}

ensure_tls() {
  print_step "Configuring trusted local TLS and LAN discovery"
  if ! command -v avahi-daemon >/dev/null 2>&1; then
    if apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y avahi-daemon libnss-mdns; then
      print_ok "mDNS packages installed"
    else
      print_warn "Could not install Avahi; TLS will work but hostname.local may be unavailable"
    fi
  fi
  local helper="$REPO_ROOT/internal/server/assets/setup-manager-tls-mdns.sh"
  if [[ ! -f "$helper" ]]; then
    print_warn "Missing TLS/mDNS helper: $helper"
    return 1
  fi
  chmod 0755 "$helper"
  LIGHTNINGOS_MANAGER_GROUP=lightningos \
    LIGHTNINGOS_MANAGER_PORT=8443 \
    "$helper"
  print_ok "Local TLS and mDNS configured"
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
  local groups=("systemd-journal")
  if [[ -n "$LND_GROUP" ]]; then
    getent group "$LND_GROUP" >/dev/null 2>&1 && groups+=("$LND_GROUP")
  else
    getent group lnd >/dev/null 2>&1 && groups+=("lnd")
  fi
  if [[ ${#groups[@]} -gt 0 ]]; then
    local group_line
    group_line=$(IFS=' '; echo "${groups[*]}")
    sed -i "s|^SupplementaryGroups=.*|SupplementaryGroups=${group_line}|" "$svc"
  else
    sed -i "/^SupplementaryGroups=/d" "$svc"
  fi
}

ensure_terminal_service() {
  cp "$REPO_ROOT/templates/systemd/lightningos-terminal.service" /etc/systemd/system/lightningos-terminal.service
}

ensure_terminal_helper() {
  local src="$REPO_ROOT/internal/server/assets/lightningos-terminal.sh"
  if [[ -f "$src" ]]; then
    install -m 0755 "$src" /usr/local/sbin/lightningos-terminal
    print_ok "Terminal helper installed"
  else
    print_warn "Missing helper script: $src"
  fi
}

ensure_manager_firewall() {
  local src="$REPO_ROOT/internal/server/assets/lightningos-manager-firewall.sh"
  if [[ ! -f "$src" ]]; then
    print_warn "Missing helper script: $src"
    return 0
  fi
  install -m 0755 "$src" "$MANAGER_FIREWALL_SCRIPT"
  # Reuse the configured LAN CIDR or accept the detected one automatically.
  if "$MANAGER_FIREWALL_SCRIPT"; then
    touch "$SYSTEM_INTEGRATIONS_MARKER"
    chmod 0644 "$SYSTEM_INTEGRATIONS_MARKER"
  else
    print_warn "Failed to configure restricted access to port 8443"
  fi
}

ensure_operator_user() {
  local user="$TERMINAL_OPERATOR_USER"
  print_step "Ensuring operator user ${user}"
  [[ "$user" == "losop" ]] || die "The web terminal requires the dedicated losop account"
  local credential
  credential=$(read_env_value TERMINAL_CREDENTIAL "$SECRETS_PATH")
  if [[ -z "$credential" || "$credential" == terminal:* ]]; then
    credential="${user}:$(openssl rand -hex 16)"
  fi
  set_env_value "TERMINAL_OPERATOR_USER" "$user"
  if grep -q '^TERMINAL_OPERATOR_PASSWORD=' "$SECRETS_PATH"; then
    set_env_value "TERMINAL_OPERATOR_PASSWORD" ""
  fi
  set_env_value "TERMINAL_CREDENTIAL" "$credential"
  set_env_value "TERMINAL_ENABLED" "0"
  set_env_value "TERMINAL_ALLOW_WRITE" "0"

  if ! id "$user" >/dev/null 2>&1; then
    useradd -m -d "/home/${user}" -s /bin/bash "$user"
  fi
  for group in lightningos sudo systemd-journal; do
    if id -nG "$user" | tr ' ' '\n' | grep -Fxq "$group"; then
      gpasswd -d "$user" "$group" >/dev/null
    fi
  done
  usermod -L "$user"
  [[ ! -L "$TERMINAL_ENV_PATH" ]] || die "Refusing symlinked terminal runtime environment"
  local runtime_tmp
  runtime_tmp=$(mktemp /etc/lightningos/.terminal.env.XXXXXX)
  (umask 077; {
    printf 'TERMINAL_ENABLED=0\n'
    printf 'TERMINAL_CREDENTIAL=%s\n' "$credential"
    printf 'TERMINAL_ALLOW_WRITE=0\n'
    printf 'TERMINAL_PORT=7681\n'
    printf 'TERMINAL_OPERATOR_USER=%s\n' "$user"
    printf 'TERMINAL_TERM=xterm\n'
    printf 'TERMINAL_SHELL=/bin/bash\n'
    printf 'TERMINAL_WS_ORIGIN=\n'
  } > "$runtime_tmp")
  install -o root -g lightningos -m 0640 "$runtime_tmp" "$TERMINAL_ENV_PATH"
  rm -f -- "$runtime_tmp"
  print_ok "Restricted terminal user ${user} ready (password locked, no privileged groups)"
}

ensure_lightningos_user() {
  if ! getent group lightningos >/dev/null 2>&1; then
    groupadd --system lightningos
  fi
  if ! id lightningos >/dev/null 2>&1; then
    useradd --system --home /var/lib/lightningos --shell /usr/sbin/nologin -g lightningos lightningos
  fi
  mkdir -p -m 0750 /var/lib/lightningos
  mkdir -p -m 0750 /var/lib/lightningos/apps /var/lib/lightningos/apps-data
  chown lightningos:lightningos /var/lib/lightningos /var/lib/lightningos/apps /var/lib/lightningos/apps-data
}

ensure_system_group() {
  local group="$1"
  if getent group "$group" >/dev/null 2>&1; then
    return 0
  fi
  groupadd --system "$group"
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
  [[ "$(systemctl show -p LoadState --value "${1}.service" 2>/dev/null || true)" == "loaded" ]]
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

ensure_lnd_restart_policy() {
  [[ -n "$LND_SERVICE" ]] || return 0
  local dropin_dir="/etc/systemd/system/${LND_SERVICE}.service.d"
  mkdir -p "$dropin_dir"
  printf '%s\n' '[Service]' 'Restart=always' 'RestartSec=60' >"$dropin_dir/20-lightningos-restart.conf"
  print_ok "LND restart policy set to always for ${LND_SERVICE}.service"
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
  local install_mode="${1:-allow-install}"
  if ! service_exists "postgresql"; then
    print_warn "PostgreSQL service not found"
    if [[ "$install_mode" != "allow-install" ]]; then
      print_warn "Not installing a new PostgreSQL server for an existing LND Postgres setup"
      return 1
    fi
    if prompt_yes_no "Install PostgreSQL now (required for reports/notifications)?" "y"; then
      if command -v apt-get >/dev/null 2>&1; then
        if ! install_postgres_packages; then
          print_warn "PostgreSQL installation failed"
          return 1
        fi
      else
        print_warn "apt-get not found; install PostgreSQL manually"
        return 1
      fi
    else
      return 1
    fi
  fi
  if ! systemctl is-active --quiet postgresql; then
    if prompt_yes_no "PostgreSQL is inactive. Enable and start it now?" "y"; then
      if systemctl enable --now postgresql; then
        print_ok "PostgreSQL started"
      else
        print_warn "Failed to start PostgreSQL"
        return 1
      fi
    else
      print_warn "PostgreSQL is required for reports/notifications"
      return 1
    fi
  fi
  systemctl is-active --quiet postgresql
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
    version=$(pg_lsclusters 2>/dev/null | awk 'NR>1 && $1 ~ /^[0-9]+$/ && $4=="online" {print $1}' | sort -nr | head -n1) || true
    if [[ -z "$version" ]]; then
      version=$(pg_lsclusters 2>/dev/null | awk 'NR>1 && $1 ~ /^[0-9]+$/ {print $1}' | sort -nr | head -n1) || true
    fi
  fi
  if [[ -z "$version" ]]; then
    version=$(dpkg-query -W -f='${Package}\n' 'postgresql-[0-9]*' 2>/dev/null | sed -n 's/^postgresql-\([0-9][0-9]*\)$/\1/p' | sort -nr | head -n1) || true
  fi
  echo "$version"
}

setup_postgres_repo() {
  print_step "Configuring PostgreSQL repository"
  local architecture codename source_tmp
  codename=$(get_os_codename)
  architecture=$(dpkg --print-architecture)
  if [[ ! "$codename" =~ ^[a-z][a-z0-9-]{1,31}$ || ( "$architecture" != "amd64" && "$architecture" != "arm64" ) ]]; then
    print_warn "Could not resolve a supported PGDG suite/architecture"
    return 1
  fi
  apt-get install -y ca-certificates curl gnupg
  install -d -o root -g root -m 0755 /usr/share/keyrings /etc/apt/sources.list.d
  lightningos_install_authenticated_apt_key "$PGDG_KEY_URL" "$PGDG_KEY_FINGERPRINT" "$PGDG_KEY_SHA256" \
    "https://apt.postgresql.org/pub/repos/apt/dists/${codename}-pgdg/InRelease" "$PGDG_KEYRING" "PGDG repository"
  source_tmp=$(mktemp)
  cat > "$source_tmp" <<EOF
Types: deb
URIs: https://apt.postgresql.org/pub/repos/apt
Suites: ${codename}-pgdg
Components: main
Architectures: ${architecture}
Signed-By: ${PGDG_KEYRING}
EOF
  install -o root -g root -m 0644 "$source_tmp" "$PGDG_SOURCE"
  rm -f -- "$source_tmp" /etc/apt/sources.list.d/pgdg.list
  print_ok "PostgreSQL repo ready (${codename}-pgdg)"
}

ensure_native_app_identity() {
  local user="$1"
  local home="$2"
  if ! getent group "$user" >/dev/null 2>&1; then
    groupadd --system "$user"
  fi
  if id "$user" >/dev/null 2>&1; then
    [[ "$(id -gn "$user")" == "$user" ]] || die "Existing ${user} account has an incompatible primary group"
    return 0
  fi
  useradd --system --gid "$user" --home-dir "$home" --no-create-home --shell /usr/sbin/nologin "$user"
}

ensure_native_app_identities() {
  ensure_native_app_identity lightningos-loop /var/lib/lightningos/apps-data/loop
  ensure_native_app_identity lightningos-elements /data/elements
  ensure_native_app_identity lightningos-peerswap /var/lib/lightningos/apps-data/peerswap/runtime
}

ensure_privileged_broker_units() {
  local manager_user="$1"
  if [[ "$manager_user" != "lightningos" ]]; then
    die "Automatic privilege cutover supports the canonical lightningos manager user only; the existing service was not changed."
  fi
  local socket_src="$REPO_ROOT/templates/systemd/lightningos-privileged.socket"
  local service_src="$REPO_ROOT/templates/systemd/lightningos-privileged@.service"
  [[ -f "$socket_src" && ! -L "$socket_src" ]] || die "Privileged broker socket template is missing"
  [[ -f "$service_src" && ! -L "$service_src" ]] || die "Privileged broker service template is missing"
  [[ -f "$PRIVILEGED_BROKER" && ! -L "$PRIVILEGED_BROKER" && -x "$PRIVILEGED_BROKER" ]] || die "Privileged broker binary is unavailable"
  install -o root -g root -m 0644 "$socket_src" /etc/systemd/system/lightningos-privileged.socket
  install -o root -g root -m 0644 "$service_src" /etc/systemd/system/lightningos-privileged@.service
}

resolve_postgres_version() {
  print_step "Resolving PostgreSQL version"
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
  versions=$(apt-cache search --names-only '^postgresql-[0-9]+$' 2>/dev/null | awk '{print $1}' | sed -n 's/^postgresql-\([0-9][0-9]*\)$/\1/p' | sort -nr)
  if [[ -z "$versions" ]]; then
    print_warn "Could not detect PostgreSQL versions; falling back to 18"
    POSTGRES_VERSION="18"
    return 0
  fi
  POSTGRES_VERSION=$(echo "$versions" | head -n1)
  print_ok "Using PostgreSQL ${POSTGRES_VERSION}"
}

install_postgres_packages() {
  print_step "Installing PostgreSQL"
  apt-get update || return 1
  setup_postgres_repo || return 1
  apt-get update || return 1
  resolve_postgres_version || return 1
  apt-get install -y \
    postgresql-common \
    postgresql-client-common \
    postgresql-"${POSTGRES_VERSION}" \
    postgresql-client-"${POSTGRES_VERSION}" || return 1
  print_ok "PostgreSQL installed"
}

psql_as_postgres() {
  if command -v runuser >/dev/null 2>&1; then
    PGCONNECT_TIMEOUT=5 runuser -u postgres -- psql -X "$@"
  else
    PGCONNECT_TIMEOUT=5 sudo -u postgres psql -X "$@"
  fi
}

ensure_psql_client() {
  if command -v psql >/dev/null 2>&1; then
    return 0
  fi
  print_warn "psql not found"
  if command -v apt-get >/dev/null 2>&1 && prompt_yes_no "Install PostgreSQL client (psql) now?" "y"; then
    if apt-get update && apt-get install -y postgresql-client; then
      return 0
    fi
  fi
  return 1
}

psql_admin() {
  local admin_dsn="${1:-}"
  shift || true
  if [[ -n "$admin_dsn" ]]; then
    PGCONNECT_TIMEOUT=5 psql -X "$admin_dsn" "$@"
  else
    psql_as_postgres "$@"
  fi
}

can_use_local_postgres_admin() {
  command -v psql >/dev/null 2>&1 || return 1
  systemctl is-active --quiet postgresql || return 1
  psql_as_postgres -tAc "select 1" >/dev/null 2>&1
}

escape_pg_password() {
  printf '%s' "$1" | sed "s/'/''/g"
}

urlencode_dsn_component() {
  local LC_ALL=C
  local raw="$1"
  local out=""
  local i c
  for (( i = 0; i < ${#raw}; i++ )); do
    c="${raw:$i:1}"
    case "$c" in
      [A-Za-z0-9.~_-]) out+="$c" ;;
      *) printf -v out '%s%%%02X' "$out" "'$c" ;;
    esac
  done
  printf '%s' "$out"
}

build_postgres_dsn_from_admin() {
  local admin_dsn="${1:-}"
  local user="$2"
  local pass="$3"
  local db="$4"
  if [[ -z "$admin_dsn" ]]; then
    echo "postgres://${user}:${pass}@127.0.0.1:5432/${db}?sslmode=disable"
    return 0
  fi
  if [[ "$admin_dsn" != *"://"* ]]; then
    echo "postgres://${user}:${pass}@127.0.0.1:5432/${db}?sslmode=disable"
    return 0
  fi
  local scheme rest host_path authority query path_part
  scheme="${admin_dsn%%://*}"
  rest="${admin_dsn#*://}"
  if [[ "$rest" == *@* ]]; then
    host_path="${rest#*@}"
  else
    host_path="$rest"
  fi
  authority="$host_path"
  query=""
  if [[ "$host_path" == */* ]]; then
    authority="${host_path%%/*}"
    path_part="${host_path#*/}"
    if [[ "$path_part" == *\?* ]]; then
      query="?${path_part#*\?}"
    fi
  elif [[ "$authority" == *\?* ]]; then
    query="?${authority#*\?}"
    authority="${authority%%\?*}"
  fi
  if [[ -z "$authority" ]]; then
    echo "postgres://${user}:${pass}@127.0.0.1:5432/${db}?sslmode=disable"
    return 0
  fi
  echo "${scheme}://${user}:${pass}@${authority}/${db}${query}"
}

ensure_pg_role() {
  local role="$1"
  local options="$2"
  local password="$3"
  local admin_dsn="${4:-}"
  local exists
  exists=$(psql_admin "$admin_dsn" -tAc "select 1 from pg_roles where rolname='${role}'" 2>/dev/null | tr -d '[:space:]')
  if [[ "$exists" == "1" ]]; then
    psql_admin "$admin_dsn" -v ON_ERROR_STOP=1 -c "alter role ${role} with ${options} password '${password}'"
  else
    psql_admin "$admin_dsn" -v ON_ERROR_STOP=1 -c "create role ${role} with login ${options} password '${password}'"
  fi
}

ensure_pg_database() {
  local db="$1"
  local owner="$2"
  local admin_dsn="${3:-}"
  local exists
  exists=$(psql_admin "$admin_dsn" -tAc "select 1 from pg_database where datname='${db}'" 2>/dev/null | tr -d '[:space:]')
  if [[ "$exists" == "1" ]]; then
    psql_admin "$admin_dsn" -v ON_ERROR_STOP=1 -c "alter database ${db} owner to ${owner}"
  else
    psql_admin "$admin_dsn" -v ON_ERROR_STOP=1 -c "create database ${db} owner ${owner}"
  fi
}

provision_notifications_db() {
  local admin_dsn="${1:-}"
  if ! ensure_psql_client; then
    print_warn "psql not available; cannot provision database"
    return 1
  fi
  if [[ -z "$admin_dsn" ]] && ! systemctl is-active --quiet postgresql; then
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

  ensure_pg_role "$NOTIFICATIONS_ADMIN_USER" "createrole createdb" "$admin_pass_esc" "$admin_dsn" || return 1
  ensure_pg_role "$NOTIFICATIONS_APP_USER" "" "$app_pass_esc" "$admin_dsn" || return 1
  ensure_pg_database "$NOTIFICATIONS_DB_NAME" "$NOTIFICATIONS_APP_USER" "$admin_dsn" || return 1

  set_env_value "NOTIFICATIONS_PG_DSN" "$(build_postgres_dsn_from_admin "$admin_dsn" "$NOTIFICATIONS_APP_USER" "$(urlencode_dsn_component "$app_pass")" "$NOTIFICATIONS_DB_NAME")"
  set_env_value "NOTIFICATIONS_PG_ADMIN_DSN" "$(build_postgres_dsn_from_admin "$admin_dsn" "$NOTIFICATIONS_ADMIN_USER" "$(urlencode_dsn_component "$admin_pass")" "postgres")"
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
  ensure_acl_support || print_warn "Lightning Loop installation will require the acl package"

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
  ensure_default_bitcoin_source "$btc_conf"
  ensure_lightningos_user
  ensure_native_app_identities
  ensure_operator_user

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
    persist_lnd_postgres_dsn "$lnd_conf"
  elif [[ "$lnd_backend" == "bolt" ]]; then
    print_ok "Detected LND backend: bolt/sqlite"
  else
    print_warn "Could not detect LND backend"
  fi

  detect_core_service_users
  ensure_lnd_restart_policy
  if [[ -n "$LND_USER" ]]; then
    print_ok "Detected LND service user: ${LND_USER}"
  else
    print_warn "LND service user not detected"
  fi
  if [[ -n "$BITCOIN_USER" ]]; then
    print_ok "Detected Bitcoin service user: ${BITCOIN_USER}"
  fi

  local local_postgres_admin_ready="0"
  if [[ "$lnd_backend" != "postgres" ]]; then
    if prompt_yes_no "Install/enable Postgres for reports/notifications?" "y"; then
      if ensure_postgres_service "allow-install" && can_use_local_postgres_admin; then
        local_postgres_admin_ready="1"
      else
        print_warn "Local PostgreSQL admin access is not ready"
      fi
    fi
  else
    if ensure_postgres_service "no-install" && can_use_local_postgres_admin; then
      local_postgres_admin_ready="1"
    else
      print_warn "Use an admin DSN for the existing PostgreSQL server when creating the LightningOS database"
    fi
  fi

  if prompt_yes_no "Create/ensure LightningOS database and users now?" "y"; then
    local provision_admin_dsn=""
    if [[ "$local_postgres_admin_ready" != "1" ]]; then
      provision_admin_dsn=$(read_env_value "NOTIFICATIONS_PG_ADMIN_DSN")
      if ! is_usable_dsn "$provision_admin_dsn"; then
        provision_admin_dsn=$(prompt_value "Enter PostgreSQL admin DSN for the existing server (blank to skip DB creation)")
      fi
      if [[ -z "$provision_admin_dsn" ]]; then
        print_warn "Database provisioning skipped"
      elif ! provision_notifications_db "$provision_admin_dsn"; then
        print_warn "Database provisioning skipped"
      fi
    elif ! provision_notifications_db; then
      print_warn "Database provisioning skipped"
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

  print_step "Configuring LightningOS terminal service (GoTTY)"
  if ! command -v tmux >/dev/null 2>&1; then
    if command -v apt-get >/dev/null 2>&1; then
      apt-get update
      apt-get install -y tmux
    else
      print_warn "apt-get not found; install tmux manually"
    fi
  fi
  if ! command -v gotty >/dev/null 2>&1; then
    install_gotty
  fi
  set_env_value "TERMINAL_ENABLED" "0"
  set_env_value "TERMINAL_ALLOW_WRITE" "0"
  ensure_terminal_helper
  ensure_terminal_service


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
  membership_groups+=("systemd-journal")
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
    if [[ "$manager_user" != "lightningos" ]]; then
      die "Automatic privilege cutover supports the canonical lightningos manager user only; use a guided migration for this layout."
    fi
    ensure_go
    bash "$REPO_ROOT/internal/server/assets/upgrade-app.sh" --prepare-cutover-only
    build_manager
    ensure_manager_service "$manager_user" "$manager_group"
    ensure_privileged_broker_units "$manager_user"
    systemctl daemon-reload
    systemctl enable --now lightningos-privileged.socket
    bash "$REPO_ROOT/internal/server/assets/upgrade-app.sh" --stage-cutover-only
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
  systemctl restart lightningos-manager >/dev/null 2>&1 || true
  if [[ -f /etc/systemd/system/lightningos-reports.timer ]]; then
    systemctl enable --now lightningos-reports.timer
  fi
  local terminal_enabled="0"
  local terminal_credential=""
  if [[ -f /etc/lightningos/secrets.env ]]; then
    terminal_enabled="$(read_env_value TERMINAL_ENABLED /etc/lightningos/secrets.env)"
    terminal_credential="$(read_env_value TERMINAL_CREDENTIAL /etc/lightningos/secrets.env)"
  fi
  if [[ -f /etc/systemd/system/lightningos-terminal.service ]]; then
    if [[ "${terminal_enabled:-0}" == "1" && -n "${terminal_credential:-}" ]]; then
      systemctl enable --now lightningos-terminal >/dev/null 2>&1 || true
      systemctl restart lightningos-terminal >/dev/null 2>&1 || true
    else
      systemctl disable --now lightningos-terminal >/dev/null 2>&1 || true
    fi
  fi

  ensure_manager_firewall

  print_step "Done"
  echo "Log: ${LOG_FILE}"
  echo "Check: systemctl status lightningos-manager --no-pager"
  local lan_ip local_name
  lan_ip=$(get_lan_ip)
  local_name=""
  if [[ -f /etc/lightningos/tls/access.env ]]; then
    local_name=$(sed -n 's/^LOCAL_HOSTNAME=//p' /etc/lightningos/tls/access.env | head -n1)
  fi
  if [[ -n "$local_name" ]]; then
    echo "Open: https://${local_name}:8443 (preferred)"
  fi
  if [[ -n "$lan_ip" ]]; then
    echo "Open: https://${lan_ip}:8443 (IP fallback)"
  else
    echo "Open: https://IP_DA_MAQUINA:8443"
  fi
  echo "Trust the node CA once on each client from the access screen."
  print_auth_setup_token
}

main "$@"
