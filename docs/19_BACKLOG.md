# Product Backlog

## Status

Audit date: 2026-07-07.

This file used to mix active backlog items with implemented design notes. Treat
the "Active Backlog" list below as the source of truth. Status labels are based
on a repository audit, not only on the age of the design notes. Implemented
sections are kept only as historical design context.

## Active Backlog

Current product backlog, after checking the repository against the docs:

1. **Implemented 2026-07-09** - `AutoTarget` adaptive `target_outbound_pct`.
   Opt-in (`auto_target_enabled`, default OFF) evaluation that runs inside the
   autopilot cycle (`runAutoScan`, not a separate loop) over the round's selected
   candidates: it raises the target of channels selling fast and viably
   (supply-limited by construction) and lowers channels that stopped selling.
   Capacity-aware (absolute `auto_target_max_local_sat` cap) and budget-aware
   (per-cycle UP throttle), max 50%. Decisions persist to
   `rebalance_auto_target_history`; per-channel opt-out via `auto_target_managed`.
   Config + activity UI in Rebalance Center; endpoint
   `GET /api/rebalance/auto-target/history`.
2. **Implemented 2026-07-09** - `Autofee` dynamic liquidity state. Autofee now
   derives per-channel `liquidity_state`, includes it in structured results and
   outcomes, and exposes it as badges in Fee Center and Channel Ranking.
3. **Implemented 2026-07-08** - Channel `parking mode`. The code now persists
   `parked`/`automation_mode`, review metadata, fixed fee metadata, and excludes
   parked channels from Autofee/Rebalance automation.
4. **Partial** - Ranking-driven per-channel automation actions. Ranking
   recommendations are computed, persisted, and rendered. The ranking detail
   panel now has a one-click parking/unparking action; standalone one-click
   remove-from-rebalance/remove-from-Autofee actions remain open.
5. **Partial** - `AutoFee` <-> `Rebalance` intent interlock. Settling-window
   interlocks exist, but the shared intent layer described in the backlog is
   not implemented.
6. **Partial** - `Graph Explorer` optional external refill. Schema/status fields
   for refill exist, including Amboss-token availability, but no refill worker,
   operator config API, or UI control was found.
7. **Open** - `Graph Close Classifier` high-confidence remote `penalty_close`
   inference. The core classifier handles LND/local and bitcoind mutual/force
   classification, but no high-confidence remote penalty-close heuristic was
   found.
8. **Open** - Wallet Flow lineage export as SVG/PNG. Lineage mode exists, but
   no SVG/PNG export control or serialization flow was found.
9. **Partial** - Rebalance source-rotation/pair-cache telemetry polish. Pair
   stats, route-hop display, and cooldown-probe cache bypass exist; watcher
   pre-check extraction, full batched pair-cache lookup for that path, and
   `pair_cache_skip_*` recovery telemetry remain open.
10. **Open** - `Succession` multisig inheritance vault. Existing succession
    covers proof-of-life and external-address retirement only; no vault service,
    dedicated Succession page, descriptor import, or vault endpoints were found.
11. **Open** - `Boltz Client` Lightning-to-on-chain reverse swaps with explicit
    fee preview. No Boltz app handler, swap service, routes, API helpers, or UI
    page were found.
12. **Open (separate repo)** - `BRLN Lightning edge node` (Taproot Assets over
    Lightning / Fase 2). The on-chain `tapd` App Store app (Camada 1) is
    implemented in this repo; distributing/spending the BRLN asset over Lightning
    needs `litd` integrated + an edge node (RFQ, price oracle, redemption to
    sats) and is a separate project. Tracked in section 13.

## Implemented Since Last Audit

- `AutoTarget` adaptive `target_outbound_pct` (autopilot). New
  `internal/server/rebalance_auto_target.go` (`decideAutoTargetAdjustment` pure
  core + `evaluateAutoTarget` hooked into `runAutoScan`), `RebalanceConfig`
  `auto_target_*` fields + columns + clamps, `auto_target_managed` per-channel
  setting, `rebalance_auto_target_history` table with 90d retention, handlers
  `GET /api/rebalance/auto-target/history` and
  `POST /api/rebalance/channel/auto-target`, Rebalance Center config card +
  activity panel + per-channel toggle, EN/PT-BR i18n, and focused tests
  (`rebalance_auto_target_test.go`). **v2 (2026-07-10):** signals rebuilt around
  per-channel sell-through (`loadChannelSellThrough7d`) with thresholds relative
  to a node baseline (`autoTargetNodeBaseline`), replacing the absolute
  success/drain gates that made v1 a one-way demoter in production; adds a
  `max_downs_per_cycle` throttle. Still opt-in.
- `Lightning Tools` custom macaroon generator with audit log. Implemented in
  `internal/lndclient/macaroon.go`, `internal/server/macaroon_handlers.go`,
  `internal/server/routes.go`, `internal/server/auth.go`, `ui/src/api.ts`, and
  `ui/src/pages/LightningOps.tsx`, with focused backend tests and API spec
  entries.
- Channel `parking mode` with a persisted `channel_automation_policies` table,
  `POST /api/lnops/channel/automation`, backend gates for Autofee/Rebalance,
  Channel Ranking action UI, Rebalance Center parked badges/disabled controls,
  API spec updates, and build/test validation.

## 10. Lightning Tools Custom Macaroon Generator

**Current status (2026-07-07): implemented in code; no longer active backlog.**
The MVP flow exists end to end: LND permission listing, root-key collision
check, custom bake, base64 conversion, reauth scope `macaroon_export`, safe
audit event `macaroon.bake`, API helpers, Lightning Tools UI card, copy/download
actions, and focused tests. Phase 2 revoke/list-root-key work remains a possible
future follow-up, but it is not part of the active MVP backlog.

### Source

GitHub issue: `jvxis/brln-os-light#24` - `Criacao de Macaroon`.

The issue asks for a ThunderHub-like macaroon creation tool. The concrete use
case mentioned is generating a base64-encoded macaroon with invoice-related
permissions for external services such as `pay.br-ln.com`.

### Goal

Add a compact tool under `Lightning Ops > Lightning Tools` that lets an
authenticated operator generate custom LND macaroons with selected permissions,
copy/export the result, and audit the action in the existing Audit Log.

The first version should be practical and compact rather than a full ThunderHub
clone.

### Product Decisions

- The feature belongs in `Lightning Ops > Lightning Tools`, not in the Audit
  Log screen. The Audit Log should record the action.
- Generated macaroons are custom LOS-generated artifacts, even when the selected
  permissions resemble LND's standard invoice/read-only/admin macaroons.
- Do not name generated files `invoice.macaroon`, `readonly.macaroon`,
  `admin.macaroon`, or any other standard LND macaroon name.
- The backend controls the export filename:
  `los-custom-macaroon-YYYYMMDDTHHMMSSZ-rk<rootKeyId>.macaroon`.
- The operator should not manually type the filename in the MVP.
- The default `root_key_id` should be generated automatically, preferably from a
  UTC timestamp such as Unix milliseconds, and checked against
  `ListMacaroonIDs` to avoid collisions.
- The UI may offer presets like `Invoice permissions`, but the exported file
  remains a custom macaroon.
- LOS should not persist the generated macaroon to disk.
- The macaroon secret should be shown only in the immediate response/result
  surface so the operator can copy or download it.

### Security Requirements

This feature intentionally exports a newly generated secret. Treat it as a
controlled exception to the general "do not expose macaroons" rule:

- require an authenticated admin session and CSRF protection through the
  existing middleware
- require fresh reauthentication before exporting a macaroon
- add a dedicated reauth scope such as `macaroon_export`
- accept `confirm_password` on the bake request so the UI can use an inline
  confirmation flow
- never log, persist, audit, or return the wallet seed, existing admin macaroon,
  existing standard LND macaroons, RPC credentials, wallet password, or any
  unrelated secret
- never write `macaroon_hex`, `macaroon_base64`, or raw macaroon bytes to the
  audit log or server logs
- return the generated macaroon only in the immediate response to the explicit
  user action
- clear the UI result when the user leaves or presses a clear action

### Backend Plan

Add a focused LND client module:

- `lightningos-light/internal/lndclient/macaroon.go`

Implement:

- `ListMacaroonPermissions(ctx)` using `lnrpc.Lightning/ListPermissions`
- `ListMacaroonIDs(ctx)` using `lnrpc.Lightning/ListMacaroonIDs`
- `BakeCustomMacaroon(ctx, params)` using `lnrpc.Lightning/BakeMacaroon`
- conversion from LND's returned hex macaroon to base64
- validation for empty permissions, duplicate permissions, invalid entity/action
  values, and invalid/generated root key IDs

Suggested internal types:

- `MacaroonPermission`
  - `Entity string`
  - `Action string`
- `BakeCustomMacaroonRequest`
  - `Permissions []MacaroonPermission`
  - `RootKeyID uint64`
  - `AllowExternalPermissions bool`
