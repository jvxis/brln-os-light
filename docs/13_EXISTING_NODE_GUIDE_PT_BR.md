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

Se o script nao estiver executavel:
```bash
chmod +x install_existing.sh
# ou:
sudo bash install_existing.sh
```

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
- O LightningOS nao le o bitcoin.conf automaticamente.
- Ele usa as credenciais em /etc/lightningos/secrets.env (BITCOIN_RPC_USER/PASS).
- Para Bitcoin local, use os mesmos valores do bitcoin.conf (rpcuser/rpcpassword).
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
- BITCOIN_RPC_USER e BITCOIN_RPC_PASS
- NOTIFICATIONS_PG_DSN e NOTIFICATIONS_PG_ADMIN_DSN
- LND_PG_DSN somente se o LND usa Postgres

Exemplo de DSNs:
```
NOTIFICATIONS_PG_DSN=postgres://losapp:SENHA@127.0.0.1:5432/lightningos?sslmode=disable
NOTIFICATIONS_PG_ADMIN_DSN=postgres://losadmin:SENHA@127.0.0.1:5432/postgres?sslmode=disable
LND_PG_DSN=postgres://lndpg:SENHA@127.0.0.1:5432/lnd?sslmode=disable
```

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
- SupplementaryGroups=lnd bitcoin systemd-journal docker

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
