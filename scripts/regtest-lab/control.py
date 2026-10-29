#!/usr/bin/env python3
"""Operate only the dedicated, local LOS regtest fixture. Never print secrets."""
import argparse
import base64
import json
import socket
import ssl
import subprocess
import time
import os
import urllib.parse
import urllib.error
import urllib.request
from pathlib import Path
from nodes import NODES, CHANNEL_PAIRS, configured_alias

ROOT = Path('/var/lib/los-regtest')
BIN = Path('/opt/los-regtest/bin')
PORTS = {name: (spec['grpc'], spec['rest']) for name, spec in NODES.items()}


def checked_lab():
    if socket.gethostname() != 'los-regtest' or Path('/etc/los-regtest/LAB_ONLY').read_text().strip() != 'LOS_REGTEST_ONLY':
        raise RuntimeError('Dedicated regtest host required')
    info = None
    for _ in range(60):
        try:
            info = btc('getblockchaininfo')
            break
        except RuntimeError:
            time.sleep(1)
    if info is None or info['chain'] != 'regtest':
        raise RuntimeError('Refusing non-regtest Bitcoin backend')


def command(*args):
    result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    if result.returncode:
        # RPC errors may contain request material; deliberately keep it private.
        raise RuntimeError('Local regtest RPC command failed: ' + Path(args[0]).name)
    if not result.stdout.strip():
        return None
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError:
        return result.stdout.strip()


def btc(*args, wallet=False):
    options = [str(BIN / 'bitcoin-cli'), '-datadir=' + str(ROOT / 'bitcoin'), '-regtest']
    if wallet:
        options.append('-rpcwallet=los-regtest-miner')
    return command(*options, *[str(arg) for arg in args])


def ln(node, *args):
    return command(str(BIN / 'lncli'), '--lnddir=' + str(ROOT / node), '--network=regtest',
                   '--rpcserver=127.0.0.1:' + str(PORTS[node][0]), *[str(arg) for arg in args])


def rest(node, endpoint, body=None, authenticated=False):
    ctx = ssl.create_default_context(cafile=str(ROOT / node / 'tls.cert'))
    headers = {'Content-Type': 'application/json'}
    if authenticated:
        headers['Grpc-Metadata-macaroon'] = (ROOT / node / 'data/chain/bitcoin/regtest/admin.macaroon').read_bytes().hex()
    request = urllib.request.Request('https://127.0.0.1:' + str(PORTS[node][1]) + endpoint,
                                    data=None if body is None else json.dumps(body).encode(), headers=headers)
    with urllib.request.urlopen(request, context=ctx, timeout=30) as response:
        return json.load(response)


def wait_info(node):
    for _ in range(60):
        try:
            info = ln(node, 'getinfo')
            if info.get('chains') != [{'chain': 'bitcoin', 'network': 'regtest'}]:
                raise RuntimeError('Refusing non-regtest LND')
            if info.get('synced_to_chain'):
                return info
        except RuntimeError:
            pass
        time.sleep(1)
    raise RuntimeError('LND regtest did not become ready: ' + node)


def initialize():
    wallets = btc('listwallets')
    if 'los-regtest-miner' not in wallets:
        existing = {item['name'] for item in btc('listwalletdir')['wallets']}
        btc('loadwallet' if 'los-regtest-miner' in existing else 'createwallet', 'los-regtest-miner')
    miner_path = Path('/etc/los-regtest/miner-address')
    if not miner_path.exists():
        miner_path.write_text(btc('getnewaddress', wallet=True) + '\n')
        miner_path.chmod(0o600)
    if btc('getblockcount') < 110:
        mine(110 - btc('getblockcount'))
    for node in PORTS:
        macaroon = ROOT / node / 'data/chain/bitcoin/regtest/admin.macaroon'
        wallet_db = ROOT / node / 'data/chain/bitcoin/regtest/wallet.db'
        if not macaroon.exists() and not wallet_db.exists():
            for _ in range(60):
                try:
                    rest(node, '/v1/state')
                    break
                except (OSError, urllib.error.URLError):
                    time.sleep(1)
            else:
                raise RuntimeError('Native wallet initializer did not become ready')
            # Seed words exist only in process memory during wallet creation.
            # Never save, print, return, or include them in an error message.
            seed = rest(node, '/v1/genseed')['cipher_seed_mnemonic']
            password = (ROOT / node / 'wallet-password').read_text().strip().encode()
            rest(node, '/v1/initwallet', {'wallet_password': base64.b64encode(password).decode(),
                                        'cipher_seed_mnemonic': seed, 'recovery_window': 0})
            del seed, password
        apply_alias(node)
        balance = int(ln(node, 'walletbalance')['total_balance'])
        if balance < 100000000:
            address = ln(node, 'newaddress', 'p2wkh')['address']
            btc('sendtoaddress', address, 2, wallet=True)
    mine(6)
    for node in PORTS:
        wait_info(node)
    print('Initialized three native LND wallets with local regtest coins; no seed words recorded.')


