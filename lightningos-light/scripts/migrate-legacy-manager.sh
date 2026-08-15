#!/usr/bin/env bash
set -Eeuo pipefail

# Normalizes pre-hardening existing-node installations whose Manager still
# runs as the original operator account. The 0.5.3 privilege reconciler can
# then perform its authenticated broker cutover without touching LND or
# Bitcoin Core.

SERVICE="lightningos-manager.service"
CANONICAL_USER="lightningos"
CANONICAL_GROUP="lightningos"
STATE_ROOT="/var/lib/lightningos/rollback/0.5.4-legacy-manager-normalization"
DROPIN_DIR="/etc/systemd/system/lightningos-manager.service.d"
DROPIN_PATH="${DROPIN_DIR}/10-legacy-manager-normalization.conf"
SUDOERS_PATH="/etc/sudoers.d/lightningos"
AUTH_SUDOERS_PATH="/etc/sudoers.d/lightningos-auth-enable"
VERSION_PATH="/opt/lightningos/ui/version.txt"
CONFIG_PATH="/etc/lightningos/config.yaml"
SECRETS_PATH="/etc/lightningos/secrets.env"
MODE="check"

log() { printf '%s\n' "$*"; }
die() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: migrate-legacy-manager.sh [--check|--apply|--finalize|--rollback]

  --check     Inspect the node without changing it (default).
  --apply     Normalize a recognized legacy Manager before installing the new build.
  --finalize  Verify the authenticated privilege cutover after installing the new build.
  --rollback  Restore the pre-normalization Manager boundary.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check) MODE="check" ;;
    --apply) MODE="apply" ;;
    --finalize) MODE="finalize" ;;
    --rollback) MODE="rollback" ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; die "unknown argument: $1" ;;
  esac
  shift
done

require_root() {
  [[ "$(id -u)" == "0" ]] || die "this migration must run as root"
}

root_regular_file() {
  local path="$1"
  [[ -f "$path" && ! -L "$path" ]] || return 1
  [[ "$(stat -c '%u:%g' "$path" 2>/dev/null)" == "0:0" ]] || return 1
  local mode
  mode="$(stat -c '%a' "$path" 2>/dev/null)" || return 1
  (( (8#$mode & 8#022) == 0 ))
}

safe_unit_name() {
  [[ "$1" =~ ^[a-zA-Z0-9_.@-]+\.service$ ]]
}

normalize_version() {
  tr '[:upper:]' '[:lower:]' <<<"${1#v}" | tr '_' '-'
}

service_user() {
  systemctl show -p User --value "$SERVICE" 2>/dev/null | tr -d '[:space:]'
}

service_group() {
  local group
  group="$(systemctl show -p Group --value "$SERVICE" 2>/dev/null | tr -d '[:space:]')"
  if [[ -z "$group" ]]; then
    group="$(id -gn "$(service_user)")"
  fi
  printf '%s\n' "$group"
}

detect_unit() {
  local candidate
  for candidate in "$@"; do
    if systemctl show "$candidate" -p LoadState --value 2>/dev/null | grep -qx loaded; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

unit_identity_group() {
  local unit="$1" user group
  user="$(systemctl show "$unit" -p User --value 2>/dev/null | tr -d '[:space:]')"
  [[ -n "$user" ]] || return 1
  group="$(systemctl show "$unit" -p Group --value 2>/dev/null | tr -d '[:space:]')"
  [[ -n "$group" ]] || group="$(id -gn "$user")"
  getent group "$group" >/dev/null 2>&1 || return 1
  printf '%s\n' "$group"
}

validate_command_list() {
  local raw="$1" command count=0 saw_systemd_run=0 saw_manager_restart=0 saw_app_upgrade=0
  IFS=',' read -r -a commands <<<"$raw"
  for command in "${commands[@]}"; do
    command="$(sed 's/^[[:space:]]*//;s/[[:space:]]*$//' <<<"$command")"
    [[ -n "$command" ]] || return 1
    case "$command" in
      */systemctl\ restart\ lnd|*/systemctl\ restart\ --no-block\ lnd|*/systemctl\ restart\ lnd@default|*/systemctl\ restart\ --no-block\ lnd@default|*/systemctl\ restart\ postgresql|*/systemctl\ is-active\ lightningos-lnd-upgrade|*/systemctl\ is-active\ lightningos-app-upgrade|*/systemctl\ reboot|*/systemctl\ poweroff|/usr/local/sbin/lightningos-fix-lnd-perms|/usr/local/sbin/lightningos-upgrade-lnd|*/smartctl\ \*|*/tee\ /etc/lightningos/config.yaml|*/apt-get\ \*|*/apt\ \*|*/dpkg\ \*|*/docker\ \*|*/docker-compose\ \*|*/ufw\ \*|/bin/true)
        ;;
      */systemctl\ restart\ lightningos-manager)
        saw_manager_restart=1
        ;;
      /usr/local/sbin/lightningos-upgrade-app)
        saw_app_upgrade=1
        ;;
      */systemd-run\ \*)
        saw_systemd_run=1
        ;;
      *) return 1 ;;
    esac
    count=$((count + 1))
  done
  ((count >= 10 && count <= 24 && saw_systemd_run == 1 && saw_manager_restart == 1 && saw_app_upgrade == 1))
}

