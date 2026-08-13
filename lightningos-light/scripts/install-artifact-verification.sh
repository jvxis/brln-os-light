#!/usr/bin/env bash

# Shared by the root-run installers. Callers must verify an artifact before
# listing, extracting, executing, or installing any content from it.

lightningos_verify_sha256() {
  local artifact="$1"
  local expected="$2"
  local label="$3"
  local actual=""

  if [[ ! "$expected" =~ ^[0-9a-f]{64}$ ]]; then
    echo "Invalid pinned SHA-256 for ${label}" >&2
    return 1
  fi
  if [[ ! -f "$artifact" || -L "$artifact" ]]; then
    echo "Downloaded ${label} artifact is not a regular file" >&2
    return 1
  fi
  if ! command -v sha256sum >/dev/null 2>&1; then
    echo "sha256sum is required to verify ${label}" >&2
    return 1
  fi

  actual=$(sha256sum -- "$artifact" | awk '{print $1}')
  if [[ "$actual" != "$expected" ]]; then
    echo "SHA-256 verification failed for ${label}" >&2
    return 1
  fi
}

lightningos_download_verified_artifact() {
  local url="$1"
  local destination="$2"
  local expected="$3"
  local label="$4"

  case "$url" in
    https://*) ;;
    *)
      echo "Refusing non-HTTPS ${label} artifact URL" >&2
      return 1
      ;;
  esac
  if [[ -e "$destination" || -L "$destination" ]]; then
    echo "Refusing existing ${label} download destination" >&2
    return 1
  fi
  if ! curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --tlsv1.2 \
    "$url" --output "$destination"; then
    rm -f -- "$destination"
    echo "Download failed for ${label}" >&2
    return 1
  fi
  if ! lightningos_verify_sha256 "$destination" "$expected" "$label"; then
    rm -f -- "$destination"
    return 1
  fi
}

lightningos_verify_openpgp_primary_fingerprint() {
  local key_file="$1"
  local expected="$2"
  local label="$3"
  local listing=""
  local primary_count=""
  local actual=""

  if [[ ! "$expected" =~ ^[0-9A-F]{40}$ ]]; then
    echo "Invalid pinned OpenPGP fingerprint for ${label}" >&2
    return 1
  fi
  if [[ ! -f "$key_file" || -L "$key_file" ]]; then
    echo "Downloaded ${label} key is not a regular file" >&2
    return 1
  fi
  if ! command -v gpg >/dev/null 2>&1; then
    echo "gpg is required to verify ${label}" >&2
    return 1
  fi
  if ! listing=$(gpg --batch --with-colons --show-keys --fingerprint "$key_file" 2>/dev/null); then
    echo "Downloaded ${label} key is not valid OpenPGP material" >&2
    return 1
  fi
  primary_count=$(printf '%s\n' "$listing" | grep -c '^pub:' || true)
  actual=$(printf '%s\n' "$listing" | grep '^fpr:' | head -n1 | cut -d: -f10 || true)
  if [[ "$primary_count" != "1" || "$actual" != "$expected" ]]; then
    echo "OpenPGP fingerprint verification failed for ${label}" >&2
    return 1
  fi
}

