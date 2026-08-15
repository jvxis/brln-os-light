#!/usr/bin/env bash
set -Eeuo pipefail
set -o errtrace

LOG_FILE="/var/log/lightningos-app-upgrade.log"

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

resolve_bin() {
  local name="$1"
  shift

  local candidate=""
  candidate="$(command -v "$name" 2>/dev/null || true)"
  if [[ -n "$candidate" ]]; then
    echo "$candidate"
    return 0
  fi

  for candidate in "$@"; do
    if [[ -x "$candidate" ]]; then
      echo "$candidate"
      return 0
    fi
  done

  return 1
}

VERSION=""
TAG=""
EXPECTED_COMMIT=""
VERIFY_ONLY=0
CUTOVER_ONLY_MODE=""
TRUSTED_CHECKOUT=0
REPO_URL="https://github.com/jvxis/brln-os-light.git"
RELEASE_TAG_API_BASE="https://api.github.com/repos/jvxis/brln-os-light/releases/tags"

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
    --tag)
      TAG="${2:-}"
      shift 2
      ;;
    --tag=*)
      TAG="${1#*=}"
      shift
      ;;
    --commit)
      EXPECTED_COMMIT="${2:-}"
      shift 2
      ;;
    --commit=*)
      EXPECTED_COMMIT="${1#*=}"
      shift
      ;;
    --verify-only)
      VERIFY_ONLY=1
      shift
      ;;
    --prepare-cutover-only)
      [[ -z "$CUTOVER_ONLY_MODE" ]] || die "Only one cutover-only mode is allowed."
      CUTOVER_ONLY_MODE="prepare"
      shift
      ;;
    --stage-cutover-only)
      [[ -z "$CUTOVER_ONLY_MODE" ]] || die "Only one cutover-only mode is allowed."
      CUTOVER_ONLY_MODE="stage"
      shift
      ;;
    --trusted-checkout)
      TRUSTED_CHECKOUT=1
      shift
      ;;
    *)
      die "Unknown argument: $1"
      ;;
  esac
done

require_root

if [[ -n "$CUTOVER_ONLY_MODE" && "$TRUSTED_CHECKOUT" -eq 1 ]]; then
  die "Cutover-only and trusted-checkout modes cannot be combined."
fi
if [[ "$VERIFY_ONLY" -eq 1 && "$TRUSTED_CHECKOUT" -eq 1 ]]; then
  die "Trusted-checkout mode performs a local operator upgrade and cannot be verify-only."
fi

export PATH="/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

ensure_native_app_identity() {
  local user="$1"
  local home="$2"
  if ! /usr/bin/getent group "$user" >/dev/null 2>&1; then
    /usr/sbin/groupadd --system "$user"
  fi
  if /usr/bin/id "$user" >/dev/null 2>&1; then
    [[ "$(/usr/bin/id -gn "$user")" == "$user" ]] || die "Existing ${user} account has an incompatible primary group"
    return 0
  fi
  /usr/sbin/useradd --system --gid "$user" --home-dir "$home" --no-create-home --shell /usr/sbin/nologin "$user"
}

ensure_native_app_identities() {
  ensure_native_app_identity lightningos-loop /var/lib/lightningos/apps-data/loop
  ensure_native_app_identity lightningos-elements /data/elements
  ensure_native_app_identity lightningos-peerswap /var/lib/lightningos/apps-data/peerswap/runtime
}

