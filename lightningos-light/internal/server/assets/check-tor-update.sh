#!/usr/bin/env bash
set -Eeuo pipefail
export LC_ALL=C

# apt-cache output is parsed below. Force stable field names regardless of the
# locale configured on the node (for example, Candidate instead of Candidato).
export LC_ALL=C
export LANG=C

TOR_REPO_URL="https://deb.torproject.org/torproject.org"
TOR_REPO_KEY_URL="${TOR_REPO_URL}/A3C4F0F979CAA22CDBA8F512EE8CBC9E886DDD89.asc"
TOR_REPO_KEY_FINGERPRINT="A3C4F0F979CAA22CDBA8F512EE8CBC9E886DDD89"
TOR_KEYRING="/usr/share/keyrings/deb.torproject.org-keyring.gpg"
ASSUME_YES=0
AUTO_CONFIGURE_REPO=0
AUTO_RESTART=0
VERIFY_ONLY=0

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
  --verify-only     Authenticate repository availability and its pinned key
                    without changing APT configuration, packages, or Tor.
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

# Called only after the downloaded key AND repository signature are verified.
# Keep this embedded so the privileged broker authenticates the entire helper.
reconcile_tor_sources() {
  require_command python3
  python3 - /etc/apt "$TOR_KEYRING" "$1" "$2" "$3" <<'TOR_SOURCES_PY'
import json
import os
from pathlib import Path
import re
import stat
import sys
import tempfile


def official_uri(uri):
    return uri.rstrip('/') in (
        'https://deb.torproject.org/torproject.org',
        'http://deb.torproject.org/torproject.org',
    )


def rewrite_list(text, suite):
    result = []
    for line in text.splitlines(keepends=True):
        match = re.match(r'^\s*deb(?:-src)?\s+(?:\[[^\]\n]*\]\s+)?(\S+)\s+(\S+)', line)
        if match and official_uri(match[1]) and match[2] == suite:
            line = '# Disabled by LightningOS Tor repository reconciliation: ' + line
        result.append(line)
    return ''.join(result)


def stanza_fields(block):
    fields = {}
    current = None
    for line in block.splitlines():
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        if line[0].isspace() and current:
            fields[current] += ' ' + line.strip()
            continue
        match = re.match(r'^([A-Za-z0-9-]+):\s*(.*)$', line)
        if not match or match[1].lower() in fields:
            raise RuntimeError('Invalid or duplicate deb822 field; review APT sources before retrying')
        current = match[1].lower()
        fields[current] = match[2]
    return fields


def rewrite_sources(text, suite, managed, canonical):
    # Preserve unrelated stanzas, comments and field continuations. A mixed
    # stanza needs an operator split: never disable unrelated URIs or suites.
    parts = re.split(r'(\r?\n[ \t]*\r?\n)', text)
    for index in range(0, len(parts), 2):
        block = parts[index]
        fields = stanza_fields(block)
        if fields.get('enabled', 'yes').lower() == 'no':
            continue
        uris = fields.get('uris', '').split()
        suites = fields.get('suites', '').split()
        if not any(official_uri(uri) for uri in uris) or suite not in suites:
            continue
        if any(not official_uri(uri) for uri in uris) or any(item != suite for item in suites):
            raise RuntimeError('Mixed Tor/non-Tor URI or suite stanza; split it manually before retrying')
        if managed:
            parts[index] = ''
        else:
            parts[index] = ''.join('# Disabled by LightningOS: ' + line for line in block.splitlines(keepends=True))
    result = ''.join(parts)
    if managed:
        result = result.rstrip()
        result = (result + '\n\n' if result else '') + canonical
    return result


def validate_path(path, directory=False):
    info = path.lstat()
    expected = stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode)
    if not expected or (not directory and info.st_nlink != 1):
        raise RuntimeError('Unsafe APT/keyring path (not a plain directory/file): ' + str(path))
    if os.name == 'posix' and (info.st_uid != 0 or info.st_mode & 0o022):
        raise RuntimeError('APT/keyring path must be root-owned and not group/world writable: ' + str(path))


