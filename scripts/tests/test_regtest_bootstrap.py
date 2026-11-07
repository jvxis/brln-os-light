"""Run the real lab bootstrap against local Git fixtures on disposable Ubuntu.

Installer fixtures record dispatch; full Linux installation is a separate check.
"""
import os
import json
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
        version=app/'ui/public/version.txt';version.parent.mkdir(parents=True)
        version.write_text('0.5.53-Beta\n')
        (app/'install_regtest.sh').write_text('set -eu\nprintf "%s\\n" "$*" >> "$TEST_MARKER"\nexit "${TEST_INSTALL_FAIL:-0}"\n')
        self.git('-C',str(self.source),'add','.')
        self.git('-C',str(self.source),'commit','-qm','fixture')
        self.sha=self.git('-C',str(self.source),'rev-parse','HEAD').stdout.strip()
        self.git('-C',str(self.source),'tag','-a','0.5.53-Beta','-m','published fixture')
        self.catalog=self.folder/'catalog'
        self.git('clone','-q','--bare',str(self.source),str(self.catalog))
        self.git('config','--global','url.'+str(self.source)+'.insteadOf','https://github.com/jvxis/brln-os-light.git')
        self.git('config','--global','url.'+str(self.catalog)+'.insteadOf','https://github.com/jvxis/brln-os-light-updates.git')
        self.metadata=self.folder/'metadata.json'
        self.metadata.write_text(json.dumps(dict(tag_name='0.5.53-Beta',immutable=True,draft=False,prerelease=False)))
        self.env['TEST_METADATA']=str(self.metadata)
        self.wrapper('curl', '[[ ${TEST_API_FAIL:-0} == 0 ]] || exit 22\ncat "$TEST_METADATA"')

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

    def test_latest_is_release_not_newer_branch(self):
        (self.source/'new-development').write_text('unreleased')
        self.git('-C',str(self.source),'add','.')
        self.git('-C',str(self.source),'commit','-qm','unreleased')
        r=self.run_bootstrap()
        self.assertEqual(r.returncode,0,r.stderr)
        self.assertIn(self.sha,r.stdout)
        self.assertIn('Immutable release, source commit and version verified',r.stdout)

    def test_metadata_failure_never_falls_back(self):
        for value in ['not json', '[]', '{"tag_name":"main"}',
                      json.dumps(dict(tag_name='0.5.53-Beta',immutable=False,draft=False,prerelease=False)),
                      json.dumps(dict(tag_name='0.5.53-Beta',immutable=True,draft=True,prerelease=False)),
                      json.dumps(dict(tag_name='0.5.53-Beta',immutable=True,draft=False,prerelease=True))]:
            with self.subTest(value=value):
                self.metadata.write_text(value)
                self.assertNotEqual(self.run_bootstrap().returncode,0)
                self.assertFalse(self.marker.exists())

    def test_catalog_unavailable_blocks_default_not_explicit_development(self):
        self.assertNotEqual(self.run_bootstrap(TEST_API_FAIL='1').returncode,0)
        self.assertFalse(self.marker.exists())
        self.assertEqual(self.run_bootstrap(TEST_API_FAIL='1',LOS_REGTEST_REF=self.sha).returncode,0)

    def test_source_and_catalog_commit_mismatch_is_rejected(self):
        (self.source/'changed').write_text('changed')
        self.git('-C',str(self.source),'add','.')
        self.git('-C',str(self.source),'commit','-qm','changed')
        self.git('-C',str(self.source),'tag','-f','0.5.53-Beta')
        r=self.run_bootstrap()
        self.assertNotEqual(r.returncode,0)
        self.assertIn('does not match the immutable release commit',r.stderr)
        self.assertFalse(self.marker.exists())

    def test_version_mismatch_is_rejected_even_when_commits_match(self):
        (self.source/'lightningos-light/ui/public/version.txt').write_text('0.5.52-Beta\n')
        self.git('-C',str(self.source),'add','.')
        self.git('-C',str(self.source),'commit','-qm','wrong version')
        self.git('-C',str(self.source),'tag','-f','0.5.53-Beta')
        self.git('-C',str(self.catalog),'fetch','--force',str(self.source),'refs/tags/0.5.53-Beta:refs/tags/0.5.53-Beta')
        r=self.run_bootstrap()
        self.assertNotEqual(r.returncode,0)
        self.assertIn('Source version does not match release tag',r.stderr)
        self.assertFalse(self.marker.exists())

    def test_opt_in_required(self):
        self.assertEqual(self.run_bootstrap(args=()).returncode,2)
        self.assertFalse(self.marker.exists())

    def test_direct_installer_defaults_to_bootstrap_and_development_is_explicit(self):
        entry=self.folder/'entry';(entry/'lightningos-light').mkdir(parents=True)
        shutil.copyfile(ROOT/'lightningos-light/install_regtest.sh',entry/'lightningos-light/install_regtest.sh')
        (entry/'lo_regtest_bootstrap.sh').write_text('printf "%s\\n" "$*" > "$TEST_MARKER"\nexit 29\n')
        for source in [None, 'latest', 'checkout', 'invalid']:
            with self.subTest(source=source):
                if self.marker.exists():self.marker.unlink()
                env=self.env.copy();env.pop('LIGHTNINGOS_INSTALL_SOURCE',None)
                env.pop('LIGHTNINGOS_REGTEST_BOOTSTRAPPED',None)
                if source is not None:env['LIGHTNINGOS_INSTALL_SOURCE']=source
                r=subprocess.run(['bash',str(entry/'lightningos-light/install_regtest.sh'),'--disposable-host'],
                                 env=env,text=True,capture_output=True)
                if source in (None,'latest'):
                    self.assertEqual(r.returncode,29,r.stderr)
                    self.assertEqual(self.marker.read_text().strip(),'--disposable-host')
                else:
                    self.assertNotEqual(r.returncode,0)
                    self.assertFalse(self.marker.exists())
                    self.assertIn('complete source checkout' if source=='checkout' else 'Invalid LIGHTNINGOS_INSTALL_SOURCE',r.stderr)

    def test_bootstrap_marker_prevents_recursive_dispatch(self):
        entry=self.folder/'entry';(entry/'lightningos-light').mkdir(parents=True)
        shutil.copyfile(ROOT/'lightningos-light/install_regtest.sh',entry/'lightningos-light/install_regtest.sh')
        (entry/'lo_regtest_bootstrap.sh').write_text('exit 29\n')
        r=subprocess.run(['bash',str(entry/'lightningos-light/install_regtest.sh'),'--disposable-host'],
                         env={**self.env,'LIGHTNINGOS_REGTEST_BOOTSTRAPPED':'1'},text=True,capture_output=True)
        self.assertEqual(r.returncode,1,r.stderr)
        self.assertIn('complete source checkout',r.stderr)

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
