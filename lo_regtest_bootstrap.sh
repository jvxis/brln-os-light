#!/usr/bin/env bash
# Learning/development lab only. Never dispatches a production installer.
set -Eeuo pipefail
umask 077

die() { echo "[ERROR] $*" >&2; exit 1; }
usage() {
  echo 'Usage: sudo bash lo_regtest_bootstrap.sh --disposable-host'
  echo 'Dedicated Ubuntu 24.04 amd64 VM or WSL2 distro, systemd, hostname los-regtest.'
  echo 'LOS_REGTEST_REF selects a development source ref (default agent/0.5.50-release).'
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
REF=${LOS_REGTEST_REF:-agent/0.5.50-release}
[[ $REF =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ ]] || die 'Invalid LOS_REGTEST_REF.'
for command in git python3; do
  if ! command -v "$command" >/dev/null; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq ca-certificates git python3
    break
  fi
done

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
GIT_TERMINAL_PROMPT=0 git -C "$WORK/source" fetch --depth=1 origin "$REF"
git -C "$WORK/source" checkout -q --detach FETCH_HEAD
COMMIT=$(git -C "$WORK/source" rev-parse HEAD)
[[ $COMMIT =~ ^[a-f0-9]{40}$ ]] || die 'Invalid source commit.'
[[ -z $(git -C "$WORK/source" status --porcelain) ]] || die 'Source checkout is not clean.'
echo "==> REGTEST learning/development source: $COMMIT ($REF); coins have no value."
echo '==> This uses development source, not the production release catalog.'
python3 "$WORK/source/scripts/regtest-lab/safety.py" check
bash "$WORK/source/lightningos-light/install_regtest.sh" --disposable-host
