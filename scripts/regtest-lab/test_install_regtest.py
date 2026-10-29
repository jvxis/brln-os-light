"""Run on Linux: python3 -m unittest discover -s scripts/regtest-lab -v."""
import importlib.util
from html.parser import HTMLParser
import json
import os
import shutil
import subprocess
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import safety
import deploy
import control
import nodes


class AliasViewTests(unittest.TestCase):
    def test_setup_verification_requires_the_matching_native_alias(self):
        spec = importlib.util.spec_from_file_location('verify_los_alias_test', Path(__file__).with_name('verify-los.py'))
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        api = SimpleNamespace(call=lambda endpoint: {'current': {'alias': 'LOS-Regtest-Alice'}})
        with patch.object(module, 'ln', return_value={'alias': 'LOS-Regtest-Alice'}):
            module.verify_alias(api, 'alice')
        for wrong in ['', 'LOS-Regtest-Bob']:
            api = SimpleNamespace(call=lambda endpoint: {'current': {'alias': wrong}})
            with patch.object(module, 'ln', return_value={'alias': 'LOS-Regtest-Alice'}):
                with self.assertRaises(RuntimeError):
                    module.verify_alias(api, 'alice')


@unittest.skipUnless(sys.platform == 'linux', 'requires Linux configure helper (pwd)')
class LabNoticeTests(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location('configure_los_notice_test', Path(__file__).with_name('configure-los.py'))
        self.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.module)

    def test_scope_panel_is_keyboard_native_and_preserves_application_assets(self):
        original = '<html><head><title>LOS</title><script type="module" src="/assets/app.js"></script></head><body class="app"><main id="root"></main></body></html>'
        result = self.module.add_lab_notice(original)
        class DOM(HTMLParser):
            def __init__(self):
                super().__init__()
                self.elements = []
            def handle_starttag(self, tag, attrs):
                self.elements.append((tag, dict(attrs)))
        dom = DOM()
        dom.feed(result)
        details = [attrs for tag, attrs in dom.elements if tag == 'details']
        self.assertEqual(len(details), 1)
        self.assertEqual(details[0]['id'], 'los-regtest-notice')
        self.assertNotIn('open', details[0], 'scope panel must start collapsed')
        self.assertNotIn('pointer-events:none', details[0].get('style', ''))
        summaries = [attrs for tag, attrs in dom.elements if tag == 'summary']
        self.assertEqual(len(summaries), 1)
        self.assertEqual(summaries[0]['aria-controls'], 'los-regtest-support')
        styles = [attrs for tag, attrs in dom.elements if tag == 'style']
        self.assertEqual(styles, [{'id': 'los-regtest-styles'}])
        self.assertLess(next(i for i, item in enumerate(dom.elements) if item[0] == 'style'),
                        next(i for i, item in enumerate(dom.elements) if item[0] == 'body'))
        panels = [attrs for tag, attrs in dom.elements if tag == 'section' and 'data-lab-language' in attrs]
        self.assertEqual([item['lang'] for item in panels], ['pt-BR', 'en'])
        self.assertEqual(len([tag for tag, _ in dom.elements if tag == 'table']), 2)
        inputs = {attrs['value']: attrs for tag, attrs in dom.elements if tag == 'input'}
        self.assertEqual(set(inputs), {'pt', 'en'})
        self.assertEqual(inputs['pt']['name'], inputs['en']['name'])
        self.assertIn('checked', inputs['pt'])
        self.assertNotIn('checked', inputs['en'])
        labels = [attrs.get('for') for tag, attrs in dom.elements if tag == 'label']
        self.assertEqual(labels, [inputs['pt']['id'], inputs['en']['id']])
        self.assertIn(('main', {'id': 'root'}), dom.elements)
        self.assertIn(('script', {'type': 'module', 'src': '/assets/app.js'}), dom.elements)
        self.assertEqual(sum(tag == 'script' for tag, _ in dom.elements), 1, 'notice must not add scripts')
        self.assertIn('REGTEST | LOS', result)
        self.assertIn('Not supported', result)
        self.assertIn('The native alias is visible in the header and read-only configuration view.', result)
        self.assertIn('GitHub', result)
        operations = {attrs['data-lab-operation']: attrs.get('data-lab-status')
                      for tag, attrs in dom.elements if tag == 'tr' and 'data-lab-operation' in attrs}
        self.assertEqual(operations['channel-read'], 'validated')
        self.assertEqual(operations['installer-channel'], 'validated')
        for operation in ['channel-open', 'channel-open-batch', 'balanced-open', 'channel-close', 'channel-force-close', 'channel-fees', 'channel-autofee', 'node-retirement']:
            self.assertEqual(operations[operation], 'validated')
        for operation in ['channel-rebalance', 'succession', 'message-signing', 'utxo-management', 'audit-log']:
            self.assertEqual(operations[operation], 'validated')
        self.assertEqual(operations['utxo-lock'], 'validated')
        self.assertEqual(operations['channel-automation'], 'unvalidated', 'manual runs do not validate scheduled automations')
        self.assertIn('Abrir canal balanceado', result)
        self.assertIn('Balanced channel opening', result)
        self.assertIn('Not validated', result)

    def test_missing_body_and_duplicate_notice_are_refused(self):
        with self.assertRaisesRegex(RuntimeError, 'Could not attach'):
            self.module.add_lab_notice('<html><head></head></html>')
        with self.assertRaisesRegex(RuntimeError, 'Could not attach'):
            self.module.add_lab_notice('<html><body></body></html>')
        once = self.module.add_lab_notice('<html><head></head><body></body></html>')
        with self.assertRaisesRegex(RuntimeError, 'already attached'):
            self.module.add_lab_notice(once)

    def test_legacy_database_credential_is_preserved_and_foreign_dsn_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); credential = root / 'database-password'; env = root / 'secrets.env'
            env.write_text('NOTIFICATIONS_PG_DSN=postgres://los_regtest_charlie:fixture%21password@127.0.0.1:5432/los_regtest_charlie?sslmode=disable\n')
            previous = env.read_bytes()
            self.assertEqual(self.module.database_password(credential, env, 'los_regtest_charlie'), 'fixture!password')
            self.assertEqual(env.read_bytes(), previous)
            self.assertEqual(credential.stat().st_mode & 0o777, 0o600)
            credential.unlink(); env.write_text('NOTIFICATIONS_PG_DSN=postgres://foreign:fixture@127.0.0.1/another_db\n')
            with self.assertRaisesRegex(RuntimeError, 'cannot be safely adopted'):
                self.module.database_password(credential, env, 'los_regtest_charlie')
            self.assertFalse(credential.exists())

    def test_panel_certificate_adds_independent_hostname_without_rotating_private_key(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); folder = root / 'alice'; folder.mkdir()
            ca = root / 'ca.crt'
            self.module.run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                            '-subj', '/CN=regtest fixture', '-keyout', str(root / 'ca.key'), '-out', str(ca))
            self.module.ensure_panel_certificate(folder, ca, 'localhost')
            original_key = (folder / 'server.key').read_bytes()
            self.module.ensure_panel_certificate(folder, ca, 'alice.localhost')
            cert = folder / 'server.crt'
            self.assertEqual((folder / 'server.key').read_bytes(), original_key)
            self.module.run('openssl', 'verify', '-CAfile', str(ca), '-verify_hostname', 'alice.localhost', str(cert))
            self.module.run('openssl', 'verify', '-CAfile', str(ca), '-verify_ip', '127.0.0.1', str(cert))
            mismatch = subprocess.run(['openssl', 'verify', '-CAfile', str(ca), '-verify_hostname', 'bob.localhost', str(cert)], capture_output=True)
            self.assertNotEqual(mismatch.returncode, 0, 'one node certificate must not claim another node hostname')
            original_cert = cert.read_bytes()
            self.module.ensure_panel_certificate(folder, ca, 'alice.localhost')
            self.assertEqual(cert.read_bytes(), original_cert, 'repeat must retain an already correct certificate')


