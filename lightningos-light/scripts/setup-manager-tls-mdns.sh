#!/usr/bin/env bash
set -Eeuo pipefail

TLS_DIR="${LIGHTNINGOS_TLS_DIR:-/etc/lightningos/tls}"
MANAGER_GROUP="${LIGHTNINGOS_MANAGER_GROUP:-lightningos}"
MANAGER_PORT="${LIGHTNINGOS_MANAGER_PORT:-8443}"
CERT_PATH="${TLS_DIR}/server.crt"
KEY_PATH="${TLS_DIR}/server.key"
CA_CERT_PATH="${TLS_DIR}/local-ca.crt"
CA_KEY_PATH="${TLS_DIR}/local-ca.key"
CA_SERIAL_PATH="${TLS_DIR}/local-ca.srl"
ACCESS_INFO_PATH="${TLS_DIR}/access.env"
AVAHI_SERVICE_PATH="${LIGHTNINGOS_AVAHI_SERVICE_PATH:-/etc/avahi/services/lightningos.service}"

log() {
  printf '[TLS] %s\n' "$*"
}

warn() {
  printf '[TLS] WARN: %s\n' "$*" >&2
}

require_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    echo "This helper must run as root" >&2
    exit 1
  fi
}

safe_host_label() {
  local raw="${LIGHTNINGOS_LOCAL_NAME:-}"
  if [[ -z "$raw" ]]; then
    raw=$(hostname -s 2>/dev/null || true)
  fi
  raw=${raw%.local}
  raw=$(printf '%s' "$raw" \
    | tr '[:upper:]' '[:lower:]' \
    | tr -cs 'a-z0-9-' '-' \
    | sed -E 's/^-+//; s/-+$//; s/-+/-/g' \
    | cut -c1-63)
  if [[ -z "$raw" ]]; then
    raw="lightningos"
  fi
  printf '%s' "$raw"
}

default_route_ip() {
  local detected="${LIGHTNINGOS_LAN_IP:-}"
  if [[ -z "$detected" ]]; then
    detected=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1; i<=NF; i++) if ($i=="src") {print $(i+1); exit}}')
  fi
  if [[ "$detected" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ && "$detected" != 127.* ]]; then
    printf '%s' "$detected"
  fi
}

cert_subject() {
  openssl x509 -in "$1" -noout -subject -nameopt RFC2253 2>/dev/null | sed 's/^subject=//'
}

cert_issuer() {
  openssl x509 -in "$1" -noout -issuer -nameopt RFC2253 2>/dev/null | sed 's/^issuer=//'
}

cert_has_san() {
  openssl x509 -in "$1" -noout -ext subjectAltName 2>/dev/null | grep -q 'Subject Alternative Name'
}

cert_matches_key() {
  local cert_hash key_hash
  cert_hash=$(openssl x509 -in "$1" -pubkey -noout 2>/dev/null | openssl pkey -pubin -outform DER 2>/dev/null | sha256sum | awk '{print $1}')
  key_hash=$(openssl pkey -in "$2" -pubout -outform DER 2>/dev/null | sha256sum | awk '{print $1}')
  [[ -n "$cert_hash" && "$cert_hash" == "$key_hash" ]]
}

is_legacy_los_certificate() {
  [[ -s "$CERT_PATH" && -s "$KEY_PATH" ]] || return 1
  cert_matches_key "$CERT_PATH" "$KEY_PATH" || return 1
  cert_has_san "$CERT_PATH" && return 1

  local subject issuer
  subject=$(cert_subject "$CERT_PATH")
  issuer=$(cert_issuer "$CERT_PATH")
  [[ -n "$subject" && "$subject" == "$issuer" ]] || return 1
  [[ "$subject" == *"CN=localhost"* || "$subject" == *"CN=$(hostname -s 2>/dev/null || true)"* || "$subject" == *"CN=$(hostname -f 2>/dev/null || true)"* ]]
}

certificate_is_current() {
  local mdns_name="$1"
  local lan_ip="$2"
  [[ -s "$CERT_PATH" && -s "$KEY_PATH" && -s "$CA_CERT_PATH" ]] || return 1
  cert_matches_key "$CERT_PATH" "$KEY_PATH" || return 1
  openssl verify -CAfile "$CA_CERT_PATH" "$CERT_PATH" >/dev/null 2>&1 || return 1
  openssl x509 -in "$CERT_PATH" -noout -checkhost "$mdns_name" >/dev/null 2>&1 || return 1
  if [[ -n "$lan_ip" ]]; then
    openssl x509 -in "$CERT_PATH" -noout -checkip "$lan_ip" >/dev/null 2>&1 || return 1
  fi
}

