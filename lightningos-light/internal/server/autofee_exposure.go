package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lightningos-light/internal/lndclient"
)

const autofeeExposurePeriod = 5 * time.Minute
const autofeeExposureMaxGap = 2 * autofeeExposurePeriod

const autofeeExposureSchema = `
create table if not exists autofee_policy_observations (
 channel_point text not null,
 observed_at timestamptz not null,
 snapshot jsonb not null,
 primary key (channel_point, observed_at)
);
create index if not exists autofee_policy_observations_time_idx on autofee_policy_observations (observed_at);
create table if not exists autofee_policy_applications (
 channel_point text not null,
 started_at timestamptz not null,
 completed_at timestamptz not null,
 observation jsonb not null,
 primary key (channel_point, started_at, completed_at)
);
create index if not exists autofee_policy_applications_time_idx on autofee_policy_applications (completed_at);
`

type autofeePolicySnapshot struct {
	lndclient.LocalPolicyObservation
	StartedAt  time.Time `json:"started_at"`
	ObservedAt time.Time `json:"observed_at"`
	Session    string    `json:"session"`
	Loss       uint64    `json:"loss"`
}

type autofeeExposureMetrics struct {
	ForwardCount         int64 `json:"forward_count"`
	ForwardAmountMsat    int64 `json:"forward_amount_msat"`
	ForwardFeeMsat       int64 `json:"forward_fee_msat"`
	IncomingForwardCount int64 `json:"incoming_forward_count"`
	RebalanceCount       int64 `json:"rebalance_count"`
}

type autofeePolicyExposure struct {
	Start           time.Time             `json:"start"`
	End             time.Time             `json:"end"`
	DurationSeconds float64               `json:"duration_seconds"`
	Confidence      string                `json:"confidence"`
	Flags           []string              `json:"flags"`
	Before          autofeePolicySnapshot `json:"before"`
	After           autofeePolicySnapshot `json:"after"`
	// Observed activity is retained even when policy attribution is unknown.
	Activity       autofeeExposureMetrics                   `json:"activity"`
	PolicyActivity *autofeeExposureMetrics                  `json:"policy_activity"`
	Applications   []lndclient.PolicyApplicationObservation `json:"applications"`
}

func autofeeExposureEnabled() bool { return os.Getenv("LIGHTNINGOS_AUTOFEE_EXPOSURE_ENABLED") != "0" }

// This observer is independent of the decision/outcome loops. No observations,
// application acknowledgements or measurements are inputs to fee/job selection.
func (s *AutofeeService) policyExposureLoop(stop <-chan struct{}) {
	if !autofeeExposureEnabled() || s.db == nil || s.lnd == nil || stop == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	events := s.lnd.PolicyApplicationObservations()
	session := time.Now().UTC().Format(time.RFC3339Nano)
	var lost uint64
	schemaReady := false
	report := func(err error) {
		if err != nil && s.logger != nil {
			s.logger.Printf("autofee: policy observation unavailable: %v", err)
		}
	}
	storeApplication := func(event lndclient.PolicyApplicationObservation) {
		if !schemaReady {
			lost++
			return
		}
		writeCtx, done := context.WithTimeout(ctx, 2*time.Second)
		defer done()
		if err := persistAutofeePolicyApplication(writeCtx, s.db, event); err != nil {
			lost++
			report(err)
		}
	}
	tick := func() {
		tickCtx, done := context.WithTimeout(ctx, 20*time.Second)
		defer done()
		if !schemaReady {
			if _, err := s.db.Exec(tickCtx, autofeeExposureSchema); err != nil {
				report(err)
				return
			}
			schemaReady = true
		}
		started := time.Now().UTC()
		policies, err := s.lnd.ObserveLocalPolicies(tickCtx)
		if err != nil {
			lost++
			report(err)
			return
		}
		observed := time.Now().UTC()
		// Persist completed applications before making this sample queryable.
		// A bounded drain cannot starve sampling under sustained policy writes.
		draining := true
		for i := 0; i < 256 && draining; i++ {
			select {
			case event := <-events:
				storeApplication(event)
			default:
				draining = false
			}
			if tickCtx.Err() != nil {
				lost++
				return
			}
		}
		loss := lost + s.lnd.PolicyObservationLoss()
		snapshots := make([]autofeePolicySnapshot, 0, len(policies))
		for _, p := range policies {
			snapshots = append(snapshots, autofeePolicySnapshot{p, started, observed, session, loss})
		}
		if err := persistAutofeePolicySnapshots(tickCtx, s.db, snapshots); err != nil {
			lost++
			report(err)
			return
		}
		// Bounded retention work; only this diagnostic ledger is affected.
		_, err = s.db.Exec(tickCtx, `delete from autofee_policy_observations where (channel_point,observed_at) in
 (select channel_point,observed_at from autofee_policy_observations where observed_at < now()-interval '30 days' order by observed_at limit 5000)`)
		report(err)
		_, err = s.db.Exec(tickCtx, `delete from autofee_policy_applications where (channel_point,started_at,completed_at) in
 (select channel_point,started_at,completed_at from autofee_policy_applications where completed_at < now()-interval '30 days' order by completed_at limit 5000)`)
		report(err)
	}
	tick()
	ticker := time.NewTicker(autofeeExposurePeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			storeApplication(event)
		case <-ticker.C:
			tick()
		}
	}
}