- `BakeCustomMacaroonResult`
  - `FileName string`
  - `RootKeyID uint64`
  - `Permissions []MacaroonPermission`
  - `MacaroonHex string`
  - `MacaroonBase64 string`

Add server handlers:

- `lightningos-light/internal/server/macaroon_handlers.go`
- `GET /api/lnops/macaroon/options`
- `POST /api/lnops/macaroon/bake`

Register routes in:

- `lightningos-light/internal/server/routes.go`

Update auth scope handling in:

- `lightningos-light/internal/server/auth.go`

Suggested scope:

- `authScopeMacaroonExport = "macaroon_export"`

`POST /api/lnops/macaroon/bake` should:

1. read JSON payload
2. validate preset or explicit permissions
3. require recent `macaroon_export` reauth, or validate `confirm_password`
4. generate/validate root key ID
5. call `BakeMacaroon`
6. compute the LOS filename
7. record a safe audit event
8. return filename, root key ID, permissions, hex, and base64

### API Contract

`GET /api/lnops/macaroon/options`

Returns presets and available permissions. The preset labels are product/UI
labels and must not imply that LOS is producing LND's standard macaroon files.

Example response:

```json
{
  "presets": [
    {
      "id": "invoice_permissions",
      "label": "Invoice permissions",
      "permissions": [
        { "entity": "invoice", "action": "read" },
        { "entity": "invoice", "action": "write" }
      ]
    }
  ],
  "permissions": [
    { "entity": "invoice", "action": "read" },
    { "entity": "invoice", "action": "write" }
  ]
}
```

`POST /api/lnops/macaroon/bake`

Example request:

```json
{
  "preset": "invoice_permissions",
  "permissions": [
    { "entity": "invoice", "action": "read" },
    { "entity": "invoice", "action": "write" }
  ],
  "confirm_password": "..."
}
```

Example response:

```json
{
  "file_name": "los-custom-macaroon-20260624T145446Z-rk1750776886123.macaroon",
  "root_key_id": 1750776886123,
  "macaroon_hex": "...",
  "macaroon_base64": "...",
  "permissions": ["invoice:read", "invoice:write"]
}
```

If a fresh password confirmation is required, return a structured error such as:

```json
{
  "error": "password confirmation required for macaroon export",
  "code": "macaroon_export_reauth_required",
  "requires_password_confirmation": true
}
```

### Audit Log Plan

Use the existing audit system and Audit Log page. The feature should record an
event when a custom macaroon is generated.

Suggested event:

- `action`: `macaroon.bake`
- `target`: generated LOS filename
- `metadata`: safe operational facts only

Example metadata:

```json
{
  "root_key_id": 1750776886123,
  "permission_count": 2,
  "permissions": ["invoice:read", "invoice:write"],
  "allow_external_permissions": false,
  "status": "success"
}
```

Do not include:

- `macaroon_hex`
- `macaroon_base64`
- raw macaroon bytes
- password or password-derived material
- existing macaroon paths or contents

The existing `AuditLog.tsx` already renders metadata JSON and supports filtering
by `action`, so no new Audit Log screen is required for the MVP. A small UI
polish can later add a documented filter hint for `macaroon.bake`.

### Frontend Plan

Update:

- `lightningos-light/ui/src/api.ts`
- `lightningos-light/ui/src/pages/LightningOps.tsx`
- `lightningos-light/ui/src/i18n/pt-BR.json`
- `lightningos-light/ui/src/i18n/en.json`

Add frontend API helpers:

- `getLnMacaroonOptions()`
- `bakeLnMacaroon(payload)`

Add Lightning Ops state for:

- macaroon options
- selected preset
- selected custom permissions
- password confirmation
- busy/status state
- generated result
- copy/download state

Add:

- `MACAROON_TOOL_SECTION_ID`
- a new Lightning Tools shortcut
- a small inline SVG glyph in `renderToolGlyph`
- a compact card in the existing Lightning Tools grid

Suggested card structure:

1. Title: `Custom macaroon`
2. Subtitle: `Generate a custom macaroon with selected LND permissions.`
3. Preset selector:
   - `Invoice permissions`
   - `Read-only permissions`
   - `Custom`
4. Permission checkboxes for custom mode.
5. Password confirmation input.
6. `Generate macaroon` button.
7. Result area:
   - filename
   - root key ID
   - selected permissions
   - copy base64
   - copy hex
   - download `.macaroon`
   - clear result

The card should stay compact and fit the existing two-column Lightning Tools
layout. Avoid a large ThunderHub-style permissions table in the MVP.

### Suggested Presets

Initial presets should be conservative and easy to understand:

- `invoice_permissions`
  - `invoice:read`
  - `invoice:write`
- `read_only`
  - only safe read permissions returned by `ListPermissions`, if available
- `custom`
  - explicit user-selected permissions

Preset labels should describe permissions, not output filenames.

### Tests

Backend tests:

- permission validation rejects empty permissions
- permission validation deduplicates or rejects duplicates consistently
- invalid entities/actions are rejected
- generated filename follows the LOS naming scheme
- generated root key ID is positive and collision-safe
- hex-to-base64 conversion works
- bake handler requires reauth/password confirmation
- audit metadata does not contain macaroon values

Frontend verification:

- `npm run build`
- verify the card renders in Lightning Tools
- verify custom permissions update the payload
- verify copy/download buttons use the generated LOS filename
- verify result can be cleared

Backend verification:

- `go test ./internal/server/...`
- `go test ./...`

### Phase 2

Possible follow-ups after MVP:

- list root key IDs currently known by LND
- revoke generated custom macaroons by root key ID using `DeleteMacaroonID`
- record `macaroon.revoke` audit events
- surface a filtered Audit Log link from the result card
- support external permissions only with an explicit advanced warning
- offer method/RPC-based permission selection using `ListPermissions`

## 11. Succession Multisig Inheritance Vault

**Current status (2026-07-07): open.** Existing succession code covers
proof-of-life scheduling, simulation, live reauth, Telegram guardrails, and
external-address node-retirement transfer. No descriptor-backed vault service,
dedicated Succession page, watch-only wallet import, or vault destination
endpoints were found.

### Source

Product discussion on 2026-06-26.

The current succession mode accepts a single external on-chain destination
address. The proposed evolution is to let the operator configure a verified
watch-only multisig vault, for example a 2-of-3 Jade setup shared by the
operator, spouse, and child, then use that vault as the destination for
succession-driven node retirement.

### Current Baseline

Existing implementation:

- `SuccessionService` stores proof-of-life state, schedule, dry-run mode,
  retirement policy, and `destination_address`.
- When the deadline expires, succession creates a `NodeRetirement` session with
  `source = succession`.
- `NodeRetirementService` closes channels, monitors settlement, and then sweeps
  confirmed LND on-chain wallet funds to `destination_address`.
- The succession UI is embedded at the bottom of `NodeRetirement.tsx`.
- Bitcoin Core RPC discovery already exists through `readBitcoinLocalRPCConfig`
  and JSON-RPC helpers such as `fetchBitcoinRPCParams`.

The right product shape is to keep `NodeRetirement` as the execution engine and
move succession planning into its own screen. The multisig vault should enrich
the destination selection; it should not duplicate the retirement state machine.

### Goal

Add a dedicated `Succession` or `Inheritance Vault` screen that lets an
authenticated operator create or import a 2-of-3 Bitcoin mainnet multisig
watch-only vault, verify it on hardware wallets, and select it as the default
destination for succession-triggered retirement sweeps.

The first production version should be a destination and monitoring feature, not
a spending coordinator. It must never generate, store, log, or return seed
words, private keys, wallet passwords, macaroons, RPC credentials, or any other
spend authority.

### Product Decisions

- Create a dedicated Succession page instead of expanding Onchain Hub or Node
  Retirement.
- Keep Node Retirement focused on execution: quiesce, drain HTLCs, close
  channels, monitor on-chain, reconcile.
- Keep Onchain Hub focused on UTXOs, provenance, and watch-only monitoring.
- Model the multisig as a descriptor-backed watch-only vault.
- MVP policy: native SegWit `wsh(sortedmulti(2,...))`, Bitcoin mainnet only.
- MVP should support import/manual entry of cosigner metadata:
  - label
  - hardware wallet model hint
  - master fingerprint
  - derivation path
  - xpub
- The app may derive receive addresses and import descriptors into Bitcoin Core,
  but it must not hold private keys.
- Spending from the vault through PSBT belongs to a later phase.

### UX Plan

Create a new frontend route and nav entry:

- `ui/src/pages/Succession.tsx`
- nav key: `succession`
- label: `Succession`

Suggested screen sections:

1. Proof of life
   - enable/disable succession
   - dry-run toggle
   - check period and reminder window
   - alive confirmation
   - simulation controls
   - current status, next check, and deadline

