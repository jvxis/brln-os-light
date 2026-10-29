import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch

import automine


class AutomineTests(unittest.TestCase):
    def test_tick_refuses_foreign_chain_without_mining(self):
        with patch.object(automine, 'local_guard'), patch.object(automine.control, 'checked_lab', side_effect=RuntimeError('Refusing non-regtest')), patch.object(automine.control, 'mine') as mine:
            with self.assertRaises(RuntimeError):
                automine.tick()
            mine.assert_not_called()

    def test_tick_mines_exactly_one_and_paused_tick_does_not_call_rpc(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(automine, 'CONFIG', Path(folder)), patch.object(automine, 'local_guard'), patch.object(automine.control, 'checked_lab') as check, patch.object(automine.control, 'mine') as mine:
            automine.tick()
            check.assert_called_once()
            mine.assert_called_once_with(1)
            (Path(folder) / 'automine-paused').touch()
            check.reset_mock(); mine.reset_mock()
            automine.tick()
            check.assert_not_called(); mine.assert_not_called()

    def test_local_guard_refuses_production_or_missing_marker(self):
        with patch.object(automine.safety, 'check', side_effect=RuntimeError('Existing production service')), patch.object(automine.control, 'mine') as mine:
            with self.assertRaises(RuntimeError):
                automine.tick()
            mine.assert_not_called()
        with tempfile.TemporaryDirectory() as folder, patch.object(automine.safety, 'check'), patch.object(automine.safety, 'MARKER', Path(folder)/'absent'), patch.object(automine.control.socket, 'gethostname', return_value='los-regtest'):
            with self.assertRaises(RuntimeError):
                automine.local_guard()

    def test_install_repeat_preserves_interval_pause_and_unrelated_files(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(automine, 'local_guard'), patch.object(automine.control, 'checked_lab'), patch.object(automine, 'systemctl') as ctl:
            config = Path(folder)/'config'; config.mkdir()
            units = Path(folder)/'units'; units.mkdir()
            (config/'miner-address').write_text('fixture')
            (config/'wallet-password').write_text('untouched')
            with patch.object(automine, 'CONFIG', config), patch.object(automine, 'UNITS', units):
                automine.install()
                self.assertEqual(automine.interval(), 60)
                ctl.assert_any_call('enable', automine.TIMER)
                (config/'automine-interval').write_text('120\n')
                (config/'automine-paused').write_text('paused\n')
                automine.install(); ctl.reset_mock(); automine.install()
                self.assertEqual(automine.interval(), 120)
                self.assertTrue((config/'automine-paused').exists())
                ctl.assert_any_call('disable', '--now', automine.TIMER)
                self.assertNotIn(('restart', automine.TIMER), [x.args for x in ctl.call_args_list])
                self.assertEqual((config/'wallet-password').read_text(), 'untouched')

    def test_pause_available_even_if_bitcoin_is_down_and_resume_fails_closed(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(automine, 'CONFIG', Path(folder)), patch.object(automine, 'local_guard'), patch.object(automine.control, 'checked_lab', side_effect=RuntimeError('offline')), patch.object(automine, 'systemctl') as ctl:
            automine.pause()
            self.assertTrue((Path(folder)/'automine-paused').exists())
            ctl.assert_any_call('stop', automine.SERVICE)
            with self.assertRaises(RuntimeError):
                automine.resume()
            self.assertTrue((Path(folder)/'automine-paused').exists())

    def test_interrupted_atomic_write_preserves_previous_interval_and_retry(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder)/'interval'; path.write_text('60\n')
            with patch.object(automine.os, 'replace', side_effect=OSError('interrupted')):
                with self.assertRaises(OSError):
                    automine.write_atomic(path, '120\n')
            self.assertEqual(path.read_text(), '60\n')
            self.assertEqual(list(Path(folder).iterdir()), [path])
            automine.write_atomic(path, '120\n')
            self.assertEqual(path.read_text(), '120\n')

    def test_invalid_intervals_rejected(self):
        for value in ['0', '-1', '9', '3601', '1m', '10\nOnBootSec=1']:
            with self.assertRaises(ValueError):
                automine.interval_seconds(value)
        self.assertEqual(automine.interval_seconds('10'), 10)
        self.assertEqual(automine.interval_seconds('3600'), 3600)