if [[ -z "$CUTOVER_ONLY_MODE" ]]; then
  VERSION="${VERSION#v}"
  VERSION="$(echo "${VERSION}" | tr -s '[:space:]' ' ' | sed 's/^ *//;s/ *$//;s/ /-/g')"
  if [[ -z "$VERSION" ]]; then
    die "Missing --version."
  fi
  if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([\-][0-9A-Za-z][0-9A-Za-z\.-]*)?$ ]]; then
    die "Invalid version format: ${VERSION}"
  fi

  if [[ "$TRUSTED_CHECKOUT" -eq 0 ]]; then
    if [[ -z "$TAG" ]]; then
      TAG="v${VERSION}"
    fi
    if [[ "$TAG" =~ [[:space:]] ]]; then
      die "Invalid tag format."
    fi
    if [[ ! "$TAG" =~ ^[vV]?[0-9]+\.[0-9]+\.[0-9]+([\-][0-9A-Za-z][0-9A-Za-z\.-]*)?$ ]]; then
      die "Invalid tag format."
    fi
    normalized_tag="${TAG#[vV]}"
    if [[ "${normalized_tag,,}" != "${VERSION,,}" ]]; then
      die "Tag does not match version."
    fi
  elif [[ -n "$TAG" ]]; then
    die "Trusted-checkout mode does not accept --tag."
  fi
  EXPECTED_COMMIT="${EXPECTED_COMMIT,,}"
  if [[ ! "$EXPECTED_COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
    die "Invalid or missing --commit."
  fi
fi

LOCK_FILE="/var/lib/lightningos/app-upgrade.lock"
PRIVILEGED_BROKER="/usr/local/libexec/lightningos-privileged"
PRIVILEGED_TMPFILES_CONFIG="/etc/tmpfiles.d/lightningos-privileged.conf"
GIT_BIN="$(resolve_bin git /usr/bin/git /bin/git)" || die "Required command missing: git"
CURL_BIN="$(resolve_bin curl /usr/bin/curl /bin/curl)" || die "Required command missing: curl"
RM_BIN="$(resolve_bin rm /usr/bin/rm /bin/rm)" || die "Required command missing: rm"

verify_immutable_release() {
  local response=""
  response=$("$CURL_BIN" --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 15 --max-time 45 --max-filesize 1048576 -fsSL \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2026-03-10" \
    -H "User-Agent: lightningos-upgrade" \
    "${RELEASE_TAG_API_BASE}/${TAG}") \
    || die "Failed to retrieve the LightningOS release attestation."
  printf '%s' "$response" | grep -Eq '"immutable"[[:space:]]*:[[:space:]]*true([,}])' \
    || die "LightningOS release is not immutable and attested."
  printf '%s' "$response" | grep -Eq '"draft"[[:space:]]*:[[:space:]]*false([,}])' \
    || die "LightningOS release is not published."
  printf '%s' "$response" | grep -Fq "\"tag_name\":\"${TAG}\"" \
    || die "LightningOS release attestation tag mismatch."
}

verify_release_ref() {
  local verify_root=""
  local actual_commit=""
  local version_path=""
  local source_version=""

  cleanup_verify_root() {
    [[ -n "$verify_root" && -d "$verify_root" && ! -L "$verify_root" ]] || return 0
    case "$verify_root" in
      /tmp/lightningos-release-verify.*)
        "$RM_BIN" -rf -- "$verify_root"
        ;;
      *)
        return 1
        ;;
    esac
  }

  verify_immutable_release
  verify_root="$(mktemp -d /tmp/lightningos-release-verify.XXXXXX)"
  chmod 0700 "$verify_root"
  trap cleanup_verify_root RETURN EXIT
  "$GIT_BIN" init --bare "$verify_root/repository.git" >/dev/null
  "$GIT_BIN" -C "$verify_root/repository.git" remote add origin "$REPO_URL"
  "$GIT_BIN" -C "$verify_root/repository.git" fetch --force --depth=1 origin \
    "refs/tags/${TAG}:refs/tags/${TAG}"
  actual_commit="$("$GIT_BIN" -C "$verify_root/repository.git" rev-parse "refs/tags/${TAG}^{commit}")"
  actual_commit="${actual_commit,,}"
  if [[ "$actual_commit" != "$EXPECTED_COMMIT" ]]; then
    die "Release tag does not resolve to the expected commit."
  fi

  for version_path in lightningos-light/ui/public/version.txt ui/public/version.txt; do
    if "$GIT_BIN" -C "$verify_root/repository.git" cat-file -e "${actual_commit}:${version_path}" 2>/dev/null; then
      source_version="$("$GIT_BIN" -C "$verify_root/repository.git" show "${actual_commit}:${version_path}")"
      break
    fi
  done
  source_version="${source_version#v}"
  source_version="$(echo "$source_version" | tr '[:upper:]_' '[:lower:]-' | tr -d '[:space:]')"
  if [[ "$source_version" != "${VERSION,,}" ]]; then
    die "Release source version does not match the requested version."
  fi

  cleanup_verify_root
  verify_root=""
  trap - RETURN EXIT
  print_ok "Release tag, commit, and source version are consistent"
}

if [[ "$VERIFY_ONLY" -eq 1 ]]; then
  verify_release_ref
  print_ok "LightningOS release source verified; no application files or services were changed."
  exit 0
fi

mkdir -p /var/log /var/lib/lightningos /opt/lightningos/manager /opt/lightningos/ui
exec > >(tee -a "$LOG_FILE") 2>&1

GO_BIN="$(resolve_bin go /usr/local/go/bin/go /usr/bin/go /bin/go)" || die "Required command missing: go"
NPM_BIN="$(resolve_bin npm /usr/bin/npm /usr/local/bin/npm /bin/npm)" || die "Required command missing: npm"
SYSTEMCTL_BIN="$(resolve_bin systemctl /usr/bin/systemctl /bin/systemctl)" || die "Required command missing: systemctl"
INSTALL_BIN="$(resolve_bin install /usr/bin/install /bin/install)" || die "Required command missing: install"
CP_BIN="$(resolve_bin cp /usr/bin/cp /bin/cp)" || die "Required command missing: cp"
MV_BIN="$(resolve_bin mv /usr/bin/mv /bin/mv)" || die "Required command missing: mv"
FLOCK_BIN="$(resolve_bin flock /usr/bin/flock /bin/flock)" || die "Required command missing: flock"
DATE_BIN="$(resolve_bin date /usr/bin/date /bin/date)" || die "Required command missing: date"
STAT_BIN="$(resolve_bin stat /usr/bin/stat /bin/stat)" || die "Required command missing: stat"
RUNUSER_BIN="$(resolve_bin runuser /usr/sbin/runuser /usr/bin/runuser /sbin/runuser)" || die "Required command missing: runuser"
TAR_BIN="$(resolve_bin tar /usr/bin/tar /bin/tar)" || die "Required command missing: tar"
OPENSSL_BIN="$(resolve_bin openssl /usr/bin/openssl /bin/openssl)" || die "Required command missing: openssl"
GPASSWD_BIN="$(resolve_bin gpasswd /usr/bin/gpasswd /usr/sbin/gpasswd /bin/gpasswd)" || die "Required command missing: gpasswd"
USERMOD_BIN="$(resolve_bin usermod /usr/sbin/usermod /usr/bin/usermod /sbin/usermod)" || die "Required command missing: usermod"
USERADD_BIN="$(resolve_bin useradd /usr/sbin/useradd /usr/bin/useradd /sbin/useradd)" || die "Required command missing: useradd"
APT_GET_BIN="$(resolve_bin apt-get /usr/bin/apt-get /bin/apt-get)" || true

available_kib="$(df -Pk /var/lib/lightningos | awk 'NR == 2 { print $4 }')"
[[ "$available_kib" =~ ^[0-9]+$ ]] || die "Could not determine free space for the application upgrade."
if (( available_kib < 3145728 )); then
  die "Application upgrade requires at least 3 GiB of free space on the system volume."
fi

exec 9>"$LOCK_FILE"
if ! "$FLOCK_BIN" -n 9; then
  die "Another app upgrade is already running."
fi

mirror_root="/var/lib/lightningos/src/brln-os-light"
worktree_root="/var/lib/lightningos/worktrees"
worktree_dir=""
project_dir=""
cutover_prepared=0
legacy_identity_normalized=0
legacy_migrator=""

owned_by_root() {
  local path=""
  local owner_uid=""
  for path in "$@"; do
    [[ -e "$path" ]] || continue
    owner_uid="$("$STAT_BIN" -c '%u' "$path" 2>/dev/null || true)"
    if [[ "$owner_uid" != "0" ]]; then
      return 1
    fi
  done
  return 0
}

cleanup() {
  local exit_status=$?
  trap - EXIT
  if [[ "$exit_status" -ne 0 && "$cutover_prepared" -eq 1 && -x /usr/local/sbin/lightningos-rollback-privilege-cutover ]]; then
    print_warn "Upgrade failed after preparing the transaction; restoring Manager, UI, and privilege boundary"
    /usr/local/sbin/lightningos-rollback-privilege-cutover || true
  fi
  if [[ "$exit_status" -ne 0 && "$legacy_identity_normalized" -eq 1 && -n "$legacy_migrator" ]]; then
    print_warn "Upgrade failed after normalizing a legacy Manager; restoring its previous identity"
    /usr/bin/bash "$legacy_migrator" --rollback || true
  fi
  if [[ -n "$worktree_dir" && -d "$worktree_dir" ]]; then
    "$GIT_BIN" -C "$mirror_root" worktree remove --force "$worktree_dir" >/dev/null 2>&1 || true
    "$RM_BIN" -rf "$worktree_dir" >/dev/null 2>&1 || true
  fi
  exit "$exit_status"
}
trap cleanup EXIT

validate_root_regular_file() {
  local path="$1"
  [[ -f "$path" && ! -L "$path" ]] || return 1
  local owner="" group="" mode=""
  owner="$("$STAT_BIN" -c '%u' "$path" 2>/dev/null)" || return 1
  group="$("$STAT_BIN" -c '%g' "$path" 2>/dev/null)" || return 1
  mode="$("$STAT_BIN" -c '%a' "$path" 2>/dev/null)" || return 1
  [[ "$owner" == "0" && "$group" == "0" ]] || return 1
  (( (8#$mode & 8#022) == 0 ))
}

validate_legacy_manager_sudoers() {
  local path="$1"
  local manager_user="$2"
  [[ ! -e "$path" ]] && return 0
  validate_root_regular_file "$path" || return 1
  [[ "$manager_user" == "lightningos" ]] || return 1
  local alias_suffix="LIGHTNINGOS"
  local system_alias="LIGHTNINGOS_SYSTEM_${alias_suffix}"
  local app_alias="LIGHTNINGOS_APPS_${alias_suffix}"
  local saw_system=0 saw_apps=0 saw_grant=0 line=""
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" != *$'\r'* ]] || return 1
    [[ -n "$line" ]] || continue
    if [[ "$line" == "Defaults:${manager_user} !requiretty" ]]; then
      continue
    fi
    if [[ "$line" == "Cmnd_Alias ${system_alias} = "* ]]; then
      ((saw_system == 0)) || return 1
      validate_legacy_system_commands "${line#*= }" || return 1
      saw_system=1
      continue
    fi
    if [[ "$line" == "Cmnd_Alias ${app_alias} = "* ]]; then
      ((saw_apps == 0)) || return 1
      validate_legacy_app_commands "${line#*= }" || return 1
      saw_apps=1
      continue
    fi
    if [[ "$line" == "${manager_user} ALL=NOPASSWD: ${system_alias}, ${app_alias}" ]]; then
      ((saw_grant == 0)) || return 1
      saw_grant=1
      continue
    fi
    return 1
  done < "$path"
  ((saw_system == 1 && saw_apps == 1 && saw_grant == 1))
}

validate_legacy_system_commands() {
  local raw="$1"
  local command=""
  local count=0
  local saw_manager_restart=0
  local saw_broker=0
  local saw_lnd_upgrade=0
  local saw_app_upgrade=0
  IFS=',' read -r -a commands <<< "$raw"
  for command in "${commands[@]}"; do
    command="$(echo "$command" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
    [[ -n "$command" ]] || return 1
    case "$command" in
      */systemctl\ restart\ lnd|*/systemctl\ restart\ --no-block\ lnd|*/systemctl\ restart\ lnd@default|*/systemctl\ restart\ --no-block\ lnd@default|*/systemctl\ restart\ postgresql|*/systemctl\ is-active\ lightningos-lnd-upgrade|*/systemctl\ is-active\ lightningos-app-upgrade|*/systemctl\ reboot|*/systemctl\ poweroff|/usr/local/sbin/lightningos-fix-lnd-perms|*/smartctl\ \*|*/tee\ /etc/lightningos/config.yaml)
        ;;
      */systemctl\ restart\ lightningos-manager)
        saw_manager_restart=1
        ;;
      /usr/local/libexec/lightningos-privileged\ \"\")
        ((saw_broker == 0)) || return 1
        saw_broker=1
        ;;
      /usr/local/sbin/lightningos-upgrade-lnd)
        saw_lnd_upgrade=1
        ;;
      /usr/local/sbin/lightningos-upgrade-app)
        saw_app_upgrade=1
        ;;
      *)
        return 1
        ;;
    esac
    count=$((count + 1))
  done
  ((count >= 10 && count <= 16 && saw_manager_restart == 1 && saw_lnd_upgrade == saw_app_upgrade))
}