func persistAutofeePolicySnapshots(ctx context.Context, pool *pgxpool.Pool, snapshots []autofeePolicySnapshot) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	batch := &pgx.Batch{}
	for _, snapshot := range snapshots {
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		batch.Queue(`insert into autofee_policy_observations(channel_point,observed_at,snapshot)
 values($1,$2,$3) on conflict do nothing`, snapshot.ChannelPoint, snapshot.ObservedAt, raw)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func persistAutofeePolicyApplication(ctx context.Context, pool *pgxpool.Pool, event lndclient.PolicyApplicationObservation) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	point := event.Request.ChannelPoint
	if event.Request.ApplyAll {
		point = "*"
	}
	_, err = pool.Exec(ctx, `insert into autofee_policy_applications(channel_point,started_at,completed_at,observation)
 values($1,$2,$3,$4) on conflict do nothing`, point, event.StartedAt, event.CompletedAt, raw)
	return err
}

func buildAutofeePolicyExposure(before, after autofeePolicySnapshot, applications []lndclient.PolicyApplicationObservation) autofeePolicyExposure {
	item := autofeePolicyExposure{
		Start: before.ObservedAt, End: after.ObservedAt, DurationSeconds: after.ObservedAt.Sub(before.ObservedAt).Seconds(),
		Confidence: "sampled", Before: before, After: after, Flags: []string{"propagation_unobserved", "between_samples_unobserved"},
		Applications: []lndclient.PolicyApplicationObservation{},
	}
	uncertain := func(flag string) { item.Confidence = "unknown"; item.Flags = append(item.Flags, flag) }
	if before.Session != after.Session {
		uncertain("observer_restart")
	}
	if before.Loss != after.Loss {
		uncertain("observation_loss")
	}
	if item.DurationSeconds <= 0 || after.ObservedAt.Sub(before.ObservedAt) > autofeeExposureMaxGap {
		uncertain("observation_gap")
	}
	if after.StartedAt.Before(before.ObservedAt) {
		uncertain("overlapping_acquisition")
	}
	if before.ChannelID != after.ChannelID {
		uncertain("channel_identity_changed")
	}
	if before.Fees == nil || after.Fees == nil {
		uncertain("policy_unavailable")
	} else {
		if before.Fees.BaseMsat != after.Fees.BaseMsat || before.Fees.RatePPM != after.Fees.RatePPM {
			uncertain("outgoing_changed")
		}
		if before.Fees.InboundBaseMsat != after.Fees.InboundBaseMsat || before.Fees.InboundRatePPM != after.Fees.InboundRatePPM {
			uncertain("inbound_changed")
		}
	}
	if !before.Active || !after.Active || before.Disabled || after.Disabled {
		item.Flags = append(item.Flags, "unavailable_observed")
	}
	if before.Active != after.Active || before.Disabled != after.Disabled {
		item.Flags = append(item.Flags, "availability_changed")
	}
	if before.LocalBalanceSat != after.LocalBalanceSat || before.UnsettledBalanceSat != after.UnsettledBalanceSat {
		item.Flags = append(item.Flags, "liquidity_changed")
	}
	for _, event := range applications {
		if !event.Request.ApplyAll && event.Request.ChannelPoint != after.ChannelPoint {
			continue
		}
		// Include the first acquisition window too: the initial policy may have
		// been read while an update was in flight. Ack is never read-back proof.
		if !event.StartedAt.Before(item.End) || event.CompletedAt.Before(before.StartedAt) {
			continue
		}
		item.Applications = append(item.Applications, event)
	}
	if len(item.Applications) > 0 {
		uncertain("policy_application_overlap")
		for _, event := range item.Applications {
			if event.Source == "manual" {
				item.Flags = append(item.Flags, "manual_intervention")
				break
			}
		}
	}
	return item
}

