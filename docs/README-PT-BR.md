# LightningOS

> [!IMPORTANT]
> O LightningOS é um painel local de controle do node e **não foi projetado para exposição direta à Internet pública**. Use-o somente em uma LAN confiável ou por uma VPN privada, como o Tailscale, sempre protegido por firewall no host ou na rede. Nunca encaminhe a porta `8443` nem as portas da App Store para a Internet. Os instaladores restringem a `8443` à LAN detectada e à interface `tailscale0` somente quando o UFW já está instalado e ativo; eles não ativam o UFW automaticamente. Confirme sempre com `sudo ufw status` ou configure um firewall externo equivalente antes de usar o node.

<img width="1920" height="1080" alt="logo" src="https://github.com/user-attachments/assets/504ec23e-31f8-407a-a848-3fa4ce3ec1f9" />

[Clique aqui](https://github.com/jvxis/brln-os-light/blob/main/README.md) para ver a versão em inglês (fonte da verdade).

LightningOS é um instalador completo de daemon de nó Lightning, com gerenciador de nó, assistente guiado, dashboard e carteira. O manager serve UI e API via HTTPS em `0.0.0.0:8443` por padrão para acesso em LAN (defina `server.host: "127.0.0.1"` para acesso somente local) e integra com systemd, Postgres, smartctl, Tor/i2pd e LND gRPC.

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
- Assistente para credenciais RPC do Bitcoin, seed/carteira nativa e primeiro cadastro de admin
- Dashboard redesenhado com pulso do node, saúde do core, risco das automações, atividade recente e painéis de receita
- Suíte Lightning Ops: peers/canais, Network Atlas, Graph Explorer, Novos Canais, Rebalance Center, Autofee, Ranking de Canais, Aposentar Node, sinais HTLC e Channel Auto Heal
- Carteira com leitura de QR de invoice, invoices blinded, preview/probing de rotas, pagamento por rota validada e detalhes de pagamento
- Chat Keysend: 1 sat por mensagem + taxas de roteamento, indicadores de não lidas, retenção de 30 dias
- Notificações em tempo real (on-chain, Lightning, canais, forwards, rebalances)
- Notificações Telegram: backups SCB, resumos financeiros, comandos sob demanda `/scb` e `/balances`
- Relatórios diários de roteamento (timer + backfill + API live + API live de movimento)
- On-chain Hub com coin control, labels/grupos/travas de UTXO, bump de taxa, detalhes de transações e grafo de proveniência Wallet Flow
- App Store com 19 apps/serviços registrados, checks de dependências, dados persistentes, integrações nativas e instalação de Docker sob demanda
- Interface dedicada para Taproot Assets com descoberta, sync de universe, mint/reissue, recebimento, preview de envio, envio e resgate
- Gestão de Bitcoin Local, Elements, LND, Tor, discos, manutenção do banco, auditoria, terminal, atalhos e logs

## Visão geral das funcionalidades
- **Setup e administração seguros:** cadastro guiado no primeiro acesso, tokens locais de setup/recovery, habilitação opcional de login para instalações antigas, sessões em cookies HTTP-only, proteção CSRF, reautenticação recente para envios on-chain externos e trilha de auditoria para ações sensíveis.
- **Dashboard e operações de sistema:** pulso do node, saúde de Bitcoin/LND/sistema, liquidez, atividade recente, receita, risco das automações, mercado/fees, ações seguras de serviço, contexto do journal, manutenção do Postgres, saúde dos discos e upgrades do app, LND e Tor.
- **Carteira e operações on-chain:** invoices, leitura de QR, invoices blinded, decode de pagamentos, preview/probing de rota, pagamento por rota automática ou validada, MPP, detalhes de pagamento, geração de endereços, preview de envio externo, coin control, metadados, grupos de UTXO, lock/unlock e bump de taxa.
- **Inteligência de rede:** Network Atlas, Graph Explorer nativo com cache, busca de nodes e histórico de policies, enriquecimento de canais fechados, recomendações de peers, Novos Canais com score baseado em evidência local de 30 dias e Ranking por sinais econômicos e operacionais.
- **Automação de liquidez:** rebalances manuais e automáticos, scheduler Sovereign em shadow/live, fast paths, pre-probing, MPP/MSPR, orçamentos, guardrails de ROI/payback, proteção de fontes, controles de Mission Control, AutoTarget v2 adaptativo e históricos auditáveis de decisões/resultados.
- **Automação de fees:** Autofee explicável por canal com seeds do grafo nativo e Amboss opcional, estados dinâmicos de liquidez, pressão HTLC, floors de rentabilidade, calibração por porte do node, modos Balanced/Market Refill, medição de outcomes e tags/histórico detalhados.
- **Ciclo de vida e segurança dos canais:** controles de peers/canais, abertura em lote, previews e recuperação de fechamento, modo Parked, Auto Heal, verificação de peers Tor, HTLC Manager, limpeza de pagamentos falhos, watchtowers, Balanced Open, Aposentar Node e dead-man switch opcional do Modo de Sucessão.
- **Observabilidade e comunicação:** notificações live, backups SCB e resumos via Telegram, relatórios e backfills de roteamento, APIs de movimento/live, chat Keysend, eventos de auditoria, logs de serviços e terminal web opcional protegido.
- **Ecossistema Bitcoin e Liquid:** Bitcoin Core remoto ou local, Elements com seleção de mainchain local/remota, Peerswap, fallbacks de proveniência do Wallet Flow, Taproot Assets e apps de carteira, mineração, federação, analytics, pagamentos e infraestrutura self-hosted.

## Estrutura do repositório
- `cmd/lightningos-manager`: backend Go (API + UI estática)
- `ui`: UI React + Tailwind
- `templates`: units systemd e templates de configuração
- `install.sh`: instalador idempotente (wrapper em `scripts/install.sh`)
- `install_existing.sh`: instalador para nós existentes (x86_64/amd64)
- `install_existing_pi.sh`: instalador para nós existentes em Raspberry Pi 4 (arm64)
- `configs/config.yaml`: configuração local de desenvolvimento

## Instalação (Ubuntu Server)
O instalador provisiona tudo que é necessário em um Ubuntu limpo:
- Postgres, smartmontools, curl, jq, ca-certificates, openssl, build tools
- Tor (ControlPort habilitado) + i2pd habilitado por padrão
- Go 1.24.12 e a major mais recente do Node.js (fallback para Node.js 20.x se a detecção falhar)
- Binários do LND (padrão `v0.21.1-beta`)
- Binário do LightningOS Manager (compilado localmente)
- Build da UI (compilada localmente)
- Serviços systemd e templates de configuração
- CA local exclusiva do node, certificado TLS para a LAN e descoberta `.local` automática

O instalador conhecido pelos usuarios muda automaticamente para a ultima release publicada do LightningOS antes da instalacao:
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

Para desenvolvimento ou instalacao offline do checkout local exato, use `sudo LIGHTNINGOS_INSTALL_SOURCE=checkout ./install.sh`.

Quando o UFW já está instalado e ativo, o instalador detecta automaticamente qual rede IPv4 local pode acessar o manager (por exemplo, `192.168.1.0/24`). Ele remove a regra pública antiga de `8443/tcp`, libera a descoberta mDNS na LAN em `5353/udp` e também libera o acesso pela interface `tailscale0` quando o Tailscale está disponível. O instalador deliberadamente não ativa o UFW em um host existente, pois isso poderia bloquear o SSH ou outros serviços do node; portanto, UFW ausente ou inativo exige que o operador configure um firewall equivalente para LAN/VPN.

### Instalação via curl (bootstrap)
Isso identifica a ultima release publicada do LightningOS, faz checkout da tag exata e depois roda `lightningos-light/install.sh`.
```bash
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo ACCEPT_MIT_LICENSE=1 bash
```

Overrides opcionais:
```bash
# Usar outro caminho de clone
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo BRLN_DIR=/opt/brln-os-light bash

# Fixar branch/tag em vez da ultima release publicada
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo BRLN_REF=main bash

# Instalar em node existente x86_64/amd64
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo BRLN_INSTALLER=install_existing.sh bash

# Instalar em node existente Raspberry Pi 4 (arm64)
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo BRLN_INSTALLER=install_existing_pi.sh bash

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

Execute o instalador correspondente ao seu ambiente; cada script seleciona automaticamente a ultima release publicada do LightningOS:
```bash
cd lightningos-light

# Nó existente em x86_64/amd64
sudo ./install_existing.sh

# Nó existente em Raspberry Pi 4 (arm64)
sudo ./install_existing_pi.sh
```

Acesse a UI de outra máquina na mesma LAN. O instalador mostra os dois endereços:

- Preferencial: `https://<NOME_DA_MAQUINA>.local:8443`
- Alternativo por IP: `https://<IP_LAN_DO_SERVIDOR>:8443`

O LightningOS cria uma CA privada exclusiva do node e um certificado válido
para os dois endereços. No primeiro acesso de cada dispositivo, use **Confiar
neste dispositivo** na tela de login para baixar o instalador de confiança do
Windows ou a CA pública para outro sistema operacional. A chave privada da CA
nunca sai do node.

O fluxo normal de upgrade do app migra automaticamente certificados
autoassinados legados reconhecidos do LightningOS e mantém um backup com data
e hora. Certificados personalizados nunca são substituídos; a descoberta
`.local` só é anunciada quando o certificado ativo cobre esse nome.

### Atualização de instalações existentes para a 0.5.8

> [!IMPORTANT]
> O caminho oficial `0.5.2-Beta -> 0.5.8-Beta` começa em **Upgrade do App na
> interface do LightningOS** e termina em duas etapas executadas no próprio
> node. Primeiro, o atualizador legado instala o Manager e a UI exatos da 0.5.8;
> depois, o novo Manager autentica a tag e o commit completo da release, instala
> o broker de privilégios restrito e remove a permissão legada revisada. Mantenha
> o host ligado e não inicie outro upgrade durante a transição. Fechar o
> navegador não interrompe a operação no servidor. O corte de privilégios não
> reinicia o Bitcoin Core nem o LND.

Nodes que já estão na `0.5.7-Beta` usam o fluxo normal de upgrade pela interface
para a `0.5.8-Beta`. Não execute novamente `install.sh` ou
`install_existing.sh` apenas para atualizar qualquer uma dessas versões
suportadas. A release `0.5.6-Beta` possui um defeito específico no bootstrap do
atualizador e exige um procedimento assistido, em vez de novas tentativas pela
interface.

## Primeiro acesso com segurança
- A proteção por login vem habilitada por padrão em instalações novas.
- Ao final de `install.sh`, `install_existing.sh` e `install_existing_pi.sh`, o instalador imprime no console a URL da UI e um setup token de admin quando ainda não existe senha configurada.
- No primeiro acesso, ou após atualizar uma instalação antiga que ainda não tenha senha de admin, a UI abre a tela de definição da senha antes de entrar no wizard ou no dashboard.
- Se precisar gerar outro setup token depois:

```bash
sudo /opt/lightningos/manager/lightningos-manager auth setup-token new
```

- Se esquecer a senha de admin, gere um recovery token localmente no node:

```bash
sudo /opt/lightningos/manager/lightningos-manager auth recovery new
```

- O recovery altera apenas a senha de admin da UI/API. Ele não altera a senha da carteira do LND.
- Serviços agendados como Autofee, Rebalance, relatórios, sucessão e outros timers do backend continuam funcionando sem login no navegador.
- Envios manuais on-chain para endereço externo exigem uma nova confirmação de senha. As automações internas e os fluxos de sucessão não são bloqueados por essa confirmação extra.

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
LightningOS inclui um sistema de notificações em tempo real que rastreia:
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
Relatórios diários de roteamento são armazenados no Postgres (mesmo DB/usuário de notificações) e reconciliados de forma idempotente até o último dia local completo.

Agenda:
- `lightningos-reports.timer` executa a reconciliação no minuto `05` de cada hora. Dias ausentes ou com falha transitória são tentados novamente automaticamente.
- A UI detecta dias completos ausentes ao abrir e oferece a ação **Reconciliar relatórios**, com progresso. A operação não reinicia LND nem Bitcoin.
- Reconciliação automática/manual: `/opt/lightningos/manager/lightningos-manager reports-reconcile`.
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
- `GET /api/reports/movement/live` (janela live de movimento/rebalance do dia atual)

## Lightning Ops (mapa de funcionalidades)
- Network Atlas: mapa live da rede com apresentação configurável e contexto de nodes/canais.
- Gestão de canais: busca de peers/canais, conexão/desconexão e boost, assistência para abertura em lote, updates de policy/status, abas detalhadas de economia/atividade/falhas, watchtowers, assinatura e restore de SCB.
- Graph Explorer: snapshot nativo do grafo, busca de nodes, abas geral/canais/fechados/fees, reconciliação de peers locais e histórico de policy de fees.
- Novos Canais: candidatos de peers a partir de rotas observadas, atrito local, qualidade do grafo, demanda, alívio e confiança.
- Ranking de Canais: score por canal, estado recomendado, comparação 7d vs 30d, estado dinâmico de liquidez, modo Parked, sinais de top source/sink, recomendações acionáveis e deep links para outras operações.
- Rebalance Center: rebalances manuais/automáticos e Sovereign shadow/live com targeting por score, AutoTarget v2 adaptativo, watchdogs, pre-probing, pisos separados de probe/execução, MSPR, guardrails de ROI/payback, peso por efetividade de source e auto-restart manual opcional.
- Autofee: automação de fees por canal com âncoras de custo, seed nativo corrigido pelo grafo, Amboss opcional, estado dinâmico de liquidez, sinais HTLC, telemetria de refresh, calibração por tamanho/liquidez do node, scheduler/manual run, outcomes e histórico detalhado.
- Abertura/fechamento de canais: previews individuais e em lote, bump de fee de pending open, caminhos coop/force-close, avisos para canais financiados pelo peer, recuperação pelo Close Manager e sessões Balanced Open.
- Aposentar Node (Node Retirement): fluxo guiado de descomissionamento seguro com linha do tempo de sessão, controle de fechamento cooperativo, tratamento de exceções e reconciliação on-chain.
- HTLC Manager: telemetria HTLC com histerese usada pelo Autofee e por decisões de liquidez.
- Channel Auto Heal + Tor peers checker: guardrails operacionais para confiabilidade de peer/canal.
- Health checks: opção de follow-bitcoin para fluxos de saúde de LND/nó.

## Graph Explorer
Graph Explorer é a camada nativa de inspeção do grafo. Ele monta e mantém um snapshot do grafo a partir do LND e permite inspecionar peers sem sair do LightningOS.

O que ele oferece:
- Busca por alias, pubkey, endereço e metadados do grafo.
- Visão geral do node com pubkey/endereço copiáveis e contexto da fonte do grafo.
- Aba de canais com capacidade pública, policy, direção e dados do peer.
- Aba de fechados com classificação de fechamento, origem do fechamento, enriquecimento de canais locais e contexto recuperado quando disponível.
- Aba de fees com resumo de policies outbound/inbound, gráficos de distribuição, histórico médio de fees e indicadores de teto de policy.
- Ação de recompute para reconstruir o snapshot em mudanças relevantes de grafo ou node.

Uso operacional:
- Use antes de abrir canais para investigar candidatos e sua presença pública no grafo.
- Use o enriquecimento de canais fechados para entender comportamento histórico de fechamento.
- Use os resumos de fee para comparar o pricing anunciado do peer com sua estratégia de Autofee e roteamento.

## Novos Canais
Novos Canais é um módulo de recomendação para abertura de canais. Ele combina evidência local de roteamento com o snapshot do grafo para ranquear peers que podem aliviar gargalos ou melhorar caminhos de receita.

Entradas do candidato:
- Rotas bem-sucedidas observadas e volume de rota nos últimos 30 dias.
- Tentativas de rota falhas e evidência de caminhos caros.
- Adjacência compartilhada com peers fortes ou problemáticos.
- Quantidade de canais públicos, capacidade total e melhores fees outbound anunciadas.
- Score de demanda, score de alívio, score de qualidade do grafo, confiança e motivos legíveis.

Como usar:
- Comece por candidatos de alta confiança que também tenham sinais de demanda ou alívio.
- Confira o candidato no Graph Explorer antes de alocar capital.
- Use a recomendação como insumo para abrir canal, não como gatilho automático.

## Ranking de Canais
O Ranking de Canais é a camada de análise dos canais abertos. Ele foi feito para responder rapidamente quatro perguntas práticas:
- este canal gera valor líquido?
- este peer merece mais capital?
- este canal está custando caro demais para manter?
- devo expandir, manter, monitorar ou preparar fechamento?

Além da economia direta de roteamento, o score também considera `receita assistida` de forwards. Isso credita parte da fee e do volume ao canal de entrada, porque alguns canais são estratégicos como porta de entrada de fluxo, mesmo quando o resultado direto de saída é fraco.

Onde ele aparece:
- Página principal: `Ranking de Canais`
- Indicador leve: cada card em `Lightning Ops > Canais` mostra apenas o badge curto e o score
- Links diretos: as recomendações podem abrir o módulo relevante em `Lightning Ops`, `Autofee`, `Rebalance Center`, `HTLC Manager` ou `Gestão de Fechamentos`

O que o score significa:
- O score é uma nota operacional de `0-100` usada para ordenar os canais, e não um gatilho cego de automação.
- Score alto significa, em geral, melhor economia e menor atrito operacional.
- Score baixo significa, em geral, retorno líquido mais fraco, pior saúde operacional ou custo de manutenção mais alto.
- O score deve ser usado principalmente de forma comparativa entre os seus próprios canais.

Leitura rápida por faixa:
- `70-100`: normalmente canal saudável e competitivo dentro do seu node
- `45-69`: normalmente canal aceitável, mas que merece leitura do detalhe antes de receber mais capital
- `25-44`: normalmente canal para monitorar de perto
- `0-24`: normalmente canal fraco em economia ou operação, muitas vezes candidato a fechamento se a condição persistir

Como o score é calculado:
- Rentabilidade: fees de forward menos custo de rebalance
- Receita assistida: crédito ponderado vindo do papel do canal como entrada de forwards, para não subavaliar canais que ajudam outros canais a monetizar
- Eficiência de capital: quanto resultado líquido o canal gera em relação à capacidade
- Utilização: quanto volume o canal encaminha e se a liquidez está equilibrada o suficiente para ser útil
- Custo de manutenção: quanto o rebalance está consumindo em relação à receita que sustenta
- Saúde operacional: atividade do canal, pressão de HTLCs pendentes, estabilidade do peer em 30 dias e pressão de falhas HTLC
- Confiança da amostra: se já existe histórico recente suficiente para julgar o canal com mais segurança

Sinais avançados mostrados no módulo:
- `Estabilidade do peer 30d`: calculada a partir de amostras repetidas de conectividade, erros recentes e qualidade de ping
- `Falhas HTLC 30d`: agregado de falhas HTLC do canal, separado em falhas de policy, liquidez e forward
- `Dependência de rebalance`: mede o quanto o canal parece depender de rebalance para continuar útil
- `Feedback`: compara score e resultado líquido recentes contra snapshots históricos para validar se a recomendação atual está ajudando

O que um score alto costuma indicar:
- Resultado líquido positivo
- Boa utilização em relação ao tamanho do canal
- Custos de rebalance sob controle
- Comportamento saudável de peer/canal
- Menor pressão de falhas HTLC

O que um score baixo costuma indicar:
- Resultado líquido fraco ou negativo
- Capital parado com pouco throughput
- Custo de rebalance consumindo a economia
- Peer instável ou baixa estabilidade do peer
- Pressão elevada de falhas HTLC
- Pouca contribuição direta ou assistida para o resultado de roteamento do node

Estados recomendados:
- `Expandir`: boa economia, boa utilização, peer saudável e sinais de que mais capacidade pode valer a pena
- `Manter`: canal saudável o suficiente para seguir com a política atual
- `Monitorar`: existe ineficiência ou instabilidade, mas ainda não há evidência suficiente para fechamento imediato
- `Fechar`: fraqueza persistente, risco ou custo de oportunidade alto o suficiente para preparar uma saída ordenada

Modo de automação do canal:
- Um canal pode ser colocado em modo `Parked` pelo Ranking de Canais, com fee outbound fixa opcional, data de revisão e nota do operador.
- O estacionamento suspende Autofee e a participação em rebalance automático/manual-restart, exclui o canal como source e impede novos jobs de rebalance para ele.
- Ao retornar o canal para `Normal`, o LightningOS restaura as configurações de automação capturadas no estacionamento.

Como ler a página:
- Lista de ranking: compare os canais por score ou ordene por resultado líquido, eficiência de capital, custo de rebalance, estabilidade do peer, falhas HTLC, dependência de rebalance ou risco operacional
- Painel de detalhe: inspecione o canal selecionado com:
  - métricas
  - `Economia 7D / 30D`
  - tendência e histórico de score
  - sinais operacionais
  - motivos que explicam o estado
  - recomendações acionáveis
  - outros canais do mesmo peer

Como usar no dia a dia:
- Comece ordenando por `Resultado líquido 30d` ou `Risco operacional`
- Abra os melhores e os piores canais para comparar por que eles estão diferentes
- Use os canais em `Monitorar` para revisar Autofee, política de rebalance, pressão HTLC e estabilidade do peer antes de decidir fechar
- Use os canais em `Expandir` como candidatos a receber mais capital ou mais suporte de liquidez
- Use os canais em `Fechar` para preparar um coop close organizado, em vez de reagir apenas quando o canal já virou problema

Nota importante:
- `Score` serve para ranquear
- `Estado` expressa a recomendação operacional
- `Recomendações` apontam o próximo caminho de revisão

Esses três elementos são relacionados, mas não idênticos. Um canal com score mediano ainda pode cair em `Monitorar` ou `Fechar` se os sinais de risco e manutenção estiverem ruins o suficiente.

## Rebalance Center
Rebalance Center é um otimizador de liquidez de entrada (local/outbound) para LND. Ele move liquidez local para canais onde essa liquidez tende a ser vendida com vantagem econômica: pouco outbound/local, `outgoing_fee_ppm > peer_fee_ppm`, source com liquidez sobrando e custo de rota abaixo do ganho esperado. Ele pode rodar rebalances manuais por canal ou varreduras totalmente automáticas que enfileiram rebalances com base em cost gate, ROI, profit guardrail, cooldowns e restrições de orçamento. Custos são rastreados por notificações (fee msat) e agregados em custo live + gasto diário auto/manual.

Curso operacional:
- PT-BR: `docs/23_REBALANCE_CENTER_COURSE_PT_BR.md`
- EN: `docs/24_REBALANCE_CENTER_COURSE_EN.md`

Comportamento principal:
- Manual Rebal In usa elegibilidade manual mais permissiva e serve para teste, correção pontual ou ação forçada em um canal específico.
- Rebalances manuais ignoram orçamento diário e podem ser iniciados por canal.
- Rebalances automáticos respeitam orçamento diário e só miram canais marcados explicitamente como `Auto`.
- Canais de origem são selecionados entre os com liquidez local suficiente e não excluídos; um canal preenchido por rebalance fica **protegido** e não pode ser usado como origem até regras de payback liberarem.
- Alvos são escolhidos quando o déficit de liquidez outbound passa do deadband e o spread de taxa é positivo; estimativa de ROI usa receita de roteamento dos últimos 7 dias vs custo estimado de rebalance.
- Alvos automáticos também passam por cost gate, ROI mínimo, profit guardrail, cooldowns de target/source, checagem de canal ocupado, piso de execução e orçamento restante.
- Alvos automáticos são ranqueados por **economic score** = (ganho esperado - custo estimado), priorizando canais de maior margem.
- Um **profit guardrail** impede enqueue automático quando ganho esperado é menor que custo estimado (quando ambos são conhecidos). Se ROI for indeterminado (cost = 0 com spread positivo), auto continua permitido.
- `Bypass cost gate` é por canal. Ele ignora apenas o gate de custo esperado; ROI e profit guardrail continuam protegendo o job depois.
- A seleção de origem também considera efetividade da origem e cooldown temporário de rota morta, evitando insistir em alvos/origens que falham repetidamente.
- Seleção de origem é ponderada por histórico do par: pares recentes bem-sucedidos com taxas menores são priorizados, e falhas recentes são despriorizadas.
- A visão geral mostra **Live cost 24h**, sources/targets elegíveis, **Last scan** em horário local, status/reasons da varredura, effectiveness/attempts, ROI/payback progress, pressão de falhas de rota, MSPR 24h e detalhes opcionais de skip.
- Métricas baseline ficam em `/api/rebalance/metrics/baseline?days=1`; use 24h para comparação rápida e 7d para decisão mais confiável.
- Manual Restart Watch respeita `EligibleAsTarget`; ele tenta novamente jobs manuais com auto-restart depois do intervalo/cooldown e só ignora o cost gate quando `Bypass cost gate` está habilitado no canal.
- **Pre-probing** de rota roda antes do envio, buscando o maior valor viável na rota.
- **Scheduler Sovereign:** o Auto Pilot pode rodar em shadow para revisar decisões ou em live para executar. Escopo de candidatos, jobs por ciclo, lucro mínimo esperado, tratamento de slow sellers, quarentena por risco de rota, custo de oportunidade das sources, parcela de exploração e score ponderado por EV são configuráveis e gravados no histórico Sovereign.
- **AutoTarget v2:** nos canais que aderem ao gerenciamento, o autopilot ajusta `target_outbound_pct` gradualmente dentro dos limites configurados. As decisões combinam sell-through, drain rate, saldo local, receita de 7 dias, sucesso de rebalance, thresholds calibrados ao node e limites de subidas/descidas por ciclo; toda mudança ou manutenção entra no histórico AutoTarget.
- O `Máximo (sats)` configurado também limita a economia do scan Sovereign e o valor gasto pelo job escolhido, mantendo o mesmo teto no score e na execução.

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
- `Budget mode` / `Budget auto only`: modo híbrido de orçamento pode proteger execuções automáticas e deixar o comportamento de manual restart mais claro.
- `Manual reserve`: reserva opcional fixa ou percentual para proteger parte do orçamento para fluxos de manual restart.
- `Deadband (%)`: déficit mínimo de outbound antes de um canal virar alvo.
- `Minimum local for source (%)`: liquidez local mínima para um canal ser origem.
- `Economic ratio`: fração da taxa outbound do canal alvo (base+ppm) usada como limite máximo de taxa.
- `Econ ratio max (ppm)`: teto opcional para o limite de taxa ao usar economic ratio (0 = sem teto).
- `Fee limit (ppm)`: sobrescreve economic ratio com limite fixo máximo de taxa ppm (0 = desativado).
- `Subtract source fees`: reduz orçamento de taxa com estimativa de source fees (mais conservador).
- `ROI minimum`: ROI mínimo estimado (receita 7d / custo estimado) para enfileirar jobs auto.
- `Rebalance cost floor (ppm)`: custo mínimo esperado de rota quando o canal não tem histórico útil.
- `Gain model version`: v1 usa receita histórica; v2 usa spread efetivo mais velocidade.
- `Velocity weight`: controla quanto o v2 prioriza canais com drain rate real em vez de fairness/idade.
- `Source min payback progress`: protege canais que compraram liquidez por rebalance até a receita pagar parte suficiente do custo.
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
- `Min probe amount (sats)` (`min_probe_sat`, padrão `5000`): piso mínimo permitido no probe quando split está ligado.
- `Min execute amount (sats)` (`min_execute_sat`, padrão `10000`): piso mínimo permitido para execução real quando split está ligado.
Interações importantes:
- As tentativas continuam começando com âncora em `Minimum (sats)` (compatível com comportamento legado).
- Com split ligado, o probe pode descer até `min_probe_sat` e a execução é bloqueada abaixo de `min_execute_sat`.
- Com split ligado, candidatos auto abaixo do piso de execução são descartados cedo.
Recomendação prática:
- Manter `Min execute amount (sats)` igual ao `Min probe amount (sats)`, a menos que você queira explicitamente probe mais baixo que execução.
- Use split quando quiser ampliar descoberta de rota sem liberar execução abaixo do piso desejado.

MSPR (`MSPR (Paralelo Multi-Source)`):
- Objetivo: aumentar a chance de sucesso no primeiro passe tentando shards em múltiplas fontes em paralelo, antes do fallback legado sequencial.
- `Enable MSPR` (`mpp_enabled`, padrão `true`): habilita o prepass MSPR com execução real.
- `MSPR for auto jobs only` (`mpp_auto_only`, padrão `true`): quando ligado, só jobs auto usam MSPR; jobs manuais ficam no legado.
- `Max shards` (`mpp_max_shards`, padrão `6`, faixa `1..20`): máximo de shards planejados na rodada MSPR.
- `Parallel workers` (`mpp_parallelism`, padrão `3`, faixa `1..max_shards`): número máximo de tentativas de shard concorrentes na rodada.
- `Min shard amount (sats)` (`mpp_min_shard_sat`, padrão `10000`): tamanho mínimo de shard planejado pelo MSPR.
- `Round timeout (sec)` (`mpp_round_timeout_sec`, padrão `35`): tempo máximo da rodada MSPR antes de cair para tentativas legadas.
Modelo de execução:
- O MSPR roda um prepass paralelo (com plano de shards e workers).
- Shards com sucesso são executados e contabilizados imediatamente.
- Após o prepass, o job continua na mesma fila/fluxo legado para o valor remanescente.
- Falhas de shard aparecem no histórico com prefixo `mpp shard:` no motivo.
Recomendação prática:
- Comece com os defaults atuais: `max_shards=6`, `parallel_workers=3`, `min_shard=10000`, `round_timeout=35`, com `auto only` habilitado.
- Se houver `mpp_structural_abort` frequente, reduza paralelismo ou número de shards.
- Se os primeiros shards ainda estiverem grandes, aumente shards (até `20`) e mantenha workers menor ou igual a shards.
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

Ele usa historico local de roteamento/rebalance (notificacoes no Postgres), seed nativo do Graph Explorer baseado em medias corrigidas de mercado, seed opcional da Amboss, sinais HTLC, calibracao por tamanho/liquidez do node e guardrails explicaveis.

Parametros da UI:
- `Enable autofee`: liga/desliga global.
- `Node operation mode`: `Balanced` ou `Market refill`.
- `Balanced`: modo padrao. Mantem o pipeline normal do Autofee, continua respeitando sinais derivados de rebalance e preserva um snapshot da politica de fees do node quando voce sai desse modo.
- `Market refill`: modo operacional do node. Desliga rebalance automatico e manual restart watch, usa pricing para atrair refill natural e restaura a politica anterior ao voltar para `Balanced`.
- `Profile`: Conservative / Moderate / Aggressive.
- `Lookback window (days)`: 5 a 21 dias (janela principal).
- `Run interval (hours)`: minimo 1 hora.
- `Cooldown up / down (hours)`: tempo minimo entre aumentos/reducoes.
- `Min fee (ppm)` e `Max fee (ppm)`: limites rigidos.
- `Rebalance cost mode`: `Per-channel`, `Global` ou `Blend`.
- `Amboss fee reference`: seed externo opcional; o seed nativo do grafo continua sendo a primeira referencia local de mercado quando disponivel.
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
- `Inbound discount reach out ratio (%)`: out ratio efetivo maximo ainda elegivel para discount inbound passivo. Maior = alcance maior.
- `Inbound discount min retained spread (%)`: spread minimo que precisa sobrar acima da ancora de custo. Maior = mais protecao de lucro.
- `Low-flow floor factor override (%)`: multiplicador aplicado ao piso quando o fluxo de saida esta baixo. Maior = mantem fees mais altas; menor = permite pisos mais baixos.
- `Global lock soften min out ratio (%)`: out ratio minimo para o lock global por margem negativa poder suavizar. Menor = mais canais ficam elegiveis.
- `Global lock soften max drop to peg (%)`: queda maxima permitida em direcao ao peg quando o lock global e suavizado. Menor = permite cortes mais profundos.
- `HTLC min attempts 60m override`: minimo de tentativas HTLC para o canal ser classificado por comportamento HTLC. Menor = mais reacoes guiadas por HTLC.
- `HTLC policy fail rate override (%)`: limite da taxa de falha policy para sinais HTLC. Menor = dispara `policy-hot` com mais facilidade.
- `HTLC liquidity fail rate override (%)`: limite da taxa de falha de liquidez para sinais HTLC. Menor = dispara `liquidity-hot` com mais facilidade.

Defaults de perfil e comportamento:
- O frontend agora le os `profile_defaults` enviados pelo backend; os textos `Profile default` e o autofill ao trocar perfil nao dependem mais de tabela hardcoded.
- `Conservative`: mais lento e mais protetor.
- `Moderate`: baseline mais utilitario, com descidas mais rapidas, `step cap` maior e lock global mais flexivel.
- `Aggressive`: janelas curtas, caps maiores e comportamento mais permissivo para `market_refill`.

Pipeline de decisao (por canal):
1. Monta referencias:
- `out_ppm7d` da janela principal.
- `rebal_ppm7d` do modo de custo selecionado.
- Quando nao ha referencia utilizavel, `floor_src=min` indica que o piso veio apenas de `min_ppm`.
- Seed (`native` com media corrigida do Graph Explorer -> `Amboss` weighted-corrected mean -> fallback para memoria/outrate/default).
2. Classifica comportamento (`sink`, `source`, `router`, `unknown`) e estado de liquidez.
3. Calcula target bruto com seed, out ratio, tendencia/margem, pressao HTLC e heuristicas de lucro.
4. Aplica controles de discovery/explorer/stagnation/profit-protect/locks globais.
5. Monta pilha de floor (`rebal`, `rebal-sink`, `outrate`, `peg`, `revfloor`, `stagnation`, `no-signal`).
6. Aplica step cap e cooldown, e decide `apply` ou `keep`.

O estado dinâmico de liquidez é persistido junto aos outcomes do Autofee e aparece no Fee Center e no Ranking de Canais. Assim, a revisão do operador e a medição posterior do resultado usam o estado que existia quando a decisão de fee foi tomada.

Diagrama do fluxo:
- `docs/AUTOFEE_FLOW_DIAGRAM_PT_BR.md`

Novidades importantes no modo `Balanced`:
- `Cooldown up` dinamico por `outnorm`: canais efetivamente muito drenados conseguem reagir mais rapido sem remover o cooldown por completo.
- `Drained explorer`: modo exploratorio dedicado a canais muito vazios e sem movimento, com pequenos passos de alta em vez de deixa-los presos em `0 ppm` ou micro-fees.
- Guardrails do seed: quando existem sinais locais fortes de `out_ppm7d` e `rebal`, o seed perde peso e passa a ser capado por perfil; em canal maduro sem sinal local, shock brusco de seed precisa repetir antes de virar nova ancora.
- Refresh de seed sensivel a liquidez: o refresh manual/idle mantem o seed bruto em `reference_ppm`, mas ajusta o `target_ppm` acima do seed quando o canal esta efetivamente drenado e abaixo do seed quando esta efetivamente cheio.
- Quedas por seed/no-signal com baixa liquidez sao seguradas: o Autofee ainda pode reduzir em direcao ao seed quando ha liquidez local util, mas evita entrar em `rescue` ou cortar fee no escuro em canais drenados sem evidencia local de out/rebal/HTLC.
- `Rescue`: estado temporario para canais estruturalmente fracos (`close` / `worsening`) que estao travados por `peg`, `floor-lock` ou `global-neg-lock` acima do que o sinal local justifica.

Modo `Market refill`:
- Usa o `target` do `Balanced` como referencia principal e aplica um premium controlado.
- Desconsidera rebalance como driver principal de target/floor.
- Mantem outbound mais alto e deriva o inbound discount a partir da outbound resultante.
- Pode usar skew de mercado da Amboss (`outgoing / incoming`) apenas como refinamento para aproximar mais o inbound da outbound.

Melhorias recentes de comportamento:
- Bootstrap para canais inbound novos com subida gradual e controlada (`new-inbound`, `bootstrap`).
- Relaxamento adaptativo de floor em canais travados (`floor-relax-stall`) para evitar lock prolongado em fee alta.
- Bloqueio de micro-subidas por floor quando o sinal e fraco (exceto cenarios fortes como surge/new-inbound), reduzindo churn.
- Forecast baseado na fee efetivamente aplicada (nao apenas candidata), deixando linhas `keep` mais coerentes.
- Troca de modo agora salva e restaura a policy real do LND (`outbound ppm`, `outbound base`, `inbound ppm`, `inbound base`, `time_lock_delta`) quando voce volta de `Market refill` para `Balanced`.

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
- Se o unico driver de queda for seed/no-signal e a liquidez local efetiva estiver baixa, o Autofee segura a fee atual em vez de forcar `rescue` ou reducao guiada apenas por seed (`seed-liq-down-hold`).

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
- Header: tipo da execucao + timestamp + modo operacional.
- Summary: contadores de up/down/flat e skips.
- Seed line: uso de seed nativo/Amboss/fallbacks.
- Calibration line: classes do node, low_out, revfloor, fatores globais HTLC.
- Linha por canal: `set/keep`, `target`, `out_ratio`, `out_ppm7d`, `rebal_ppm7d`, `seed`, `floor`, `margin`, `rev_share`, mudanca do inbound discount, tags, contadores HTLC e forecast.

Glossario de tags (Autofee Results):
- Referencia completa: `docs/AUTOFEE_TAG_GLOSSARIO_PT_BR.md` (PT-BR) e `docs/AUTOFEE_TAG_GLOSSARY_EN.md` (EN).
- Papel do canal e tendencia:
- `sink`, `source`, `router`, `unknown`, `trend-up`, `trend-down`, `trend-flat`.
- Controles de movimento:
- `stepcap`, `stepcap-lock`, `floor-lock`, `floor-relax-stall`, `reversal-guard`, `reversal-confirmed`, `downcap-general`, `htlc-low-sample-downcap`, `hold-small`, `same-ppm`, `cooldown`, `cooldown-profit`, `cooldown-skip`, `rebal-recent`, `rebal-attempt`, `rebal-recent-noup`.
- Controles de lucro e margem:
- `neg-margin`, `negm+X%`, `no-down-low`, `no-down-neg-margin`, `global-neg-lock`, `lock-skip-no-chan-rebal`, `lock-skip-sink-profit`, `profit-protect-lock`, `profit-protect-relax`.
- Floors/anchors de mercado:
- `outrate-floor`, `peg`, `peg-grace`, `peg-demand`, `revfloor`, `sink-floor`, `min`.
- Controles adaptativos:
- `circuit-breaker`, `extreme-drain`, `extreme-drain-unlock`, `extreme-drain-turbo`.
- Estagnacao e anti-lock:
- `stagnation`, `stagnation-rN`, `stagnation-cap-<ppm>`, `normalize-out`, `normalize-rebal`, `stagnation-floor`, `stagnation-floor-relax`, `stagnation-neg-override`, `stagnation-pressure`, `peg-paused-stagnation`.
- Low-out e falta de sinal:
- `low-out-slow-up`, `low-out-noflow-cap`, `no-signal-noup`, `no-signal-floor-relax`, `seed-liq-down-hold`.
- Discovery/explorer:
- `discovery`, `discovery-hard`, `explorer`, `drained-explorer*`, `surge*`.
- Sinais HTLC:
- `htlc-policy-hot`, `htlc-liquidity-hot`, `htlc-forward-hot`, `htlc-sample-low`, `htlc-neutral-lock`, `htlc-liq+X%`, `htlc-policy+X%`, `htlc-liq-nodown`, `htlc-policy-nodown`, `htlc-neutral-nodown`, `htlc-step-boost`.
- Super-source e inbound:
- `super-source`, `super-source-like`, `new-inbound`, `bootstrap`, `market-refill*`, `inb-<n>`.
- Rescue / liberacao seletiva de piso:
- `rescue`, `rescue-enter`, `rescue-exit`, `rescue-expired`, `rescue-floor-relax`, `rescue-global-relax`, `rescue-peg-paused`, `rescue-lowliq-block`, `rescue-lowliq-exit`.
- Seed e origem de fallback:
- `seed:native`, `seed:native-corrected`, `seed:amboss`, `seed:amboss-missing`, `seed:amboss-empty`, `seed:amboss-error`, `seed:med`, `seed:vol-<n>%`, `seed:ratio<factor>`, `seed:outrate`, `seed:mem`, `seed:default`, `seed:guard`, `seed:shock-*`, `seed:p95cap`, `seed:absmax`, `seed:outcap`, `seed:rebalcap`, `seed:rebalfloor`, `liq-low`, `liq-high`, `out-fallback-21d`, `rebal-fallback-21d`.

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
- Pula a confirmação manual do fechamento colaborativo e avança automaticamente para a etapa simulada de fechamento.
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
LightningOS pode expor um terminal web protegido usando GoTTY.

O instalador provisiona o terminal desativado. A conta `losop`
tem a senha Linux bloqueada, não pertence a grupos suplementares privilegiados e
recebe somente o ambiente dedicado do terminal.
Habilite ou desabilite o terminal pela página Terminal do LightningOS. A entrada
pelo teclado, o retorno ao modo somente leitura e a desativação exigem nova
confirmação da senha de administrador. Os comandos interativos continuam rodando
como o usuário restrito `losop`, dentro do sandbox do systemd, e somente o serviço
do terminal é reiniciado durante uma mudança de modo.
Você pode revisar as opções de runtime em `/etc/lightningos/terminal.env`:
- `TERMINAL_ENABLED=0` (gerenciado pela página Terminal)
- `TERMINAL_CREDENTIAL=user:pass`
- `TERMINAL_ALLOW_WRITE=0` (somente leitura) ou `1` (opt-in interativo explícito)
- `TERMINAL_PORT=7681` (opcional)
- `TERMINAL_WS_ORIGIN=^https://.*:8443$` (opcional, padrão permite todas as origens)

A rotação exige reautenticação recente no LightningOS e retorna a nova senha
GoTTY apenas uma vez. Ela nunca altera nem desbloqueia a senha Linux.

## Taproot Assets (experimental)

Ao instalar **Taproot Assets (tapd)**, o LightningOS libera uma página dedicada apoiada pelo daemon oficial e pela conexão com o LND local. A integração atual é mainnet, alpha e somente on-chain.

- Exibe status amigável do daemon e saldos de ativos.
- Descobre ativos em um catálogo de universe ou sincroniza manualmente um universe host.
- Inclui atalho de sync do BRLN e oferece ativos conhecidos/sincronizados no seletor de recebimento.
- Faz mint de novo ativo, reissue em grupo existente e permite escolher fee em sat/vbyte sugerida pelo mempool.
- Gera endereço de recebimento com QR, decodifica endereços, mostra preview de valor/custo on-chain estimado, envia e resgata.
- O LightningOS ignora transações-âncora do Taproot Assets nas notificações genéricas de envio on-chain para evitar alertas enganosos.
- O `tapd` standalone e o Fedimint Lightning Gateway são mutuamente exclusivos porque ambos precisam do interceptor HTLC do LND. Pare/desinstale um antes de instalar o outro.

Transferências Lightning de Taproot Assets não estão habilitadas nesse modo standalone; elas dependem do trabalho separado do edge node comunitário.

## Wallet Flow / grafo de proveniência (opcional)

A aba Wallet Flow do On-chain Hub renderiza um grafo Sankey das transações tocadas pela carteira, incluindo ancestrais e contrapartes externas. Para decodificar txids arbitrários, o backend usa a primeira fonte disponível nesta ordem:

1. **Bitcoin Core local** — fonte preferencial quando está sincronizado, não podado e com `txindex=1` pronto.
2. **Electrs local** — fallback configurável por `ELECTRUM_RPC_ADDR` (padrão `127.0.0.1:50001`).
3. **Servidores Electrum públicos** — fallback mainnet habilitado por padrão e configurável por `PROVENANCE_PUBLIC_ELECTRUM`.

> **Aviso de privacidade:** ao usar o fallback público, o operador Electrum pode observar os txids consultados. Defina `PROVENANCE_PUBLIC_ELECTRUM=disabled` em `/etc/lightningos/secrets.env` para desativá-lo.

Use `PROVENANCE_PRIMARY=chain|bitcoind|electrs` para manter a cadeia padrão ou fixar uma única fonte local. A UI mostra a fonte ativa, saúde e freshness; a API de métricas expõe hits, erros, fallthroughs e latência por classe de fonte. O Wallet Flow também oferece rebuild, status e telemetria, e os relatórios diários incluem alerta quando a proveniência está ausente, atrasada ou com erro.

## Notas de segurança
- A seed phrase nunca é armazenada. Ela é mostrada uma vez no assistente.
- Credenciais RPC são armazenadas apenas em `/etc/lightningos/secrets.env` (root:lightningos, `chmod 660`).
- API/UI bindam em `0.0.0.0` por padrão para acesso LAN. Para localhost-only, defina `server.host: "127.0.0.1"` em `/etc/lightningos/config.yaml`.
- Defina `UTXO_LOCK_REQUIRES_REAUTH=true` para exigir o mesmo escopo de reautenticação de envio da carteira antes de lock/unlock de UTXO.
- Ações sensíveis da API são gravadas em `audit_events` no Postgres e podem ser revisadas na página Auditoria.
- Eventos de auditoria são removidos após `AUDIT_EVENTS_RETENTION_DAYS` dias (padrão `365`). Use `0` ou `forever` para mantê-los indefinidamente.
- O fallback Electrum público amplia a cobertura do Wallet Flow, mas revela os txids consultados ao servidor escolhido; desative-o se isso não atender ao seu modelo de privacidade.

## Troubleshooting
Se `https://<IP_LAN_DO_SERVIDOR>:8443` não estiver acessível:
```bash
systemctl status lightningos-manager --no-pager
journalctl -u lightningos-manager -n 200 --no-pager
ss -ltn | grep :8443
```

### Catálogo da App Store

O registro do backend expõe atualmente 19 apps/serviços. Os arquivos dos apps são gerenciados pelo LightningOS, os dados persistentes ficam separados e o Docker só é instalado quando um app precisa dele.

| App | Finalidade e integração atual |
| --- | --- |
| **Bitcoin Core** | Node mainnet local em Docker. Usa `/data/bitcoin` por padrão, aceita storage customizado já montado na instalação e configura `txindex=1` para consumidores de índice completo. |
| **Bark Wallet** | Carteira self-custodial beta para Ark, Lightning e on-chain, servida via HTTPS local com login gerenciado pelo LightningOS. Usa o operador Ark mainnet público da Second, não usa o LND local e preserva os dados ao desinstalar. |
| **Electrs** | Indexador Electrum para o Bitcoin Core local com índice completo; expõe TCP `50001`, métricas locais em `127.0.0.1:4224` e progresso de indexação/sync na loja. |
| **Mempool** | Stack mempool.space self-hosted na porta `8999`; exige Bitcoin Core e Electrs locais instalados, rodando e prontos. |
| **Fedimint Guardian** | `fedimintd` para federações solo ou multi-guardian via Iroh, usando o backend Bitcoin ativo. |
| **Fedimint Lightning Gateway** | Gateway independente `gatewayd lnd` que conecta o LND local a federações Fedimint via Iroh. Não pode rodar junto com Taproot Assets standalone porque ambos precisam do interceptor HTLC do LND. |
| **LNDg** | Analytics e automação avançados para LND em Docker, na porta `8889`, com credenciais admin gerenciadas e integração ao LND local. |
| **LNbits** | Plataforma de contas/carteiras Lightning e extensões financiada pelo LND local. |
| **BTCPay Server** | Processador self-hosted de pagamentos Bitcoin e Lightning integrado à stack local do node. |
| **Elements** | Serviço Liquid Elements nativo com RPC em `127.0.0.1:7041`, mainchain Bitcoin local/remota selecionável, detecção do node local e diretório de dados customizado opcional em volume já montado. |
| **Peerswap** | `peerswapd` nativo com `psweb` na porta `1984`; usa Elements local ou uma fonte RPC remota testada, com wallet específica do node no modo remoto. |
| **RoboSats Gateway** | Cliente RoboSats self-hosted para negociação P2P de Bitcoin via Tor, fixado em release testada e exposto pelo proxy HTTPS do LightningOS. |
| **Public Pool** | Backend e UI self-hosted de pool para solo mining com suporte a Bitcoin RPC local ou remoto. |
| **CPU Lottery Miner** | Minerador solo opcional por CPU contra o Public Pool local. A quantidade de threads é ajustável na UI e eventual recompensa vai diretamente para o endereço da carteira LND. |
| **Buy DePix** | Checkout integrado de PIX para DePix com criação de cotação/pedido, acompanhamento de status e página dedicada. |
| **FSwap** | Pagamento de boletos e contas brasileiras com sats do node Lightning local pelo fluxo dedicado Pagar Boleto. |
| **Taproot Assets (tapd)** | `tapd` oficial standalone conectado ao LND local para descoberta on-chain, sync de universe, mint/reissue, recebimento, preview/envio e resgate de ativos. Alpha experimental em mainnet; transferências Lightning de ativos dependem do trabalho separado do edge node comunitário. |
| **Lightning Loop** | Cliente oficial de swaps da Lightning Labs com instalação gerenciada pelo LightningOS e integração ao LND local. |
| **Loop Out BR⚡LN** | Fluxo nativo de liquidez de saída que divide um total em pagamentos controlados para Lightning Address, preserva um piso configurável de saldo local e mantém o histórico de jobs, pagamentos e eventos no LightningOS. |

Os detalhes do Fedimint estão no [guia de configuração](27_FEDIMINT_CONFIGURATION_PT_BR.md) ([EN](28_FEDIMINT_CONFIGURATION_EN.md)).

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

## Licenca
Licenciado sob a Licenca MIT. Veja `LICENSE` para o texto canonico e `LICENSE.pt-BR.md` para a traducao informativa em PT-BR.


