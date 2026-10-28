#!/usr/bin/env python3
"""Fail before provisioning an existing node or an unowned laboratory tree."""
import os
import stat
import subprocess
import sys
from pathlib import Path

MARKER = Path('/etc/los-regtest/LAB_ONLY')
ROOTS = ['/etc/los-regtest', '/opt/los-regtest', '/var/lib/los-regtest']


def check():
    if os.geteuid() != 0:
        raise RuntimeError('Root is required on the disposable host')
    # Reject symlink ancestors, including paths not yet created.
    for value in ROOTS + ['/var/lib/lightningos', '/etc/lightningos']:
        path = Path(value)
        for part in [path, *path.parents]:
            if part.is_symlink():
                raise RuntimeError('Refusing symlink in a managed laboratory path')
    owned = MARKER.is_file() and not MARKER.is_symlink()
    if owned:
        mode = MARKER.stat()
        if mode.st_uid != 0 or stat.S_IMODE(mode.st_mode) not in [0o600, 0o644] or MARKER.read_text().strip() != 'LOS_REGTEST_ONLY':
            raise RuntimeError('Laboratory ownership marker is invalid')
    for value in ROOTS:
        path = Path(value)
        if path.exists() and not owned:
            raise RuntimeError('Existing unowned laboratory path; refusing adoption')
        if path.exists():
            if value != '/var/lib/los-regtest' and (path.stat().st_uid != 0 or path.stat().st_mode & 0o022):
                raise RuntimeError('Laboratory executable/configuration root is not root-controlled')
            for folder, dirs, files in os.walk(path, followlinks=False):
                if any((Path(folder) / name).is_symlink() for name in dirs + files):
                    raise RuntimeError('Refusing symlink inside laboratory data')
    forbidden = ['/data/lnd', '/data/bitcoin', '/var/lib/lnd', '/var/lib/bitcoin', '/var/lib/bitcoind',
                 '/etc/lightningos/config.yaml', '/opt/lightningos/manager', '/root/.lnd', '/root/.bitcoin']
    for home in Path('/home').glob('*'):
        forbidden.extend([str(home / '.lnd'), str(home / '.bitcoin')])
    if any(Path(value).exists() or Path(value).is_symlink() for value in forbidden):
        raise RuntimeError('Existing node/configuration detected; use a fresh disposable VM')
    state = Path('/var/lib/lightningos')
    if state.exists() and (not state.is_dir() or any(state.iterdir())):
        raise RuntimeError('Existing LightningOS state detected')
    config = Path('/etc/lightningos')
    if config.exists() and (not config.is_dir() or any(config.iterdir())):
        raise RuntimeError('Existing LightningOS configuration directory detected')
    for service in ['lnd', 'bitcoind', 'bitcoin', 'lightningos-manager', 'lightningos-privileged.socket']:
        result = subprocess.run(['systemctl', 'show', service, '-p', 'LoadState', '--value'], capture_output=True, text=True)
        if result.stdout.strip() not in ['', 'not-found']:
            raise RuntimeError('Existing production service definition detected: ' + service)


def initialize():
    check()
    for value in ROOTS:
        Path(value).mkdir(mode=0o700 if value.startswith('/var/lib/') else 0o755, parents=True, exist_ok=True)
    if not MARKER.exists():
        with MARKER.open('x') as stream:
            stream.write('LOS_REGTEST_ONLY\n')
    # This acknowledgment contains no credential; the unprivileged startup
    # guard must read it. All credential files retain their private modes.
    Path('/etc/los-regtest').chmod(0o755)
    MARKER.chmod(0o644)


if __name__ == '__main__':
    try:
        if sys.argv[1:] == ['check']:
            check()
        elif sys.argv[1:] == ['initialize']:
            initialize()
        else:
            raise RuntimeError('Expected check or initialize')
    except (OSError, RuntimeError) as error:
        sys.exit('Regtest preflight: ' + str(error))
