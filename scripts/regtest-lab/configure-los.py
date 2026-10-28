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
    from html import escape

    if 'id="los-regtest-notice"' in html or 'id="los-regtest-styles"' in html:
        raise RuntimeError('Regtest notice is already attached')
    css = '''<style id="los-regtest-styles">
#los-regtest-notice{position:fixed;z-index:2147483647;bottom:0;left:0;right:0;background:#091720;color:#e4edf3;border-top:1px solid #bc9035;box-shadow:0 -12px 40px #0006;font:14px/1.5 system-ui,sans-serif;color-scheme:dark}
#los-regtest-notice>summary{cursor:pointer;list-style-position:inside;text-align:center;padding:11px 18px;font-size:13px;line-height:20px;color:#dbe6ee;background:#0b1b25}
#los-regtest-notice>summary:focus-visible{outline:2px solid #f5c451;outline-offset:-3px}
#los-regtest-notice .rt-brand{font-weight:800;color:#f5c451;letter-spacing:.08em;margin-right:10px}
#los-regtest-notice .rt-scroll{max-height:min(70vh,720px);overflow:auto;overscroll-behavior:contain;scrollbar-color:#405867 #0b1b25}
#los-regtest-notice .rt-width{max-width:1040px;margin:0 auto;padding:0 24px}
#los-regtest-notice .rt-toolbar{position:sticky;top:0;z-index:2;background:#091720;border-bottom:1px solid #253a47}
#los-regtest-notice .rt-toolbar-inner{display:flex;align-items:center;justify-content:space-between;gap:16px;padding-top:12px;padding-bottom:12px}
#los-regtest-notice .rt-kicker{font-size:11px;font-weight:750;letter-spacing:.12em;color:#8ca7b8}
#los-regtest-notice .rt-switch{display:flex;gap:4px;border:1px solid #344b59;border-radius:10px;padding:3px;margin:0;min-width:0}
#los-regtest-notice .rt-switch label{cursor:pointer;padding:6px 12px;border-radius:7px;color:#b8cbd7;font-size:13px;font-weight:650;line-height:20px}
#los-regtest-notice .rt-switch label:hover{background:#203745}
#los-regtest-notice .rt-sr{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip-path:inset(50%);white-space:nowrap;border:0}
#los-regtest-notice .rt-switch input:checked+label{background:#f5c451;color:#221a08}
#los-regtest-notice .rt-switch input:focus-visible+label{outline:2px solid #fff;outline-offset:2px}
#los-regtest-notice .rt-panel{padding-top:22px;padding-bottom:26px}
#los-regtest-notice .rt-title{font-size:23px;line-height:1.25;font-weight:750;margin:0 0 10px;color:#f0f6fa}
#los-regtest-notice .rt-copy{font-size:14px;line-height:1.6;color:#a8bdcb;margin:0 0 18px;overflow-wrap:anywhere}
#los-regtest-notice .rt-warning{display:inline-block;font-size:11px;font-weight:750;letter-spacing:.07em;color:#f5c451;margin-bottom:10px}
#los-regtest-notice .rt-legend{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;margin:18px 0 22px}
#los-regtest-notice .rt-legend-item{border:1px solid #29404e;border-radius:10px;background:#10232f;padding:12px}
#los-regtest-notice .rt-legend-item p{font-size:12px;line-height:1.5;color:#a8bdcb;margin:7px 0 0}
#los-regtest-notice .rt-badge{display:inline-block;font-size:12px;line-height:18px;font-weight:700;padding:3px 9px;border:1px solid;border-radius:999px;white-space:nowrap}
#los-regtest-notice .rt-validated{color:#8be0b4;background:#11392e;border-color:#28654e}
#los-regtest-notice .rt-unvalidated{color:#f2cc76;background:#392e15;border-color:#6b5424}
#los-regtest-notice .rt-unsupported{color:#bac7d1;background:#263440;border-color:#475b69}
#los-regtest-notice .rt-table-wrap{border:1px solid #29404e;border-radius:12px;overflow:hidden;margin-bottom:18px}
#los-regtest-notice .rt-table{width:100%;border-collapse:collapse;table-layout:fixed;text-align:left;font-size:14px;line-height:1.5}
#los-regtest-notice .rt-table th,#los-regtest-notice .rt-table td{padding:11px 14px;border-bottom:1px solid #233946;vertical-align:middle;text-align:left;overflow-wrap:anywhere}
#los-regtest-notice .rt-table thead th{background:#17303e;color:#adc3d0;font-size:12px;font-weight:750}
#los-regtest-notice .rt-table th:last-child,#los-regtest-notice .rt-table td:last-child{width:155px}
#los-regtest-notice .rt-table tbody tr:nth-child(even){background:#0e202c}
#los-regtest-notice .rt-table tbody tr:last-child td{border-bottom:0}
#los-regtest-notice .rt-operation{display:block;color:#e2edf4;font-weight:600}
#los-regtest-notice .rt-note{display:block;color:#94aebb;font-size:12px;line-height:1.45;margin-top:3px}
#los-regtest-notice .rt-tip{border-left:3px solid #598aa1;background:#102733;border-radius:0 8px 8px 0;padding:12px 14px;margin-top:14px}
#los-regtest-notice .rt-tip strong{display:block;font-weight:700;color:#cde2ef;margin-bottom:4px}
#los-regtest-notice .rt-tip p{margin:0;font-size:13px;line-height:1.6;color:#a8bdcb}
#los-regtest-notice #los-regtest-panel-en,#los-regtest-notice .rt-summary-en{display:none}
#los-regtest-notice:has(#los-regtest-lang-en:checked) #los-regtest-panel-pt,#los-regtest-notice:has(#los-regtest-lang-en:checked) .rt-summary-pt{display:none}
#los-regtest-notice:has(#los-regtest-lang-en:checked) #los-regtest-panel-en{display:block}
#los-regtest-notice:has(#los-regtest-lang-en:checked) .rt-summary-en{display:inline}
@media(max-width:600px){#los-regtest-notice .rt-width{padding-left:14px;padding-right:14px}#los-regtest-notice .rt-title{font-size:20px}#los-regtest-notice .rt-legend{grid-template-columns:1fr;gap:7px}#los-regtest-notice .rt-legend-item{padding:10px}#los-regtest-notice .rt-table th,#los-regtest-notice .rt-table td{padding:10px 8px}#los-regtest-notice .rt-table th:last-child,#los-regtest-notice .rt-table td:last-child{width:112px}#los-regtest-notice .rt-badge{font-size:11px;padding:3px 7px}}
</style>'''
    languages = {
        'pt': {
            'lang': 'pt-BR', 'title': 'Guia do laboratório regtest', 'warning': 'MOEDAS SEM VALOR',
            'intro': 'Consulte o estado de cada operação antes de usar os menus. Este guia descreve o que foi testado na instalação atual.',
            'headers': ('Operação', 'Estado'),
            'statuses': {'validated': 'Validado', 'unvalidated': 'Não validado', 'unsupported': 'Fora de escopo'},
            'legend': ('Testado com sucesso neste laboratório.', 'O fluxo completo ainda não foi testado. Isso não significa que ele falha.', 'Depende de recursos de produção que este perfil não oferece.'),
            'rows': [
                ('wallet', 'validated', 'Carteira e acesso', 'Login, saldos on-chain/Lightning e endereços de recebimento.'),
                ('payments', 'validated', 'Pagamentos Lightning', 'Invoices e pagamentos entre Alice e Bob, com confirmação de senha.'),
                ('onchain', 'validated', 'Pagamentos on-chain', 'Depósitos, envios e confirmações por mineração manual.'),
                ('channel-read', 'validated', 'Consultar canais e saldos', ''),
                ('installer-channel', 'validated', 'Canal inicial do laboratório', 'Criado pelo preparador via lncli; não valida abertura pela interface.'),
                ('channel-open', 'unvalidated', 'Abrir canal simples', 'Pela interface do LOS.'),
                ('channel-open-batch', 'unvalidated', 'Abrir canais em lote', 'Pela interface do LOS.'),
                ('balanced-open', 'unvalidated', 'Abrir canal balanceado', 'Recurso habilitado; fluxo completo ainda não validado.'),
                ('channel-close', 'unvalidated', 'Fechar canal cooperativamente', ''),
                ('channel-force-close', 'unvalidated', 'Forçar fechamento de canal', ''),
                ('channel-automation', 'unvalidated', 'Fees, rebalance e automações', ''),
                ('lnd-config', 'unsupported', 'Configuração e manutenção do LND', 'Nome/alias, cor, rede e controles de reinício.'),
                ('upgrades', 'unsupported', 'Upgrades de LND e LOS pela interface', ''),
                ('external', 'unsupported', 'Apps da loja e integrações externas', ''),
            ],
            'tips': [
                ('Mineração é manual', 'Após um período sem blocos, o LND pode indicar falta de sincronização. Com a mineradora carregada, minere um novo bloco local antes de testar pagamentos.'),
                ('Menus de produção podem continuar visíveis', 'Os Managers só acessam localhost e não usam o broker de produção. Consultas ao GitHub podem expirar. A aplicação de produção permanece inalterada.'),
            ],
        },
        'en': {
            'lang': 'en', 'title': 'Regtest laboratory guide', 'warning': 'COINS HAVE NO VALUE',
            'intro': 'Check each operation before using the menus. This guide describes what has been tested in the current installation.',
            'headers': ('Operation', 'Status'),
            'statuses': {'validated': 'Validated', 'unvalidated': 'Not validated', 'unsupported': 'Not supported'},
            'legend': ('Tested successfully in this laboratory.', 'The complete workflow has not been tested. This does not mean it fails.', 'Requires production capabilities that this profile does not provide.'),
            'rows': [
                ('wallet', 'validated', 'Wallet and access', 'Login, on-chain/Lightning balances and receiving addresses.'),
                ('payments', 'validated', 'Lightning payments', 'Invoices and payments between Alice and Bob, with password confirmation.'),
                ('onchain', 'validated', 'On-chain payments', 'Deposits, sends and confirmations through manual mining.'),
                ('channel-read', 'validated', 'Read channels and balances', ''),
                ('installer-channel', 'validated', 'Initial laboratory channel', 'Created by setup through lncli; this does not validate UI channel opening.'),
                ('channel-open', 'unvalidated', 'Open a single channel', 'Through the LOS interface.'),
                ('channel-open-batch', 'unvalidated', 'Batch-open channels', 'Through the LOS interface.'),
                ('balanced-open', 'unvalidated', 'Balanced channel opening', 'Feature enabled; complete workflow not yet validated.'),
                ('channel-close', 'unvalidated', 'Cooperative channel close', ''),
                ('channel-force-close', 'unvalidated', 'Force-close a channel', ''),
                ('channel-automation', 'unvalidated', 'Fees, rebalance and automations', ''),
                ('lnd-config', 'unsupported', 'LND configuration and maintenance', 'Name/alias, color, networking and restart controls.'),
                ('upgrades', 'unsupported', 'LND and LOS upgrades through the UI', ''),
                ('external', 'unsupported', 'App Store and external integrations', ''),
            ],
            'tips': [
                ('Mining is manual', 'After an idle period without blocks, LND can report that it is not synced. With the miner wallet loaded, mine a new local block before testing payments.'),
                ('Production menus can remain visible', 'Managers can only reach localhost and do not use the production broker. GitHub checks can time out. The production application remains unchanged.'),
            ],
        },
    }
    panels = []
    for language, text in languages.items():
        legend = ''.join(f'<div class="rt-legend-item"><span class="rt-badge rt-{state}">{escape(text["statuses"][state])}</span><p>{escape(description)}</p></div>' for state, description in zip(('validated', 'unvalidated', 'unsupported'), text['legend']))
        rows = ''.join(f'<tr data-lab-operation="{operation}" data-lab-status="{state}"><td><span class="rt-operation">{escape(label)}</span>' + (f'<span class="rt-note">{escape(note)}</span>' if note else '') + f'</td><td><span class="rt-badge rt-{state}">{escape(text["statuses"][state])}</span></td></tr>' for operation, state, label, note in text['rows'])
        tips = ''.join(f'<aside class="rt-tip"><strong>{escape(title)}</strong><p>{escape(copy)}</p></aside>' for title, copy in text['tips'])
        panels.append(f'<section id="los-regtest-panel-{language}" data-lab-language="{language}" lang="{text["lang"]}" class="rt-panel rt-width" aria-labelledby="los-regtest-title-{language}"><span class="rt-warning">{text["warning"]}</span><h2 id="los-regtest-title-{language}" class="rt-title">{text["title"]}</h2><p class="rt-copy">{text["intro"]}</p><div class="rt-legend">{legend}</div><div class="rt-table-wrap"><table class="rt-table"><thead><tr><th scope="col">{text["headers"][0]}</th><th scope="col">{text["headers"][1]}</th></tr></thead><tbody>{rows}</tbody></table></div>{tips}</section>')
    notice = '''<details id="los-regtest-notice">
<summary aria-controls="los-regtest-support"><span class="rt-brand">REGTEST</span><span class="rt-summary-pt">Moedas sem valor · O que funciona</span><span class="rt-summary-en" lang="en">Coins have no value · Supported features</span></summary>
<div id="los-regtest-support" class="rt-scroll"><div class="rt-toolbar"><div class="rt-width rt-toolbar-inner"><span class="rt-kicker">LOCAL · REGTEST</span><fieldset class="rt-switch"><legend class="rt-sr">Idioma / Language</legend><input class="rt-sr" type="radio" name="los-regtest-language" id="los-regtest-lang-pt" value="pt" checked><label for="los-regtest-lang-pt">Português</label><input class="rt-sr" type="radio" name="los-regtest-language" id="los-regtest-lang-en" value="en"><label for="los-regtest-lang-en">English</label></fieldset></div></div>''' + ''.join(panels) + '</div></details>'
    html, head_count = re.subn(r'(</head\s*>)', lambda match: css + match.group(1), html, count=1, flags=re.IGNORECASE)
    html, body_count = re.subn(r'(<body\b[^>]*>)', lambda match: match.group(1) + notice, html, count=1)
    if head_count != 1 or body_count != 1:
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
