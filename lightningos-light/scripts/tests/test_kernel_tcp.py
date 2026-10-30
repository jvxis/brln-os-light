#!/usr/bin/env python3
"""Unit regression checks; packet/systemd coverage lives in the Linux lab test."""
import copy
import importlib.util
from pathlib import Path
import unittest
from unittest import mock
from types import SimpleNamespace

source = Path(__file__).resolve().parents[2] / 'internal/server/assets/kernel-tcp.py'
spec = importlib.util.spec_from_file_location('kernel_tcp', source)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class KernelTCPTests(unittest.TestCase):
    def test_multiple_systemd_environment_files_and_repeat_install(self):
        def command(*args, **kwargs):
            field = args[args.index('-p')+1]
            return SimpleNamespace(stdout={'LoadState':'loaded', 'Environment':'GODEBUG=panicnil=1',
                'EnvironmentFiles':'/etc/operator.env (ignore_errors=no)\n/etc/lightningos-kernel-tcp/lnd.service.env (ignore_errors=no)\n'}[field])
        with mock.patch.object(module, 'run', side_effect=command), \
             mock.patch.object(module, 'read_environment_file', return_value='GODEBUG="http2client=0,multipathtcp=1"\n'):
            self.assertEqual(module.unit_godebug('lnd.service'), 'http2client=0,multipathtcp=0')

    def test_preserves_other_godebug_options(self):
        self.assertEqual(module.without_mptcp('http2client=0,multipathtcp=1,panicnil=1'), 'http2client=0,panicnil=1,multipathtcp=0')
        self.assertEqual(module.without_mptcp('multipathtcp=2,multipathtcp=3'), 'multipathtcp=0')
        self.assertEqual(module.without_mptcp(''), 'multipathtcp=0')
        with self.assertRaises(RuntimeError):module.without_mptcp('x=1\nINJECT=value')

    def test_semantic_rule_validation_rejects_false_protection(self):
        chain = {'family':'ip','table':module.TABLE,'name':'pre','type':'filter','hook':'prerouting','prio':-300,'policy':'accept'}
        rule = {'family':'ip','table':module.TABLE,'chain':'pre','expr':[
            {'match':{'op':'==','left':{'meta':{'key':'l4proto'}},'right':'tcp'}},
            {'match':{'op':'>','left':{'payload':{'protocol':'ip','field':'hdrlength'}},'right':5}},
            {'counter':{'packets':12,'bytes':480}}, {'drop':None}]}
        good = {'nftables':[{'chain':chain},{'rule':rule},{'table':{'family':'ip','name':module.TABLE}}]}
        self.assertTrue(module.valid_rules(good))
        for change in ['accept','wrong_hook','wrong_priority','wrong_length','missing_counter','extra_rule','dormant']:
            bad = copy.deepcopy(good)
            if change=='accept':bad['nftables'][1]['rule']['expr'][-1]={'accept':None}
            if change=='wrong_hook':bad['nftables'][0]['chain']['hook']='input'
            if change=='wrong_priority':bad['nftables'][0]['chain']['prio']=0
            if change=='wrong_length':bad['nftables'][1]['rule']['expr'][1]['match']['right']=6
            if change=='missing_counter':bad['nftables'][1]['rule']['expr'].pop(2)
            if change=='extra_rule':bad['nftables'].append(copy.deepcopy(bad['nftables'][1]))
            if change=='dormant':bad['nftables'][2]['table']['flags']=['dormant']
            self.assertFalse(module.valid_rules(bad),change)


if __name__ == '__main__':unittest.main()
