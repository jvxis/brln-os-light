"""Run with python3 -m unittest discover -s scripts -p test_lnd_onion.py -v."""
import base64
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import socket
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('onion', Path(__file__).resolve().parents[1]
                                             / 'internal/privileged/assets/lnd-onion.py')
onion = importlib.util.module_from_spec(spec)
spec.loader.exec_module(onion)
runtime_config = onion.runtime_config
SERVICE = 'a' * 56
KEY = 'ED25519-V3:' + base64.b64encode(bytes(range(64))).decode()


class RuntimeConfigTests(unittest.TestCase):
    def load(self, args=(), text='tor.active=true\ntor.v3=true\n',
             account_home='/home/node', status='Uid:\t1001\t1001\t1001\t1001\n'):
        command = ('/usr/local/bin/lnd\0' + '\0'.join(args) + '\0').encode()
        self.config_reads = []

        def read(path, *args, **kwargs):
            if str(path) == '/proc/42/status':
                return status
            self.config_reads.append(str(path))
            return text

        with patch.object(Path, 'read_bytes', return_value=command), \
                patch.object(Path, 'read_text', read), \
                patch('pwd.getpwuid', return_value=SimpleNamespace(pw_dir=account_home)) as lookup:
            result = runtime_config(42)
            self.lookup_calls = lookup.call_args_list
            return result

    def test_default_directory_belongs_to_running_lnd_not_root_helper(self):
        cfg = self.load()
        self.assertEqual(cfg['lnddir'], ['/home/node/.lnd'])
        self.assertEqual(self.config_reads, ['/home/node/.lnd/lnd.conf'])
        self.assertEqual(self.lookup_calls[0].args, (1001,))

    def test_configfile_override_without_lnddir(self):
        cfg = self.load(['--configfile=/etc/node/lnd.conf'])
        self.assertEqual(self.config_reads, ['/etc/node/lnd.conf'])
        self.assertEqual(cfg['lnddir'], ['/home/node/.lnd'])

    def test_short_configfile_option_is_not_silently_ignored(self):
        for args in (['-C', '/etc/node.conf'], ['-C/etc/node.conf'], ['-C=/etc/node.conf']):
            with self.subTest(args=args):
                cfg = self.load(args)
                self.assertEqual(self.config_reads, ['/etc/node.conf'])
                self.assertEqual(cfg['lnddir'], ['/home/node/.lnd'])

    def test_root_service_default_uses_its_account_home(self):
        cfg = self.load(account_home='/root', status='Uid: 0 0 0 0\n')
        self.assertEqual(cfg['lnddir'], ['/root/.lnd'])
        self.assertEqual(self.lookup_calls[0].args, (0,))

    def test_config_lnddir_is_honored_after_selecting_config_file(self):
        cfg = self.load(text='lnddir=/srv/node\ntor.v3=true\n')
        self.assertEqual(self.config_reads, ['/home/node/.lnd/lnd.conf'])
        self.assertEqual(cfg['lnddir'], ['/srv/node'])

    def test_explicit_directory_and_cli_options_still_take_precedence(self):
        cfg = self.load(['--lnddir', '/srv/cli', '--configfile', '/etc/node.conf',
                         '--tor.v3=false'], text='lnddir=/srv/file\ntor.v3=true\n')
        self.assertEqual(cfg['lnddir'], ['/srv/cli'])
        self.assertEqual(cfg['tor.v3'], ['false'])
        self.assertEqual(self.config_reads, ['/etc/node.conf'])
        self.assertEqual(self.lookup_calls, [])

    def test_ambiguous_or_missing_process_user_fails_closed(self):
        for status in ('', 'Uid: bad data\n', 'Uid: 1001 0 0 0\n'):
            with self.subTest(status=status), self.assertRaises(onion.RecoveryError):
                self.load(status=status)
            self.assertEqual(self.config_reads, [])

    def test_unknown_account_does_not_fall_back_to_helpers_home(self):
        with patch.object(Path, 'read_bytes', return_value=b'lnd\0'), \
                patch.object(Path, 'read_text', return_value='Uid: 1001 1001 1001 1001\n'), \
                patch('pwd.getpwuid', side_effect=KeyError):
            with self.assertRaises(onion.RecoveryError):
                runtime_config(42)

    def test_relative_or_unexpanded_paths_are_not_guessed(self):
        for args in (['--lnddir=relative'], ['--configfile=relative'],
                     ['--lnddir=/srv/$USER/lnd']):
            with self.subTest(args=args), self.assertRaises(onion.RecoveryError):
                self.load(args)
        for account_home in ('', 'relative'):
            with self.subTest(home=account_home), self.assertRaises(onion.RecoveryError):
                self.load(account_home=account_home)

    def test_default_home_symlink_is_read_without_modifying_it(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            (base / 'data').mkdir()
            content = 'tor.active=true\ntor.v3=true\n'
            (base / 'data/lnd.conf').write_text(content)
            (base / '.lnd').symlink_to(base / 'data', target_is_directory=True)
            read_text = Path.read_text

            def read(path, *args, **kwargs):
                if str(path) == '/proc/42/status':
                    return 'Uid: 1001 1001 1001 1001\n'
                return read_text(path, *args, **kwargs)

            with patch.object(Path, 'read_bytes', return_value=b'lnd\0'), \
                    patch.object(Path, 'read_text', read), \
                    patch('pwd.getpwuid', return_value=SimpleNamespace(pw_dir=temp)):
                cfg = runtime_config(42)
            self.assertEqual(cfg['lnddir'], [str(base / '.lnd')])
            self.assertTrue((base / '.lnd').is_symlink())
            self.assertEqual((base / 'data/lnd.conf').read_text(), content)


class FakeControl:
    def __init__(self, code=552):
        self.commands = []
        self.code = code
        self.add_code = 250
        self.delete_code = 250
        self.reply = []
        self.add_reply = ['ServiceID=' + SERVICE]

    def authenticate(self):
        pass

    def close(self):
        pass

    def command(self, value):
        self.commands.append(value)
        if value.startswith('GETINFO'):
            return self.code, self.reply
        if value.startswith('ADD_ONION'):
            return self.add_code, self.add_reply
        return self.delete_code, []


class RecoveryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.key = self.root / 'key'
        self.key.write_text(KEY)
        self.hook = self.root / 'hook'
        self.hook.write_text(onion.HOOK_TEXT)
        self.cfg = onion.config('tor.active=true\ntor.v3=true\nlisten=0.0.0.0:19735\nlisten=[::]:29735\n')
        self.cfg['tor.privatekeypath'] = [str(self.key)]
        self.ctrl = FakeControl()
        for name, value in [('ROOT', self.root), ('HOOK', self.hook)]:
            p = patch.object(onion, name, value)
            p.start()
            self.addCleanup(p.stop)
        for name, value in [('active_pid', 42), ('runtime_config', self.cfg),
                            ('advertised', (SERVICE, 9735)), ('Control', self.ctrl),
                            ('run', onion.HELPER + ' release')]:
            p = patch.object(onion, name, return_value=value)
            setattr(self, name + '_mock', p.start())
            self.addCleanup(p.stop)

    def test_recover_keeps_pid_key_and_all_listen_ports(self):
        result = onion.recover()
        self.assertEqual(result, 'LND onion: recovered ' + SERVICE)
        self.assertEqual(self.ctrl.commands[-1], 'ADD_ONION ' + KEY +
                         ' Flags=Detach Port=9735,127.0.0.1:19735 Port=9735,127.0.0.1:29735')
        self.assertEqual(json.loads((self.root / 'pending.json').read_text()),
                         {'service': SERVICE, 'control': '127.0.0.1:9051'})
        self.assertNotIn(KEY, result)
        self.assertNotIn(KEY, (self.root / 'pending.json').read_text())
        self.assertEqual(self.active_pid_mock.call_count, 3)

    def test_existing_onion_does_not_read_key_or_mutate(self):
        self.ctrl.code = 250
        self.key.unlink()
        self.assertIn('already published', onion.recover())
        self.assertEqual(len(self.ctrl.commands), 1)
        self.assertFalse((self.root / 'pending.json').exists())

    def test_encrypted_key_refuses_registration(self):
        self.cfg['tor.encryptkey'] = ['true']
        with self.assertRaisesRegex(onion.RecoveryError, 'operator-scheduled'):
            onion.recover()
        self.assertEqual(self.ctrl.commands, [])

    def test_disabled_or_stopped_lnd(self):
        self.active_pid_mock.return_value = 0
        self.assertIn('not active', onion.recover())
        self.runtime_config_mock.assert_not_called()
        self.active_pid_mock.return_value = 42
        self.cfg['tor.active'] = ['false']
        self.assertIn('disabled', onion.recover())
        self.Control_mock.assert_not_called()

    def test_missing_hook_refuses_detach(self):
        self.run_mock.return_value = ''
        with self.assertRaisesRegex(onion.RecoveryError, 'release hook'):
            onion.recover()
        self.assertFalse(any(c.startswith('ADD_ONION') for c in self.ctrl.commands))

    def test_lnd_restart_during_recovery_releases_detached_onion(self):
        self.active_pid_mock.side_effect = [42, 42, 43]
        with self.assertRaisesRegex(onion.RecoveryError, 'changed'):
            onion.recover()
        self.assertEqual(self.ctrl.commands[-1], 'DEL_ONION ' + SERVICE)

    def test_failed_add_keeps_write_ahead_record(self):
        self.ctrl.add_code = 550
        with self.assertRaisesRegex(onion.RecoveryError, 'registration failed'):
            onion.recover()
        self.assertTrue((self.root / 'pending.json').exists())

    def test_missing_descriptor_cache_can_be_recovered(self):
        self.ctrl.code = 551
        self.ctrl.reply = ['Not found in cache']
        self.assertIn('recovered', onion.recover())

    def test_existing_service_without_descriptor_is_not_claimed(self):
        self.ctrl.code = 551
        self.ctrl.reply = ['Not found in cache']
        self.ctrl.add_code = 550
        self.ctrl.add_reply = ['Onion address collision']
        self.assertIn('already registered', onion.recover())
        self.assertFalse((self.root / 'pending.json').exists())
        self.assertFalse(any(c.startswith('DEL_ONION') for c in self.ctrl.commands))

    def test_release_repeated_and_already_absent(self):
        onion.recover()
        self.ctrl.delete_code = 552
        onion.release()
        self.assertFalse((self.root / 'pending.json').exists())
        before = len(self.ctrl.commands)
        onion.release()
        self.assertEqual(len(self.ctrl.commands), before)

    def test_release_failure_keeps_retry_record(self):
        onion.recover()
        self.ctrl.delete_code = 551
        with self.assertRaises(onion.RecoveryError):
            onion.release()
        self.assertTrue((self.root / 'pending.json').exists())

    def test_unknown_publication_reply_is_not_treated_as_absent(self):
        self.ctrl.code = 551
        with self.assertRaisesRegex(onion.RecoveryError, 'could not be verified'):
            onion.recover()
        self.assertEqual(len(self.ctrl.commands), 1)

    def test_port_mapping_target_ipv6_and_duplicate_ports(self):
        cfg = onion.config('listen=0.0.0.0:1234\nlisten=[::]:1234\ntor.targetipaddress=::1\n')
        self.assertEqual(onion.mapping(cfg, 9735), 'Port=9735,[::1]:1234')
        self.assertEqual(onion.mapping({}, 9735), 'Port=9735,127.0.0.1:9735')

    def test_command_injection_addresses_rejected(self):
        for addr in ('127.0.0.1:9051\r\nDEL_ONION abc', 'unix:/tmp/tor', 'x:9735', '127.0.0.1:0'):
            with self.subTest(addr=addr), self.assertRaises((ValueError, onion.RecoveryError)):
                onion.endpoint(addr)

    def test_runtime_configuration_honors_custom_paths_and_cli_override(self):
        command = b'/usr/local/bin/lnd\0--lnddir=/srv/lnd\0--configfile=/etc/lnd.conf\0--listen=127.0.0.1:1234\0--tor.targetipaddress=127.0.0.2\0'
        with patch.object(Path, 'read_bytes', return_value=command), \
                patch.object(Path, 'read_text', return_value='tor.active=true\ntor.v3=true\nlisten=:9999\ntor.privatekeypath=/srv/key\n'):
            cfg = runtime_config(42)
        self.assertEqual(cfg['lnddir'], ['/srv/lnd'])
        self.assertEqual(cfg['tor.privatekeypath'], ['/srv/key'])
        self.assertEqual(onion.mapping(cfg, 9735), 'Port=9735,127.0.0.2:1234')

    def test_release_entrypoint_never_blocks_startup_on_error(self):
        (self.root / 'pending.json').write_text('invalid-record')
        with patch.object(onion.os, 'geteuid', return_value=0), \
                patch.object(onion.sys, 'argv', [onion.HELPER, 'release']), \
                patch.object(onion.signal, 'signal'), patch.object(onion.signal, 'alarm'), \
                contextlib.redirect_stdout(io.StringIO()) as output:
            self.assertEqual(onion.main(), 0)
        self.assertIn('[WARN]', output.getvalue())
        self.assertNotIn('invalid-record', output.getvalue())
        self.assertTrue((self.root / 'pending.json').exists())


class ProtocolTests(unittest.TestCase):
    def test_fake_control_server_cannot_steal_cookie(self):
        control = onion.Control.__new__(onion.Control)
        control.command = unittest.mock.Mock(side_effect=[
            (250, ['AUTH METHODS=COOKIE,SAFECOOKIE COOKIEFILE="/run/tor/control.authcookie"', 'OK']),
            (250, ['AUTHCHALLENGE SERVERHASH=' + '00' * 32 + ' SERVERNONCE=' + '00' * 32]),
        ])
        cookie = b'x' * 32
        with patch.object(onion, 'read_control_cookie', return_value=cookie):
            with self.assertRaisesRegex(onion.RecoveryError, 'server authentication failed'):
                control.authenticate()
        self.assertEqual(control.command.call_count, 2)
        self.assertNotIn(cookie.hex(), str(control.command.call_args_list))

    def test_multiline_descriptor_consumed_before_next_reply(self):
        control = onion.Control.__new__(onion.Control)
        control.sock = unittest.mock.Mock()
        control.stream = io.BytesIO(b'250+hs/service/desc/id/test=\r\nsecret descriptor\r\n..dot\r\n.\r\n250 OK\r\n552 absent\r\n')
        self.assertEqual(control.command('GETINFO test'), (250, ['hs/service/desc/id/test=', 'OK']))
        self.assertEqual(control.command('DEL_ONION test'), (552, ['absent']))

    def test_disconnect_never_returns_payload_in_exception(self):
        control = onion.Control.__new__(onion.Control)
        control.sock = unittest.mock.Mock()
        control.stream = io.BytesIO(KEY.encode())
        with self.assertRaises(onion.RecoveryError) as caught:
            control.command('GETINFO test')
        self.assertNotIn(KEY, str(caught.exception))


if __name__ == '__main__':
    unittest.main()