validate_legacy_sudoers() {
  local path="$1" user="$2" line command_list="" saw_grant=0
  root_regular_file "$path" || return 1
  [[ "$(stat -c '%a' "$path")" == "440" ]] || return 1
  visudo -cf "$path" >/dev/null 2>&1 || return 1

  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" != *$'\r'* ]] || return 1
    line="$(sed 's/^[[:space:]]*//;s/[[:space:]]*$//' <<<"$line")"
    [[ -n "$line" || "$line" == \#* ]] && : || continue
    [[ "$line" == \#* ]] && continue
    [[ "$line" == "Defaults:${user} !requiretty" ]] && continue
    [[ "$line" == Cmnd_Alias\ * ]] && continue
    if [[ "$line" == "${user} ALL=NOPASSWD: "* ]]; then
      command_list="${line#*NOPASSWD: }"
    elif [[ "$line" == "${user} ALL=(root) NOPASSWD: "* ]]; then
      command_list="${line#*NOPASSWD: }"
    elif [[ "$line" == "${user} ALL=(ALL : ALL) NOPASSWD: "* ]]; then
      command_list="${line#*NOPASSWD: }"
    else
      return 1
    fi
    [[ "$command_list" != *LIGHTNINGOS_* ]] || {
      local system_alias app_alias system_raw app_raw
      system_alias="$(awk '/^Cmnd_Alias LIGHTNINGOS_SYSTEM_/ {print $2}' "$path" | head -n1)"
      app_alias="$(awk '/^Cmnd_Alias LIGHTNINGOS_APPS_/ {print $2}' "$path" | head -n1)"
      [[ "$system_alias" =~ ^LIGHTNINGOS_SYSTEM_[A-Z0-9_]+$ ]] || return 1
      [[ "$app_alias" =~ ^LIGHTNINGOS_APPS_[A-Z0-9_]+$ ]] || return 1
      system_raw="$(awk -v alias="$system_alias" '$1 == "Cmnd_Alias" && $2 == alias && $3 == "=" {$1=$2=$3=""; sub(/^ +/, ""); print}' "$path")"
      app_raw="$(awk -v alias="$app_alias" '$1 == "Cmnd_Alias" && $2 == alias && $3 == "=" {$1=$2=$3=""; sub(/^ +/, ""); print}' "$path")"
      validate_command_list "${system_raw}, ${app_raw}" || return 1
      saw_grant=1
      continue
    }
    validate_command_list "$command_list" || return 1
    saw_grant=1
  done < "$path"
  ((saw_grant == 1))
}

validate_manager_unit() {
  local fragment exec_start env_file
  fragment="$(systemctl show "$SERVICE" -p FragmentPath --value 2>/dev/null)"
  [[ "$fragment" == "/etc/systemd/system/lightningos-manager.service" ]] || return 1
  root_regular_file "$fragment" || return 1
  exec_start="$(systemctl show "$SERVICE" -p ExecStart --value 2>/dev/null)"
  [[ "$exec_start" == *"/opt/lightningos/manager/lightningos-manager --config /etc/lightningos/config.yaml"* ]] || return 1
  env_file="$(systemctl cat "$SERVICE" 2>/dev/null | awk -F= '$1 == "EnvironmentFile" {print $2}' | tail -n1)"
  [[ "$env_file" == "$SECRETS_PATH" ]]
}

supported_layout() {
  local user version uid
  user="$(service_user)"
  [[ -n "$user" && "$user" != root && "$user" != "$CANONICAL_USER" ]] || return 1
  [[ "$user" =~ ^[a-z_][a-z0-9_-]*$ ]] || return 1
  uid="$(id -u "$user" 2>/dev/null)" || return 1
  ((uid >= 1000)) || return 1
  version="$(normalize_version "$(cat "$VERSION_PATH" 2>/dev/null)")"
  [[ "$version" == 0.5.2-beta* || "$version" == 0.5.3-beta* ]] || return 1
  validate_manager_unit || return 1
  validate_legacy_sudoers "$SUDOERS_PATH" "$user" || return 1
  [[ -f "$CONFIG_PATH" && ! -L "$CONFIG_PATH" ]] || return 1
  [[ -f "$SECRETS_PATH" && ! -L "$SECRETS_PATH" ]] || return 1
}

protected_snapshot() {
  local output="$1" lnd_unit bitcoin_unit unit
  lnd_unit="$(detect_unit lnd.service lnd@default.service || true)"
  bitcoin_unit="$(detect_unit bitcoind.service bitcoin.service bitcoind@default.service bitcoin@default.service || true)"
  : > "$output"
  for unit in "$lnd_unit" "$bitcoin_unit"; do
    [[ -n "$unit" ]] || continue
    safe_unit_name "$unit" || die "unsafe protected service name"
    printf '%s|%s|%s|%s\n' \
      "$unit" \
      "$(systemctl show "$unit" -p MainPID --value)" \
      "$(systemctl show "$unit" -p NRestarts --value)" \
      "$(systemctl is-active "$unit")" >> "$output"
  done
}

verify_protected_snapshot() {
  local snapshot="$1" unit pid restarts active
  while IFS='|' read -r unit pid restarts active; do
    [[ -n "$unit" ]] || continue
    [[ "$(systemctl show "$unit" -p MainPID --value)" == "$pid" ]] || return 1
    [[ "$(systemctl show "$unit" -p NRestarts --value)" == "$restarts" ]] || return 1
    [[ "$(systemctl is-active "$unit")" == "$active" && "$active" == active ]] || return 1
  done < "$snapshot"
}

capture_optional() {
  local source="$1" name="$2"
  [[ ! -e "$source" && ! -L "$source" ]] && return 0
  [[ -f "$source" && ! -L "$source" ]] || return 1
  cp -a -- "$source" "$STATE_ROOT/$name"
  : > "$STATE_ROOT/${name}.existed"
}

restore_optional() {
  local destination="$1" name="$2"
  if [[ -f "$STATE_ROOT/${name}.existed" ]]; then
    install -o root -g root -m "$(stat -c '%a' "$STATE_ROOT/$name")" "$STATE_ROOT/$name" "$destination"
  else
    rm -f -- "$destination"
  fi
}

capture_path_metadata() {
  local path="$1"
  [[ -e "$path" && ! -L "$path" ]] || return 0
  [[ "$path" != *$'\n'* && "$path" != *'|'* ]] || die "unsafe path in rollback metadata"
  printf '%s|%s|%s|%s\n' "$path" "$(stat -c %u "$path")" "$(stat -c %g "$path")" "$(stat -c %a "$path")" >> "$STATE_ROOT/path-metadata"
}

restore_path_metadata() {
  local path uid gid mode
  [[ -f "$STATE_ROOT/path-metadata" ]] || return 0
  while IFS='|' read -r path uid gid mode; do
    [[ "$path" == /etc/lightningos* || "$path" == /var/lib/lightningos* || "$path" == /var/log/lightningos* ]] || return 1
    [[ -e "$path" && ! -L "$path" ]] || continue
    chown "$uid:$gid" "$path"
    chmod "$mode" "$path"
  done < "$STATE_ROOT/path-metadata"
}

prepare_state() {
  local legacy_user="$1" legacy_group="$2" path
  if [[ -e "$STATE_ROOT" ]]; then
    [[ -d "$STATE_ROOT" && ! -L "$STATE_ROOT" ]] || die "unsafe rollback state"
    [[ "$(stat -c '%u:%g:%a' "$STATE_ROOT")" == "0:0:700" ]] || die "invalid rollback state ownership"
    [[ -f "$STATE_ROOT/prepared" ]] || die "incomplete rollback state requires operator review"
    return 0
  fi
  install -d -o root -g root -m 0700 "$STATE_ROOT"
  printf '%s\n' "$legacy_user" > "$STATE_ROOT/legacy-user"
  printf '%s\n' "$legacy_group" > "$STATE_ROOT/legacy-group"
  id -nG "$CANONICAL_USER" > "$STATE_ROOT/canonical-groups.before" 2>/dev/null || : > "$STATE_ROOT/canonical-user.absent"
  getent group "$CANONICAL_GROUP" >/dev/null 2>&1 || : > "$STATE_ROOT/canonical-group.absent"
  capture_optional "$DROPIN_PATH" manager-dropin
  capture_optional "$SUDOERS_PATH" sudoers
  capture_optional "$AUTH_SUDOERS_PATH" auth-sudoers
  : > "$STATE_ROOT/path-metadata"
  for path in /etc/lightningos /etc/lightningos/tls "$CONFIG_PATH" "$SECRETS_PATH" /var/lib/lightningos /var/lib/lightningos/apps /var/lib/lightningos/apps-data /var/log/lightningos; do
    capture_path_metadata "$path"
  done
  if [[ -d /etc/lightningos/tls && ! -L /etc/lightningos/tls ]]; then
    while IFS= read -r -d '' path; do
      [[ -f "$path" && ! -L "$path" && "$(stat -c %u "$path")" == 0 ]] \
        || die "unsafe TLS asset requires operator review: $path"
      capture_path_metadata "$path"
    done < <(find /etc/lightningos/tls -xdev -mindepth 1 -maxdepth 1 -print0)
  fi
  while IFS= read -r -d '' path; do
    case "$path" in
      /var/lib/lightningos/apps|/var/lib/lightningos/apps-data|/var/lib/lightningos/rollback) continue ;;
    esac
    [[ ! -L "$path" ]] || die "unsafe Manager state symlink requires operator review: $path"
    capture_path_metadata "$path"
  done < <(find /var/lib/lightningos -xdev -mindepth 1 -maxdepth 1 -print0)
  if [[ -d /var/lib/lightningos/chat && ! -L /var/lib/lightningos/chat ]]; then
    while IFS= read -r -d '' path; do
      [[ ! -L "$path" ]] || die "unsafe chat state symlink requires operator review: $path"
      capture_path_metadata "$path"
    done < <(find /var/lib/lightningos/chat -xdev -mindepth 1 -print0)
  fi
  protected_snapshot "$STATE_ROOT/protected-services.before"
  : > "$STATE_ROOT/prepared"
}

write_transition_sudoers() {
  local tmp
  tmp="$(mktemp /etc/sudoers.d/.lightningos-transition.XXXXXX)"
  cat > "$tmp" <<'EOF'
Defaults:lightningos !requiretty
Cmnd_Alias LIGHTNINGOS_SYSTEM_LIGHTNINGOS = /usr/bin/systemctl restart lnd, /usr/bin/systemctl restart --no-block lnd, /usr/bin/systemctl restart lightningos-manager, /usr/bin/systemctl restart postgresql, /usr/bin/systemctl is-active lightningos-lnd-upgrade, /usr/bin/systemctl is-active lightningos-app-upgrade, /usr/bin/systemctl reboot, /usr/bin/systemctl poweroff, /usr/local/sbin/lightningos-fix-lnd-perms, /usr/local/sbin/lightningos-upgrade-lnd, /usr/local/sbin/lightningos-upgrade-app, /usr/sbin/smartctl *, /usr/bin/tee /etc/lightningos/config.yaml
Cmnd_Alias LIGHTNINGOS_APPS_LIGHTNINGOS = /usr/bin/apt-get *, /usr/bin/apt *, /usr/bin/dpkg *, /usr/bin/docker *, /usr/bin/docker-compose *, /usr/bin/systemd-run *, /usr/sbin/ufw *
lightningos ALL=NOPASSWD: LIGHTNINGOS_SYSTEM_LIGHTNINGOS, LIGHTNINGOS_APPS_LIGHTNINGOS
EOF
  chown root:root "$tmp"
  chmod 0440 "$tmp"
  visudo -cf "$tmp" >/dev/null || { rm -f -- "$tmp"; return 1; }
  mv -f -- "$tmp" "$SUDOERS_PATH"
}

canonicalize_paths() {
  local path legacy_user legacy_group uid gid
  legacy_user="$(cat "$STATE_ROOT/legacy-user")"
  legacy_group="$(cat "$STATE_ROOT/legacy-group")"
  chown root:"$CANONICAL_GROUP" /etc/lightningos "$CONFIG_PATH" "$SECRETS_PATH"
  chmod 0750 /etc/lightningos
  if [[ -e /etc/lightningos/tls ]]; then
    [[ -d /etc/lightningos/tls && ! -L /etc/lightningos/tls ]] || die "unsafe TLS directory"
    chown root:"$CANONICAL_GROUP" /etc/lightningos/tls
    chmod 0750 /etc/lightningos/tls
    while IFS= read -r -d '' path; do
      [[ -f "$path" && ! -L "$path" && "$(stat -c %u "$path")" == 0 ]] \
        || die "unsafe TLS asset requires operator review: $path"
      chown root:"$CANONICAL_GROUP" "$path"
    done < <(find /etc/lightningos/tls -xdev -mindepth 1 -maxdepth 1 -print0)
  fi
  chmod 0640 "$CONFIG_PATH"
  chmod 0660 "$SECRETS_PATH"
  [[ -d /var/lib/lightningos && ! -L /var/lib/lightningos ]] || die "unsafe LightningOS state directory"
  chown "$CANONICAL_USER:$CANONICAL_GROUP" /var/lib/lightningos
  for path in /var/lib/lightningos/apps /var/lib/lightningos/apps-data; do
    [[ ! -e "$path" ]] && continue
    [[ -d "$path" && ! -L "$path" ]] || die "unsafe LightningOS application state directory"
    chown "$CANONICAL_USER:$CANONICAL_GROUP" "$path"
  done

  while IFS= read -r -d '' path; do
    case "$path" in
      /var/lib/lightningos/apps|/var/lib/lightningos/apps-data|/var/lib/lightningos/rollback) continue ;;
    esac
    [[ ! -L "$path" ]] || die "unsafe Manager state symlink requires operator review: $path"
    uid="$(stat -c %U "$path")"
    gid="$(stat -c %G "$path")"
    if [[ "$uid:$gid" == "$legacy_user:$legacy_group" ]]; then
      chown "$CANONICAL_USER:$CANONICAL_GROUP" "$path"
    elif [[ "$uid:$gid" != root:root && "$uid:$gid" != "$CANONICAL_USER:$CANONICAL_GROUP" ]]; then
      die "mixed ownership in Manager state requires operator review: $path"
    fi
  done < <(find /var/lib/lightningos -xdev -mindepth 1 -maxdepth 1 -print0)
  if [[ -d /var/lib/lightningos/chat && ! -L /var/lib/lightningos/chat ]]; then
    while IFS= read -r -d '' path; do
      [[ ! -L "$path" ]] || die "unsafe chat state symlink requires operator review: $path"
      uid="$(stat -c %U "$path")"
      gid="$(stat -c %G "$path")"
      if [[ "$uid:$gid" == "$legacy_user:$legacy_group" ]]; then
        chown "$CANONICAL_USER:$CANONICAL_GROUP" "$path"
      elif [[ "$uid:$gid" != root:root && "$uid:$gid" != "$CANONICAL_USER:$CANONICAL_GROUP" ]]; then
        die "mixed ownership in chat state requires operator review: $path"
      fi
    done < <(find /var/lib/lightningos/chat -xdev -mindepth 1 -print0)
  fi

  [[ -d /var/log/lightningos && ! -L /var/log/lightningos ]] || die "unsafe LightningOS log directory"
  if find /var/log/lightningos -xdev \( ! -user "$legacy_user" -o ! -group "$legacy_group" \) -print -quit | grep -q .; then
    die "mixed ownership under /var/log/lightningos requires operator review"
  fi
  chown -R "$CANONICAL_USER:$CANONICAL_GROUP" /var/log/lightningos
  chmod 0750 /var/log/lightningos
}

ensure_canonical_identity() {
  local group
  getent group "$CANONICAL_GROUP" >/dev/null 2>&1 || groupadd --system "$CANONICAL_GROUP"
  if id "$CANONICAL_USER" >/dev/null 2>&1; then
    [[ "$(id -gn "$CANONICAL_USER")" == "$CANONICAL_GROUP" ]] || die "existing lightningos user has an incompatible primary group"
  else
    useradd --system --gid "$CANONICAL_GROUP" --home-dir /var/lib/lightningos --no-create-home --shell /usr/sbin/nologin "$CANONICAL_USER"
  fi

  local lnd_unit bitcoin_unit groups=(systemd-journal)
  lnd_unit="$(detect_unit lnd.service lnd@default.service || true)"
  bitcoin_unit="$(detect_unit bitcoind.service bitcoin.service bitcoind@default.service bitcoin@default.service || true)"
  for group in "$(unit_identity_group "$lnd_unit" 2>/dev/null || true)" "$(unit_identity_group "$bitcoin_unit" 2>/dev/null || true)"; do
    [[ -n "$group" ]] || continue
    [[ "$group" != docker && "$group" != sudo && "$group" != root && "$group" != shadow ]] || die "unsafe service group detected: $group"
    [[ "$group" =~ ^[a-zA-Z0-9_.@-]+$ ]] || die "unsafe service group name"
    groups+=("$group")
  done
  local unique=() candidate
  for group in "${groups[@]}"; do
    getent group "$group" >/dev/null 2>&1 || continue
    for candidate in "${unique[@]}"; do [[ "$candidate" == "$group" ]] && continue 2; done
    unique+=("$group")
  done
  usermod -a -G "$(IFS=,; echo "${unique[*]}")" "$CANONICAL_USER"
  printf '%s\n' "${unique[*]}" > "$STATE_ROOT/canonical-supplementary-groups"
}

write_manager_dropin() {
  local groups tmp
  groups="$(cat "$STATE_ROOT/canonical-supplementary-groups")"
  install -d -o root -g root -m 0755 "$DROPIN_DIR"
  tmp="$(mktemp "$DROPIN_DIR/.10-legacy-manager-normalization.XXXXXX")"
  {
    printf '%s\n' '[Service]' "User=$CANONICAL_USER" "Group=$CANONICAL_GROUP" 'SupplementaryGroups='
    [[ -n "$groups" ]] && printf 'SupplementaryGroups=%s\n' "$groups"
  } > "$tmp"
  chown root:root "$tmp"
  chmod 0644 "$tmp"
  mv -f -- "$tmp" "$DROPIN_PATH"
}

wait_manager() {
  local attempt
  for attempt in {1..30}; do
    if [[ "$(systemctl is-active "$SERVICE" 2>/dev/null || true)" == active ]] \
      && curl -ksS --max-time 3 https://127.0.0.1:8443/api/auth/state >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

wait_cutover() {
  local attempt
  for attempt in {1..180}; do
    if systemctl is-active --quiet lightningos-privileged.socket 2>/dev/null \
      && [[ "$(systemctl show "$SERVICE" -p User --value)" == "$CANONICAL_USER" ]] \
      && [[ "$(systemctl show "$SERVICE" -p NoNewPrivileges --value)" == yes ]] \
      && [[ "$(systemctl show "$SERVICE" -p SupplementaryGroups --value)" != *docker* ]] \
      && [[ ! -e "$SUDOERS_PATH" ]]; then
      return 0
    fi
    sleep 5
  done
  return 1
}

apply_migration() {
  require_root
  supported_layout || die "this node is not a recognized legacy Manager layout"
  local legacy_user legacy_group
  legacy_user="$(service_user)"
  legacy_group="$(service_group)"
  prepare_state "$legacy_user" "$legacy_group"
  ensure_canonical_identity
  canonicalize_paths
  write_transition_sudoers || die "could not create the validated transition sudoers"
  # The legacy auth helper grant is bound to the original operator account.
  # It is optional during cutover and the broker replaces it, so remove it
  # after capturing rollback state instead of widening it to another user.
  rm -f -- "$AUTH_SUDOERS_PATH"
  write_manager_dropin
  systemctl daemon-reload
  systemctl restart "$SERVICE"
  wait_manager || { rollback_migration; die "Manager did not become healthy under the canonical identity"; }
  [[ "$(service_user)" == "$CANONICAL_USER" ]] || { rollback_migration; die "Manager identity did not change"; }
  verify_protected_snapshot "$STATE_ROOT/protected-services.before" || die "Bitcoin or LND changed during Manager normalization"
  : > "$STATE_ROOT/normalized"
  log "[OK] Legacy Manager normalized; authenticated privilege cutover may proceed"
}

finalize_migration() {
  require_root
  [[ -d "$STATE_ROOT" && ! -L "$STATE_ROOT" && -f "$STATE_ROOT/prepared" && -f "$STATE_ROOT/normalized" ]] \
    || die "no normalized legacy Manager migration is ready to finalize"
  [[ ! -f "$STATE_ROOT/completed" ]] || { log "[OK] Legacy Manager privilege hardening was already finalized"; return 0; }
  [[ "$(service_user)" == "$CANONICAL_USER" ]] || die "Manager identity is not canonical"
  if ! wait_cutover; then
    die "privilege cutover did not complete within 15 minutes; inspect the transition logs before rollback"
  fi
  verify_protected_snapshot "$STATE_ROOT/protected-services.before" || die "Bitcoin or LND changed during privilege cutover"
  : > "$STATE_ROOT/completed"
  log "[OK] Manager privilege hardening completed without restarting Bitcoin or LND"
}

rollback_migration() {
  require_root
  [[ -d "$STATE_ROOT" && ! -L "$STATE_ROOT" && -f "$STATE_ROOT/prepared" ]] || die "no valid normalization rollback state found"
  [[ ! -f "$STATE_ROOT/completed" ]] || die "normalization is already part of an accepted privilege cutover; use the privilege-cutover rollback command"
  local legacy_user legacy_group
  legacy_user="$(cat "$STATE_ROOT/legacy-user")"
  legacy_group="$(cat "$STATE_ROOT/legacy-group")"
  [[ "$legacy_user" =~ ^[a-z_][a-z0-9_-]*$ ]] || die "unsafe legacy user in rollback state"
  [[ "$legacy_group" =~ ^[a-z_][a-z0-9_-]*$ ]] || die "unsafe legacy group in rollback state"
  restore_optional "$DROPIN_PATH" manager-dropin
  restore_optional "$SUDOERS_PATH" sudoers
  restore_optional "$AUTH_SUDOERS_PATH" auth-sudoers
  if [[ -d /var/log/lightningos && ! -L /var/log/lightningos ]]; then
    chown -R "$legacy_user:$legacy_group" /var/log/lightningos
  fi
  restore_path_metadata
  systemctl daemon-reload
  systemctl restart "$SERVICE"
  wait_manager || die "Manager did not recover after rollback"
  verify_protected_snapshot "$STATE_ROOT/protected-services.before" || die "Bitcoin or LND changed during rollback"
  log "[OK] Legacy Manager normalization rolled back"
}

check_migration() {
  require_root
  local user version broker="inactive" status="unsupported"
  user="$(service_user)"
  version="$(cat "$VERSION_PATH" 2>/dev/null || true)"
  systemctl is-active --quiet lightningos-privileged.socket 2>/dev/null && broker="active"
  if [[ "$user" == "$CANONICAL_USER" && "$broker" == active ]]; then
    status="hardened"
  elif supported_layout; then
    status="legacy_migratable"
  elif [[ "$user" == "$CANONICAL_USER" ]]; then
    status="canonical_pending_or_incomplete"
  fi
  printf 'status=%s\nversion=%s\nmanager_user=%s\nbroker=%s\n' "$status" "$version" "$user" "$broker"
  [[ "$status" != unsupported ]]
}

case "$MODE" in
  check) check_migration ;;
  apply) apply_migration ;;
  finalize) finalize_migration ;;
  rollback) rollback_migration ;;
esac
