# Regtest Lightning validation — 2026-10-28

## Environment and boundaries

Ubuntu 24.04.3 amd64, one disposable VM, 4 CPUs / 4 GiB RAM; native Bitcoin
Core 31.1 and LND 0.21.5-beta. LOS 0.5.50 source release commit
`e188e67a4f07ff27fd38b92eea6c7c0a458da618`, tree
`c17633f5a7f54bc943ba0c3823023a1c97bd4822`. Installed lab artifact came from
`fe8cd2e1b56737ae02ae283343a5fa14d29c58da`, with the same reviewed tree.

Alice and Bob each ran native LND and a separate authenticated LOS Manager.
Bitcoin/LND/Managers were restricted to localhost. The production privileged
broker was disabled. LOS-TEST2 and every mainnet service were untouched.
All amounts below are fictitious regtest sats.

Before mutation, captured native identities, channel points, balances, pending
channels and source receipt. Both nodes were synced, all five lab services were
active, and the initial 1,000,000-sat Alice/Bob channel was active. It was never
closed. A temporary third local LND, Charlie, allowed a real two-peer batch;
duplicate peers are rejected by both UI and backend. No second disposable VM
was started.

## Exercised workflows

Used the actual deployed React UI through verified SSH tunnels, separate browser
contexts for Alice/Bob, normal login, server financial snapshots and password
confirmation dialogs. No fabricated API responses or substituted LND RPCs.
Native RPCs independently checked each result; mining was manual.

| Workflow | Inputs / native evidence | Result |
| --- | --- | --- |
| Simple open | Alice → Bob, 300,000 capacity, 100,000 push, 2 sat/vB. Mined six blocks; active at both ends, Alice 196,530 / Bob 100,000 after commitment reserves/fees. | Passed. |
| Balanced open | Alice proposes 400,000 at 2 sat/vB; Bob accepts; Alice executes. After mining, active 400,000 channel, contribution 200,000 per side; both session UIs eventually reported Channel active. | Passed happy path. |
| Batch open | Alice → Bob 250,000 and Alice → Charlie 260,000 at 2 sat/vB. Same funding txid, distinct output indexes; both channels active after mining. | Passed. |
| One-channel fees | Simple channel: 123 ppm, base 0 msat, timelock 40. Native `getchaninfo --chan_point=...` matched all values; original channel policy stayed 1 ppm / 1,000 msat / 80. | Passed. |
| Cooperative close, manual | Alice/Charlie test channel, 2 sat/vB. UI returned peer-offline/channel-link-not-found; native pending state showed cooperative broadcast, then `COOPERATIVE_CLOSE`, 256,250 sats settled, no locked balance. No user retry or force close was submitted. | Qualified; [#343](https://github.com/jvxis/brln-os-light/issues/343). Root cause unconfirmed. |
| Cooperative close, Auto | Alice/Bob simple test channel. Confirmation froze 25 sat/vB and a 51 sat/vB negotiation ceiling. UI displayed closing txid; native `COOPERATIVE_CLOSE`, 244,999 sats settled. | Close passed; fee hints disagree with confirmation, [#344](https://github.com/jvxis/brln-os-light/issues/344). |
| Force close | Alice/Bob 250,000 batch channel. UI showed commitment txid; mined confirmation and remaining CSV delay, then confirmed sweep. Native `LOCAL_FORCE_CLOSE`; no pending close or limbo balance at completion. | Passed through recovery. Historical closed-channel fields alone were not treated as current pending balance. |
| Manual rebalance | UI custom job into 300,000 test channel, target 90%, amount 50,000, cap 100 ppm (5 sats). Native incoming payment 49,824 sats, fee 2 sats. Source was the existing Alice/Bob channel, which remained active; its local balance changed from 988,530 to 938,704. Target rose from 196,530 to 246,354. | Qualified: actual movement verified; job reported `partial`, remaining 176 sats. Exact full-amount completion was not demonstrated. |
| Manual AutoFee | Temporarily excluded original channel from AutoFee via normal authenticated settings API. UI Dry run proposed balanced test channel 1 → 10 ppm; native policy stayed 1. UI Run now applied 10 ppm, confirmed natively. Original channel stayed 1 ppm and its enabled setting was restored. Global AutoFee/rebalance remained off. | Passed manual execution only. |

Native observations used the installed helper with its regtest guard:
`checked_lab()`, `btc('getblockchaininfo')`, `ln(node, 'getinfo')`,
`ln(node, 'listchannels')`, `ln(node, 'pendingchannels')`,
`ln(node, 'closedchannels')`, and
`ln(node, 'getchaninfo', '--chan_point=' + point)`.
For new LND versions, `chan_id` may be an opaque hex identifier; use the explicit
channel-point option instead of assuming a decimal CLI channel ID. LOS read-only
rebalance queue/history and channel fee APIs were cross-checked against native
balances/policies. No secret material belongs in this report.

## Preserved state and gaps

Finished with the original channel plus the new balanced channel active;
all three closed test channels resolved and no pending channels. Charlie was
stopped, not enabled at boot, with wallet data retained. Both scheduled master
switches remained off. The original channel's fee policy was preserved; its
balance changed through the fictitious rebalance, as recorded above.

Not validated: automatic scheduler execution, auto-heal with a failed peer,
HTLC Manager with genuinely stuck HTLCs, time-based fee learning/economics,
balanced-open cancellation/recovery, other failure/retry paths, all funding
options/address types, public-network gossip, watchtowers or external services.
Successful manual execution is not evidence for those workflows. No arm64 or
mainnet regression fixture was used for these tests.

The documentation/notice update changes no production Go/React code, production
installer or mainnet configuration. Baseline lab unit suite: 12 tests passed
on Linux amd64 before changing the notice. Subsequent validation belongs in the
corresponding PR, including the rendered EN/PT table and preserved lab state.

## Resumo em português

Validados pela interface e conferidos no LND: abertura simples, lote com dois
peers, abertura balanceada, fechamento forçado com recuperação do saldo,
edição de fees e AutoFee manual. O fechamento cooperativo em Auto concluiu;
o manual confirmou na blockchain, mas a tela mostrou erro (#343). As mensagens
de taxas de fechamento divergem do que a confirmação autoriza (#344).

O rebalance moveu 49.824 de 50.000 sats por 2 sats e informou resultado parcial.
O canal original continuou ativo, com política preservada; seu saldo mudou
porque foi a origem desse rebalance fictício. O canal balanceado ficou disponível
para o laboratório. Não há fechamentos pendentes e Charlie foi parado.

As automações agendadas, falhas de peers/HTLCs e recuperação de sessões ainda
precisam de ensaios específicos. Nenhuma alteração de comportamento em mainnet
foi necessária para estes testes.
