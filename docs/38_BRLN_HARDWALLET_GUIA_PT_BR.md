# BR⚡LN Hardwallet: guia do piloto (LightningOS 0.5.49)

Este guia leva você do Signer novo, ainda com o firmware de fábrica, até o
primeiro envio e o teste de recuperação. Depois de instalar o app (parte 1), são
cinco etapas: preparar o Signer, criar a carteira, receber, enviar e recuperar a
carteira em uma segunda placa. Leia uma parte inteira antes de começar a fazê-la.

## Antes de começar

**O que é.** O BR⚡LN Hardwallet é uma carteira Bitcoin on-chain com assinador
físico. O BR⚡LN Signer (uma LilyGO T-Display) guarda as chaves e confere cada
endereço e cada transação na própria tela. O LightningOS consulta a blockchain e
transmite a transação. A seed, a senha e a passphrase só entram pelos botões do
Signer: elas nunca passam pelo computador.

> [!WARNING]
> **Este é um piloto.** O firmware e o app ainda não têm auditoria
> independente. Use somente valores pequenos, que você aceita perder. A carteira
> é separada do LND: ela não usa o saldo nem os canais do seu node.

**Você precisa de:**

- Um node com BR⚡LN LightningOS **0.5.49** ou mais recente.
- Uma **LilyGO T-Display original** (ESP32, tela de 1,14″, dois botões). Para
  o teste de recuperação da parte 6, uma segunda placa igual.
- Um **cabo USB de dados**. Muitos cabos só carregam: se a placa não aparecer no
  computador, troque o cabo primeiro.
- Um **computador** (Windows, macOS ou Linux) com **Chrome ou Edge**. Celular e
  Firefox não funcionam, porque não têm Web Serial.
- Acesso ao LightningOS com **HTTPS confiável** no navegador. Se aparecer "Não
  seguro" na barra de endereço, use **Confiar neste dispositivo** na tela de
  login do LightningOS e reinicie o navegador.
- Papel e caneta para anotar as 24 palavras.

**Os botões do Signer.** Segure a placa com o conector USB para a direita. Ela
tem dois botões: o **de cima** e o **de baixo**. A tela sempre mostra, junto da
borda de cada botão, o que ele faz naquela tela:

| Na tela | O que faz |
|---|---|
| CONFIRMAR | Aceita o que está na tela (botão de baixo) |
| CANCELAR | Cancela a operação inteira (botão de cima) |
| PROXIMO / ESCOLHER | Na digitação e nas escolhas: o de cima passa para a próxima opção e o de baixo escolhe |
| PAGINA / QR | Ao mostrar um endereço: troca a página do texto ou alterna para o QR code |

A tela não usa acentos ("PROXIMO", "Preparar memoria"): é uma limitação da
fonte da placa.

## Parte 1: instalar o app no LightningOS

1. No LightningOS, abra **Apps** e encontre **BR⚡LN Hardwallet**.
2. Em **Servidor Electrum**, escolha de onde vêm as consultas:
   - **Meu Electrs local (padrão)**: mais privado. Requer o Electrs instalado e
     sincronizado no seu node.
   - **Electrs do clube (servidor público)**: funciona sem Electrs local, mas o
     servidor do clube e a rede podem observar quais endereços você consulta.
3. Clique em **Aplicar servidor e iniciar** e aguarde o app ficar em execução.
4. Abra o app pelo menu lateral: **Apps e liquidez → BR⚡LN Hardwallet**.

O app abre na aba **Configurar Signer**, com quatro passos. Os próximos passos
deste guia seguem esses quatro passos.

## Parte 2: preparar o Signer

### Passo 1: instalar o firmware

Uma placa nova vem com o firmware de demonstração da LilyGO. A instalação
substitui esse firmware pelo do BR⚡LN Signer, direto pelo navegador.

1. Ligue o Signer ao computador pelo cabo USB.
2. Feche qualquer outro programa que use a placa: Arduino IDE, monitor serial
   ou outra aba do app.
3. No passo **1. Instale o firmware**, confira o quadro **Hash que o Signer vai
   mostrar**. São 64 dígitos em 4 linhas. Para a 0.5.49, o valor é:

   ```text
   f15ca565b627bed0
   7a3063704a069272
   33d97a13662810fc
   3c8dc52bd1843dad
   ```

   Se o quadro do app mostrar outro valor, pare e avise no grupo do piloto.
4. Marque a caixa de autorização ("Esta é a T-Display original...").
5. Clique em **Selecionar BR⚡LN Signer USB**. O navegador abre uma lista de
   portas: escolha a da placa (geralmente "USB Single Serial", "CH9102",
   "CH340" ou "CP210x") e clique em **Conectar**.