validate_legacy_app_commands() {
  local raw="$1"
  local command=""
  local count=0
  IFS=',' read -r -a commands <<< "$raw"
  for command in "${commands[@]}"; do
    command="$(echo "$command" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
    case "$command" in
      /bin/true|*/apt-get\ \*|*/apt\ \*|*/dpkg\ \*|*/docker\ \*|*/docker-compose\ \*|*/systemd-run\ \*|*/ufw\ \*) ;;
      *) return 1 ;;
    esac
    count=$((count + 1))
  done
  ((count >= 1 && count <= 7))
}

validate_legacy_auth_enable_sudoers() {
  local path="$1"
  local manager_user="$2"
  [[ ! -e "$path" ]] && return 0
  validate_root_regular_file "$path" || return 1
  local saw_grant=0 line=""
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" != *$'\r'* ]] || return 1
    [[ -n "$line" ]] || continue
    if [[ "$line" == "Defaults:${manager_user} !requiretty" ]]; then
      continue
    fi
    if [[ "$line" =~ ^${manager_user}[[:space:]]ALL=NOPASSWD:[[:space:]]/(usr/)?bin/tee[[:space:]]/etc/lightningos/config.yaml,[[:space:]]/(usr/)?bin/systemctl[[:space:]]restart[[:space:]]lightningos-manager,[[:space:]]/(usr/)?bin/systemd-run[[:space:]]\*$ ]]; then
      ((saw_grant == 0)) || return 1
      saw_grant=1
      continue
    fi
    return 1
  done < "$path"
  ((saw_grant == 1))
}

capture_optional_file() {
  local source="$1"
  local state_root="$2"
  local name="$3"
  [[ ! -e "$source" ]] && return 0
  [[ -f "$source" && ! -L "$source" ]] || return 1
  "$CP_BIN" -a -- "$source" "$state_root/$name"
  : > "$state_root/${name}.existed"
}

capture_lnd_manager_credential_boundary() {
  local state_root="$1"
  local admin_path="/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"
  local credential_path="/var/lib/lightningos-credentials/lnd/manager.macaroon"
  local credential_state_path="/var/lib/lightningos-credentials/lnd/manager-state.json"
  local metadata=""

  for path in "$admin_path" "$credential_path" "$credential_state_path"; do
    [[ ! -L "$path" ]] || return 1
  done
  if [[ -e "$admin_path" ]]; then
    [[ -f "$admin_path" ]] || return 1
    metadata="$("$STAT_BIN" -c '%u:%g:%a' "$admin_path" 2>/dev/null)" || return 1
    [[ "$metadata" =~ ^[0-9]+:[0-9]+:[0-7]{3,4}$ ]] || return 1
    printf '%s\n' "$metadata" > "$state_root/lnd-admin-macaroon.metadata"
    : > "$state_root/lnd-admin-macaroon.existed"
  fi
  if [[ -e "$credential_path" ]]; then
    [[ -f "$credential_path" ]] || return 1
    capture_optional_file "$credential_path" "$state_root" "lnd-manager-macaroon" || return 1
  fi
  if [[ -e "$credential_state_path" ]]; then
    [[ -f "$credential_state_path" ]] || return 1
    capture_optional_file "$credential_state_path" "$state_root" "lnd-manager-state" || return 1
  fi
}

upgrade_lnd_manager_credential_rollback_state() {
  local state_root="$1"
  local source=""
  local name=""

  for entry in \
    "/var/lib/lightningos-credentials/lnd/manager.macaroon:lnd-manager-macaroon" \
    "/var/lib/lightningos-credentials/lnd/manager-state.json:lnd-manager-state"; do
    source="${entry%%:*}"
    name="${entry#*:}"
    if [[ -f "$state_root/${name}.existed" && ! -L "$state_root/${name}.existed" ]]; then
      if [[ ! -f "$state_root/$name" ]]; then
        [[ -f "$source" && ! -L "$source" ]] || return 1
        "$CP_BIN" -a -- "$source" "$state_root/$name" || return 1
      fi
      [[ -f "$state_root/$name" && ! -L "$state_root/$name" ]] || return 1
    elif [[ -e "$state_root/$name" ]]; then
      return 1
    fi
  done
}

capture_manager_ui_boundary() {
  local state_root="$1"
  local ui_root="/opt/lightningos/ui"
  [[ -d "$ui_root" && ! -L "$ui_root" ]] || return 1
  "$TAR_BIN" -C /opt/lightningos -cpf "$state_root/manager-ui.tar" ui
  [[ -f "$state_root/manager-ui.tar" && ! -L "$state_root/manager-ui.tar" ]]
}

