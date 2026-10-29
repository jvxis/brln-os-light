# Retirement and Succession regtest validation — 2026-10-28

Native UI testing found and corrected four problems: cooperative-close error
reconciliation ([#343](https://github.com/jvxis/brln-os-light/issues/343)),
incorrect fee hints ([#344](https://github.com/jvxis/brln-os-light/issues/344)),
a Retirement password loop ([#346](https://github.com/jvxis/brln-os-light/issues/346))
and Succession spending outputs below its configured confirmation threshold
([#347](https://github.com/jvxis/brln-os-light/issues/347)).

## Environment and scope

Ubuntu 24.04.3 amd64, native Bitcoin Core 31.1 and LND 0.21.5-beta, one disposable
VM with 4 CPUs / 4 GiB. Initial source was release `e188e67a`; final functional
code was tested at `0470c195db72c72f57323a1897e4b2cc4bc81e8f`. Subsequent
notice/documentation revisions are identified in the PR validation record.

Alice and Bob remained the owner's laboratory nodes. Dedicated Charlie LND,
LOS Manager and Postgres database isolated Retirement/Succession from their
channels and settings. Temporary Delta LND supplied an offline peer. All
services and Bitcoin peers were local; there was no production privileged
broker or external Telegram credential. No additional VM was started.
Mainnet nodes stayed running and were not accessed.

The actual deployed UI supplied normal login, CSRF, financial snapshots,
password approval and operation submission. Browser contexts were separated
per node. Native LND and Bitcoin Core independently verified the outcomes.
No fixture response replaced the Manager or LND during these UI tests.

## Corrections and regression evidence

| Problem | Corrected behavior and evidence |
| --- | --- |
| RPC error after close broadcast | Before an error or bounded fee retry, the client checks the exact pending/closed channel for up to two seconds. Tests cover immediate and delayed persistence, confirmed close, fee error after broadcast, genuine offline failure, another channel's close and unavailable observation. The original code failed; the corrected native UI returned the exact confirmed closing txid. No evidence means the original error remains an error. |
| Fee explanations | EN/PT distinguish starting negotiation rate from the frozen confirmation ceiling. Native manual UI close used 2 sat/vB with a 6 sat/vB ceiling; Auto used 25 with a 51 ceiling. Both returned the native cooperative closing txid and confirmed. The fee limits were preserved. |
| Retirement confirmation loop | The exported frontend helper now serializes an empty JSON object when its optional payload is omitted, allowing financial-intent preparation. The omitted-payload regression failed before the fix; omitted and explicit payloads both pass afterward, using only the approved normalized body. The actual Retirement UI then advanced after password approval. Backend authorization was not relaxed. |
| Succession minimum confirmations | With policy 6, the original automatic sweep spent inputs aged 3, 19, 165 and 195 confirmations. The corrected runtime waits for mixed mature/immature wallet funds and supplies the explicit minimum to native coin selection. Existing `SendCoins` callers retain their original defaults; RPC tests verify both paths and reject an invalid explicit minimum before dispatch. |

The whole-wallet policy is deliberately conservative: new unconfirmed deposits
can postpone its transfer. The EN/PT form hint and laboratory guide disclose
this behavior. The existing scheduler and transfer retry intervals were not
shortened.

## Actual native workflows

| Scenario | Observed result |
| --- | --- |
| Manual Retirement dry-run | Completed with a live 60,000-sat channel and 2,000,000 on-chain sats unchanged. |
| Succession “Simulate not alive” | Created a source=`succession`, dry-run session and completed without changing two real native test channels or their balances. |
| Live Retirement start | Required financial confirmation/password. Snapshot captured exactly Charlie's two test channels; quiesce reported zero errors; no pending HTLCs. It stopped at explicit cooperative confirmation. |
| Online/offline handling | After the corrected UI confirmation, Bob's channel closed cooperatively. Offline Delta produced an exception and `awaiting_user_decision`; it was not automatically forced. |
| Explicit force-close decision | A separate UI financial approval authorized Delta's exact test channel. Native `LOCAL_FORCE_CLOSE` was mined through the remaining 139 CSV blocks and sweep. Retirement reached `completed`, with final reconciliation and no pending balance. |
| Live Succession arming | UI approval froze the Bob-owned destination, 2 sat/vB and minimum 6 confirmations. A separate ordinary login could not change the live fee policy. Mirror-off arming and a forged API source=`succession` were rejected. |
| Proof of life | “I'm alive (UI)” reset the next check to 30 days and the deadline to 60 days after the confirmation. The active test channel stayed open. |
| Grace period | The real minute scheduler entered `reminder_window` without retiring the node. A new UI proof of life restored future dates and prevented retirement. Actual Telegram delivery was not tested. |
| Expired deadline | The real scheduler created a new live Succession session; the normal Retirement runtime automatically closed the online test channel. No manual close action substituted for this trigger. |
| Corrected confirmation boundary | At three confirmations, with other mature wallet funds present, transfer stayed `waiting_funds`: no txid and zero send attempts. After mining three more blocks, the ordinary retry loop submitted a sweep whose native inputs had exactly 6 and 18 confirmations. |
| Destination and completion | Core verified the sweep to the approved Bob address. After confirmation, transfer became `confirmed`, Retirement `completed`, and Succession `retirement_completed`; the source wallet was empty and no channels or balances remained pending. The confirmed transfer was also displayed in the EN/PT UI audit. |

For the scheduler boundary tests, only timestamps in Charlie's isolated
Postgres database were moved to consistent reminder/deadline boundaries. The
host clock, 30-day check/grace settings, one-minute scheduler and production
code paths were unchanged. This exercises the live scheduler at those
boundaries; it is not a claim of waiting sixty calendar days.

## Validation and retained state

Backend: `go test ./...`, `go vet ./...`, and focused close/maturity regression
tests on Linux amd64. Initial root-only Loop ownership tests also failed on
the unchanged baseline because the disposable lacked fixed OS test identities;
after creating those nologin test identities, baseline and changed tests passed.
No App Store daemon or package was installed for that prerequisite.

Frontend: `node --test tests/retirement-confirmation.test.mjs
tests/rebalance-eligibility.test.mjs tests/css-toolchain.test.mjs` — 14 passed;
`npm run build` passed. The actual candidate was deployed, with installer replay
and authenticated native Lightning/on-chain smoke checks. The empty-body and
mixed-maturity regressions were demonstrated failing before their fixes.

Native read-only checks included `getinfo`, `listchannels`, `pendingchannels`,
`closedchannels`, `walletbalance`, `listunspent`, `getchaninfo --chan_point=...`
and Core `getrawtransaction <txid> true`. Core input ages were recorded at the
observation height, before further mining. The first unsafe-policy result was
retained as baseline evidence; a distinct new session tested the corrected flow.

The owner's original 1,000,000-sat channel and 400,000-sat balanced channel
remained active on Alice and Bob. All additional test channels were resolved;
their identities, passwords, wallet unlock material and TLS were preserved.
Test transfers and fees changed fictitious balances. Charlie's Succession and
mirror settings were disabled, then Charlie/Delta were stopped and left
disabled at boot, with their data retained. No production installer, network
selection or authentication exception was introduced for these tests.

Coverage limits: actual Telegram transport/replies, real calendar waiting,
stuck-HTLC recovery, offline force-close preapproval, peer recovery after choosing
Wait, and exhaustive retry/rollback combinations remain unvalidated. No arm64
or funds-bearing/mainnet fixture was used. Other channel automations remain
separately unvalidated in the feature matrix.

## Resumo em português

O Retirement passou em dry-run e ao vivo, incluindo confirmação de senha,
fechamento cooperativo, peer offline, decisão explícita de force-close,
maturação CSV, recuperação e conciliação final. A confirmação cooperativa
estava presa em um ciclo de senha; a chamada foi corrigida sem reduzir a
exigência de autorização financeira.

Succession passou no fluxo local de prova de vida, carência, disparo automático,
fechamento e transferência para o destino aprovado. O ensaio encontrou uma
falha no mínimo de confirmações: a versão anterior incluía uma entrada com
3 quando a política exigia 6. Após a correção, aguardou em 3 e transferiu apenas
entradas com 6 e 18 confirmações, terminando com origem vazia e sem pendências.

As datas foram antecipadas apenas no banco isolado de testes. O envio e a
resposta reais pelo Telegram não foram validados. A transferência total agora
aguarda a maturação de todos os fundos atuais; depósitos recentes podem adiá-la.
Alice/Bob continuaram com seus dois canais preservados; os auxiliares foram
parados e os dados mantidos. Nenhuma mudança de rede ou exceção de autenticação
foi necessária para o regtest.
