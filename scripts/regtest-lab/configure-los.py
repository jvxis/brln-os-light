#!/usr/bin/env python3
"""Configure two restricted LOS processes using explicitly supplied lab artifacts."""
import argparse
import hashlib
import json
import os
import pwd
import re
import secrets
import shutil
import subprocess
import tarfile
from pathlib import Path

from control import checked_lab, wait_info


def run(*args, input=None):
    subprocess.run(args, input=input, text=True, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)


def add_lab_notice(html):
    if 'id="los-regtest-notice"' in html:
        raise RuntimeError('Regtest notice is already attached')
    notice = '''<details id="los-regtest-notice" style="position:fixed;z-index:2147483647;bottom:0;left:0;right:0;max-height:75vh;overflow:auto;background:#ffce00;color:#171717;text-align:center;font:bold 13px sans-serif">
<summary aria-controls="los-regtest-support" style="cursor:pointer;padding:6px">REGTEST — MOEDAS SEM VALOR — LABORATÓRIO DESCARTÁVEL · O que funciona / Supported features</summary>
<div id="los-regtest-support" style="background:#fff6c9;text-align:left;padding:10px 14px;font:13px/1.45 sans-serif">
<p><strong>Validado neste laboratório / Validated in this lab</strong></p>
<ul>
<li>Login, saldos on-chain/Lightning, endereços de recebimento e consulta de canais.<br>Login, on-chain/Lightning balances, receiving addresses and channel reads.</li>
<li>Invoices e pagamentos Lightning entre Alice e Bob; envios on-chain e confirmação por mineração manual.<br>Invoices and Lightning payments between Alice and Bob; on-chain sends and confirmation through manual mining.</li>
<li>Confirmação de senha antes dos pagamentos e envios.<br>Password confirmation before payments and sends.</li>
</ul>
<p><strong>Fora de escopo / Not supported</strong></p>
<ul>
<li>Configuração do LND pela interface: nome/alias, cor, rede, manutenção e reinícios.<br>LND configuration through the UI: name/alias, color, networking, maintenance and restarts.</li>
<li>Upgrades de LND/LOS pela interface, Apps da loja e integrações externas.<br>LND/LOS upgrades through the UI, App Store and external integrations.</li>
</ul>
<p>A interface mantém menus de produção. Itens fora de escopo podem aparecer e apresentar erro; consultas ao GitHub podem expirar porque os Managers só acessam localhost. Outras funcionalidades não têm validação garantida neste perfil.<br>The UI retains production menus. Unsupported items can remain visible and show errors; GitHub checks can time out because Managers can only reach localhost. Other features are not guaranteed validated in this profile.</p>
<p>Use somente moedas regtest, sem valor. A instalação de produção continua mainnet-only.<br>Use only worthless regtest coins. Production installation remains mainnet-only.</p>
</div></details>'''
    html, count = re.subn(r'(<body\b[^>]*>)', lambda match: match.group(1) + notice, html, count=1)
    if count != 1:
        raise RuntimeError('Could not attach the mandatory regtest UI notice')
    return html.replace('<title>', '<title>REGTEST | ')


