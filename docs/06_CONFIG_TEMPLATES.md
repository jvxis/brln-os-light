# FILE: docs/06_CONFIG_TEMPLATES.md

# Config Templates

## /etc/lightningos/config.yaml
server:
  host: "127.0.0.1"
  port: 8443
  tls_cert: "/etc/lightningos/tls/server.crt"
  tls_key: "/etc/lightningos/tls/server.key"

lnd:
  grpc_host: "127.0.0.1:10009"
  tls_cert_path: "/data/lnd/tls.cert"
  admin_macaroon_path: "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"
  # OBS: paths podem variar. Garantir que o instalador padronize.

bitcoin_remote:
  rpchost: "bitcoin.br-ln.com:8085"
  zmq_rawblock: "tcp://bitcoin.br-ln.com:28332"
  zmq_rawtx: "tcp://bitcoin.br-ln.com:28333"

postgres:
  # DSN vem de /etc/lightningos/secrets.env
  db_name: "lnd"

ui:
  static_dir: "/opt/lightningos/ui"

features:
  enable_login: false
  enable_bitcoin_local_placeholder: true
  enable_app_store_placeholder: true

## /etc/lightningos/secrets.env (chmod 660 root:lightningos)
# Postgres DSN for LND backend
LND_PG_DSN=postgres://lndpg:CHANGE_ME@127.0.0.1:5432/lnd?sslmode=disable

# Bitcoin remote credentials (filled by wizard)
BITCOIN_RPC_USER=
BITCOIN_RPC_PASS=

