# Graph Explorer Plan

## Status

Mostly implemented.

Audit date: 2026-06-20.

Implemented: `GraphExplorerService`, local graph persistence, search, node
General/Channels/Closed/Fee views, status/recompute APIs, storage limits,
storage cleanup UI, and coverage messaging.

Still open: optional external refill remains a design item. The code exposes
refill status fields, but no `POST /api/lnops/graph-explorer/refill` route,
worker, or operator flow was found.

This document defines the proposal for a new module called `Graph Explorer` / `Explorador do Grafo`.

The goal is to let the operator search Lightning Network nodes by `alias` or `pubkey` and inspect:

- basic node information
- public channels of the node
- observed closed channels of the node
- fee analytics built from public routing policy history

The module should use `LND + local Postgres` as the primary source of truth and build its own historical base from the moment it starts running.

External refill must remain optional. If the operator already has an `Amboss` token configured, the app may use it as an explicit bootstrap/enrichment path, but never as a hard dependency for the core feature.

## Motivation

The project already has important building blocks:

- `Network Atlas` already renders a graph-oriented view of peers and geography.
- `Lightning Ops` already exposes operational channel and peer data.
- `Channel Ranking` already follows the desired backend pattern:
  - dedicated service
  - Postgres persistence
  - background refresh
  - dedicated UI page
- `Autofee` already stores an optional `Amboss` token and already talks to the Amboss GraphQL API.
- the app already has a stable `pgx` + Postgres pattern for read-heavy operational features.

At the time this plan was written, the missing piece was a dedicated graph
intelligence module that treats the public Lightning graph as a first-class
dataset. The module now exists; the remaining planned gap is optional external
refill.

Today the operator can inspect:

- own channels
- own peers
- own closed channels
- own fee policies

But cannot easily inspect another public node as an object with its own:

- identity
- public channel footprint
- observed closure history
- fee policy profile

This limits operational discovery and makes the operator depend on external explorers for analysis that should live inside the app.

## Objectives

- Create a dedicated `Graph Explorer` page in the UI.
- Allow searching nodes by `alias` or `pubkey`.
- Persist the public graph locally in Postgres.
- Build historical coverage over time, even with no external provider enabled.
- Expose four operator views for a target node:
  - `General`
  - `Channels`
  - `Closed Channels`
  - `Fee Report`
- Make data provenance explicit in the UI:
  - local graph history
  - optional external refill
- Reuse the existing service pattern already used by modules such as `Channel Ranking`.

## Non-objectives

- Do not attempt to clone `amboss.space` exactly.
- Do not depend on an external provider for the MVP.
- Do not promise complete remote history before the service start date.
- Do not infer private channels for remote nodes.
- Do not promise exact remote forwarding revenue or proprietary fee metrics for third-party nodes.
- Do not hide the difference between:
  - native local history
  - external refill

## Product truth and data limitations

The module must be honest about what the app can and cannot know.

### What native LND can provide

From the public graph and graph updates, the app can natively learn:

- public nodes
- public channels
- public routing policies
- node aliases, colors and addresses
- channel open presence in the known graph
- channel close events observed by graph topology updates

### What native LND cannot fully provide for arbitrary remote nodes

Without external enrichment or extra heuristics, the app cannot fully know:

- private channels of remote nodes
- complete remote closed-channel history from before the feature was enabled
- exact remote close type for arbitrary public graph closures
- remote forwarding revenue
- Amboss-specific "corrected" fee metrics

This means the UI must clearly communicate:

- `coverage since`
- `source`
- `known vs unknown` classification coverage when applicable

## Product positioning

### Navigation

Create a new top-level page:

- `Graph Explorer` in EN
- `Explorador do Grafo` in PT-BR

Recommended placement:

- below `Network Atlas`
- above `Lightning Ops`

Reasoning:

- `Network Atlas` is geographic/topology-oriented.
- `Graph Explorer` is node-centric analysis.
- `Lightning Ops` remains the operator area for the local node.

### Relationship with existing modules

`Graph Explorer` should complement, not replace, existing modules:

- `Network Atlas`
  - good for spatial view
  - can deep-link into `Graph Explorer` for a selected pubkey
- `Lightning Ops`
  - remains the local-node operational panel
- `Channel Ranking`
  - remains channel analysis for the local node only

## UX proposal

### Search experience

The page opens with a search box:

- search by exact `pubkey`
- search by partial `alias`
- search by prefix `pubkey`

Recommended behavior:

- exact `pubkey` match first
- alias prefix matches next
- alias substring matches after
- limit to top 10 or 20 results

### Page structure

Top area:

- search box
- result picker
- source badge
- coverage badge
- last graph sync

Node header:

- alias
- pubkey
- node color
- clearnet/onion addresses
- channel count
- total capacity
- oldest public channel age
- youngest public channel age
- last policy update seen

Tabs:

1. `General`
- node identity
- current public footprint
- capacity and channel aggregates
- peer/address summary

2. `Channels`
- public open channels of the target node
- peer alias
- peer pubkey
- capacity
- age
- current public routing policies on both directions
- disabled flags
- last policy update

3. `Closed Channels`
- observed public channel closures involving the node
- coverage banner:
  - `Native coverage since YYYY-MM-DD`
- table of closures
- breakdown charts only when classification coverage is sufficient

4. `Fee Report`
- policy-based fee analytics
- current distribution of outbound policies
- current distribution of peer-side policies toward the node
- historical snapshots built locally over time

### Important naming recommendation

In native-only mode, the `Fee Report` tab should be careful not to imply proprietary or revenue-level insight.

Recommended approach:

- keep the tab label `Fee Report`
- but label the native metrics inside as:
  - `Public policy analytics`
  - `Outbound policy distribution`
  - `Peer-side policy distribution`

This avoids over-claiming what the local dataset actually knows.

## Relevant existing pieces to reuse

The feature should follow patterns already present in the codebase:

- `NetworkAtlas` page and routes for graph-like UI behavior
- `ChannelRankingService` for:
  - schema management
  - background loop
  - stale refresh on read
  - dedicated handlers
- existing `Server` lifecycle integration
- existing shared Postgres pool
- existing optional Amboss token storage in `Autofee`

## Proposed backend architecture

Create a new backend service:

- `GraphExplorerService`

Suggested files:

- `internal/server/graph_explorer_init.go`
- `internal/server/graph_explorer_handlers.go`
- `internal/server/graph_explorer_service.go`

Suggested `Server` fields:

- `graphExplorerInitAt`
- `graphExplorerMu`
- `graphExplorer`
- `graphExplorerErr`

Suggested UI files:

- `ui/src/pages/GraphExplorer.tsx`
- `ui/src/api.ts` additions
- i18n additions in `en.json` and `pt-BR.json`

### Service responsibilities

The service should:

- ensure schema
- bootstrap local graph tables
- maintain a live graph update stream
- periodically reconcile with a full graph snapshot
- materialize node-level aggregates
- expose read APIs for search and detail pages
- optionally run explicit refill jobs

### Suggested internal loops

1. `bootstrap loop`
- if graph tables are empty or stale, run a full graph load

2. `stream loop`
- keep `SubscribeChannelGraph` connected
- persist incremental changes

3. `reconcile loop`
- periodically re-read the graph to recover from missed stream updates

4. `snapshot loop`
- materialize daily node and policy aggregates

5. `optional refill loop`
- run only when explicitly enabled by the operator

## Primary data sources

The native implementation should use the public LND graph RPCs as the base:

- `DescribeGraph`
- `GetNodeInfo`
- `GetChanInfo`
- `SubscribeChannelGraph`

Recommended usage:

- `DescribeGraph`
  - bootstrap the public graph snapshot
- `GetNodeInfo(pubkey, includeChannels=true)`
  - refresh selected node detail on demand
- `GetChanInfo(chan_id)`
  - enrich channel detail when needed
- `SubscribeChannelGraph`
  - keep local graph state updated over time

## Persistence model

### 1. `graph_explorer_config`

Purpose:

- module settings
- refill controls
- retention controls

Suggested columns:

- `id`
- `enabled`
- `refill_enabled`
- `refill_provider`
- `history_retention_days`
- `reconcile_interval_sec`
- `snapshot_interval_sec`
- `created_at`
- `updated_at`

### 2. `graph_sync_state`

Purpose:

- runtime cursors and coverage metadata

Suggested records:

- `first_native_coverage_at`
- `last_bootstrap_at`
- `last_stream_event_at`
- `last_reconcile_at`
- `last_snapshot_at`
- `last_refill_at`
- `last_refill_provider`

### 3. `graph_nodes`

Purpose:

- current known node state

Suggested columns:

- `pubkey text primary key`
- `alias text`
- `color text`
- `addresses_json jsonb`
- `features_json jsonb`
- `channel_count integer`
- `total_capacity_sat bigint`
- `first_seen_at timestamptz`
- `last_seen_at timestamptz`
- `last_graph_update_at timestamptz`
- `last_indexed_at timestamptz`
- `source text`

Indexes:

- exact `pubkey`
- `lower(alias)`
- optional trigram index later if needed

### 4. `graph_channels`

Purpose:

- current known public channel state

Suggested columns:

- `chan_id bigint primary key`
- `chan_point text unique`
- `node1_pubkey text`
- `node2_pubkey text`
- `capacity_sat bigint`
- `open_block_height integer`
- `status text`
- `first_seen_at timestamptz`
- `last_seen_at timestamptz`
- `closed_at timestamptz null`
- `closed_height integer null`
- `close_source text null`
- `close_type text null`
- `last_indexed_at timestamptz`

Status examples:

- `open`
- `closed`

Important note:

- in native-only mode, `close_type` will often remain `unknown`
- that is acceptable and should be explicit in the product

### 5. `graph_channel_policy_current`

Purpose:

- fast reads for the latest known public routing policy per directed edge

Suggested columns:

- `chan_id bigint`
- `advertising_pubkey text`
- `connecting_pubkey text`
- `fee_base_msat bigint`
- `fee_rate_ppm bigint`
- `time_lock_delta integer`
- `min_htlc_msat bigint`
- `max_htlc_msat bigint`
- `disabled boolean`
- `policy_last_update_at timestamptz`
- `last_indexed_at timestamptz`

Primary key:

- `(chan_id, advertising_pubkey)`

### 6. `graph_channel_policy_history`

Purpose:

- historical routing policy snapshots
- input for fee analytics

Suggested columns:

- `id bigserial primary key`
- `chan_id bigint`
- `advertising_pubkey text`
- `connecting_pubkey text`
- `fee_base_msat bigint`
- `fee_rate_ppm bigint`
- `time_lock_delta integer`
- `min_htlc_msat bigint`
- `max_htlc_msat bigint`
- `disabled boolean`
- `captured_at timestamptz`
- `source text`

Retention recommendation:

- keep raw policy history for `365 days` by default
- keep daily aggregates indefinitely

### 7. `graph_close_events`

Purpose:

- closure events observed over time

Suggested columns:

- `id bigserial primary key`
- `chan_id bigint`
- `chan_point text`
- `node1_pubkey text`
- `node2_pubkey text`
- `capacity_sat bigint`
- `closed_height integer`
- `observed_at timestamptz`
- `close_type text null`
- `source text`
- `metadata_json jsonb`

### 8. `graph_node_daily_metrics`

Purpose:

- stable historical aggregates per node

Suggested columns:

- `snapshot_date date`
- `pubkey text`
- `public_channel_count integer`
- `total_capacity_sat bigint`
- `median_capacity_sat bigint`
- `largest_capacity_sat bigint`
- `smallest_capacity_sat bigint`
- `oldest_channel_age_sec bigint`
- `youngest_channel_age_sec bigint`
- `outbound_fee_rate_avg_ppm bigint`
- `outbound_fee_rate_median_ppm bigint`
- `inbound_peer_fee_rate_avg_ppm bigint`
- `close_count_total integer`
- `close_count_known_type integer`
- `created_at timestamptz`

Primary key:

- `(snapshot_date, pubkey)`

## Sync model

### Phase A: bootstrap

On first start:

- call `DescribeGraph`
- persist nodes
- persist channels
- persist current policies
- write initial coverage state

This creates the baseline from which history begins.

### Phase B: live graph tracking

Keep `SubscribeChannelGraph` connected in the background.

Persist:

- node updates
- policy updates
- new channel announcements
- closed channel notifications

For policy changes:

- update `graph_channel_policy_current`
- append to `graph_channel_policy_history`

For closed channels:

- update `graph_channels.status = closed`
- set `closed_at` and `closed_height`
- insert into `graph_close_events`

### Phase C: reconciliation

Because the stream may disconnect, add periodic reconciliation.

Recommended behavior:

- every `6h`, run a lightweight full-graph reconciliation
- compare current `graph_channels` with `DescribeGraph`
- recover missing updates
- mark channels missing from the current graph as closure candidates only if supported by reconciliation logic

Recommendation:

- prefer explicit stream close events as the trusted native signal for `closed`
- use reconciliation mainly to heal drift, not to invent unsupported detail

### Phase D: daily materialization

At least once per day:

- compute node-level metrics
- compute fee distribution snapshots
- materialize daily aggregates for fast UI reads

## Search design

### Initial search strategy

Start simple:

- exact `pubkey`
- `pubkey` prefix
- alias prefix using `lower(alias) like`
- alias substring using `lower(alias) like`

This is sufficient for the first version.

### Later search upgrade

If needed, add:

- `pg_trgm`
- weighted ranking by exactness
- result caching for hot queries

### Search result payload

Each search result should include:

- `pubkey`
- `alias`
- `channel_count`
- `total_capacity_sat`
- `last_seen_at`
- `coverage_since`

## Detailed feature design

### General tab

Native data should show:

- alias
- pubkey
- node color
- known public addresses
- number of public channels
- total public capacity
- smallest public channel
- largest public channel
- average public channel size
- oldest observed public channel
- youngest observed public channel
- last policy update seen

All of these can be derived from the local persisted graph.

### Channels tab

The table should include:

- peer alias
- peer pubkey
- channel id
- channel point
- capacity
- age
- current policy advertised by the target node
- current policy advertised by the peer
- disabled status
- last update seen

This is a strong native feature and should be part of the MVP.

### Closed Channels tab

This tab must be explicit about coverage:

- `Observed public closures since YYYY-MM-DD`

The native dataset can reliably show:

- channel id
- channel point
- capacity
- close height
- observed close time
- peer snapshot if captured before closure

Native-only limitations:

- close type will often be `unknown`
- historical closures from before module activation will be missing

Recommendation for charts:

- only show a close-type donut when enough events have known type
- otherwise show:
  - total closures observed
  - total capacity closed
  - known vs unknown classification coverage

### Fee Report tab

The native implementation should be based on public policy history, not on remote forwarding revenue.

Recommended native metrics:

- outbound policy average ppm
- outbound policy median ppm
- outbound policy weighted average by channel capacity
- outbound policy min and max
- peer-side policy average ppm toward the target node
- peer-side policy median ppm toward the target node
- peer-side weighted average by channel capacity
- histograms by ppm bucket
- daily trend built from local snapshots

Recommended wording:

- `Policy Report`
- `Public fee policy distribution`
- `Peer-side policy distribution`

Avoid claiming:

- actual remote earnings
- corrected proprietary market metrics

## Optional external refill

### Product principle

External refill is optional and operator-controlled.

The core feature must still work with:

- no token
- no outbound external request
- no third-party dependency

### Recommended configuration behavior

- if an `Amboss` token already exists in `Autofee`, the app may detect that a token is available
- but no refill should happen automatically
- the operator must explicitly enable refill inside `Graph Explorer`

### Refill goals

External refill can be used for:

- older fee-history enrichment
- older node-level aggregates
- optional metadata enrichment for closures when supported

Important rule:

- never overwrite native truth silently
- every externally derived row must keep provenance via `source`

### Recommended provenance values

- `native`
- `amboss_refill`
- `amboss_enriched`

## API proposal

Suggested namespace:

- `/api/lnops/graph-explorer/*`

### Suggested endpoints

`GET /api/lnops/graph-explorer/status`

- service availability
- first native coverage date
- last bootstrap
- last stream event
- last reconcile
- token available yes/no
- refill enabled yes/no

`GET /api/lnops/graph-explorer/search?q=...&limit=20`

- node search results

`GET /api/lnops/graph-explorer/nodes/{pubkey}/general`

- header and general aggregates

`GET /api/lnops/graph-explorer/nodes/{pubkey}/channels`

- open public channels

`GET /api/lnops/graph-explorer/nodes/{pubkey}/closed?range=30d|90d|1y|all`

- observed closures

`GET /api/lnops/graph-explorer/nodes/{pubkey}/fees?range=7d|30d|90d|1y|all`

- policy analytics

`POST /api/lnops/graph-explorer/recompute`

- force a reconciliation pass

`POST /api/lnops/graph-explorer/refill`

- explicit external refill
- disabled unless token is available and operator enabled refill

## UI implementation notes

Suggested route key:

- `graph-explorer`

Suggested hash deep links:

- `#graph-explorer?pubkey=<pubkey>`
- `#graph-explorer?pubkey=<pubkey>&tab=channels`
- `#graph-explorer?pubkey=<pubkey>&tab=closed`
- `#graph-explorer?pubkey=<pubkey>&tab=fees`

Useful deep-link integrations:

- `Network Atlas` marker -> open target node in `Graph Explorer`
- remote peer links from `Lightning Ops` -> open in `Graph Explorer`
- future cross-links from `Channel Ranking` peer context if useful

## Recommended implementation phases

### Phase 1 - graph foundation

Deliverables:

- `GraphExplorerService`
- schema creation
- native graph bootstrap via `DescribeGraph`
- status endpoint
- reconciliation endpoint
- background stream via `SubscribeChannelGraph`

Success criteria:

- service can start with empty DB
- service can rebuild from scratch
- graph state persists in Postgres

### Phase 2 - search and general view

Deliverables:

- search endpoint
- `Graph Explorer` UI page
- `General` tab
- node header
- coverage/source badges

Success criteria:

- operator can search by alias or pubkey
- operator can open a node profile with current public footprint

### Phase 3 - channels and observed closures

Deliverables:

- `Channels` tab
- `Closed Channels` tab
- close event persistence
- explicit coverage messaging

Success criteria:

- operator can inspect public channels of a target node
- operator can see observed closure history since native coverage start

### Phase 4 - native fee analytics

Deliverables:

- `Fee Report` tab
- policy history snapshots
- histograms and summary cards
- daily aggregates

Success criteria:

- operator can inspect a node's public fee policy profile over time
- UI does not claim revenue insight it does not actually have

### Phase 5 - optional external refill

Deliverables:

- refill config
- refill endpoint
- provenance tracking
- optional use of existing Amboss token

Success criteria:

- external refill is opt-in
- native mode remains fully functional without refill
- source provenance is visible in the API and UI

## Testing strategy

### Backend tests

- schema init is idempotent
- bootstrap upserts are idempotent
- node search ranking behaves as expected
- policy update handling appends history correctly
- close events transition channels to `closed`
- reconciliation recovers from dropped stream state
- refill cannot run without explicit enablement

### UI tests

- search empty state
- search results and node load
- tab navigation
- coverage banner rendering
- correct empty states for:
  - no close history yet
  - no fee history yet
  - refill disabled

### Operational tests

- cold start on empty DB
- stream reconnect after disconnection
- full rebuild after manual table wipe
- large graph bootstrap without service crash

## Definition of done

The feature can be considered done for the first release when:

- the module starts automatically with the backend
- it persists graph state locally in Postgres
- it survives restart without losing history
- the operator can search nodes by alias or pubkey
- the operator can view:
  - general info
  - channels
  - observed closed channels
  - fee policy analytics
- the UI shows native coverage start
- the UI shows source provenance
- the app works fully with no external token

## Main risks and mitigations

### Risk 1: operator expects Amboss-level history on day 1

Mitigation:

- show `coverage since`
- label native-only history clearly
- keep refill optional but visible

### Risk 2: stream gaps create silent drift

Mitigation:

- periodic reconciliation
- persist sync state
- expose last stream event timestamp

### Risk 3: close-type expectations are too high

Mitigation:

- allow `unknown`
- do not fake precision
- chart only known coverage when enough data exists

### Risk 4: fee tab over-claims insight

Mitigation:

- phrase native metrics as policy analytics
- keep proprietary/provider metrics separate by source

### Risk 5: graph tables grow too fast

Mitigation:

- current-state tables stay compact
- raw policy history gets retention control
- daily aggregates remain lightweight

## Recommended first implementation slice

To keep the rollout disciplined, the first actual coding slice should cover only:

1. schema + service lifecycle
2. bootstrap + stream + reconcile
3. search endpoint
4. `General` tab
5. `Channels` tab

Reasoning:

- this creates immediate operator value
- this proves the local graph persistence model
- this avoids overcommitting on closure classification and fee semantics too early

After that, `Closed Channels` and `Fee Report` can be added on top of a stable native historical base.

## References

Primary RPC references:

- LND Lightning Service index:
  - https://api.lightning.community/category/lightning-service/index.html
- LND `SubscribeChannelGraph`:
  - https://api.lightning.community/api/lnd/lightning/subscribe-channel-graph/index.html
- LND `GetChanInfo`:
  - https://api.lightning.community/api/lnd/lightning/get-chan-info/index.html
- LND `DescribeGraph`:
  - https://api.lightning.community/api/lnd/lightning/describe-graph/index.html

Related existing project design references:

- `docs/18_CHANNEL_RANKING_PLAN.md`
- `docs/17_CLOSE_RECOVERY_MANAGER_PLAN.md`
- `docs/19_BACKLOG.md`