prepare_privilege_cutover() {
  local state_root="/var/lib/lightningos/rollback/0.5.3-privilege-cutover"
  local config_path="/etc/lightningos/config.yaml"
  local service_path="/etc/systemd/system/lightningos-manager.service"
  local dropin_dir="/etc/systemd/system/lightningos-manager.service.d"
  local dropin_path="${dropin_dir}/30-privilege-hardening.conf"
  local rollback_src="$project_dir/internal/server/assets/rollback-privilege-cutover.sh"
  local rollback_bin="/usr/local/sbin/lightningos-rollback-privilege-cutover"
  local manager_user=""
  local sudoers_path=""
  local auth_sudoers_path="/etc/sudoers.d/lightningos-auth-enable"

  manager_user="$($SYSTEMCTL_BIN show -p User --value lightningos-manager 2>/dev/null | tr -d '[:space:]')"
  [[ -n "$manager_user" ]] || manager_user="lightningos"
  [[ "$manager_user" == "lightningos" ]] || die "Automatic privilege cutover supports the canonical lightningos manager user only."
  sudoers_path="/etc/sudoers.d/lightningos"

  [[ -f "$config_path" && ! -L "$config_path" ]] || return 1
  [[ -f "$service_path" && ! -L "$service_path" ]] || return 1
  [[ -f "/opt/lightningos/manager/lightningos-manager" && ! -L "/opt/lightningos/manager/lightningos-manager" ]] || return 1
  [[ -f "$rollback_src" && ! -L "$rollback_src" ]] || return 1
  for path in "$state_root" "$dropin_dir" "$dropin_path" "$rollback_bin"; do
    [[ ! -L "$path" ]] || return 1
  done
  if [[ -e "$state_root" ]]; then
    [[ -d "$state_root" && ! -L "$state_root" ]] || return 1
    [[ "$("$STAT_BIN" -c '%u:%g:%a' "$state_root" 2>/dev/null)" == "0:0:700" ]] || return 1
  fi
  "$INSTALL_BIN" -d -o root -g root -m 0700 "$state_root"
  "$INSTALL_BIN" -d -o root -g root -m 0755 "$dropin_dir"
  if [[ -f "$state_root/prepared" && ! -L "$state_root/prepared" ]]; then
    validate_root_regular_file "$state_root/prepared" || return 1
    if [[ ! -f "$state_root/schema-v2" || -L "$state_root/schema-v2" ]]; then
      die "Legacy privilege rollback state requires operator review before the final cutover."
    fi
    validate_root_regular_file "$state_root/schema-v2" || return 1
    if [[ ! -f "$state_root/schema-v3" ]]; then
      capture_lnd_manager_credential_boundary "$state_root" || return 1
      : > "$state_root/schema-v3"
    fi
    validate_root_regular_file "$state_root/schema-v3" || return 1
    if [[ ! -f "$state_root/schema-v4" ]]; then
      capture_manager_ui_boundary "$state_root" || return 1
      : > "$state_root/schema-v4"
    fi
    validate_root_regular_file "$state_root/schema-v4" || return 1
    validate_root_regular_file "$state_root/manager-ui.tar" || return 1
    if [[ ! -f "$state_root/schema-v5" ]]; then
      upgrade_lnd_manager_credential_rollback_state "$state_root" || return 1
      : > "$state_root/schema-v5"
    fi
    validate_root_regular_file "$state_root/schema-v5" || return 1
    if [[ ! -f "$state_root/schema-v6" ]]; then
      capture_optional_file "/opt/lightningos/manager/.build_stamp" "$state_root" "manager-build-stamp" || return 1
      : > "$state_root/schema-v6"
    fi
    validate_root_regular_file "$state_root/schema-v6" || return 1
    "$INSTALL_BIN" -o root -g root -m 0755 "$rollback_src" "$rollback_bin"
    return 0
  fi

  validate_legacy_manager_sudoers "$sudoers_path" "$manager_user" || die "Unrecognized manager sudoers policy; refusing privilege cutover."
  validate_legacy_auth_enable_sudoers "$auth_sudoers_path" "$manager_user" || die "Unrecognized login-enable sudoers policy; refusing privilege cutover."

  "$CP_BIN" -a -- "$config_path" "$state_root/config.yaml"
  capture_optional_file "$service_path" "$state_root" "lightningos-manager.service"
  capture_optional_file "$dropin_path" "$state_root" "30-privilege-hardening.conf"
  capture_optional_file "$sudoers_path" "$state_root" "sudoers"
  capture_optional_file "$auth_sudoers_path" "$state_root" "auth-enable-sudoers"
  capture_optional_file "/opt/lightningos/manager/lightningos-manager" "$state_root" "lightningos-manager"
  capture_optional_file "/opt/lightningos/manager/.build_stamp" "$state_root" "manager-build-stamp"
  capture_optional_file "$PRIVILEGED_BROKER" "$state_root" "lightningos-privileged"
  capture_optional_file "$PRIVILEGED_TMPFILES_CONFIG" "$state_root" "lightningos-privileged.conf"
  capture_optional_file "/etc/systemd/system/lightningos-privileged.socket" "$state_root" "lightningos-privileged.socket"
  capture_optional_file "/etc/systemd/system/lightningos-privileged@.service" "$state_root" "lightningos-privileged@.service"
  capture_optional_file "$rollback_bin" "$state_root" "rollback-command"
  capture_lnd_manager_credential_boundary "$state_root"
  capture_manager_ui_boundary "$state_root"
  printf '%s\n' "$sudoers_path" > "$state_root/sudoers.path"
  printf '%s\n' "$manager_user" > "$state_root/manager.user"
  "$DATE_BIN" --utc +'%Y-%m-%dT%H:%M:%SZ' > "$state_root/created-at"
  if id -nG "$manager_user" | tr ' ' '\n' | grep -qx docker; then
    : > "$state_root/had-docker-group"
  fi
  if "$SYSTEMCTL_BIN" is-enabled --quiet lightningos-privileged.socket 2>/dev/null; then
    : > "$state_root/socket-enabled"
  fi
  if "$SYSTEMCTL_BIN" is-active --quiet lightningos-privileged.socket 2>/dev/null; then
    : > "$state_root/socket-active"
  fi
  : > "$state_root/schema-v2"
  : > "$state_root/schema-v3"
  : > "$state_root/schema-v4"
  : > "$state_root/schema-v5"
  : > "$state_root/schema-v6"
  : > "$state_root/prepared"
  "$INSTALL_BIN" -o root -g root -m 0755 "$rollback_src" "$rollback_bin"
  print_ok "Root-only privilege rollback bundle prepared"
}

write_manager_build_stamp() {
  local stamp_path="/opt/lightningos/manager/.build_stamp"
  local stamp_tmp=""
  [[ -d /opt/lightningos/manager && ! -L /opt/lightningos/manager ]] || return 1
  if [[ -e "$stamp_path" || -L "$stamp_path" ]]; then
    validate_root_regular_file "$stamp_path" || return 1
  fi
  stamp_tmp="$(mktemp /opt/lightningos/manager/.build_stamp.XXXXXX)" || return 1
  if ! printf '%s %s\n' "$EXPECTED_COMMIT" "$VERSION" >"$stamp_tmp" \
    || ! chown root:root "$stamp_tmp" \
    || ! chmod 0644 "$stamp_tmp" \
    || ! "$MV_BIN" -f -- "$stamp_tmp" "$stamp_path"; then
    "$RM_BIN" -f -- "$stamp_tmp"
    return 1
  fi
}