// Source observations are durable and immutable. Adjacent windows are [start,
// end); a forwarding event can belong to at most one window for this channel.
// Measurements are computed in one read-only snapshot, so late notifications
// can be included on a later read without freezing incomplete aggregates.
func readAutofeePolicyExposures(ctx context.Context, pool *pgxpool.Pool, point string, since, until time.Time, limit int) ([]autofeePolicyExposure, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `select snapshot from autofee_policy_observations
 where channel_point=$1 and observed_at >= $2 and observed_at <= $3 order by observed_at desc limit $4`, point, since.Add(-autofeeExposureMaxGap), until, limit+2)
	if err != nil {
		return nil, err
	}
	snapshots := make([]autofeePolicySnapshot, 0, limit+2)
	for rows.Next() {
		var raw []byte
		var snapshot autofeePolicySnapshot
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			rows.Close()
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := make([]autofeePolicyExposure, 0, limit)
	if len(snapshots) < 2 {
		return items, nil
	}
	first := snapshots[len(snapshots)-1]
	rows, err = tx.Query(ctx, `select observation from autofee_policy_applications
 where channel_point in ($1,'*') and completed_at >= $2 and started_at < $3
 order by started_at limit 2001`, point, first.StartedAt, snapshots[0].ObservedAt)
	if err != nil {
		return nil, err
	}
	applications := []lndclient.PolicyApplicationObservation{}
	for rows.Next() {
		var raw []byte
		var event lndclient.PolicyApplicationObservation
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			rows.Close()
			return nil, err
		}
		applications = append(applications, event)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(applications) > 2000 {
		return nil, fmt.Errorf("too many policy applications; request a shorter window")
	}
	for i := 0; i+1 < len(snapshots) && len(items) < limit; i++ {
		item := buildAutofeePolicyExposure(snapshots[i+1], snapshots[i], applications)
		if item.Start.Before(since) {
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return items, nil
	}
	// Indexable channel predicates plus bounded time windows. Count zero-fee
	// forwards as movement; incoming/assisted activity has no added revenue.
	batch := &pgx.Batch{}
	for i := range items {
		item := &items[i]
		id := int64(item.After.ChannelID)
		batch.Queue(`select
 count(*) filter(where type='forward' and coalesce(chan_id_out,channel_id)=$1),
 coalesce(sum(case when amount_out_msat>0 then amount_out_msat else amount_sat*1000 end) filter(where type='forward' and coalesce(chan_id_out,channel_id)=$1),0),
 coalesce(sum(case when fee_msat>0 then fee_msat when fee_sat>0 then fee_sat*1000
 when amount_in_msat>0 and amount_out_msat>0 and amount_in_msat>amount_out_msat then amount_in_msat-amount_out_msat else 0 end)
 filter(where type='forward' and coalesce(chan_id_out,channel_id)=$1),0),
 count(*) filter(where type='forward' and chan_id_in=$1),
 count(*) filter(where type='rebalance' and status in ('SETTLED','SUCCEEDED'))
 from notifications where occurred_at >= $2 and occurred_at < $3
 and ((type='forward' and (chan_id_out=$1 or (chan_id_out is null and channel_id=$1) or chan_id_in=$1))
 or (type='rebalance' and (rebal_target_chan_id=$1 or rebal_source_chan_id=$1 or (rebal_target_chan_id is null and channel_id=$1))))`, id, item.Start, item.End)
	}
	results := tx.SendBatch(ctx, batch)
	defer results.Close()
	for i := range items {
		item := &items[i]
		err = results.QueryRow().Scan(
			&item.Activity.ForwardCount, &item.Activity.ForwardAmountMsat, &item.Activity.ForwardFeeMsat, &item.Activity.IncomingForwardCount, &item.Activity.RebalanceCount)
		if err != nil {
			return nil, err
		}
		if item.Activity.RebalanceCount > 0 {
			item.Flags = append(item.Flags, "rebalance_observed")
		}
		if item.Activity.IncomingForwardCount > 0 {
			item.Flags = append(item.Flags, "incoming_movement_observed")
		}
		if item.Confidence == "sampled" {
			metrics := item.Activity
			item.PolicyActivity = &metrics
		}
	}
	if err := results.Close(); err != nil {
		return nil, err
	}
	return items, tx.Commit(ctx)
}