6. O app identifica o chip. Clique em **Confirmar e instalar firmware**.
7. Aguarde a barra chegar ao fim. **Não tire o cabo e não feche a página.** A
   gravação é conferida automaticamente.

Ao terminar, o Signer reinicia sozinho.

**Na primeira inicialização de uma placa nova**, o Signer mostra:

1. **Preparar memoria / APAGAR a carteira?**: aperte **CONFIRMAR** (baixo).
2. **Tem seu backup? / APAGAR tudo?**: aperte **CONFIRMAR** de novo.

Em uma placa nova não há nada a perder. Essas duas confirmações só aparecem
quando a memória de dados ainda não foi preparada.

**Depois, o Signer mostra o hash do firmware** (SHA256): os mesmos 64 dígitos,
em 4 linhas. Compare linha por linha com o quadro do app. Se forem iguais,
aperte **CONFIRMAR** (baixo). Se forem diferentes, não confirme: desligue a
placa e avise no grupo. O Signer só volta a pedir essa confirmação quando
receber outro firmware.

Volte ao app e clique em **Próximo**.

> Se a placa já tem o firmware BR⚡LN instalado, use **Já tenho o firmware**.
> Reinstalar o firmware normalmente mantém a carteira guardada na placa.
>
> **Para começar do zero**, marque também **Apagar tudo** antes de selecionar a
> porta. O app pede uma confirmação, apaga a memória inteira da placa, grava o
> firmware e confere se a área da carteira ficou realmente vazia. A carteira que
> estava na placa só volta com as 24 palavras.

### Passo 2: conectar e parear

1. Clique em **Conectar por USB** e escolha a porta da placa no navegador.
2. O app reinicia a placa e mostra uma janela com um **código de 8 dígitos**
   (por exemplo `5D51 CEBF`). O mesmo código aparece na tela do Signer.
3. Se os códigos forem iguais, clique em **Sim, confirmo**. Se forem diferentes,
   clique em **Cancelar** e conecte de novo.

O pareamento cria um canal cifrado entre o app e o Signer. Cada nova conexão
reinicia a placa e pede um novo código.

## Parte 3: criar a carteira

### Passo 3: abra sua carteira

Escolha **Criar carteira nova** e continue nos botões do Signer:

1. **No Signer / Criar carteira?**: **CONFIRMAR**.
2. **Senha da carteira**: digite a senha caractere por caractere:
   - **PROXIMO** (cima) passa para o próximo caractere. A ordem é `a`–`z`,
     `A`–`Z`, `0`–`9`, símbolos e, no fim, **PRONTO**, **APAGAR** e **CANCELAR**.
   - **ESCOLHER** (baixo) adiciona o caractere selecionado.
   - A senha não aparece na tela: aparece só o tamanho.
   - Ela precisa ter pelo menos **12 caracteres, sem espaços**. Até lá, a opção
     PRONTO aparece como **MINIMO 12**.
   - Ao terminar, avance até **PRONTO** e aperte **ESCOLHER**.
3. **Repita a senha**: digite a mesma senha.
4. **Gerar seed**: escolha **Automatica** (recomendado) com **ESCOLHER**. A
   opção **Com dados** usa 100 lançamentos de um dado real.
5. **Anote 1/24 ... Anote 24/24**: o Signer mostra uma palavra por tela. Anote
   cada uma no papel, na ordem, e aperte **CONFIRMAR** para ver a próxima.
6. **Anotou a seed? / Mantenha offline**: confira o papel e aperte
   **CONFIRMAR**.
7. **Criando carteira / Aguarde**: não desconecte. Ao final, aparece
   **Carteira pronta / Continue no LightningOS**.

A senha protege a carteira guardada no Signer: você vai digitá-la sempre que
desbloquear. As **24 palavras** recuperam a carteira se o Signer quebrar ou se
perder. Guarde-as offline: nunca fotografe nem digite as palavras em um
computador ou celular.

### Passo 4: tudo pronto

O app salva automaticamente a **conta pública** neste navegador e mostra a
conta (por exemplo `d11bc45b`) e o caminho `m/84'/0'/0'`. Com ela, você
consulta saldo e histórico mesmo com o Signer desconectado.

- **Conferir backup no Signer** mostra as 24 palavras de novo, para comparar
  com o papel.
- **Ir para a carteira** abre a aba **Carteira**.

> **Já tem 12 ou 24 palavras?** Use **Recuperar carteira** no passo 3. Depois
> das duas senhas, escolha **12 palavras** ou **24 palavras** e monte cada
> palavra letra por letra. O Signer sugere a palavra quando ela fica única;
> aperte **CONFIRMAR** para aceitar.
>
> **"Carteira existente / Use outro Signer"** significa que este Signer já tem
> uma carteira. Use **Desbloquear carteira**.

