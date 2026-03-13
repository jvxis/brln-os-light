# LightningOS Light

<img width="1920" height="1080" alt="logo" src="https://github.com/user-attachments/assets/504ec23e-31f8-407a-a848-3fa4ce3ec1f9" />

[Clique aqui](https://github.com/jvxis/brln-os-light/blob/main/README.md) para ver a versão em inglês (fonte da verdade).

LightningOS Light é um instalador completo de daemon de nó Lightning, com gerenciador de nó, assistente guiado, dashboard e carteira. O manager serve UI e API via HTTPS em `0.0.0.0:8443` por padrão para acesso em LAN (defina `server.host: "127.0.0.1"` para acesso somente local) e integra com systemd, Postgres, smartctl, Tor/i2pd e LND gRPC.

<img width="1494" height="1045" alt="image" src="https://github.com/user-attachments/assets/8fb801c0-4946-48d8-8c24-c36a53d193b3" />
<img width="1491" height="903" alt="image" src="https://github.com/user-attachments/assets/cfda34d5-bccc-4b18-9970-bad494ae77b3" />
<img width="1576" height="1337" alt="image" src="https://github.com/user-attachments/assets/019cfff2-f354-4c2b-a595-2a15bb228864" />
<img width="1280" height="660" alt="image" src="https://github.com/user-attachments/assets/84489b07-8397-4195-b0d4-7e332618666d" />
<img width="1779" height="1106" alt="image" src="https://github.com/user-attachments/assets/fb505e13-f7d1-4cbe-98de-92d65f562f44" />

## Destaques
- Apenas Mainnet (Bitcoin remoto por padrão)
- Sem Docker na stack principal
- LND gerenciado via systemd, gRPC em localhost
- A seed phrase nunca é persistida nem registrada em logs
- Assistente para credenciais RPC do Bitcoin e setup de carteira
- Suite Lightning Ops: peers/canais, Rebalance Center, Autofee, Aposentar Node, sinais HTLC e Channel Auto Heal
- Chat Keysend: 1 sat por mensagem + taxas de roteamento, indicadores de não lidas, retenção de 30 dias
- Notificações em tempo real (on-chain, Lightning, canais, forwards, rebalances)
- Notificações Telegram: backups SCB, resumos financeiros, comandos sob demanda `/scb` e `/balances`
- Relatórios diários de roteamento (timer + backfill + API live)
- App Store: LNDg, Peerswap (psweb), Elements, Bitcoin Core
- Gestão de Bitcoin Local (status + config) e visualizador de logs

## Estrutura do repositório
- `cmd/lightningos-manager`: backend Go (API + UI estática)
- `ui`: UI React + Tailwind
- `templates`: units systemd e templates de configuração
- `install.sh`: instalador idempotente (wrapper em `scripts/install.sh`)
- `configs/config.yaml`: configuração local de desenvolvimento

## Instalação (Ubuntu Server)
O instalador provisiona tudo que é necessário em um Ubuntu limpo:
- Postgres, smartmontools, curl, jq, ca-certificates, openssl, build tools
- Tor (ControlPort habilitado) + i2pd habilitado por padrão
- Go 1.22.x e Node.js 20.x (se ausentes ou antigos)
- Binários do LND (padrão `v0.20.0-beta`)
- Binário do LightningOS Manager (compilado localmente)
- Build da UI (compilada localmente)
- Serviços systemd e templates de configuração
- Certificado TLS autoassinado

Uso:
```bash
git clone https://github.com/jvxis/brln-os-light
cd brln-os-light/lightningos-light
sudo ./install.sh
```

Se você já clonou e está em `brln-os-light`, use:
```bash
cd lightningos-light
sudo ./install.sh
```

### Instalação via curl (bootstrap)
Isso baixa o repo (ou executa `git pull` se já existir) e depois roda `lightningos-light/install.sh`.
```bash
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo ACCEPT_MIT_LICENSE=1 bash
```

Overrides opcionais:
```bash
# Usar outro caminho de clone
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo BRLN_DIR=/opt/brln-os-light bash

# Fixar branch/tag
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo BRLN_BRANCH=main bash

# Usar outra URL de repositório
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo REPO_URL=https://github.com/jvxis/brln-os-light bash
```

Nota de UFW (App Store/LNDg):
Se o LNDg não alcançar o LND gRPC e o UFW estiver ativo, tráfego Docker-to-host pode ser bloqueado.
Rode os checks abaixo e permita a interface bridge usada pela rede do LNDg:
```bash
sudo docker exec -it lndg-lndg-1 getent hosts host.docker.internal
sudo docker exec -it lndg-lndg-1 bash -lc 'timeout 3 bash -lc "</dev/tcp/host.docker.internal/10009" && echo OK || echo FAIL'
sudo docker network inspect lndg_default --format '{{.Id}}'
# nome da bridge = br-<primeiros 12 caracteres do id>
sudo ufw allow in on br-<id> to any port 10009 proto tcp
```
Se ainda falhar, tente:
```bash
sudo iptables -I INPUT -i br-<id> -p tcp --dport 10009 -j ACCEPT
```

**Atenção (nós existentes):** Se você já tem um nó Lightning com LND/Bitcoin rodando, não use `install.sh`.
Siga o guia de Nó Existente:
- PT-BR: `docs/13_EXISTING_NODE_GUIDE_PT_BR.md`
- EN: `docs/14_EXISTING_NODE_GUIDE_EN.md`

Acesse a UI de outra máquina na mesma LAN:
`https://<IP_LAN_DO_SERVIDOR>:8443`

Notas:
- Você pode sobrescrever a URL do LND com `LND_URL=...` ou a versão com `LND_VERSION=...`.
- O instalador gera uma role no Postgres e atualiza `LND_PG_DSN` em `/etc/lightningos/secrets.env`.
- O rótulo de versão da UI vem de `ui/public/version.txt`.
- PostgreSQL usa o repositório PGDG por padrão. Defina `POSTGRES_VERSION=18` (ou outra major) para sobrescrever.
- Tor usa o repositório Tor Project quando disponível. Se o codinome Ubuntu não for suportado, faz fallback para `jammy`.

## Permissões do instalador (o que `install.sh` aplica)
- Usuários:
  - `lnd` (usuário de sistema, dono de `/data/lnd`)
  - `lightningos` (usuário de sistema, executa o manager service)
- Grupos:
  - `lightningos` nos grupos `lnd` e `systemd-journal`
  - `lnd` no grupo `debian-tor`
- Caminhos-chave:
  - `/etc/lightningos` e `/etc/lightningos/tls`: `root:lightningos`, `chmod 750`
  - `/etc/lightningos/secrets.env`: `root:lightningos`, `chmod 660`
  - `/data/lnd`: `lnd:lnd`, `chmod 750`
  - `/data/lnd/data/chain/bitcoin/mainnet`: `lnd:lnd`, `chmod 750`
  - `/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon`: `lnd:lnd`, `chmod 640`

## Caminhos de configuração
- `/etc/lightningos/config.yaml`
- `/etc/lightningos/secrets.env` (chmod 660)
- `/data/lnd/lnd.conf`
- `/data/lnd` (diretório de dados LND)

## Notificações e backups
LightningOS Light inclui um sistema de notificações em tempo real que rastreia:
- Transações on-chain (recebidas/enviadas)
- Invoices Lightning (liquidadas) e pagamentos (enviados)
- Eventos de canal (abertura, fechamento, pendente)
- Forwards e rebalances

Notificações são armazenadas em um Postgres dedicado (veja `NOTIFICATIONS_PG_DSN` em `/etc/lightningos/secrets.env`).

## Chat (Keysend)
O chat Keysend está disponível na UI e mira apenas peers online.
- Cada mensagem envia 1 sat + taxas de roteamento.
- Mensagens são armazenadas localmente em `/var/lib/lightningos/chat/messages.jsonl` e retidas por 30 dias.
- Peers com não lidas ficam destacados até o chat ser aberto.

Notificações Telegram:
- Configure na UI: Notifications -> Telegram.
- A UI inclui um card de regras gerais com defaults operacionais.
- Backup SCB em abertura/fechamento de canal (toggle).
- Resumo financeiro agendado (intervalos de 1 a 12 horas).
- Comandos sob demanda: `/scb` (backup) e `/balances` (resumo).
- `/scb` e `/balances` são auto-registrados no menu do bot do Telegram.
- Mensagens de backup SCB incluem contexto do alias do peer na legenda.
- Token do bot vem do @BotFather e chat id vem do @userinfobot.
- Somente chat direto; deixar ambos os campos vazios desativa Telegram.

Chaves de ambiente:
- `NOTIFICATIONS_TG_BOT_TOKEN`
- `NOTIFICATIONS_TG_CHAT_ID`

## Relatórios
Relatórios diários de roteamento são calculados à meia-noite no horário local e armazenados no Postgres (mesmo DB/usuário de notificações).

Agenda:
- `lightningos-reports.timer` executa `lightningos-reports.service` às `00:00` local.
- Execução manual: `/opt/lightningos/manager/lightningos-manager reports-run --date YYYY-MM-DD` (padrão: ontem).
- Backfill: `/opt/lightningos/manager/lightningos-manager reports-backfill --from YYYY-MM-DD --to YYYY-MM-DD` (máximo padrão de 730 dias; use `--max-days N` para sobrescrever).
- Pin opcional de timezone: defina `REPORTS_TIMEZONE=America/Sao_Paulo` em `/etc/lightningos/secrets.env` para forçar relatórios diários, backfill e live no mesmo timezone IANA.

Tabela armazenada: `reports_daily`
- `report_date` (DATE, dia local)
- `forward_fee_revenue_sats`
- `forward_fee_revenue_msat`
- `rebalance_fee_cost_sats`
- `rebalance_fee_cost_msat`
- `net_routing_profit_sats`
- `net_routing_profit_msat`
- `forward_count`
- `rebalance_count`
- `routed_volume_sats`
- `routed_volume_msat`
- `onchain_balance_sats`
- `lightning_balance_sats`
- `total_balance_sats`
- `created_at`, `updated_at`

Endpoints de API:
- `GET /api/reports/range?range=d-1|month|3m|6m|12m|all` (month = últimos 30 dias)
- `GET /api/reports/custom?from=YYYY-MM-DD&to=YYYY-MM-DD` (máx. 730 dias)
- `GET /api/reports/summary?range=...`
- `GET /api/reports/live` (hoje 00:00 local -> agora, cache ~60s)

## Lightning Ops (mapa de funcionalidades)
- Gestão de canais: controles de peer/canal, atualizações de policy e refinamentos de card/saldo de canal.
- Rebalance Center: rebalances manuais + automáticos com targeting por score, watchdogs, pre-probing, guardrails de ROI e auto-restart opcional no modo manual.
- Autofee: automação de taxas por canal com âncoras de custo, seed Amboss, integração de sinais HTLC, calibração por tamanho/liquidez do nó, scheduler/manual run e histórico detalhado.
- Aposentar Node (Node Retirement): fluxo guiado de descomissionamento seguro com linha do tempo de sessão, controle de fechamento cooperativo, tratamento de exceções e reconciliação on-chain.
- HTLC Manager: telemetria HTLC com histerese usada pelo Autofee e por decisões de liquidez.
- Channel Auto Heal + Tor peers checker: guardrails operacionais para confiabilidade de peer/canal.
- Health checks: opção de follow-bitcoin para fluxos de saúde de LND/nó.

## Rebalance Center
Rebalance Center é um otimizador de liquidez de entrada (local/outbound) para LND. Ele pode rodar rebalances manuais por canal ou varreduras totalmente automáticas que enfileiram rebalances com base em ROI e restrições de orçamento. Um rebalance só avança quando **outgoing fee > peer fee**, para que você nunca pague mais do que a cobrança do peer sem spread positivo. Custos são rastreados por notificações (fee msat) e agregados em custo live + gasto diário auto/manual.

Comportamento principal:
- Rebalances manuais ignoram orçamento diário e podem ser iniciados por canal.
- Rebalances automáticos respeitam orçamento diário e só miram canais marcados explicitamente como `Auto`.
- Canais de origem são selecionados entre os com liquidez local suficiente e não excluídos; um canal preenchido por rebalance fica **protegido** e não pode ser usado como origem até regras de payback liberarem.
- Alvos são escolhidos quando o déficit de liquidez outbound passa do deadband e o spread de taxa é positivo; estimativa de ROI usa receita de roteamento dos últimos 7 dias vs custo estimado de rebalance.
- Alvos automáticos são ranqueados por **economic score** = (ganho esperado - custo estimado), priorizando canais de maior margem.
- Um **profit guardrail** impede enqueue automático quando ganho esperado é menor que custo estimado (quando ambos são conhecidos). Se ROI for indeterminado (cost = 0 com spread positivo), auto continua permitido.
- Seleção de origem é ponderada por histórico do par: pares recentes bem-sucedidos com taxas menores são priorizados, e falhas recentes são despriorizadas.
- A visão geral mostra **Last scan** em horário local e status da varredura (ex.: sem origens, sem candidatos, orçamento esgotado), além de telemetria econômica (top score, skips por profit guardrail) e detalhes opcionais de skip.
- Rebalances manuais podem opcionalmente usar **auto-restart** (toggle por canal) com cooldown de 60s até o alvo ser alcançado.
- **Pre-probing** de rota roda antes do envio, buscando o maior valor viável na rota.

Channel Workbench:
- Define percentual-alvo de outbound por canal.
- Toggle `Auto` para permitir que auto mode rebalanceie o canal.
- Toggle no ícone de restart para auto-restart de rebalances manuais do canal.
- Toggle `Exclude source` para bloquear um canal como origem.
- Ordenação: **Economic** (baseada em score) ou **Emptiest** (menor % local primeiro).

Codificação por cor (linhas de canal):
- Fundo verde = origem elegível (pode financiar rebalances).
- Fundo vermelho = alvo elegível (auto habilitado e precisando de outbound).
- Fundo âmbar = alvo potencial (precisa de outbound, mas auto desabilitado).

Parâmetros de configuração:
- Configurações somente auto: `Enable auto rebalance`, `Scan interval (sec)`, `Daily budget (% of revenue)`.
- `Enable auto rebalance`: liga/desliga varredura automática.
- `Scan interval (sec)`: frequência da varredura automática.
- `Daily budget (% of revenue)`: percentual da receita de roteamento das últimas 24h alocado para auto-rebalances.
- `Deadband (%)`: déficit mínimo de outbound antes de um canal virar alvo.
- `Minimum local for source (%)`: liquidez local mínima para um canal ser origem.
- `Economic ratio`: fração da taxa outbound do canal alvo (base+ppm) usada como limite máximo de taxa.
- `Econ ratio max (ppm)`: teto opcional para o limite de taxa ao usar economic ratio (0 = sem teto).
- `Fee limit (ppm)`: sobrescreve economic ratio com limite fixo máximo de taxa ppm (0 = desativado).
- `Subtract source fees`: reduz orçamento de taxa com estimativa de source fees (mais conservador).
- `ROI minimum`: ROI mínimo estimado (receita 7d / custo estimado) para enfileirar jobs auto.
- `Max concurrent`: máximo de rebalances simultâneos.
- `Minimum (sats)`: âncora legada de início das tentativas; com split desligado, também vira o piso efetivo de probe/execução.
- `Maximum (sats)`: limite superior do tamanho de rebalance (0 = ilimitado).
- `Fee ladder steps`: número de fee caps tentados do menor para o maior antes de desistir.
- `Amount probe steps`: número de sondas de valor (do maior para o menor) quando ocorre falha temporária no último salto.
- `Fail tolerance (ppm)`: probing para quando delta entre valores ficar abaixo desse limite.
- `Adaptive amount probing`: limita a próxima tentativa com base no último valor bem-sucedido.
- `Attempt timeout (sec)`: tempo máximo por tentativa antes de seguir para próxima taxa/valor.
- `Rebalance timeout (sec)`: tempo máximo por job de rebalance (auto ou manual).
- `Mission control half-life (sec)`: tempo de decaimento de falhas no mission control (menor = esquece mais rápido, 0 = padrão do LND).
- `Payback policy`: três modos podem ser habilitados juntos.
- `Release by payback`: libera liquidez protegida quando receita de roteamento paga o custo do rebalance.
- `Release by time`: libera após `Unlock days` desde o último rebalance.
- `Critical mode`: libera uma fração quando origens ficam escassas por várias varreduras.
- `Unlock days`: número de dias para desbloqueio por tempo.
- `Critical release (%)`: percentual de liquidez protegida liberada por ciclo crítico.
- `Critical cycles`: varreduras consecutivas com poucas origens antes de acionar liberação crítica.
- `Critical min sources`: mínimo de canais origem elegíveis para evitar modo crítico.
- `Critical min available sats`: liquidez total mínima de origem para evitar modo crítico.

Controles de split mínimo (`Split min (probe/execute)`):
- Objetivo: separar a âncora econômica de início (`Minimum (sats)`) dos pisos rígidos de probe e execução.
- `Split min (probe/execute)`: habilita pisos separados para probe e execução.
- `Min probe amount (sats)` (`min_probe_sat`, padrão `0`): piso mínimo permitido no probe quando split está ligado. `0` herda `Minimum (sats)`.
- `Min execute amount (sats)` (`min_execute_sat`, padrão `0`): piso mínimo permitido para execução real quando split está ligado. `0` herda `Minimum (sats)`.
Interações importantes:
- As tentativas continuam começando com âncora em `Minimum (sats)` (compatível com comportamento legado).
- Com split ligado, o probe pode descer até `min_probe_sat` e a execução é bloqueada abaixo de `min_execute_sat`.
- Com split ligado, candidatos auto abaixo do piso de execução são descartados cedo.
Recomendação prática:
- Manter `Min execute amount (sats)` igual ao `Min probe amount (sats)`, a menos que você queira explicitamente probe mais baixo que execução.
- Use split quando quiser ampliar descoberta de rota sem liberar execução abaixo do piso desejado.

MSPR (`MSPR (Paralelo Multi-Source)`):
- Objetivo: aumentar a chance de sucesso no primeiro passe tentando shards em múltiplas fontes em paralelo, antes do fallback legado sequencial.
- `Enable MSPR` (`mpp_enabled`, padrão `false`): habilita o prepass MSPR com execução real.
- `MSPR for auto jobs only` (`mpp_auto_only`, padrão `false`): quando ligado, só jobs auto usam MSPR; jobs manuais ficam no legado.
- `Max shards` (`mpp_max_shards`, padrão `8`, faixa `1..8`): máximo de shards planejados na rodada MSPR.
- `Parallel workers` (`mpp_parallelism`, padrão `6`, faixa `1..max_shards`): número máximo de tentativas de shard concorrentes na rodada.
- `Min shard amount (sats)` (`mpp_min_shard_sat`, padrão `1000`): tamanho mínimo de shard planejado pelo MSPR.
- `Round timeout (sec)` (`mpp_round_timeout_sec`, padrão `30`): tempo máximo da rodada MSPR antes de cair para tentativas legadas.
Modelo de execução:
- O MSPR roda um prepass paralelo (com plano de shards e workers).
- Shards com sucesso são executados e contabilizados imediatamente.
- Após o prepass, o job continua na mesma fila/fluxo legado para o valor remanescente.
- Falhas de shard aparecem no histórico com prefixo `mpp shard:` no motivo.
Recomendação prática:
- Comece com `max_shards=8`, `parallel_workers=6`, `min_shard=1000`, `round_timeout=30`.
- Se os primeiros shards ainda estiverem grandes, aumente shards (até o limite) e mantenha workers menor ou igual a shards.
- Se o nó estiver sensível a carga, reduza primeiro os workers paralelos (não o número de shards).

Quando usar cada modo:
- Mais conservador (foco legado): split desligado e MSPR desligado.
- Melhor descoberta com piso controlado: split ligado, `min_probe=min_execute` (ex.: `1000`), mantendo `Minimum (sats)` como alvo econômico (ex.: `30000`).
- Maior taxa de acerto no primeiro passe em grafo movimentado: split ligado + MSPR ligado, com ajuste gradual por métricas MSPR 24h.

## Lightning Ops: Autofee
O Autofee ajusta **outbound fees** por canal com esta prioridade:
1. Manter economia unitaria positiva (lucro).
2. Manter movimento do node (evitar liquidez presa).
3. Manter updates estaveis e explicaveis.

Ele usa historico local de roteamento/rebalance (notificacoes no Postgres), seed opcional da Amboss, sinais HTLC e guardrails calibrados.

Parametros da UI:
- `Enable autofee`: liga/desliga global.
- `Profile`: Conservative / Moderate / Aggressive.
- `Lookback window (days)`: 5 a 21 dias (janela principal).
- `Run interval (hours)`: minimo 1 hora.
- `Cooldown up / down (hours)`: tempo minimo entre aumentos/reducoes.
- `Min fee (ppm)` e `Max fee (ppm)`: limites rigidos.
- `Rebalance cost mode`: `Per-channel`, `Global` ou `Blend`.
- `Amboss fee reference`: seed externo opcional.
- `Inbound passive rebalance`, `Discovery mode`, `Explorer mode`, `Revenue floor`, `Circuit breaker`, `Extreme drain`, `Super source`.
- `HTLC signal integration` e `HTLC mode` (`observe_only`, `policy_only`, `full`).

Configuracoes de movimento (gaveta no card do Autofee):
- Use `0` ou deixe vazio para manter o padrao do perfil selecionado.
- `Cooldown up (hours)`: espera minima antes de nova alta de fee. Maior = sobe mais devagar; menor = reage mais rapido.
- `Cooldown down (hours)`: espera minima antes de nova queda de fee. Maior = cai mais devagar; menor = reduz taxas mais rapido.
- `Step cap override (%)`: mudanca maxima permitida por rodada. Maior = mais movimento por execucao; menor = comportamento mais suave.
- `Discovery down cap override (%)`: cap extra de queda em cenarios de discovery. Maior = destrava e abaixa mais rapido.
- `Stall relax gap trigger (%)`: gap minimo entre fee atual e alvo antes do stall-relax suavizar o piso. Menor = relaxa antes; maior = preserva o piso por mais tempo.
- `Inbound discount max ratio (%)`: desconto inbound maximo como fracao da fee outbound aplicada. Maior = pricing inbound mais agressivo em canais tipo sink.
- `Low-flow floor factor override (%)`: multiplicador aplicado ao piso quando o fluxo de saida esta baixo. Maior = mantem fees mais altas; menor = permite pisos mais baixos.
- `Global lock soften min out ratio (%)`: out ratio minimo para o lock global por margem negativa poder suavizar. Menor = mais canais ficam elegiveis.
- `Global lock soften max drop to peg (%)`: queda maxima permitida em direcao ao peg quando o lock global e suavizado. Menor = permite cortes mais profundos.
- `HTLC min attempts 60m override`: minimo de tentativas HTLC para o canal ser classificado por comportamento HTLC. Menor = mais reacoes guiadas por HTLC.
- `HTLC policy fail rate override (%)`: limite da taxa de falha policy para sinais HTLC. Menor = dispara `policy-hot` com mais facilidade.
- `HTLC liquidity fail rate override (%)`: limite da taxa de falha de liquidez para sinais HTLC. Menor = dispara `liquidity-hot` com mais facilidade.

Pipeline de decisao (por canal):
1. Monta referencias:
- `out_ppm7d` da janela principal.
- `rebal_ppm7d` do modo de custo selecionado.
- Seed (`Amboss` -> fallback para memoria/outrate/default).
2. Classifica comportamento (`sink`, `source`, `router`, `unknown`) e estado de liquidez.
3. Calcula target bruto com seed, out ratio, tendencia/margem, pressao HTLC e heuristicas de lucro.
4. Aplica controles de discovery/explorer/stagnation/profit-protect/locks globais.
5. Monta pilha de floor (`rebal`, `rebal-sink`, `outrate`, `peg`, `revfloor`, `stagnation`, `no-signal`).
6. Aplica step cap e cooldown, e decide `apply` ou `keep`.

Melhorias recentes de comportamento:
- Bootstrap para canais inbound novos com subida gradual e controlada (`new-inbound`, `bootstrap`).
- Relaxamento adaptativo de floor em canais travados (`floor-relax-stall`) para evitar lock prolongado em fee alta.
- Bloqueio de micro-subidas por floor quando o sinal e fraco (exceto cenarios fortes como surge/new-inbound), reduzindo churn.
- Forecast baseado na fee efetivamente aplicada (nao apenas candidata), deixando linhas `keep` mais coerentes.

Janelas de dados e regras de fallback:
- Janela principal: `lookback` configuravel (5-21d).
- Janelas extras sempre calculadas:
- `1d`: movimento recente e estagnacao.
- `7d`: referencia canonica de `out_ppm7d`.
- `21d`: fallback apenas quando falta dado recente e ha qualidade minima.
- Fallback de outrate 21d exige:
- pelo menos `5` forwards e
- volume outbound >= `max(50k sats, 0.5% da capacidade do canal)`.
- Fallback de rebal 21d exige volume rebalanceado >= `max(30k sats, 0.3% da capacidade)`.
- Se nao houver sinal valido de out/rebal e o canal estiver ocioso, o Autofee evita subida cega (`no-signal-noup`).

Comportamento de sinais HTLC:
- Janela de sinal segue a cadencia: `max(run_interval, 60m)`.
- Limites minimos de amostra/falha sao autoescalados por tamanho do node e classe de liquidez.
- Linha de resumo mostra: `htlc_liq_hot`, `htlc_policy_hot`, `htlc_forward_hot`, `htlc_low_sample`, `reversal_blocked`, `reversal_confirmed`, `downcap_general`, `downcap_low_sample`, `floor_relax`, `htlc_window`.
- Linha por canal pode mostrar: `htlc<window>m a=<attempts> p=<policy_fails> l=<liquidity_fails> f=<forward_fails> u=<unclassified>`.

Calibracao automatica:
- Classe de tamanho do node (`small`, `medium`, `large`, `xl`) por capacidade total e numero de canais.
- Classe de liquidez do node (`drained`, `balanced`, `full`) por local ratio.
- Linha de calibracao mostra: `low_out x<factor> t<...> p<...>`.
- Isso ajusta dinamicamente os thresholds de low-out (menos agressivo em node balanced, mais protetor em node drained).

Linhas de Autofee Results:
- Header: tipo da execucao + timestamp.
- Summary: contadores de up/down/flat e skips.
- Seed line: uso de Amboss/fallbacks.
- Calibration line: classes do node, low_out, revfloor, fatores globais HTLC.
- Linha por canal: `set/keep`, `target`, `out_ratio`, `out_ppm7d`, `rebal_ppm7d`, `seed`, `floor`, `margin`, `rev_share`, tags, contadores HTLC e forecast.

Glossario de tags (Autofee Results):
- Referencia completa: `docs/AUTOFEE_TAG_GLOSSARIO_PT_BR.md` (PT-BR) e `docs/AUTOFEE_TAG_GLOSSARY_EN.md` (EN).
- Papel do canal e tendencia:
- `sink`, `source`, `router`, `unknown`, `trend-up`, `trend-down`, `trend-flat`.
- Controles de movimento:
- `stepcap`, `stepcap-lock`, `floor-lock`, `floor-relax-stall`, `reversal-guard`, `reversal-confirmed`, `downcap-general`, `htlc-low-sample-downcap`, `hold-small`, `same-ppm`, `cooldown`, `cooldown-profit`, `cooldown-skip`, `rebal-recent`, `rebal-attempt`, `rebal-recent-noup`.
- Controles de lucro e margem:
- `neg-margin`, `negm+X%`, `no-down-low`, `no-down-neg-margin`, `global-neg-lock`, `lock-skip-no-chan-rebal`, `lock-skip-sink-profit`, `profit-protect-lock`, `profit-protect-relax`.
- Floors/anchors de mercado:
- `outrate-floor`, `peg`, `peg-grace`, `peg-demand`, `revfloor`, `sink-floor`.
- Controles adaptativos:
- `circuit-breaker`, `extreme-drain`, `extreme-drain-unlock`, `extreme-drain-turbo`.
- Estagnacao e anti-lock:
- `stagnation`, `stagnation-rN`, `stagnation-cap-<ppm>`, `normalize-out`, `normalize-rebal`, `stagnation-floor`, `stagnation-floor-relax`, `stagnation-neg-override`, `stagnation-pressure`, `peg-paused-stagnation`.
- Low-out e falta de sinal:
- `low-out-slow-up`, `low-out-noflow-cap`, `no-signal-noup`, `no-signal-floor-relax`.
- Discovery/explorer:
- `discovery`, `discovery-hard`, `explorer`, `surge*`.
- Sinais HTLC:
- `htlc-policy-hot`, `htlc-liquidity-hot`, `htlc-forward-hot`, `htlc-sample-low`, `htlc-neutral-lock`, `htlc-liq+X%`, `htlc-policy+X%`, `htlc-liq-nodown`, `htlc-policy-nodown`, `htlc-neutral-nodown`, `htlc-step-boost`.
- Super-source e inbound:
- `super-source`, `super-source-like`, `new-inbound`, `bootstrap`, `inb-<n>`.
- Seed e origem de fallback:
- `seed:amboss`, `seed:amboss-missing`, `seed:amboss-empty`, `seed:amboss-error`, `seed:med`, `seed:vol-<n>%`, `seed:ratio<factor>`, `seed:outrate`, `seed:mem`, `seed:default`, `seed:guard`, `seed:p95cap`, `seed:absmax`, `out-fallback-21d`, `rebal-fallback-21d`.

Exemplos de leitura:
- Exemplo A (sink saudavel e lucrativo):
```text
keep 844 ppm | target 844 | out_ratio 0.21 | out_ppm7d~625 | rebal_ppm7d~513 | floor>=657(peg) | margin~61 | ... outrate-floor peg peg-demand ...
```
Leitura: canal com movimento e margem positiva, floor ancorado em mercado/custo, sem ajuste forcado.

- Exemplo B (local alto, ocioso, sem sinal de qualidade):
```text
keep 1500 ppm | target 1500 | out_ratio 0.24 | out_ppm7d~0 | rebal_ppm7d~0 | ... low-out-slow-up no-signal-noup no-signal-floor-relax ...
```
Leitura: sem sinal confiavel, o algoritmo evita aumentar fee no escuro.

- Exemplo C (pressao de estagnacao em local alto):
```text
keep 1461 ppm | target 1139 | out_ratio 0.35 | ... stagnation normalize-out stagnation-r5 stagnation-cap-1139 stagnation-floor peg-paused-stagnation ...
```
Leitura: modo de estagnacao tentando normalizar para baixo sem contradicao com peg.

## Aposentar Node (Node Retirement)
Aposentar Node é um fluxo guiado para descomissionar um nó LND com segurança, fechar canais de forma ordenada e manter trilha auditável de recuperação.

Objetivos:
- Parar novas atividades operacionais antes de fechar canais.
- Drenar HTLCs em voo antes dos fechamentos cooperativos.
- Fechar cooperativamente tudo o que for possível e tratar exceções de forma explícita.
- Monitorar reconciliação final on-chain e, em sucessão, o status da auto-transferência.

Modelo central:
- O fluxo é orientado a sessões (`manual` ou `succession`).
- Apenas uma sessão ativa pode rodar por vez.
- Cada etapa registra eventos e estado no Postgres para sobreviver a refresh/restart da UI.
- Sessão manual exige aceite de disclaimer antes da execução.

Máquina de estados (alto nível):
- `created`: sessão aceita.
- `snapshot_taken`: baseline de saldos/canais capturado.
- `quiescing`: tentativa best-effort de parar rebalance/autofee e bloquear forwards.
- `draining_htlcs`: aguarda HTLCs pendentes chegarem a zero.
- `ready_for_coop_confirmation`: gate de confirmação antes de fechar cooperativamente.
- `closing_coop`: tentativas de fechamento cooperativo dos canais elegíveis.
- `awaiting_user_decision`: canais que exigem decisão do operador (`aguardar` vs `force_close`).
- `force_closing`: aplica force close apenas onde houver aprovação explícita.
- `monitoring_onchain`: aguarda fechamento completo de todos os canais rastreados.
- estados terminais: `completed`, `dry_run_completed`, `failed`, `canceled`.

Política de taxa no fechamento cooperativo:
- Hoje o Aposentar Node chama o fechamento cooperativo no LND com `sat_per_vbyte=0` (estimativa dinâmica/alvo padrão do LND).
- Isso mantém consistência com o estimador do LND e evita dependência externa de fee durante o descomissionamento.

Componentes de UI:
- Painel de disclaimer + criação de sessão:
- escolhe entre `Modo dry-run (somente simulação)` ou execução real.
- Quadro de etapas:
- badge por etapa (`Concluída`, `Em andamento`, `Pendente`).
- Lista de sessões:
- origem, modo, estado, timestamps e último erro.
- Painel Snapshot inicial:
- baseline no início da sessão (canais abertos/pendentes, HTLCs, saldo on-chain e Lightning).
- Painel de reconciliação:
- resumo final (saldos/canais) e resultado de transferência quando aplicável.
- Linha do tempo de canais (inicial vs atual):
- compara por canal o estado inicial com o atual (ativo, saldos local/remoto, HTLCs, modo/txid de fechamento, decisão, erros).
- Eventos da sessão:
- trilha cronológica de execução para diagnóstico/auditoria.
- Modal de confirmação para fechamento cooperativo:
- confirmação explícita sem volta para sessões manuais.
- Ações de exceção por canal:
- decisões `Aguardar` / `Force close` para peer offline ou HTLC travado.
- Auditoria de transferência (sessões disparadas por sucessão):
- destino, tentativas, status, txid com explorer, confirmações, política de fee, timestamps e erros.

Comportamento do dry-run:
- Simula o fluxo completo sem enviar fechamentos cooperativos/forçados reais.
- Gera snapshot, atualiza linha do tempo dos canais, registra eventos e conclui como `dry_run_completed`.
- Serve para validar política e entendimento operacional antes da execução real.

### Modo de Sucessão (dead-man switch)
O Modo de Sucessão automatiza o disparo da aposentadoria quando a prova de vida não é confirmada no prazo.

Padrões e pré-requisitos:
- Desabilitado por padrão.
- Só pode ser armado quando o `Espelho de atividade no Telegram` estiver habilitado em Notificações.
- Usa o mesmo motor de aposentadoria com `source=succession`.

Configuração na UI:
- `Ativar modo sucessão`: arma o agendador.
- `Succession em dry-run`: quando habilitado, sessões automáticas de aposentadoria rodam em simulação.
- `Carteira externa de destino on-chain`: destino dos fundos recuperados.
- `Intervalo da prova de vida (dias)`: prazo entre confirmações válidas e início dos lembretes.
- `Janela de lembrete diário (dias)`: prazo final após início dos lembretes.
- `Mínimo de confirmações para auto-transferência`: aguarda UTXOs com essa profundidade antes do sweep.
- `Taxa da auto-transferência (sat/vbyte)`: se `0`, o LND estima dinamicamente.
- `Pré-aprovar FC para peers offline` e `Pré-aprovar FC para canais com HTLC travado`: política de exceção para fluxo sem operador.

Entradas de prova de vida:
- Botão na UI: `Estou vivo (UI)`.
- Comando/mensagem no Telegram: `/alive` ou `estou vivo`.
- Qualquer caminho reseta `last_alive_at`, `next_check_at` e `deadline_at`.

Ciclo de lembrete e disparo:
- O scheduler verifica sucessão a cada minuto.
- Antes de `next_check_at`: estado permanece armado.
- Entre `next_check_at` e `deadline_at`: envia um lembrete no Telegram por dia.
- Depois de `deadline_at`: dispara Aposentar Node automaticamente (real ou dry-run conforme configuração).

Controles de simulação:
- `Simular vivo`: registra confirmação de vida imediatamente.
- `Simular não vivo`: dispara imediatamente uma sessão de aposentadoria por sucessão em dry-run para validação.

Notas operacionais:
- Se já existir sessão de aposentadoria ativa, a sucessão entra em espera e tenta novamente depois.
- O status final é espelhado no estado da sucessão (`retirement_completed` / `dry_run_completed`) e pode notificar no Telegram.
- Em sucessão real, o monitoramento da auto-transferência acompanha envio e confirmações da transação de sweep.

## Terminal web (opcional)
LightningOS Light pode expor um terminal web protegido usando GoTTY.

O instalador habilita automaticamente o terminal e gera credencial quando ausente.
Você pode revisar/sobrescrever em `/etc/lightningos/secrets.env`:
- `TERMINAL_ENABLED=1`
- `TERMINAL_CREDENTIAL=user:pass`
- `TERMINAL_ALLOW_WRITE=0` (defina `1` para permitir input)
- `TERMINAL_PORT=7681` (opcional)
- `TERMINAL_WS_ORIGIN=^https://.*:8443$` (opcional, padrão permite todas as origens)

Inicie (ou reinicie) o serviço:
```bash
sudo systemctl enable --now lightningos-terminal
```
A página Terminal mostra a senha atual e botão de cópia.

## Notas de segurança
- A seed phrase nunca é armazenada. Ela é mostrada uma vez no assistente.
- Credenciais RPC são armazenadas apenas em `/etc/lightningos/secrets.env` (root:lightningos, `chmod 660`).
- API/UI bindam em `0.0.0.0` por padrão para acesso LAN. Para localhost-only, defina `server.host: "127.0.0.1"` em `/etc/lightningos/config.yaml`.

## Troubleshooting
Se `https://<IP_LAN_DO_SERVIDOR>:8443` não estiver acessível:
```bash
systemctl status lightningos-manager --no-pager
journalctl -u lightningos-manager -n 200 --no-pager
ss -ltn | grep :8443
```

### App Store (LNDg, Peerswap, Elements, Bitcoin Core)
- LNDg roda em Docker e escuta em `http://<IP_LAN_DO_SERVIDOR>:8889`.
- Peerswap instala `peerswapd` + `psweb` (UI em `http://<IP_LAN_DO_SERVIDOR>:1984`) e requer Elements.
- Elements roda como serviço nativo (Liquid Elements node, RPC em `127.0.0.1:7041`).
- Bitcoin Core roda via Docker com dados em `/data/bitcoin`.

Notas LNDg:
- A página de logs do LNDg lê `/var/log/lndg-controller.log` dentro do container. Se estiver vazio, verifique `docker logs lndg-lndg-1`.
- Se aparecer `Is a directory: /var/log/lndg-controller.log`, remova `/var/lib/lightningos/apps-data/lndg/data/lndg-controller.log` no host e reinicie o LNDg.
- Se LND estiver usando Postgres, o LNDg pode logar ausência de `channel.db`. Isso é esperado e inofensivo.

## Arquitetura da App Store
- Cada app implementa um handler em `internal/server/apps_<app>.go`.
- Apps são registrados em `internal/server/apps_registry.go`.
- Arquivos de app ficam em `/var/lib/lightningos/apps/<app>` e dados persistentes em `/var/lib/lightningos/apps-data/<app>`.
- Docker é instalado sob demanda por apps que precisam dele (a instalação core continua sem Docker).
- Checks de sanidade de registry garantem IDs e portas únicos.

### Adicionando um novo app
1) Crie `internal/server/apps_<app>.go` e implemente a interface `appHandler`.
2) Registre o app em `internal/server/apps_registry.go`.
3) Adicione um card em `ui/src/pages/AppStore.tsx` e um ícone em `ui/src/assets/apps/`.

### Checks da App Store
Rode os testes de sanidade do registry:
```bash
go test ./internal/server -run TestValidateAppRegistry
```

## Changelog
Notas por versão são mantidas no GitHub Releases:
- https://github.com/jvxis/brln-os-light/releases

## Desenvolvimento
Veja `DEVELOPMENT.md` para setup local e instruções de build.

## Systemd
Templates estão em `templates/systemd/`.

## Rebuild apenas (manager/UI)
Use quando quiser apenas recompilar sem rodar o instalador completo.

Rebuild do manager:
```bash
sudo /usr/local/go/bin/go build -o dist/lightningos-manager ./cmd/lightningos-manager
sudo install -m 0755 dist/lightningos-manager /opt/lightningos/manager/lightningos-manager
sudo systemctl restart lightningos-manager
```

Rebuild da UI:
```bash
cd ui && sudo npm install && sudo npm run build
cd ..
sudo rm -rf /opt/lightningos/ui/*
sudo cp -a ui/dist/. /opt/lightningos/ui/
```


