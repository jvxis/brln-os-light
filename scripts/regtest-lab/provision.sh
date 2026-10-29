#!/usr/bin/env bash
# Dedicated disposable Ubuntu amd64 lab only. Does not use the LOS installers.
set -euo pipefail
umask 077
[[ $EUID == 0 && $(hostname) == los-regtest ]]
[[ $(cat /etc/los-regtest/LAB_ONLY) == LOS_REGTEST_ONLY ]]
[[ $(uname -m) == x86_64 && ! -d /data/lnd ]]
python3 "$(dirname -- "${BASH_SOURCE[0]}")/safety.py" check
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
mapfile -t LND_NODES < <(python3 "$SCRIPT_DIR/nodes.py" names)
[[ ${#LND_NODES[@]} == 3 ]]
for service in lnd bitcoind lightningos-manager; do
  if systemctl is-active --quiet "$service"; then echo "Refusing existing service: $service" >&2; exit 1; fi
done
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl python3 openssl postgresql >/dev/null
id los-regtest >/dev/null 2>&1 || useradd --system --home-dir /var/lib/los-regtest --create-home --shell /usr/sbin/nologin los-regtest
install -d -m 0755 /opt/los-regtest/bin /opt/los-regtest/downloads
install -d -m 0700 -o los-regtest -g los-regtest /var/lib/los-regtest
fetch() {
  local name=$1 url=$2 digest=$3
  cd /opt/los-regtest/downloads
  if ! printf '%s  %s\n' "$digest" "$name" | sha256sum -c --status 2>/dev/null; then
    curl --silent --show-error --fail --location --proto '=https' --tlsv1.2 --retry 3 "$url" -o "$name.partial"
    printf '%s  %s\n' "$digest" "$name.partial" | sha256sum -c --status
    mv -- "$name.partial" "$name"
  fi
}
# Same pinned releases as the reviewed LOS source; compare before executing.
fetch bitcoin-31.1-x86_64-linux-gnu.tar.gz https://bitcoincore.org/bin/bitcoin-core-31.1/bitcoin-31.1-x86_64-linux-gnu.tar.gz b80d9c3e04da78fb6f0569685673418cf686fadba9042d926d13fb87ff503f9e
tar -xzf bitcoin-31.1-x86_64-linux-gnu.tar.gz -C /opt/los-regtest/downloads
install -m 0755 bitcoin-31.1/bin/bitcoind bitcoin-31.1/bin/bitcoin-cli /opt/los-regtest/bin/
fetch lnd-linux-amd64-v0.21.5-beta.tar.gz https://github.com/lightningnetwork/lnd/releases/download/v0.21.5-beta/lnd-linux-amd64-v0.21.5-beta.tar.gz 2127d4794ea9cbd98e762ba16283615158743646d2d6d2a5bf88664dd6255ce7
tar -xzf lnd-linux-amd64-v0.21.5-beta.tar.gz -C /opt/los-regtest/downloads
install -m 0755 lnd-linux-amd64-v0.21.5-beta/lnd lnd-linux-amd64-v0.21.5-beta/lncli /opt/los-regtest/bin/
cd /
python3 - "$SCRIPT_DIR" <<'PY'
from pathlib import Path
import secrets,os,pwd,sys
sys.path.insert(0,sys.argv[1])
from nodes import NODES,migrate_alias
root=Path('/var/lib/los-regtest');etc=Path('/etc/los-regtest')
credential=etc/'bitcoin-rpc-password'
if not credential.exists():credential.write_text(secrets.token_urlsafe(32)+'\n');credential.chmod(0o600)
password=credential.read_text().strip()
bitcoin=root/'bitcoin';bitcoin.mkdir(mode=0o700,exist_ok=True)
if not (bitcoin/'bitcoin.conf').exists():(bitcoin/'bitcoin.conf').write_text(f'regtest=1\nserver=1\nlisten=0\ndiscover=0\ndnsseed=0\ntxindex=1\nfallbackfee=0.00002\ndbcache=256\n[regtest]\nconnect=0\nrpcbind=127.0.0.1\nrpcallowip=127.0.0.1\nrpcport=18443\nrpcuser=los-regtest\nrpcpassword={password}\nzmqpubrawblock=tcp://127.0.0.1:28332\nzmqpubrawtx=tcp://127.0.0.1:28333\n')
for name,spec in NODES.items():
 grpc,rest,p2p=spec['grpc'],spec['rest'],spec['p2p']
 folder=root/name;folder.mkdir(mode=0o700,exist_ok=True)
 wallet=folder/'wallet-password'
 if not wallet.exists():wallet.write_text(secrets.token_urlsafe(32)+'\n');wallet.chmod(0o600)
 if not (folder/'lnd.conf').exists():(folder/'lnd.conf').write_text(f'[Application Options]\nalias=LOS-REGTEST-{name.upper()}\nlisten=127.0.0.1:{p2p}\nrpclisten=127.0.0.1:{grpc}\nrestlisten=127.0.0.1:{rest}\ntlsextraip=127.0.0.1\nwallet-unlock-password-file={wallet}\nwallet-unlock-allow-create=true\naccept-keysend=true\n[Bitcoin]\nbitcoin.regtest=true\nbitcoin.node=bitcoind\n[Bitcoind]\nbitcoind.rpchost=127.0.0.1:18443\nbitcoind.rpcuser=los-regtest\nbitcoind.rpcpass={password}\nbitcoind.zmqpubrawblock=tcp://127.0.0.1:28332\nbitcoind.zmqpubrawtx=tcp://127.0.0.1:28333\n[Protocol]\nprotocol.wumbo-channels=true\n')
 config=folder/'lnd.conf';previous=config.read_text();updated=migrate_alias(previous,name)
 if updated!=previous:
  pending=config.with_name('lnd.conf.new');pending.write_text(updated);pending.chmod(0o600);pending.replace(config)
user=pwd.getpwnam('los-regtest')
for folder,dirs,files in os.walk(root):
 os.chown(folder,user.pw_uid,user.pw_gid,follow_symlinks=False)
 for name in files:
  file=Path(folder)/name
  if file.is_symlink():raise RuntimeError('Refusing symlink in native laboratory data')
  os.chown(file,user.pw_uid,user.pw_gid,follow_symlinks=False)
  if name.endswith('.conf'):file.chmod(0o600)
PY
for node in bitcoin "${LND_NODES[@]}"; do
  if [[ $node == bitcoin ]]; then
    command='/opt/los-regtest/bin/bitcoind -datadir=/var/lib/los-regtest/bitcoin -printtoconsole=0'
    dependencies=''
  else
    command="/opt/los-regtest/bin/lnd --lnddir=/var/lib/los-regtest/$node"
    dependencies='Requires=los-regtest-bitcoin.service
After=los-regtest-bitcoin.service'
  fi
  cat >"/etc/systemd/system/los-regtest-$node.service" <<EOF
[Unit]
Description=LOS REGTEST ONLY - $node
$dependencies
ConditionPathExists=/etc/los-regtest/LAB_ONLY
[Service]
User=los-regtest
Group=los-regtest
ExecStart=$command
Restart=on-failure
RestartSec=3
TimeoutStopSec=60
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/los-regtest
IPAddressDeny=any
IPAddressAllow=localhost
[Install]
WantedBy=multi-user.target
EOF
done
systemctl daemon-reload
systemctl enable --now los-regtest-bitcoin "${LND_NODES[@]/#/los-regtest-}" >/dev/null

# enable --now leaves an already active process running its previous executable.
# Compare the live executable too, so retrying an interrupted upgrade converges.
restart_changed_lnd() {
  local service=$1 binary=$2 pid
  pid=$(systemctl show "$service" --property=MainPID --value)
  if [[ $pid =~ ^[1-9][0-9]*$ && -e /proc/$pid/exe ]] && ! cmp -s -- "/proc/$pid/exe" "$binary"; then
    systemctl restart "$service"
  fi
}
for node in "${LND_NODES[@]}"; do
  restart_changed_lnd "los-regtest-$node" /opt/los-regtest/bin/lnd
done
echo 'Native Bitcoin/LND regtest units installed. Wallet initialization/funding is a separate step.'
