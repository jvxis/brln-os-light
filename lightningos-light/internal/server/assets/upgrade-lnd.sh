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
VERIFY_ONLY=0
INSTALL_NEW=0

LND_RELEASE_BASE_URL="https://github.com/lightningnetwork/lnd/releases/download"
LND_RELEASE_KEYS_BASE_URL="https://raw.githubusercontent.com/lightningnetwork/lnd"
MIN_REQUIRED_SIGNATURES=5

# Fingerprints and signer names are pinned from the official LND
# scripts/verify-install.sh verifier. A signature is counted only when its
# VALIDSIG fingerprint matches the corresponding value below exactly.
LND_SIGNERS=(
  "F4FC70F07310028424EFC20A8E4256593F177720:guggero"
  "15E7ECF257098A4EF91655EB4CA7FE54A6213C91:carlaKC"
  "A5B61896952D9FDA83BC054CDC42612E89237182:roasbeef"
  "9FC6B0BFD597A94DBF09708280E5375C094198D8:bhandras"
  "26984CB69EB8C4A26196F7A4D7D916376026F177:ellemouton"
  "C97AAA1470F979878F7A6DEDC3440ACF100A33B4:ffranr"
  "4DC235556B18694E08518DBB671103D881A5F0E4:sputn1ck"
  "E85497D2DBA0EB9ADB0024279BCD95C4FF296868:yyforyongyu"
  "32F7EA1E7A0339F7D37164B9F82D456EA023C9BF:hieblmi"
  "5295A477FFC8064D7057B191FA7E65C951F12439:proofofkeags"
  "3E9BD4436C288039CA827A9200C9E2BC2E45666F:suheb"
  "5F75437E11695F86D50C11BB1AFF9C4DCED6D666:ziggie1984"
  "C20A78516A0944900EBFCA29961CC8259AE675D4:ViktorT-11"
  "1583B601BB57CC7CD2DF8A87E08DEA9B12B66AF6:georgetsagk"
)

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
    --verify-only)
      VERIFY_ONLY=1
      shift
      ;;
    --install-new)
      INSTALL_NEW=1
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
VERSION="${VERSION#v}"
if [[ -z "$VERSION" ]]; then
  die "Missing --version. Example: --version 0.20.1-beta.rc1"
fi

if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([\-\.][0-9A-Za-z\.-]+)?$ ]]; then
  die "Invalid version format: ${VERSION}"
fi

case "$(dpkg --print-architecture 2>/dev/null || uname -m)" in
  amd64|x86_64) LND_ARCH="amd64" ;;
  arm64|aarch64) LND_ARCH="arm64" ;;
  armhf|armv7l) LND_ARCH="armv7" ;;
  *) die "Unsupported LND upgrade architecture." ;;
esac

ARCHIVE="lnd-linux-${LND_ARCH}-v${VERSION}.tar.gz"
MANIFEST="manifest-v${VERSION}.txt"
RELEASE_URL="${LND_RELEASE_BASE_URL}/v${VERSION}"
URL="${RELEASE_URL}/${ARCHIVE}"

if [[ "$VERIFY_ONLY" -eq 1 && "$INSTALL_NEW" -eq 1 ]]; then
  die "--verify-only and --install-new are mutually exclusive."
fi

print_step "Starting LND upgrade to v${VERSION}"
echo "Download URL: ${URL}"

if [[ "$INSTALL_NEW" -eq 1 ]]; then
  if [[ -e /usr/local/bin/lnd || -L /usr/local/bin/lnd || -e /usr/local/bin/lncli || -L /usr/local/bin/lncli ]]; then
    die "Refusing new LND installation over existing binaries."
  fi
elif [[ ! -x /usr/local/bin/lnd ]]; then
  die "LND binary not found at /usr/local/bin/lnd"
fi

current_version=""
if [[ "$INSTALL_NEW" -ne 1 ]]; then
  current_raw=$(/usr/local/bin/lnd --version 2>/dev/null || true)
  current_version=$(parse_version_from_output "$current_raw")
  if [[ -n "$current_version" ]]; then
    echo "Current LND version: v${current_version}"
  else
    print_warn "Could not parse current LND version."
  fi
fi

if [[ -n "$current_version" && "$current_version" == "$VERSION" && "$VERIFY_ONLY" -ne 1 ]]; then
  print_ok "Already running v${VERSION}. No upgrade needed."
  exit 0
fi

if ! command -v curl >/dev/null 2>&1; then
  die "curl is required but not installed."
fi
if ! command -v tar >/dev/null 2>&1; then
  die "tar is required but not installed."
fi
if ! command -v sha256sum >/dev/null 2>&1; then
  die "sha256sum is required but not installed."