@unittest.skipUnless(sys.platform == 'linux', 'requires Linux /proc executable identity')
class ProvisionRuntimeTests(unittest.TestCase):
    def test_changed_running_binary_restarts_but_repeat_and_inactive_do_not(self):
        script = Path(__file__).with_name('provision.sh').read_text()
        start = script.index('restart_changed_native() {')
        function = script[start:script.index('\n}', start) + 2]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / 'lnd'
            shutil.copyfile('/usr/bin/sleep', binary)
            binary.chmod(0o755)
            process = subprocess.Popen([str(binary), '60'])
            try:
                calls = root / 'calls'
                # Keep the service wrapper local; executable comparison uses real /proc.
                shell = '''set -eu
fixture_pid=$1; calls=$2; binary=$3
systemctl() {
  if [[ $1 == show ]]; then printf '%s\\n' "$fixture_pid"; else printf '%s\\n' "$*" >> "$calls"; fi
}
''' + function + '\nrestart_changed_native los-regtest-alice "$binary"\n'
                def run(pid):
                    subprocess.run(['bash', '-c', shell, 'test', str(pid), str(calls), str(binary)], check=True)
                run(process.pid)
                self.assertFalse(calls.exists(), 'unchanged live executable must stay running')
                replacement = root / 'replacement'
                replacement.write_bytes(b'new verified executable fixture')
                replacement.replace(binary)
                run(process.pid)
                self.assertEqual(calls.read_text().splitlines(), ['restart los-regtest-alice'])
                calls.unlink()
                run(0)
                self.assertFalse(calls.exists(), 'inactive service is started by enable --now')
                shutil.copyfile('/usr/bin/sleep', binary)
                run(process.pid)
                self.assertFalse(calls.exists(), 'identical binary on retry must not restart')
            finally:
                process.terminate()
                process.wait(timeout=5)

    def test_bitcoin_restart_restores_only_previously_active_dependents(self):
        script = Path(__file__).with_name('provision.sh').read_text()
        start = script.index('restart_changed_native() {')
        function = script[start:script.index('\n}', start) + 2]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / 'bitcoind'
            shutil.copyfile('/usr/bin/sleep', binary)
            binary.chmod(0o755)
            process = subprocess.Popen([str(binary), '60'])
            try:
                replacement = root / 'replacement'
                replacement.write_bytes(b'verified replacement fixture')
                replacement.replace(binary)
                calls = root / 'calls'
                shell = '''set -eu
fixture_pid=$1; calls=$2; binary=$3
LND_NODES=(alice bob charlie)
systemctl() {
  if [[ $1 == show ]]; then printf '%s\\n' "$fixture_pid"
  elif [[ $1 == is-active ]]; then [[ $3 != los-regtest-ui-bob ]]
  else printf '%s\\n' "$*" >> "$calls"; fi
}
''' + function + '\nrestart_changed_native los-regtest-bitcoin "$binary"\n'
                subprocess.run(['bash', '-c', shell, 'test', str(process.pid), str(calls), str(binary)], check=True)
                self.assertEqual(calls.read_text().splitlines(), [
                    'restart los-regtest-bitcoin',
                    'start los-regtest-alice los-regtest-ui-alice los-regtest-bob los-regtest-charlie los-regtest-ui-charlie'])
            finally:
                process.terminate()
                process.wait(timeout=5)

    def test_native_binary_install_is_atomic_and_failed_copy_preserves_live_binary(self):
        script = Path(__file__).with_name('provision.sh').read_text()
        start = script.index('install_native_binary() {')
        function = script[start:script.index('\n}', start) + 2]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / 'bitcoind'
            shutil.copyfile('/usr/bin/sleep', binary)
            binary.chmod(0o755)
            process = subprocess.Popen([str(binary), '60'])
            try:
                original = binary.read_bytes()
                shell = 'set -eu\n' + function + '\ninstall_native_binary "$1" "$2"\n'
                failed = subprocess.run(['bash', '-c', shell, 'test', str(root/'absent'), str(binary)], capture_output=True)
                self.assertNotEqual(failed.returncode, 0)
                self.assertEqual(binary.read_bytes(), original)
                subprocess.run(['bash', '-c', shell, 'test', '/usr/bin/true', str(binary)], check=True)
                self.assertEqual(binary.read_bytes(), Path('/usr/bin/true').read_bytes())
                self.assertIsNone(process.poll(), 'old executable must stay alive until controlled service restart')
                self.assertEqual(Path('/proc/'+str(process.pid)+'/exe').read_bytes(), original)
                self.assertFalse(binary.with_name('bitcoind.new').exists())
            finally:
                process.terminate()
                process.wait(timeout=5)


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

    def test_recovery_accepts_all_three_panels_without_dropping_charlie(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / 'lab'; backup = root / '.deployment-backup'
            backup.mkdir(parents=True)
            active = ['los-regtest-ui-' + n for n in nodes.NODES]
            (backup / 'rollback.json').write_text(json.dumps({'assets': [], 'active': active}))
            with patch.object(deploy, 'ROOT', root), patch.object(deploy, 'BACKUP', backup), patch.object(deploy, 'run') as run, patch.object(deploy.subprocess, 'run'):
                deploy.recover()
                run.assert_any_call('systemctl', 'start', *active)
                run.assert_any_call(sys.executable, str(root / 'verify-los.py'), '--setup-only', '--node', 'charlie')


class NodeAliasTests(unittest.TestCase):
    def test_default_alias_migration_preserves_other_options_and_custom_names(self):
        for node, spec in nodes.NODES.items():
            original = '[Application Options]\nalias=LOS-REGTEST-' + node.upper() + '\nwallet-unlock-password-file=/private/fixture\n[Bitcoin]\nbitcoin.regtest=true\n'
            updated = nodes.migrate_alias(original, node)
            self.assertEqual(nodes.configured_alias(updated), spec['alias'])
            self.assertEqual(nodes.migrate_alias(updated, node), updated)
            self.assertEqual(updated.replace(spec['alias'], 'LOS-REGTEST-' + node.upper()), original)
            custom = original.replace('LOS-REGTEST-' + node.upper(), 'My custom test node')
            self.assertEqual(nodes.migrate_alias(custom, node), custom)

    def test_missing_alias_is_added_to_application_section_and_duplicates_refused(self):
        original = '[Application Options]\nlisten=127.0.0.1:9735\n[Bitcoin]\nbitcoin.regtest=true\n'
        self.assertEqual(nodes.configured_alias(nodes.migrate_alias(original, 'alice')), 'LOS-Regtest-Alice')
        with self.assertRaisesRegex(RuntimeError, 'Duplicate'):
            nodes.migrate_alias('[Application Options]\nalias=Alice\nalias=Bob\n', 'alice')

    def test_interrupted_alias_update_is_applied_once_from_actual_native_state(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); (root / 'charlie').mkdir()
            (root / 'charlie/lnd.conf').write_text('[Application Options]\nalias=LOS-Regtest-Charlie\n')
            with patch.object(control, 'ROOT', root), patch.object(control, 'wait_info', side_effect=[{'alias': 'LOS-REGTEST-CHARLIE'}, {'alias': 'LOS-Regtest-Charlie'}]), patch.object(control.subprocess, 'run') as restart:
                control.apply_alias('charlie')
                restart.assert_called_once_with(['systemctl', 'restart', 'los-regtest-charlie'], check=True)
            with patch.object(control, 'ROOT', root), patch.object(control, 'wait_info', return_value={'alias': 'LOS-Regtest-Charlie'}), patch.object(control.subprocess, 'run') as restart:
                control.apply_alias('charlie')
                restart.assert_not_called()


class FinancialSmokeTests(unittest.TestCase):
    def test_native_triangle_smoke_waits_for_chain_and_reuses_channels(self):
        events, channels, pending = [], {n: [] for n in nodes.NODES}, []
        opened, payments, queries = [], [], {}
        def native(node, action, *args):
            if action == 'listpeers':
                return {'peers': [{'pub_key': n} for n in nodes.NODES if n != node]}
            if action == 'listchannels':
                return {'channels': channels[node]}
            if action == 'pendingchannels':
                return {'pending_open_channels': [{'channel': {'remote_node_pub': target}} for source, target in pending if source == node]}
            if action == 'openchannel':
                target = next(a.split('=', 1)[1] for a in args if a.startswith('--node_key='))
                self.assertIn('--push_amt=500000', args)
                pending.append((node, target)); opened.append((node, target))
                return {}
            if action == 'queryroutes':
                queries[node] = queries.get(node, 0) + 1
                if queries[node] < 3:
                    raise RuntimeError('native bandwidth not ready')
                return {'routes': [{}]}
            if action == 'addinvoice':
                events.append('invoice:' + node)
                return {'payment_request': 'fixture-' + node}
            raise AssertionError(action)
        def mine(_):
            events.append('mine')
            for source, target in pending:
                channels[source].append({'remote_pubkey': target, 'active': True})
                channels[target].append({'remote_pubkey': source, 'active': True})
            pending.clear()
        def synced(node):
            events.append('synced:' + node)
            return {'identity_pubkey': node}
        def send(node, path, payload, **kwargs):
            payments.append((node, payload['payment_request']))
            return {'result': {'status': 'SUCCEEDED'}}
        with patch.object(control, 'ln', side_effect=native), patch.object(control, 'wait_info', side_effect=synced), patch.object(control, 'mine', side_effect=mine), patch.object(control.time, 'sleep'), patch.object(control, 'rest', side_effect=send):
            control.smoke()
            self.assertEqual(opened, list(nodes.CHANNEL_PAIRS))
            last_mine = max(i for i, e in enumerate(events) if e == 'mine')
            first_invoice = next(i for i, e in enumerate(events) if e.startswith('invoice:'))
            self.assertEqual(set(events[last_mine + 1:first_invoice]), {'synced:' + n for n in nodes.NODES})
            self.assertEqual(payments, [(source, 'fixture-' + target) for source, target in nodes.CHANNEL_PAIRS])
            self.assertTrue(all(len(c) == 2 for c in channels.values()))
            control.smoke()
            self.assertEqual(opened, list(nodes.CHANNEL_PAIRS), 'repeat must not duplicate existing channels')
            self.assertEqual(events.count('mine'), 3)

    def test_pending_channel_retry_mines_existing_funding_without_reopening(self):
        mined = []
        def native(node, action, *args):
            if action == 'listpeers': return {'peers': [{'pub_key': 'bob'}]}
            if action == 'pendingchannels': return {'pending_open_channels': [{'channel': {'remote_node_pub': 'bob'}}]}
            if action == 'listchannels': return {'channels': [{'remote_pubkey': 'bob', 'active': True}] if mined else []}
            raise AssertionError('unexpected native mutation: ' + action)
        with patch.object(control, 'ln', side_effect=native), patch.object(control, 'mine', side_effect=mined.append):
            control.ensure_channel('alice', 'bob', 'bob')
        self.assertEqual(mined, [6])

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
