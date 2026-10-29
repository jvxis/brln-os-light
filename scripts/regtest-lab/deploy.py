#!/usr/bin/env python3
"""Deploy a lab Manager with recovery of the last working UI on failure/retry."""
import argparse
import hashlib
import importlib.util
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

from control import checked_lab
from safety import check
from nodes import NODES

ROOT = Path('/opt/los-regtest')
BACKUP = ROOT / '.deployment-backup'
ASSETS = ['bin/lightningos-manager', 'ui', 'source.json']
UNITS = ['los-regtest-ui-' + node for node in NODES]


def run(*args):
    result = subprocess.run(args, capture_output=True, text=True)
    if result.returncode:
        # Helpers handle credentials: never echo their captured output.
        raise RuntimeError('Laboratory step failed: ' + Path(args[0]).name)


def remove_asset(path):
    if path not in [ROOT / name for name in ASSETS] and path != BACKUP:
        raise RuntimeError('Unexpected rollback target')
    if path.is_symlink():
        raise RuntimeError('Unsafe deployment artifact')
    if path.is_dir():
        shutil.rmtree(path)
    elif path.exists():
        path.unlink()


def copy_asset(source, target):
    target.parent.mkdir(parents=True, exist_ok=True)
    if source.is_dir():
        shutil.copytree(source, target)
    else:
        shutil.copy2(source, target)


def recover():
    manifest = BACKUP / 'rollback.json'
    if not BACKUP.exists():
        return
    # Services have not been stopped before this completed manifest exists.
    if not manifest.exists():
        remove_asset(BACKUP)
        return
    data = json.loads(manifest.read_text())
    if not set(data['assets']).issubset(ASSETS) or not set(data['active']).issubset(UNITS):
        raise RuntimeError('Invalid deployment recovery manifest')
    for unit in UNITS:
        subprocess.run(['systemctl', 'stop', unit], capture_output=True)
    for name in ASSETS:
        remove_asset(ROOT / name)
        if name in data['assets']:
            copy_asset(BACKUP / name, ROOT / name)
    if data['active']:
        run('systemctl', 'start', *data['active'])
        for unit in data['active']:
            run(sys.executable, str(ROOT / 'verify-los.py'), '--setup-only', '--node', unit.removeprefix('los-regtest-ui-'))
    remove_asset(BACKUP)
    print('Restored previous laboratory Manager/UI; wallets and passwords retained.')


def deploy(args):
    check()
    checked_lab()
    recover()
    for filename, digest in [(args.manager, args.manager_sha256), (args.ui_archive, args.ui_sha256)]:
        if hashlib.sha256(Path(filename).read_bytes()).hexdigest() != digest:
            raise RuntimeError('Deployment artifact digest mismatch')
    BACKUP.mkdir(mode=0o700)
    previous = {'assets': [name for name in ASSETS if (ROOT / name).exists()],
                'active': [unit for unit in UNITS if subprocess.run(['systemctl', 'is-active', '--quiet', unit]).returncode == 0]}
    for name in previous['assets']:
        copy_asset(ROOT / name, BACKUP / name)
    temporary = BACKUP / 'rollback.partial'
    with temporary.open('w') as stream:
        json.dump(previous, stream)
        stream.flush()
        os.fsync(stream.fileno())
    temporary.replace(BACKUP / 'rollback.json')
    try:
        for unit in UNITS:
            subprocess.run(['systemctl', 'stop', unit], capture_output=True)
        spec = importlib.util.spec_from_file_location('configure_los', ROOT / 'configure-los.py')
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        module.configure(args)
        run(sys.executable, str(ROOT / 'verify-los.py'), '--setup-only')
    except BaseException:
        recover()
        raise
    remove_asset(BACKUP)
    print('Laboratory Manager/UI deployment verified over authenticated HTTPS.')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for option in ['manager', 'manager-sha256', 'ui-archive', 'ui-sha256', 'source-commit']:
        parser.add_argument('--' + option, required=True)
    try:
        deploy(parser.parse_args())
    except Exception:
        sys.exit('Regtest deployment failed. Previous installation recovery was attempted; inspect service status before retrying.')
