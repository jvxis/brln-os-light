#!/usr/bin/env bash
# Explicit global LTS migration for the immutable 0.5.48/49 updaters.
# Does not install LOS, restart services, remove apps or alter LND credentials.
set -Eeuo pipefail
umask 077
fail() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }
[[ ${1:-} != --help ]] || { echo 'Usage: sudo bash recover-node-for-0.5.49.sh --check | --migrate-to-lts'; exit 0; }
[[ $# == 1 && ( $1 == --migrate-to-lts || $1 == --check ) ]] || fail 'Explicit --migrate-to-lts consent required: global Node/npm will be aligned to the 0.5.49 LTS pin. Review other Node applications first.'
migration_action=$1
[[ $EUID == 0 && $(uname -s) == Linux && -f /etc/debian_version ]] || fail 'Root on Debian/Ubuntu Linux required'
for tool in curl sha256sum dpkg dpkg-deb dpkg-query apt-get apt-mark python3 stat readlink env flock find grep systemctl; do
  command -v "$tool" >/dev/null || fail "Missing prerequisite: $tool"
done
[[ -f /opt/lightningos/manager/lightningos-manager && ! -L /opt/lightningos/manager/lightningos-manager ]] || fail 'Existing LightningOS installation required'
version=$(tr -d '\r\n' < /opt/lightningos/ui/version.txt)
case "$version" in 0.5.4[0-9]-[Bb]eta) ;; *) fail 'Recovery is limited to LOS 0.5.40 through 0.5.49; older nodes must take the mandatory bridge first' ;; esac
systemctl is-active --quiet lightningos-app-upgrade.service && fail 'An app upgrade is running; wait before retrying'
# All mutable paths are root-controlled, never beneath a user checkout.
for directory in /var /var/cache /var/backups; do
  [[ -d $directory && ! -L $directory && $(stat -c %u "$directory") == 0 && -z $(find "$directory" -maxdepth 0 -perm /022 -print) ]] || fail 'Unsafe recovery parent directory'
done
stage=$(mktemp -d /var/cache/lightningos-node-recovery.XXXXXX)
[[ $stage == /var/cache/lightningos-node-recovery.* && -d $stage && ! -L $stage ]] || fail 'Unsafe staging directory'
# Preserve the small root-only recovery record even if APT is interrupted.
echo "Recovery staging: $stage"
curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 20 --max-time 120 \
  https://raw.githubusercontent.com/jvxis/brln-os-light/da04315c48a47228602afaea315d41e6249beb16/lightningos-light/scripts/prepare-node-upgrade.sh \
  --output "$stage/prepare-node-upgrade.sh"
printf '%s  %s\n' 2d6f56e7aaa5b1774287322e685cb52621dd2b01273480a99793be64eba04780 "$stage/prepare-node-upgrade.sh" | sha256sum --check --status || fail 'Published preparer checksum mismatch'
source "$stage/prepare-node-upgrade.sh"
lightningos_node_policy
lightningos_node_safe_directory /var/cache
lock=/var/cache/lightningos-node.lock
if [[ -e $lock || -L $lock ]]; then
  [[ -f $lock && ! -L $lock && $(stat -c %u "$lock") == 0 && -z $(find "$lock" -maxdepth 0 -perm /022 -print) ]] || fail 'Unsafe Node lock'
fi
exec 9>"$lock"
flock -x 9
systemctl is-active --quiet lightningos-app-upgrade.service && fail 'An app upgrade started; wait before retrying'
lightningos_node_check_shadowing
lightningos_node_check_npm_config
installed=$(dpkg-query -W -f='${Version}' nodejs 2>/dev/null || true)
[[ -n $installed ]] || fail 'No packaged Node found; use the normal preparer'
prefix=$(lightningos_node_npm prefix -g)
lightningos_node_validate_prefix "$prefix"
# No app names, configurations or repository credentials are printed.
lightningos_node_npm ls -g --depth=0 --json > "$stage/globals.before.json" || fail 'Global npm inventory is unhealthy; review it before migration'
archive="$stage/nodejs_${LOS_NODE_DEB_VERSION}_${LOS_NODE_ARCH}.deb"
curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 20 --max-time 600 \
  "https://deb.nodesource.com/node_24.x/pool/main/n/nodejs/$(basename "$archive")" --output "$archive"
printf '%s  %s\n' "$LOS_NODE_SHA256" "$archive" | sha256sum --check --status || fail 'Node package checksum mismatch'
[[ $(dpkg-deb -f "$archive" Package) == nodejs && $(dpkg-deb -f "$archive" Version) == "$LOS_NODE_DEB_VERSION" && $(dpkg-deb -f "$archive" Architecture) == "$LOS_NODE_ARCH" ]] || fail 'Unexpected Node package identity'
# Changing only nodejs is intentional; no removals, autoremove or repairs.
LC_ALL=C apt-get -o DPkg::Lock::Timeout=120 -o APT::Get::AutomaticRemove=false --simulate install --only-upgrade --allow-downgrades "$archive" > "$stage/apt-plan"
while read -r apt_action package rest; do
  case "$apt_action" in
    Remv) fail 'APT would remove a package; migration refused' ;;
    Inst|Conf) [[ ${package%%:*} == nodejs ]] || fail 'APT would change another package; migration refused' ;;
  esac