fi
if ! command -v gpg >/dev/null 2>&1; then
  die "gpg is required but not installed."
fi

tmp_dir=""
backup_lnd=""
backup_lncli=""
lnd_bin=""
lncli_bin=""
lnd_staged=""
lncli_staged=""
install_new_lnd_committed=0
install_new_lncli_committed=0
rollback_ready=0

cleanup() {
  if [[ -n "$tmp_dir" && -d "$tmp_dir" ]]; then
    rm -rf "$tmp_dir"
  fi
  case "$lnd_staged" in
    /usr/local/bin/.lightningos-lnd-new-*) rm -f -- "$lnd_staged" ;;
  esac
  case "$lncli_staged" in
    /usr/local/bin/.lightningos-lncli-new-*) rm -f -- "$lncli_staged" ;;
  esac
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
    if [[ "$INSTALL_NEW" -eq 1 ]]; then
      if [[ "$install_new_lncli_committed" -eq 1 && -f /usr/local/bin/lncli && ! -L /usr/local/bin/lncli && -n "$lncli_bin" ]] \
        && cmp -s -- /usr/local/bin/lncli "$lncli_bin"; then
        rm -f -- /usr/local/bin/lncli
      fi
      if [[ "$install_new_lnd_committed" -eq 1 && -f /usr/local/bin/lnd && ! -L /usr/local/bin/lnd && -n "$lnd_bin" ]] \
        && cmp -s -- /usr/local/bin/lnd "$lnd_bin"; then
        rm -f -- /usr/local/bin/lnd
      fi
    fi
    if [[ $rollback_ready -eq 1 ]]; then
      rollback
    fi
  fi
  cleanup
}

trap on_exit EXIT

print_step "Downloading LND tarball"
tmp_dir=$(mktemp -d)
curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL "$URL" -o "$tmp_dir/$ARCHIVE"

print_step "Authenticating the official LND release manifest"
curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL "${RELEASE_URL}/${MANIFEST}" -o "$tmp_dir/$MANIFEST"
mkdir -m 0700 "$tmp_dir/gnupg"