def atomic_write(path, content, mode):
    fd, temporary = tempfile.mkstemp(prefix='.lightningos-tor-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as output:
            output.write(content)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(temporary, mode)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def reconcile(apt_root, keyring, authenticated_key, suite, architecture):
    sources_dir = apt_root / 'sources.list.d'
    managed = sources_dir / 'tor.sources'
    for path in (apt_root, sources_dir, keyring.parent):
        validate_path(path, directory=True)
    if not re.fullmatch(r'[a-z0-9][a-z0-9-]*', suite) or architecture not in ('amd64', 'arm64'):
        raise RuntimeError('Unsupported Tor repository suite/architecture')
    canonical = ('Types: deb\nURIs: https://deb.torproject.org/torproject.org/\n'
                 f'Suites: {suite}\nComponents: main\nArchitectures: {architecture}\n'
                 f'Signed-By: {keyring}\n')
    paths = [apt_root / 'sources.list'] + sorted(
        path for path in sources_dir.iterdir()
        if re.fullmatch(r'[A-Za-z0-9_.-]+\.(list|sources)', path.name)
    )
    if managed not in paths:
        paths.append(managed)
    originals, updates = {}, {}
    # Preflight ALL files and build the full plan before writing anything.
    for path in paths + [keyring]:
        exists = os.path.lexists(path)
        if exists:
            validate_path(path)
        old = path.read_bytes() if exists else None
        mode = stat.S_IMODE(path.stat().st_mode) if exists else 0o644
        originals[path] = (old, mode)
        if path == keyring:
            new = authenticated_key.read_bytes()
            if not new:
                raise RuntimeError('Authenticated keyring is empty')
        elif old is None and path != managed:
            continue
        else:
            text = old.decode('utf-8') if old is not None else ''
            try:
                if path.suffix == '.sources':
                    new = rewrite_sources(text, suite, path == managed, canonical).encode('utf-8')
                else:
                    new = rewrite_list(text, suite).encode('utf-8')
            except RuntimeError as error:
                raise RuntimeError(str(path) + ': ' + str(error)) from error
        if new != old or (os.name == 'posix' and path == keyring and mode != 0o644):
            updates[path] = new
    if not updates:
        print('[OK] Tor repository sources already reconciled.')
        return
    backup = Path(tempfile.mkdtemp(prefix='lightningos-tor-backup-', dir=apt_root))
    os.chmod(backup, 0o700)
    manifest = []
    for index, path in enumerate(updates):
        old, mode = originals[path]
        saved = f'{index}.original'
        if old is not None:
            (backup / saved).write_bytes(old)
            os.chmod(backup / saved, 0o600)
        manifest.append({'path': str(path), 'backup': saved if old is not None else None, 'mode': mode})
    (backup / 'manifest.json').write_text(json.dumps(manifest, indent=2), encoding='utf-8')
    print('[OK] APT/keyring backup: ' + str(backup), flush=True)
    applied = []
    try:
        for path, content in updates.items():
            atomic_write(path, content, 0o644 if path == keyring else originals[path][1])
            applied.append(path)
    except Exception:
        # Restore only exact paths changed by this invocation. Keep the backup
        # even after rollback so a disk/permission failure remains recoverable.
        for path in reversed(applied):
            old, mode = originals[path]
            if old is None:
                path.unlink()
            else:
                atomic_write(path, old, mode)
        raise
    print(f'[OK] Reconciled {len(updates)} APT/keyring files; unrelated repositories preserved.')


if __name__ == '__main__':
    try:
        reconcile(Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3]), sys.argv[4], sys.argv[5])
    except Exception as error:
        sys.exit('[ERROR] Tor repository reconciliation failed: ' + str(error))
TOR_SOURCES_PY
}

configure_official_repo() {
  local architecture codename tmp_dir key_file keyring_file inrelease_file imported_fingerprint

  require_command curl
  require_command gpg
  require_command gpgv

  architecture=$(dpkg --print-architecture)
  case "$architecture" in
    amd64|arm64) ;;
    *) die "The Tor Project APT repository does not support architecture: ${architecture}" ;;
  esac

  codename=$(get_os_codename)
  [[ -n "$codename" ]] || die "Could not detect the Debian/Ubuntu codename."

  tmp_dir=$(mktemp -d)
  chmod 0700 "$tmp_dir"
  trap 'rm -rf "${tmp_dir:-}"' RETURN EXIT
  key_file="$tmp_dir/tor-project.asc"
  keyring_file="$tmp_dir/tor-project.gpg"
  inrelease_file="$tmp_dir/InRelease"

  print_step "Authenticating Tor Project repository for ${codename}/${architecture}"
  curl --proto '=https' --tlsv1.2 -fsSL "${TOR_REPO_URL}/dists/${codename}/InRelease" -o "$inrelease_file" \
    || die "The Tor Project repository is unavailable for codename: ${codename}"
  curl --proto '=https' --tlsv1.2 -fsSL "$TOR_REPO_KEY_URL" -o "$key_file" \
    || die "Could not download the Tor Project repository key."
  gpg --batch --homedir "$tmp_dir" --import "$key_file" >/dev/null 2>&1 \
    || die "Could not import the Tor Project repository key."
  imported_fingerprint=$(gpg --batch --homedir "$tmp_dir" --with-colons \
    --fingerprint "$TOR_REPO_KEY_FINGERPRINT" 2>/dev/null \
    | awk -F: '$1 == "fpr" { print $10; exit }')
  if [[ "$imported_fingerprint" != "$TOR_REPO_KEY_FINGERPRINT" ]]; then
    die "Tor Project repository key fingerprint mismatch."
  fi
  gpg --batch --homedir "$tmp_dir" --export "$TOR_REPO_KEY_FINGERPRINT" >"$keyring_file" \
    || die "Could not export the pinned Tor Project repository key."
  [[ -s "$keyring_file" ]] || die "Pinned Tor Project repository key export is empty."
  gpgv --keyring "$keyring_file" "$inrelease_file" >/dev/null 2>&1 \
    || die "Tor Project repository metadata signature verification failed."

  if [[ "$VERIFY_ONLY" -eq 1 ]]; then
    print_ok "Tor Project repository and pinned signing key verified; no system state was changed."
    rm -rf "$tmp_dir"
    trap - RETURN EXIT
    return 0
  fi

  reconcile_tor_sources "$keyring_file" "$codename" "$architecture"
  rm -rf "$tmp_dir"
  trap - RETURN EXIT

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
      --verify-only)
        VERIFY_ONLY=1
        AUTO_CONFIGURE_REPO=1
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
  if [[ "$VERIFY_ONLY" -eq 1 ]]; then
    configure_official_repo
    exit 0
  fi

  print_step "Checking Tor package source"
  if official_repo_configured; then
    print_ok "Official Tor Project repository is configured; re-authenticating it."
    configure_official_repo
    repo_ready=1
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

  restart_since=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
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
