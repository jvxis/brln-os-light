# Leased UTXO inventory correction — 2026-10-29

The locked-output failure in [#349](https://github.com/jvxis/brln-os-light/issues/349)
is corrected. The baseline native reproduction in the
[earlier report](REGTEST_COVERAGE_2026-10-29.md) remains recorded as historical evidence.

The UTXO display now joins native spendable outputs with active wallet leases,
reconstructing only wallet-owned outputs with matching native value/script,
address and confirmations. Native transaction inputs exclude already-spent
outputs, including unconfirmed spends and raw-transaction inputs. Leases are
read again after history collection; a changing or incomplete snapshot returns
an error instead of supplying incomplete data for metadata pruning. Filters
and the response cap run after full enrichment. Lease IDs are not returned;
external leases are distinguished from the deterministic UTXO Manager leases.

Only the inventory endpoint uses the additional display data. The existing
spendable-list, payment/channel previews and Succession callers keep their native
coin-selection behavior. Inventory reads create/release no leases. Wallet
history is requested only when active leased outputs are absent from native
ListUnspent; a read without missing leased outputs does not scan history. That
history read shares the endpoint's existing timeout. Large-wallet performance
was not benchmarked.

## Automated regression

The new `TestUtxoInventory*` HTTP tests use actual TLS gRPC fixtures for Lightning
and WalletKit. They failed on the original handler (missing locked rows and
successful incomplete inventories), then passed after the fix. Coverage includes
own/external lease ownership, exact value/address/script/depth, no ID leakage,
unchanged spendable caller, filters and metadata live-set preservation, expired
and spent outputs, raw spends, missing/unowned/mismatched native data, lease/history
RPC errors, byte-form outpoints, unconfirmed depth and concurrent release.

Windows amd64 focused regression passed. On disposable Ubuntu 24.04 amd64,
Go 1.26.10: `go test ./...` and `go vet ./...` passed. Initial root-only fixture
failures were caused by the harness's restrictive `umask 077`; the same failures
were reproduced on unchanged baseline source. Baseline fixture checks and the
full candidate suite passed with normal fixture `umask 022`. Assertions and
production ownership checks were not weakened. Lab unit suite: 12/12 passed.

Functional Manager source: `506aa1c3b39179005d91ee69dbf6d7488b636d1a`.
Linux amd64 Manager SHA256:
`0b0b4bf9ad5c44b14926e5dc69c6149f86dc0ac363a7c5ef4743da8e544bd045`.
Later guide/notice/evidence edits do not change the application source.

## Native regtest — LND 0.21.5-beta / Bitcoin Core 31.1

Both Alice and Bob passed:

- The existing opt-in `verify-operations.py` now completes successfully, with
  the original locked-row assertion still enabled. Message signing, UTXO groups,
  metadata, native leases/releases, Audit and auth checks also passed.
- A 120-second LOS lease retained the same address, script, amount, depth,
  label/tag/color/group and own-lease marker. Confirmation filters and response
  limits worked. Native spendable reads excluded the output; a selected-input
  send preview rejected it and the open-channel preview excluded its value.
- Synthetic external native leases remained visible with the own-lease marker
  false. Explicit release and a separate real two-second expiry restored the
  original lease state. Original metadata/groups and native balances restored.
- The real browser retained the locked card and lock icon after reload. Native
  pointer selection and the normal UI Unlock button restored the unlocked card
  on both nodes. Failed browser-driver checks were cleaned up before the final
  successful UI run; no test lease was left behind or financial mutation blindly
  retried.
- Normal authenticated Lightning payment (1,000 fictitious sats) and on-chain
  send (10,000 fictitious sats), confirmation and reauthentication checks passed.

Final regtest health: two existing active channels per node, synced, no leases,
pending channels or limbo. Auxiliary fixtures stayed stopped. Bitcoin/LND were
not restarted for deployment. Manager deployment keeps rollback assets, and the
unchanged compiled UI artifact was reused. Financial smoke tests changed only
fictitious balances as expected.

## Separately authorized mainnet acceptance — LND 0.21.4-beta

After all native regtest checks passed, the owner-authorized mainnet test node
received the exact same Manager binary through verified SSH, with an old-binary
backup for rollback. Only Manager restarted. Native LND, broker socket and
container process identities stayed unchanged; configuration, credentials/TLS,
native wallet balances, leases and channel set were preserved. No installer,
broker binary, LND/Bitcoin upgrade or release publication was involved.

Normal CA-verified HTTPS login/CSRF and independent native LND inspection checked
one small confirmed unlocked output. A 120-second lease added exactly one native
lease, and the output **remained visible with locked=true** and the same native
address/script/value/depth. Explicit LOS release restored the unlocked row and
the original empty lease inventory. Native balances, channel set and active
count remained unchanged throughout. The node finished healthy, synced, with
three UTXOs and two active channels. No real payment/channel operation was used
as a regression fixture and no test lease remains.

This mainnet acceptance was specifically requested by the owner; the reusable
test harness remains confined to regtest. Node names, VM identifiers, wallet
outpoints/amounts/addresses and connection details are omitted from this public
report. No ARM, other LND version, external Telegram or large-wallet benchmark
coverage is claimed.

## Português

Corrigida a #349: o UTXO bloqueado continua visível com cadeado e metadados,
sem voltar a ser elegível para gasto. A correção passou nos testes automatizados,
no LND nativo de Alice/Bob e no teste mainnet autorizado pelo dono. O bloqueio
mainnet foi de 120 segundos e foi desfeito explicitamente; saldos e canais foram
preservados. Somente o Manager reiniciou para aplicar o binário já validado no
regtest; Bitcoin e LND continuaram rodando. Os guias públicos de desenvolvedores
usam instruções genéricas, sem depender do ambiente particular do mantenedor.
