# Plano de revisão e correção do Dependabot

Data: 2026-09-28. Repositório: `jvxis/brln-os-light`.

> **Registro histórico, incorporado à PR #211 em 30/09/2026.** O levantamento,
> as referências ao checkout e os checklists abaixo refletem a elaboração do
> plano em 28/09; não representam o estado atual de implementação ou validação.
> A execução da 0.5.40 está documentada em [Preparação do Go](GO_UPGRADE_0.5.40.md)
> e no [relatório de integração](baselines/go-upgrade-0.5.40-virtualbox-2026-09-30.md),
> incluindo as pendências e a dispensa explícita dos testes nativos ARM.
> As correções de dependências da 0.5.41 seguem na
> [PR #214](https://github.com/jvxis/brln-os-light/pull/214).
> Os números de alertas e as versões candidatas são os do levantamento datado;
> esta incorporação documental não constitui uma nova auditoria.

Status: levantamento inicial concluído; plano ampliado para atualização automática do Go pelo upgrade interno e revisão das versões dos produtos nos instaladores. Correções e validações funcionais ainda não executadas. Este documento não atesta ausência de impacto nem autoriza mudanças em nós operacionais.

Requisito do usuário: usuários de instalações existentes devem receber os pré-requisitos pelo upgrade de versão dentro do LOS, sem executar `install.sh`, `install_existing.sh` ou `install_existing_pi.sh`. Esses scripts continuam destinados à primeira instalação do LOS, inclusive sua integração inicial em um node existente.

Decisão do usuário: **0.5.40 será uma atualização intermediária obrigatória para instalações anteriores, antes de qualquer release posterior.** A obrigatoriedade deve ser aplicada pelo fluxo de atualização; instruções nas release notes não bastam. Novas instalações podem iniciar diretamente numa release posterior, desde que seus pré-requisitos sejam preparados e validados pelo instalador.

## Entregas propostas: 0.5.40 e 0.5.41

A branch `agent/0.5.40-release` foi confirmada localmente e no remoto. A divisão abaixo organiza a execução; não representa implementação, merge ou autorização de publicação. O plano continua no checkout atual até sua integração na branch de trabalho apropriada.

| Release | Escopo | Condição de entrega |
| --- | --- | --- |
| **0.5.40: preparar upgrades** | Entregar preparação automática e verificada do Go no upgrade interno; alinhar versões/requisitos dos instaladores; detectar Node/npm e demais requisitos críticos; incluir progresso, repetição segura e rollback. Avaliar manutenção do PostgreSQL conforme necessidade comprovada. | A própria 0.5.40 deve continuar compilável pelo caminho de upgrade antigo. Comprovar transição de versão anterior para 0.5.40 e dela para uma candidata 0.5.41, sem instaladores manuais. |
| **0.5.41: corrigir dependências** | Elevar requisitos Go e atualizar pgx, gRPC/x/net/x/crypto, chi e ferramentas da UI, em lotes revisáveis, com validação funcional. | Usar a preparação entregue na 0.5.40 e comprovar também o caminho para usuários que a pularem; confirmar fechamento dos alertas e estabilidade. |

**Limite importante da primeira entrega:** preparar a atualização do Go no novo helper não significa que o salto para a própria 0.5.40 já executará essa preparação. Quem inicia esse salto é o helper antigo. A 0.5.40 pode entregar a capacidade que será acionada no upgrade seguinte. Se for necessário atualizar o Go ainda na chegada à 0.5.40, desenhar e testar uma etapa adicional compatível com o fluxo antigo. Não elevar antecipadamente `go.mod` ou dependências a ponto de impedir a instalação da release preparatória.

**PostgreSQL:** os três alertas deste levantamento pertencem ao driver Go `pgx`, não ao servidor PostgreSQL. A correção desse driver fica na 0.5.41, junto da toolchain compatível. Para a 0.5.40, inventariar a versão do servidor e avaliar patches de segurança dentro da major já instalada; só incluir sua aplicação automática se houver necessidade e homologação de backup, reinício, recuperação e compatibilidade. Migração de major do PostgreSQL deve ser uma entrega própria, com estratégia de dados, e não um pré-requisito presumido para corrigir pgx.

**Outros componentes críticos:** antecipar apenas o que for necessário ao próximo upgrade ou tiver exposição urgente comprovada. Node/npm entram no preparo quando seus requisitos demandarem atualização. LND e demais serviços mantêm avaliação e homologação específicas. A separação por release não deve adiar uma correção urgentemente necessária se a análise comprovar exploração alcançável.

**Gate de publicação:** comprovar a sequência versão anterior → 0.5.40 → versão posterior. Para instalações legadas, o destino oferecido deve permanecer 0.5.40 mesmo depois da publicação de 0.5.41 e seguintes. A política atual de selecionar somente a release mais recente não impõe essa passagem.

### Como impor a passagem pela 0.5.40

O cliente examinado consulta diretamente `https://api.github.com/repos/jvxis/brln-os-light/releases?per_page=10`, percorre a resposta e aceita a primeira release não draft com versão/tag válida. Ele também aceita prereleases: o indicador serve para classificá-las como beta. Não lê um campo de versão mínima nem a marcação de release obrigatória. O início do upgrade exige o mesmo destino resolvido nessa consulta.

Consequências: marcar 0.5.40 como obrigatória apenas em seu próprio código não altera clientes anteriores; publicar 0.5.41 como prerelease no mesmo catálogo também não a oculta deles. A marcação visual de "Latest" ou um aviso textual não é uma barreira comprovada para esse algoritmo.

**Estratégia proposta para implementação:** manter o catálogo consultado pelos clientes legados limitado ao destino de transição 0.5.40 e entregar nessa versão um resolvedor que consulte um catálogo distinto para releases posteriores. O novo catálogo pode usar um repositório de distribuição dedicado ou outro mecanismo autenticado, a definir antes da publicação; mover somente o código da branch não altera a descoberta de releases. Preservar resolução verificável de tag/commit, vínculo com o código-fonte oficial e integridade dos artefatos.

Essa separação é uma proposta de arquitetura, ainda não implementada. Se for adotada outra solução, ela precisa demonstrar o mesmo resultado usando clientes antigos sem modificá-los previamente. Não publicar uma release posterior que esses clientes possam selecionar até essa garantia estar validada.

- [ ] Conferir a descoberta de releases em todas as versões de origem suportadas, incluindo mecanismos legados distintos do checkout examinado.
- [ ] Definir o catálogo de transição e o catálogo pós-0.5.40, cache, autenticação/verificação e política de indisponibilidade, preservando a 0.5.40 como destino acessível de forma duradoura.
- [ ] Na 0.5.40, distinguir a última versão publicada do próximo destino permitido e validar os requisitos antes de iniciar o upgrade. Atualizar API/UI/documentação se o contrato mudar.
- [ ] Após concluir a instalação e os healthchecks da 0.5.40, oferecer a release posterior. Uma tentativa incompleta, falha de migração ou troca apenas de `version.txt` não deve contar como preparação bem-sucedida.
- [ ] Manter indicação clara na UI: "Atualize primeiro para 0.5.40" quando houver suporte no cliente. Nos clientes antigos, a própria descoberta deve entregar esse destino, sem depender de uma interface nova.
- [ ] Testar cliente anterior sem alterações, com 0.5.41 e releases futuras publicadas no catálogo novo: deve continuar descobrindo/instalando 0.5.40 pelo caminho antigo.
- [ ] Testar cache antigo/expirado, API indisponível, tentativa direta de destino posterior, rollback e nova tentativa; nenhuma falha deve liberar o salto proibido.
- [ ] Testar uma instalação antiga após várias releases posteriores, além de primeira instalação diretamente na versão nova. A política não deve obrigar reinstalação ou downgrade de nós já atualizados.

## Base da análise

- API autenticada do GitHub, com paginação: **36 alertas abertos**, em 10 pacotes; 11 críticos, 8 altos, 15 médios e 2 baixos. A severidade é a informada pelo Dependabot, não uma medida da exposição efetiva do LOS.
- Manifestos: `lightningos-light/go.mod` e `lightningos-light/ui/package-lock.json`.
- Checkout examinado: `a8f804d0e23e0ff005645e0dc7856d128d8982c7`, branch `agent/0.5.39-seed-v2-counters-close-risk`.
- `main` remoto consultado: `ea829834be0e85c12c41775da7cc84b209ef0857`. O checkout diverge dele (1 commit à frente e 7 atrás). Os conteúdos de `go.mod`, `ui/package.json` e `ui/package-lock.json` foram comparados e são iguais. Repetir a análise de alcance no commit escolhido para implementação.
- Foram lidas as descrições dos 36 alertas, examinados imports e configurações e consultados metadados oficiais de versões no Go Module Proxy e npm.
- `go list -mod=readonly -deps ./cmd/...`, com alvo Linux/amd64, foi usado para conferir os pacotes efetivamente importados pelos comandos do projeto. Não substitui análise de chamadas com `govulncheck` nem inspeção dos binários instalados.
- Não havia PRs abertos na consulta. Não foram alteradas dependências, executados testes funcionais, publicados commits ou acessados nós.

## Agrupamentos e versões candidatas

Versões abaixo são candidatas para avaliação, não versões já homologadas. Reconsultar advisories, releases e suporte antes de implementar.

| Grupo | Alertas | Versão atual | Correção candidata e relação |
| --- | --- | --- | --- |
| gRPC + módulos Go relacionados | 25: 5 gRPC, 17 crypto, 3 net | gRPC 1.70.0; crypto 0.30.0; net 0.32.0 | Avaliar gRPC 1.83.2: seu manifesto requer net 0.58.0 e crypto 0.55.0, acima dos pisos de correção 0.55.0 e 0.52.0. Conferir versões finais selecionadas pelo Go. |
| PostgreSQL/pgx | 3: #9–#11 | 5.5.5 | Avaliar 5.9.2, que cobre os dois avisos de memória e o de SQL. São falhas distintas, mesmo quando compartilham versão corretiva. |
| Roteador chi | 1: #5 | 5.0.10 | Piso corretivo 5.2.2; conferir release escolhida e comportamento de rotas. |
| Vite + esbuild | 4: #30–#33 | Vite 5.4.21; esbuild 0.21.5 | Avaliar Vite 6.4.3, que depende de esbuild ^0.25.0. Evitar forçar esbuild incompatível dentro do Vite 5. |
| Browserslist + baseline | 2: #35 e #37 | 4.28.1; 2.10.0 | Selecionar Browserslist >=4.28.7 e baseline >=2.11.0; atualizar um não garante sozinho que o lockfile selecione o outro corrigido. |
| Parser CSS | 1: #34 | 6.1.2 | Selecionar 6.1.3 na faixa aceita pelo Tailwind 3.4.19. Não exige migrar Tailwind para outra major. |

**Cuidado com gRPC:** o campo simplificado do alerta #29 informa primeiro patch 1.82.2, mas o advisory discrimina correções por linha: 1.82.2, 1.83.2 e 1.84.0. Escolher 1.83.1 apenas por ser numericamente maior que 1.82.2 deixa de considerar esse backport. A candidata 1.83.2 também cobre #27 e #28.

O grafo de gRPC 1.83.2 eleva ainda protobuf, genproto, x/sys, x/sync e x/text, entre outros. O fechamento potencial de 25 alertas tem como contrapartida uma mudança ampla no transporte e nas dependências compartilhadas; revisar o diff completo.

## Exposição preliminar e risco de regressão

**Criptografia:** os 17 avisos tratam de SSH/agent. O grafo dos comandos Linux/amd64 examinado não importa `golang.org/x/crypto/ssh` nem `ssh/agent`. Os usos diretos encontrados são Argon2 em autenticação, bcrypt no Fedimint e RIPEMD-160 no balanced open; há usos indiretos como PBKDF2 e SHA3. A evidência reduz a exposição aos avisos de SSH, mas não elimina a necessidade de corrigir a dependência e testar esses usos reais. O SSH do sistema operacional é um componente separado.

**gRPC:** o LOS usa cliente gRPC com TLS e macaroon para falar com LND. `grpc.NewServer()` apareceu em testes, e não foi encontrado import de xDS/authz nos comandos. Os avisos de autorização/servidor não demonstram por si só uma falha explorável no manager. O aviso de fragmentação #27 exige análise adicional dos caminhos de transporte compartilhados pelo cliente. Atualizar o módulo do manager não atualiza a versão de gRPC embutida no daemon LND.

**PostgreSQL:** pgx é usado efetivamente por pool de conexões e persistência. Os dois avisos críticos apontam a símbolos de decodificação do lado servidor do protocolo (`pgproto3.Backend.Receive`, `Bind.Decode`, `FunctionCall.Decode`); o LOS se conecta como cliente, e não foram encontradas chamadas explícitas desses símbolos no código próprio. Validar alcance com `govulncheck`, sem tratar os dois avisos como duplicatas. O aviso de SQL depende de protocolo simples, dollar quoting e entrada controlável; não foi encontrada configuração explícita de protocolo simples, mas opções de DSN e caminhos internos ainda precisam ser verificados sem expor credenciais.

**chi e x/net:** `RedirectSlashes` não está registrado no roteador examinado. Os comandos não importam `x/net/html` nem o pacote externo `x/net/http/httpproxy`. Existe implementação vendorizada pela biblioteca padrão de Go; seu ciclo de correção é o da toolchain, não o do módulo x/net do projeto.

**Interface:** os sete avisos npm estão marcados como desenvolvimento. O Go serve os arquivos estáticos da SPA; as falhas do dev server não se transferem automaticamente à interface publicada. Ainda afetam a estação Windows de desenvolvimento e o processo de build executado pelos instaladores. `vite.config.ts` não habilita `server.host`, mas flags de execução podem fazê-lo. Não reproduzir ataques contra o ambiente de trabalho real.

## Etapas de execução

### 1. Fixar baseline e concluir análise de alcance

- [ ] Abrir branch/worktree a partir do `main` atualizado, preservando a branch atual.
- [ ] Salvar inventário datado por alerta: GHSA/CVE, pacote, caminho, versão, cadeia de dependências, condição de exploração, versão corretiva, alcance e validação exigida.
- [ ] Rodar baseline de build/testes antes das atualizações e registrar falhas preexistentes.
- [ ] Executar `govulncheck` no código para Linux e, quando aplicável, nos binários reais; separar pacote presente de função vulnerável alcançável. Cobrir amd64 e arm64.
- [ ] Executar `npm audit --json` completo e conferir a árvore do lockfile; a análise com `--omit=dev` é complementar, pois ocultaria os sete alertas npm encontrados.
- [ ] Conferir release notes e correções de todos os saltos, inclusive transitivos. Registrar alertas adicionais descobertos pelo audit ou pelo scanner da biblioteca padrão.

Saída: matriz individual validada, baseline reproduzível e lotes finais definidos. Não encerrar alertas como falso positivo somente porque uma busca textual não encontrou um import.

### 2. Preparar toolchain e caminho de atualização

Os manifestos consultados de pgx 5.9.2, gRPC 1.83.2, crypto 0.52.0/0.55.0 e net 0.55.0/0.58.0 exigem **Go >=1.25.0**. Hoje `go.mod` declara Go 1.24/toolchain 1.24.12; os três instaladores fixam 1.24.12. A exigência mínima não é uma recomendação para instalar 1.25.0 sem os patches posteriores.

- [ ] Escolher uma toolchain ainda suportada e corrigida na data da implementação; revisar o requisito máximo de todo o grafo resolvido.
- [ ] Sincronizar `go.mod`, `install.sh`, `install_existing.sh`, `install_existing_pi.sh`, checksums oficiais por arquitetura, documentação e testes que fixam a versão.
- [ ] Inspecionar o helper completo de upgrade chamado pelo broker, inclusive assets embutidos, verificações de integridade e rollback. Garantir que o upgrade a partir de uma instalação antiga obtenha a toolchain necessária antes de compilar.
- [ ] Validar instalação limpa e atualização de instalação existente. Testar a disponibilidade da toolchain com ambiente restrito; não depender acidentalmente do download automático de `GOTOOLCHAIN`.
- [ ] Compilar manager, broker e mesh, onde suportado; executar testes em Linux, pois compilação cruzada no Windows não exercita systemd, permissões ou sockets reais.

Saída: base de build/upgrade validada, necessária antes dos lotes Go.

### 2.1. Atualização automática do Go pelo upgrade interno

**Comportamento atual confirmado no código:**

- `internal/server/app_upgrade.go` incorpora `assets/upgrade-app.sh` no binário em execução e envia esse conteúdo ao broker. Portanto, quem inicia o upgrade é o helper da versão instalada, não o helper novo presente no destino.
- `internal/privileged/lightningos_upgrade.go` fixa o SHA-256 esperado desse helper. Alterações no script precisam atualizar o digest e os testes correspondentes de forma coerente; a transição deve preservar essa verificação.
- O helper atual resolve `GO_BIN` antes de preparar o worktree, exige que Go já exista e usa esse executável nos três builds. Não instala explicitamente uma nova toolchain nem confere uma versão mínima antes da compilação.
- O fluxo confere tag, commit e versão, prepara um worktree controlado por root, compila manager/broker/mesh e executa `npm ci`/build antes da publicação da aplicação. A preparação de Go deve se integrar a esse fluxo.
- Nos instaladores, `install_go()` aceita qualquer Go com minor >=24: alterar somente `GO_VERSION` não atualiza essa condição e pode continuar aceitando uma versão incompatível ou um patch antigo.

**Fluxo proposto para o helper com suporte à migração:**

1. Validar a identidade da release e seu checkout com as verificações existentes.
2. Ler de metadados versionados da release a toolchain homologada, os requisitos mínimos, a arquitetura e os checksums. Compartilhar essa definição com os instaladores para evitar versões divergentes. Usar formato de dados validado, sem tratar valores remotos como comandos.
3. Detectar o Go realmente disponível, considerando PATH, versão completa (major/minor/patch), arquitetura e configuração de toolchain. Não usar somente `minor >=24` nem fazer downgrade silencioso de toolchains mais novas; selecionar uma versão homologada para o build do LOS.
4. Quando necessário, baixar a distribuição oficial com HTTPS e checksum fixado; extrair em staging controlado por root, validar o executável e preparar a toolchain sem apagar antecipadamente a instalação funcional. Dimensionar espaço para download, staging e cópia anterior.
5. Selecionar explicitamente o executável preparado para os builds. Preferir diretório versionado gerenciado pelo LOS, com política documentada para `/usr/local/go` e instalações mantidas por apt; evitar substituir arquivos de outro gerenciador de pacotes.
6. Executar um preflight de compilação com a toolchain escolhida antes de qualquer migração de identidade/permissões ou publicação de binários. Depois, compilar manager/broker/mesh e UI com versões conhecidas. No caminho gerenciado, impedir downloads implícitos de outra toolchain durante o build.
7. Publicar a aplicação, validar saúde e registrar a versão efetivamente usada. Exibir progresso como preparação de requisitos, download/verificação, compilação e conclusão, com erro recuperável pelo fluxo normal do LOS.
8. Em falha de download, checksum, extração ou preflight, manter a aplicação anterior operante. Em falha posterior, acionar a reversão da aplicação e restaurar a seleção anterior da toolchain se tiver sido alterada. Manter a toolchain anterior até a homologação da nova instalação; execução de binários já compilados não depende de trocar o Go do sistema.

Não chamar os instaladores de primeira instalação como atalho dentro desse fluxo.

### 2.2. Primeira transição e usuários que pulam releases

Este é um bloqueio de desenho a resolver **antes de elevar os requisitos da release publicada**: o helper antigo não passa a executar uma etapa nova apenas porque ela existe no checkout de destino.

- [ ] Inventariar helpers e configurações das versões de origem suportadas; testar com esses binários reais no laboratório, não somente com o helper novo sobre código antigo.
- [ ] Avaliar primeiro uma release preparatória, ainda compilável com a toolchain anterior, que entregue o helper capaz de preparar Go e mantenha manager/broker/digest compatíveis.
- [ ] Aplicar a decisão de passagem obrigatória pela 0.5.40 mediante descoberta compatível com clientes antigos, conforme a estratégia de catálogos acima. O endpoint atual só aceita `target_version` igual ao destino resolvido; uma ponte publicada isoladamente não garante a migração de quem pula versões.
- [ ] Testar separadamente a troca automática nativa de toolchains pelo Go, que pode permitir o primeiro salto com Go >=1.21 e `GOTOOLCHAIN=auto`. Esse mecanismo não equivale a atualizar `/usr/local/go` e depende de configuração, rede, proxy, cache e verificação de downloads. Não presumir que funcione com `GOTOOLCHAIN=local` ou ambientes restritos.
- [ ] Escolher e documentar o caminho efetivo da primeira migração para 0.5.40, preservando a cadeia de confiança. A troca automática de toolchain pode ser estudada como mecanismo técnico, mas não como exceção à passagem obrigatória. Não remover a validação do hash do broker para fazer o helper novo rodar no broker antigo.
- [ ] Liberar a release com dependências que exigem Go novo somente depois de comprovar o caminho para cada origem suportada. Se o salto ainda não funcionar, manter essa parte da publicação pendente; reinstalação manual não atende ao requisito.

Referência: [seleção e download de toolchains no Go](https://go.dev/doc/toolchain).

### 2.3. Versões recentes dos produtos nas novas instalações

Criar uma matriz de versões homologadas por release do LOS, distinguindo produto, versão atual, candidata, suporte, arquitetura, origem do artefato e verificação de integridade. As versões abaixo são o estado encontrado no checkout, não recomendações de atualização.

| Componente | Estado encontrado | Trabalho previsto |
| --- | --- | --- |
| Go | 1.24.12 nos três instaladores; teste de aceitação por minor >=24 | Selecionar patch suportado que atenda ao grafo corrigido; compartilhar versão/requisitos/checksums com o upgrade e corrigir comparação de versões. |
| Node.js/npm | Node major 24 via NodeSource | Confirmar patch e npm compatíveis com a UI; fixar política de suporte e validar detecção em PATH e repositório autenticado. Preparar atualização interna de Node se alguma release passar a exigir isso. |
| LND | `install.sh` fixa 0.21.3-beta | Revisar versão upstream e compatibilidade com RPCs/stubs e apps; atualizar o padrão de instalação após homologação. Na integração inicial em node existente, respeitar o LND já instalado e seu fluxo específico de atualização. |
| GoTTY | 1.8.0, com artefatos/checksums amd64 e arm64 | Conferir versão mantida, advisories e compatibilidade do terminal antes de trocar o padrão. |
| PostgreSQL | Política `latest`, com reaproveitamento de instalação existente e fallback no script | Tornar explícitas as majors suportadas/testadas por Ubuntu e arquitetura. Escolher padrão de novas instalações; migração de major de banco existente exige fluxo próprio e teste de dados. |
| Tor, i2pd e pacotes de sistema | Instalação por repositórios autenticados | Inventariar candidatos suportados por distribuição, chaves e política de atualização; validar integração/rede após mudanças. |

- [ ] Consultar releases/advisories oficiais para cada candidata no início da implementação; escolher versões recentes e compatíveis, sem transformar toda instalação em adoção automática da última major disponível.
- [ ] Atualizar `install.sh`, `install_existing.sh` e `install_existing_pi.sh` de forma coerente, inclusive checksums, validação de versões já instaladas e documentação.
- [ ] Reutilizar a biblioteca de verificação de artefatos ou uma implementação compartilhada com o upgrade, mantendo a origem confiável dos metadados.
- [ ] Revisar produtos adicionais descobertos durante o inventário; componentes do App Store mantêm sua própria matriz e ciclo quando não forem pré-requisito do upgrade do LOS.
- [ ] Homologar instalação inicial em máquina limpa e integração inicial em node existente, preservando dados e serviços existentes. Atualização do driver pgx não implica atualização da major do servidor PostgreSQL.

### 2.4. Testes obrigatórios da preparação de requisitos

- [ ] Origem suportada com Go antigo, cache vazio e nenhuma preparação manual: concluir atualização pela UI.
- [ ] Origem que pula a release preparatória: comprovar o caminho interno até a versão final.
- [ ] Go ausente no helper novo, versão abaixo do patch mínimo, versão válida, versão mais nova e executáveis diferentes em PATH; usar a toolchain prevista e documentar o comportamento.
- [ ] Download interrompido, checksum incorreto, espaço insuficiente e arquitetura incorreta: falhar antes de publicar a aplicação e permitir nova tentativa.
- [ ] `GOTOOLCHAIN=auto` e `local`, com e sem cache, proxy configurado e indisponibilidade da origem: distinguir bootstrap antigo de preparação explícita nova.
- [ ] Duas solicitações concorrentes: manter exclusão pelo lock; repetição após sucesso não reinstala requisitos desnecessariamente.
- [ ] Falha nos builds, no healthcheck ou após seleção da toolchain: ensaiar rollback e confirmar aplicação anterior, dados e serviços operantes.
- [ ] `--verify-only`: continuar sem alterar toolchain, aplicação ou serviços.
- [ ] Cobrir Linux amd64/arm64 e Ubuntu suportados; executar instaladores somente nos cenários de primeira instalação, nunca como preparação oculta do teste de upgrade.

### 3. Corrigir em lotes revisáveis

Ordem inicial proposta, ajustável se a análise de alcance encontrar exposição maior:

1. **Preparação da migração e toolchain**, resolvendo primeiro a transição do helper antigo, usuários que pulam releases e os testes de instalação/upgrade acima.
2. **pgx**, validando persistência e recuperação de conexões. Manter em lote próprio para isolar regressões de banco.
3. **gRPC + x/net + x/crypto e transitivos exigidos**, aproveitando a correção conjunta. Conferir o grafo depois de `go mod tidy`; preservar stubs gerados de LND, salvo incompatibilidade comprovada que exija uma tarefa específica.
4. **chi**, com validação de rotas, middleware e terminal.
5. **Vite + esbuild**, avaliando a migração 5→6 e o suporte da versão escolhida. O plugin React já está travado em 4.7.0 e declara compatibilidade com Vite 6; confirmar no build. Preservar a deduplicação de d3 da configuração atual.
6. **Browserslist/baseline + parser CSS**, com atualização dirigida do lockfile e revisão visual. Autoprefixer 10.4.24 admite Browserslist corrigido; Tailwind 3.4.19 admite parser 6.1.3.

Os dois últimos lotes não dependem da toolchain Go. Usar versões explícitas e revisar alterações transitivas; evitar `npm audit fix --force`, `go get -u ./...` ou atualização geral sem delimitação. Cada lote deve indicar quais alertas espera fechar, testes executados e método de reversão.

A revisão dos produtos dos instaladores é uma frente adicional, com mudanças separadas por componente e homologação própria. As versões de Go/build precisam acompanhar esta correção; mudanças de LND, PostgreSQL e serviços têm seus critérios específicos de compatibilidade.

### 4. Validar o funcionamento do LOS

| Área | Validação necessária |
| --- | --- |
| Build/backend | `go test ./...`, `go vet ./...`, build dos comandos aplicáveis e `go test ./internal/server -run TestValidateAppRegistry`; `-race` nos pacotes alterados quando suportado. Comparar com baseline. |
| Autenticação | Login com hashes existentes, logout, sessão, CSRF, reautenticação e erro de senha; verificar compatibilidade Argon2/bcrypt sem registrar segredos. |
| LND/gRPC | TLS, macaroon, status, canais, grafo, streams, deadlines, cancelamento e reconexão. Exercitar fluxos de pagamentos/canais com mocks ou ambiente apropriado; não movimentar fundos mainnet como teste automático. |
| PostgreSQL | Pool, reconexão, transações, migrações, NULL/arrays/JSON, relatórios, histórico, notificações e automações. Rodar integração em PostgreSQL descartável; testes de banco pulados por falta de DSN não contam como aprovados. |
| Criptografia de negócio | Vetores existentes de hashes, PSBT/balanced open e autenticação Fedimint; checar manutenção dos formatos persistidos. |
| HTTP/rotas | Auth/CSRF, parâmetros e barras, APIs, fallback da SPA, terminal/WebSocket e notificações/streams. |
| Interface | `npm ci` e `npm run build` em instalação limpa; navegar na SPA servida pelo Go, verificar console, assets/fontes, QR, i18n, gráficos, ReactFlow, rede e layout responsivo. Validar também proxy `/api` e HMR em desenvolvimento. |
| Instalação/upgrade | Ubuntu 22/24 conforme suportado, amd64 e caminho arm64; manager/broker/mesh, permissões, healthcheck, reboot e reversão em laboratório. |
| Estabilidade | Comparar RAM, CPU, tempo de resposta, erros de DB/gRPC e recuperação de streams com baseline; observar ao menos um ciclo das automações relevantes em laboratório, sem disparar ações financeiras. |

Novos testes devem cobrir comportamento ou risco específico identificado. Não basta construir testes que apenas confiram o número da versão.

### 5. Encerramento e publicação

- [ ] Builds e testes necessários aprovados em Linux; integrações realmente executadas e resultados documentados.
- [ ] Scanners posteriores sem os avisos tratados e sem novas regressões de segurança; qualquer pendência com causa e encaminhamento explícitos.
- [ ] Ensaio de instalação, upgrade e rollback concluído no laboratório antes de propor publicação.
- [ ] Artefatos anteriores identificados e preservados: manager, broker, mesh e UI compatíveis, além da configuração e da toolchain necessárias. Evitar migração de esquema neste trabalho; se surgir necessidade, reavaliar reversibilidade.
- [ ] Rollback se houver falha de login, banco, conexão LND, streams, broker ou interface, ou aumento sustentado de erros/recursos em comparação com a baseline. Validar a reversão completa, não apenas substituir o manager.
- [ ] Após a integração autorizada na branch padrão, aguardar a reanálise do GitHub e conferir o fechamento de cada alerta. Alertas abertos antes do merge não significam que o patch falhou; não os dispensar manualmente para reduzir a contagem.

Critério de conclusão: cada um dos 36 alertas tem destino documentado, as correções são confirmadas no grafo e no GitHub, e há evidência funcional e de rollback. Ausência de impacto é uma conclusão dos testes, não uma premissa do plano.

## Fontes principais

- [Alertas do repositório](https://github.com/jvxis/brln-os-light/security/dependabot).
- [gRPC: correções por linha do alerta #29](https://github.com/grpc/grpc-go/security/advisories/GHSA-2v4p-qf9q-27wj) e [fragmentação HTTP/2](https://github.com/grpc/grpc-go/security/advisories/GHSA-vp52-pcj8-j9qc).
- [Manifesto gRPC 1.83.2](https://proxy.golang.org/google.golang.org/grpc/@v/v1.83.2.mod), [pgx 5.9.2](https://proxy.golang.org/github.com/jackc/pgx/v5/@v/v5.9.2.mod), [x/crypto 0.55.0](https://proxy.golang.org/golang.org/x/crypto/@v/v0.55.0.mod) e [x/net 0.58.0](https://proxy.golang.org/golang.org/x/net/@v/v0.58.0.mod).
- [pgx: GO-2026-4771](https://pkg.go.dev/vuln/GO-2026-4771), [GO-2026-4772](https://pkg.go.dev/vuln/GO-2026-4772) e [SQL/protocolo simples](https://github.com/jackc/pgx/security/advisories/GHSA-j88v-2chj-qfwx).
- [Guia oficial de migração Vite 5→6](https://v6.vite.dev/guide/migration).

## Matriz individual dos alertas

A matriz abaixo registra a triagem inicial de todos os alertas abertos consultados. A coluna de piso corretivo reproduz o campo do Dependabot; a escolha final deve respeitar também os patches por linha de release, especialmente em gRPC.

| Alerta | Pacote | Severidade | GHSA/CVE | Piso corretivo | Condi??o e avalia??o inicial |
| --- | --- | --- | --- | --- | --- |
| [#1](https://github.com/jvxis/brln-os-light/security/dependabot/1) | golang.org/x/crypto | Crítica | [GHSA-v778-237x-gjrc](https://github.com/advisories/GHSA-v778-237x-gjrc) / CVE-2024-45337 | 0.31.0 | Autorização por PublicKeyCallback SSH; pacote SSH ausente no grafo dos comandos analisados. |
| [#2](https://github.com/jvxis/brln-os-light/security/dependabot/2) | golang.org/x/net | Média | [GHSA-qxp5-gwg8-xv66](https://github.com/advisories/GHSA-qxp5-gwg8-xv66) / CVE-2025-22870 | 0.36.0 | Bypass de proxy por zona IPv6; x/net/http/httpproxy externo ausente. Distinguir a cópia na biblioteca padrão. |
| [#3](https://github.com/jvxis/brln-os-light/security/dependabot/3) | golang.org/x/crypto | Alta | [GHSA-hcg3-q754-cr77](https://github.com/advisories/GHSA-hcg3-q754-cr77) / CVE-2025-22869 | 0.35.0 | DoS em troca de chaves/transferência SSH; pacote SSH ausente no grafo analisado. |
| [#4](https://github.com/jvxis/brln-os-light/security/dependabot/4) | golang.org/x/net | Média | [GHSA-vvgc-356p-c3xw](https://github.com/advisories/GHSA-vvgc-356p-c3xw) / CVE-2025-22872 | 0.38.0 | Tokenizer HTML e escopo de tags; x/net/html ausente no grafo analisado. |
| [#5](https://github.com/jvxis/brln-os-light/security/dependabot/5) | github.com/go-chi/chi/v5 | Média | [GHSA-vrw8-fxc6-2r93](https://github.com/advisories/GHSA-vrw8-fxc6-2r93) | 5.2.2 | RedirectSlashes usa Host no redirecionamento; middleware não encontrado no roteador atual. |
| [#6](https://github.com/jvxis/brln-os-light/security/dependabot/6) | golang.org/x/crypto | Média | [GHSA-j5w8-q4qc-rx2x](https://github.com/advisories/GHSA-j5w8-q4qc-rx2x) / CVE-2025-58181 | 0.45.0 | Consumo de memória ao interpretar GSSAPI; pacote SSH ausente no grafo analisado. |
| [#7](https://github.com/jvxis/brln-os-light/security/dependabot/7) | golang.org/x/crypto | Média | [GHSA-f6x5-jh6r-wrfv](https://github.com/advisories/GHSA-f6x5-jh6r-wrfv) / CVE-2025-47914 | 0.45.0 | Pânico em mensagem de agent; ssh/agent ausente no grafo analisado. |
| [#8](https://github.com/jvxis/brln-os-light/security/dependabot/8) | google.golang.org/grpc | Crítica | [GHSA-p77j-4mvh-x3m3](https://github.com/advisories/GHSA-p77j-4mvh-x3m3) / CVE-2026-33186 | 1.79.3 | Bypass por :path sem barra em servidor gRPC; LOS usa cliente. Servidores encontrados apenas em testes. |
| [#9](https://github.com/jvxis/brln-os-light/security/dependabot/9) | github.com/jackc/pgx/v5 | Crítica | [GHSA-9jj7-4m8r-rfcm](https://github.com/advisories/GHSA-9jj7-4m8r-rfcm) / CVE-2026-33816 | 5.9.0 | Memória em pgproto3.Backend.Receive/FunctionCall.Decode (lado servidor); conferir alcance com govulncheck. |
| [#10](https://github.com/jvxis/brln-os-light/security/dependabot/10) | github.com/jackc/pgx/v5 | Baixa | [GHSA-j88v-2chj-qfwx](https://github.com/advisories/GHSA-j88v-2chj-qfwx) / CVE-2026-41889 | 5.9.2 | SQL injection exige protocolo simples + dollar quoting + valor controlável; verificar DSN e consultas efetivas. |
| [#11](https://github.com/jvxis/brln-os-light/security/dependabot/11) | github.com/jackc/pgx/v5 | Crítica | [GHSA-xgrm-4fwx-7qm8](https://github.com/advisories/GHSA-xgrm-4fwx-7qm8) / CVE-2026-33815 | 5.9.0 | Memória em pgproto3.Backend.Receive/Bind.Decode (lado servidor); distinto de #9, mesmo patch. Conferir alcance. |
| [#12](https://github.com/jvxis/brln-os-light/security/dependabot/12) | golang.org/x/net | Média | [GHSA-5cv4-jp36-h3mw](https://github.com/advisories/GHSA-5cv4-jp36-h3mw) / CVE-2026-25680 | 0.55.0 | CPU excessiva ao interpretar HTML; x/net/html ausente no grafo analisado. |
| [#13](https://github.com/jvxis/brln-os-light/security/dependabot/13) | golang.org/x/crypto | Alta | [GHSA-q4h4-gmj2-qvw2](https://github.com/advisories/GHSA-q4h4-gmj2-qvw2) / CVE-2026-46597 | 0.52.0 | Pânico no decoder AES-GCM de SSH; pacote SSH ausente no grafo analisado. |
| [#14](https://github.com/jvxis/brln-os-light/security/dependabot/14) | golang.org/x/crypto | Média | [GHSA-45gg-vh54-h5m9](https://github.com/advisories/GHSA-45gg-vh54-h5m9) / CVE-2026-39828 | 0.52.0 | Permissões perdidas com PartialSuccessError SSH; pacote SSH ausente no grafo analisado. |
| [#15](https://github.com/jvxis/brln-os-light/security/dependabot/15) | golang.org/x/crypto | Média | [GHSA-78mq-xcr3-xm33](https://github.com/advisories/GHSA-78mq-xcr3-xm33) / CVE-2026-39835 | 0.52.0 | CertChecker SSH com callbacks nulos; pacote SSH ausente no grafo analisado. |
| [#16](https://github.com/jvxis/brln-os-light/security/dependabot/16) | golang.org/x/crypto | Média | [GHSA-qpw4-5x99-6vjp](https://github.com/advisories/GHSA-qpw4-5x99-6vjp) / CVE-2026-39827 | 0.52.0 | Memória retida ao rejeitar canais SSH; pacote SSH ausente no grafo analisado. |
| [#17](https://github.com/jvxis/brln-os-light/security/dependabot/17) | golang.org/x/crypto | Crítica | [GHSA-vgwf-h737-ff37](https://github.com/advisories/GHSA-vgwf-h737-ff37) / CVE-2026-39830 | 0.52.0 | Deadlock por respostas SSH inesperadas; pacote SSH ausente no grafo analisado. |
| [#18](https://github.com/jvxis/brln-os-light/security/dependabot/18) | golang.org/x/crypto | Alta | [GHSA-w879-237q-wc7r](https://github.com/advisories/GHSA-w879-237q-wc7r) / CVE-2026-39829 | 0.52.0 | CPU excessiva em parâmetros RSA/DSA no parser SSH; pacote SSH ausente no grafo analisado. |
| [#19](https://github.com/jvxis/brln-os-light/security/dependabot/19) | golang.org/x/crypto | Crítica | [GHSA-89gr-r52h-f8rx](https://github.com/advisories/GHSA-89gr-r52h-f8rx) / CVE-2026-39831 | 0.52.0 | Flag de presença física em chave FIDO/U2F SSH; pacote SSH ausente no grafo analisado. |
| [#20](https://github.com/jvxis/brln-os-light/security/dependabot/20) | golang.org/x/crypto | Crítica | [GHSA-rm3j-f69w-wqmq](https://github.com/advisories/GHSA-rm3j-f69w-wqmq) / CVE-2026-39834 | 0.52.0 | Loop em escrita SSH maior que 4 GB; pacote SSH ausente no grafo analisado. |
| [#21](https://github.com/jvxis/brln-os-light/security/dependabot/21) | golang.org/x/crypto | Crítica | [GHSA-5cgq-3rg8-m6cv](https://github.com/advisories/GHSA-5cgq-3rg8-m6cv) / CVE-2026-42508 | 0.52.0 | Revogação de chave CA SSH não aplicada; pacote SSH ausente no grafo analisado. |
| [#22](https://github.com/jvxis/brln-os-light/security/dependabot/22) | golang.org/x/crypto | Crítica | [GHSA-x527-x647-q7gg](https://github.com/advisories/GHSA-x527-x647-q7gg) / CVE-2026-46595 | 0.52.0 | Permissões/source-address em callbacks SSH; pacote SSH ausente no grafo analisado. Relacionado à correção de #1. |
| [#23](https://github.com/jvxis/brln-os-light/security/dependabot/23) | golang.org/x/crypto | Crítica | [GHSA-jppx-rxg9-jmrx](https://github.com/advisories/GHSA-jppx-rxg9-jmrx) / CVE-2026-39833 | 0.52.0 | ConfirmBeforeUse ignorado pelo keyring; ssh/agent ausente no grafo analisado. |
| [#24](https://github.com/jvxis/brln-os-light/security/dependabot/24) | golang.org/x/crypto | Crítica | [GHSA-f5wc-c3c7-36mc](https://github.com/advisories/GHSA-f5wc-c3c7-36mc) / CVE-2026-39832 | 0.52.0 | Restrições removidas ao encaminhar chave; ssh/agent ausente no grafo analisado. Relacionado a #23, causa distinta. |
| [#25](https://github.com/jvxis/brln-os-light/security/dependabot/25) | golang.org/x/crypto | Média | [GHSA-9m57-25v3-79x9](https://github.com/advisories/GHSA-9m57-25v3-79x9) / CVE-2026-46598 | 0.52.0 | Chave Ed25519 malformada provoca pânico no fluxo SSH; pacote SSH ausente no grafo analisado. |
| [#26](https://github.com/jvxis/brln-os-light/security/dependabot/26) | google.golang.org/grpc | Alta | [GHSA-hrxh-6v49-42gf](https://github.com/advisories/GHSA-hrxh-6v49-42gf) | 1.82.1 | Conjunto de falhas xDS/RBAC e transporte servidor HTTP/2; xDS ausente. Analisar transporte separadamente. |
| [#27](https://github.com/jvxis/brln-os-light/security/dependabot/27) | google.golang.org/grpc | Alta | [GHSA-vp52-pcj8-j9qc](https://github.com/advisories/GHSA-vp52-pcj8-j9qc) / CVE-2026-84304 | 1.83.1 | Exaustão de memória por fragmentação HTTP/2; confirmar alcance no transporte cliente e validar streams/reconexão. |
| [#28](https://github.com/jvxis/brln-os-light/security/dependabot/28) | google.golang.org/grpc | Média | [GHSA-qc2q-p7wx-3px3](https://github.com/advisories/GHSA-qc2q-p7wx-3px3) / CVE-2026-84303 | 1.83.1 | Matching de headers em RBAC xDS; xDS/authz ausentes no grafo dos comandos analisados. |
| [#29](https://github.com/jvxis/brln-os-light/security/dependabot/29) | google.golang.org/grpc | Alta | [GHSA-2v4p-qf9q-27wj](https://github.com/advisories/GHSA-2v4p-qf9q-27wj) / CVE-2026-84445 | 1.82.2 | Pânico em servidor xDS sem authority/Host; xDS ausente. Na linha 1.83, usar 1.83.2, não 1.83.1. |
| [#30](https://github.com/jvxis/brln-os-light/security/dependabot/30) | esbuild | Média | [GHSA-67mh-4wv8-2f99](https://github.com/advisories/GHSA-67mh-4wv8-2f99) | 0.25.0 | CORS no servidor próprio do esbuild; uso como ferramenta de build não prova exposição desse servidor. Corrigir via Vite. |
| [#31](https://github.com/jvxis/brln-os-light/security/dependabot/31) | vite | Média | [GHSA-4w7w-66w2-5vf9](https://github.com/advisories/GHSA-4w7w-66w2-5vf9) / CVE-2026-39365 | 6.4.2 | Leitura de sourcemaps externos pelo dev server Vite exposto; avaliar flags --host e arquivos acessíveis. |
| [#32](https://github.com/jvxis/brln-os-light/security/dependabot/32) | vite | Alta | [GHSA-fx2h-pf6j-xcff](https://github.com/advisories/GHSA-fx2h-pf6j-xcff) / CVE-2026-53571 | 6.4.3 | Bypass de fs.deny em caminhos Windows/NTFS; estação local usa Windows. Avaliar exposição do dev server. |
| [#33](https://github.com/jvxis/brln-os-light/security/dependabot/33) | vite | Média | [GHSA-v6wh-96g9-6wx3](https://github.com/advisories/GHSA-v6wh-96g9-6wx3) / CVE-2026-53632 | 6.4.3 | Divulgação de hash NTLMv2 via UNC/launch-editor em Windows; avaliar dev server local, sem reproduzir contra credenciais reais. |
| [#34](https://github.com/jvxis/brln-os-light/security/dependabot/34) | postcss-selector-parser | Baixa | [GHSA-w9m9-85wc-3x92](https://github.com/advisories/GHSA-w9m9-85wc-3x92) / CVE-2026-9358 | 6.1.3 | Recursão no parser/serializador CSS; dependência transitiva do Tailwind. Validar build e CSS emitido. |
| [#35](https://github.com/jvxis/brln-os-light/security/dependabot/35) | browserslist | Alta | [GHSA-73wf-gq98-2v4g](https://github.com/advisories/GHSA-73wf-gq98-2v4g) / CVE-2026-73088 | 4.28.7 | Crash/escrita de protótipo com estatísticas Browserslist não confiáveis; cadeia Autoprefixer/Babel. Verificar lockfile. |
| [#37](https://github.com/jvxis/brln-os-light/security/dependabot/37) | baseline-browser-mapping | Média | [GHSA-w5vr-8v7q-w6rv](https://github.com/advisories/GHSA-w5vr-8v7q-w6rv) / CVE-2026-45819 | 2.11.0 | Encerramento do processo por parâmetros inválidos no baseline; transitiva de Browserslist. Garantir seleção >=2.11.0. |
