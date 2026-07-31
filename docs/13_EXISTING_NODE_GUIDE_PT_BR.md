# LightningOS - Guia para Node Existente (PT-BR)

## AVISO MUITO IMPORTANTE
**O LightningOS NAO FOI CONCEBIDO PARA NODES COM INSTALACOES EXISTENTES.**  
Ele foi pensado com base em nodes configurados pelos tutoriais **BRLN BOLT** e **MINIBOLT**, mas existem diversas particularidades que infelizmente nao podem ser totalmente mapeadas em uma instalacao livre.  
Por isso, **se voce nao tem conhecimentos medianos de Linux e linha de comando, nao recomendamos esta instalacao**.  
Pode ser necessario realizar varios ajustes manuais e adaptacoes que nao estao totalmente cobertas neste guia.

## Escopo
- Este guia e para quem ja tem Bitcoin Core e LND funcionando.
- Nao use o install.sh em node existente.
- Prefira o install_existing.sh (fluxo principal). A configuracao manual abaixo e fallback/legado.
- LND gRPC local apenas (127.0.0.1:10009).
- Mainnet apenas.

## Premissas
- Dados em /data/lnd e /data/bitcoin.
- Se o seu Bitcoin Core esta em outro diretorio (ex: /mnt/bitcoin-data), prefira expor as credenciais locais no bloco `[Bitcoind]` do `/data/lnd/lnd.conf`. Como fallback, mantenha um `bitcoin.conf` legivel em um caminho detectavel: `/data/bitcoin/bitcoin.conf`, `/etc/bitcoin/bitcoin.conf`, `/var/lib/bitcoind/bitcoin.conf`, `/home/bitcoin/.bitcoin/bitcoin.conf`, `/home/admin/.bitcoin/bitcoin.conf` ou `/root/.bitcoin/bitcoin.conf`.
- Se voce ja usa /home/admin/.lnd e /home/admin/.bitcoin, o instalador guiado pode criar /data/lnd e /data/bitcoin apontando para esses caminhos.
- Usuario admin com links simbolicos /home/admin/.lnd -> /data/lnd e /home/admin/.bitcoin -> /data/bitcoin.
- Alternativa: usuarios lnd e bitcoin com dados em /data, e o admin nos grupos lnd e bitcoin.

## Instalacao da ultima release publicada
```bash
git clone https://github.com/jvxis/brln-os-light
cd brln-os-light/lightningos-light
sudo ./install_existing.sh
```

O proprio `install_existing.sh` identifica a ultima release publicada do LightningOS e instala sua tag exata. Para desenvolvimento ou instalacao offline do checkout local exato, use `sudo LIGHTNINGOS_INSTALL_SOURCE=checkout ./install_existing.sh`.

## Instalacao guiada
O `install_existing.sh` pergunta sobre Go/npm (necessarios para build), Postgres, terminal e ajustes basicos.
Quando voce optar por Postgres, o script cria os usuarios e o banco do LightningOS e preenche o secrets.env automaticamente.
Se o LND ja usa Postgres, o script nao instala um novo servidor Postgres por padrao: ele tenta usar o servico local existente e, se nao houver acesso administrativo local, pede um `NOTIFICATIONS_PG_ADMIN_DSN` para criar apenas o banco `lightningos` no servidor existente.
Quando encontra `db.postgres.dsn` no `lnd.conf`, o script tambem preenche `LND_PG_DSN` se o valor em `secrets.env` ainda estiver no placeholder.
O script tambem cria/atualiza os services do systemd com estes usuarios:
- lightningos-manager: usa o usuario/grupo `lightningos`.
- lightningos-reports: usa o mesmo usuario/grupo (`lightningos`).
- lightningos-terminal: roda como `lightningos`.
Para SupplementaryGroups, o script so adiciona grupos que existem no host.
O script recompila e reinstala manager/UI a partir da release publicada selecionada (prompts com default `y`).

Checklist rapido (pos-instalacao):
- Verifique o manager:
```bash
systemctl status lightningos-manager --no-pager
journalctl -u lightningos-manager -n 50 --no-pager
```
- Descubra o IP e acesse a UI:
```bash
hostname -I | awk '{print $1}'
```
Acesse: `https://IP_DA_MAQUINA:8443`
- Confirme grupos do usuario `lightningos`:
```bash
id lightningos
```
- Valide sudoers:
```bash
sudo visudo -cf /etc/sudoers.d/lightningos
```
- Se usa UFW, confirme a porta 8443:
```bash
sudo ufw status
```
Se voce usa Bitcoin/LND remoto, alguns checks podem aparecer como "ERR" ate configurar o acesso remoto.

Diagnostico rapido (sudo sem senha para App Store):
```bash
sudo -u lightningos sudo -n docker compose version || sudo -u lightningos sudo -n docker-compose version
```
Se aparecer `sudo: a password is required`, o sudoers do usuario `lightningos` nao esta aplicado corretamente.

