#!/usr/bin/python3 -I
"""Bounded, root-only LND onion recovery. Never print protocol replies or keys."""
import base64
import contextlib
import fcntl
import hashlib
import hmac
import ipaddress
import json
import os
from pathlib import Path
import re
import signal
import socket
import stat
import subprocess
import sys
import time

ROOT = Path('/var/lib/lightningos-onion')
HOOK = Path('/etc/systemd/system/lnd.service.d/50-lightningos-onion.conf')
HELPER = '/usr/local/libexec/lightningos-lnd-onion'
HOOK_TEXT = '[Service]\nExecStartPre=-+' + HELPER + ' release\n'
ID = re.compile(r'[a-z2-7]{56}')


class RecoveryError(Exception):
    pass


def run(*args):
    # stderr can contain configuration or authentication data; never forward it.
    p = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                       timeout=15, check=False)
    if p.returncode:
        raise RecoveryError('LND onion: local service query failed')
    return p.stdout.decode('utf-8')


def active_pid():
    values = dict(line.split('=', 1) for line in run(
        '/usr/bin/systemctl', 'show', 'lnd.service', '-p', 'MainPID',
        '-p', 'ActiveState').splitlines() if '=' in line)
    return int(values.get('MainPID', '0')) if values.get('ActiveState') == 'active' else 0


def endpoint(value, default=9735):
    value = value.strip()
    if value.startswith('['):
        match = re.fullmatch(r'\[([^]]+)\](?::([0-9]+))?', value)
        if not match:
            raise RecoveryError('LND onion: unsupported address')
        host, port = match[1], match[2] or str(default)
    elif value.count(':') == 1:
        host, port = value.rsplit(':', 1)
    elif ':' in value:
        host, port = value, str(default)
    else:
        host, port = value, str(default)
    port = int(port)
    if not 1 <= port <= 65535:
        raise RecoveryError('LND onion: invalid port')
    if host not in ('', 'localhost'):
        ipaddress.ip_address(host)
    return host, port


def config(text):
    values = {}
    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith(('#', ';', '[')):
            continue
        key, sep, value = line.partition('=')
        if sep:
            values.setdefault(key.strip().lower(), []).append(value.strip())
    return values


def last(cfg, key, default=''):
    return cfg.get(key, [default])[-1]


def enabled(cfg, key):
    value = last(cfg, key, 'false').lower()
    if value not in ('true', 'false', '1', '0'):
        raise RecoveryError('LND onion: unsupported boolean configuration')
    return value in ('true', '1')


def runtime_config(pid):
    # Respect the running daemon's lnddir/configfile and relevant CLI overrides.
    args = Path('/proc/%d/cmdline' % pid).read_bytes().decode().split('\0')[1:]
    overrides = {}
    i = 0
    while i < len(args):
        arg = args[i]
        i += 1
        if not arg.startswith('--'):
            continue
        key, sep, value = arg[2:].partition('=')
        if not sep:
            if i < len(args) and args[i] and not args[i].startswith('-'):
                value = args[i]
                i += 1
            else:
                value = 'true'
        overrides.setdefault(key.lower(), []).append(value)
    # LOS installs use an explicit --lnddir. Refuse to guess a foreign layout.
    directory = last(overrides, 'lnddir')
    if not directory or not os.path.isabs(directory):
        raise RecoveryError('LND onion: explicit absolute lnddir required')
    conf = last(overrides, 'configfile', str(Path(directory) / 'lnd.conf'))
    cfg = config(Path(conf).read_text())
    cfg.update(overrides)
    cfg['lnddir'] = [directory]
    return cfg


def mapping(cfg, virtual_port):
    # LND maps its virtual peer port to ALL listen ports, using TargetIPAddress
    # (127.0.0.1 by default), not the listen host. See server.createNewHiddenService.
    target = last(cfg, 'tor.targetipaddress') or '127.0.0.1'
    address = ipaddress.ip_address(target)
    target = '[' + str(address) + ']' if address.version == 6 else str(address)
    ports = sorted({endpoint(v)[1] for v in cfg.get('listen', [':9735'])})
    return ' '.join('Port=%d,%s:%d' % (virtual_port, target, p) for p in ports)


def advertised(cfg):
    args = ['/usr/local/bin/lncli', '--lnddir=' + last(cfg, 'lnddir')]
    for key in ('tlscertpath', 'adminmacaroonpath'):
        if last(cfg, key):
            args.append('--' + ('macaroonpath' if key == 'adminmacaroonpath' else key)
                        + '=' + last(cfg, key))
    if 'rpclisten' in cfg:
        host, port = endpoint(cfg['rpclisten'][0], 10009)
        if host in ('', '0.0.0.0', '::'):
            host = '127.0.0.1'
        args.append('--rpcserver=' + ('[%s]' % host if ':' in host else host) + ':' + str(port))
    info = json.loads(run(*args, 'getinfo'))
    onions = set()
    for uri in info.get('uris', []):
        match = re.fullmatch(r'[0-9a-fA-F]{66}@([a-z2-7]{56})\.onion:([0-9]+)', uri)
        if match:
            onions.add((match[1], int(match[2])))
    if len(onions) != 1:
        raise RecoveryError('LND onion: a unique advertised v3 onion is required')
    service, port = onions.pop()
    if not 1 <= port <= 65535:
        raise RecoveryError('LND onion: invalid advertised port')
    return service, port