2. Inheritance vault
   - create/import 2-of-3 vault
   - cosigner table for three participants
   - descriptor preview with checksum
   - first derived address
   - verification checklist for each Jade/hardware wallet
   - recovery bundle export

3. Retirement destination
   - choose legacy external address or active multisig vault
   - show the next reserved receive address
   - configure sweep min confirmations and fee rate
   - configure force-close preapproval policy

4. Operational status
   - last successful descriptor import
   - Bitcoin Core watch-only wallet status
   - latest vault balance/UTXO summary
   - most recent succession retirement transfer

Move the existing succession controls out of `NodeRetirement.tsx` once the new
page is available. Node Retirement can keep a compact read-only banner linking
to the Succession page when `source = succession` sessions exist.

### Backend Plan

Add a focused vault service rather than overloading `SuccessionService`:

- `internal/server/succession_vault_service.go`
- `internal/server/succession_vault_handlers.go`
- `internal/server/succession_vault_init.go`

Suggested tables:

- `succession_vaults`
  - `id`
  - `vault_id`
  - `name`
  - `policy_type`
  - `threshold`
  - `cosigner_count`
  - `descriptor`
  - `descriptor_checksum`
  - `external_descriptor`
  - `internal_descriptor`
  - `next_receive_index`
  - `watch_only_wallet`
  - `status`
  - `last_import_at`
  - `created_at`
  - `updated_at`

- `succession_vault_cosigners`
  - `id`
  - `vault_id`
  - `label`
  - `hardware_hint`
  - `fingerprint`
  - `derivation_path`
  - `xpub`
  - `sort_order`
  - `verified_at`
  - `created_at`
  - `updated_at`

- `succession_vault_addresses`
  - `id`
  - `vault_id`
  - `branch`
  - `address_index`
  - `address`
  - `status`
  - `reserved_for`
  - `created_at`
  - `used_at`

Extend `succession_config` with destination metadata:

- `destination_type`: `external_address` or `vault`
- `destination_vault_id`

Keep `destination_address` for backward compatibility and as a session-level
resolved address. When a vault is selected, resolve a fresh receive address
before creating the Node Retirement session and place that concrete address in
the retirement config JSON.

### Bitcoin Core RPC Scope

Reuse the existing local Bitcoin Core RPC discovery and helper functions:

- `readBitcoinLocalRPCConfig`
- `fetchBitcoinRPCParams`

Required RPC calls for MVP:

- `getblockchaininfo` to confirm mainnet and sync status
- `getdescriptorinfo` to normalize descriptor and compute checksum
- `deriveaddresses` to derive preview/receive addresses
- `createwallet` for the watch-only wallet if missing
- `importdescriptors` to import ranged external/internal descriptors
- `getwalletinfo` for watch-only wallet status
- `listunspent` or equivalent wallet-scoped calls for balance/UTXO summary

The feature should require a local Bitcoin Core source for descriptor import and
watch-only monitoring. It can still generate/export a recovery bundle without
importing if Bitcoin Core is temporarily unavailable, but it must clearly mark
the vault as not yet watched.

### API Contract

Suggested endpoints:

```text
GET    /api/lnops/succession/vaults
POST   /api/lnops/succession/vaults
GET    /api/lnops/succession/vaults/{id}
POST   /api/lnops/succession/vaults/{id}/verify-cosigner
POST   /api/lnops/succession/vaults/{id}/derive-address
POST   /api/lnops/succession/vaults/{id}/reserve-address
POST   /api/lnops/succession/vaults/{id}/import-watch-only
GET    /api/lnops/succession/vaults/{id}/recovery-bundle
GET    /api/lnops/succession/vaults/{id}/utxos
```

Update existing config endpoint:

```text
GET    /api/lnops/succession/config
POST   /api/lnops/succession/config
```

New config fields:

```json
{
  "destination_type": "vault",
  "destination_vault_id": "sv_..."
}
```

The response may still include `destination_address`, but for vault mode it
should be the next or currently reserved concrete receive address, not the only
source of truth.

### Security Requirements

- Never accept, persist, log, or return seed words or private keys.
- Never log raw descriptors if they are later considered privacy-sensitive in
  production logs; safe audit events should use vault IDs and fingerprints only.
- Treat xpubs/descriptors as privacy-sensitive data: return them only to
  authenticated admin sessions.
- Require CSRF protection for all mutating calls through existing middleware.
- Require fresh reauthentication before:
  - exporting a recovery bundle
  - replacing an active vault
  - switching real succession from external address to vault destination
- Audit safe events:
  - `succession.vault.create`
  - `succession.vault.import_watch_only`
  - `succession.vault.reserve_address`
  - `succession.config.destination_change`
- Do not include xpubs, descriptors, RPC credentials, or wallet passwords in
  audit metadata.

### Integration With Node Retirement

MVP integration should be deliberately narrow:

1. `SuccessionService` loads config when a timeout or simulation triggers.
2. If `destination_type = vault`, it asks the vault service to reserve the next
   receive address.
3. The created `NodeRetirement` session receives the resolved address in
   `config_json.destination_address`, plus optional metadata:
   - `destination_type`
   - `destination_vault_id`
   - `destination_address_index`
4. Existing `processSuccessionTransfer` continues to use LND `SendCoins` to
   sweep to the concrete address.
5. Reconciliation records transfer status as it does today.

Later, extend `node_retirement_transfers` with:

- `destination_type`
- `destination_vault_id`
- `destination_address_index`

Do not block the first version on this migration if the session `config_json`
captures enough metadata for audit.

### Delivery Phases

Phase 1: UI separation

- Add the Succession page and nav item.
- Move proof-of-life and policy controls out of Node Retirement.
- Keep the same existing `/api/lnops/succession/*` config, alive, and simulate
  endpoints.
- Leave a compact Node Retirement link/status panel.

Phase 2: vault model and descriptor validation

- Add vault/cosigner/address tables.
- Validate fingerprints, derivation paths, xpub shape, threshold, and cosigner
  count.
- Build deterministic sortedmulti descriptors.
- Use Bitcoin Core `getdescriptorinfo` and `deriveaddresses` when available.
- Add backend tests for descriptor construction and validation.

Phase 3: watch-only import

- Create or open the watch-only Bitcoin Core wallet.
- Import external and internal descriptors with a conservative range.
- Track import status and first derived addresses.
- Surface clear unavailable states when local Bitcoin Core is missing,
  unreachable, pruned, or still syncing.

Phase 4: succession destination integration

- Add `destination_type` and `destination_vault_id` to succession config.
- Reserve a fresh vault address when creating a succession retirement session.
- Preserve the resolved address in session config.
- Add tests covering external-address compatibility and vault address
  reservation.

Phase 5: Onchain Hub monitoring

- Add a watch-only vault filter/section in Onchain Hub.
- Show vault balance, UTXOs, receive addresses, and transaction history when
  Bitcoin Core watch-only status is available.

Phase 6: PSBT coordinator

- Add PSBT creation, import/export, signature collection, finalization, and
  broadcast for vault spending.
- Keep this outside the MVP because it is a separate high-risk spending flow.

### Acceptance Criteria

MVP is complete when:

- Operators can configure succession from a dedicated Succession page.
- Existing proof-of-life scheduling and dry-run behavior still work.
- Operators can create a 2-of-3 descriptor-backed vault from three cosigners.
- The app derives at least one receive address and shows verification steps.
- The recovery bundle exports descriptor metadata without any private material.
- The vault can be selected as the succession destination.
- A succession-triggered retirement session resolves and records a concrete
  vault receive address before running.
- Existing external-address succession mode remains backward compatible.
- Backend tests cover descriptor validation, config compatibility, and vault
  address reservation.

### Non-Goals For MVP

- Generating seeds or private keys.
- Holding hardware wallet PINs, seeds, or wallet passwords.
- Spending from the vault.
- General-purpose multisig wallet management.
- Support for testnet, signet, Taproot multisig, Miniscript policy editing, or
  arbitrary M-of-N policies.

## 12. Boltz Client Lightning-To-On-Chain Reverse Swaps

**Current status (2026-07-07): open.** No Boltz Client App Store handler,
`boltzd` management, swap service, reverse-swap API routes, frontend API helpers,
or native Swap Out page were found.

### Source

Product discussion on 2026-07-07.

The operator wants LightningOS users to use Boltz services for one focused swap
type only: move sats from the node's Lightning balance to a Bitcoin on-chain
address. ThunderHub and the Boltz Web App were considered, but the preferred
product direction is a native LightningOS flow backed by Boltz Client
(`boltzd`).

Relevant upstream references:

- https://github.com/BoltzExchange/boltz-client
- https://client.docs.boltz.exchange/
- https://client.docs.boltz.exchange/grpc.html
- https://github.com/BoltzExchange/boltz-client/blob/master/pkg/boltzrpc/boltzrpc.proto

### Goal

Add a first-class `Swap Out` flow that lets an authenticated operator convert
Lightning funds from the local LND node into Bitcoin on-chain funds via Boltz
reverse swaps.

