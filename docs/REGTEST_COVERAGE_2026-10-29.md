# Regtest test objectives and additional operations — 2026-10-29

Update: the inventory failure recorded below was subsequently corrected and
validated in native regtest and in a separately authorized mainnet test. See
the [UTXO inventory follow-up](REGTEST_UTXO_INVENTORY_FIX_2026-10-29.md). The original
failed assertions below are historical evidence, not the current matrix status.

Rebalance and Succession are **Validated** in the supported-features matrix.
Validation describes whether the test objective passed. It is not a promise
that every rebalance moves its entire requested amount or that an unrelated
external delivery integration was exercised.

The [initial report](REGTEST_LIGHTNING_VALIDATION_2026-10-28.md) retains its exact
rebalance result: 49,824 of 50,000 sats moved, 2 sats cost, 176 sats remaining.
Liquidity movement, accounting and the partial-result status worked. That is a
successful test of the manual rebalance workflow, with a normal partial attempt.

The [Succession report](REGTEST_RETIREMENT_SUCCESSION_VALIDATION_2026-10-28.md)
retains proof-of-life, grace/reset, actual scheduler-triggered retirement,
cooperative close, automatic transfer and terminal confirmation evidence. Its
corrected minimum-confirmation test waited at 3 confirmations, then spent inputs
with ages 6 and 18 under the approved 6-confirmation policy. Test database dates
were advanced rather than waiting 60 real days. Telegram delivery remains a
separate, untested integration requiring a configured token and destination;
it does not downgrade that successful Succession test.

## Additional native checks

Environment: existing disposable Ubuntu 24.04 amd64 VM, native Bitcoin
Core 31.1 and LND 0.21.5-beta. Installed source at baseline: `2aa99255`, whose
application tree equals release merge `e46fbbe3`. Managers use verified local
HTTPS, normal login cookies and CSRF. No production node or real funds used.

The additional checks exercise the authenticated LOS API, independently checked
against native LND. They do not claim full related browser workflow coverage.
They run on both Alice and Bob:

| Objective | Result and independent evidence |
| --- | --- |
| Sign a public message | Passed. The other node's native LND verifies the signature and the expected signer. A changed message cannot retain that signer identity; empty input receives HTTP 400. |
| Organize UTXOs | Passed. Create a temporary group, edit label/tag/color, assign and detach an output, read back every field, delete the group and restore the previous metadata. |
| Native output lease/release | Passed. A successful LOS lock adds exactly one native LND lease. LOS release restores the original lease inventory and output availability. |
| Display the locked output | **Failed, baseline bug [#349](https://github.com/jvxis/brln-os-light/issues/349).** Alice's LOS inventory changes 7→6 outputs, Bob's 15→14: the locked row disappears instead of remaining visible with `locked=true`. Polling for 30 seconds does not recover it. Release makes it reappear. |
| Audit events and filters | Passed. `utxo.lock` and `utxo.unlock` records appear with the exact outpoint target. Action/target filters and response limit work. Unauthenticated Audit/UTXO reads return HTTP 401; negative Audit limit returns HTTP 400. |
| Preserve existing funds/channels | Passed. Operation checks leave native wallet balances unchanged, existing channel points active, original native lease inventory and metadata restored, and zero limbo balance. |

The matrix separates successful UTXO metadata/group operations from the known
inventory failure. A real failed assertion is not relabelled as an expected
partial rebalance result. No production API/authentication, coin selection or
wallet implementation was modified by this documentation/lab change.

## Reproduction and verification

The new opt-in helper is deliberately separate from installation:

```bash
sudo python3 scripts/regtest-lab/verify-operations.py
```

Run from a checkout containing this helper on the configured disposable lab.
It checks the dedicated-host marker and native regtest chain before operations,
uses existing private credentials without printing them and restores temporary
metadata/groups/leases in `finally` blocks. It makes no payments, opens no
channels and enables no automation. Do not run concurrently with another wallet
operation. Mine a block first if native LND is no longer synced after inactivity.

Before the fix, the helper exited **1**, correctly reporting #349 on both nodes after
completing the other checks. This is an existing product failure, not a passing
test or a failure introduced by the matrix change. It keeps asserting the desired
locked-row behavior so the same native check can validate a future correction.

Baseline Linux lab suite: `python3 -m unittest discover -s scripts/regtest-lab -v`,
12/12 passed before editing and 12/12 passed after updating the notice.
The changed surface is lab Python, the generated static notice and documentation;
the production Go/React source tree is unchanged. Functional notice verification
covers both languages, all 23 operation rows and their statuses, mobile scrolling,
keyboard selection and collapsed state against the actual Alice/Bob pages; all
passed. Exact pre-existing Manager/UI artifact hashes were reused for deployment,
with unchanged Bitcoin/LND process IDs. No new production installation, ARM or
mainnet validation is claimed. Existing scheduled channel automations remain
outside the exercised scenarios.

Initial health after overnight inactivity showed both nodes at height 637,
two active channels each, zero limbo, but not yet synced. One manual local block
restored synchronization. The existing `control.py mine` command also runs its
native 1,000-sat Lightning smoke, as does the notice deployment's lab preparer;
those fictitious transfers are separate from the
balance-preserving operation checks above. Auxiliary Charlie/Delta stay stopped;
Unrelated mainnet nodes and services stayed running and untouched.

## Português

Rebalance e Succession passam a **Validados** pelo objetivo comprovado do teste.
Uma tentativa de rebalance parcial é um resultado esperado; Telegram é uma
integração separada que exige token e destino configurados.

Os testes adicionais pela API autenticada validaram assinatura de mensagens,
etiquetas/tags/cores e grupos de UTXOs, bloqueio/desbloqueio no LND e consulta dos
eventos no Audit. Metadados, grupos, leases e saldos foram restaurados/preservados.
Foi encontrada uma falha real de exibição: o UTXO bloqueado desaparece da lista
do LOS (#349). Ela aparece separadamente como **Falha encontrada**, e o teste
mantém a asserção que precisa passar após a correção. Os testes não alteraram
qualquer node ou serviço mainnet.

## Separately authorized mainnet reproduction — 2026-10-29

After the disposable checks, the owner explicitly requested a bounded test on
an existing mainnet test node. That authorization was limited to one temporary
UTXO lease/release; no send, channel operation, upgrade or restart was performed.
This is separate from the regtest checks above and is not a general production
regression fixture or a new installation/upgrade validation.

With native LND 0.21.4-beta, verified HTTPS, normal LOS authentication and strict
SSH host-key checking, the baseline was healthy and synced: three wallet UTXOs,
two active channels, no native leases, and Manager/LND/broker socket active.
The smallest confirmed unlocked output was cross-checked against native LND,
then locked through the normal LOS endpoint with `expiry_sec: 120`.

Native `wallet listleases` gained one lease, while the exact output disappeared
from the LOS UTXO inventory. An explicit LOS unlock immediately restored the
row and the original native lease inventory. Native wallet balances, channel
points/count, active-channel count and Manager/LND process IDs were unchanged.
No amounts, addresses, outpoints, credentials or node connection details are
included in this public report.

**Conclusion:** #349 is confirmed on mainnet as well as regtest, on the tested
LND versions. The failure concerns locked-output visibility; it did not transfer
or lose funds. The test lease was explicitly released, not left to expire.

The developer READMEs now use generic host/resource/isolation instructions.
Owner-specific VM names, IP/UUID, template requirements and the private
VirtualBox startup script are no longer part of the public regtest guide/tools.