def read_control_cookie(path):
    cookie_path = Path(path).resolve()
    if str(cookie_path) not in ('/run/tor/control.authcookie', '/var/lib/tor/control_auth_cookie'):
        raise RecoveryError('LND onion: unsupported Tor cookie location')
    cookie = cookie_path.read_bytes()
    if len(cookie) != 32:
        raise RecoveryError('LND onion: invalid Tor cookie')
    return cookie


class Control:
    def __init__(self, address):
        host, port = endpoint(address, 9051)
        if host == 'localhost':
            host = '127.0.0.1'
        if not ipaddress.ip_address(host).is_loopback:
            raise RecoveryError('LND onion: Tor control must be local')
        self.sock = socket.create_connection((host, port), timeout=5)
        self.stream = self.sock.makefile('rb')

    def close(self):
        self.stream.close()
        self.sock.close()

    def command(self, command):
        if '\r' in command or '\n' in command:
            raise RecoveryError('LND onion: invalid control command')
        self.sock.sendall((command + '\r\n').encode('ascii'))
        lines = []
        total = 0
        while True:
            raw = self.stream.readline(16385)
            total += len(raw)
            if not raw.endswith(b'\r\n') or len(raw) > 16384 or total > 1024 * 1024:
                raise RecoveryError('LND onion: invalid control response')
            line = raw.decode('ascii').rstrip('\r\n')
            if len(line) < 4 or not line[:3].isdigit():
                raise RecoveryError('LND onion: invalid control response')
            code, separator = int(line[:3]), line[3]
            lines.append(line[4:])
            if separator == '+':
                while True:
                    raw = self.stream.readline(16385)
                    total += len(raw)
                    if not raw.endswith(b'\r\n') or len(raw) > 16384 or total > 1024 * 1024:
                        raise RecoveryError('LND onion: invalid control response')
                    if raw == b'.\r\n':
                        break
                    # Descriptor content is deliberately discarded.
            elif separator == ' ':
                return code, lines
            elif separator != '-':
                raise RecoveryError('LND onion: invalid control response')

    def authenticate(self):
        code, lines = self.command('PROTOCOLINFO 1')
        match = re.search(r'COOKIEFILE="([^"\\]+)"', ' '.join(lines))
        if code != 250 or not match or 'SAFECOOKIE' not in ' '.join(lines):
            raise RecoveryError('LND onion: Tor cookie authentication unavailable')
        cookie = read_control_cookie(match[1])
        nonce = os.urandom(32)
        code, lines = self.command('AUTHCHALLENGE SAFECOOKIE ' + nonce.hex())
        challenge = re.fullmatch(r'AUTHCHALLENGE SERVERHASH=([0-9A-Fa-f]{64}) SERVERNONCE=([0-9A-Fa-f]{64})', ' '.join(lines))
        if code != 250 or not challenge:
            raise RecoveryError('LND onion: invalid Tor authentication challenge')
        material = cookie + nonce + bytes.fromhex(challenge[2])
        expected = hmac.new(b'Tor safe cookie authentication server-to-controller hash', material, hashlib.sha256).digest()
        if not hmac.compare_digest(bytes.fromhex(challenge[1]), expected):
            raise RecoveryError('LND onion: Tor server authentication failed')
        response = hmac.new(b'Tor safe cookie authentication controller-to-server hash', material, hashlib.sha256).hexdigest()
        if self.command('AUTHENTICATE ' + response)[0] != 250:
            raise RecoveryError('LND onion: Tor cookie authentication failed')


