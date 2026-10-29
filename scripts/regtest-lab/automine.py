#!/usr/bin/env python3
"""Automatic mining for the dedicated LOS regtest host only (run with sudo)."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile

import control
import safety

CONFIG = Path('/etc/los-regtest')
UNITS = Path('/etc/systemd/system')
TIMER = 'los-regtest-automine.timer'
SERVICE = 'los-regtest-automine.service'


def systemctl(*args):
    subprocess.run(['systemctl', *args], check=True)


def local_guard():
    # Also rejects production paths/services, symlinks and unowned lab trees.
    safety.check()
    if control.socket.gethostname() != 'los-regtest' or not safety.MARKER.is_file():
        raise RuntimeError('Dedicated initialized regtest host required')


def interval_seconds(value):
    seconds = int(value)
    if not 10 <= seconds <= 3600:
        raise ValueError('Interval must be between 10 and 3600 seconds')
    return seconds


def interval():
    path = CONFIG / 'automine-interval'
    return interval_seconds(path.read_text().strip()) if path.exists() else 60


def write_atomic(path, text):
    # Same-directory replacement keeps an interrupted write from truncating a unit.
    fd, temporary = tempfile.mkstemp(dir=path.parent, prefix='.' + path.name + '-')
    try:
        with os.fdopen(fd, 'w') as stream:
            stream.write(text)
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(temporary, 0o644)
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


def install():
    local_guard()
    control.checked_lab()
    if not (CONFIG / 'miner-address').is_file():
        raise RuntimeError('Initialize the regtest miner wallet first')
    seconds = interval()
    write_atomic(CONFIG / 'automine-interval', str(seconds) + '\n')
    write_atomic(UNITS / SERVICE, '''[Unit]
Description=LOS regtest only: mine one fictitious block
After=los-regtest-bitcoin.service
ConditionPathExists=/etc/los-regtest/LAB_ONLY

[Service]
Type=oneshot
ExecStart=/usr/bin/python3 /opt/los-regtest/automine.py tick
TimeoutStartSec=75
UMask=0077
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
''')
    write_atomic(UNITS / TIMER, f'''[Unit]
Description=LOS regtest only: automatic block every {seconds} seconds

[Timer]
OnActiveSec={seconds}s
OnUnitInactiveSec={seconds}s
AccuracySec=1s
Unit={SERVICE}

[Install]
WantedBy=timers.target
''')
    systemctl('daemon-reload')
    if (CONFIG / 'automine-paused').exists():
        systemctl('disable', '--now', TIMER)
        systemctl('stop', SERVICE)
    else:
        systemctl('enable', TIMER)
        systemctl('restart', TIMER)
    print('Regtest automatic mining configured: interval=' + str(seconds) + 's; paused=' + str((CONFIG / 'automine-paused').exists()))


def tick():
    local_guard()
    if (CONFIG / 'automine-paused').exists():
        return
    # A stale or redirected RPC must never generate a block on another network.
    control.checked_lab()
    control.mine(1)
    print('Mined one local regtest block (coins have no value).')


def pause():
    local_guard()
    write_atomic(CONFIG / 'automine-paused', 'paused\n')
    systemctl('disable', '--now', TIMER)
    systemctl('stop', SERVICE)
    print('Regtest automatic mining paused; manual mining remains available.')


def resume():
    local_guard()
    control.checked_lab()
    (CONFIG / 'automine-paused').unlink(missing_ok=True)
    install()


def status():
    local_guard()
    print('Interval: ' + str(interval()) + ' seconds; paused: ' + str((CONFIG / 'automine-paused').exists()))
    subprocess.run(['systemctl', 'show', TIMER, '-p', 'LoadState', '-p', 'ActiveState', '-p', 'UnitFileState', '-p', 'NextElapseUSecMonotonic'], check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['install', 'pause', 'resume', 'status', 'interval', 'tick'])
    parser.add_argument('seconds', nargs='?', type=interval_seconds)
    args = parser.parse_args()
    if (args.action == 'interval') != (args.seconds is not None):
        parser.error('Only interval requires a seconds argument')
    if args.action == 'interval':
        local_guard()
        control.checked_lab()
        write_atomic(CONFIG / 'automine-interval', str(args.seconds) + '\n')
        install()
    else:
        globals()[args.action]()


if __name__ == '__main__':
    try:
        main()
    except (OSError, RuntimeError, ValueError, subprocess.CalledProcessError):
        raise SystemExit('Regtest automatic mining failed; check the lab state and timer service.')
