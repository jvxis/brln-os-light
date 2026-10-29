#!/usr/bin/env python3
"""Explicit, opt-in native regtest checks; never runs during an installation.

Requires an already configured Alice/Bob lab. Signs a public test message and
temporarily edits metadata/groups and leases one unlocked output per node.
Restores those edits in finally blocks; creates no payments or channels.
"""
import importlib.util
from pathlib import Path
import time
from urllib.parse import urlencode

from control import checked_lab, ln, wait_info
from nodes import NODES, CHANNEL_PAIRS


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def eventually(read, predicate, message):
    for _ in range(30):
        value = read()
        if predicate(value):
            return value
        time.sleep(1)
    raise RuntimeError(message)


def verify():
    checked_lab()
    findings = []
    spec = importlib.util.spec_from_file_location('verify_los', Path(__file__).with_name('verify-los.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    for node, peer in CHANNEL_PAIRS:
        port = NODES[node]['ui']
        info = wait_info(node)
        api = module.LabAPI(node, port)
        api.call('/api/onchain/utxos', expected=401)
        api.call('/api/audit/events', expected=401)
        api.login()
        balance = ln(node, 'walletbalance')
        channels = ln(node, 'listchannels')['channels']
        leases = lambda: ln(node, 'wallet', 'listleases')
        original_leases = leases()

        message = 'LightningOS regtest operation verification: ' + node
        signature = api.call('/api/lnops/sign-message', {'message': message})['signature']
        result = ln(peer, 'verifymessage', '--msg=' + message, '--sig=' + signature)
        require(result.get('valid') and result.get('pubkey') == info['identity_pubkey'], 'Native signature verification failed')
        invalid = ln(peer, 'verifymessage', '--msg=' + message + ' changed', '--sig=' + signature)
        require(not invalid.get('valid') or invalid.get('pubkey') != info['identity_pubkey'], 'Modified message retained signer identity')
        api.call('/api/lnops/sign-message', {'message': ''}, expected=400)
        print('PASS: ' + node + ' LOS signature verified by peer native LND; modified and empty messages rejected.')

        items = lambda: api.call('/api/onchain/utxos')['items']
        candidate = next((u for u in items() if not u['locked'] and u['confirmations'] >= 6), None)
        require(candidate is not None, 'A mature unlocked output is required')
        outpoint = candidate['outpoint']
        group_id = None
        restore = {key: candidate.get(key, '') for key in ['label', 'tag', 'color', 'group_id']}
        restore['outpoint'] = outpoint
        try:
            group_id = api.call('/api/onchain/utxos/groups', {'name': 'Temporary regtest verification', 'color': '#123456'})['id']
            api.call('/api/onchain/utxos/metadata', {'outpoint': outpoint, 'label': 'Regtest verification', 'tag': 'lab-check', 'color': '#123456'})
            api.call('/api/onchain/utxos/groups/' + group_id + '/assign', {'outpoints': [outpoint]})
            current = next(u for u in items() if u['outpoint'] == outpoint)
            require(all(current.get(k) == v for k, v in {'label': 'Regtest verification', 'tag': 'lab-check', 'color': '#123456', 'group_id': group_id}.items()), 'Metadata/group readback mismatch')
            require(any(g['id'] == group_id for g in api.call('/api/onchain/utxos/groups')['groups']), 'Group not listed')
            api.call('/api/onchain/utxos/groups/' + group_id + '/assign', {'outpoints': [outpoint], 'detach': True})
            require(not next(u for u in items() if u['outpoint'] == outpoint).get('group_id'), 'Group detach failed')
        finally:
            api.call('/api/onchain/utxos/metadata', restore)
            if group_id:
                request = module.urllib.request.Request(api.base + '/api/onchain/utxos/groups/' + group_id,
                    method='DELETE', headers={'Origin': api.base, 'X-CSRF-Token': api.csrf})
                with api.client.open(request, timeout=30) as response:
                    require(response.status == 200, 'Temporary group cleanup failed')
        current = next(u for u in items() if u['outpoint'] == outpoint)
        require(all(current.get(k, '') == v for k, v in restore.items() if k != 'outpoint'), 'Original metadata not restored')
        print('PASS: ' + node + ' UTXO label/tag/color, group create/assign/detach/delete and restoration.')

        lock_path = '/api/onchain/utxos/lock'
        unlock_path = '/api/onchain/utxos/unlock'
        try:
            locked = api.call(lock_path, {'outpoints': [outpoint], 'expiry_sec': 120})
            require(len(locked['results']) == 1 and not locked['results'][0].get('error'), 'Output lease failed')
            require(len(leases()) == len(original_leases) + 1, 'Native lease absent')
            try:
                eventually(items, lambda rows: any(u['outpoint'] == outpoint and u['locked'] for u in rows), 'LOS lock state absent')
            except RuntimeError:
                findings.append(node + ': locked UTXO missing from inventory (#349)')
        finally:
            # This output was verified unlocked before the test; never release
            # a lease belonging to an existing user operation.
            released = api.call(unlock_path, {'outpoints': [outpoint]})
            require(not any(r.get('error') for r in released['results']), 'Output release failed')
        require(leases() == original_leases, 'Native leases not restored')
        eventually(items, lambda rows: any(u['outpoint'] == outpoint and not u['locked'] for u in rows), 'LOS unlock state absent')
        for action in ['utxo.lock', 'utxo.unlock']:
            events = eventually(lambda: api.call('/api/audit/events?' + urlencode({'action': action, 'target': outpoint, 'limit': 10}))['items'],
                lambda rows: any(e['action'] == action and e['target'] == outpoint for e in rows), 'Audit event absent')
            require(all(e['action'] == action and e['target'] == outpoint for e in events), 'Audit filter mismatch')
        api.call('/api/audit/events?limit=-1', expected=400)
        require(len(api.call('/api/audit/events?limit=1')['items']) <= 1, 'Audit limit ignored')
        require(ln(node, 'walletbalance') == balance, 'Wallet balance changed')
        after = ln(node, 'listchannels')['channels']
        require({c['channel_point'] for c in after} == {c['channel_point'] for c in channels} and all(c['active'] for c in after), 'Channels changed')
        require(ln(node, 'pendingchannels')['total_limbo_balance'] == '0', 'Unexpected limbo balance')
        print('PASS: ' + node + ' native lease/release, filtered Audit records, auth/invalid-limit rejection; funds/channels preserved.')
    if findings:
        for finding in findings:
            print('FAIL: ' + finding)
        raise RuntimeError('Locked-output inventory regression remains unresolved (#349)')


if __name__ == '__main__':
    try:
        verify()
    except Exception as error:
        # Requests, cookies, passwords and native RPC details stay private.
        raise SystemExit('Regtest operations verification failed (' + type(error).__name__ + '); inspect the guarded scenario without printing secrets.') from None

