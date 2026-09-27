# AutoFee: seed nativa v2 (amostragem corrigida) — 0.5.38

Entrega 1B da issue #184. A seed nativa (histórico público do Graph Explorer) é a
fonte principal do AutoFee: no Friendspool, 157 de 159 avaliações do último run
usaram `seed:native`; Amboss é só fallback. A linha de resumo somava native e
amboss no mesmo contador (`amboss=`), o que escondia isso.

## Problemas da amostragem antiga (`fetchNativeSeed`)

1. **Auto-referência.** O conjunto "inbound" (o que o mercado cobra para entrar
   no peer) incluía a política que o **próprio nó** anuncia para o peer. Em peer
   com poucos canais, a "referência de mercado" era em grande parte a nossa fee.
2. **Registros, não canais.** Cada reanúncio no dia contava como amostra nova; um
   canal instável pesava várias vezes.
3. **Só quem mudou aparece.** O dia só tinha os canais que emitiram atualização;
   canais estáveis (os mais confiáveis) sumiam.
4. **Disabled na média.** Direção desabilitada contava nos percentis.
5. **Canal fechado** entrava com a informação de hoje.

## O que a v2 faz (`autofee_native_seed_v2.go`)

- Replay dia a dia dentro da janela de lookback, com estado por (canal, anunciante)
  levado adiante; lê 30 dias antes da janela para conhecer políticas anunciadas
  antes e não alteradas desde então.
- Exclui a política anunciada pelo próprio nó (identidade via LND).
- Uma amostra por canal por dia (última captura do dia).
- Exclui direção `disabled` e canal com `closed_at` anterior ao dia.
- Confiança explícita: mínimo de 3 dias, 6 amostras e **2 canais distintos**.
- A matemática depois da série (p65, blend com mediana, penalidade de
  volatilidade, skew, cap p95, max_ppm) é **idêntica** à antiga, para que a única
  diferença medida seja a amostragem.

## Modo comparativo (padrão) e aplicação

- Com `native_seed_enabled` ligado, a v2 é sempre calculada e registrada por canal
  no resultado do run: `seed_v2`, `seed_v2_ok`, `seed_v2_days`, `seed_v2_channels`,
  `seed_v2_self_excluded`, `seed_v2_delta_pct` (v2 vs seed aplicada) e a tag
  `seed:v2≈N(±x%)`. A linha de resumo ganha
  `native=N v2_ok=N v2_insufficient=N v2_applied=N`.
- `native_seed_v2_enabled` (novo, padrão **false**): quando ligado, a v2
  substitui a seed nativa antiga **só quando a amostra corrigida é suficiente**;
  caso contrário o caminho antigo (nativa antiga → Amboss → memória) segue
  intacto. Toggle no Fee Center, abaixo de "Seed nativa do grafo".
- Guard de salto de seed (`SeedGuardMaxJump`) vale igual para a v2.

## Como avaliar durante a semana

No log do AutoFee (`/api/lnops/autofee/results`), por canal:

- `seed_v2_ok=false` em muitos canais = a amostra corrigida é fina; ligar a v2
  faria esses canais caírem para Amboss/memória. Ver `seed_v2_channels`.
- `seed_v2_delta_pct` negativo e `seed_v2_self_excluded > 0` = a seed antiga
  estava inflada pela nossa própria fee.
- Distribuição de `seed_v2_delta_pct`: se a mediana for pequena (±10%) a troca é
  segura; se for grande, olhar os canais no extremo antes de ligar.

Ligar `native_seed_v2_enabled` é reversível: desligar volta à seed antiga no
próximo run, sem tocar nas fees já publicadas.

## Fora do escopo

Percentis, blend, caps, precedência nativa sobre Amboss, base fee por tamanho e
a estatística descritiva do Graph Explorer não mudam.
