#!/usr/bin/env bash
# Learning/development lab only. Never dispatches a production installer.
set -Eeuo pipefail
umask 077

die() { echo "[ERROR] $*" >&2; exit 1; }
usage() {
  echo 'Usage: sudo bash lo_regtest_bootstrap.sh --disposable-host'
  echo 'Dedicated Ubuntu 24.04 amd64 VM or WSL2 distro, systemd, hostname los-regtest.'
  echo 'Default: latest immutable published LOS release. LOS_REGTEST_REF explicitly selects development source.'
}
if [[ ${1:-} == --help && $# == 1 ]]; then usage; exit 0; fi
[[ $# == 1 && $1 == --disposable-host ]] || { usage >&2; exit 2; }
[[ $EUID == 0 ]] || die 'Run with sudo inside the dedicated Linux lab.'
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || die 'Ubuntu 24.04 amd64 is required.'
source /etc/os-release
[[ $ID == ubuntu && $VERSION_ID == 24.04 ]] || die 'Ubuntu 24.04 is required.'
[[ $(cat /proc/1/comm) == systemd && -d /run/systemd/system ]] || die 'systemd must run as PID 1; enable it in WSL2 before installing.'
[[ $(hostname) == los-regtest ]] || die 'Prepare the dedicated lab hostname los-regtest first; see the regtest README.'

# Refuse existing production nodes before installing any bootstrap prerequisite.
for path in /data/lnd /data/bitcoin /var/lib/lnd /var/lib/bitcoin /var/lib/bitcoind \
  /etc/lightningos/config.yaml /opt/lightningos/manager /root/.lnd /root/.bitcoin \
  /home/*/.lnd /home/*/.bitcoin; do
  [[ ! -e "$path" && ! -L "$path" ]] || die 'Existing node data detected; use a dedicated empty lab.'
done
for service in lnd bitcoind bitcoin lightningos-manager lightningos-privileged.socket; do
  state=$(systemctl show "$service" -p LoadState --value)
  [[ -z $state || $state == not-found ]] || die "Existing production service detected: $service"
done
REF=${LOS_REGTEST_REF:-latest}
[[ $REF =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ ]] || die 'Invalid LOS_REGTEST_REF.'
for command in git python3 curl; do
  if ! command -v "$command" >/dev/null; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq ca-certificates curl git python3
    break
  fi
done

release_request() {
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 15 --max-time 45 --max-filesize 1048576 -fsSL \
    -H 'Accept: application/vnd.github+json' \
    -H 'X-GitHub-Api-Version: 2026-03-10' \
    -H 'User-Agent: lightningos-regtest-bootstrap' "$1"
}

release_tag_from_json() {
  python3 -c '
import json, re, sys
try:
    release = json.load(sys.stdin)
    if not isinstance(release, dict):
        raise ValueError("expected a release object")
    if release.get("immutable") is not True or release.get("draft") is not False or release.get("prerelease") is not False:
        raise ValueError("release must be immutable, published and not a prerelease")
    tag = release.get("tag_name", "")
    if not isinstance(tag, str) or not re.fullmatch(r"[vV]?[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9][A-Za-z0-9.-]*)?", tag):
        raise ValueError("invalid release tag")
    print(tag)
except (ValueError, TypeError) as error:
    print("[ERROR] Invalid LightningOS release metadata: " + str(error), file=sys.stderr)
    sys.exit(1)
'
}

EXPECTED_COMMIT=''
if [[ $REF == latest ]]; then
  API=https://api.github.com/repos/jvxis/brln-os-light-updates/releases
  metadata=$(release_request "$API/latest") || die 'Cannot query latest release; no development or legacy fallback.'
  REF=$(printf '%s' "$metadata" | release_tag_from_json) || die 'Invalid latest release.'
  metadata=$(release_request "$API/tags/$REF") || die 'Cannot verify immutable release.'
  verified=$(printf '%s' "$metadata" | release_tag_from_json) || die 'Invalid release attestation.'
  [[ $verified == "$REF" ]] || die 'Release attestation tag mismatch.'
  refs=$(git ls-remote --exit-code https://github.com/jvxis/brln-os-light-updates.git "refs/tags/$REF" "refs/tags/$REF^{}") \
    || die 'Cannot resolve immutable release commit.'
  EXPECTED_COMMIT=$(printf '%s\n' "$refs" | python3 -c '
import re, sys
tag = "refs/tags/" + sys.argv[1]
refs = dict((ref, sha) for sha, ref in (line.split() for line in sys.stdin if line.strip()))
commit = refs.get(tag + "^{}", refs.get(tag, ""))
if not re.fullmatch(r"[0-9a-f]{40}", commit):
    sys.exit("Invalid immutable release commit")
print(commit)
' "$REF") || die 'Invalid immutable release commit.'
else
  echo '[WARN] Explicit LOS_REGTEST_REF: development checkout, not an attested latest-release installation.' >&2
fi

# Always use our own private checkout. Never reset or overwrite a user's repo.
WORK=$(mktemp -d /var/tmp/los-regtest-bootstrap.XXXXXXXX)
cleanup() {
  if [[ $WORK == /var/tmp/los-regtest-bootstrap.* && -d $WORK && ! -L $WORK ]]; then
    rm -rf -- "$WORK"
  fi
}
trap cleanup EXIT
git init -q "$WORK/source"
git -C "$WORK/source" remote add origin https://github.com/jvxis/brln-os-light.git
FETCH_REF=$REF
[[ -z $EXPECTED_COMMIT ]] || FETCH_REF="refs/tags/$REF"
GIT_TERMINAL_PROMPT=0 git -C "$WORK/source" fetch --depth=1 origin "$FETCH_REF"
git -C "$WORK/source" checkout -q --detach FETCH_HEAD
COMMIT=$(git -C "$WORK/source" rev-parse HEAD)
[[ $COMMIT =~ ^[a-f0-9]{40}$ ]] || die 'Invalid source commit.'
[[ -z $(git -C "$WORK/source" status --porcelain) ]] || die 'Source checkout is not clean.'
echo "==> REGTEST source: $COMMIT ($REF); coins have no value."
if [[ -n $EXPECTED_COMMIT ]]; then
  [[ $COMMIT == "$EXPECTED_COMMIT" ]] || die 'Source does not match the immutable release commit; installer was not run.'
  VERSION=$(tr -d '\r\n' < "$WORK/source/lightningos-light/ui/public/version.txt")
  [[ ${VERSION#[vV]} == "${REF#[vV]}" ]] || die 'Source version does not match release tag; installer was not run.'
  echo '==> Immutable release, source commit and version verified. Installing isolated REGTEST only.'
fi
python3 "$WORK/source/scripts/regtest-lab/safety.py" check
LIGHTNINGOS_REGTEST_BOOTSTRAPPED=1 bash "$WORK/source/lightningos-light/install_regtest.sh" --disposable-host
