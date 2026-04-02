# Graph Close Classifier Plan

## Status

Proposed.

This document defines the proposal for adding native close-type classification to `Graph Explorer`, using the app's own `LND + bitcoind + Postgres` stack.

The goal is to reduce `unknown` close types for public remote channels and classify observed closures into:

- `mutual_close`
- `force_close`
- `penalty_close`
- `unknown`

The feature must work without external providers. External refill remains optional and out of scope for the core classifier.

## Motivation

`Graph Explorer` already persists public graph closure events and can already enrich close type when the channel belongs to the local node through `LND ClosedChannels`.

That is enough for:

- local-node closed channels
- partial classification when `LND` is authoritative

It is not enough for:

- arbitrary remote public channels seen only through graph topology updates

Today, the native graph stream reports that a channel closed, but does not tell us the close type. This leaves many remote closures as `unknown`, even when explorers such as Amboss show a more specific result.

The app already has most of the plumbing needed to close that gap:

- graph close persistence in `Graph Explorer`
- `bitcoind` RPC access already used elsewhere
- `chan_point` and `closed_height` already stored for graph close events

This makes a native close classifier a realistic next step.

## Current state in the codebase

The current implementation already has the following building blocks:

- native graph close ingestion:
  - `lightningos-light/internal/server/graph_explorer_service.go`
- local-node close enrichment through `LND ClosedChannels`:
  - `lightningos-light/internal/server/graph_explorer_closed_enrichment.go`
- graph close query/reporting:
  - `lightningos-light/internal/server/graph_explorer_tabs.go`
- Bitcoin RPC config resolution and block header lookup:
  - `lightningos-light/internal/server/closed_channel_time.go`
  - `lightningos-light/internal/server/bitcoin_local.go`

This is a strong foundation because the app already stores:

- `chan_id`
- `chan_point`
- `node1_pubkey`
- `node2_pubkey`
- `capacity_sat`
- `closed_height`
- `observed_at`

What is missing is the on-chain classification layer.

## Objectives

- Classify remote public closures natively with no required external provider.
- Preserve the existing local `LND ClosedChannels` enrichment path.
- Normalize all product-facing close types into a small, stable taxonomy.
- Persist the classification result and the evidence source.
- Support background backfill of previously `unknown` close events.
- Make confidence and provenance explicit in the data model.

## Non-objectives

- Do not promise 100% classification coverage.
- Do not promise exact parity with Amboss.
- Do not depend on Amboss to classify the core dataset.
- Do not classify private remote channels that never appeared in the public graph.
- Do not present weak heuristics as high-confidence truth.

## Product truth

The classifier must distinguish between:

- `authoritative local classification`
  - derived from `LND ClosedChannels`
- `high-confidence native on-chain classification`
  - derived from Bitcoin transaction evidence
- `insufficient evidence`
  - remains `unknown`

The UI must reflect this difference through source and confidence metadata.

## Close taxonomy

### Product-facing types

The normalized UI/API values should be:

- `mutual_close`
- `force_close`
- `penalty_close`
- `unknown`

### Mapping rules

Recommended normalization:

- `COOPERATIVE_CLOSE` from LND -> `mutual_close`
- `LOCAL_FORCE_CLOSE` from LND -> `force_close`
- `REMOTE_FORCE_CLOSE` from LND -> `force_close`
- `BREACH_CLOSE` from LND -> `penalty_close`
- `FUNDING_CANCELED` from LND -> `unknown`
- `ABANDONED` from LND -> `unknown`

Reasoning:

- `penalty_close` is the product term the operator will understand more easily than `breach_close`
- `funding_canceled` and `abandoned` are edge cases better treated as `unknown` in the first product iteration unless the UI explicitly adds them later

## Data sources

### Source 1: LND graph topology updates

Provides:

- channel close event existence
- `chan_id`
- `chan_point`
- `capacity`
- `closed_height`

Does not provide:

- close type
- close txid
- close fee

### Source 2: LND ClosedChannels

Provides:

- authoritative local close type
- close height
- close tx hash
- local channel resolution details

This only applies when the closed channel belongs to the local node.

### Source 3: bitcoind RPC

Provides the basis for remote native classification:

- block hash at close height
- block contents / transactions
- raw transaction details
- transaction inputs and outputs
- follow-up spends when needed

This is the core source for classifying remote graph closures.

## Proposed backend architecture

### New component

Add a dedicated backend component, for example:

- `GraphCloseClassifier`

Recommended file:

- `lightningos-light/internal/server/graph_close_classifier.go`

The classifier should run as a background worker owned by `GraphExplorerService`, because:

- the dataset already lives in `Graph Explorer`
- the state and tables are already there
- classification is part of graph enrichment, not a separate product

### Execution model

Recommended flow:

1. graph close event is persisted with `close_type = null`
2. `GraphExplorerService` enqueues or marks the event as pending classification
3. background classifier resolves the close transaction and close type
4. result is stored back in `graph_close_events`
5. `graph_channels.close_type` is updated from the best known classification

### Suggested runtime triggers

- classify immediately after observing a new graph close
- periodic retry for pending or `unknown` events
- manual backfill endpoint later if needed

## Proposed schema changes

### graph_close_events

Add fields:

- `close_txid text`
- `close_fee_sat bigint`
- `close_classifier text`
- `close_confidence text`
- `close_reason text`
- `classified_at timestamptz`
- `classification_error text`
- `classification_attempts integer not null default 0`

Recommended semantics:

- `close_classifier`
  - `lnd`
  - `bitcoind`
  - `amboss_refill`
- `close_confidence`
  - `high`
  - `medium`
  - `low`
- `close_reason`
  - short machine-readable explanation such as:
    - `lnd_closedchannels_breach`
    - `close_tx_single_spend_no_resolution`
    - `commitment_pattern_detected`
    - `penalty_pattern_detected`

### graph_channels

Optional but recommended mirrored fields:

- `close_txid text`
- `close_confidence text`
- `classified_at timestamptz`

This keeps the latest close classification easily queryable without scanning events.

## Core classification workflow

### Step 1: authoritative local enrichment

First attempt:

- if the closed channel belongs to the local node, use `LND ClosedChannels`

This should always win over heuristic on-chain classification because it is authoritative for the local node.

### Step 2: resolve close transaction

If local LND cannot answer:

1. read `chan_point`
2. parse funding txid and funding vout
3. use `closed_height` to inspect the close block
4. find the transaction in that block that spends the funding outpoint

Recommended primary method:

- inspect the transactions in block `closed_height`

Fallback methods:

- inspect a small adjacent window if needed:
  - `closed_height - 1`
  - `closed_height`
  - `closed_height + 1`

Reasoning:

- the graph event height should usually be enough
- a tiny window makes the classifier resilient to off-by-one edge cases

### Step 3: inspect close transaction structure

Once the close tx is found:

- record `close_txid`
- compute fee if transaction inputs are available through RPC
- inspect output structure
- inspect descendant spends where needed

### Step 4: assign normalized type and confidence

Return:

- `close_type`
- `close_confidence`
- `close_reason`
- `close_txid`
- `close_fee_sat` when available

## Classification rules

### mutual_close

Recommended classification when all or most of the following are true:

- the close tx directly spends the funding outpoint
- no second-stage LN resolution pattern is observed
- no commitment-style delayed outputs are observed
- output pattern looks like a direct negotiated settlement
- there is no evidence of breach/penalty sweep behavior

Recommended confidence:

- `high` if the transaction structure is strongly clean
- `medium` if inferred from partial evidence

### force_close

Recommended classification when at least one of the following is true:

- commitment-style output pattern is observed
- anchor or HTLC-related resolution pattern is observed
- descendant sweep behavior is consistent with unilateral close resolution
- there is strong evidence the channel closed through commitment transaction rather than cooperative settlement

Recommended confidence:

- `high` when descendant resolution clearly confirms it
- `medium` when the close tx itself strongly indicates unilateral close

### penalty_close

This type must exist explicitly in the classifier.

Recommended classification only when evidence is strong. Do not overuse it.

Candidate indicators:

- local `LND ClosedChannels` reports `BREACH_CLOSE`
- transaction/spend pattern matches revoked-state penalty sweep behavior
- descendant spend pattern is inconsistent with ordinary unilateral close and strongly matches justice-style claim behavior