Nota sobre UFW e LNDg (App Store):
Se o LNDg nao conseguir acessar o gRPC do LND e voce usa UFW, o trafego do Docker para o host pode estar bloqueado.
Siga estes passos para liberar a bridge usada pela rede do LNDg:
```bash
sudo docker exec -it lndg-lndg-1 getent hosts host.docker.internal
sudo docker exec -it lndg-lndg-1 bash -lc 'timeout 3 bash -lc "</dev/tcp/host.docker.internal/10009" && echo OK || echo FAIL'
sudo docker network inspect lndg_default --format '{{.Id}}'
# o nome da bridge e br-<primeiros 12 caracteres do id>
sudo ufw allow in on br-<id> to any port 10009 proto tcp
```
Se ainda falhar:
```bash
sudo iptables -I INPUT -i br-<id> -p tcp --dport 10009 -j ACCEPT
```

Se o script nao estiver executavel:
```bash
chmod +x install_existing.sh
# ou:
sudo bash install_existing.sh
```


## Configuracao manual (fallback legado)
Use esta secao somente se o `install_existing.sh` nao puder ser executado no host.

Fluxo minimo recomendado:
1) ajustar sudoers do usuario `lightningos`
2) garantir Docker + Compose funcionando sem prompt de senha para o manager
3) compilar/instalar manager e UI
4) reiniciar servicos e validar

### 1) Sudoers (essencial para App Store e upgrades)
```bash
sudo tee /etc/sudoers.d/lightningos >/dev/null <<'EOF'
Defaults:lightningos !requiretty
Cmnd_Alias LIGHTNINGOS_SYSTEM = /usr/bin/systemctl restart lnd, /usr/bin/systemctl restart lightningos-manager, /usr/bin/systemctl restart postgresql, /usr/bin/systemctl is-active lightningos-lnd-upgrade, /usr/bin/systemctl is-active lightningos-app-upgrade, /usr/bin/systemctl reboot, /usr/bin/systemctl poweroff, /usr/local/sbin/lightningos-fix-lnd-perms, /usr/local/sbin/lightningos-upgrade-lnd, /usr/local/sbin/lightningos-upgrade-app, /usr/sbin/smartctl *
Cmnd_Alias LIGHTNINGOS_APPS = /usr/bin/apt-get *, /usr/bin/apt *, /usr/bin/dpkg *, /usr/bin/docker *, /usr/bin/docker-compose *, /usr/bin/systemd-run *, /usr/sbin/ufw *
lightningos ALL=NOPASSWD: LIGHTNINGOS_SYSTEM, LIGHTNINGOS_APPS
EOF
sudo chmod 440 /etc/sudoers.d/lightningos
sudo visudo -cf /etc/sudoers.d/lightningos
```

Teste obrigatorio:
```bash
sudo -u lightningos sudo -n docker compose version || sudo -u lightningos sudo -n docker-compose version
```

### 2) Docker + Compose
```bash
sudo apt-get update
sudo apt-get install -y docker.io docker-compose-plugin || sudo apt-get install -y docker.io docker-compose
sudo systemctl enable --now docker
sudo systemctl is-active docker
```

### 3) Build e instalacao (manager/UI)
```bash
cd lightningos-light

go build -o dist/lightningos-manager ./cmd/lightningos-manager
sudo install -m 0755 dist/lightningos-manager /opt/lightningos/manager/lightningos-manager

cd ui
npm install
npm run build
cd ..

sudo rm -rf /opt/lightningos/ui/*
sudo cp -a ui/dist/. /opt/lightningos/ui/
```

### 4) Reinicio e validacao
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lightningos-manager
sudo systemctl restart lightningos-manager

systemctl status lightningos-manager --no-pager
journalctl -u lightningos-manager -n 100 --no-pager
curl -k https://127.0.0.1:8443/api/health
```

### 5) Requisitos de caminho
- `/data/lnd` continua sendo o caminho esperado para recursos como editor de `lnd.conf` e auto-unlock.
- Para Bitcoin local, garanta `bitcoind.rpcuser` e `bitcoind.rpcpass` no bloco `[Bitcoind]` do `/data/lnd/lnd.conf`, ou um `bitcoin.conf` legivel em uma origem detectavel, incluindo `/home/admin/.bitcoin/bitcoin.conf`.

## Troubleshooting rapido
- `sudo: a password is required` ao usar `sudo -n`: sudoers do `lightningos` invalido ou incompleto.
- `docker-compose failed ... sudo failed`: normalmente mesmo problema de sudoers, ou Docker inativo.
- Versao da app voltou para antiga: confirme se `LIGHTNINGOS_INSTALL_SOURCE=checkout` foi usado ou se o repositorio veio de um origin diferente do oficial.
- App mostra Bitcoin como `remote`/`ERR` mesmo com bitcoind local: confirme no `/data/lnd/lnd.conf` uma secao ativa `[Bitcoind]` com RPC local (linhas nao comentadas), por exemplo:
```ini
[Bitcoind]
bitcoind.rpchost=127.0.0.1:8332
bitcoind.rpcuser=SEU_RPC_USER
bitcoind.rpcpass=SEU_RPC_PASS
```
Se faltar esse bloco, a autodeteccao pode cair para `remote`. Depois de ajustar, reinicie `lightningos-manager` e `lnd`.
