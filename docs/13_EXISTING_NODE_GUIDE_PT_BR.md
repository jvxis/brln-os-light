# LightningOS Light - Guia para Node Existente (PT-BR)

## Escopo
- Este guia e para quem ja tem Bitcoin Core e LND funcionando.
- Nao use o install.sh. O fluxo e manual apos git pull e build.
- LND gRPC local apenas (127.0.0.1:10009).
- Mainnet apenas.

## Premissas
- Dados em /data/lnd e /data/bitcoin.
- Se o seu Bitcoin Core esta em outro diretorio (ex: /mnt/bitcoin-data), crie um bind mount ou symlink para /data/bitcoin (o LightningOS so le /data/bitcoin/bitcoin.conf).
- Se voce ja usa /home/admin/.lnd e /home/admin/.bitcoin, o instalador guiado pode criar /data/lnd e /data/bitcoin apontando para esses caminhos.
- Usuario admin com links simbolicos /home/admin/.lnd -> /data/lnd e /home/admin/.bitcoin -> /data/bitcoin.
- Alternativa: usuarios lnd e bitcoin com dados em /data, e o admin nos grupos lnd e bitcoin.

## Clonar repositorio
```bash
git clone https://github.com/jvxis/brln-os-light
cd brln-os-light/lightningos-light
```

## Instalacao guiada (opcional)
Se voce ja tem LND e Bitcoin Core rodando, pode usar o instalador guiado:
```bash
sudo ./install_existing.sh
```
Ele pergunta sobre Go/npm (necessarios para build), Postgres, terminal e ajustes basicos.
Quando voce optar por Postgres, o script cria os usuarios e o banco do LightningOS e preenche o secrets.env automaticamente.
O script tambem cria/atualiza os services do systemd com os usuarios informados:
- lightningos-manager: usa o Manager service user/group escolhidos.
- lightningos-reports: usa o mesmo user/group do manager.
- lightningos-terminal: usa o usuario informado para o terminal.
Para SupplementaryGroups, o script so adiciona grupos que existem no host.

Se o script nao estiver executavel:
```bash
chmod +x install_existing.sh
# ou:
sudo bash install_existing.sh
```

## App Store (LNDg e outros apps)
Para usar a App Store em nodes existentes (sem instalar o Bitcoin pelo LightningOS), siga estes passos:

1) Garanta Docker ativo:
```bash
sudo apt update && sudo apt upgrade -y
sudo apt install -y ca-certificates curl gnupg lsb-release

sudo mkdir -p /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg

echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu noble stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

sudo apt update
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo systemctl status docker
sudo systemctl enable docker
```

2) Adicione o usuario do manager ao grupo docker (ex: admin) e re-login:
```bash
sudo usermod -aG docker admin
```
Se preferir nao fazer logout, use os comandos docker com sudo (ex.: `sudo docker ...`).

3) Habilite sudo sem senha para o usuario do manager (ex: admin):
```bash
sudo tee /etc/sudoers.d/lightningos >/dev/null <<'EOF'
Defaults:admin !requiretty
Cmnd_Alias LIGHTNINGOS_SYSTEM = /bin/systemctl restart lnd, /bin/systemctl restart lightningos-manager, /bin/systemctl restart postgresql, /usr/sbin/smartctl *
Cmnd_Alias LIGHTNINGOS_APPS = /usr/bin/apt-get *, /usr/bin/apt *, /usr/bin/dpkg *, /usr/bin/docker *, /usr/bin/docker-compose *, /usr/bin/systemd-run *
admin ALL=NOPASSWD: LIGHTNINGOS_SYSTEM, LIGHTNINGOS_APPS
EOF
sudo chmod 440 /etc/sudoers.d/lightningos
sudo visudo -cf /etc/sudoers.d/lightningos
```

4) Permita que o LND receba gRPC a partir do Docker (necessario para LNDg).
**Importante:** essas linhas devem ficar fora de blocos como `[Bitcoind]`. Coloque no topo do arquivo ou dentro de `[Application Options]`.
```bash
GATEWAY=$(sudo docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}')
LND_CONF=/data/lnd/lnd.conf
sudo cp "$LND_CONF" "${LND_CONF}.bak.$(date +%F_%H%M%S)"
OWNER=$(sudo stat -c '%u:%g' "$LND_CONF")
MODE=$(sudo stat -c '%a' "$LND_CONF")
sudo sed -i '/^rpclisten=/d;/^tlsextraip=/d;/^tlsextradomain=/d' "$LND_CONF"
tmp=$(mktemp)
awk -v gw="$GATEWAY" '
  BEGIN {added=0}
  /^[[:space:]]*\\[.*\\]/ && added==0 {
    print "[Application Options]"
    print "rpclisten=127.0.0.1:10009"
    print "rpclisten=" gw ":10009"
    print "tlsextraip=" gw
    print "tlsextradomain=host.docker.internal"
    added=1
  }
  {print}
  END {
    if (added==0) {
      print "[Application Options]"
      print "rpclisten=127.0.0.1:10009"
      print "rpclisten=" gw ":10009"
      print "tlsextraip=" gw
      print "tlsextradomain=host.docker.internal"
    }
  }
' "$LND_CONF" > "$tmp"
sudo mv "$tmp" "$LND_CONF"
sudo chown "$OWNER" "$LND_CONF"
sudo chmod "$MODE" "$LND_CONF"
sudo rm -f /data/lnd/tls.cert /data/lnd/tls.key
sudo systemctl restart lnd
```

