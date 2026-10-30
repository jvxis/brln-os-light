#!/usr/bin/python3
"""Root-only, narrowly scoped CVE-2026-98432 mitigation. No wallet access."""
import contextlib
import fcntl
import json
import os
from pathlib import Path
import re
import shlex
import stat
import subprocess
import sys
import tempfile
import time

TABLE = 'lightningos_kernel_tcp'
CONFIG = Path('/etc/lightningos-kernel-tcp')
RUNTIME = Path('/run/lightningos-kernel-tcp')
HELPER = Path('/usr/local/libexec/lightningos-kernel-tcp')
DISABLED = CONFIG / 'disabled'
STATUS = RUNTIME / 'status.json'
RULESET = '''add table ip lightningos_kernel_tcp
delete table ip lightningos_kernel_tcp
table ip lightningos_kernel_tcp {
 chain pre {
  type filter hook prerouting priority -300; policy accept;
  meta l4proto tcp ip hdrlength gt 5 counter drop
 }
}
'''
SERVICE = '''[Unit]
Description=LightningOS IPv4 TCP options mitigation (CVE-2026-98432)
DefaultDependencies=no
After=local-fs.target systemd-tmpfiles-setup.service systemd-modules-load.service nftables.service ufw.service
Before=network-pre.target
Wants=network-pre.target

[Service]
Type=oneshot
ExecStart=/usr/local/libexec/lightningos-kernel-tcp apply
TimeoutStartSec=30
UMask=0022
NoNewPrivileges=yes
ProtectHome=yes
ProtectSystem=strict
ReadWritePaths=/run/lightningos-kernel-tcp
CapabilityBoundingSet=CAP_NET_ADMIN
RestrictAddressFamilies=AF_UNIX AF_NETLINK

[Install]
WantedBy=multi-user.target
'''
TIMER = '''[Unit]
Description=Verify and restore the LightningOS IPv4 TCP options mitigation

[Timer]
OnBootSec=30s
OnUnitActiveSec=30s
AccuracySec=1s
Unit=lightningos-kernel-tcp.service

[Install]
WantedBy=timers.target
'''


def run(*args, input=None, check=True, timeout=20):
    result = subprocess.run(args, input=input, text=True, capture_output=True, timeout=timeout)
    if check and result.returncode:
        # Never echo command output: systemd environment/configuration can contain secrets.
        raise RuntimeError(Path(args[0]).name + ' operation failed')
    return result