def apply_alias(node):
    desired = configured_alias((ROOT / node / 'lnd.conf').read_text())
    info = wait_info(node)
    # Comparing live state also repairs an interrupted run after config write.
    # Repeated successful runs do not restart a correctly configured node.
    if info.get('alias') != desired:
        subprocess.run(['systemctl', 'restart', 'los-regtest-' + node], check=True)
        if wait_info(node).get('alias') != desired:
            raise RuntimeError('Native LND did not apply its configured alias')


def mine(blocks):
    if not 1 <= blocks <= 10000:
        raise RuntimeError('Block count must be between 1 and 10000')
    btc('generatetoaddress', blocks, Path('/etc/los-regtest/miner-address').read_text().strip(), wallet=True)


def ensure_channel(source, target, pubkey):
    if not any(peer['pub_key'] == pubkey for peer in ln(source, 'listpeers').get('peers', [])):
        ln(source, 'connect', pubkey + '@127.0.0.1:' + str(NODES[target]['p2p']))
    existing = any(c['remote_pubkey'] == pubkey for c in ln(source, 'listchannels').get('channels', []))
    pending = any(c.get('channel', {}).get('remote_node_pub') == pubkey
                  for c in ln(source, 'pendingchannels').get('pending_open_channels', []))
    if not existing and not pending:
        ln(source, 'openchannel', '--node_key=' + pubkey, '--local_amt=1000000',
           '--push_amt=500000', '--sat_per_vbyte=2')
        pending = True
    if pending:
        mine(6)
    # An inactive existing channel or an interrupted pending funding is reused;
    # never open a duplicate merely because the link is not ready yet.
    for _ in range(60):
        if any(c['remote_pubkey'] == pubkey and c['active']
               for c in ln(source, 'listchannels').get('channels', [])):
            return
        time.sleep(1)
    raise RuntimeError('Native regtest channel did not become active')


def smoke():
    info = {node: wait_info(node) for node in NODES}
    for source, target in CHANNEL_PAIRS:
        ensure_channel(source, target, info[target]['identity_pubkey'])
    for node in NODES:
        wait_info(node)
    for source, target in CHANNEL_PAIRS:
        pubkey = info[target]['identity_pubkey']
        for _ in range(60):
            try:
                if ln(source, 'queryroutes', '--dest=' + pubkey, '--amt=1000').get('routes'):
                    break
            except RuntimeError:
                pass
            time.sleep(1)
        else:
            raise RuntimeError('Native channel routing did not become ready')
        invoice = ln(target, 'addinvoice', '--amt=1000', '--memo=LOS REGTEST native smoke test')
        payment = rest(source, '/v2/router/send', {'payment_request': invoice['payment_request'],
                                                 'timeout_seconds': 30, 'fee_limit_sat': '1000',
                                                 'no_inflight_updates': True}, authenticated=True)
        if payment.get('result', payment).get('status') != 'SUCCEEDED':
            raise RuntimeError('Native Lightning payment failed; no automatic payment retry')
        print('PASS: native ' + source + ' -> ' + target + ' channel and payment of 1000 regtest sats.')


def status():
    result = {'network': 'regtest', 'height': btc('getblockcount'), 'nodes': {}}
    for node in PORTS:
        info = wait_info(node)
        result['nodes'][node] = {'alias': info['alias'], 'version': info['version'], 'synced': info['synced_to_chain'],
                                  'confirmed_sat': int(ln(node, 'walletbalance')['confirmed_balance']),
                                  'active_channels': info['num_active_channels']}
    print(json.dumps(result, indent=2))


def guard(node):
    wait_info(node)
    config = json.loads(Path('/etc/lightningos/config.yaml').read_text())
    expected = {'grpc_host': '127.0.0.1:' + str(PORTS[node][0]),
                'tls_cert_path': str(ROOT / node / 'tls.cert'),
                'admin_macaroon_path': str(ROOT / node / 'data/chain/bitcoin/regtest/admin.macaroon')}
    dsn = urllib.parse.urlsplit(os.environ.get('NOTIFICATIONS_PG_DSN', ''))
    if config['lnd'] != expected or config['server']['host'] != '127.0.0.1' or config['features']['enable_login'] is not True:
        raise RuntimeError('Unsafe laboratory Manager configuration')
    if config['privileged']['mode'] != 'disabled' or dsn.hostname != '127.0.0.1' or dsn.path != '/los_regtest_' + node:
        raise RuntimeError('Unsafe laboratory database/privilege configuration')
    print('Verified native Bitcoin/LND regtest and isolated Manager configuration: ' + node)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['initialize', 'status', 'smoke', 'mine', 'guard'])
    parser.add_argument('argument', nargs='?', default='1')
    args = parser.parse_args()
    checked_lab()
    if args.action == 'mine':
        mine(int(args.argument))
    elif args.action == 'guard':
        if args.argument not in PORTS:
            raise RuntimeError('Unknown laboratory node')
        guard(args.argument)
    else:
        globals()[args.action]()
