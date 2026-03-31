# Config Templates

## /etc/lightningos/config.yaml
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
  rpchost: "bitcoin.br-ln.com:8085"
  zmq_rawblock: "tcp://bitcoin.br-ln.com:28332"
  zmq_rawtx: "tcp://bitcoin.br-ln.com:28333"

postgres:
  db_name: "lnd"

ui:
  static_dir: "/opt/lightningos/ui"

features:
  enable_login: true
  enable_bitcoin_local_placeholder: true
  enable_app_store_placeholder: true

## /etc/lightningos/secrets.env (chmod 660 root:lightningos)
# Postgres DSN for LND backend
LND_PG_DSN=postgres://lndpg:CHANGE_ME@127.0.0.1:5432/lnd?sslmode=disable

# Postgres DSN for LightningOS notifications and reports
NOTIFICATIONS_PG_DSN=postgres://losapp:CHANGE_ME@127.0.0.1:5432/lightningos?sslmode=disable

# Admin DSN for provisioning the notifications DB/user
NOTIFICATIONS_PG_ADMIN_DSN=postgres://losadmin:CHANGE_ME@127.0.0.1:5432/postgres?sslmode=disable

# Bitcoin remote credentials (filled by wizard)
BITCOIN_RPC_USER=
BITCOIN_RPC_PASS=

# Telegram backup (optional)
NOTIFICATIONS_TG_BOT_TOKEN=
NOTIFICATIONS_TG_CHAT_ID=

# Web terminal (GoTTY)
TERMINAL_ENABLED=0
TERMINAL_CREDENTIAL=
TERMINAL_ALLOW_WRITE=1
TERMINAL_PORT=7681
TERMINAL_OPERATOR_USER=losop
TERMINAL_OPERATOR_PASSWORD=
TERMINAL_TERM=xterm
TERMINAL_SHELL=/bin/bash
TERMINAL_WS_ORIGIN=

## /data/lnd/lnd.conf
- Managed by the installer and UI.
- See docs/07_LND_CONF_TEMPLATE.md for the full template.
