# Instalando o LightningOS Light no Raspberry Pi 5 — Debian 12 (Bookworm)

> **Guia comunitário** — Adaptação do instalador oficial do LightningOS Light para
> **Raspberry Pi 5 (8 GB RAM)** rodando **Debian 12 Bookworm (aarch64)** com Bitcoin Core já instalado.
>
> Projeto original: [https://github.com/jvxis/brln-os-light](https://github.com/jvxis/brln-os-light)
> Este fork: [https://github.com/MarcosJRcwb/brln-os-light](https://github.com/MarcosJRcwb/brln-os-light)

---

[Read in English](16_INSTALL_RASPBERRYPI5_DEBIAN12.md)

---

## Por que um guia separado?

O instalador oficial `install.sh` e `install_existing_pi.sh` foram desenvolvidos para
**Ubuntu Server 22.04/24.04**. O Debian 12 tem diferenças importantes que causam falhas
silenciosas se não forem tratadas:

| Problema | Detalhe |
|---|---|
| Nome do grupo do Tor | Debian usa `debian-tor`, não `tor` |
| Arquitetura do binário Go | O instalador pode baixar `amd64` — precisa forçar `arm64` |
| Arquitetura do GoTTY | Binário bundled é `amd64` — precisa substituir |
| Conflito de porta | Porta 8080 pode estar em uso — use 8082 |
| Método de instalação do PostgreSQL | APT (PGDG) pode falhar — use Docker |
| Logs do journalctl na UI | Mostra aviso de permissão — apenas cosmético, sem impacto |

---

## Pré-requisitos

| Item | Requisito |
|---|---|
| Hardware | Raspberry Pi 5, 8 GB RAM |
| Sistema Operacional | Debian 12 Bookworm, aarch64 |
| Armazenamento | SSD externo, mínimo 1 TB (2 TB recomendado) |
| Bitcoin Core | Já instalado e **completamente sincronizado** |
| Acesso | SSH (PuTTY ou qualquer terminal) ou terminal local |
| Internet | Conexão estável durante toda a instalação |

> ⚠️ **Não inicie até que o Bitcoin Core esteja 100% sincronizado.**
> Verifique com: `bitcoin-cli getblockchaininfo | grep verificationprogress`
> O valor precisa ser `0.9999...` ou `1`.

---

## Visão geral da instalação

```
FASE 1: Preparação do ambiente
  1.1  Verificar SO e arquitetura
  1.2  Confirmar que o Bitcoin Core está rodando e sincronizado
  1.3  Adicionar ZMQ ao bitcoin.conf
  1.4  Criar symlinks em /data
       ↓
FASE 2: Dependências
  2.1  Instalar Go 1.24 (linux-arm64 — obrigatório)
  2.2  Instalar Node.js 20.x
  2.3  Instalar PostgreSQL via Docker
       ↓
FASE 3: Instalação do LightningOS
  3.1  Clonar repositório
  3.2  Criar usuários e diretórios
  3.3  Compilar o manager (Go build)
  3.4  Compilar a UI (npm build)
  3.5  Gerar certificado TLS
       ↓
FASE 4: Instalação do LND
  4.1  Baixar LND v0.20.0 (linux-arm64)
  4.2  Criar serviço systemd
       ↓
FASE 5: Ajustes específicos do Debian
  5.1  Verificar conflito na porta 8080
  5.2  Configurar Tor com grupo debian-tor
  5.3  Substituir GoTTY pelo binário arm64 correto
  5.4  Configurar permissões de grupos
  5.5  Criar secrets.env e config.yaml
  5.6  Criar serviço systemd do LightningOS
       ↓
FASE 6: Validação
  6.1  Verificar todos os serviços
  6.2  Acessar a UI e executar o wizard
  6.3  Criar carteira — anotar a seed em papel físico
```

> **Um comando por vez.** Verifique a saída antes de prosseguir para o próximo passo.

---

## FASE 1 — Preparação do ambiente

### 1.1 Verificar o sistema operacional

```bash
uname -a
```

Saída esperada (deve conter `aarch64` e `Debian`):
```
Linux raspnode 6.12.62+rpt-rpi-2712 #1 SMP PREEMPT Debian aarch64 GNU/Linux
```

> ❌ Se `aarch64` não aparecer: este guia não se aplica ao seu hardware.

---

### 1.2 Verificar o Bitcoin Core

```bash
systemctl status bitcoin --no-pager | head -10
```

Esperado: `Active: active (running)`

> ⏳ Se estiver sincronizando: aguarde `verificationprogress` chegar em `1` antes de continuar.

---

### 1.3 Adicionar ZMQ ao bitcoin.conf

O LND precisa do ZMQ para receber notificações em tempo real de blocos e transações.

Verifique se já está configurado:
```bash
sudo grep zmq /etc/bitcoin/bitcoin.conf
```

Se as linhas abaixo já estiverem presentes, pule para o passo 1.4:
```
zmqpubrawblock=tcp://127.0.0.1:28332
zmqpubrawtx=tcp://127.0.0.1:28333
```

Se não estiverem, adicione e reinicie:
```bash
sudo bash -c 'printf "\n# ZMQ para o LND\nzmqpubrawblock=tcp://127.0.0.1:28332\nzmqpubrawtx=tcp://127.0.0.1:28333\n" >> /etc/bitcoin/bitcoin.conf'
sudo systemctl restart bitcoin
sleep 30 && systemctl status bitcoin --no-pager | grep Active
```

---

### 1.4 Criar symlinks em /data

O LightningOS espera encontrar dados em `/data/bitcoin` e `/data/lnd`.
Adapte os caminhos abaixo para o ponto de montagem real do seu SSD.

```bash
sudo mkdir -p /data
sudo ln -sf /mnt/ssd/bitcoin /data/bitcoin
sudo mkdir -p /mnt/ssd/lnd
sudo ln -sf /mnt/ssd/lnd /data/lnd
sudo ln -sf /etc/bitcoin/bitcoin.conf /mnt/ssd/bitcoin/bitcoin.conf
```

Verifique:
```bash
ls -la /data/
```

Esperado:
```
bitcoin -> /mnt/ssd/bitcoin
lnd -> /mnt/ssd/lnd
```

---

## FASE 2 — Dependências

### 2.1 Instalar Go 1.24 (linux-arm64 — CRÍTICO)

> ⚠️ **Ponto de falha comum:** scripts automáticos podem baixar o binário `amd64`.
> No Raspberry Pi, sempre instale `linux-arm64` manualmente.

```bash
cd /tmp && curl -LO https://go.dev/dl/go1.24.0.linux-arm64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.24.0.linux-arm64.tar.gz
```

Verifique (deve mostrar `linux/arm64`):
```bash
/usr/local/go/bin/go version
```

Esperado:
```
go version go1.24.0 linux/arm64
```

> ❌ Se mostrar `amd64`: apague e reinstale usando o arquivo correto acima.

---

### 2.2 Instalar Node.js 20.x

```bash
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt-get install -y nodejs
node --version
```

Esperado: `v20.x.x`

---

### 2.3 Instalar PostgreSQL via Docker

Verifique se o Docker está instalado:
```bash
docker --version
```

Se não estiver:
```bash
sudo apt-get install -y docker.io docker-compose-plugin
sudo systemctl enable --now docker
```

Crie o diretório de dados e o arquivo Docker Compose:

```bash
sudo mkdir -p /mnt/ssd/postgres/data /opt/lightningos-postgres
```

Crie `/opt/lightningos-postgres/docker-compose.yml` com o conteúdo abaixo.
**Substitua todas as senhas de exemplo por valores fortes e anote em local seguro.**

```yaml
version: "3.8"
services:
  postgres:
    image: postgres:16
    container_name: lightningos-postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: losadmin
      POSTGRES_PASSWORD: <SENHA_ADMIN_FORTE>
      POSTGRES_DB: postgres
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - /mnt/ssd/postgres/data:/var/lib/postgresql/data
```

Inicie o PostgreSQL:
```bash
cd /opt/lightningos-postgres && sudo docker compose up -d
sleep 10 && sudo docker ps | grep postgres
```

Esperado: container com status `Up`.

Crie os bancos de dados e usuários necessários (substitua as senhas pelas que você definiu):
```bash
sudo docker exec -it lightningos-postgres psql -U losadmin -c "
CREATE USER lndpg WITH PASSWORD '<SENHA_LND_DB>';
CREATE DATABASE lnd OWNER lndpg;
CREATE USER losapp WITH PASSWORD '<SENHA_APP_DB>';
CREATE DATABASE lightningos OWNER losapp;
GRANT ALL PRIVILEGES ON DATABASE lnd TO lndpg;
GRANT ALL PRIVILEGES ON DATABASE lightningos TO losapp;
"
```

---

## FASE 3 — Instalação do LightningOS

### 3.1 Clonar o repositório

```bash
sudo git clone https://github.com/MarcosJRcwb/brln-os-light /opt/brln-os-light
ls /opt/brln-os-light/lightningos-light/
```

---

### 3.2 Criar usuários e diretórios

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin lightningos 2>/dev/null || echo "já existe"
sudo useradd --system --no-create-home --shell /usr/sbin/nologin lnd 2>/dev/null || echo "já existe"
sudo mkdir -p /opt/lightningos/manager /opt/lightningos/ui
sudo mkdir -p /etc/lightningos/tls
sudo mkdir -p /var/lib/lightningos /var/log/lightningos
```

---

### 3.3 Compilar o manager (backend Go)

> ⏳ **Esta etapa pode demorar de 5 a 15 minutos.** Nenhuma saída é mostrada durante a compilação — isso é normal. Aguarde o prompt retornar.

```bash
cd /opt/brln-os-light/lightningos-light
sudo GOFLAGS="-mod=mod" /usr/local/go/bin/go build -o dist/lightningos-manager ./cmd/lightningos-manager
```

Verifique se o binário foi criado (deve ter entre 20 e 40 MB):
```bash
ls -lh dist/lightningos-manager
```

Instale:
```bash
sudo install -m 0755 dist/lightningos-manager /opt/lightningos/manager/lightningos-manager
```

> ❌ Se aparecerem erros de módulos: execute `sudo /usr/local/go/bin/go mod download` antes de tentar novamente.

---

### 3.4 Compilar a interface web (UI)

```bash
cd /opt/brln-os-light/lightningos-light/ui
sudo npm install
```

> ⏳ Aguarde a instalação dos pacotes npm (pode demorar ~5 minutos).

```bash
sudo npm run build
sudo cp -a dist/. /opt/lightningos/ui/
```

Verifique:
```bash
ls /opt/lightningos/ui/ | head -5
```

Esperado: `index.html  assets/  ...`

---

### 3.5 Gerar certificado TLS autoassinado

```bash
sudo openssl req -x509 -newkey rsa:4096 \
  -keyout /etc/lightningos/tls/server.key \
  -out /etc/lightningos/tls/server.crt \
  -days 3650 -nodes \
  -subj "/CN=lightningos" \
  -addext "subjectAltName=IP:127.0.0.1,IP:0.0.0.0"
```

---

## FASE 4 — Instalação do LND

### 4.1 Baixar LND v0.20.0 (linux-arm64)

> ⚠️ Sempre use `linux-arm64`. O binário `amd64` retorna `Exec format error` no Raspberry Pi.

```bash
cd /tmp
curl -LO https://github.com/lightningnetwork/lnd/releases/download/v0.20.0-beta/lnd-linux-arm64-v0.20.0-beta.tar.gz
tar -xzf lnd-linux-arm64-v0.20.0-beta.tar.gz
sudo install -m 0755 lnd-linux-arm64-v0.20.0-beta/lnd /usr/local/bin/lnd
sudo install -m 0755 lnd-linux-arm64-v0.20.0-beta/lncli /usr/local/bin/lncli
lnd --version
```

Esperado: `lnd version 0.20.0-beta`

---

### 4.2 Configurar lnd.conf

Crie `/mnt/ssd/lnd/lnd.conf` com o conteúdo abaixo.

> **Substitua:**
> - `<SEU_RPC_USER>` e `<SEU_RPC_PASS>` pelas credenciais RPC do seu Bitcoin Core
> - `<SENHA_LND_DB>` pela senha do PostgreSQL que você definiu na Fase 2
> - Se a porta 8080 já estiver em uso, mantenha `restlisten=127.0.0.1:8082` (veja passo 5.1)

```ini
[Application Options]
alias=MeuNode-BR
color=#ff9900
debuglevel=info
restlisten=127.0.0.1:8082

# Descomente após criar a carteira:
# wallet-unlock-password-file=/data/lnd/password.txt
# wallet-unlock-allow-create=true

tlsautorefresh=true
tlsextraip=127.0.0.1
tlsdisableautofill=true

accept-keysend=true
accept-amp=true
allow-circular-route=true

[Bitcoin]
bitcoin.mainnet=1
bitcoin.node=bitcoind
bitcoin.basefee=1000
bitcoin.feerate=1
bitcoin.timelockdelta=144

[db]
db.backend=postgres
db.use-native-sql=true

[postgres]
db.postgres.dsn=postgres://lndpg:<SENHA_LND_DB>@127.0.0.1:5432/lnd?sslmode=disable
db.postgres.timeout=0

[Bitcoind]
bitcoind.rpchost=127.0.0.1:8332
bitcoind.rpcuser=<SEU_RPC_USER>
bitcoind.rpcpass=<SEU_RPC_PASS>
bitcoind.zmqpubrawblock=tcp://127.0.0.1:28332
bitcoind.zmqpubrawtx=tcp://127.0.0.1:28333
bitcoind.estimatemode=ECONOMICAL

[tor]
tor.active=true
tor.v3=true
tor.skip-proxy-for-clearnet-targets=false
tor.streamisolation=true

[rpcmiddleware]
rpcmiddleware.enable=true
```

Configure as permissões:
```bash
sudo chown -R lnd:lnd /mnt/ssd/lnd
sudo chmod 750 /mnt/ssd/lnd
```

---

### 4.3 Criar serviço systemd do LND

Crie `/etc/systemd/system/lnd.service`:

```ini
[Unit]
Description=Lightning Network Daemon (LND)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/lnd --lnddir=/data/lnd
ExecStop=/usr/local/bin/lncli --lnddir=/data/lnd stop
Restart=on-failure
RestartSec=60
Type=notify
TimeoutStartSec=1200
TimeoutStopSec=3600
User=lnd
Group=lnd

[Install]
WantedBy=multi-user.target
```

Habilite e inicie:
```bash
sudo systemctl daemon-reload
sudo systemctl enable lnd
sudo systemctl start lnd
sleep 30 && systemctl status lnd --no-pager | grep Active
```

Esperado: `Active: active (running)`

> Mensagem "wallet locked" nos logs é normal antes de criar a carteira.

---

## FASE 5 — Ajustes específicos do Debian 12

### 5.1 Verificar conflito na porta 8080

```bash
ss -tlnp | grep 8080
```

- Se algum serviço aparecer: use `restlisten=127.0.0.1:8082` no `lnd.conf` (já definido no template acima).
- Se não aparecer nada: a porta padrão está livre, mas `8082` também funciona normalmente.

---

### 5.2 Configurar Tor — grupo debian-tor

> ⚠️ **Diferença crítica Debian vs Ubuntu:** o grupo do cookie do Tor é `debian-tor`, não `tor`.
> Ignorar este passo faz o LND falhar ao conectar no Tor silenciosamente.

```bash
sudo usermod -aG debian-tor lnd
```

Verifique se o Tor está rodando:
```bash
systemctl status tor --no-pager | head -5
```

Se não estiver instalado:
```bash
sudo apt-get install -y tor
```

Adicione a configuração necessária ao Tor se ainda não estiver presente:
```bash
sudo bash -c 'grep -q "ControlPort 9051" /etc/tor/torrc || cat >> /etc/tor/torrc << EOF

# LightningOS LND
ControlPort 9051
CookieAuthentication 1
CookieAuthFileGroupReadable 1
EOF'
sudo systemctl restart tor
```

---

### 5.3 Substituir GoTTY pelo binário arm64

> ⚠️ **Problema frequente:** o GoTTY instalado pelo script é `amd64`.
> No Raspberry Pi (arm64), ele retorna `Exec format error`. Substitua pelo binário correto.

```bash
curl -L https://github.com/sorenisanerd/gotty/releases/download/v1.5.0/gotty_v1.5.0_linux_arm64.tar.gz \
  -o /tmp/gotty.tar.gz
tar -xzf /tmp/gotty.tar.gz -C /tmp
sudo install -m 0755 /tmp/gotty /usr/local/bin/gotty
gotty --version
```

Esperado: `gotty version v1.5.0`

---

### 5.4 Configurar permissões de grupos

```bash
sudo usermod -aG lnd lightningos
sudo usermod -aG docker lightningos
```

---

### 5.5 Configurar secrets.env e config.yaml

Crie `/etc/lightningos/secrets.env` — substitua todos os valores entre `< >`:

```bash
sudo tee /etc/lightningos/secrets.env > /dev/null << 'EOF'
LND_PG_DSN=postgres://lndpg:<SENHA_LND_DB>@127.0.0.1:5432/lnd?sslmode=disable
NOTIFICATIONS_PG_DSN=postgres://losapp:<SENHA_APP_DB>@127.0.0.1:5432/lightningos?sslmode=disable
NOTIFICATIONS_PG_ADMIN_DSN=postgres://losadmin:<SENHA_ADMIN_DB>@127.0.0.1:5432/postgres?sslmode=disable
BITCOIN_RPC_USER=<SEU_RPC_USER>
BITCOIN_RPC_PASS=<SEU_RPC_PASS>
REPORTS_TIMEZONE=America/Sao_Paulo
TERMINAL_ENABLED=1
TERMINAL_CREDENTIAL=losop:<GERE_UMA_SENHA_FORTE>
TERMINAL_OPERATOR_USER=losop
TERMINAL_OPERATOR_PASSWORD=<MESMA_SENHA_ACIMA>
TERMINAL_PORT=7681
EOF

sudo chmod 660 /etc/lightningos/secrets.env
sudo chown root:lightningos /etc/lightningos/secrets.env
```

Crie `/etc/lightningos/config.yaml`:

```bash
sudo tee /etc/lightningos/config.yaml > /dev/null << 'EOF'
server:
  host: "0.0.0.0"
  port: 8443
  tls_cert: "/etc/lightningos/tls/server.crt"
  tls_key: "/etc/lightningos/tls/server.key"

lnd:
  grpc_host: "127.0.0.1:10009"
  tls_cert_path: "/data/lnd/tls.cert"
  admin_macaroon_path: "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"

bitcoin_remote:
  rpchost: "127.0.0.1:8332"
  zmq_rawblock: "tcp://127.0.0.1:28332"
  zmq_rawtx: "tcp://127.0.0.1:28333"

postgres:
  db_name: "lnd"

ui:
  static_dir: "/opt/lightningos/ui"

features:
  enable_login: false
  enable_bitcoin_local_placeholder: true
  enable_app_store_placeholder: true
EOF
```

---

### 5.6 Criar serviço systemd do LightningOS Manager

Crie `/etc/systemd/system/lightningos-manager.service`:

```ini
[Unit]
Description=LightningOS Manager
After=network-online.target lnd.service
Wants=network-online.target

[Service]
User=lightningos
Group=lightningos
SupplementaryGroups=lnd systemd-journal docker
Type=simple
EnvironmentFile=/etc/lightningos/secrets.env
ExecStart=/opt/lightningos/manager/lightningos-manager --config /etc/lightningos/config.yaml
Restart=on-failure
RestartSec=3
LimitNOFILE=65536
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=/var/lib/lightningos /var/log/lightningos /etc/lightningos /data/lnd

[Install]
WantedBy=multi-user.target
```

Habilite e inicie:
```bash
sudo systemctl daemon-reload
sudo systemctl enable lightningos-manager
sudo systemctl start lightningos-manager
```

---

## FASE 6 — Validação e primeiro acesso

### 6.1 Verificar todos os serviços

```bash
for svc in bitcoin lnd lightningos-manager; do
  STATUS=$(systemctl is-active $svc 2>/dev/null || echo "não encontrado")
  echo "$svc: $STATUS"
done
sudo docker ps --format "{{.Names}}: {{.Status}}" | grep postgres
```

Esperado:
```
bitcoin: active
lnd: active
lightningos-manager: active
lightningos-postgres: Up ...
```

Teste a API:
```bash
curl -sk https://127.0.0.1:8443/api/health | python3 -m json.tool
```

---

### 6.2 Descobrir o IP e acessar a UI

```bash
hostname -I | awk '{print $1}'
```

Abra no navegador do seu computador (na mesma rede local):
```
https://SEU_IP_AQUI:8443
```

> O navegador vai mostrar um aviso de certificado autoassinado. Clique em
> **"Avançado"** → **"Prosseguir mesmo assim"**. Isso é esperado e seguro na sua rede local.

---

### 6.3 Completar o Wizard e criar a carteira

1. **Bitcoin Remote:** informe as credenciais RPC do seu Bitcoin Core
2. **Criar carteira:** o sistema vai gerar 24 palavras (seed)
3. **Anotar a seed:** escreva em papel físico — nunca em foto ou arquivo digital
4. **Desbloquear:** crie uma senha forte para a carteira

> 🔐 **As 24 palavras são a ÚNICA forma de recuperar seus fundos.**
> Guarde em local seguro e físico. Nunca fotografe. Nunca salve no computador ou na nuvem.

---

### 6.4 Configurar desbloqueio automático (opcional)

Após criar a carteira:

```bash
echo "SUA_SENHA_DA_CARTEIRA" | sudo tee /data/lnd/password.txt
sudo chmod 600 /data/lnd/password.txt
sudo chown lnd:lnd /data/lnd/password.txt
```

Descomente no `/mnt/ssd/lnd/lnd.conf`:
```
wallet-unlock-password-file=/data/lnd/password.txt
wallet-unlock-allow-create=true
```

Reinicie o LND:
```bash
sudo systemctl restart lnd
```

---

## 🎉 Seu nó está no ar!

Parabéns — você agora tem um nó Lightning totalmente operacional no Raspberry Pi 5 com Debian 12!

Seu nó está conectado à mainnet do Bitcoin, o LND está rodando e o LightningOS Light
gerencia tudo através de uma interface web em `https://SEU_IP:8443`.

**Próximos passos sugeridos:**
- Abra canais com peers bem conectados (500k+ sats recomendado para roteamento)
- Habilite o **Autofee** em Lightning Ops para gerenciar taxas automaticamente
- Configure **notificações no Telegram** para eventos on-chain e Lightning
- Explore a **App Store** para instalar o LNDg para monitoramento avançado

---

## ⚡ Gostou do guia? Manda uns sats!

Este guia foi escrito pela comunidade brasileira de Lightning Network com base em
experiência real — instalando e operando o LightningOS Light num Raspberry Pi 5 com Debian 12.

Horas de tentativa, erro e troubleshooting foram documentadas aqui para que você
não precisasse descobrir cada problema no caminho difícil. Se este guia te poupou
tempo, considere enviar uma doação simbólica para o nó que tornou isso possível!

```
⚡ Node:   RaspNode-BR
   Pubkey: 025b2f2679168c49dc72b6ac93ba8b9b7f782113eef613b7bc09bcf53ea05b792c
```

> Conecte via [Amboss](https://amboss.space/node/025b2f2679168c49dc72b6ac93ba8b9b7f782113eef613b7bc09bcf53ea05b792c)
> ou abra um canal diretamente — cada satoshi ajuda a comunidade brasileira de Lightning Network crescer! 🇧🇷

---

## Troubleshooting

| Problema | Causa provável | Solução |
|---|---|---|
| `Exec format error` no LND ou GoTTY | Binário `amd64` num sistema `arm64` | Baixe explicitamente a versão `linux-arm64` |
| LightningOS não abre na porta 8443 | Manager não iniciou corretamente | `journalctl -u lightningos-manager -n 50 --no-pager` |
| LND não conecta ao Tor | Usuário `lnd` não está no grupo `debian-tor` | `sudo usermod -aG debian-tor lnd && sudo systemctl restart lnd` |
| PostgreSQL inacessível | Container Docker parado | `cd /opt/lightningos-postgres && sudo docker compose up -d` |
| `log read failed: journalctl failed` na UI | Limitação de permissão do Debian | Aviso cosmético — nenhum impacto funcional |
| Conflito na porta 8080 | Outro serviço (ex: Nominatim) | Use `restlisten=127.0.0.1:8082` no `lnd.conf` |
| Erros de módulos no Go build | Cache de módulos corrompido | Execute `sudo /usr/local/go/bin/go mod download` antes |

---

## Referência — Caminhos importantes

| O quê | Caminho |
|---|---|
| Bitcoin Core conf | `/etc/bitcoin/bitcoin.conf` |
| Bitcoin dados | `/mnt/ssd/bitcoin` → symlink `/data/bitcoin` |
| LND conf | `/mnt/ssd/lnd/lnd.conf` |
| LND dados | `/mnt/ssd/lnd` → symlink `/data/lnd` |
| LightningOS config | `/etc/lightningos/config.yaml` |
| LightningOS secrets | `/etc/lightningos/secrets.env` |
| Manager binary | `/opt/lightningos/manager/lightningos-manager` |
| UI (interface web) | `/opt/lightningos/ui/` |
| PostgreSQL dados | `/mnt/ssd/postgres/data` |
| Repositório | `/opt/brln-os-light/lightningos-light` |

---

## Status esperado após instalação completa

| Serviço | Verificação |
|---|---|
| Bitcoin Core | ✅ `systemctl status bitcoin` |
| LND | ✅ `systemctl status lnd` |
| LightningOS Manager | ✅ `systemctl status lightningos-manager` |
| PostgreSQL (Docker) | ✅ `docker ps \| grep postgres` |
| Tor | ✅ `systemctl status tor` |
| Terminal Web (GoTTY) | ✅ `systemctl status lightningos-terminal` |

---

## Links úteis

- Projeto original: [github.com/jvxis/brln-os-light](https://github.com/jvxis/brln-os-light)
- Este fork: [github.com/MarcosJRcwb/brln-os-light](https://github.com/MarcosJRcwb/brln-os-light)
- Comunidade BR⚡LN: [services.br-ln.com](https://services.br-ln.com)
- Documentação LND: [docs.lightning.engineering](https://docs.lightning.engineering)
- Verificar seu nó: [amboss.space](https://amboss.space)
- Releases do LND: [github.com/lightningnetwork/lnd/releases](https://github.com/lightningnetwork/lnd/releases)

---

*Guia comunitário — Março 2026 — Comunidade Brasileira de Lightning Network*
