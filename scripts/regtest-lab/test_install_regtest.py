"""Run on Linux: python3 -m unittest discover -s scripts/regtest-lab -v."""
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import safety
import deploy
import control


@unittest.skipUnless(sys.platform == 'linux' and os.geteuid() == 0, 'requires disposable Linux root')
class PreflightTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.path = lambda value: Path(value) if Path(value).is_relative_to(self.root) else self.root / str(value).lstrip('/')
        self.patches = [patch.object(safety, 'Path', self.path),
                        patch.object(safety, 'MARKER', self.path('/etc/los-regtest/LAB_ONLY')),
                        patch.object(safety.subprocess, 'run', return_value=SimpleNamespace(stdout='not-found\n', returncode=0))]
        for item in self.patches:
            item.start()
            self.addCleanup(item.stop)

    def test_fresh_and_repeated_owned_setup(self):
        safety.check()
        previous = os.umask(0o077)
        try:
            safety.initialize()
        finally:
            os.umask(previous)
        safety.check()
        marker = self.path('/etc/los-regtest/LAB_ONLY')
        self.assertEqual(marker.read_text(), 'LOS_REGTEST_ONLY\n')
        self.assertEqual(marker.stat().st_mode & 0o777, 0o644)
        self.assertEqual(self.path('/opt/los-regtest').stat().st_mode & 0o777, 0o755)
        self.assertEqual(self.path('/var/lib/los-regtest').stat().st_mode & 0o777, 0o700)

    def test_mainnet_data_is_refused_without_mutation(self):
        wallet = self.path('/data/lnd/wallet.db')
        wallet.parent.mkdir(parents=True)
        wallet.write_bytes(b'private-fixture-do-not-touch')
        with self.assertRaisesRegex(RuntimeError, 'Existing node'):
            safety.initialize()
        self.assertEqual(wallet.read_bytes(), b'private-fixture-do-not-touch')
        self.assertFalse(self.path('/etc/los-regtest').exists())

    def test_inactive_production_service_is_refused(self):
        def show(args, **kwargs):
            return SimpleNamespace(stdout='loaded\n' if args[2] == 'lnd' else 'not-found\n')
        with patch.object(safety.subprocess, 'run', side_effect=show):
            with self.assertRaisesRegex(RuntimeError, 'production service'):
                safety.check()

    def test_no_adoption_of_unowned_existing_lab(self):
        self.path('/var/lib/los-regtest').mkdir(parents=True)
        with self.assertRaisesRegex(RuntimeError, 'unowned'):
            safety.initialize()

    def test_secret_only_production_config_is_refused(self):
        config = self.path('/etc/lightningos/secrets.env')
        config.parent.mkdir(parents=True)
        config.write_text('PRIVATE_FIXTURE=value\n')
        with self.assertRaisesRegex(RuntimeError, 'configuration directory'):
            safety.check()
        self.assertEqual(config.read_text(), 'PRIVATE_FIXTURE=value\n')

    def test_symlink_is_refused_in_owned_data(self):
        safety.initialize()
        target = self.root / 'unrelated'
        target.write_text('do not change')
        self.path('/var/lib/los-regtest/unsafe').symlink_to(target)
        with self.assertRaisesRegex(RuntimeError, 'symlink'):
            safety.check()
        self.assertEqual(target.read_text(), 'do not change')