Alternativa manual (sem script):
1) Descubra o IP do docker0:
```bash
ip -4 addr show docker0 | awk '/inet / {print $2}' | cut -d/ -f1
```

2) No `lnd.conf`, adicione as linhas **fora de seções** (ou dentro de `[Application Options]`):
```
[Application Options]
rpclisten=127.0.0.1:10009
rpclisten=SEU_GATEWAY:10009
tlsextraip=SEU_GATEWAY
tlsextradomain=host.docker.internal
```

3) Reinicie o LND para regenerar o TLS:
```bash
sudo rm -f /data/lnd/tls.cert /data/lnd/tls.key
sudo systemctl restart lnd
```

Depois disso, instale o LNDg pela App Store.

## Importante sobre /data/lnd
- O LightningOS usa caminhos fixos para lnd.conf e wallet.db em /data/lnd.
- Se o LND nao estiver em /data/lnd, o editor de lnd.conf e o auto-unlock nao funcionam.
- Recomendacao: usar /data/lnd ou criar symlink/bind para /data/lnd.

## Checagem rapida (somente leitura)
```bash
systemctl status lnd --no-pager
systemctl status bitcoind --no-pager

ls -la /data/lnd /data/bitcoin
ls -la /home/admin/.lnd /home/admin/.bitcoin

ls -l /data/lnd/tls.cert /data/lnd/data/chain/bitcoin/mainnet/admin.macaroon
ss -ltnp | rg ':10009' || ss -ltnp | grep -n ':10009'

rg -n "rpcuser|rpcpassword|zmqpubraw" /data/bitcoin/bitcoin.conf || \
  grep -nE "rpcuser|rpcpassword|zmqpubraw" /data/bitcoin/bitcoin.conf
```

## Build do manager e UI
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

## Pastas e permissoes
```bash
sudo mkdir -p /etc/lightningos/tls /opt/lightningos/manager /opt/lightningos/ui \
  /var/lib/lightningos /var/log/lightningos

sudo chown -R root:admin /etc/lightningos
sudo chmod 750 /etc/lightningos /etc/lightningos/tls
sudo chown -R admin:admin /opt/lightningos /var/lib/lightningos /var/log/lightningos

sudo usermod -aG lnd,bitcoin admin
sudo ln -sfn /data/lnd /home/admin/.lnd
sudo ln -sfn /data/bitcoin /home/admin/.bitcoin
```

## TLS do manager
```bash
sudo openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
  -subj "/CN=$(hostname -f)" \
  -keyout /etc/lightningos/tls/server.key \
  -out /etc/lightningos/tls/server.crt

sudo chown root:admin /etc/lightningos/tls/server.key /etc/lightningos/tls/server.crt
sudo chmod 640 /etc/lightningos/tls/server.key /etc/lightningos/tls/server.crt
```

## Configuracao do LightningOS
1) Copie o template:
```bash
sudo cp lightningos-light/templates/lightningos.config.yaml /etc/lightningos/config.yaml
sudo ${EDITOR:-nano} /etc/lightningos/config.yaml
```

2) Ajuste estes campos:
- server.host: "0.0.0.0" para acesso LAN, ou "127.0.0.1" para local.
- lnd.grpc_host: "127.0.0.1:10009"
- lnd.tls_cert_path: "/data/lnd/tls.cert"
- lnd.admin_macaroon_path: "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"
- bitcoin_remote.rpchost: "bitcoin.br-ln.com:8085"
- bitcoin_remote.zmq_rawblock: "tcp://bitcoin.br-ln.com:28332"
- bitcoin_remote.zmq_rawtx: "tcp://bitcoin.br-ln.com:28333"
- postgres.db_name: "lnd" (somente se o LND usa Postgres; se usa Bolt/SQLite, este campo nao e usado)