def safe_path(path, directory=False):
    """Check every ancestor, not just the leaf, before any privileged write."""
    path = Path(path)
    if not path.is_absolute():
        raise RuntimeError('Expected an absolute installation path')
    for part in [*reversed(path.parents), path]:
        try:
            info = part.lstat()
        except FileNotFoundError:
            continue
        if stat.S_ISLNK(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise RuntimeError('Unsafe installation path')
        if part != path or directory:
            if not stat.S_ISDIR(info.st_mode):
                raise RuntimeError('Expected a directory')
        elif not stat.S_ISREG(info.st_mode):
            raise RuntimeError('Expected a regular file')


def directory(path):
    safe_path(path, directory=True)
    path.mkdir(mode=0o755, parents=True, exist_ok=True)


def atomic_write(path, data, mode=0o644):
    safe_path(path)
    directory(path.parent)
    fd, name = tempfile.mkstemp(prefix='.kernel-tcp-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as target:
            os.fchown(target.fileno(), 0, 0)
            os.fchmod(target.fileno(), mode)
            target.write(data)
            target.flush()
            os.fsync(target.fileno())
        os.replace(name, path)
    finally:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(name)


def without_mptcp(value):
    if '\n' in value or '\r' in value or '\0' in value:
        raise RuntimeError('Unsupported multiline GODEBUG')
    values = [v for v in value.split(',') if v and v.split('=', 1)[0] != 'multipathtcp']
    return ','.join([*values, 'multipathtcp=0'])


def read_environment_file(path, optional):
    # Existing LOS secrets.env is intentionally group-writable. We only read
    # GODEBUG from the service's configured files; never tighten their policy,
    # echo their contents or copy any other variable into a privileged file.
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        if optional:
            return ''
        raise
    with os.fdopen(fd, 'r') as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > 128 * 1024:
            raise RuntimeError('Unsafe or oversized service environment file')
        text = source.read(128 * 1024 + 1)
        if len(text) > 128 * 1024:
            raise RuntimeError('Oversized service environment file')
        return text


def unit_godebug(unit):
    value = ''
    if run('/usr/bin/systemctl', 'show', unit, '-p', 'LoadState', '--value', check=False).stdout.strip() == 'not-found':
        return without_mptcp('')
    configured = run('/usr/bin/systemctl', 'show', unit, '-p', 'Environment', '--value').stdout
    for entry in shlex.split(configured):
        if entry.startswith('GODEBUG='):
            value = entry[len('GODEBUG='):]
    # EnvironmentFile settings override Environment. Refuse unsupported paths/syntax
    # rather than silently losing an operator's existing runtime options.
    files = run('/usr/bin/systemctl', 'show', unit, '-p', 'EnvironmentFiles', '--value').stdout.strip()
    matches = list(re.finditer(r'(\S+) \(ignore_errors=(yes|no)\)', files))
    if files and re.sub(r'\s+', '', ''.join(m.group(0) for m in matches)) != re.sub(r'\s+', '', files):
        raise RuntimeError('Unsupported systemd EnvironmentFiles layout')
    for match in matches:
        path = Path(match[1])
        if path == CONFIG / (unit + '.env'):
            continue  # Do not retain our own previous override across upgrades.
        text = read_environment_file(path, match[2] == 'yes')
        for line in text.splitlines():
            if re.match(r'^\s*GODEBUG\s*=', line):
                rhs = line.split('=', 1)[1].strip()
                parsed = shlex.split(rhs, comments=False)
                if len(parsed) > 1:
                    raise RuntimeError('Unsupported GODEBUG EnvironmentFile syntax')
                value = parsed[0] if parsed else ''
    return without_mptcp(value)


def valid_rules(document):
    """Validate semantics, hook/order and the complete dedicated table, not a label."""
    entries = document.get('nftables', [])
    tables = [x['table'] for x in entries if 'table' in x]
    if len(tables) != 1 or tables[0].get('family') != 'ip' or tables[0].get('name') != TABLE or tables[0].get('flags'):
        return False
    if any(set(x) - {'metainfo','table','chain','rule'} for x in entries):
        return False
    chains = [x['chain'] for x in entries if 'chain' in x]
    rules = [x['rule'] for x in entries if 'rule' in x]
    if len(chains) != 1 or len(rules) != 1:
        return False
    chain, rule = chains[0], rules[0]
    if any(chain.get(k) != v for k, v in {'family':'ip','table':TABLE,'name':'pre','type':'filter','hook':'prerouting','prio':-300,'policy':'accept'}.items()):
        return False
    if any(rule.get(k) != v for k, v in {'family':'ip','table':TABLE,'chain':'pre'}.items()):
        return False
    expr = rule.get('expr', [])
    return (len(expr) == 4 and expr[0] == {'match':{'op':'==','left':{'meta':{'key':'l4proto'}},'right':'tcp'}}
            and expr[1] == {'match':{'op':'>','left':{'payload':{'protocol':'ip','field':'hdrlength'}},'right':5}}
            and isinstance(expr[2].get('counter'), dict) and expr[3] == {'drop':None})


def inspect_rule():
    result = run('/usr/sbin/nft', '-j', 'list', 'table', 'ip', TABLE, check=False)
    if result.returncode:
        return False
    try:
        return valid_rules(json.loads(result.stdout))
    except (ValueError, TypeError, KeyError, AttributeError):
        return False


def apply():
    old = {}
    if STATUS.exists():
        safe_path(STATUS)
        with contextlib.suppress(ValueError):
            old = json.loads(STATUS.read_text())
    safe_path(DISABLED)
    disabled = DISABLED.exists()
    repaired = False
    present = inspect_rule()
    if not disabled and not present:
        # Syntax validation and the replacement each use a single atomic transaction.
        run('/usr/sbin/nft', '-c', '-f', '-', input=RULESET)
        run('/usr/sbin/nft', '-f', '-', input=RULESET)
        present = inspect_rule()
        if not present:
            raise RuntimeError('Kernel TCP mitigation could not be verified')
        repaired = True
    state = {'version':1, 'checked_at':int(time.time()), 'disabled':disabled,
             'filter_verified':present, 'repair_count':int(old.get('repair_count', 0)) + int(repaired)}
    if disabled and present:
        run('/usr/sbin/nft', 'delete', 'table', 'ip', TABLE)
        state['filter_verified'] = False
    atomic_write(STATUS, json.dumps(state) + '\n')
    print(json.dumps(state))


def install():
    # Validate all targets and collect existing environment values BEFORE mutation.
    units = ['lightningos-manager.service', 'lnd.service', 'lnd@default.service']
    targets = [HELPER, CONFIG / 'policy', STATUS,
               Path('/etc/systemd/system/lightningos-kernel-tcp.service'),
               Path('/etc/systemd/system/lightningos-kernel-tcp.timer')]
    for unit in units:
        targets += [CONFIG / (unit + '.env'), Path('/etc/systemd/system') / (unit + '.d') / '99-lightningos-kernel-tcp.conf']
    for path in targets:
        safe_path(path)
    safe_path(DISABLED)
    if DISABLED.exists():
        print('Kernel TCP mitigation explicitly disabled by the operator')
        return
    envs = {unit:unit_godebug(unit) for unit in units}
    if not Path('/usr/sbin/nft').exists():
        masked_here = False
        try:
            enabled = run('/usr/bin/systemctl','is-enabled','nftables.service',check=False).stdout.strip()
            if not enabled.startswith('masked'):
                run('/usr/bin/systemctl','mask','--runtime','nftables.service')
                masked_here = True
            run('/usr/bin/env','DEBIAN_FRONTEND=noninteractive','NEEDRESTART_MODE=l','/usr/bin/apt-get','update',timeout=300)
            run('/usr/bin/env','DEBIAN_FRONTEND=noninteractive','NEEDRESTART_MODE=l','/usr/bin/apt-get','install','-y','--no-install-recommends','nftables',timeout=300)
        finally:
            if masked_here:
                run('/usr/bin/systemctl','unmask','--runtime','nftables.service')
    for path in [CONFIG, RUNTIME, HELPER.parent]:
        directory(path)
    atomic_write(HELPER, Path(__file__).read_text(), 0o755)
    for unit, value in envs.items():
        escaped = value.replace('\\', '\\\\').replace('"', '\\"')
        atomic_write(CONFIG / (unit + '.env'), 'GODEBUG="' + escaped + '"\n', 0o600)
        atomic_write(Path('/etc/systemd/system') / (unit + '.d') / '99-lightningos-kernel-tcp.conf',
                     '[Service]\nEnvironmentFile=' + str(CONFIG / (unit + '.env')) + '\n')
    atomic_write(Path('/etc/systemd/system/lightningos-kernel-tcp.service'), SERVICE)
    atomic_write(Path('/etc/systemd/system/lightningos-kernel-tcp.timer'), TIMER)
    # /run is cleared at boot; create the root-owned status directory via tmpfiles.
    atomic_write(Path('/etc/tmpfiles.d/lightningos-kernel-tcp.conf'), 'd /run/lightningos-kernel-tcp 0755 root root -\n')
    run('/usr/bin/systemctl', 'daemon-reload')
    run('/usr/bin/systemctl', 'enable', 'lightningos-kernel-tcp.service', 'lightningos-kernel-tcp.timer')
    run('/usr/bin/systemctl', 'start', 'lightningos-kernel-tcp.service', 'lightningos-kernel-tcp.timer')
    # No LND/Bitcoin restart. Existing listeners are protected immediately by nft.
    print('IPv4 TCP filter verified; MPTCP defaults apply on the next service start')


def main():
    if os.geteuid() != 0 or sys.platform != 'linux':
        raise RuntimeError('Requires Linux root')
    if len(sys.argv) != 2 or sys.argv[1] not in ('install','apply','status','disable'):
        raise RuntimeError('Usage: lightningos-kernel-tcp install|apply|status|disable')
    directory(RUNTIME)
    os.chown(RUNTIME, 0, 0)
    os.chmod(RUNTIME, 0o755)  # Status must remain readable even with a root umask of 077.
    lock = RUNTIME / 'lock'
    safe_path(lock)
    with lock.open('a') as handle:
        fcntl.flock(handle, fcntl.LOCK_EX)
        command = sys.argv[1]
        if command == 'install':
            # The systemd oneshot needs this lock; release before starting it.
            fcntl.flock(handle, fcntl.LOCK_UN)
            install()
        elif command == 'disable':
            atomic_write(DISABLED, 'operator-disabled\n', 0o600)
            run('/usr/bin/systemctl','disable','--now','lightningos-kernel-tcp.timer')
            apply()
        elif command == 'status':
            print(json.dumps({'filter_verified':inspect_rule(),'disabled':DISABLED.exists()}))
        else:
            apply()


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('Kernel TCP mitigation failed; check prerequisites, root-owned paths and systemd configuration', file=sys.stderr)
        sys.exit(1)
