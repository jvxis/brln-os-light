#!/usr/bin/env python3
"""Configure lab login and verify real LOS-to-LND calls, without printing secrets."""
import argparse
import http.cookiejar
import json
import re
import secrets
import ssl
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path

from control import checked_lab, ln, mine, wait_info


class LabAPI:
    def __init__(self, node, port):
        self.node = node
        self.base = 'https://127.0.0.1:' + str(port)
        context = ssl.create_default_context(cafile='/etc/los-regtest/ca.crt')
        self.client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
                                                 urllib.request.HTTPSHandler(context=context))
        self.csrf = ''

    def call(self, endpoint, payload=None, expected=200, *, headers=None):
        request_headers = {'Origin': self.base, 'Content-Type': 'application/json'}
        if self.csrf:
            request_headers['X-CSRF-Token'] = self.csrf
        request_headers.update(headers or {})
        request = urllib.request.Request(self.base + endpoint, data=None if payload is None else json.dumps(payload).encode(), headers=request_headers)
        try:
            response = self.client.open(request, timeout=90)
        except urllib.error.HTTPError as error:
            if error.code != expected:
                raise RuntimeError(f'LOS {self.node} {endpoint}: HTTP {error.code}; expected {expected}') from None
            response = error
        with response:
            if response.status != expected:
                raise RuntimeError('Unexpected LOS response status')
            return json.load(response)

    def financial(self, endpoint, payload):
        intent = self.call('/api/financial/intents', {'path': endpoint, 'method': 'POST', 'payload': payload}, expected=201)
        if not intent.get('required') or intent.get('path') != endpoint or intent.get('method') != 'POST' or not re.fullmatch('[a-f0-9]{32}', intent.get('intent_id', '')):
            raise RuntimeError('Invalid laboratory financial snapshot')
        self.call('/api/auth/reauth', {'scope': intent['scope'], 'password': (Path('/etc/los-regtest') / self.node / 'ui-password').read_text()})
        headers = {'X-LOS-Financial-Intent': intent['intent_id']}
        result = self.call(endpoint, intent['payload'], headers=headers)
        if self.call(endpoint, intent['payload'], headers=headers) != result:
            raise RuntimeError('Identical intent retry did not return its original result')
        return result

    def login(self):
        folder = Path('/etc/los-regtest') / self.node
        password_file = folder / 'ui-password'
        state = self.call('/api/auth/state')
        if state['setup_required']:
            if not password_file.exists():
                password_file.write_text(secrets.token_urlsafe(32))
                password_file.chmod(0o600)
            pid = subprocess.check_output(['systemctl', 'show', 'los-regtest-ui-' + self.node, '-p', 'MainPID', '--value'], text=True).strip()
            output = subprocess.check_output(['nsenter', '--target', pid, '--mount', '--', 'runuser', '-u', 'los-regtest', '--',
                                              '/opt/los-regtest/bin/lightningos-manager', 'auth', 'setup-token', 'new'], text=True)
            token = re.search(r'^setup_token=(.+)$', output, re.M).group(1)
            password = password_file.read_text()
            state = self.call('/api/auth/setup', {'setup_token': token, 'password': password, 'confirm_password': password})
            del output, token, password
        else:
            state = self.call('/api/auth/login', {'password': password_file.read_text()})
        self.csrf = state.get('csrf_token', '')
        if not self.csrf:
            self.csrf = self.call('/api/auth/state')['csrf_token']
        if not self.call('/api/auth/state')['authenticated']:
            raise RuntimeError('Lab authentication failed')


def verify(setup_only=False):
    checked_lab()
    clients = {}
    for node, port in [('alice', 8443), ('bob', 8543)]:
        api = LabAPI(node, port)
        for _ in range(60):
            try:
                api.call('/api/auth/state')
                break
            except urllib.error.URLError:
                time.sleep(1)
        else:
            raise RuntimeError('LOS listener did not become ready: ' + node)
        with api.client.open(api.base + '/', timeout=20) as response:
            html = response.read().decode()
            if 'id="los-regtest-notice"' not in html or 'MOEDAS SEM VALOR' not in html:
                raise RuntimeError('Regtest UI notice absent')
        api.call('/api/wallet/summary', expected=401)
        api.login()
        summary = api.call('/api/wallet/summary')
        native = int(ln(node, 'walletbalance')['total_balance'])
        if summary['balances']['onchain_sat'] != native:
            raise RuntimeError('LOS wallet balance does not match native LND')
        api.call('/api/lnops/channels')
        clients[node] = api
        print('PASS: ' + node + ' verified HTTPS, REGTEST notice, login, native wallet balance and channels.')
    if setup_only:
        return
    invoice = clients['bob'].call('/api/wallet/invoice', {'amount_sat': 1000, 'memo': 'LOS REGTEST API smoke'})
    request = {'payment_request': invoice['payment_request'], 'max_fee_sat': 1000}
    clients['alice'].call('/api/wallet/pay', request, expected=428)
    clients['alice'].financial('/api/wallet/pay', request)
    print('PASS: LOS invoice and native Lightning payment of 1000 regtest sats; reauthentication enforced.')
    address = clients['bob'].call('/api/wallet/address', {})['address']
    before = int(ln('bob', 'walletbalance')['confirmed_balance'])
    request = {'address': address, 'amount_sat': 10000, 'sat_per_vbyte': 2}
    clients['alice'].call('/api/wallet/send', request, expected=428)
    sent = clients['alice'].financial('/api/wallet/send', request)
    if not sent.get('txid'):
        raise RuntimeError('LOS did not return an on-chain transaction ID')
    mine(6)
    for node in ['alice', 'bob']:
        wait_info(node)
    for _ in range(60):
        if int(ln('bob', 'walletbalance')['confirmed_balance']) == before + 10000:
            break
        time.sleep(1)
    else:
        raise RuntimeError('Native on-chain transfer did not confirm in the destination wallet')
    print('PASS: LOS on-chain send of 10000 regtest sats confirmed in the other native wallet; reauthentication enforced.')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--setup-only', action='store_true', help='initialize login and verify reads without submitting payments')
    options = parser.parse_args()
    try:
        verify(options.setup_only)
    except Exception:
        raise SystemExit('Laboratory HTTPS/authentication/financial verification failed; no automatic financial retry was submitted.')