ensure_local_ca() {
  local host_label="$1"
  if [[ -s "$CA_CERT_PATH" && -s "$CA_KEY_PATH" ]]; then
    return 0
  fi

  local tmp_dir
  tmp_dir=$(mktemp -d "${TLS_DIR}/.ca.XXXXXX")
  trap 'rm -rf -- "${tmp_dir:-}"' RETURN
  log "Creating a node-local certificate authority"
  openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
    -subj "/CN=LightningOS Local CA - ${host_label}" \
    -addext "basicConstraints=critical,CA:TRUE" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" \
    -addext "subjectKeyIdentifier=hash" \
    -keyout "$tmp_dir/local-ca.key" \
    -out "$tmp_dir/local-ca.crt" >/dev/null 2>&1

  install -m 0600 -o root -g root "$tmp_dir/local-ca.key" "$CA_KEY_PATH"
  install -m 0644 -o root -g root "$tmp_dir/local-ca.crt" "$CA_CERT_PATH"
  rm -rf -- "$tmp_dir"
  trap - RETURN
}

backup_existing_certificate() {
  [[ -e "$CERT_PATH" || -e "$KEY_PATH" ]] || return 0
  local backup_dir="${TLS_DIR}/backups/$(date +%Y%m%d-%H%M%S)"
  install -d -m 0700 -o root -g root "$backup_dir"
  [[ -e "$CERT_PATH" ]] && cp -a -- "$CERT_PATH" "$backup_dir/server.crt"
  [[ -e "$KEY_PATH" ]] && cp -a -- "$KEY_PATH" "$backup_dir/server.key"
  log "Previous manager certificate backed up at ${backup_dir}"
}

issue_server_certificate() {
  local host_label="$1"
  local mdns_name="$2"
  local lan_ip="$3"
  local cert_group="$MANAGER_GROUP"
  if ! getent group "$cert_group" >/dev/null 2>&1; then
    cert_group="root"
  fi

  ensure_local_ca "$host_label"
  backup_existing_certificate

  local tmp_dir
  tmp_dir=$(mktemp -d "${TLS_DIR}/.server-cert.XXXXXX")
  trap 'rm -rf -- "${tmp_dir:-}"' RETURN

  cat > "$tmp_dir/server.cnf" <<EOF
[req]
distinguished_name=req_dn
prompt=no

[req_dn]
CN=${mdns_name}
O=LightningOS

[server_ext]
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid,issuer
subjectAltName=@alt_names

[alt_names]
DNS.1=localhost
DNS.2=${host_label}
DNS.3=${mdns_name}
IP.1=127.0.0.1
EOF
  if [[ -n "$lan_ip" ]]; then
    printf 'IP.2=%s\n' "$lan_ip" >> "$tmp_dir/server.cnf"
  fi

  log "Issuing manager certificate for ${mdns_name}${lan_ip:+ and ${lan_ip}}"
  openssl req -new -newkey rsa:3072 -sha256 -nodes \
    -config "$tmp_dir/server.cnf" \
    -keyout "$tmp_dir/server.key" \
    -out "$tmp_dir/server.csr" >/dev/null 2>&1
  openssl x509 -req -sha256 -days 825 \
    -in "$tmp_dir/server.csr" \
    -CA "$CA_CERT_PATH" \
    -CAkey "$CA_KEY_PATH" \
    -CAserial "$CA_SERIAL_PATH" \
    -CAcreateserial \
    -extfile "$tmp_dir/server.cnf" \
    -extensions server_ext \
    -out "$tmp_dir/server.crt" >/dev/null 2>&1

  openssl verify -CAfile "$CA_CERT_PATH" "$tmp_dir/server.crt" >/dev/null
  openssl x509 -in "$tmp_dir/server.crt" -noout -checkhost "$mdns_name" >/dev/null
  if [[ -n "$lan_ip" ]]; then
    openssl x509 -in "$tmp_dir/server.crt" -noout -checkip "$lan_ip" >/dev/null
  fi
  cert_matches_key "$tmp_dir/server.crt" "$tmp_dir/server.key"

  install -m 0640 -o root -g "$cert_group" "$tmp_dir/server.crt" "$CERT_PATH"
  install -m 0640 -o root -g "$cert_group" "$tmp_dir/server.key" "$KEY_PATH"
  chmod 0600 "$CA_KEY_PATH"
  chmod 0644 "$CA_CERT_PATH"
  rm -rf -- "$tmp_dir"
  trap - RETURN
}