stage_privilege_cutover() {
  local state_root="/var/lib/lightningos/rollback/0.5.3-privilege-cutover"
  local config_path="/etc/lightningos/config.yaml"
  local dropin_dir="/etc/systemd/system/lightningos-manager.service.d"
  local dropin_path="${dropin_dir}/30-privilege-hardening.conf"
  local manager_user="lightningos"
  local sudoers_path="/etc/sudoers.d/lightningos"
  local auth_sudoers_path="/etc/sudoers.d/lightningos-auth-enable"
  local config_tmp=""
  local dropin_tmp=""
  local raw_groups=""
  local group=""
  local safe_groups=()

  [[ -f "$state_root/prepared" && ! -L "$state_root/prepared" ]] || return 1
  "$SYSTEMCTL_BIN" is-active --quiet lightningos-privileged.socket || return 1
  validate_legacy_manager_sudoers "$sudoers_path" "$manager_user" || return 1
  validate_legacy_auth_enable_sudoers "$auth_sudoers_path" "$manager_user" || return 1

  config_tmp="$(mktemp /etc/lightningos/.config.yaml.privilege-cutover.XXXXXX)"
  if ! "$CP_BIN" -a -- "$config_path" "$config_tmp"; then
    "$RM_BIN" -f -- "$config_tmp"
    return 1
  fi
  if ! awk '/^privileged:[[:space:]]*$/ { count++ } END { exit(count > 1) }' "$config_path"; then
    "$RM_BIN" -f -- "$config_tmp"
    return 1
  fi
  if ! awk '
    BEGIN { in_privileged = 0; saw_privileged = 0; saw_mode = 0 }
    /^privileged:[[:space:]]*$/ { in_privileged = 1; saw_privileged = 1; print; next }
    in_privileged && /^[^[:space:]#]/ {
      if (!saw_mode) print "  mode: \"enforce\""
      in_privileged = 0
    }
    in_privileged && /^[[:space:]]+mode:[[:space:]]*/ { print "  mode: \"enforce\""; saw_mode = 1; next }
    { print }
    END {
      if (in_privileged && !saw_mode) print "  mode: \"enforce\""
      if (!saw_privileged) print "\nprivileged:\n  mode: \"enforce\""
    }
  ' "$config_path" > "$config_tmp"; then
    "$RM_BIN" -f -- "$config_tmp"
    return 1
  fi
  mv -f -- "$config_tmp" "$config_path"

  raw_groups="$($SYSTEMCTL_BIN show -p SupplementaryGroups --value lightningos-manager 2>/dev/null || true)"
  for group in $raw_groups; do
    [[ "$group" != "docker" ]] || continue
    [[ "$group" =~ ^[a-zA-Z0-9_.@-]+$ ]] || return 1
    safe_groups+=("$group")
  done
  dropin_tmp="$(mktemp "${dropin_dir}/.30-privilege-hardening.conf.XXXXXX")"
  {
    printf '%s\n' \
      '[Service]' \
      'SupplementaryGroups='
    if [[ ${#safe_groups[@]} -gt 0 ]]; then
      printf 'SupplementaryGroups=%s\n' "${safe_groups[*]}"
    fi
    printf '%s\n' \
      'NoNewPrivileges=true' \
      'PrivateTmp=true' \
      'PrivateDevices=true' \
      'ProtectSystem=strict' \
      'ProtectHome=true' \
      'ProtectKernelTunables=true' \
      'ProtectKernelModules=true' \
      'ProtectKernelLogs=true' \
      'ProtectControlGroups=true' \
      'ProtectClock=true' \
      'ProtectHostname=true' \
      'LockPersonality=true' \
      'RestrictRealtime=true' \
      'RestrictSUIDSGID=true' \
      'CapabilityBoundingSet=' \
      'AmbientCapabilities=' \
      'RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK' \
      'SystemCallArchitectures=native' \
      'SystemCallFilter=~@clock @cpu-emulation @debug @module @mount @obsolete @raw-io @reboot @swap' \
      'ReadWritePaths=' \
      'ReadWritePaths=/var/lib/lightningos /var/log/lightningos /etc/lightningos /data/lnd'
  } > "$dropin_tmp"
  chmod 0644 "$dropin_tmp"
  chown root:root "$dropin_tmp"
  mv -f -- "$dropin_tmp" "$dropin_path"

  if id -nG "$manager_user" | tr ' ' '\n' | grep -qx docker; then
    gpasswd -d "$manager_user" docker >/dev/null
  fi
  "$RM_BIN" -f -- "$sudoers_path" "$auth_sudoers_path"
  "$SYSTEMCTL_BIN" daemon-reload
  print_ok "Privilege cutover staged with root-only rollback state"
}

if [[ -n "$CUTOVER_ONLY_MODE" ]]; then
  project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
  [[ "$project_dir" == */lightningos-light ]] || die "Cutover-only mode requires a trusted LightningOS checkout."
  [[ -f "$project_dir/internal/server/assets/rollback-privilege-cutover.sh" ]] || die "Cutover rollback asset is missing."
  case "$CUTOVER_ONLY_MODE" in
    prepare)
      prepare_privilege_cutover
      print_ok "Existing-node privilege cutover prepared"
      ;;
    stage)
      if ! stage_privilege_cutover; then
        /usr/local/sbin/lightningos-rollback-privilege-cutover || true
        die "Existing-node privilege cutover could not be staged safely."
      fi
      if ! "$SYSTEMCTL_BIN" restart lightningos-manager; then
        /usr/local/sbin/lightningos-rollback-privilege-cutover || true
        die "Manager restart failed after existing-node privilege cutover."
      fi
      ready=0
      for _ in $(seq 1 30); do
        if "$CURL_BIN" -ksSf https://127.0.0.1:8443/api/health >/dev/null 2>&1; then
          ready=1
          break
        fi
        sleep 1
      done
      if [[ "$ready" != 1 ]] || ! "$RUNUSER_BIN" -u lightningos -- /opt/lightningos/manager/lightningos-manager broker-self-test >/dev/null; then
        /usr/local/sbin/lightningos-rollback-privilege-cutover || true
        die "Existing-node privilege cutover health gate failed and was rolled back."
      fi
      print_ok "Existing-node privilege cutover committed"
      ;;
  esac
  exit 0
fi

stage_peerswap_assets() {
  local src="$project_dir/assets/binaries/peerswap/version_5_0/amd64"
  local dest="/opt/lightningos/manager/assets/binaries/peerswap/version_5_0/amd64"
  local bin=""

  if [[ ! -d "$src" ]]; then
    print_warn "Peerswap assets not found at $src; skipping"
    return 0
  fi

  print_step "Staging Peerswap assets"
  mkdir -p "$dest"
  for bin in peerswapd pscli psweb; do
    if [[ -f "$src/$bin" ]]; then
      "$INSTALL_BIN" -m 0755 "$src/$bin" "$dest/$bin"
    else
      print_warn "Peerswap binary missing: $src/$bin"
    fi
  done
  print_ok "Peerswap assets staged"
}

refresh_terminal_helper() {
  local src="$project_dir/internal/server/assets/lightningos-terminal.sh"
  local service_src="$project_dir/templates/systemd/lightningos-terminal.service"
  local secrets_path="/etc/lightningos/secrets.env"
  local runtime_path="/etc/lightningos/terminal.env"
  local operator_user="losop"
  local credential=""
  local runtime_tmp=""
  if [[ ! -f "$src" ]]; then
    print_warn "Terminal helper not found at $src; skipping"
    return 0
  fi
  [[ -f "$service_src" && ! -L "$service_src" ]] || die "Terminal service template is missing or unsafe"
  [[ -f "$secrets_path" && ! -L "$secrets_path" ]] || die "Terminal secrets source is missing or unsafe"
  [[ ! -L "$runtime_path" ]] || die "Terminal runtime environment is symlinked"
  if ! /usr/bin/id "$operator_user" >/dev/null 2>&1; then
    "$USERADD_BIN" -m -d "/home/${operator_user}" -s /bin/bash "$operator_user"
  fi

  print_step "Hardening the optional web terminal"
  "$SYSTEMCTL_BIN" disable --now lightningos-terminal >/dev/null 2>&1 || true
  "$INSTALL_BIN" -m 0755 "$src" /usr/local/sbin/lightningos-terminal
  "$INSTALL_BIN" -o root -g root -m 0644 "$service_src" /etc/systemd/system/lightningos-terminal.service
  "$RM_BIN" -f -- /usr/local/sbin/lightningos-terminal-password

  credential="$(awk -F= '$1 == "TERMINAL_CREDENTIAL" { print substr($0, length($1) + 2) }' "$secrets_path" | tail -n1)"
  if [[ ! "$credential" =~ ^losop:[A-Za-z0-9]{16,128}$ ]]; then
    credential="losop:$($OPENSSL_BIN rand -hex 16)"
  fi
  for assignment in \
    "TERMINAL_ENABLED=0" \
    "TERMINAL_ALLOW_WRITE=0" \
    "TERMINAL_OPERATOR_USER=losop" \
    "TERMINAL_OPERATOR_PASSWORD=" \
    "TERMINAL_CREDENTIAL=${credential}"; do
    key="${assignment%%=*}"
    value="${assignment#*=}"
    if grep -q "^${key}=" "$secrets_path"; then
      sed -i "s|^${key}=.*|${key}=${value}|" "$secrets_path"
    elif [[ "$key" != "TERMINAL_OPERATOR_PASSWORD" ]]; then
      printf '%s=%s\n' "$key" "$value" >> "$secrets_path"
    fi
  done

  runtime_tmp="$(mktemp /etc/lightningos/.terminal.env.XXXXXX)"
  {
    printf 'TERMINAL_ENABLED=0\n'
    printf 'TERMINAL_CREDENTIAL=%s\n' "$credential"
    printf 'TERMINAL_ALLOW_WRITE=0\n'
    printf 'TERMINAL_PORT=7681\n'
    printf 'TERMINAL_OPERATOR_USER=losop\n'
    printf 'TERMINAL_TERM=xterm\n'
    printf 'TERMINAL_SHELL=/bin/bash\n'
    printf 'TERMINAL_WS_ORIGIN=\n'
  } > "$runtime_tmp"
  "$INSTALL_BIN" -o root -g lightningos -m 0640 "$runtime_tmp" "$runtime_path"
  "$RM_BIN" -f -- "$runtime_tmp"

  for group in lightningos sudo systemd-journal; do
    if /usr/bin/id -nG "$operator_user" | tr ' ' '\n' | grep -Fxq "$group"; then
      "$GPASSWD_BIN" -d "$operator_user" "$group" >/dev/null
    fi
  done
  "$USERMOD_BIN" -L "$operator_user"
  "$SYSTEMCTL_BIN" daemon-reload
  print_ok "Web terminal disabled, read-only by default, isolated from Manager secrets, and removed from privileged groups"
}

configure_lnd_restart_policy() {
  if ! "$SYSTEMCTL_BIN" show lnd.service >/dev/null 2>&1; then
    print_warn "lnd.service not found; skipping restart policy"
    return 0
  fi

  print_step "Configuring LND restart policy"
  local dropin_dir="/etc/systemd/system/lnd.service.d"
  mkdir -p "$dropin_dir"
  printf '%s\n' '[Service]' 'Restart=always' 'RestartSec=60' >"$dropin_dir/20-lightningos-restart.conf"
  "$SYSTEMCTL_BIN" daemon-reload
  print_ok "LND will restart after PostgreSQL or other unexpected interruptions"
}

configure_manager_tls_mdns() {
  local src="$project_dir/internal/server/assets/setup-manager-tls-mdns.sh"
  local dest="/usr/local/sbin/lightningos-setup-manager-tls-mdns"
  local manager_user=""
  local manager_group=""

  if [[ ! -f "$src" ]]; then
    print_warn "Manager TLS/mDNS helper not found at $src; skipping"
    return 0
  fi

  if ! command -v avahi-daemon >/dev/null 2>&1; then
    if [[ -n "${APT_GET_BIN:-}" ]]; then
      print_step "Installing local hostname discovery"
      if DEBIAN_FRONTEND=noninteractive "$APT_GET_BIN" update \
        && DEBIAN_FRONTEND=noninteractive "$APT_GET_BIN" install -y avahi-daemon libnss-mdns; then
        print_ok "mDNS packages installed"
      else
        print_warn "Could not install Avahi; trusted IP access will remain available"
      fi
    else
      print_warn "apt-get is unavailable; .local discovery cannot be installed automatically"
    fi
  fi

  manager_user="$("$SYSTEMCTL_BIN" show -p User --value lightningos-manager 2>/dev/null | tr -d '[:space:]')"
  [[ -n "$manager_user" ]] || manager_user="lightningos"
  manager_group="$("$SYSTEMCTL_BIN" show -p Group --value lightningos-manager 2>/dev/null | tr -d '[:space:]')"
  if [[ -z "$manager_group" ]] && id "$manager_user" >/dev/null 2>&1; then
    manager_group="$(id -gn "$manager_user")"
  fi
  [[ -n "$manager_group" ]] || manager_group="lightningos"

  print_step "Migrating manager TLS and local discovery"
  "$INSTALL_BIN" -m 0755 "$src" "$dest"
  if LIGHTNINGOS_MANAGER_GROUP="$manager_group" LIGHTNINGOS_MANAGER_PORT=8443 "$dest"; then
    print_ok "Manager TLS and local discovery are current"
  else
    print_warn "Manager TLS migration was not applied; the existing certificate was preserved"
  fi
}

configure_manager_firewall() {
  local src="$project_dir/internal/server/assets/lightningos-manager-firewall.sh"
  if [[ ! -f "$src" ]]; then
    print_warn "Manager firewall helper not found at $src; skipping"
    return 0
  fi

  print_step "Restricting manager firewall access"
  "$INSTALL_BIN" -m 0755 "$src" /usr/local/sbin/lightningos-manager-firewall
  if /usr/local/sbin/lightningos-manager-firewall; then
    touch /var/lib/lightningos/system-integrations-20260731-v1
    chmod 0644 /var/lib/lightningos/system-integrations-20260731-v1
  else
    print_warn "Manager firewall configuration could not be updated"
  fi
}

normalize_legacy_manager_identity() {
  local manager_user migrator
  manager_user="$($SYSTEMCTL_BIN show -p User --value lightningos-manager 2>/dev/null | tr -d '[:space:]')"
  [[ -n "$manager_user" ]] || manager_user="lightningos"
  if [[ "$manager_user" == "lightningos" ]]; then
    return 0
  fi

  migrator="$project_dir/scripts/migrate-legacy-manager.sh"
  [[ -f "$migrator" && ! -L "$migrator" ]] || die "Legacy Manager identity requires the authenticated migration helper."
  [[ "$($STAT_BIN -c '%u' "$migrator")" == "0" ]] || die "Legacy Manager migration helper ownership is unsafe."
  print_step "Normalizing legacy Manager identity without restarting Bitcoin or LND"
  /usr/bin/bash "$migrator" --check || die "This legacy Manager layout is not recognized for automatic migration."
  /usr/bin/bash "$migrator" --apply || die "Legacy Manager identity migration failed safely."
  [[ "$($SYSTEMCTL_BIN show -p User --value lightningos-manager 2>/dev/null | tr -d '[:space:]')" == "lightningos" ]] \
    || die "Legacy Manager identity migration did not reach the canonical user."
  legacy_migrator="$migrator"
  legacy_identity_normalized=1
  print_ok "Legacy Manager identity normalized; Bitcoin and LND remained running"
}

finalize_legacy_manager_identity() {
  [[ "$legacy_identity_normalized" -eq 1 ]] || return 0
  /usr/bin/bash "$legacy_migrator" --finalize \
    || die "Legacy Manager privilege cutover could not be finalized safely."
  legacy_identity_normalized=0
  print_ok "Legacy Manager privilege cutover finalized; Bitcoin and LND remained running"
}

mkdir -p "$(dirname "$mirror_root")" "$worktree_root"
if [[ "$TRUSTED_CHECKOUT" -eq 1 ]]; then
  print_step "Preparing root-owned source from trusted checkout"
  source_project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
  [[ "$source_project_dir" == */lightningos-light ]] || die "Trusted-checkout mode must run from the LightningOS source tree."
  source_repo_dir="$("$GIT_BIN" -C "$source_project_dir" rev-parse --show-toplevel 2>/dev/null)" \
    || die "Trusted-checkout source is not a git repository."
  actual_commit="$("$GIT_BIN" -C "$source_repo_dir" rev-parse HEAD)"
  actual_commit="${actual_commit,,}"
  [[ "$actual_commit" == "$EXPECTED_COMMIT" ]] || die "Trusted checkout HEAD does not match --commit."
  "$GIT_BIN" -C "$source_repo_dir" diff --quiet --no-ext-diff -- \
    || die "Trusted checkout has modified tracked files."
  "$GIT_BIN" -C "$source_repo_dir" diff --cached --quiet --no-ext-diff -- \
    || die "Trusted checkout has staged changes."
  worktree_dir="${worktree_root}/app-upgrade-trusted-$($DATE_BIN +%Y%m%d%H%M%S)"
  "$INSTALL_BIN" -d -o root -g root -m 0700 "$worktree_dir"
  "$GIT_BIN" -C "$source_repo_dir" archive "$EXPECTED_COMMIT" | "$TAR_BIN" -x -C "$worktree_dir"
  TAG="trusted-checkout"
else
  print_step "Preparing repository mirror"
  if [[ -d "$mirror_root" && ! -d "$mirror_root/.git" ]]; then
    print_warn "Repository mirror path is not a git repository. Recreating it."
    "$RM_BIN" -rf "$mirror_root"
  fi
  if [[ -d "$mirror_root" ]] && ! owned_by_root "$mirror_root" "$mirror_root/.git"; then
    print_warn "Repository mirror ownership is incompatible with root. Recreating it."
    "$RM_BIN" -rf "$mirror_root"
  fi
  if [[ ! -d "$mirror_root/.git" ]]; then
    "$RM_BIN" -rf "$mirror_root"
    "$GIT_BIN" clone --no-checkout "$REPO_URL" "$mirror_root"
  else
    "$GIT_BIN" -C "$mirror_root" remote set-url origin "$REPO_URL" || true
  fi
  "$GIT_BIN" -C "$mirror_root" fetch --tags --prune origin

  if ! "$GIT_BIN" -C "$mirror_root" rev-parse -q --verify "refs/tags/$TAG^{}" >/dev/null 2>&1; then
    alt_tag=""
    while IFS= read -r candidate; do
      if [[ "${candidate,,}" == "${TAG,,}" ]]; then
        alt_tag="$candidate"
        break
      fi
    done < <("$GIT_BIN" -C "$mirror_root" tag --list)
    if [[ -n "$alt_tag" ]]; then
      TAG="$alt_tag"
    fi
  fi

  if ! "$GIT_BIN" -C "$mirror_root" rev-parse -q --verify "refs/tags/$TAG^{}" >/dev/null 2>&1; then
    die "Tag not found in repository: ${TAG}"
  fi

  actual_commit="$("$GIT_BIN" -C "$mirror_root" rev-parse "refs/tags/${TAG}^{commit}")"
  actual_commit="${actual_commit,,}"
  if [[ "$actual_commit" != "$EXPECTED_COMMIT" ]]; then
    die "Release tag does not resolve to the expected commit."
  fi

  print_step "Creating temporary worktree for ${TAG}"
  safe_tag="$(echo "$TAG" | tr '/\\' '__')"
  worktree_dir="${worktree_root}/app-upgrade-${safe_tag}-$("$DATE_BIN" +%Y%m%d%H%M%S)"
  "$GIT_BIN" -C "$mirror_root" worktree add --detach "$worktree_dir" "$EXPECTED_COMMIT"
fi

if [[ -f "$worktree_dir/go.mod" ]]; then
  project_dir="$worktree_dir"
elif [[ -f "$worktree_dir/lightningos-light/go.mod" ]]; then
  project_dir="$worktree_dir/lightningos-light"
else
  die "Could not find go.mod in worktree."
fi

if [[ ! -d "$project_dir/ui" ]]; then
  die "Could not find UI directory in worktree."
fi

[[ -f "$project_dir/ui/public/version.txt" ]] || die "Release source version file is missing"
source_version="$(tr '[:upper:]_' '[:lower:]-' <"$project_dir/ui/public/version.txt" | tr -d '[:space:]')"
if [[ "${source_version#v}" != "${VERSION,,}" ]]; then
  die "Release source version does not match the requested version."
fi
print_ok "Using authenticated project directory: $project_dir"

normalize_legacy_manager_identity

go_env="GOPATH=/opt/lightningos/go GOCACHE=/opt/lightningos/go-cache GOMODCACHE=/opt/lightningos/go/pkg/mod"
mkdir -p /opt/lightningos/go /opt/lightningos/go-cache /opt/lightningos/go/pkg/mod

print_step "Building manager binary"
mkdir -p "$project_dir/dist"
(cd "$project_dir" && env $go_env GOFLAGS="-mod=mod -buildvcs=false" "$GO_BIN" build -o dist/lightningos-manager ./cmd/lightningos-manager)
print_ok "Manager built"

print_step "Building privileged broker"
(cd "$project_dir" && env $go_env GOFLAGS="-mod=mod -buildvcs=false" "$GO_BIN" build -o dist/lightningos-privileged ./cmd/lightningos-privileged)
print_ok "Privileged broker built"

print_step "Building UI"
[[ -f "$project_dir/ui/package-lock.json" ]] || die "UI lockfile is missing"
(cd "$project_dir/ui" && "$NPM_BIN" ci && "$NPM_BIN" run build)
print_ok "UI built"

print_step "Preparing reversible privilege cutover"
prepare_privilege_cutover
cutover_prepared=1

print_step "Installing UI"
"$RM_BIN" -rf /opt/lightningos/ui/*
"$CP_BIN" -a "$project_dir/ui/dist/." /opt/lightningos/ui/
print_ok "UI installed"

print_step "Ensuring fixed native application identities"
ensure_native_app_identities
print_ok "Native application identities ready"

print_step "Installing manager and socket-activated privileged broker"
for broker_path in /usr/local/libexec /var/log/lightningos-privileged /run/lock/lightningos /run/lightningos-privileged "$PRIVILEGED_BROKER" "$PRIVILEGED_TMPFILES_CONFIG"; do
  [[ ! -L "$broker_path" ]] || die "Refusing symlinked privileged broker path: $broker_path"
done
for template in \
  "$project_dir/templates/lightningos-privileged.tmpfiles.conf" \
  "$project_dir/templates/systemd/lightningos-privileged.socket" \
  "$project_dir/templates/systemd/lightningos-privileged@.service"; do
  [[ -f "$template" && ! -L "$template" ]] || die "Privileged broker template is missing or unsafe: $template"
done
"$INSTALL_BIN" -o root -g root -m 0755 "$project_dir/dist/lightningos-manager" /opt/lightningos/manager/lightningos-manager
"$INSTALL_BIN" -d -o root -g root -m 0755 /usr/local/libexec /etc/tmpfiles.d
"$INSTALL_BIN" -d -o root -g root -m 0750 /var/log/lightningos-privileged /run/lock/lightningos
"$INSTALL_BIN" -o root -g root -m 0644 "$project_dir/templates/lightningos-privileged.tmpfiles.conf" "$PRIVILEGED_TMPFILES_CONFIG"
/usr/bin/systemd-tmpfiles --create "$PRIVILEGED_TMPFILES_CONFIG"
"$INSTALL_BIN" -o root -g root -m 0755 "$project_dir/dist/lightningos-privileged" "$PRIVILEGED_BROKER"
"$INSTALL_BIN" -o root -g root -m 0644 "$project_dir/templates/systemd/lightningos-privileged.socket" /etc/systemd/system/lightningos-privileged.socket
"$INSTALL_BIN" -o root -g root -m 0644 "$project_dir/templates/systemd/lightningos-privileged@.service" /etc/systemd/system/lightningos-privileged@.service
broker_response="$(printf '%s\n' '{"version":1,"request_id":"upgrade_self_test","operation":"self_test","params":{}}' | env -u SUDO_UID -u SUDO_USER -u SUDO_COMMAND "$PRIVILEGED_BROKER")"
if [[ "$broker_response" != *'"request_id":"upgrade_self_test"'* || "$broker_response" != *'"ok":true'* || "$broker_response" != *'"ready":true'* ]]; then
  /usr/local/sbin/lightningos-rollback-privilege-cutover || true
  die "Privileged broker direct-root self-test failed"
fi
"$SYSTEMCTL_BIN" daemon-reload
"$SYSTEMCTL_BIN" enable --now lightningos-privileged.socket
broker_ready=0
for _ in $(seq 1 20); do
  if "$SYSTEMCTL_BIN" is-active --quiet lightningos-privileged.socket \
    && [[ -S /run/lightningos-privileged/broker.sock ]] \
    && "$RUNUSER_BIN" -u lightningos -- /opt/lightningos/manager/lightningos-manager broker-self-test >/dev/null 2>&1; then
    broker_ready=1
    break
  fi
  sleep 1
done
if [[ "$broker_ready" -ne 1 ]]; then
  /usr/local/sbin/lightningos-rollback-privilege-cutover || true
  die "Privileged broker service-user self-test failed"
fi
print_ok "Manager and privileged broker transport installed and self-tested"

stage_peerswap_assets
refresh_terminal_helper
configure_lnd_restart_policy
configure_manager_tls_mdns
configure_manager_firewall

print_step "Staging privilege cutover"
if ! stage_privilege_cutover; then
  print_warn "Privilege cutover staging failed; restoring the previous boundary"
  /usr/local/sbin/lightningos-rollback-privilege-cutover || true
  die "Privilege cutover could not be staged safely"
fi

print_step "Converging the restricted LND manager credential"
if ! lnd_manager_credential_state="$("$RUNUSER_BIN" -u lightningos -- /opt/lightningos/manager/lightningos-manager lnd-manager-credential-ensure)"; then
  print_warn "LND manager credential migration failed; restoring the previous boundary"
  /usr/local/sbin/lightningos-rollback-privilege-cutover || true
  die "Restricted LND manager credential could not be prepared safely"
fi
case "$lnd_manager_credential_state" in
  status=ready\ *|status=pending\ *) ;;
  *)
    print_warn "LND manager credential migration returned an invalid state; restoring the previous boundary"
    /usr/local/sbin/lightningos-rollback-privilege-cutover || true
    die "Restricted LND manager credential returned an invalid state"
    ;;
esac
print_ok "LND manager credential boundary checked without restarting LND"

print_step "Restarting lightningos-manager"
manager_ready=0
if "$SYSTEMCTL_BIN" restart lightningos-manager; then
  for attempt in $(seq 1 20); do
    if "$SYSTEMCTL_BIN" is-active --quiet lightningos-manager \
      && curl -sk --max-time 3 https://127.0.0.1:8443/api/health >/dev/null; then
      manager_ready=1
      break
    fi
    sleep 1
  done
fi
if [[ "$manager_ready" -eq 1 ]]; then
  print_ok "lightningos-manager is active"
else
  print_warn "Manager failed after privilege cutover; restoring the previous boundary"
  /usr/local/sbin/lightningos-rollback-privilege-cutover || true
  die "lightningos-manager failed to start after cutover"
fi

if ! write_manager_build_stamp; then
  print_warn "Manager build provenance could not be recorded; restoring the previous boundary"
  /usr/local/sbin/lightningos-rollback-privilege-cutover || true
  die "lightningos-manager build provenance could not be recorded"
fi

finalize_legacy_manager_identity

cutover_prepared=0
print_ok "App upgrade complete to ${VERSION} (${TAG})"