lightningos_install_verified_apt_key() {
  local url="$1"
  local expected="$2"
  local expected_sha256="$3"
  local destination="$4"
  local label="$5"
  local tmp=""

  case "$url" in
    https://*) ;;
    *)
      echo "Refusing non-HTTPS ${label} key URL" >&2
      return 1
      ;;
  esac
  if [[ "$destination" != /* || -L "$destination" ]]; then
    echo "Refusing unsafe ${label} key destination" >&2
    return 1
  fi
  tmp=$(mktemp -d)
  if ! curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --tlsv1.2 \
    "$url" --output "$tmp/key.asc"; then
    rm -rf -- "$tmp"
    echo "Download failed for ${label} key" >&2
    return 1
  fi
  if ! lightningos_verify_sha256 "$tmp/key.asc" "$expected_sha256" "${label} key"; then
    rm -rf -- "$tmp"
    return 1
  fi
  if ! lightningos_verify_openpgp_primary_fingerprint "$tmp/key.asc" "$expected" "$label"; then
    rm -rf -- "$tmp"
    return 1
  fi
  if ! gpg --batch --yes --dearmor --output "$tmp/keyring.gpg" "$tmp/key.asc"; then
    rm -rf -- "$tmp"
    echo "Failed to build ${label} keyring" >&2
    return 1
  fi
  if ! install -o root -g root -m 0644 "$tmp/keyring.gpg" "$destination"; then
    rm -rf -- "$tmp"
    return 1
  fi
  rm -rf -- "$tmp"
}

lightningos_verify_inrelease_envelope() {
  local inrelease="$1"
  local label="$2"
  local begin_signed="-----BEGIN PGP SIGNED MESSAGE-----"
  local begin_signature="-----BEGIN PGP SIGNATURE-----"
  local end_signature="-----END PGP SIGNATURE-----"
  local expected_suffix_hash=""
  local actual_suffix_hash=""
  local suffix_bytes=0

  if [[ ! -f "$inrelease" || -L "$inrelease" ]]; then
    echo "Downloaded ${label} metadata is not a regular file" >&2
    return 1
  fi
  if [[ "$(head -n 1 -- "$inrelease")" != "$begin_signed" ]]; then
    echo "Repository metadata envelope is invalid for ${label}" >&2
    return 1
  fi
  if [[ "$(grep -Fxc -- "$begin_signed" "$inrelease" || true)" != "1" || \
        "$(grep -Fxc -- "$begin_signature" "$inrelease" || true)" != "1" || \
        "$(grep -Fxc -- "$end_signature" "$inrelease" || true)" != "1" ]]; then
    echo "Repository metadata envelope is ambiguous for ${label}" >&2
    return 1
  fi
  suffix_bytes=$((${#end_signature} + 1))
  expected_suffix_hash=$(printf '%s\n' "$end_signature" | sha256sum | awk '{print $1}')
  actual_suffix_hash=$(tail -c "$suffix_bytes" -- "$inrelease" | sha256sum | awk '{print $1}')
  if [[ "$actual_suffix_hash" != "$expected_suffix_hash" ]]; then
    echo "Repository metadata has trailing or malformed bytes for ${label}" >&2
    return 1
  fi
}

lightningos_install_authenticated_apt_key() {
  local key_url="$1"
  local expected_fingerprint="$2"
  local expected_key_sha256="$3"
  local inrelease_url="$4"
  local destination="$5"
  local label="$6"
  local tmp=""

  case "$inrelease_url" in
    https://*) ;;
    *)
      echo "Refusing non-HTTPS ${label} metadata URL" >&2
      return 1
      ;;
  esac
  if [[ "$destination" != /* || -L "$destination" ]]; then
    echo "Refusing unsafe ${label} key destination" >&2
    return 1
  fi
  if ! command -v gpgv >/dev/null 2>&1; then
    echo "gpgv is required to authenticate ${label} metadata" >&2
    return 1
  fi

  tmp=$(mktemp -d)
  if ! lightningos_install_verified_apt_key "$key_url" "$expected_fingerprint" \
    "$expected_key_sha256" "$tmp/keyring.gpg" "$label"; then
    rm -rf -- "$tmp"
    return 1
  fi
  if ! curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --tlsv1.2 \
    "$inrelease_url" --output "$tmp/InRelease"; then
    rm -rf -- "$tmp"
    echo "Download failed for ${label} repository metadata" >&2
    return 1
  fi
  if ! lightningos_verify_inrelease_envelope "$tmp/InRelease" "$label"; then
    rm -rf -- "$tmp"
    return 1
  fi
  if ! gpgv --keyring "$tmp/keyring.gpg" "$tmp/InRelease" >/dev/null 2>&1; then
    rm -rf -- "$tmp"
    echo "Repository metadata authentication failed for ${label}" >&2
    return 1
  fi
  if ! install -o root -g root -m 0644 "$tmp/keyring.gpg" "$destination"; then
    rm -rf -- "$tmp"
    return 1
  fi
  rm -rf -- "$tmp"
}
