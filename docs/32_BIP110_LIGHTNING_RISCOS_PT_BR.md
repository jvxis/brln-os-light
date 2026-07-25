# BIP 110 e Lightning: o que está em risco e como proteger um node

> Artigo educativo para os membros do Clube BRLN  
> Atualizado em 24 de julho de 2026

## Resumo em um minuto

O BIP 110 propõe uma mudança temporária nas regras de consenso do Bitcoin para limitar algumas formas de armazenamento de dados arbitrários. Ele foi desenhado como um **soft fork**, isto é, como uma restrição adicional: blocos aceitos por quem aplica o BIP 110 continuam dentro das regras antigas, mas alguns blocos aceitos pelo Bitcoin Core podem ser rejeitados por nodes que aplicam o BIP 110.

Para canais Lightning comuns, o risco principal **não é o formato das transações**. Os scripts usados atualmente por canais padrão cabem nos limites propostos, e UTXOs criados antes da ativação são expressamente isentos pelas regras do BIP.

O risco importante é indireto: uma ativação contestada pode fazer grupos de nodes e mineradores acompanharem históricos diferentes. Um node Lightning herda a visão de blockchain do seu backend Bitcoin. Se esse backend estiver acompanhando uma chain diferente daquela em que a contraparte publicou um fechamento ou um estado antigo do canal, o node pode não reagir dentro do prazo necessário.

Em termos práticos:

- não há motivo técnico, neste momento, para fechar em massa canais Lightning padrão;
- desligar o node durante uma incerteza é pior do que mantê-lo online;
- o operador deve saber qual backend Bitcoin está usando e quais regras ele aplica;
- perto das alturas críticas, convém evitar operações on-chain não essenciais;
- divergência de altura, hash ou chainwork exige investigação, não troca automática de software;
- sinalização baixa hoje reduz a probabilidade de adoção ampla, mas não elimina o risco de uma chain minoritária.

Este artigo não toma posição a favor ou contra o BIP 110. O objetivo é proteger a operação Lightning enquanto a discussão acontece.

---

## 1. O que é o BIP 110?

BIP significa *Bitcoin Improvement Proposal*: um documento técnico que descreve uma possível mudança no Bitcoin. Um BIP não se torna regra apenas porque recebeu um número ou porque seu status documental aparece como `Complete`.

No caso do BIP 110, `Complete` significa que seus autores consideram a especificação completa. **Não significa que a rede Bitcoin aprovou ou ativou a proposta.** Bitcoin não possui uma votação central ou um comitê que aprove mudanças em nome de todos.

O BIP 110, chamado de *Reduced Data Temporary Softfork*, propõe aplicar por aproximadamente um ano restrições como:

- `scriptPubKeys` novos limitados a 34 bytes, com exceção de `OP_RETURN` de até 83 bytes;
- determinados dados empurrados para scripts e itens de witness limitados a 256 bytes;
- restrições temporárias a anexos Taproot, control blocks grandes e alguns recursos ainda não utilizados por canais Lightning implantados hoje;
- isenção para o gasto de UTXOs criados antes da altura de ativação.

O objetivo declarado pelos proponentes é reduzir o uso do Bitcoin para armazenamento de dados arbitrários. Os críticos questionam a eficácia técnica, a forma de ativação, o precedente de restringir usos que hoje são válidos e, principalmente, o risco de divisão da chain.

Fontes primárias:

- [Especificação completa do BIP 110](https://github.com/bitcoin/bips/blob/master/bip-0110.mediawiki)
- [Apresentação e argumentos dos proponentes](https://bip110.org/)
- [Argumentos públicos contrários ao BIP 110](https://nobip110.com/)

## 2. Soft fork não significa “sem risco de split”

Um soft fork cria regras mais restritivas.

Imagine dois conjuntos:

- **Regras atuais:** aceitam um conjunto maior de blocos.
- **Regras do BIP 110:** aceitam somente um subconjunto desses blocos.

Um Bitcoin Core sem o BIP 110 pode aceitar tanto um bloco compatível quanto um bloco incompatível com o BIP 110. Um node que aplica o BIP 110 rejeita o segundo caso.

Isso preserva a compatibilidade em uma direção, razão pela qual a proposta é classificada tecnicamente como soft fork. Entretanto, se uma parte relevante da rede produzir e aceitar blocos que a outra parte rejeita, podem existir dois históricos concorrentes.

Portanto, estas duas frases podem ser verdadeiras ao mesmo tempo:

1. BIP 110 foi especificado como soft fork.
2. Uma ativação contestada pode provocar uma divisão persistente ou temporária da chain.

Chamar todo esse evento simplesmente de “hard fork” não descreve corretamente a relação entre as regras. Dizer que “por ser soft fork não pode haver split” também seria incorreto.

## 3. As alturas que realmente importam

O BIP 110 usa o bit 4 da versão dos blocos e um mecanismo de ativação diferente dos soft forks mais conservadores do passado.

| Marco | Regra |
|---|---|
| Sinalização voluntária | Mineradores podem sinalizar usando o bit 4 |
| Lock-in antecipado | 1.109 de 2.016 blocos, ou 55%, em um período de dificuldade |
| Sinalização obrigatória | Blocos `961.632` a `963.647` |
| Lock-in obrigatório máximo | Altura `963.648` |
| Ativação máxima | Altura `965.664` |
| Duração | 52.416 blocos após a ativação efetiva |

Durante a janela obrigatória, um node que aplica o BIP 110 rejeita blocos que não sinalizam o bit 4. Se o lock-in não ocorrer antecipadamente, as novas restrições entram em vigor um período de 2.016 blocos depois do lock-in obrigatório.

As datas de calendário são estimativas. Blocos não chegam exatamente a cada dez minutos. O que vale para o software são as **alturas**, não uma data marcada no calendário.

No retrato consultado em 24 de julho de 2026, o [monitor público](https://bip110monitor.com/) mostrava:

- tip `959.355`;
- 19 blocos sinalizando em 1.756 já minerados no período;
- taxa de sinalização de aproximadamente 1,08%;
- 2.277 blocos até a altura `961.632`.

Esse número muda a cada bloco. Use sempre o monitor atual e compare-o com o seu próprio Bitcoin Core.

## 4. Por que isso interessa à Lightning?

Um canal Lightning mantém a maior parte da atividade fora da blockchain. Mesmo assim, a segurança final do canal depende do Bitcoin.

Cada participante possui transações que representam estados sucessivos do saldo. Se alguém publicar um estado antigo de maneira indevida, a outra parte possui uma janela de tempo para detectar o evento e reagir na blockchain. Fechamentos forçados, penalidades, expiração de HTLCs, âncoras e fee bumping dependem de:

1. enxergar a chain relevante;
2. enxergá-la a tempo;
3. conseguir publicar uma transação;
4. pagar uma taxa suficiente para confirmá-la.

O LND não decide sozinho qual é a “chain correta”. Ele pergunta isso ao backend Bitcoin configurado. Se o backend observar o histórico errado, o LND também observará o histórico errado.

Uma analogia simples: o LND é o sistema de segurança de uma loja e o Bitcoin Core é a câmera. O alarme pode estar funcionando perfeitamente, mas não reagirá a um incidente que aconteceu em uma câmera diferente.

## 5. O BIP 110 quebra transações Lightning padrão?

As evidências disponíveis indicam que **não**.

A [análise técnica da Amboss](https://amboss.tech/blog/bip110-lightning-network) compara os limites do BIP com as construções usadas por canais padrão:

| Item | Limite do BIP 110 | Uso em canais Lightning padrão |
|---|---:|---:|
| Novo output script | 34 bytes | outputs de canal usam até 34 bytes |
| Dados de script/witness abrangidos | 256 bytes | scripts de canal ficam abaixo do limite |
| Witness versions permitidas | v0, Taproot e P2A | versões usadas pelos canais implantados |
| Annex e recursos Taproot restritos | restritos temporariamente | não usados por canais implantados comuns |

Além disso, a própria especificação isenta inputs que gastam UTXOs criados antes da ativação. Um funding output de canal confirmado antes da ativação não passa a ser magicamente inválido.

Isso não é uma garantia sobre qualquer construção imaginável. Exigem revisão específica:

- canais experimentais ou protocolos fora dos BOLTs usuais;
- scripts Taproot feitos manualmente;
- carteiras com transações pré-assinadas e caminhos de gasto incomuns;
- contratos que dependam de Taproot annex, `OP_SUCCESS`, control blocks profundos ou versões futuras de witness;
- pesquisas como LN-Symmetry, que ainda não representam os canais LND comuns em produção.

Para um node LightningOS com LND padrão e canais padrão, o risco de incompatibilidade direta é considerado baixo. O risco de acompanhar uma chain diferente continua separado e merece atenção.

## 6. Como fundos poderiam ser perdidos?

Um split não entrega automaticamente os sats do canal a um atacante. Para ocorrer perda, eventos adicionais precisam se combinar. O cenário mais preocupante é:

1. duas chains continuam avançando;
2. seu backend Bitcoin acompanha apenas uma delas;
3. uma contraparte publica um estado antigo do canal na outra;
4. seu node e suas watchtowers não observam essa outra chain;
5. o prazo de contestação expira;
6. a contraparte consegue gastar os fundos naquela chain.

Outros problemas possíveis são:

- fechamento confirmado em uma chain e ausente ou reorganizado na outra;
- HTLCs expirando de formas diferentes entre os históricos;
- mempools congestionadas e taxas elevadas durante fechamentos urgentes;
- depósitos aceitos com poucas confirmações sendo reorganizados;
- a mesma transação sendo reproduzida nas duas chains por falta de replay protection;
- exchanges, swaps e serviços pausando operações até escolherem qual ativo ou histórico aceitar.

O valor econômico de uma eventual chain minoritária também é incerto. “Ter moedas nas duas chains” não significa dobrar riqueza. Liquidez, preço, suporte de carteiras e capacidade de movimentar os fundos podem ser muito diferentes.

## 7. Quatro cenários para entender o risco

### Cenário A — A sinalização cresce e há convergência

Mineradores passam a sinalizar, o limiar é atingido e a maior parte do ecossistema converge para os blocos compatíveis.

- Um node que aplica o BIP 110 segue essa chain.
- Um Bitcoin Core sem essas regras também pode segui-la, pois os blocos compatíveis com o BIP continuam válidos pelas regras antigas.
- Canais Lightning padrão continuam funcionando.
- O risco residual fica em operações incomuns, congestionamento e eventuais softwares não preparados.

Este é o cenário de ativação mais limpo.

### Cenário B — Quase ninguém sinaliza

A sinalização continua muito baixa. Quando a janela obrigatória começa, nodes que aplicam o BIP rejeitam a maioria dos blocos produzidos pela rede.

- Bitcoin Core tende a continuar acompanhando a chain com mais trabalho válido pelas regras atuais.
- Um backend que aplica o BIP pode parar ou avançar muito lentamente, aceitando apenas os raros blocos sinalizadores.
- Um LND ligado a esse backend pode parecer online, mas estar observando uma chain atrasada.

Para Lightning, “backend sincronizado” não pode significar apenas que o processo está rodando. É preciso comparar altura, hash e chainwork com fontes independentes.

### Cenário C — Uma chain minoritária persiste

Uma fração relevante do hashrate mantém blocos compatíveis com o BIP, enquanto outra chain possui mais trabalho.

- Existem dois históricos vivos.
- Nodes não enforcing normalmente escolhem a chain com maior trabalho acumulado entre as que consideram válidas.
- Nodes enforcing permanecem na melhor chain que também respeita o BIP 110.
- Serviços podem escolher lados diferentes.

Esse cenário exige cuidado com canais, depósitos, swaps e transações reproduzidas. Não existe um botão automático capaz de saber qual chain terá maior valor econômico no futuro.

### Cenário D — Disputa equilibrada e reorganizações

Duas chains acumulam trabalho relevante, mercados tratam ambas como valiosas e a liderança pode oscilar.

- confirmações tornam-se menos confiáveis;
- reorganizações podem ser mais profundas;
- canais podem fechar de maneira diferente em cada chain;
- taxas e demanda por blockspace podem aumentar;
- decisões precipitadas de fechamento podem piorar a exposição.

É o cenário mais complexo, mas não é o cenário indicado pela sinalização observada no momento desta atualização.

## 8. Bitcoin Core, Knots e LightningOS

O LightningOS Light executa LND e pode usar um Bitcoin Core local ou um backend Bitcoin remoto, conforme a configuração. Na tela do monitor, uma identificação como `/Satoshi:31.0.0/` indica um backend Bitcoin Core.

Bitcoin Core não aplica as regras do BIP 110. Ele valida blocos conforme as regras de consenso que implementa e escolhe, entre as chains válidas para ele, aquela com maior trabalho acumulado.

Uma versão do Bitcoin Knots configurada para aplicar o BIP 110 adiciona as restrições descritas neste artigo. Ela pode rejeitar blocos que o Core aceita.

Isso não torna automaticamente um software “seguro” e o outro “perigoso” em todos os cenários:

- Core possui flexibilidade para acompanhar a chain de maior trabalho que continue válida nas regras atuais.
- Um node BIP 110 garante que não acompanhará blocos que violem as novas regras, mesmo se eles tiverem mais trabalho.
- Se houver split, escolher uma regra de consenso é também escolher quais históricos o backend está autorizado a observar.

Trocar de cliente no meio de uma divergência, reutilizando o mesmo datadir sem um procedimento validado, cria riscos adicionais. Históricos aceitos, blocos marcados como inválidos e chainstate podem precisar de reconsideração ou reindexação. Não faça uma troca emergencial apenas copiando comandos publicados em redes sociais.

## 9. O que o monitor do LightningOS realmente informa

O monitor implementado no LightningOS é **somente informativo**. Ele:

- lê o Bitcoin backend ativo;
- calcula localmente a sinalização do bit 4;
- consulta a API pública do monitor do BIP 110;
- compara tip, período e contagem de blocos sinalizadores;
- informa a fase e a proximidade das alturas programadas;
- não altera Bitcoin Core, LND, canais ou configurações.

Quando aparece `Sources match`, isso significa que o cálculo local e a fonte pública concordam na amostra comparada. Não significa, sozinho, que foi provado matematicamente que não existe split.

A API pública do BIP 110 não fornece o hash do bloco ou chainwork. Por isso:

- igualdade de altura e sinalização é um bom sinal operacional;
- divergência de contagem é um alerta útil;
- confirmação forte de que duas fontes estão na mesma chain exige comparar hashes de bloco;
- detectar qual chain possui maior peso econômico exige informações além de hash e hashrate.

O badge `Janela obrigatória próxima` também não significa que um fork foi detectado. É uma classificação preventiva do LightningOS, exibida quando a sinalização está abaixo do limiar e faltam no máximo dois períodos de dificuldade para a janela obrigatória.

## 10. Plano prudente para operadores do Clube BRLN

### Agora, antes da janela

1. **Confirme o backend Bitcoin.** Saiba se o LND usa Bitcoin Core local ou um RPC remoto e quem controla esse backend.
2. **Mantenha LND e Bitcoin Core saudáveis.** Verifique sincronização, disco, relógio do sistema e conectividade.
3. **Revise os backups.** Confirme seed, Static Channel Backup e procedimento de recuperação. Não armazene seed no mesmo equipamento.
4. **Mantenha UTXOs confirmados para taxas.** Uma reserva on-chain separada ajuda em CPFP, anchor bump e fechamentos urgentes.
5. **Revise canais problemáticos.** Se já existe um canal offline ou que você fecharia de qualquer forma, prefira fechamento cooperativo e antecipado.
6. **Não feche canais bons por pânico.** Fechamentos em massa consomem taxas, destroem posições de liquidez e podem congestionar a rede.
7. **Evite trocar de implementação impulsivamente.** Uma mudança de regras de consenso precisa de decisão consciente e plano de reversão.

### Próximo da altura 961.632

1. **Mantenha o node online e monitorado.** Desligar LND ou bitcoind reduz sua capacidade de defesa.
2. **Evite opens, closes, splices e swaps on-chain não essenciais.**
3. **Evite grandes recebimentos on-chain com poucas confirmações.**
4. **Acompanhe sinalização de pools grandes, não apenas o percentual agregado.**
5. **Compare seu tip e hashes recentes com mais de uma fonte independente.**
6. **Teste alertas e watchtowers.** Uma watchtower ligada à mesma visão errada de chain não resolve a divergência.
7. **Para nodes de roteamento muito conservadores**, reduzir temporariamente novos HTLCs pode limitar obrigações com prazo durante uma instabilidade comprovada. Isso deve ser resposta a sinais concretos, não a rumores.

### Se uma divergência for detectada

1. **Não desligue o node.**
2. **Suspenda novas operações on-chain não essenciais.**
3. **Não force-close canais em massa.**
4. **Compare altura, best block hash, chainwork e ritmo de blocos em fontes independentes.**
5. **Registre evidências:** horários, hashes, peers, logs e versão do backend.
6. **Não troque Core por Knots, ou Knots por Core, no mesmo datadir sem procedimento específico.**
7. **Procure coordenação técnica do Clube BRLN antes de uma ação irreversível.**
8. **Se houver fechamento ou HTLC com prazo ativo, trate-o como incidente urgente**, com análise do canal e das duas chains relevantes.

## 11. O que não fazer

- Não interpretar baixa sinalização como garantia de risco zero.
- Não interpretar um badge amarelo como prova de fork.
- Não assumir que “mais nodes” equivale automaticamente a “mais hashrate” ou “maior valor econômico”.
- Não assumir que uma chain com mais hashrate hoje necessariamente terá maior valor amanhã.
- Não aceitar depósitos grandes com confiança normal durante uma divergência confirmada.
- Não importar seeds ou macaroons em ferramentas sugeridas por desconhecidos.
- Não seguir “suporte técnico” recebido por mensagem privada.
- Não executar comandos de `invalidateblock`, `reconsiderblock`, `reindex` ou troca de binário sem entender o histórico que será afetado.
- Não publicar backups, channel databases, seeds, macaroons ou credenciais RPC para pedir ajuda.

Eventos de consenso atraem golpes. O risco social pode ser maior e mais imediato que o risco técnico.

## 12. Perguntas frequentes

### Posso perder todos os fundos apenas porque uso LND?

Não. Canais padrão não se tornam inválidos automaticamente. Perda exige uma combinação adicional de split, visão errada da chain, evento de canal com prazo e falha de reação. O objetivo da preparação é impedir essa combinação.

### Preciso fechar todos os canais?

Não há justificativa técnica geral para fechar canais padrão em massa. Feche antecipadamente apenas canais que já possuem uma razão operacional para serem encerrados.

### É melhor desligar o node e esperar?

Não. Um canal não “pausa” porque seu node foi desligado. Prazos on-chain continuam correndo. Mantenha Bitcoin Core e LND online.

### Uma watchtower resolve tudo?

Não. Ela ajuda contra publicação de estado antigo, mas precisa observar a chain onde o evento acontece. Uma tower presa à mesma chain minoritária não enxerga a chain concorrente.

### `Sources match` prova que não existe fork?

Não. Prova apenas que as fontes concordaram nos dados comparados. A confirmação de chain exige hashes; a confirmação de relevância econômica exige ainda mais contexto.

### Bitcoin Core adotará automaticamente o BIP 110?

Não há esse código de enforcement no Bitcoin Core. Contudo, por ser um soft fork, se a chain compatível com o BIP 110 tiver mais trabalho, seus blocos também são válidos para o Core, que pode acompanhá-la sem aplicar as restrições por conta própria.

### O BIP 110 já foi aprovado?

Não existe uma aprovação central. A especificação está completa, mas adoção por software, mineradores, usuários e agentes econômicos é uma questão separada.

### E se nada acontecer?

Esse continua sendo um resultado plausível. Mesmo assim, melhorar uptime, backups, reserva de taxas e monitoramento deixa o node mais seguro contra muitos outros incidentes.

## Conclusão

O debate público costuma apresentar duas certezas opostas: “não existe risco algum” ou “todos perderão fundos”. Nenhuma delas descreve bem a situação.

Para Lightning, a pergunta útil não é apenas “você apoia o BIP 110?”. A pergunta é:

> Qual chain o backend do seu LND está observando, e essa é a mesma chain em que seus canais precisam ser defendidos?

Até que haja evidência de divergência, a conduta prudente é simples: manter o node online, usar um backend conhecido, acompanhar dados locais e públicos, preservar capacidade de fee bump, evitar operações on-chain desnecessárias perto das alturas críticas e não tomar decisões irreversíveis com base em redes sociais.

Calma não significa passividade. Significa preparar-se antes, monitorar fatos e agir apenas quando o risco observado justificar a ação.

## Leituras e fontes

- [BIP 110 — especificação oficial](https://github.com/bitcoin/bips/blob/master/bip-0110.mediawiki)
- [BIP110.org — apresentação dos proponentes](https://bip110.org/)
- [BIP 110 Monitor — sinalização pública](https://bip110monitor.com/)
- [Amboss — What Does BIP110 Mean for the Lightning Network?](https://amboss.tech/blog/bip110-lightning-network)
- [Artigo-base no Primal — Lightning and BIP-110: How to not lose Sats while everyone else argues](https://primal.net/e/naddr1qpzkc6t8dp6xu6twvukkzmny943xjupdxycnqttgdamj6ar094hx7apdd3hhxefdwdshguedwa5xjmr994jhvetj09hkuefdv4k8xefdv9exwat9wvpzplgzvey9waaaw05hclph75svs0yzud30unp956lf8uecqzpagertqvzqqqr4gu6cnpu9)
- [NoBIP110 — argumentos e críticas públicas](https://nobip110.com/)

> Aviso: este material é educacional e operacional. Não substitui análise individual do seu node, das suas implementações, dos seus canais ou aconselhamento financeiro.
