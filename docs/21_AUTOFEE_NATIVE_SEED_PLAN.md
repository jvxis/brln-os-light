# Autofee Native Seed Plan

## Status

Proposed.

This document defines the proposal for adding a `native graph seed` to `Autofee`, built from the app's own `Graph Explorer` dataset.

The goal is to let `Autofee` derive fee reference signals from the locally persisted Lightning graph history, while keeping the existing `Amboss` seed as an optional source in the UI and in the backend.

## Motivation

`Autofee` already supports an optional `Amboss`-based fee seed and already exposes the related checkbox and token handling in the current UI.

At the same time, the app now persists public graph policy history through `Graph Explorer`.

This creates a clear product opportunity:

- reduce dependency on external providers for market reference
- use the app's own historical base as the primary source when coverage is good
- keep `Amboss` as optional enrichment or fallback
- make seed provenance explicit to the operator

This is a better long-term model than treating `Amboss` as the only market-aware source.

## Current behavior

Today, `Autofee` resolves the seed in this order:

1. `Amboss`, if enabled and token is available
2. local `outrate`, if fresh enough
3. in-memory previous seed
4. hardcoded default

The current `Amboss` logic is richer than a simple average. It uses:

- incoming fee metric series
- percentiles such as `p65` and `p95`
- median blending
- volatility penalty using `std / mean`
- `outgoing / incoming` skew adjustment

This means a native replacement should not just be "weighted average ppm". It needs a proper seed derivation model.

## Objectives

- Add a `native graph seed` to `Autofee`.
- Keep the existing `Amboss` checkbox in the UI.
- Add a second UI control for enabling the native seed.
- Support both sources at the same time with deterministic precedence.
- Use local graph history when coverage is sufficient.
- Fall back cleanly when local graph coverage is weak.
- Expose seed provenance in tags and UI.

## Non-objectives

- Do not remove `Amboss` support.
- Do not promise that the native seed is identical to Amboss metrics.
- Do not use private channel data.
- Do not use external refill as a requirement for the native seed MVP.
- Do not enable native seed silently without operator visibility.

## Product decision

### UI model

Keep the current `Amboss fee reference` checkbox.

Add a second checkbox:

- `Use native graph seed` in EN
- `Usar seed nativa do grafo` in PT-BR

Recommended behavior:

- if only `native` is enabled:
  - use native graph seed when coverage is sufficient
  - otherwise fall back to existing local behavior
- if only `Amboss` is enabled:
  - preserve the current behavior
- if both are enabled:
  - prefer native graph seed first
  - fall back to `Amboss` if native coverage is insufficient
  - fall back to existing local behavior if both are unavailable

This keeps the UI simple and matches the user's mental model better than introducing a provider dropdown in the first iteration.

### Why checkboxes instead of a selector

Two checkboxes communicate intent clearly:

- `I want to use our own data`
- `I also want external fallback if available`

This is especially useful during the first months of native history accumulation, where operators may want:

- native-first behavior
- but still retain Amboss when a node has sparse native coverage

## Proposed seed resolution order

Recommended backend order:

1. native graph seed, if `native_seed_enabled = true` and coverage is sufficient
2. Amboss seed, if `amboss_enabled = true` and token is valid
3. local `outrate`, if fresh enough
4. previous in-memory seed
5. hardcoded default

Recommended seed tags:

- `seed:native`
- `seed:native-insufficient`
- `seed:native-error`
- `seed:amboss`
- `seed:amboss-missing`
- `seed:amboss-error`
- `seed:outrate`
- `seed:mem`
- `seed:default`

Recommended combined-mode tag when native failed and Amboss won:

- `seed:native-fallback-amboss`

## Native seed data source

The native seed should be derived from the `Graph Explorer` local history, especially:

- current public policies
- persisted `graph_channel_policy_history`
- node-level daily fee analytics derived from that history

The important constraint is that the native seed must be based on public routing policy history, not on private forwarding revenue.

That makes the native seed a `public market reference`, not a proprietary profitability metric.

## Proposed backend architecture

### New Autofee config fields

Extend `AutofeeConfig` and `AutofeeConfigUpdate` with:

- `NativeSeedEnabled bool json:"native_seed_enabled"`
- `NativeSeedMinDays int json:"native_seed_min_days"`
- `NativeSeedMinSamples int json:"native_seed_min_samples"`

Recommended defaults:

- `native_seed_enabled = false`
- `native_seed_min_days = 7`
- `native_seed_min_samples = 12`

Reasoning:

- native seed should be opt-in at first
- minimum-day and minimum-sample thresholds should be explicit and tunable

### New native seed resolver

Add a dedicated resolver, for example:

- `fetchNativeSeed(pubkey string) (seed float64, seedP95 float64, peerMarketSkew float64, tags []string, err error)`

This should mirror the shape already used by the `Amboss` path so the caller logic in `seedForChannel` stays simple.

### Suggested implementation layout

- keep `seedForChannel` as the single orchestration point
- add a native fee seed helper in the `Autofee` backend
- do not query the `Graph Explorer` HTTP API from `Autofee`
- use direct DB access internally, because both modules already live in the same backend process

This avoids unnecessary service-to-service HTTP coupling inside the app.

## Native metrics model

### Phase 1 recommendation

For a first implementation, derive the native seed from daily node-level metrics computed from `graph_channel_policy_history`.

Recommended derived series per target pubkey and day:

- `outbound_avg_ppm`
- `outbound_weighted_avg_ppm`
- `outbound_median_ppm`
- `outbound_p65_ppm`
- `outbound_p95_ppm`
- `outbound_stddev_ppm`
- `outbound_sample_count`
- `inbound_avg_ppm`
- `inbound_weighted_avg_ppm`
- `inbound_median_ppm`
- `inbound_p65_ppm`
- `inbound_p95_ppm`
- `inbound_stddev_ppm`
- `inbound_sample_count`

### Recommended storage

Add a derived table, for example:

- `graph_node_fee_metrics_daily`

Suggested key:

- `(pubkey, day)`

Suggested indexes:

- `(pubkey, day desc)`
- `(day desc)`

Reasoning:

- `Autofee` may need seeds for many peer pubkeys during a run
- repeatedly aggregating directly from raw policy history will get expensive
- a daily derived table makes the seed computation cheap, deterministic and inspectable

### Population strategy

Recommended approach:

- compute or refresh daily metrics after graph refresh cycles
- recompute the current UTC day incrementally
- recompute a limited recent window, for example the last `7-14` days, to absorb late observations

This is preferable to doing full-history recomputation on every run.

## Native seed formula

The native seed does not need to match Amboss exactly, but it should follow the same reasoning pattern.

Recommended initial formula:

1. take the last `N` complete days inside the configured lookback
2. use `inbound_weighted_avg_ppm` daily series as the primary market reference
3. compute `p65` of that daily series as the base seed
4. blend part of the result with `inbound_median_ppm`
5. apply a volatility penalty derived from `stddev / mean`
6. compute `peerMarketSkew` from `outbound_weighted_avg_ppm / inbound_weighted_avg_ppm`
7. apply a bounded skew factor
8. cap the result at native `p95`
9. cap by `MaxPpm` if configured

This preserves the spirit of the current `Amboss` logic while staying honest about the fact that the source is local public graph history.

### Why inbound should remain primary

The current `Amboss` seed uses incoming fee market data as the anchor.

That is still a reasonable default for the native model because it reflects what the rest of the public graph is charging to route into the target node.

Outbound should still matter, but as a skew or comparison factor rather than the primary anchor.

## Coverage and eligibility rules

Native seed should only be eligible when coverage is good enough.

Recommended initial rules:

- at least `native_seed_min_days` complete days available
- at least `native_seed_min_samples` inbound samples across the usable window
- baseline series must not be dominated by zeros
- metrics older than the configured lookback should not qualify

If any of these fail:

- return no native seed
- add tag `seed:native-insufficient`
- continue to the next source in the fallback chain

This is critical. A weak native seed is worse than no native seed.

## Observability and operator feedback

The operator should be able to understand which source won.

Recommended UI additions in `Autofee`:

- keep the existing `Amboss` checkbox and token field
- add `Use native graph seed`
- show a short helper line:
  - `Uses local Graph Explorer history when coverage is sufficient`
- show native coverage metadata when available:
  - `Coverage since YYYY-MM-DD`
- expose the winning seed source in decision tags and status views

Recommended run tags:

- `seed:native`
- `seed:native-p65`
- `seed:native-med`
- `seed:native-vol-penalty`
- `seed:native-ratiox1.12`
- `seed:native-p95cap`
- `seed:native-fallback-amboss`

This matters because operators will compare `Autofee` behavior before and after enabling native seed.

## Relationship with Graph Explorer

`Graph Explorer` should remain the historical data collector and analytics base.

`Autofee` should consume that persisted data, not duplicate collection logic.

Recommended division of responsibilities:

- `Graph Explorer`
  - collect and persist graph policy history
  - derive node-level daily fee metrics
- `Autofee`
  - choose the seed source
  - apply channel-level policy logic
  - expose decision tags and operator controls

This keeps collection and policy decisions cleanly separated.

## Risks

### Sparse nodes

Some nodes will not have enough public history to produce a strong native seed.

Mitigation:

- explicit coverage thresholds
- fallback to Amboss or existing local seed flow

### Public-only visibility

The native seed only sees public policy history.

Mitigation:

- be explicit in UI and docs
- do not describe it as forwarding revenue or proprietary market truth

### Cold-start period

During the first days after enabling `Graph Explorer`, native coverage will be weak.

Mitigation:

- keep native seed opt-in initially
- support native-first with Amboss fallback

### Performance

Computing per-node seed analytics directly from raw history during every `Autofee` run may be too expensive.

Mitigation:

- use a daily derived metrics table
- keep seed resolution query-light

## Delivery phases

### Phase 1

- add plan-approved config fields
- add UI checkbox for native seed
- keep existing Amboss UI unchanged
- add seed precedence logic in `seedForChannel`
- no native seed activation until metrics path exists

### Phase 2

- create `graph_node_fee_metrics_daily`
- add background aggregation from graph policy history
- expose coverage metadata needed by `Autofee`

### Phase 3

- implement `fetchNativeSeed`
- integrate native-first and fallback behavior
- add new seed tags
- validate with known nodes and compare against Amboss-enabled runs

### Phase 4

- add operator-facing visibility of native coverage and winning source
- refine thresholds based on production observations
- consider making native seed the recommended default for mature installs

## Definition of done

This plan should be considered implemented when:

- `Autofee` can use a native graph seed with no external provider
- the operator can enable or disable native seed in the UI
- the operator can still enable or disable `Amboss`
- combined mode works with deterministic precedence
- insufficient native coverage falls back cleanly
- seed provenance is visible in tags or UI
- the solution does not materially regress `Autofee` runtime performance

## Recommended product position

The app should treat this as:

- `native market reference, built locally over time`

not as:

- `an Amboss clone inside Autofee`

That framing is more honest, more defensible, and more aligned with the architecture already built in `Graph Explorer`.
