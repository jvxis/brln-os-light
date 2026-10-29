"""Run the real lab bootstrap against local Git fixtures on disposable Ubuntu.

Installer fixtures record dispatch; full Linux installation is a separate check.
"""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


@unittest.skipUnless(os.name == 'posix' and os.geteuid() == 0,
                     'Run on root disposable Ubuntu 24.04 with systemd')
class RegtestBootstrapTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='regtest-bootstrap-test-')
        self.addCleanup(self.tmp.cleanup)
        self.folder = Path(self.tmp.name)
        self.source = self.folder / 'source'
        self.source.mkdir()
        self.bin = self.folder / 'bin'; self.bin.mkdir()
        self.marker = self.folder / 'dispatched'
        self.env = os.environ.copy()
        for k in list(self.env):
            if k.startswith(('GIT_', 'LOS_REGTEST_', 'BRLN_')):
                self.env.pop(k)
        self.env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=str(self.folder/'gitconfig'),
                        GIT_AUTHOR_NAME='Fixture', GIT_AUTHOR_EMAIL='fixture@example.invalid',
                        GIT_COMMITTER_NAME='Fixture', GIT_COMMITTER_EMAIL='fixture@example.invalid',
                        TEST_MARKER=str(self.marker), PATH=str(self.bin)+os.pathsep+self.env['PATH'])
        self.git('init', '-q', '-b', 'agent/0.5.50-release', str(self.source))
        lab=self.source/'scripts/regtest-lab'; lab.mkdir(parents=True)
        shutil.copyfile(ROOT/'scripts/regtest-lab/safety.py',lab/'safety.py')
        app=self.source/'lightningos-light';app.mkdir()
        (app/'install_regtest.sh').write_text('set -eu\nprintf "%s\\n" "$*" >> "$TEST_MARKER"\nexit "${TEST_INSTALL_FAIL:-0}"\n')
        self.git('-C',str(self.source),'add','.')
        self.git('-C',str(self.source),'commit','-qm','fixture')
        self.sha=self.git('-C',str(self.source),'rev-parse','HEAD').stdout.strip()
        self.git('config','--global','url.'+str(self.source)+'.insteadOf','https://github.com/jvxis/brln-os-light.git')

    def git(self,*args):
        return subprocess.run(['git',*args],env=self.env,text=True,capture_output=True,check=True)

    def wrapper(self,name,body):
        p=self.bin/name;p.write_text('#!/bin/bash\nset -eu\n'+body+'\n');p.chmod(0o755)

    def run_bootstrap(self,args=('--disposable-host',),**env):
        return subprocess.run(['bash',str(ROOT/'lo_regtest_bootstrap.sh'),*args],
                              env={**self.env,**env},text=True,capture_output=True)

    def test_default_and_repeat_dispatch_only_regtest_without_mutating_source(self):
        for _ in range(2):
            r=self.run_bootstrap(BRLN_INSTALLER='install.sh',REPO_URL='https://invalid.example/')
            self.assertEqual(r.returncode,0,r.stderr)
            self.assertIn(self.sha,r.stdout)
        self.assertEqual(self.marker.read_text().splitlines(),['--disposable-host']*2)
        self.assertEqual(self.git('-C',str(self.source),'status','--porcelain').stdout,'')

    def test_exact_commit_ref(self):
        r=self.run_bootstrap(LOS_REGTEST_REF=self.sha)
        self.assertEqual(r.returncode,0,r.stderr)
        self.assertIn(self.sha,r.stdout)

    def test_opt_in_required(self):
        self.assertEqual(self.run_bootstrap(args=()).returncode,2)
        self.assertFalse(self.marker.exists())

    def test_unknown_ref_and_option_injection_do_not_dispatch(self):
        for ref in ['missing-branch','--upload-pack=bad','main;touch /tmp/bad']:
            self.assertNotEqual(self.run_bootstrap(LOS_REGTEST_REF=ref).returncode,0)
        self.assertFalse(self.marker.exists())

    def test_non_systemd_wsl_is_rejected(self):
        self.wrapper('cat','if [[ "$1" == /proc/1/comm ]]; then echo init; else exec /bin/cat "$@"; fi')
        r=self.run_bootstrap();self.assertNotEqual(r.returncode,0)
        self.assertIn('systemd must run as PID 1',r.stderr)
        self.assertFalse(self.marker.exists())

    def test_wrong_hostname_is_rejected(self):
        self.wrapper('hostname','echo production-node')
        self.assertNotEqual(self.run_bootstrap().returncode,0)
        self.assertFalse(self.marker.exists())

    def test_production_service_is_rejected(self):
        self.wrapper('systemctl','echo loaded')
        r=self.run_bootstrap();self.assertNotEqual(r.returncode,0)
        self.assertIn('production service',r.stderr)
        self.assertFalse(self.marker.exists())

    def test_installer_failure_status_and_checkout_cleanup(self):
        before=set(Path('/var/tmp').glob('los-regtest-bootstrap.*'))
        r=self.run_bootstrap(TEST_INSTALL_FAIL='23')
        self.assertEqual(r.returncode,23,r.stderr)
        self.assertEqual(self.marker.read_text().strip(),'--disposable-host')
        self.assertEqual(set(Path('/var/tmp').glob('los-regtest-bootstrap.*')),before)


if __name__ == '__main__':
    unittest.main()
