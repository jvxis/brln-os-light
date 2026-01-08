# FILE: docs/01_REQUIREMENTS_PRD.md

# PRD — LightningOS Light (v0.1)

## 1. Personas
### P1 — Iniciante
Quer “rodar Lightning” sem instalar Bitcoin local. Precisa de UI guiada e clara.

### P2 — Intermediário
Quer UI bonita mas também quer acessar logs e aprender com o sistema.

## 2. User Stories (MVP)
1) Como usuário, quero abrir uma interface web e ver se “está tudo ok”.
2) Como usuário, quero conectar meu LND a um Bitcoin remoto comunitário informando RPC user/senha.
3) Como usuário, quero criar uma wallet nova e ver minhas 24 palavras para anotar (e o sistema não pode guardar isso).
4) Como usuário, quero importar uma wallet existente digitando minhas 24 palavras.
5) Como usuário, quero desbloquear (unlock) minha wallet ao iniciar.
6) Como usuário, quero gerar invoice e pagar invoice numa wallet web simples.
7) Como usuário, quero ver sinais vitais do sistema (CPU/RAM/DISK/uptime).
8) Como usuário, quero ver a saúde do Postgres e do LND.
9) Como usuário, quero ver vida útil do disco (wear/SMART), com alertas.

## 3. Requisitos Funcionais
### Dashboard
- Exibir status geral (OK/WARN/ERR) com lista de problemas detectados.
- Cards:
  - Sistema (CPU, RAM, disco, uptime)
  - Discos (wear/SMART + estimativa heurística)
  - PostgreSQL (status, conexões, DB size)
  - Bitcoin (modo remoto BRLN, RPC OK/Fail, ZMQ OK/Fail)
  - LND (locked/unlocked, synced_to_chain, synced_to_graph, blockheight, canais ativos/inativos, saldos)

### Wizard (primeiro acesso)
- Passo 1: “Conectar ao Bitcoin remoto”
  - Solicitar rpcuser e rpcpass
  - Testar RPC e ZMQ
  - Salvar com segurança (arquivo root:lightningos 660 ou secrets store local)
- Passo 2: “Wallet do LND”
  - Escolha: Criar nova wallet / Importar wallet
  - Criar: gerar seed, mostrar 24 palavras UMA VEZ, exigir confirmação do usuário (“eu anotei”)
  - Importar: campo para digitar 24 palavras (com validação básica)
- Passo 3: Unlock wallet
- Passo 4: Finalizar e ir ao Dashboard

### Wallet Web (MVP)
- Ver saldo on-chain e saldo lightning
- Gerar invoice
- Pagar invoice (colando invoice)
- Histórico básico (opcional; se não, mostrar “últimos 20 pagamentos/recebimentos” se fácil)

### Operações
- Botões (somente LAN/VPN):
  - Restart LND
  - Restart LightningOS Manager
- Visualizar logs recentes:
  - LND logs (tail)
  - Manager logs (tail)

## 4. Requisitos Não-Funcionais
- Sem Docker no core.
- Services via systemd.
- Princípio de menor privilégio (usuários `lnd` e `lightningos`).
- UI e API em HTTPS local (self-signed) ou HTTP local com recomendação de VPN. (Preferência: HTTPS self-signed).
- Não armazenar seed phrase.
- Não mostrar credenciais depois de salvas.
- Resiliente a reboot (auto-start).
- Observabilidade: logs estruturados no manager.

## 5. Critérios de Aceite (MVP)
- Instalação termina com serviços UP e UI acessível em `https://localhost:8443`.
- Usuário consegue:
  - inserir rpcuser/rpcpass e validar conectividade
  - criar OU importar wallet
  - unlock wallet
  - gerar invoice e pagar invoice
- Dashboard mostra status coerente.
- Discos mostram wear/SMART e alertas.
- Nenhuma porta WAN aberta por padrão.

# FILE: docs/01_REQUIREMENTS_PRD.md  (APPEND)

## Configurações do LND (MVP)
- Deve existir uma tela "LND -> Configurações" para alterar:
  - Node alias
  - tamanho mínimo de canal
  - tamanho máximo de canal
- A UI deve editar o arquivo /data/lnd/lnd.conf apenas nas chaves suportadas.
- O sistema deve persistir as alterações no arquivo /data/lnd/lnd.conf.
- Ao salvar, o sistema deve validar os valores e reiniciar o LND (apply_now=true por padrão).
- Deve existir modo avançado opcional para editar o lnd.conf com rollback em caso de falha.