## Bitcoin RPC (local e remoto)
- BITCOIN_RPC_USER e BITCOIN_RPC_PASS em /etc/lightningos/secrets.env sao para o Bitcoin remoto (normalmente preenchidos pelo wizard).
- Bitcoin local e lido de /data/bitcoin/bitcoin.conf ou do bloco [Bitcoind] em /data/lnd/lnd.conf.
- Para o LightningOS reconhecer como local, o bitcoind.rpchost pode ser 127.0.0.1, localhost ou o IP da propria maquina.
- Opcional: defina BITCOIN_SOURCE=local ou BITCOIN_SOURCE=remote no secrets.env para forcar o modo.
- Se voce usa rpcauth, precisa do usuario e da senha original (o hash do rpcauth sozinho nao serve) ou crie um usuario rpcuser/rpcpassword dedicado.
- Se voce usa o Bitcoin remoto do clube, pode manter bitcoin.br-ln.com:8085 e os ZMQs do template.

Exemplo minimo de bitcoin.conf (local):
```
server=1
rpcuser=usuario_rpc
rpcpassword=senha_rpc
rpcallowip=127.0.0.1
zmqpubrawblock=tcp://127.0.0.1:28332
zmqpubrawtx=tcp://127.0.0.1:28333
```

Nota: se voce usa rpcport diferente, o LightningOS usa essa porta para o RPC local ao ler /data/bitcoin/bitcoin.conf.

Teste RPC local (opcional):
```bash
bitcoin-cli -conf=/data/bitcoin/bitcoin.conf getblockchaininfo
# ou, se nao tiver bitcoin-cli:
curl --user usuario_rpc:senha_rpc --data-binary '{"jsonrpc":"1.0","id":"curl","method":"getblockchaininfo","params":[]}' \
  -H 'content-type:text/plain;' http://127.0.0.1:8332/
```

## Secrets (credenciais e DSNs)
1) Copie o template:
```bash
sudo cp lightningos-light/templates/secrets.env /etc/lightningos/secrets.env
sudo ${EDITOR:-nano} /etc/lightningos/secrets.env
sudo chown root:admin /etc/lightningos/secrets.env
sudo chmod 640 /etc/lightningos/secrets.env
```

2) Preencha:
- BITCOIN_RPC_USER e BITCOIN_RPC_PASS (apenas para Bitcoin remoto)
- NOTIFICATIONS_PG_DSN e NOTIFICATIONS_PG_ADMIN_DSN
- LND_PG_DSN somente se o LND usa Postgres
- Opcional: BITCOIN_SOURCE=local ou BITCOIN_SOURCE=remote para forcar o modo
- Opcional: NOTIFICATIONS_FORWARDS_BACKFILL=1 para backfill completo de forwards (ver secao abaixo)
- Opcional: REPORTS_LIVE_TIMEOUT_SEC e REPORTS_LIVE_LOOKBACK_HOURS para ajustar o relatorio ao vivo (ver secao abaixo)

Exemplo de DSNs:
```
NOTIFICATIONS_PG_DSN=postgres://losapp:SENHA@127.0.0.1:5432/lightningos?sslmode=disable
NOTIFICATIONS_PG_ADMIN_DSN=postgres://losadmin:SENHA@127.0.0.1:5432/postgres?sslmode=disable
LND_PG_DSN=postgres://lndpg:SENHA@127.0.0.1:5432/lnd?sslmode=disable
```

## Relatorios ao vivo (timeout/volume alto)
Em nodes com volume muito alto, o relatorio ao vivo (hoje 00:00 -> agora) pode estourar timeout.
Voce pode aumentar o timeout e/ou reduzir a janela:
```
REPORTS_LIVE_TIMEOUT_SEC=60
REPORTS_LIVE_LOOKBACK_HOURS=6
```

Depois disso, reinicie o manager:
```bash
sudo systemctl restart lightningos-manager
```

## Relatorio diario (reports-run) - timeout
Se o relatorio diario estiver falhando em nodes com alto volume, aumente o timeout:
```
REPORTS_RUN_TIMEOUT_SEC=300
```
Depois reinicie o timer/servico ou rode manualmente:
```bash
sudo systemctl restart lightningos-reports.timer
# ou manual:
/opt/lightningos/manager/lightningos-manager reports-run
```

## Notificacoes - Backfill de forwards (opcional)
Por padrao o LightningOS evita backfill completo de forwards (nodes com historico grande podem levar horas).
Se voce quiser trazer todo o historico de forwards:
1) Edite o secrets.env:
```bash
sudo ${EDITOR:-nano} /etc/lightningos/secrets.env
```
2) Adicione:
```
NOTIFICATIONS_FORWARDS_BACKFILL=1
```
3) Zere o cursor (opcional, recomendado para forcar backfill):
```bash
psql "$NOTIFICATIONS_PG_DSN" -c "delete from notification_cursors where key in ('forwards_after','forwards_offset');"
```
4) Reinicie o manager:
```bash
sudo systemctl restart lightningos-manager
```