class DeploymentTests(unittest.TestCase):
    def test_interrupted_deployment_restores_assets_and_preserves_unrelated_data(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / 'lab'
            backup = root / '.deployment-backup'
            (root / 'bin').mkdir(parents=True)
            (root / 'bin/lightningos-manager').write_text('new manager')
            (root / 'ui').mkdir()
            (root / 'ui/index.html').write_text('new UI')
            (backup / 'bin').mkdir(parents=True)
            (backup / 'bin/lightningos-manager').write_text('old manager')
            (backup / 'ui').mkdir()
            (backup / 'ui/index.html').write_text('old UI')
            (backup / 'rollback.json').write_text(json.dumps({'assets': ['bin/lightningos-manager', 'ui'], 'active': ['los-regtest-ui-alice']}))
            wallet = Path(directory) / 'wallet.db'
            wallet.write_bytes(b'unchanged')
            with patch.object(deploy, 'ROOT', root), patch.object(deploy, 'BACKUP', backup), patch.object(deploy, 'run') as run, patch.object(deploy.subprocess, 'run'):
                deploy.recover()
                run.assert_any_call('systemctl', 'start', 'los-regtest-ui-alice')
                run.assert_any_call(sys.executable, str(root / 'verify-los.py'), '--setup-only', '--node', 'alice')
                self.assertNotIn(unittest.mock.call('systemctl', 'start', 'los-regtest-ui-bob'), run.call_args_list)
            self.assertEqual((root / 'bin/lightningos-manager').read_text(), 'old manager')
            self.assertEqual((root / 'ui/index.html').read_text(), 'old UI')
            self.assertEqual(wallet.read_bytes(), b'unchanged')
            self.assertFalse(backup.exists())


class FinancialSmokeTests(unittest.TestCase):
    def test_native_smoke_waits_for_chain_after_channel_confirmation(self):
        events = []
        lists = 0
        queries = 0
        def native(node, action, *args):
            nonlocal lists, queries
            if action == 'listpeers':
                return {'peers': [{'pub_key': 'bob'}]}
            if action == 'listchannels':
                lists += 1
                return {'channels': [] if lists == 1 else [{'remote_pubkey': 'bob', 'active': True}]}
            if action == 'addinvoice':
                events.append('invoice')
                return {'payment_request': 'private-fixture-invoice'}
            if action == 'queryroutes':
                queries += 1
                events.append('route')
                if queries < 3:
                    raise RuntimeError('new channel bandwidth not yet available')
                return {'routes': [{}]}
            return {}
        def synced(node):
            events.append('synced:' + node)
            return {'identity_pubkey': node}
        with patch.object(control, 'ln', side_effect=native), patch.object(control, 'wait_info', side_effect=synced), patch.object(control, 'mine', side_effect=lambda _: events.append('mine')), patch.object(control.time, 'sleep'), patch.object(control, 'rest', return_value={'result': {'status': 'SUCCEEDED'}}) as send:
            control.smoke()
        self.assertEqual(events[-7:], ['mine', 'synced:alice', 'synced:bob', 'route', 'route', 'route', 'invoice'])
        self.assertEqual(send.call_count, 1)

    def test_approval_and_retry_use_normalized_snapshot_not_original_body(self):
        spec = importlib.util.spec_from_file_location('verify_los_test', Path(__file__).with_name('verify-los.py'))
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        with tempfile.TemporaryDirectory() as directory:
            credential = Path(directory) / 'ui-password'
            credential.write_text('private-test-password')
            class API(module.LabAPI):
                def __init__(self):
                    self.node = 'alice'
                    self.calls = []
                def call(self, endpoint, payload=None, expected=200, **kwargs):
                    self.calls.append((endpoint, payload, kwargs))
                    if endpoint == '/api/financial/intents':
                        return {'required': True, 'path': '/api/wallet/send', 'method': 'POST', 'intent_id': 'a' * 32, 'scope': 'financial_intent:' + 'a' * 32, 'payload': {'address': 'fixture', 'amount_sat': 123, 'outpoints': ['frozen:0']}}
                    return {'txid': 'same-result'}
            class Folder:
                def __truediv__(self, item):
                    return credential if item == 'ui-password' else self
            api = API()
            with patch.object(module, 'Path', return_value=Folder()):
                self.assertEqual(api.financial('/api/wallet/send', {'address': 'fixture', 'amount_sat': 123}), {'txid': 'same-result'})
            execution = [call for call in api.calls if call[0] == '/api/wallet/send']
            self.assertEqual(len(execution), 2)
            self.assertEqual(execution[0], execution[1])
            self.assertEqual(execution[0][1]['outpoints'], ['frozen:0'])
            self.assertEqual(execution[0][2]['headers']['X-LOS-Financial-Intent'], 'a' * 32)
            self.assertNotIn('confirm_password', execution[0][1])


if __name__ == '__main__':
    unittest.main()