The MVP must support only:

- swap direction: `Lightning -> Bitcoin on-chain`
- Boltz Client method: `CreateReverseSwap`
- swap type: `REVERSE`
- pair: `BTC/BTC`
- default confirmation policy: `accept_zero_conf = false`

Do not expose normal swaps, chain swaps, Liquid swaps, autoswap, Boltz Pro, or
ThunderHub as part of this MVP.

### Product Decisions

- Install `boltzd` as an App Store-managed daemon named `Boltz Client`.
- Provide the actual user workflow inside LightningOS, not inside a third-party
  UI.
- The App Store entry may have no public app port. Its primary action should
  deep-link to the native `Swap Out` page once installed and running.
- Backend Go talks to `boltzd`; the browser never calls `boltzd` directly.
- Bind Boltz Client's REST/gRPC listener to localhost or an internal-only
  network surface. Do not expose port `9002` or `9003` on the LAN.
- Pin the Docker image version after verifying a current stable tag.
- Store Boltz Client data under:
  `/var/lib/lightningos/apps-data/boltz-client`.
- Keep the swap mnemonic and Boltz Client database inside the app data
  directory. The UI must warn operators that this data is part of swap recovery
  material and should be backed up.

### Fee Transparency Requirement

The preview and confirmation screens must show fees as separate line items.
This is a core product requirement, not UI polish.

Required confirmation breakdown:

```text
You send via Lightning:        100,000 sats
Boltz service fee:                 500 sats
On-chain miner fee estimate:     1,200 sats
Lightning routing fee limit:       250 sats
Estimated on-chain receive:     98,300 sats
```

Behavior rules:

- Label the Boltz fee explicitly as `Boltz service fee`.
- Label the miner fee separately as an on-chain fee estimate.
- Label routing fee as a maximum or limit because the final route fee can vary.
- Show total estimated fees.
- Show the final estimated receive amount.
- If the quote changes between preview and confirmation, block execution and
  require a new preview.
- Use the upstream `accepted_pair`/quote acceptance mechanism when creating the
  reverse swap so the executed swap cannot silently use materially different
  fees than the user approved.
- Audit the approved fee breakdown without logging invoices, preimages, private
  keys, macaroons, redeem scripts, swap mnemonic, or other secrets.

### Backend Plan

Add a Boltz Client App Store handler:

- `lightningos-light/internal/server/apps_boltz_client.go`

Responsibilities:

- ensure Docker is available
- create app root and data directories
- generate `boltz.toml`
- configure `network = "mainnet"` and `node = "lnd"`
- configure LND host as `host.docker.internal:10009`
- mount/copy only the LND certificate and macaroon material required by
  `boltzd`
- reuse or generalize the existing Docker-to-LND gRPC access logic currently
  used by LNDg
- start/stop/uninstall the `boltzd` container
- report installed/running status to the App Store

Add a focused swap service:

- `lightningos-light/internal/server/boltz_swap_service.go`
- `lightningos-light/internal/server/boltz_swap_handlers.go`
- optional init file if lazy initialization follows existing local patterns

The service should wrap only the required Boltz Client calls:

- `GetInfo`
- `GetSwapQuote`
- `CreateReverseSwap`
- `ListSwaps`
- `GetSwapInfo`
- `GetSwapInfoStream` or polling-based status, depending on implementation
  cost

Initial endpoints:

- `GET /api/boltz/status`
- `POST /api/boltz/reverse/quote`
- `POST /api/boltz/reverse`
- `GET /api/boltz/swaps`
- `GET /api/boltz/swaps/{id}`

Register routes in:

- `lightningos-light/internal/server/routes.go`

Update API helpers in:

- `lightningos-light/ui/src/api.ts`

Update public API documentation in:

- `docs/03_API_SPEC.md`

### Suggested API Contract

`POST /api/boltz/reverse/quote`

Example request:

```json
{
  "amount_sat": 100000,
  "destination_address": "bc1q...",
  "routing_fee_limit_ppm": 2500
}
```

Example response:

```json
{
  "amount_sat": 100000,
  "destination_address": "bc1q...",
  "boltz_service_fee_sat": 500,
  "onchain_miner_fee_sat": 1200,
  "routing_fee_limit_sat": 250,
  "estimated_receive_sat": 98300,
  "total_estimated_fee_sat": 1950,
  "limits": {
    "minimal_sat": 50000,
    "maximal_sat": 10000000
  },
  "quote_id": "opaque-local-quote-id",
  "quote_expires_at": "2026-07-07T15:30:00Z"
}
```

`POST /api/boltz/reverse`

Example request:

```json
{
  "quote_id": "opaque-local-quote-id",
  "amount_sat": 100000,
  "destination_address": "bc1q...",
  "routing_fee_limit_ppm": 2500,
  "confirm_fee_breakdown": true
}
```

Example response:

```json
{
  "swap_id": "boltz-swap-id",
  "status": "pending",
  "amount_sat": 100000,
  "estimated_receive_sat": 98300,
  "boltz_service_fee_sat": 500,
  "onchain_miner_fee_sat": 1200,
  "routing_fee_limit_sat": 250
}
```

### Frontend Plan

Add a native page, for example:

- `lightningos-light/ui/src/pages/BoltzSwap.tsx`

Suggested UI sections:

1. Swap form
   - amount in sats
   - destination address
   - option to generate/use a new LightningOS on-chain wallet address
   - routing fee limit control with a conservative default

2. Fee preview
   - Boltz service fee
   - on-chain miner fee estimate
   - Lightning routing fee limit
   - total estimated fees
   - estimated receive amount

3. Confirmation
   - explicit acknowledgement of the fee breakdown
   - clear note that the on-chain transaction depends on Bitcoin network fees
   - clear note that routing fee is capped and actual fee may be lower

4. Swap status
   - swap id
   - state/status
   - paid/claim txid when available
   - destination address
   - final fee values when known

5. History
   - recent reverse swaps
   - filter by pending/success/failed/refunded

### Security Requirements

- Require authenticated admin session and CSRF protection.
- Require fresh reauthentication before creating a reverse swap, similar in
  spirit to external on-chain sends.
- Do not return or log:
  - LND macaroons
  - Boltz Client admin macaroon/password
  - swap mnemonic
  - preimage
  - private key
  - redeem script
  - raw invoice when avoidable
- API responses should expose only operational data needed by the UI:
  amounts, fee breakdown, status, destination address, txids, and timestamps.
- Audit events should include safe metadata:
  - amount
  - destination address fingerprint or full address only if already standard for
    comparable send flows
  - Boltz service fee
  - on-chain fee estimate
  - routing fee limit
  - swap id after creation
- Never expose Boltz Client REST/gRPC directly to the LAN.
- Treat app data as recovery-sensitive because it contains Boltz Client's swap
  database and mnemonic.

### Tests

Backend tests:

- App registry accepts `boltz-client`.
- Generated compose and `boltz.toml` contain mainnet/LND/reverse-swap-safe
  defaults.
- Quote handler rejects invalid amounts and invalid destination addresses.
- Quote response separates Boltz service fee, on-chain miner fee, routing fee
  limit, total fees, and estimated receive amount.
- Create handler rejects expired or mismatched quote approvals.
- Create handler calls only reverse swap paths.
- Create handler requires fresh reauthentication.
- API responses and audit events do not contain secrets.

Frontend verification:

- fee preview clearly displays each fee line item
- confirmation cannot proceed without a fresh quote
- quote changes force the user back to preview
- status/history views do not expose secrets
- `npm run build`

Backend verification:

- `go test ./internal/server/...`
- `go test ./...`

### Acceptance Criteria

- A user can install and start Boltz Client from the App Store.
- A user can open the native `Swap Out` page from LightningOS.
- A user can preview a Lightning-to-on-chain reverse swap before execution.
- The preview explicitly shows the Boltz service fee as its own line item.
- The user can execute only after confirming the fee breakdown.
- The created swap is tracked in LightningOS status/history.
- No normal swap, Liquid swap, chain swap, or autoswap control is exposed in the
  MVP UI.

### Later Follow-Ups

- Streaming status updates instead of polling.
- Advanced zero-conf option for small swaps, disabled by default.
- Fee policy presets for routing fee limit.
- Backup/export guidance for Boltz Client swap mnemonic and database.
- Operator-configurable Boltz API endpoint for advanced users.
- Optional support for other swap types after separate product approval.

## 13. BRLN Lightning Edge Node (Taproot Assets over Lightning)

**Current status (2026-07-03): open — separate repository.** Camada 1 (the
standalone on-chain `tapd` App Store app) is implemented and in active on-chain
testing in this repo (`apps_tapd.go`, `apps_tapd_handlers.go`, the Taproot
Assets page). This section tracks Fase 2 (Camada 2): moving the BRLN asset over
Lightning. It will be built in a **separate repository**, not brln-os-light.

### Source