Para voltar ao modo rapido, remova a variavel NOTIFICATIONS_FORWARDS_BACKFILL do secrets.env e reinicie o manager.

## Postgres - Trilha A (LND em Postgres)
1) Verifique o backend do LND:
```bash
rg -n "db.backend|db.postgres.dsn" /data/lnd/lnd.conf || \
  grep -nE "db.backend|db.postgres.dsn" /data/lnd/lnd.conf
```

2) Garanta que o Postgres esta ativo:
```bash
systemctl status postgresql --no-pager
```

3) Crie usuarios e DB para notificacoes/relatorios (se nao existir):
```bash
sudo -u postgres psql -c "create role losadmin with login password 'SENHA_ADMIN' createrole createdb;"
sudo -u postgres psql -c "create role losapp with login password 'SENHA_APP';"
sudo -u postgres psql -c "create database lightningos owner losapp;"
```

4) Ajuste secrets.env:
- NOTIFICATIONS_PG_DSN e NOTIFICATIONS_PG_ADMIN_DSN com as senhas criadas
- LND_PG_DSN apontando para o DB do LND

5) No config.yaml:
- postgres.db_name: "lnd"

## Postgres - Trilha B (LND em Bolt/SQLite)
1) Instale o Postgres (exemplos):
```bash
# Debian/Ubuntu
sudo apt-get update
sudo apt-get install -y postgresql postgresql-client

# RHEL/Fedora/CentOS
sudo dnf install -y postgresql-server postgresql
sudo postgresql-setup --initdb
sudo systemctl enable --now postgresql
```

2) Crie usuarios e DB para notificacoes/relatorios:
```bash
sudo -u postgres psql -c "create role losadmin with login password 'SENHA_ADMIN' createrole createdb;"
sudo -u postgres psql -c "create role losapp with login password 'SENHA_APP';"
sudo -u postgres psql -c "create database lightningos owner losapp;"
```

3) Ajuste secrets.env:
- NOTIFICATIONS_PG_DSN e NOTIFICATIONS_PG_ADMIN_DSN com as senhas criadas
- Deixe LND_PG_DSN vazio ou remova do arquivo

4) No config.yaml:
- postgres.db_name: "lnd" (pode manter o padrao; nao e usado se o LND nao usa Postgres)

## Atualização dos Relatórios
### 1) Sincronização de custos e lucros diários
Comando para calcular e armazenar os custos e lucros diários, garantindo que os relatórios financeiros apresentem informações corretas e completas.
```bash
/opt/lightningos/manager/lightningos-manager reports-backfill --from YYYY-MM-DD --to YYYY-MM-DD
```

### 2) Agendamento diário de atualização
Configure um systemd timer para executar diariamente um serviço que atualiza o banco de dados com os dados do dia anterior.
```bash
sudo cp templates/systemd/lightningos-reports.timer \
  /etc/systemd/system/lightningos-reports.timer
```

```bash
sudo cp templates/systemd/lightningos-reports.service \
  /etc/systemd/system/lightningos-reports.service
```

### 3) Ajustes recomendados
Edite o arquivo lightningos-reports.service e ajuste conforme o seu ambiente:
- User=admin
- Group=admin
- SupplementaryGroups=systemd-journal (somente se existir)
```bash
sudo ${EDITOR:-nano} /etc/systemd/system/lightningos-reports.service
```

### 4) Habilite e inicie
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lightningos-reports.timer
```

## Systemd do manager
1) Copie a unit e ajuste User/Group:
```bash
sudo cp templates/systemd/lightningos-manager.service \
  /etc/systemd/system/lightningos-manager.service
sudo ${EDITOR:-nano} /etc/systemd/system/lightningos-manager.service
```

2) Ajustes recomendados:
- User=admin
- Group=admin
- SupplementaryGroups=lnd bitcoin systemd-journal docker (somente os grupos que existirem)

3) Habilite e inicie:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lightningos-manager
```

## Validacao
```bash
systemctl status lightningos-manager --no-pager
journalctl -u lightningos-manager -n 200 --no-pager

curl -k https://127.0.0.1:8443/api/health
curl -k https://127.0.0.1:8443/api/postgres
curl -k https://127.0.0.1:8443/api/lnd/status
```

## Problemas comuns
- TLS/macaroon: erro de permissao ou arquivo ausente.
- Postgres: service inativo ou DSN invalido.
- Bitcoin RPC/ZMQ: credenciais ou portas incorretas.
- LND gRPC: porta diferente de 10009 ou bind nao local.
- Se o service do Postgres nao se chama "postgresql", crie um alias systemd ou ajuste sua distro para expor esse nome.