Recommended rule:

- only emit `penalty_close` on `high` confidence
- otherwise downgrade to `force_close` or keep `unknown`

Reasoning:

- false-positive `penalty_close` is worse than leaving the item as `force_close` or `unknown`
- penalty/breach inference for remote channels is the hardest category

### unknown

Remain `unknown` when:

- close tx cannot be resolved
- evidence is contradictory
- only weak heuristics are available
- there is not enough information to separate `mutual` from `force`

This is an expected outcome, not a failure.

## Confidence model

Recommended confidence semantics:

- `high`
  - authoritative local LND result
  - or strong on-chain structural evidence
- `medium`
  - likely classification with good but not definitive evidence
- `low`
  - weak heuristic result

Product recommendation:

- only persist `close_type` for `medium` or `high`
- keep `unknown` for `low`

This avoids false certainty.

## Bitcoin RPC needs

The classifier should reuse the existing Bitcoin RPC wiring already present in the backend.

Expected RPC usage:

- `getblockhash`
- `getblock` or `getrawtransaction` with decoded verbose responses
- `getblockheader`
- optional `gettxout`

Implementation recommendation:

- extend the existing Bitcoin RPC helper layer rather than creating a second RPC client abstraction

## Persistence strategy

### New events

For fresh close events:

- classify as soon as possible
- write result back to the event row
- update the `graph_channels` mirror fields

### Historical backfill

Add a periodic worker that scans:

- recent `unknown` events first
- then older `unknown` events in batches

This allows historical quality to improve over time without requiring a one-shot migration.

## UI recommendations

### Closed channels tab

Extend the table with the improved values already stored by the backend.

Recommended additions later:

- `close txid`
- `close fee`
- `confidence`

### Source badge

Recommended source values:

- `native`
  - pure graph event
- `native+lnd`
  - local authoritative enrichment
- `native+bitcoind`
  - native on-chain classification

### Tooltip

Recommended tooltip for `unknown`:

- `Native graph event observed, but close type could not be classified with sufficient confidence.`

## Operational safeguards

- do not block graph ingestion on classification failure
- store classification errors for retry and debugging
- cap per-run batch size
- cap adjacent-block search window
- cache decoded transactions during a batch

## Suggested implementation phases

### Phase 1: schema and scaffolding

- add classifier fields to `graph_close_events`
- add helper for funding outpoint parsing
- add Bitcoin RPC helpers needed for tx/block lookup
- add background job skeleton

### Phase 2: local + remote mutual/force classification

- keep current local LND enrichment
- add remote `close_txid` resolution from `chan_point + closed_height`
- implement `mutual_close`
- implement `force_close`
- persist `close_txid` and `close_fee_sat`

### Phase 3: backfill and UI enrichment

- backfill recent `unknown` events
- expose `close_txid`, `close_fee_sat`, `confidence`
- show `short_channel_id` consistently in the UI

### Phase 4: penalty close

- add `penalty_close` classification for local `BREACH_CLOSE`
- add high-confidence remote penalty heuristics
- keep downgrade path to `force_close` or `unknown` when evidence is insufficient

## Open questions

- whether the current Bitcoin RPC layer already exposes enough decoded transaction detail or should gain a dedicated tx decoder helper
- whether remote penalty classification should require descendant-spend inspection across multiple blocks
- whether `funding_canceled` and `abandoned` should remain `unknown` or become explicit UI values later

## Definition of done

The feature is done when:

- remote graph close events are no longer limited to `unknown` by default
- local closed channels still prefer authoritative `LND` classification
- `mutual_close`, `force_close`, `penalty_close`, and `unknown` are consistently normalized
- classification source and confidence are persisted
- historical `unknown` rows can be retried in the background
- the UI can show the normalized type without inventing data

## Recommendation

Implement this as a native `Graph Explorer` enrichment path owned by the backend.

This keeps the product aligned with the long-term design principle already established for the graph module:

- local dataset first
- native history first
- external enrichment optional

It also creates a durable foundation for future features such as:

- close cost reporting
- close transaction deep links
- closure-type charts
- channel lifecycle analytics
