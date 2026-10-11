"""Opt-in real Tor lifecycle regression in a disposable Linux environment.

LOS_ONION_DISPOSABLE=1 python3 scripts/test_lnd_onion_tor.py
Uses a private Tor process/control port; no LND, wallets, or system Tor changes.
"""
import contextlib
import importlib.util
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('onion', Path(__file__).resolve().parents[1]
                                             / 'internal/privileged/assets/lnd-onion.py')
onion = importlib.util.module_from_spec(spec)
spec.loader.exec_module(onion)


def main():
    assert os.environ.get('LOS_ONION_DISPOSABLE') == '1', 'disposable opt-in required'
    with tempfile.TemporaryDirectory(prefix='los-onion-test-') as temporary:
        root = Path(temporary)
        cookie = root / 'control.cookie'
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        address = '127.0.0.1:%d' % port
        process = None

        def start():
            nonlocal process
            process = subprocess.Popen(['/usr/bin/tor', '--DataDirectory', str(root / 'tor'),
                '--ControlPort', address, '--SocksPort', '0', '--CookieAuthentication', '1',
                '--CookieAuthFile', str(cookie), '--DisableNetwork', '1'],
                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                try:
                    result = RealControl(address)
                    result.authenticate()
                    return result
                except (OSError, onion.RecoveryError):
                    time.sleep(0.1)
            raise AssertionError('private Tor control not ready')

        class RealControl(onion.Control):
            def authenticate(self):
                # The test cookie lives in its private temporary directory.
                with patch.object(onion, 'read_control_cookie', return_value=cookie.read_bytes()):
                    super().authenticate()

        try:
            owner = start()
            code, lines = owner.command('ADD_ONION NEW:ED25519-V3 Port=9735,127.0.0.1:19735')
            assert code == 250
            values = dict(line.split('=', 1) for line in lines if '=' in line)
            service = values['ServiceID']
            key = values['PrivateKey']
            (root / 'key').write_text(key)
            os.chmod(root / 'key', 0o600)
            with contextlib.closing(RealControl(address)) as observer:
                observer.authenticate()
                code, reply = observer.command('GETINFO hs/service/desc/id/' + service)
                print('Tor descriptor query for attached service, network disabled: %d' % code)

            # Original failure: Tor restarts, LND's existing control connection
            # disappears, and no controller re-registers its advertised onion.
            process.terminate()
            process.wait(timeout=10)
            owner.close()
            observer = start()
            code, lines = observer.command('GETINFO onions/detached')
            assert code == 250 and service not in ' '.join(lines)
            code, reply = observer.command('GETINFO hs/service/desc/id/' + service)
            assert code == 551 and reply == ['Not found in cache']
            print('PASS baseline: Tor restart loses the attached onion')
            observer.close()

            hook = root / 'hook'
            hook.write_text(onion.HOOK_TEXT)
            cfg = onion.config('tor.active=true\ntor.v3=true\nlisten=127.0.0.1:19735\n')
            cfg['tor.privatekeypath'] = [str(root / 'key')]
            cfg['tor.control'] = [address]
            with patch.object(onion, 'ROOT', root), patch.object(onion, 'HOOK', hook), \
                    patch.object(onion, 'active_pid', return_value=42), \
                    patch.object(onion, 'runtime_config', return_value=cfg), \
                    patch.object(onion, 'advertised', return_value=(service, 9735)), \
                    patch.object(onion, 'run', return_value=onion.HELPER + ' release'), \
                    patch.object(onion, 'Control', RealControl):
                result = onion.recover()
                assert result == 'LND onion: recovered ' + service
                assert key not in result
                print('PASS recovery: same identity registered and detached')
                assert 'already registered' in onion.recover()
                print('PASS repeated recovery: existing onion preserved before descriptor publication')
                with contextlib.closing(RealControl(address)) as next_lnd:
                    next_lnd.authenticate()
                    code, lines = next_lnd.command('GETINFO onions/detached')
                    assert code == 250 and service in ' '.join(lines)
                    command = 'ADD_ONION ' + key + ' Port=9735,127.0.0.1:19735'
                    assert next_lnd.command(command)[0] == 550
                    print('PASS baseline: detached onion collides with the next LND registration')
                    onion.release()
                    onion.release()
                    assert next_lnd.command(command)[0] == 250
                    print('PASS release: next attached registration succeeds; repeated release is harmless')
        finally:
            if process and process.poll() is None:
                process.terminate()
                process.wait(timeout=10)


if __name__ == '__main__':
    main()