Product discussion 2026-07-01 to 2026-07-03 (Taproot Assets investigation +
on-chain testing with community users).

### Why it is separate

A Taproot Asset can only ride Lightning inside **asset channels**, which require
the LND `aux` components injected in-process — i.e. **`litd` integrated mode**
(`lnd`+`tapd`+`litd` one binary). That conflicts with the LOS native `lnd`
(systemd) and with the single-HTLC-interceptor limit (already blocks running the
standalone `tapd` app alongside the Fedimint Gateway). So the edge node is a
**dedicated node**, not the LOS-managed one.

### Goal

Let BRLN circulate and be spent over Lightning: instant, near-zero-fee, and
convertible to/from sats so any user with a plain `lnd` can redeem value — the
"Redeem to sats" hook already present (returns 501) in the tapd app UI.

### Architecture

- **Dedicated `litd` integrated node** (`github.com/lightninglabs/lightning-terminal`;
  current release bundles `lnd v0.21.1-beta` + `tapd v0.8.0` — matches this
  ecosystem, so BRLN interop is guaranteed). `lnd.conf`:
  `protocol.simple-taproot-chans=true`, `simple-taproot-overlay-chans=true`,
  `rpcmiddleware.enable=true`; `lnd-mode=integrated` + `taproot-assets-mode=integrated`.
  Can reuse Postgres (litd has native SQL backend now).
- **Edge topology:** the edge opens **asset channels** (BRLN) with litd peers and
  **normal BTC channels** with the wider LN — including a plain BTC channel to
  the LOS community node, which stays plain `lnd` and acts as the sat-routing
  backbone. **No asset channel to the community node** (plain lnd can't hold
  one).
- **RFQ + price oracle** convert BRLN <-> sat at the edge; needs a hedging policy
  for the shifting BRLN/sat balance.

### Two consumption flavors

- **Hold BRLN over LN (niche):** the user also runs `litd` + an asset channel.
  Not mass-market (litd + channel per user; one on-chain funding tx each).
- **Redeem value as sats (mass):** a plain-`lnd` user receives **sats** (the edge
  converts BRLN->sat via RFQ). Reaches everyone; they get sats, not the token.

### Economic model / backing (decide BEFORE building the edge)