done < "$stage/apt-plan"
# Stage and validate the known NodeSource major-line change before APT.
# Other repository formats, duplicate NodeSource entries and unsafe files fail
# closed. Original content stays private in the recovery record.
python3 - "$stage" <<'PY'
import json, os, pathlib, re, stat, sys
stage = pathlib.Path(sys.argv[1])
directory = pathlib.Path('/etc/apt/sources.list.d')
for p in (pathlib.Path('/etc'), pathlib.Path('/etc/apt'), directory):
    s = p.lstat()
    if not stat.S_ISDIR(s.st_mode) or s.st_uid != 0 or s.st_mode & 0o022:
        raise SystemExit('Unsafe APT source directory')
candidates = []
for p in [pathlib.Path('/etc/apt/sources.list'), *directory.glob('*.list'), *directory.glob('*.sources')]:
    if not p.exists() and not p.is_symlink():
        continue
    s = p.lstat()
    if not stat.S_ISREG(s.st_mode) or s.st_uid != 0 or s.st_nlink != 1 or s.st_mode & 0o022:
        raise SystemExit('Unsafe APT source file; no repository changed')
    text = p.read_text()
    if not any('deb.nodesource.com/' in line and not line.lstrip().startswith('#') for line in text.splitlines()):
        continue
    if not re.search(r'https://deb\.nodesource\.com/node_(?:22|24|25|26)\.x(?=/|\s|$)', text):
        raise SystemExit('Unrecognized NodeSource configuration; review required')
    replacement = re.sub(r'https://deb\.nodesource\.com/node_(?:22|24|25|26)\.x(?=/|\s|$)', 'https://deb.nodesource.com/node_24.x', text)
    candidates.append((p, text, replacement, s))
if len(candidates) != 1:
    raise SystemExit('Exactly one recognized NodeSource source file is required; no repository changed')
p, original, replacement, s = candidates[0]
(stage/'source.before').write_text(original)
(stage/'source.after').write_text(replacement)
(stage/'source.json').write_text(json.dumps(dict(path=str(p), uid=s.st_uid, gid=s.st_gid, mode=stat.S_IMODE(s.st_mode))))
PY
if [[ $migration_action == --check ]]; then
  echo '[OK] Migration preflight passed. No Node packages, npm configuration or repositories changed. Run again with --migrate-to-lts to apply; this is not Ready for the LOS upgrade yet.'
  exit 0
fi
lightningos_node_prepare_npm_prefix "$prefix"
echo '[WARN] Explicitly migrating global Node/npm to the 0.5.49 LTS pin. Native npm addons may require their own rebuild; no apps will be restarted.'
DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l LC_ALL=C apt-get -o DPkg::Lock::Timeout=120 -o APT::Get::AutomaticRemove=false --yes install --only-upgrade --allow-downgrades "$archive" || fail "APT failed. Preserve $stage and inspect dpkg state; LOS was not upgraded."
lightningos_node_check_global || fail 'Post-install Node/npm verification failed; LOS was not upgraded'
[[ $(lightningos_node_npm prefix -g) == "$prefix" ]] || fail 'Global npm prefix changed unexpectedly'
lightningos_node_npm ls -g --depth=0 --json > "$stage/globals.after.json" || fail 'Global npm inventory needs review after migration; LOS was not upgraded'
python3 - "$stage" <<'PY'
import json, os, pathlib, stat, sys, tempfile
stage = pathlib.Path(sys.argv[1])
def packages(name):
    data = json.loads((stage/name).read_text())
    return {k:v.get('version') for k,v in data.get('dependencies', {}).items() if k != 'npm'}
if packages('globals.before.json') != packages('globals.after.json'):
    raise SystemExit('Global npm package inventory changed; review recovery record')
meta = json.loads((stage/'source.json').read_text())
p = pathlib.Path(meta['path'])
s = p.lstat()
if not stat.S_ISREG(s.st_mode) or s.st_uid != 0 or s.st_nlink != 1 or s.st_mode & 0o022 or p.read_bytes() != (stage/'source.before').read_bytes():
    raise SystemExit('APT source changed concurrently; source not overwritten')
fd, temporary = tempfile.mkstemp(prefix='.los-node-', dir=p.parent)
try:
    with os.fdopen(fd, 'wb') as f:
        f.write((stage/'source.after').read_bytes())
        f.flush(); os.fsync(f.fileno())
        os.fchown(f.fileno(), meta['uid'], meta['gid'])
        os.fchmod(f.fileno(), meta['mode'])
    os.replace(temporary, p)
finally:
    if os.path.exists(temporary): os.unlink(temporary)
PY
lightningos_node_runtime_notice
echo '[OK] Ready: global Node 24.22.1 / npm 11.19.1 verified, global package inventory preserved, NodeSource aligned to node_24.x. Now upgrade LOS to 0.5.49 through the dashboard.'
echo "Private recovery record (do not publish its contents): $stage"
