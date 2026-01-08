# FILE: docs/03_API_SPEC.md

# API Spec — LightningOS Manager (v0.1)

Base URL: https://127.0.0.1:8443

## Auth
MVP: sessão local simples com senha definida no primeiro acesso (opcional).
Se quiser simplificar: sem login no MVP, assumindo LAN/VPN.
Recomendação: incluir senha simples em v0.1.1.

## Endpoints

### GET /api/health
Retorna status geral.
Response:
{
  "status": "OK|WARN|ERR",
  "issues": [
    {"component":"lnd","level":"ERR","message":"LND is locked"},
    {"component":"bitcoin","level":"WARN","message":"ZMQ not reachable"}
  ],
  "timestamp": "RFC3339"
}

### GET /api/system
{
  "uptime_sec": 12345,
  "cpu_load_1": 0.42,
  "cpu_percent": 12.3,
  "ram_total_mb": 32000,
  "ram_used_mb": 8400,
  "disk": [
    {"mount":"/","total_gb": 512,"used_gb": 210,"used_percent": 41.0}
  ],
  "temperature_c": 55.2
}

### GET /api/disk
[
  {
    "device": "/dev/nvme0",
    "type": "NVMe",
    "power_on_hours": 1234,
    "wear_percent_used": 12,
    "days_left_estimate": 2400,
    "smart_status": "PASSED|FAILED|UNKNOWN",
    "alerts": ["wear_warn"]
  }
]

### GET /api/postgres
{
  "service_active": true,
  "db_name": "lnd",
  "db_size_mb": 512,
  "connections": 8
}

### GET /api/bitcoin
{
  "mode": "remote",
  "rpchost": "bitcoin.br-ln.com:8085",
  "rpc_ok": true,
  "chain": "main",
  "blocks": 931396,
  "headers": 931396,
  "verification_progress": 0.9999,
  "initial_block_download": false,
  "zmq_rawblock_ok": true,
  "zmq_rawtx_ok": true
}

### GET /api/lnd/status
{
  "service_active": true,
  "wallet_state": "locked|unlocked|unknown",
  "synced_to_chain": true,
  "synced_to_graph": true,
  "block_height": 900000,
  "channels": {"active": 10, "inactive": 1},
  "balances": {"onchain_sat": 123456, "lightning_sat": 789012}
}

### POST /api/wizard/bitcoin-remote
Body:
{
  "rpcuser": "string",
  "rpcpass": "string"
}
Behavior:
- Validar RPC e ZMQ com host padrão
- Salvar credenciais de forma segura
- Atualizar lnd.conf
- Restart LND
Response:
{
  "ok": true,
  "info": {
    "chain": "main",
    "blocks": 931396,
    "headers": 931396,
    "verification_progress": 0.9999,
    "initial_block_download": false
  }
}

### POST /api/wizard/lnd/create-wallet
Body:
{
  "seed_passphrase": "string (optional)"
}
Behavior:
- Chamar gRPC: GenSeed
- Retornar a seed para exibir UMA vez
- NÃO armazenar seed
Response:
{
  "seed_words": ["...24 words..."]
}

### POST /api/wizard/lnd/init-wallet
Body:
{
  "wallet_password": "string",
  "seed_words": ["...24 words..."]
}
Behavior:
- Chamar InitWallet
Response:
{ "ok": true }

### POST /api/wizard/lnd/unlock
Body:
{
  "wallet_password": "string"
}
Behavior:
- Chamar UnlockWallet
Response:
{ "ok": true }

### POST /api/actions/restart
Body:
{ "service": "lnd|lightningos-manager|postgresql" }
Response:
{ "ok": true }

### GET /api/logs?service=lnd&lines=200
Response:
{
  "service":"lnd",
  "lines":[ "....", "...." ]
}

# FILE: docs/03_API_SPEC.md  (APPEND)

### GET /api/lnd/config
Retorna os campos suportados e valores atuais.
Obs: `raw_user_conf` contém o conteúdo do /data/lnd/lnd.conf.
Response:
{
  "supported": {
    "alias": true,
    "min_channel_size_sat": true,
    "max_channel_size_sat": true
  },
  "current": {
    "alias": "MyNode",
    "min_channel_size_sat": 20000,
    "max_channel_size_sat": 16777215
  },
  "raw_user_conf": "[Application Options]\nalias=...\n..."
}

### POST /api/lnd/config
Atualiza lnd.conf com valores validados e opcionalmente reinicia o LND.
Body:
{
  "alias": "string",
  "min_channel_size_sat": 20000,
  "max_channel_size_sat": 5000000,
  "apply_now": true
}
Response:
{ "ok": true }

### POST /api/lnd/config/raw
Modo avançado: substitui o conteúdo do lnd.conf.
Body:
{
  "raw_user_conf": "string",
  "apply_now": true
}
Response:
{ "ok": true }