## Parte 4: receber

1. Na aba **Carteira**, deixe o Signer **conectado e desbloqueado**. O cartão
   **BR⚡LN Signer**, no topo, mostra o estado e tem os botões **Conectar por
   USB** e **Desbloquear**.
2. Na aba **Receber**, clique em **Conferir endereço para receber**.
3. O Signer mostra o endereço completo. Use **PAGINA** (cima) para ver o texto
   inteiro e **QR** (baixo) para ver o QR code.
4. Compare **todos os caracteres** do endereço no Signer com o endereço do app.
5. Se forem iguais, marque **Conferi todos os caracteres no BR⚡LN Signer e são
   iguais**. Só então o app libera o **QR code** e o botão **Copiar endereço**.
6. Envie para esse endereço um valor pequeno a partir de outra carteira.
7. Clique em **Sincronizar**. O valor aparece em **Saldo** e na aba
   **Histórico** como "Não confirmada", até entrar em um bloco.

Cada clique em **Conferir endereço para receber** gera um endereço novo. Não
reutilize endereços.

## Parte 5: enviar

Só é possível gastar valores **confirmados** (pelo menos 1 bloco).

1. Conecte e desbloqueie o Signer, abra a aba **Enviar** e clique em
   **Sincronizar**.
2. Em **UTXOs**, deixe marcadas as entradas que quer gastar.
3. Preencha **Endereço de destino** (um destinatário por transação).
4. Preencha **Valor em sats**. Para esvaziar as entradas selecionadas, marque
   **Enviar todo o saldo selecionado, sem troco**.
5. Em **Taxa em sat/vB**, clique em **Consultar** para a sugestão do node, ou
   digite a taxa.
6. Clique em **Preparar transação**. O painel da direita mostra a **Proposta
   congelada**:
   - **Destino**: o endereço e o valor que você vai enviar;
   - **Troco**: o que volta para a sua própria carteira (quando existe);
   - **Taxa**: em sats e sat/vB.

   Se a taxa passar de 10% do valor enviado, o app pede uma confirmação extra.
7. Clique em **Conferir e assinar no BR⚡LN Signer** e confira no Signer:
   1. **Destino N**: o endereço completo. Aperte **CONFIRMAR** em cada página.
      Depois vem a tela de valor, com o mesmo título e os sats: **CONFIRMAR**.
   2. **Troco N**: o endereço de troco e o valor dele, da mesma forma.
   3. **Taxa da rede**: os sats de taxa: **CONFIRMAR**.
   4. **Assinar agora? / Destinos e taxa ja conferidos**: **CONFIRMAR**.
   5. **Assinando**, e depois **Entradas assinadas: N**.

   Compare cada endereço e cada valor com o painel do app. **Se qualquer coisa
   for diferente, aperte CANCELAR**: nada é assinado.
8. O app mostra **Assinatura verificada** e o **Txid**. A transmissão é um
   passo separado: clique em **Transmitir pelo Electrum selecionado**.
9. O botão muda para **Transmitida**. O app atualiza o saldo e o histórico
   sozinho, e o formulário fica vazio para o próximo envio.

O Signer só aceita transações desta carteira: até 20 entradas, um destino e um
troco que volta para a sua própria conta. O troco sempre aparece como **Troco**
no Signer. Se uma proposta tiver outra forma, o Signer recusa sem assinar.

Para desistir antes de transmitir, use **Encerrar proposta**. **Exportar
transação assinada** salva a transação em arquivo, para transmitir por outro
meio.

## Parte 6: testar a recuperação

Este teste faz parte do piloto. Ele confirma que as 24 palavras anotadas
recriam a mesma carteira. Você vai precisar do papel com as palavras. **Não
digite as palavras em computador ou celular:** a recuperação acontece só nos
botões do Signer.

Há dois jeitos de fazer o teste:

- **Com uma segunda T-Display** (recomendado): a primeira placa continua
  intacta durante todo o teste.
- **Com uma placa só**: faça o teste **logo depois de criar a carteira, antes
  de receber qualquer valor**. Anote a **Conta pública** mostrada pelo app (por
  exemplo `d11bc45b`). Siga os itens abaixo a partir do item 2 na mesma placa,
  marcando **Apagar tudo** ao reinstalar o firmware. Se a conta recuperada for a
  mesma, a carteira está pronta para receber.

1. Desconecte o primeiro Signer e guarde-o.
2. Ligue a segunda placa e repita o **passo 1** (firmware) e o **passo 2**
   (pareamento) da parte 2: conferir o hash, "Preparar memoria" e o código de
   pareamento.
