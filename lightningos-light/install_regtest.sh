#!/usr/bin/env bash
# Developer-only native regtest installation from this exact source checkout.
set -Eeuo pipefail
umask 077

usage() {
  cat <<'EOF'
Usage: sudo ./lightningos-light/install_regtest.sh --disposable-host

Install a developer lab on a dedicated Ubuntu 24.04 amd64 VM named los-regtest.
Builds this committed checkout, native Bitcoin and three LND/LOS instances.
Requires Internet access. Repeated runs preserve wallets and login credentials.
Each successful run performs small transfers of worthless regtest coins.
Production nodes, App Store lifecycle and the production updater are unsupported.
EOF
}
if [[ ${1:-} == --help && $# == 1 ]]; then usage; exit 0; fi
[[ $# == 1 && $1 == --disposable-host ]] || { usage >&2; exit 2; }
[[ $EUID == 0 ]] || { echo 'Run with sudo on the disposable VM.' >&2; exit 1; }
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'Ubuntu 24.04 amd64 is required.' >&2; exit 1; }
source /etc/os-release
[[ $ID == ubuntu && $VERSION_ID == 24.04 ]] || { echo 'Ubuntu 24.04 is required.' >&2; exit 1; }
[[ $(hostname) == los-regtest ]] || { echo 'Use a dedicated VM named los-regtest (sudo hostnamectl set-hostname los-regtest).' >&2; exit 1; }
for command in python3 git systemctl flock; do command -v "$command" >/dev/null || { echo "Missing prerequisite: $command" >&2; exit 1; }; done
PROJECT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
REPO=$(cd -- "$PROJECT/.." && pwd -P)
LAB="$REPO/scripts/regtest-lab"
[[ -f "$LAB/safety.py" && -f "$LAB/deploy.py" ]] || { echo 'Use the complete source checkout.' >&2; exit 1; }
LOCK_DIR=/run/los-regtest-installer
if [[ -e "$LOCK_DIR" || -L "$LOCK_DIR" ]]; then
  [[ -d "$LOCK_DIR" && ! -L "$LOCK_DIR" && $(stat -c '%u:%a' "$LOCK_DIR") == 0:700 ]] || { echo 'Unsafe installation lock directory.' >&2; exit 1; }
else
  mkdir -m 0700 -- "$LOCK_DIR"
fi
[[ ! -L "$LOCK_DIR/install.lock" ]] || exit 1
exec 9>"$LOCK_DIR/install.lock"
flock -n 9 || { echo 'Another regtest installation is running.' >&2; exit 1; }
python3 "$LAB/safety.py" check
git_repo() { git -c safe.directory="$REPO" -C "$REPO" "$@"; }
[[ -z $(git_repo status --porcelain --untracked-files=normal) ]] || { echo 'Commit your development changes before installing. The source checkout must be clean.' >&2; exit 1; }
COMMIT=$(git_repo rev-parse HEAD)
[[ $COMMIT =~ ^[a-f0-9]{40}$ ]] || exit 1

# Packages and toolchains are prepared before stopping any existing lab UI.
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl git python3 openssl postgresql build-essential >/dev/null
python3 "$LAB/safety.py" initialize
source "$PROJECT/scripts/install-artifact-verification.sh"
source "$PROJECT/scripts/prepare-go-toolchain.sh"
source "$PROJECT/scripts/prepare-node-upgrade.sh"
lightningos_go_read_policy "$PROJECT/scripts/go-toolchain.conf"
lightningos_go_check_module "$PROJECT/go.mod" "$LOS_GO_VERSION"
install -d -m 0755 /opt/los-regtest/toolchains
lightningos_go_install /opt/los-regtest/toolchains "$LOS_GO_VERSION" amd64 "$LOS_GO_AMD64_SHA256"
GO_ROOT="/opt/los-regtest/toolchains/go${LOS_GO_VERSION}.linux-amd64"
lightningos_prepare_node
if ! id los-regtest-build >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/los-regtest-build --create-home --shell /usr/sbin/nologin los-regtest-build
fi
[[ $(getent passwd los-regtest-build | cut -d: -f6) == /var/lib/los-regtest-build && $(id -u los-regtest-build) != 0 ]] || exit 1
BUILD=$(mktemp -d /var/tmp/los-regtest-build.XXXXXX)
cleanup() { [[ -z ${BUILD:-} ]] || rm -rf -- "$BUILD"; }
trap cleanup EXIT
git_repo archive HEAD | tar --no-same-owner -xf - -C "$BUILD"
chown -R los-regtest-build:los-regtest-build "$BUILD"
chmod 0700 "$BUILD"
runuser -u los-regtest-build -- env -i HOME=/var/lib/los-regtest-build \
  PATH="$GO_ROOT/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
  GOROOT="$GO_ROOT" GOTOOLCHAIN=local GOENV=off GOWORK=off CGO_ENABLED=0 \
  GOMAXPROCS=2 GOMEMLIMIT=1024MiB bash -c '
    set -euo pipefail
    umask 022
    cd "$1/lightningos-light"
    mkdir -p "$1/artifacts"
    go build -trimpath -buildvcs=false -o "$1/artifacts/lightningos-manager" ./cmd/lightningos-manager
    cd ui
    npm ci --no-fund
    npm run build
    tar -czf "$1/artifacts/ui.tar.gz" -C dist .
  ' build "$BUILD"

# Native helpers remain separate from all production installers and services.
for file in nodes.py control.py configure-los.py verify-los.py safety.py deploy.py; do
  install -o root -g root -m 0755 "$LAB/$file" "/opt/los-regtest/$file"
done
bash "$LAB/provision.sh"
python3 /opt/los-regtest/control.py initialize
python3 /opt/los-regtest/control.py smoke
python3 /opt/los-regtest/deploy.py \
  --manager "$BUILD/artifacts/lightningos-manager" \
  --manager-sha256 "$(sha256sum "$BUILD/artifacts/lightningos-manager" | cut -d' ' -f1)" \
  --ui-archive "$BUILD/artifacts/ui.tar.gz" \
  --ui-sha256 "$(sha256sum "$BUILD/artifacts/ui.tar.gz" | cut -d' ' -f1)" \
  --source-commit "$COMMIT"
python3 /opt/los-regtest/verify-los.py --require-alias
cat <<'EOF'

REGTEST developer lab ready. Coins have no value.
Forward ports over verified SSH (replace user@lab-host):
  ssh -N -L 127.0.0.1:19043:127.0.0.1:8443 -L 127.0.0.1:19053:127.0.0.1:8543 -L 127.0.0.1:19063:127.0.0.1:8643 user@lab-host
LOS-Regtest-Alice:   https://alice.localhost:19043
LOS-Regtest-Bob:     https://bob.localhost:19053
LOS-Regtest-Charlie: https://charlie.localhost:19063
Use these distinct browser hostnames to keep all three login cookies separate.
Public certificate authority: /etc/los-regtest/ca.crt
Private UI passwords: /etc/los-regtest/{alice,bob,charlie}/ui-password
Mine confirmations: sudo python3 /opt/los-regtest/control.py mine 6
EOF
