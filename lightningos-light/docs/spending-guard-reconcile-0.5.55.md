# Spending Guard — reconciliação de reservas (0.5.55)

O Spending Guard reserva `amount + max_fee` antes de cada pagamento de carteira,
keysend do chat ou Loop Out BRLN e liquida/libera a reserva assim que o LND
responde. Um ticker de 1 minuto (`startSpendingGuardReconciler`) é a rede de
segurança para reservas que ficaram `reserved` após queda ou timeout do manager
entre o despacho e a liquidação.

## Problema corrigido

Até a 0.5.54 uma reserva cujo `payment_hash` o LND **não conhece** (pagamento
rejeitado antes de ser registrado) nunca saía do estado `reserved`:

- o reconciliador tratava `NotFound` como inconclusivo e consultava o LND de
  novo a cada minuto, para sempre (≈1.440 erros `TrackPaymentV2 … payment
  isn't initiated` por dia no journal do LND por reserva presa);
- o caminho síncrono (`finishSpendingReservation`, chat keysend) só liberava se
  o LND dissesse `FAILED` ou se o despacho nem tivesse sido tentado;
- `windowTotals` soma todo `reserved` sem olhar idade, então o valor preso
  reduzia o limite de 24h indefinidamente.

## Comportamento a partir da 0.5.55

1. **Resposta terminal.** Se o LND responde sem erro e sem registro do hash e a
   reserva tem ≥ 10 min (`spendingGuardUnknownPaymentGrace`), ela é liberada
   com o motivo `LND has no record of this payment hash; the payment was never
   initiated`. Um hash só é vinculado no despacho, então um pagamento que o LND
   não iniciou em 10 min não vai iniciar depois. Erro na consulta continua
   mantendo a reserva.
2. **Liberação imediata no caminho síncrono.** Se o envio retornou erro
   definitivo (não ambíguo: sem timeout/transport) e o LND não conhece o hash,
   a reserva é liberada na hora, sem esperar o reconciliador.
3. **Backoff por reserva.** Nova coluna `spending_guard_entries.reconcile_attempts`.
   Cada consulta ao LND incrementa o contador; a próxima só acontece após
   1, 2, 4, 8 … minutos, teto de 1 hora (`spendingGuardReconcileBackoff`). O
   ticker continua em 1 minuto, mas é só o relógio: quem decide se consulta o
   LND é o backoff de cada entrada. Sem reservas pendentes o ciclo é um
   `SELECT` vazio.

## Validação em produção

- Journal do LND: a linha `[/routerrpc.Router/TrackPaymentV2]: … payment
  isn't initiated` deixa de repetir a cada minuto.
- `GET /api/wallet/spending-guard`: `reserved_sat` volta a 0 quando não há
  pagamento em curso.