3. No passo 3, escolha **Recuperar carteira** e continue no Signer:
   1. **No Signer / Recuperar carteira?**: **CONFIRMAR**.
   2. **Senha da carteira** e **Repita a senha**: crie uma senha para esta placa.
      Ela pode ser diferente da senha da primeira placa.
   3. **Recuperar backup**: escolha **24 palavras** com **ESCOLHER**.
   4. **Palavra 1/24 ... Palavra 24/24**: monte cada palavra letra por letra.
      **PROXIMO** passa para a próxima letra possível e **ESCOLHER** adiciona a
      letra. Quando só sobra uma palavra possível, o Signer mostra a palavra
      inteira: confira com o papel e aperte **CONFIRMAR**. Se a palavra
      completa também for o começo de outra, escolha **USAR**.
   5. O Signer mostra as palavras de volta (**Anote 1/24...**), para você
      conferir com o papel, e pergunta **Anotou a seed?**: **CONFIRMAR**.
   6. **Recuperando / Aguarde**, e depois **Carteira pronta**.
4. Confira no app:
   - a **Conta pública** (por exemplo `d11bc45b`) deve ser **a mesma** da
     primeira placa;
   - o app não deve perguntar se quer substituir a conta salva;
   - depois de **Sincronizar**, o saldo e o histórico devem ser os mesmos.
5. Na aba **Receber**, gere um endereço e confira no Signer: ele continua a
   sequência da carteira.

Se a conta pública for **diferente**, alguma palavra está errada ou fora de
ordem. Nesse caso, o app pergunta se quer substituir a conta salva: responda
**Cancelar**. Não envie fundos para essa carteira; confira o papel e avise no
grupo do piloto. Para repetir o teste nessa placa, reinstale o firmware com
**Apagar tudo** marcado e recomece a recuperação.

Ao terminar, você pode usar qualquer uma das duas placas: as duas abrem a mesma
carteira. Cada uma tem a sua própria senha.

## No dia a dia

- **Saldo e histórico** funcionam sem o Signer: basta abrir o app e clicar em
  **Sincronizar**.
- **O Signer só é necessário** para gerar um endereço de recebimento e para
  assinar um envio. Ao terminar, use **Bloquear** e desconecte o cabo.
- **A conexão continua** enquanto você navega por outras páginas do
  LightningOS. Ela cai ao recarregar ou fechar a aba, e cada nova conexão pede
  um novo código de pareamento.
- **Em outro navegador ou computador**, conecte e desbloqueie o Signer: o app
  salva a conta pública automaticamente. Em **Ajustes** você também pode
  exportar e importar a configuração pública. Ela revela seu saldo e seu
  histórico, então guarde-a com privacidade.
- **Carteira com passphrase** (em Ajustes) é um recurso avançado: cada
  passphrase abre outra carteira, e não há aviso de passphrase errada. Não use
  durante o piloto, a menos que você já conheça BIP39.
- **Idioma e tema** seguem os do LightningOS.

## Problemas comuns

| Sintoma | O que fazer |
|---|---|
| O botão de conectar não faz nada ou avisa sobre Web Serial | Use Chrome ou Edge no computador, com HTTPS confiável (sem "Não seguro"). |
| A placa não aparece na lista de portas | Troque o cabo por um cabo de dados. Feche o Arduino IDE e outras abas do app. No Windows, pode ser preciso instalar o driver da ponte USB (CH9102/CH340 ou CP210x). |
| A gravação falhou no meio | Não crie uma carteira. Reconecte a placa e repita o passo 1. Se não conectar, segure o botão de baixo (BOOT), aperte e solte o RESET na lateral, solte o BOOT e tente de novo. |
| A tela ficou preta depois de gravar | Desconecte e reconecte o cabo. |
| Os códigos de pareamento são diferentes | Clique em **Cancelar** e conecte de novo. Se continuar diferente, pare e avise no grupo. |
| "Carteira existente / Use outro Signer" | O Signer já tem uma carteira: use **Desbloquear carteira**. Para trocar de carteira nessa placa, reinstale o firmware com **Apagar tudo** (a carteira atual só volta com as palavras). |
| "Cancelado / invalido" depois da senha | A senha está errada ou tem menos de 12 caracteres. Tente de novo. |
| A sincronização falha | Com o Electrs local, confira se ele está sincronizado. Em **Apps**, você pode trocar para o Electrs do clube. |
| O Signer recusou a transação ("Transacao recusada", "Aceita um destino") | A proposta saiu do formato aceito. Encerre a proposta e prepare outra com um único destino. |

## Ao relatar um problema

Informe o passo deste guia, o que apareceu no app e na tela do Signer (uma foto
da tela ajuda), o navegador e o sistema. **Nunca** envie as 24 palavras, a senha
ou a passphrase: ninguém do clube vai pedir essas informações.