def save(record):
    temporary = ROOT / 'pending.tmp'
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as out:
        json.dump(record, out)
        out.flush()
        os.fsync(out.fileno())
    os.replace(temporary, ROOT / 'pending.json')
    fd = os.open(ROOT, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def release():
    path = ROOT / 'pending.json'
    if not path.exists():
        return
    record = json.loads(path.read_text())
    service = record['service']
    if not ID.fullmatch(service):
        raise RecoveryError('LND onion: invalid release record')
    with contextlib.closing(Control(record['control'])) as control:
        control.authenticate()
        code, _ = control.command('DEL_ONION ' + service)
        if code not in (250, 552):  # 552 = already absent; all other failures retain retry state.
            raise RecoveryError('LND onion: release deferred; Tor control unavailable')
    path.unlink(missing_ok=True)


def recover():
    pid = active_pid()
    if not pid:
        return 'LND onion: skipped (LND is not active)'
    cfg = runtime_config(pid)
    if not enabled(cfg, 'tor.active') or not enabled(cfg, 'tor.v3'):
        return 'LND onion: skipped (v3 onion is disabled)'
    if enabled(cfg, 'tor.encryptkey'):
        raise RecoveryError('LND onion: encrypted key; onion recovery requires an operator-scheduled LND restart. LND was not restarted.')
    if enabled(cfg, 'nolisten'):
        raise RecoveryError('LND onion: inbound listening is disabled')
    service, port = advertised(cfg)
    control_address = last(cfg, 'tor.control', '127.0.0.1:9051')
    with contextlib.closing(Control(control_address)) as control:
        control.authenticate()
        code, reply = control.command('GETINFO hs/service/desc/id/' + service)
        if code == 250:
            return 'LND onion: already published ' + service
        if code != 552 and not (code == 551 and reply == ['Not found in cache']):
            raise RecoveryError('LND onion: publication state could not be verified')
        key_path = last(cfg, 'tor.privatekeypath', str(Path(last(cfg, 'lnddir')) / 'v3_onion_private_key'))
        if not os.path.isabs(key_path):
            raise RecoveryError('LND onion: absolute private key path required')
        with open(key_path, 'rb') as key_file:
            raw = key_file.read(256)
        key = raw.decode('ascii').strip()
        if not re.fullmatch(r'ED25519-V3:[A-Za-z0-9+/]{86}==', key) or len(base64.b64decode(key[11:], validate=True)) != 64:
            raise RecoveryError('LND onion: unsupported or encrypted onion key; schedule an LND restart')
        # Never register a detached service without the loaded release hook.
        if HOOK.read_text() != HOOK_TEXT or HELPER + ' release' not in run(
                '/usr/bin/systemctl', 'show', 'lnd.service', '-p', 'ExecStartPre', '--value'):
            raise RecoveryError('LND onion: release hook is not loaded; recovery refused')
        if active_pid() != pid:
            raise RecoveryError('LND onion: LND changed during recovery; retry later')
        # Write ahead of ADD_ONION so interruption/connection loss is recoverable.
        record = {'service': service, 'control': control_address}
        pending = ROOT / 'pending.json'
        had_record = pending.exists()
        if had_record and json.loads(pending.read_text()) != record:
            raise RecoveryError('LND onion: previous recovery requires release before changing identity')
        save(record)
        code, lines = control.command('ADD_ONION ' + key + ' Flags=Detach ' + mapping(cfg, port))
        if code == 550 and lines == ['Onion address collision']:
            # A service can exist before its descriptor is available. ADD_ONION
            # does not replace it. Never claim ownership of someone else's onion.
            if not had_record:
                pending.unlink(missing_ok=True)
            return 'LND onion: already registered ' + service
        if code != 250:
            raise RecoveryError('LND onion: registration failed; no LND restart was performed')
        actual = next((line[10:] for line in lines if line.startswith('ServiceID=')), '')
        if actual != service:
            if ID.fullmatch(actual):
                save({'service': actual, 'control': control_address})
                control.command('DEL_ONION ' + actual)
            raise RecoveryError('LND onion: onion key does not match the advertised address')
        if active_pid() != pid:
            control.command('DEL_ONION ' + service)
            raise RecoveryError('LND onion: LND changed during recovery; detached onion released')
    return 'LND onion: recovered ' + service


def main():
    if os.geteuid() != 0 or len(sys.argv) != 2 or sys.argv[1] not in ('recover', 'release'):
        return 1
    mode = sys.argv[1]
    # A release hook may wait up to 30s on the lock. Recovery must terminate
    # sooner, so an ignored release error can never race a late ADD_ONION.
    def timed_out(_signum, _frame):
        raise RecoveryError('LND onion: recovery timed out')
    signal.signal(signal.SIGALRM, timed_out)
    signal.alarm(25 if mode == 'recover' else 45)
    try:
        ROOT.mkdir(mode=0o700, exist_ok=True)
        info = ROOT.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077:
            raise RecoveryError('LND onion: unsafe recovery state directory')
        fd = os.open(ROOT / 'lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w'):
            deadline = time.monotonic() + 30
            while True:
                try:
                    fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    break
                except BlockingIOError:
                    if time.monotonic() >= deadline:
                        raise RecoveryError('LND onion: recovery lock timed out')
                    time.sleep(0.1)
            if mode == 'release':
                release()
            else:
                print(recover())
        return 0
    except Exception as exc:
        # Only our constant diagnostics are safe. Library exceptions may include
        # private input, protocol payloads, paths, or subprocess output.
        message = str(exc) if isinstance(exc, RecoveryError) else 'LND onion: recovery unavailable; inspect local configuration'
        print('[WARN] ' + message)
        return 0 if mode == 'release' else 1


if __name__ == '__main__':
    sys.exit(main())