valid_signatures=0
for signer in "${LND_SIGNERS[@]}"; do
  fingerprint="${signer%%:*}"
  username="${signer#*:}"
  signature="manifest-${username}-v${VERSION}.sig"
  signature_path="$tmp_dir/$signature"

  # Each release is signed by a subset of the pinned maintainers. A missing
  # optional signature is expected and must not look like a failed upgrade in
  # the UI log. Authentication still fails closed below unless the threshold
  # of valid pinned signatures is reached.
  if ! curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL \
    "${RELEASE_URL}/${signature}" -o "$signature_path" 2>/dev/null; then
    rm -f "$signature_path"
    continue
  fi

  key_path="$tmp_dir/${username}.asc"
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL \
    "${LND_RELEASE_KEYS_BASE_URL}/v${VERSION}/scripts/keys/${username}.asc" \
    -o "$key_path" \
    || die "Could not retrieve the LND release key for ${username}."
  gpg --batch --homedir "$tmp_dir/gnupg" --import "$key_path" >/dev/null 2>&1 \
    || die "Could not import the LND release key for ${username}."

  imported_fingerprint=$(gpg --batch --homedir "$tmp_dir/gnupg" --with-colons \
    --fingerprint "$fingerprint" 2>/dev/null \
    | awk -F: '$1 == "fpr" { print $10; exit }')
  if [[ "$imported_fingerprint" != "$fingerprint" ]]; then
    die "Pinned LND signing key fingerprint mismatch for ${username}."
  fi

  status_file="$tmp_dir/${username}.status"
  if ! gpg --batch --homedir "$tmp_dir/gnupg" --status-fd=1 \
    --verify "$signature_path" "$tmp_dir/$MANIFEST" >"$status_file" 2>/dev/null; then
    die "Invalid LND release signature from ${username}."
  fi
  # VALIDSIG field 3 is the key that made the signature. When a signing
  # subkey is used, field 12 is the cryptographically linked primary-key
  # fingerprint. Accept only the pinned primary key itself or one of its
  # subkeys; a signature made by another imported LND key cannot count for
  # this signer name.
  valid_fingerprints=$(awk '
    $1 == "[GNUPG:]" && $2 == "VALIDSIG" {
      print $3
      if (NF >= 12) print $12
      exit
    }
  ' "$status_file")
  if ! grep -qx "$fingerprint" <<<"$valid_fingerprints"; then
    die "LND release signature fingerprint mismatch for ${username}."
  fi
  valid_signatures=$((valid_signatures + 1))
done

if [[ "$valid_signatures" -lt "$MIN_REQUIRED_SIGNATURES" ]]; then
  die "Not enough valid LND release signatures: ${valid_signatures}/${MIN_REQUIRED_SIGNATURES}."
fi
print_ok "Authenticated ${MANIFEST} with ${valid_signatures} pinned LND signers."

print_step "Verifying the LND archive before extraction"
expected_hash=$(awk -v archive="$ARCHIVE" '
  $2 == archive || $2 == "*" archive {
    if (found) exit 2
    print $1
    found=1
  }
  END { if (!found) exit 1 }
' "$tmp_dir/$MANIFEST") || die "The authenticated manifest has no unique checksum for ${ARCHIVE}."
if [[ ! "$expected_hash" =~ ^[0-9a-fA-F]{64}$ ]]; then
  die "The authenticated manifest contains an invalid checksum for ${ARCHIVE}."
fi
actual_hash=$(sha256sum "$tmp_dir/$ARCHIVE" | awk '{print $1}')
if [[ "${actual_hash,,}" != "${expected_hash,,}" ]]; then
  die "LND archive checksum does not match the authenticated manifest."
fi
print_ok "Archive SHA-256 matches the authenticated manifest."

archive_root="lnd-linux-${LND_ARCH}-v${VERSION}"
if ! tar -tzf "$tmp_dir/$ARCHIVE" | awk -v root="${archive_root}/" '
  index($0, root) != 1 || $0 ~ /(^|\/)\.\.($|\/)/ || $0 ~ /^\// { exit 1 }
'; then
  die "LND archive contains an unsafe path."
fi
tar --no-same-owner --no-same-permissions -xzf "$tmp_dir/$ARCHIVE" -C "$tmp_dir"

lnd_bin="$tmp_dir/$archive_root/lnd"
lncli_bin="$tmp_dir/$archive_root/lncli"
if [[ ! -f "$lnd_bin" || -L "$lnd_bin" || ! -f "$lncli_bin" || -L "$lncli_bin" ]]; then
  die "Unexpected LND archive contents."
fi

archive_lnd_version=$(parse_version_from_output "$("$lnd_bin" --version 2>/dev/null || true)")
if [[ -z "$archive_lnd_version" ]]; then
  die "Could not read the verified LND archive version."
fi
if [[ "$archive_lnd_version" != "$VERSION" && "$(base_version "$VERSION")" != "$archive_lnd_version" ]]; then
  die "Verified LND archive version does not match the requested version."
fi

if [[ "$VERIFY_ONLY" -eq 1 ]]; then
  print_ok "Official LND v${VERSION} release verification complete; no service or binary was changed."
  exit 0
fi

if [[ "$INSTALL_NEW" -eq 1 ]]; then
  print_step "Installing verified LND binaries"
  lnd_staged="/usr/local/bin/.lightningos-lnd-new-$$"
  lncli_staged="/usr/local/bin/.lightningos-lncli-new-$$"
  if [[ -e "$lnd_staged" || -L "$lnd_staged" || -e "$lncli_staged" || -L "$lncli_staged" ]]; then
    die "Refusing existing LND staging paths."
  fi
  install -o root -g root -m 0755 "$lnd_bin" "$lnd_staged" \
    || die "Failed to stage the verified lnd binary."
  if ! install -o root -g root -m 0755 "$lncli_bin" "$lncli_staged"; then
    rm -f -- "$lnd_staged"
    die "Failed to stage the verified lncli binary."
  fi
  if [[ -e /usr/local/bin/lnd || -L /usr/local/bin/lnd || -e /usr/local/bin/lncli || -L /usr/local/bin/lncli ]]; then
    rm -f -- "$lnd_staged" "$lncli_staged"
    die "LND binaries appeared during installation."
  fi
  if ! mv --no-clobber -- "$lnd_staged" /usr/local/bin/lnd || [[ -e "$lnd_staged" ]]; then
    rm -f -- "$lnd_staged" "$lncli_staged"
    die "Failed to commit the verified lnd binary."
  fi
  install_new_lnd_committed=1
  if ! mv --no-clobber -- "$lncli_staged" /usr/local/bin/lncli || [[ -e "$lncli_staged" ]]; then
    die "Failed to commit the verified lncli binary."
  fi
  install_new_lncli_committed=1
  installed_version=$(parse_version_from_output "$(/usr/local/bin/lnd --version 2>/dev/null || true)")
  if [[ "$installed_version" != "$VERSION" && "$(base_version "$VERSION")" != "$installed_version" ]]; then
    die "Installed LND version does not match the authenticated release."
  fi
  install_new_lnd_committed=0
  install_new_lncli_committed=0
  print_ok "Installed authenticated LND v${installed_version}; service start remains with the installer."
  exit 0
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