The protocol gives **no backing and no price** — BRLN's value = the issuer's
redemption commitment + reserves. "How many sats" = the oracle rate the issuer
**honors**; the lastro is the **sat reserve** held to buy BRLN back (in practice
the edge's sat liquidity). Three models:

1. **Gamified points** — no redemption, no backing/price (no edge needed).
2. **Redeemable points** — fixed rate + matched sat reserve. Golden rule:
   BRLN issued <= sat reserve / rate. Recommended for loyalty.
3. **Stablecoin** — pegged to BRL/USD, reserves = supply (heavy, Tether-like;
   avoid).

### On-chain cost context (why Lightning matters)

Every on-chain asset transfer anchors ~1000 sats of Bitcoin dust per output +
miner fee (Taproot Assets design; dust follows the asset, recoverable). Fine for
airdrops/proof; costly at scale — Lightning removes per-transfer dust/fee.

### Non-goals here

Building this in brln-os-light. The LOS side only needs the Fase 2 hook: the
`POST /api/apps/tapd/redeem` endpoint + the "Redeem to sats" button call the
edge's redemption service once it exists.

## Implemented Or No Longer Active Here

These items are implemented enough that they should not drive new work from this
file:

- `Lightning Tools` custom macaroon generator with audit log.
- `Autofee` signal hierarchy and stability redesign.
- Rebalance budget redesign with manual reserve.
- `Graph Explorer` storage limits and existing-node cleanup.
- `Channel Ranking`.
- `Close Recovery Manager`.
- `Graph Explorer` base module.
- `Autofee` native graph seed.
- Core `Graph Close Classifier` path, except for the penalty-close gap noted in
  its dedicated plan.

## 1. Autofee Signal Hierarchy And Stability Redesign

**Current status (2026-06-20): implemented in code.** Keep this section as
historical design context. The active Autofee backlog starts at `Autofee Dynamic
Liquidity State`.

### Goal

Make `Autofee` more efficient, less upward-biased, and significantly more stable by:

- using the full signal set already available in the product
- distinguishing channel role from liquidity urgency
- distinguishing real rebalance execution from theoretical rebalance need
- protecting assisted-revenue channels from unnecessary fee increases
- reducing fee churn to avoid unnecessary graph updates

The intended outcome is:

- fewer cases where fees keep drifting upward without strong evidence
- better handling of `sink` channels with weak economics
- better preservation of low-fee channels that are useful for assisted routing
- lower risk of frequent fee changes that could harm graph reputation

### Problem

Today the system already sees a rich set of signals:

- `out_ppm_7d`
- `rebal_ppm_7d`
- `out_ppm_30d`
- `rebal_ppm_30d`
- `forward_in/out` counts and volumes
- assisted revenue
- `Channel Ranking` state, score, trend, profitability
- rebalance attempts and outcomes
- budget exhaustion and ROI guardrails
- HTLC policy and liquidity failures
- Amboss/native seeds
- node and channel liquidity normalization via `outnorm`

But the runtime hierarchy is still too permissive toward upward protection, especially for:

- mature empty `sink` channels with poor economics
- channels with little or no real recent rebalance execution
- channels that are valuable mainly for assisted revenue at very low outbound fees
- channels where seed or stale protection remains stronger than local evidence

This creates the current operator perception:

- Autofee often feels like it "always wants to go up"
- channels can stay far above `out_ppm` or `rebal_ppm`
- some useful low-fee channels need to be removed from Autofee manually

### Proposed Direction

Redesign the decision flow into four explicit layers:

1. `anchor`
   - derive the economic reference price
2. `channel role`
   - define what the channel is good for
3. `execution reality`
   - distinguish channels that can actually be rebalanced from those only theoretically needing rebalance
4. `stability guards`
   - suppress unnecessary fee churn

The key design change is:

- local economics and channel role should decide first
- scarcity and seed should only amplify later

### Stage 1. Assisted Routing Role

#### Goal

Prevent useful low-outbound channels from being pushed upward just because they are not marked as `super-source`.

#### New Runtime States

Add internal role tags:

- `assist-channel`
- `assist-preserve`

#### Candidate Criteria

Initial candidate conditions should combine:

- meaningful `forward_in_count_7d` and/or `forward_in_amount_7d`
- meaningful `assisted_forward_fee_7d_sat`
- low or modest `forward_out`
- low or moderate `rebalance_dependence_score`
- `profit_fee_7d_sat` not strongly negative

Optional profile-sensitive thresholds can be applied later, but first iteration should use conservative hard thresholds.

#### Behavior

For `assist-channel`:

- outbound fee should not rise easily
- `surge`, `sink-floor`, and `peg` should be weakened
- cooldown for upward changes should be stricter
- hold current fee unless there is a hard upward signal

#### Hard Upward Signals Allowed

Only permit easier increases when at least one is true:

- `htlc-liquidity-hot`
- real `rebal-recent`
- strong `surge-confirmed`
- strong and recent outbound growth beyond assisted role

#### Expected Gains

- better preservation of channels intentionally kept at low or zero outbound
- less need to remove assisted channels from Autofee manually
- better support for routing strategies based on inbound attractiveness

### Stage 2. Ranking-Aware Gates

#### Goal

Use `Channel Ranking` as a primary decision gate, not only as a late correction.

#### Proposed Policy Mapping

- `expand`
  - upward freedom can remain relatively normal
- `maintain`
  - keep target close to anchor
- `monitor`
  - upward moves require stronger evidence
- `close`
  - default behavior becomes decompression or conservative hold, not protection

#### Additional Gates

Reduce or disable upward pressure when:

- `state = close`
- `state = monitor` and `trend = worsening`
- `profit_fee_7d_sat <= 0`
- `profit_fee_30d_sat <= 0`
- `score` and `score_30d` are weak

Allow stronger upward freedom only if a hard signal is present.

#### Expected Gains

- fewer channels defended by Autofee when ranking already says they are weak
- cleaner alignment between `Channel Ranking` and `Autofee`
- less manual conflict between modules

### Stage 3. Rebalance Reality Gates

#### Goal

Stop defending high fees for channels that are not actually receiving viable rebalance execution.

#### New Runtime Signals

Add explicit runtime tags and gates such as:

- `rebal-budget-exhausted`
- `rebal-roi-blocked`
- `rebal-no-attempt-recent`
- `rebal-disabled-channel`

These should be derived from:

- rebalance overview budget state
- skipped candidates by reason
- recent per-channel job and attempt history
- channel-level `auto_enabled`
- channel-level `manual_restart_enabled`

#### Behavior

For mature empty `sink` channels with local history and no real execution:

- do not raise fees just because they remain empty
- reduce force of `rebal-sink`, `peg`, `surge`, and `no-down-neg-margin`
- anchor downward toward a safe band around:
  - `max(out_ppm_7d, rebal_ref_soft)`

This stage should extend the existing `empty-sink-*` logic instead of replacing it.

#### Expected Gains

- better decisions when rebalance is blocked by budget or ROI
- less divergence between fee policy and actual capital deployment
- fewer channels stuck high simply because they look scarce on paper

### Stage 4. Stability And Anti-Churn Guards

#### Goal

Reduce unnecessary fee churn and graph-level noise.

#### Proposed Guards

Add runtime controls such as:

- larger deadband before publishing a change
- minimum ppm delta required to publish
- per-channel maximum changes per 24h
- maximum consecutive upward changes
- strict upward cooldown by default
- only explicit hard signals may bypass upward cooldown

#### Explicit Rule

Simple forward activity should not bypass `cooldown_up`.

Forwards should be treated as:

- evidence that the current fee is acceptable
- not as automatic license to raise again

#### Expected Gains

- fewer gossip updates
- more stable channel pricing
- reduced chance of fee thrash harming graph reputation

### Economic Anchor Model

The new hierarchy should treat the economic anchor as:

- `max(out_ppm_7d, rebal_ref)`

Where:

- `rebal_ref` comes from true recent rebalance cost when available
- `30d` references are used only for channels protected by `slow-cycle-30d`
- Amboss/native seed remains fallback, not dominant anchor, when mature local evidence exists

### Suggested Runtime Order

Refactor the runtime so `evaluateChannel` behaves conceptually like this:

1. compute local market anchor
2. derive channel role
3. derive ranking-aware policy
4. derive rebalance execution policy
5. apply upward/downward pressure only after the above
6. apply anti-churn stability guards
7. compute inbound discount from the final outbound result

### Suggested Technical Shape

The implementation should introduce or refactor toward helpers such as:

- `deriveChannelRole(...)`
- `deriveAssistChannel(...)`
- `deriveRankingPolicy(...)`
- `deriveRebalanceExecutionPolicy(...)`
- `deriveEconomicAnchor(...)`
- `applyAutofeeStabilityGuards(...)`

Relevant existing files:

- `lightningos-light/internal/server/autofee_service.go`
- `lightningos-light/internal/server/autofee_service_test.go`
- `lightningos-light/internal/server/channel_ranking_service.go`
- `lightningos-light/internal/server/rebalance_service.go`
- `lightningos-light/internal/server/rebalance_handlers.go`

### Observability

Add explicit runtime tags so behavior remains auditable in Results and Telegram.

New tags should include at least:

- `assist-channel`
- `assist-preserve`
- `rank-expand`
- `rank-maintain`
- `rank-monitor`
- `rank-close`
- `rebal-budget-exhausted`
- `rebal-roi-blocked`
- `rebal-no-attempt-recent`
- `rebal-disabled-channel`
- `stability-hold`
- `stability-delta-min`
- `stability-max-changes`

### Testing Plan

Add tests for at least:

1. assisted-revenue channel with low outbound fee does not rise without hard signal
2. `close` channel with weak economics cannot receive normal upward `surge`
3. mature empty `sink` with no attempts and blocked rebalance is anchored down
4. `expand` channel with true liquidity pressure can still rise
5. seed cannot dominate mature local signals
6. upward cooldown is strict unless explicit bypass signal exists
7. anti-churn guard suppresses small and repeated changes

### Rollout Plan

Implement in this order:

1. `assist-channel`
2. ranking-aware gates
3. rebalance reality gates
4. anti-churn guards

This order is important because:

- assisted routing protection fixes a real operator pain point immediately
- ranking-aware gates improve decision quality with data already available
- rebalance-reality gates reduce false scarcity
- anti-churn should come last, after the main decision hierarchy is improved

### Non-Goals For First Iteration

Do not attempt all of the following in the same rollout:

- complete UI rework of Autofee controls
- fully automatic channel closure
- replacing `market_refill`
- removing all existing protection logic at once

The first goal is not a full rewrite. It is a hierarchy correction.

### Execution Breakdown

The implementation should be split into small, reviewable increments.

#### Milestone A. Assisted Channel Detection

##### Scope

Add explicit runtime classification for channels that are economically useful mainly because of assisted routing.

##### Backend Tasks

1. Add helper:
   - `deriveAssistChannel(...)`
2. Use these signals in the helper:
   - `forward_in_count_7d`
   - `forward_in_amount_sat_7d`
   - `forward_out_count_7d`
   - `forward_out_amount_sat_7d`
   - `assisted_forward_fee_7d_sat`
   - `rebalance_dependence_score`
   - `profit_fee_7d_sat`
3. Add runtime tags:
   - `assist-channel`
   - `assist-preserve`
4. Reduce upward pressure for these channels in `balanced` mode unless hard signals exist.

##### Likely Files

- `lightningos-light/internal/server/autofee_service.go`
- `lightningos-light/internal/server/autofee_service_test.go`

##### Acceptance Criteria

- low-outbound assisted channels no longer rise by default
- channels with weak assisted contribution are unaffected
- results output clearly shows the new tags

#### Milestone B. Ranking-Aware Gates

##### Scope

Make `Channel Ranking` state and trend explicitly influence upward and downward permissions.

##### Backend Tasks

1. Add helper:
   - `deriveRankingPolicy(...)`
2. Use:
   - `state`
   - `score`
   - `score_30d`
   - `trend_direction`
   - `profit_fee_7d_sat`
   - `profit_fee_30d_sat`
3. Add runtime tags:
   - `rank-expand`
   - `rank-maintain`
   - `rank-monitor`
   - `rank-close`
4. Block or weaken `surge` and protection logic for:
   - `close`
   - `monitor + worsening`
   - negative `7d` and `30d`

##### Likely Files

- `lightningos-light/internal/server/autofee_service.go`
- `lightningos-light/internal/server/autofee_service_test.go`

##### Acceptance Criteria

- `close` channels do not receive normal upward freedom
- `expand` channels still behave close to current baseline
- ranking policy is visible in tags and tests

#### Milestone C. Rebalance Reality Gates

##### Scope

Differentiate channels that need rebalance in theory from channels that are actually getting viable rebalance execution.

##### Backend Tasks

1. Add helper:
   - `deriveRebalanceExecutionPolicy(...)`
2. Feed it with:
   - recent `rebal-recent`
   - recent `rebal-attempt`
   - channel `auto_enabled`
   - channel `manual_restart_enabled`
   - rebalance budget status
   - skipped reasons such as `roi_guardrail` and `budget_too_low`
3. Add tags:
   - `rebal-budget-exhausted`
   - `rebal-roi-blocked`
   - `rebal-no-attempt-recent`
   - `rebal-disabled-channel`
4. Extend the existing `empty-sink-*` family so mature empty sinks can converge downward when rebalance is not happening in reality.

##### Likely Files

- `lightningos-light/internal/server/autofee_service.go`
- `lightningos-light/internal/server/rebalance_service.go`
- `lightningos-light/internal/server/rebalance_handlers.go`
- `lightningos-light/internal/server/autofee_service_test.go`

##### Acceptance Criteria

- empty sinks with no real execution no longer stay artificially high
- channels still receiving real rebalance pressure preserve current behavior
- budget exhaustion becomes visible in Autofee results

#### Milestone D. Stability And Anti-Churn

##### Scope

Reduce unnecessary fee updates and protect graph stability.

##### Backend Tasks

1. Add helper:
   - `applyAutofeeStabilityGuards(...)`
2. Add controls for:
   - minimum ppm delta to publish
   - per-channel max changes per 24h
   - max consecutive upward changes
   - strict upward cooldown
3. Keep bypass narrow:
   - `htlc-liquidity-hot`
   - real `rebal-recent`
4. Add tags:
   - `stability-hold`
   - `stability-delta-min`
   - `stability-max-changes`

##### Likely Files

- `lightningos-light/internal/server/autofee_service.go`
- `lightningos-light/internal/server/autofee_service_test.go`

##### Acceptance Criteria

- forward activity alone no longer causes repeated fee increases
- small oscillations do not generate fee changes
- change frequency per channel is measurably reduced

### Suggested Delivery Order

Use this delivery order, with tests after every milestone:

1. Milestone A
2. Milestone B
3. Milestone C
4. Milestone D

This keeps early changes targeted and lowers regression risk.

### Suggested Review Gates

After each milestone, validate on a test or production-like node using:

- latest Autofee rounds
- ranking snapshots
- rebalance overview
- live reports

Look specifically for:

- fewer unjustified upward moves
- fewer channels far above `out_ppm` and `rebal_ppm`
- preservation of strong assisted-routing channels
- lower fee churn over repeated rounds

### Rollout Safety Notes

Each milestone should be:

- behind conservative gating in code
- covered by dedicated tests
- observable through explicit tags in Results and Telegram

Do not combine multiple milestone behaviors into a single opaque patch.

## Active Detailed Proposals

The sections below include active, partially active, and recently implemented
details. Older implemented sections above are retained only for historical
context.

## 1. Autofee Dynamic Liquidity State

**Current status (2026-07-09): implemented.** `class_label`, node
`liquidity_class`, dynamic low-out thresholds, ranking-aware gates, and
rebalance-execution gates exist in Autofee. Autofee now derives explicit
per-channel `liquidity_state = offer-ready/low/drained/extreme-drained`, stores
it in structured Autofee payloads and outcomes, and surfaces the badge in Fee
Center and Channel Ranking.

### Goal

Improve Autofee decisions by separating:

- channel behavior role: `sink`, `router`, `source`
- liquidity urgency state: `offer-ready`, `low`, `drained`, `extreme-drained`

The goal is to make Autofee react more coherently to:

- current node local liquidity
- node size
- selected Autofee profile
- per-channel local liquidity ratio

### Problem

Today Autofee mixes two different ideas:

- historical channel behavior
- current liquidity urgency

Current logic already uses:

- node-level `liquidity_class`
- channel-level `out_ratio`
- `sink/router/source`
- `low-out`
- `extreme-drain`

But these concerns are not cleanly separated.

This can cause:

- protection becoming too early for some channels
- weak distinction between low liquidity and true drain
- less coherent behavior when the node still has acceptable local liquidity

### Current Baseline

Relevant current behavior:

- node `liquidity_class`
  - `drained` if `local_ratio < 25%`
  - `balanced` if `25% <= local_ratio <= 75%`
  - `full` if `local_ratio > 75%`
- channel `sink`
  - requires `out_ratio < 15%` plus behavior bias
- `low-out`
  - effective threshold currently near `9.5%` for the observed node/profile
- `extreme-drain`
  - active in `moderate` around `4%`
- `extreme-drain-turbo`
  - active around `1%`

### Proposed Direction

Keep:

- `class_label = sink/router/source`

Add:

- `liquidity_state = offer-ready/low/drained/extreme-drained`

Example combinations:

- `sink + offer-ready`
- `sink + drained`
- `router + low`

### Suggested Profile Bases

Example starting point:

- `conservative`
  - `offer_ready_min = 22%`
  - `low_out_max = 10%`
  - `drained_out_max = 6%`
  - `extreme_drained_out_max = 1.5%`
- `moderate`
  - `offer_ready_min = 20%`
  - `low_out_max = 9%`
  - `drained_out_max = 5%`
  - `extreme_drained_out_max = 1%`
- `aggressive`
  - `offer_ready_min = 18%`
  - `low_out_max = 8%`
  - `drained_out_max = 4%`
  - `extreme_drained_out_max = 1%`

These values are backlog proposals, not approved defaults.

### Dynamic Adjustments

Thresholds should also depend on:

- node size: `small`, `medium`, `large`, `xl`
- node `local_ratio`

Example rule:

- if node `local_ratio < 15%`
  - protect earlier
- if node `15% to 25%`
  - mildly protect earlier
- if node `25% to 40%`
  - neutral
- if node `> 40%`
  - allow more aggressive liquidity offering

### Expected Gains

Expected benefits:

- better decisions for channels in the `5% to 20%` range
- clearer distinction between normal low liquidity and real emergency drain
- more coherent offering behavior when node local liquidity is still healthy

Expected limits:

- does not solve `peg`, `floor-lock`, `stepcap`, or dead peers by itself
- should be treated as a quality-of-decision improvement, not a complete reset

### Suggested Implementation Steps

1. Add effective liquidity threshold computation helper.
2. Add `liquidity_state` to Autofee runtime and result payloads.
3. Keep `class_label` unchanged.
4. Route `low-out` and `extreme-drain` logic through the new thresholds.
5. Expose effective thresholds in the calibration section of results/API.
6. Add tests by profile, node size, and node liquidity ratio.

## 2. Channel Parking Mode

**Current status (2026-07-08): implemented in code; no longer active backlog.**
`Channel Ranking` can place a channel in `parked` mode through the detail panel.
The backend persists the policy, captures prior Autofee/Rebalance/source states,
and disables Autofee, rebalance auto, manual restart, and source usage while
parked.

### Goal

Create a controlled path for channels that are:

- stagnant for a long time
- poor in net profitability
- highly dependent on rebalances
- poor candidates for normal Autofee optimization

Instead of immediately closing them, allow a temporary parked state.

### Proposed Behavior

When a channel is parked:

- use fixed fee per channel
- remove channel from Autofee
- remove channel from rebalance
- disable inbound discount automation for that channel
- keep review metadata and review date

### Why

This avoids wasting Autofee cycles on channels that:

- stay `same-ppm`
- do not react to normal fee changes
- absorb operator time without clear return

It also creates a safer middle state before cooperative close.

### Expected Gains

- cleaner Autofee signal on active channels
- less noise from dead peers
- better operator workflow before closing channels

### Suggested Implementation Steps

1. Add channel automation mode:
   - `normal`
   - `parked`
   - `close_candidate`
2. Store fixed-fee override per parked channel.
3. Exclude parked channels from Autofee and rebalance.
4. Show parked state in UI and ranking.
5. Add review date and operator note fields.

## 3. Ranking-Driven Per-Channel Automation Policy

**Current status (2026-07-08): partially implemented.** Recommendations are
computed, persisted, and shown in `Channel Ranking`. One-click parking/unparking
is implemented in the ranking detail panel. Standalone one-click actions for
removing from rebalance or removing from Autofee outside parking remain open.

### Goal

Use `Channel Ranking` output to drive actionable automation hints or semi-automatic policies.

This should not blindly execute closures, but it should help move from analysis to action.

### Proposed Direction

Use ranking states to suggest or trigger:

- `maintain`
  - stay in normal Autofee and rebalance
- `monitor`
  - keep normal automation, but highlight weak economics
- `close`
  - suggest `parking mode` or manual review
- `expand`
  - preserve or boost active strategy

### First Safe Scope

Initial scope should be recommendation-oriented:

- suggest parking candidates
- suggest fixed fee ranges
- suggest rebalance exclusion
- suggest close review

Avoid fully automatic closure in first iteration.

### Expected Gains

- faster operator decisions
- stronger connection between ranking and action
- less manual cross-checking between ranking and Autofee behavior

### Suggested Implementation Steps

1. Add recommendation layer on top of ranking state.
2. Surface recommended action in UI.
3. Add optional one-click actions:
   - park
   - remove from rebalance
   - remove from Autofee
4. Keep irreversible actions manual.

## 5. Rebalance Budget Redesign With Manual Reserve

**Current status (2026-06-20): implemented in code.** Rebalance config, backend
budget accounting, manual reserve fields, and UI controls exist. Keep this
section as historical design context.

### Goal

Replace the current automatic rebalance budget model, which is based only on a percentage of the last 24 hours of routing revenue, with a model that:

- controls total daily rebalance spend at node level
- explicitly accounts for both `auto` and `manual` rebalance cost
- optionally protects a separate reserve for manual restart operations
- becomes more stable and less pro-cyclical than a pure `24h revenue pct` rule

### Problem

Today the automatic scanner computes:

- `daily_budget_sat = forward_revenue_24h * daily_budget_pct`

and the scan gate is effectively checked against:

- `remaining = budget - spent_auto`

This creates three issues:

- total node spend can drift higher than intended when manual restarts consume a lot of fee budget
- a strong or weak single day can distort the next day too much
- the operator cannot explicitly protect room for manual interventions

### Proposed Model

The budget should become a total daily node budget:

- `daily_budget_total_sat`

The runtime should also support an optional manual reserve:

- `manual_reserve_enabled`
- `manual_reserve_mode`
  - `fixed_sat`
  - `pct`
- `manual_reserve_value`

The automatic scanner should then consume:

- `remaining_for_auto = daily_budget_total_sat - spent_total - manual_reserve_remaining`

Where:

- `spent_total = spent_auto + spent_manual`
- `manual_reserve_remaining` is the still-protected portion of the reserve
- if manual reserve is disabled, this value is `0`

### Budget Formula

The current `24h` formula should not remain the only anchor.

The recommended budget source should be hybrid:

- `avg_revenue_7d` as the main anchor
- `revenue_24h` as a short-term adjustment

Suggested first formula:

- `base_budget_sat = avg_revenue_7d * daily_budget_pct`
- `short_term_budget_sat = revenue_24h * daily_budget_pct`
- `daily_budget_total_sat = round(0.70 * base_budget_sat + 0.30 * short_term_budget_sat)`

Optional later additions:

- absolute minimum floor
- absolute maximum cap
- small carry-over from prior day

### Behavior Rules

#### Automatic Scan

The automatic scan should:

- use `spent_total`, not only `spent_auto`
- respect the protected manual reserve
- stop queueing jobs when only the reserved manual portion remains

#### Manual Restart

Manual restart should:

- still consume total daily spend
- be allowed to use the reserved portion
- optionally warn the user when the action would exceed the total daily budget

First iteration should prefer:

- warnings and visibility
- not hard-blocking manual actions unless explicitly configured later

### Data Model Changes

#### Backend Config

Extend `RebalanceConfig` with fields such as:

- `budget_mode`
  - `revenue_24h_pct`
  - `hybrid_revenue`
- `manual_reserve_enabled`
- `manual_reserve_mode`
- `manual_reserve_value`
- optional:
  - `daily_budget_min_sat`
  - `daily_budget_max_sat`

#### Budget Tracking

The existing `rebalance_budget_daily` table already tracks:

- `budget_sat`
- `spent_auto_sat`
- `spent_manual_sat`
- `spent_sat`

This should be extended conceptually with computed outputs in API/overview:

- `budget_total_sat`
- `manual_reserved_sat`
- `manual_reserved_remaining_sat`
- `remaining_for_auto_sat`
- `remaining_total_sat`

First iteration may keep storage unchanged and compute these values at runtime.

### Backend Scope

Files likely involved:

- `lightningos-light/internal/server/rebalance_service.go`
- `lightningos-light/internal/server/rebalance_handlers.go`

Implementation tasks:

1. Add new config fields and schema migration.
2. Refactor budget calculation away from pure `24h` revenue.
3. Make `auto` scan consume against total spend.
4. Add manual reserve computation.
5. Surface reserve and remaining budget clearly in `RebalanceOverview`.
6. Ensure manual job creation records spend consistently in the same total budget model.

### UI Scope

Primary screen:

- `Rebalance Center`

Files likely involved:

- `lightningos-light/ui/src/pages/RebalanceCenter.tsx`
- `lightningos-light/ui/src/i18n/pt-BR.json`
- `lightningos-light/ui/src/i18n/en.json`

#### Config UI Changes

Add settings for:

- budget mode
- daily budget percent
- manual reserve enabled
- manual reserve type
- manual reserve value
- optional min/max total budget

These should live close to the existing rebalance automation and budget settings.

#### Overview UI Changes

Expose clearly:

- total daily budget
- spent auto
- spent manual
- total spent
- reserved for manual
- remaining for auto
- remaining total

The overview should make it visually obvious when:

- `auto` is paused because only manual reserve is left
- manual actions are already consuming most of the day budget

#### Manual Restart UX

When the user triggers manual restart:

- show current total budget state
- show whether reserve is being consumed
- show a warning if the run would push total spend above budget

First iteration should prefer:

- clear warning text
- not complex modal logic

### Acceptance Criteria

The implementation is successful when:

1. automatic rebalance no longer ignores large manual spend
2. the operator can reserve daily budget for manual restart
3. budget behavior is less volatile than pure `24h revenue pct`
4. the overview explains budget state clearly without needing logs
5. existing manual flows continue working without hidden hard blocks

### Rollout Plan

1. backend config and hybrid total-budget calculation
2. update scanner to consume against total spend and reserve
3. API/overview exposure
4. Rebalance Center settings and overview UI
5. manual restart warning UX
6. observe behavior for several days before adding any hard manual block

## 6. Graph Explorer Storage Limits And Existing-Node Cleanup

**Current status (2026-06-20): implemented in code.** Storage config, status,
projections, cleanup, and UI controls exist. Keep this section as historical
design context.

### Goal

Make `Graph Explorer` usable on small nodes without letting public graph policy
history consume unbounded Postgres storage.

The graph's current state should remain available, while the expensive
historical data becomes explicitly configurable and cleanable by the operator.

### Current Baseline

Observed on 2026-06-12:

- production node:
  - `Graph Explorer` total: about `11.93 GB`
  - `graph_channel_policy_history`: about `10.44 GB`
  - coverage: about `72.2 days`
  - projected 90-day graph storage: about `14.5 GB`
- test node:
  - `Graph Explorer` total: about `4.30 GB`
  - `graph_channel_policy_history`: about `4.09 GB`
  - coverage: about `32.9 days`
  - projected 90-day graph storage: about `11.4 GB`

The main growth driver is `graph_channel_policy_history`. The stable current
graph tables are much smaller and should not be pruned as part of history
retention:

- `graph_nodes`
- `graph_channels`
- `graph_channel_policy_current`
- `graph_close_events`

There is already a `graph_explorer_config.history_retention_days` column, but
the runtime currently uses the hard-coded
`graphExplorerPolicyHistoryRetentionDays = 90` constant instead.

### Proposed Model

Add real storage policy controls for `graph_channel_policy_history`:

- `history_retention_days`
- `history_max_bytes`
- optional `history_storage_mode`
  - `days`
  - `size`
  - `days_and_size`

Recommended product defaults:

- existing nodes:
  - keep current effective retention (`90d`)
  - no size cap by default
  - do not delete historical rows automatically just because the feature ships
- new installs:
  - conservative default such as `30d / 5GB`

Suggested UI presets:

- `Lite`: `7d / 2GB`
- `Standard`: `30d / 5GB`
- `Full`: `90d / 15GB`
- `Custom`

### Cleanup For Existing Nodes

Existing nodes need an explicit path to reduce storage after the operator opts
in.

Add a `Graph Explorer Storage` action that:

1. Shows current size, current coverage, estimated GB/day, and projected 30/60/90-day storage.
2. Lets the user choose retention days and/or max GB.
3. Shows the estimated remaining coverage before applying.
4. Updates `graph_explorer_config`.
5. Prunes `graph_channel_policy_history` in batches.
6. Runs `VACUUM (ANALYZE)` on `graph_channel_policy_history`.
7. Reports rows removed and effective coverage after cleanup.

Important behavior:

- `cleanup/prune` deletes rows.
- `VACUUM (ANALYZE)` only marks freed pages reusable by Postgres and refreshes
  planner stats.
- It should not be presented as guaranteed physical disk shrink.
- Physical compaction (`VACUUM FULL`, `pg_repack`, or table rebuild) is an
  advanced later feature because it can require locks, extra temporary disk, or
  operational downtime.

### API Scope

Add or extend an endpoint such as:

- `GET /api/lnops/graph-explorer/storage`
- `POST /api/lnops/graph-explorer/storage`
- `POST /api/lnops/graph-explorer/storage/cleanup`

The storage response should include:

- current config:
  - `history_retention_days`
  - `history_max_bytes`
- current measurements:
  - `history_bytes`
  - `history_rows`
  - `history_dead_tuples`
  - `graph_total_bytes`
  - `coverage_since`
  - `coverage_days`
  - `gb_per_day`
- projections:
  - `projected_7d_bytes`
  - `projected_30d_bytes`
  - `projected_60d_bytes`
  - `projected_90d_bytes`
- effective UI state:
  - `effective_coverage_days`
  - `is_partial_coverage`
  - `cleanup_available`

### Prune Rules

Prune should apply only to `graph_channel_policy_history`.

Order:

1. Delete rows older than `history_retention_days`.
2. If `history_max_bytes > 0` and estimated table size is still above cap,
   delete oldest rows in batches until the table is under the cap or until the
   minimum practical coverage floor is reached.
3. Run `VACUUM (ANALYZE)` after user-triggered cleanup.

The current policy table must never be pruned by this flow:

- `graph_channel_policy_current`

### UI Scope

In `Graph Explorer`, add a storage/settings surface showing:

- current historical coverage
- current table size
- recommended preset for this node
- estimated storage at 7/30/60/90 days
- selected retention days
- selected max GB
- expected remaining coverage after cleanup
- explicit warning that normal vacuum reuses storage internally but may not
  shrink the physical database file immediately

Fee report behavior should follow available coverage:

- if the user asks for `90d` but only `32d` exist, show partial coverage instead
  of pretending the full range is available
- `General`, `Channels`, and `Closed Channels` should remain available because
  they do not depend on long policy history

### Suggested Implementation Steps

1. Wire `graph_explorer_config.history_retention_days` into the service instead
   of using only the hard-coded constant.
2. Add `history_max_bytes` schema migration and config model.
3. Add storage stats query for graph tables and history coverage.
4. Implement configured prune by days.
5. Implement configured prune by size cap.
6. Add explicit cleanup endpoint for existing nodes.
7. Run `VACUUM (ANALYZE)` only after explicit cleanup, not on every background
   prune.
8. Add UI presets, estimates, and partial-coverage messaging.
9. Add tests for config defaults, days prune, size prune, and partial fee-report
   coverage labels.

### Later Follow-Ups

These are intentionally not part of the first storage-control delivery:

- reduce `graph_nodes` bloat by avoiding JSONB updates when alias/address/features
  did not change
- add aggregated daily policy history so long-range reports can survive after
  raw event history is pruned
- add advanced physical compaction for operators who need to return disk to the
  filesystem immediately

## Non-Goals For Now

Do not add all of this to the UI at once.

Prefer:

- backend logic first
- API visibility
- result validation
- then selective UI exposure

## Notes

This file is intended to remain the single backlog document for these pending proposals.