def configure(args):
    checked_lab()
    for node in ['alice', 'bob']:
        wait_info(node)
    if Path('/etc/lightningos/config.yaml').exists():
        raise RuntimeError('Refusing existing production LOS configuration')
    binary = Path(args.manager)
    if hashlib.sha256(binary.read_bytes()).hexdigest() != args.manager_sha256:
        raise RuntimeError('Manager artifact digest mismatch')
    if len(args.source_commit) != 40 or any(ch not in '0123456789abcdef' for ch in args.source_commit):
        raise RuntimeError('Full source commit required')
    if hashlib.sha256(Path(args.ui_archive).read_bytes()).hexdigest() != args.ui_sha256:
        raise RuntimeError('UI artifact digest mismatch')
    destination = Path('/opt/los-regtest')
    installed = destination / 'bin/lightningos-manager'
    if not installed.exists() or hashlib.sha256(installed.read_bytes()).hexdigest() != args.manager_sha256:
        for node in ['alice', 'bob']:
            active = subprocess.run(['systemctl', 'is-active', '--quiet', 'los-regtest-ui-' + node]).returncode == 0
            if active:
                raise RuntimeError('Stop the two lab UI units before changing their artifact')
        staged = destination / 'bin/lightningos-manager.new'
        shutil.copyfile(binary, staged)
        staged.chmod(0o755)
        staged.replace(installed)
    ui = destination / 'ui'
    ui.mkdir(mode=0o755, exist_ok=True)
    with tarfile.open(args.ui_archive) as archive:
        archive.extractall(ui, filter='data')
    for folder, dirs, files in os.walk(ui):
        Path(folder).chmod(0o755)
        for name in dirs + files:
            file = Path(folder) / name
            if file.is_symlink():
                raise RuntimeError('Symlink in UI artifact')
            file.chmod(0o755 if file.is_dir() else 0o644)
    index = ui / 'index.html'
    index.write_text(add_lab_notice(index.read_text()))
    ca = Path('/etc/los-regtest/ca.crt')
    if not ca.exists():
        run('openssl', 'req', '-x509', '-newkey', 'rsa:3072', '-nodes', '-days', '3650',
            '-subj', '/CN=LOS REGTEST Lab', '-keyout', '/etc/los-regtest/ca.key', '-out', str(ca))
        Path('/etc/los-regtest/ca.key').chmod(0o600)
        ca.chmod(0o644)
    account = pwd.getpwnam('los-regtest')
    Path('/etc/lightningos').mkdir(mode=0o755, exist_ok=True)
    state_target = Path('/var/lib/lightningos')
    if state_target.exists() and any(state_target.iterdir()):
        raise RuntimeError('Refusing existing host LOS state')
    state_target.mkdir(mode=0o755, exist_ok=True)
    for node, grpc, port in [('alice', 10009, 8443), ('bob', 11009, 8543)]:
        folder = Path('/etc/los-regtest') / node / 'lightningos'
        folder.mkdir(parents=True, mode=0o700, exist_ok=True)
        state = Path('/var/lib/los-regtest') / node / 'los-state'
        state.mkdir(mode=0o700, exist_ok=True)
        os.chown(state, account.pw_uid, account.pw_gid)
        credential = folder.parent / 'database-password'
        if not credential.exists():
            credential.write_text(secrets.token_hex(32))
            credential.chmod(0o600)
        password = credential.read_text()
        role = 'los_regtest_' + node
        # Generated hex password and fixed identifiers; SQL travels over stdin.
        sql = f"select 1 from pg_roles where rolname='{role}';"
        exists = subprocess.check_output(['runuser', '-u', 'postgres', '--', 'psql', '-At', '-c', sql], text=True).strip()
        if not exists:
            run('runuser', '-u', 'postgres', '--', 'psql', '-v', 'ON_ERROR_STOP=1', input=f"CREATE ROLE {role} LOGIN PASSWORD '{password}';\n")
        exists = subprocess.check_output(['runuser', '-u', 'postgres', '--', 'psql', '-At', '-c', f"select 1 from pg_database where datname='{role}';"], text=True).strip()
        if not exists:
            run('runuser', '-u', 'postgres', '--', 'createdb', '-O', role, role)
        tlskey = folder / 'server.key'
        cert = folder / 'server.crt'
        if not cert.exists():
            run('openssl', 'req', '-new', '-newkey', 'rsa:2048', '-nodes', '-subj', '/CN=localhost',
                '-keyout', str(tlskey), '-out', str(folder / 'server.csr'))
            ext = folder / 'certificate.ext'
            ext.write_text('subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n')
            run('openssl', 'x509', '-req', '-in', str(folder / 'server.csr'), '-CA', str(ca), '-CAkey', '/etc/los-regtest/ca.key',
                '-CAcreateserial', '-out', str(cert), '-days', '3650', '-extfile', str(ext))
        config = {'server': {'host': '127.0.0.1', 'port': port, 'tls_cert': '/etc/lightningos/server.crt', 'tls_key': '/etc/lightningos/server.key'},
                  'lnd': {'grpc_host': '127.0.0.1:' + str(grpc), 'tls_cert_path': f'/var/lib/los-regtest/{node}/tls.cert',
                          'admin_macaroon_path': f'/var/lib/los-regtest/{node}/data/chain/bitcoin/regtest/admin.macaroon'},
                  'bitcoin_remote': {'rpchost': '127.0.0.1:18443', 'zmq_rawblock': 'tcp://127.0.0.1:28332', 'zmq_rawtx': 'tcp://127.0.0.1:28333'},
                  'postgres': {'db_name': role}, 'ui': {'static_dir': str(ui)}, 'features': {'enable_login': True, 'enable_balanced_open': True},
                  'privileged': {'mode': 'disabled'}}
        if not (folder / 'config.yaml').exists():
            (folder / 'config.yaml').write_text(json.dumps(config, indent=2))
        env = folder / 'secrets.env'
        # Preserve auth material added by setup/change-password on repeated runs.
        if not env.exists():
            rpc_password = Path('/etc/los-regtest/bitcoin-rpc-password').read_text().strip()
            env.write_text(f'NOTIFICATIONS_PG_DSN=postgres://{role}:{password}@127.0.0.1:5432/{role}?sslmode=disable\nBITCOIN_RPC_USER=los-regtest\nBITCOIN_RPC_PASS={rpc_password}\n')
        os.chown(folder, account.pw_uid, account.pw_gid)
        for file in folder.iterdir():
            os.chown(file, account.pw_uid, account.pw_gid)
            file.chmod(0o600)
        unit = f'''[Unit]
Description=LOS REGTEST ONLY - {node} UI
Requires=los-regtest-{node}.service postgresql.service
After=los-regtest-{node}.service postgresql.service
[Service]
User=los-regtest
Group=los-regtest
EnvironmentFile={env}
BindPaths={folder}:/etc/lightningos
BindPaths={state}:/var/lib/lightningos
ExecStartPre=/usr/bin/python3 /opt/los-regtest/control.py guard {node}
ExecStart=/opt/los-regtest/bin/lightningos-manager --config=/etc/lightningos/config.yaml
Restart=on-failure
RestartSec=10
UMask=0077
NoNewPrivileges=true
ProtectHome=true
PrivateTmp=true
IPAddressDeny=any
IPAddressAllow=localhost
[Install]
WantedBy=multi-user.target
'''
        Path('/etc/systemd/system/los-regtest-ui-' + node + '.service').write_text(unit)
    (destination / 'source.json').write_text(json.dumps({'source_commit': args.source_commit, 'manager_sha256': args.manager_sha256,
                                                       'ui_sha256': args.ui_sha256, 'network': 'regtest'}, indent=2))
    run('systemctl', 'daemon-reload')
    run('systemctl', 'enable', '--now', 'los-regtest-ui-alice', 'los-regtest-ui-bob')
    print('Configured two local HTTPS LOS instances. Login setup remains required; production installers unchanged.')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for option in ['manager', 'manager-sha256', 'ui-archive', 'ui-sha256', 'source-commit']:
        parser.add_argument('--' + option, required=True)
    configure(parser.parse_args())
