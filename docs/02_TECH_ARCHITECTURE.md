# FILE: docs/02_TECH_ARCHITECTURE.md

# Arquitetura Técnica (v0.1)

## Componentes
1) LightningOS Manager (Go)
- Servidor HTTPS em 127.0.0.1:8443
- Serve UI estática (SPA) e API REST JSON
- Coleta métricas do sistema
- Interage com systemd (start/stop/restart)
- Interage com LND via gRPC (localhost)
- Valida Bitcoin remoto via RPC/ZMQ

2) UI (React + Tailwind)
- SPA
- Wizard e Dashboard
- Chamadas para API do Manager

3) PostgreSQL
- DB do LND
- Usuário/DB dedicados

4) LND
- Binário instalado no sistema
- Config em /data/lnd/lnd.conf (preferido)
- Dados em /data/lnd (ou /home/lnd/.lnd conforme setup)
- gRPC apenas localhost

## Usuários do sistema
- `lnd`: roda lnd.service
- `lightningos`: roda lightningos-manager.service
- Postgres roda padrão do sistema

## Diretórios
- /opt/lightningos/manager (binário)
- /opt/lightningos/ui (build SPA)
- /etc/lightningos/config.yaml
- /etc/lightningos/secrets.env (660 root:lightningos)
- /var/lib/lightningos/state.db (opcional)
- /var/log/lightningos/manager.log

## Networking
- Manager: 127.0.0.1:8443 (HTTPS)
- LND gRPC: 127.0.0.1:10009
- LND REST (opcional): 127.0.0.1:8080
- Bitcoin remoto:
  - RPC: bitcoin.br-ln.com:8085
  - ZMQ: 28332/28333 (tcp)

## Integração com LND (gRPC)
- Manager lê macaroon e TLS cert do LND (paths configuráveis).
- Recomendação: manager roda com permissões para ler macaroon/tls apenas via grupo ou ACL específica.

## SMART / Disk Lifespan
- Leitura via `smartctl` (smartmontools)
- Parse de:
  - NVMe: Percentage Used + Power On Hours
  - SATA: Power_On_Hours, Reallocated, Pending, etc.
- Heurística:
  - wear_rate_per_hour = percent_used / power_on_hours
  - hours_left = (100 - percent_used) / wear_rate_per_hour
  - days_left = hours_left / 24
- Estados:
  - OK: wear < 70% e sem flags SMART críticas
  - WARN: wear >= 70% ou alguns atributos preocupantes
  - ERR: wear >= 90% ou SMART critical

# FILE: docs/02_TECH_ARCHITECTURE.md  (APPEND)

## Configura????o do LND
- Arquivo ?nico: /data/lnd/lnd.conf
- A UI altera apenas chaves suportadas (alias, minchansize, maxchansize) na se??o [Application Options].
- O servi?o inicia com lnd, usando /home/lnd/.lnd (symlink para /data/lnd).