ensure_manager_certificate() {
  local host_label="$1"
  local mdns_name="$2"
  local lan_ip="$3"

  if certificate_is_current "$mdns_name" "$lan_ip"; then
    log "Manager certificate already covers the current LAN name and address"
    return 0
  fi

  if [[ -s "$CERT_PATH" && -s "$KEY_PATH" ]] && ! is_legacy_los_certificate; then
    if [[ -s "$CA_CERT_PATH" && -s "$CA_KEY_PATH" ]] \
      && openssl verify -CAfile "$CA_CERT_PATH" "$CERT_PATH" >/dev/null 2>&1; then
      issue_server_certificate "$host_label" "$mdns_name" "$lan_ip"
      return 0
    fi
    warn "Preserving an existing custom certificate; automatic local CA was not applied"
    return 0
  fi

  issue_server_certificate "$host_label" "$mdns_name" "$lan_ip"
}

configure_avahi_service() {
  local host_label="$1"
  local service_dir
  service_dir=$(dirname "$AVAHI_SERVICE_PATH")
  install -d -m 0755 -o root -g root "$service_dir"
  cat > "$AVAHI_SERVICE_PATH" <<EOF
<?xml version="1.0" standalone='no'?><!--*-nxml-*-->
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<service-group>
  <name replace-wildcards="yes">LightningOS on %h</name>
  <service>
    <type>_https._tcp</type>
    <port>${MANAGER_PORT}</port>
    <txt-record>path=/</txt-record>
    <txt-record>product=LightningOS</txt-record>
    <txt-record>host=${host_label}.local</txt-record>
  </service>
</service-group>
EOF
  chmod 0644 "$AVAHI_SERVICE_PATH"

  if systemctl list-unit-files avahi-daemon.service >/dev/null 2>&1; then
    systemctl enable --now avahi-daemon.service >/dev/null 2>&1 || warn "Could not enable avahi-daemon"
    systemctl restart avahi-daemon.service >/dev/null 2>&1 || warn "Could not restart avahi-daemon"
  else
    warn "avahi-daemon is not installed; ${host_label}.local will not be announced yet"
  fi
}

write_access_info() {
  local mdns_name="$1"
  local lan_ip="$2"
  local ca_available="0"
  [[ -s "$CA_CERT_PATH" ]] && ca_available="1"
  {
    printf 'LOCAL_HOSTNAME=%s\n' "$mdns_name"
    printf 'LAN_IP=%s\n' "$lan_ip"
    printf 'PORT=%s\n' "$MANAGER_PORT"
    printf 'LOCAL_CA_AVAILABLE=%s\n' "$ca_available"
  } > "$ACCESS_INFO_PATH"
  chmod 0644 "$ACCESS_INFO_PATH"
}

print_summary() {
  local mdns_name="$1"
  local lan_ip="$2"
  local fingerprint=""
  if [[ -s "$CA_CERT_PATH" ]]; then
    fingerprint=$(openssl x509 -in "$CA_CERT_PATH" -noout -fingerprint -sha256 2>/dev/null | cut -d= -f2-)
  fi
  printf 'LOCAL_URL=https://%s:%s\n' "$mdns_name" "$MANAGER_PORT"
  if [[ -n "$lan_ip" ]]; then
    printf 'IP_URL=https://%s:%s\n' "$lan_ip" "$MANAGER_PORT"
  fi
  if [[ -n "$fingerprint" ]]; then
    printf 'CA_SHA256=%s\n' "$fingerprint"
  fi
}

main() {
  require_root
  command -v openssl >/dev/null 2>&1 || { echo "openssl is required" >&2; exit 1; }
  command -v ip >/dev/null 2>&1 || { echo "iproute2 is required" >&2; exit 1; }

  local tls_group="$MANAGER_GROUP"
  if ! getent group "$tls_group" >/dev/null 2>&1; then
    tls_group="root"
  fi
  install -d -m 0750 -o root -g "$tls_group" "$TLS_DIR"
  local host_label mdns_name lan_ip
  host_label=$(safe_host_label)
  mdns_name="${host_label}.local"
  lan_ip=$(default_route_ip)

  ensure_manager_certificate "$host_label" "$mdns_name" "$lan_ip"
  if [[ "${LIGHTNINGOS_CONFIGURE_AVAHI:-1}" != "0" ]]; then
    configure_avahi_service "$host_label"
  fi
  write_access_info "$mdns_name" "$lan_ip"
  print_summary "$mdns_name" "$lan_ip"
}

main "$@"
