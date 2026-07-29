package reports

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func EnsureSchema(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(ctx, `
create table if not exists reports_daily (
  report_date date primary key,
  forward_fee_revenue_sats bigint not null default 0,
  forward_fee_revenue_msat bigint not null default 0,
  rebalance_fee_cost_sats bigint not null default 0,
  rebalance_fee_cost_msat bigint not null default 0,
  payment_fee_cost_sats bigint not null default 0,
  payment_fee_cost_msat bigint not null default 0,
  onchain_fee_cost_sats bigint not null default 0,
  onchain_fee_cost_msat bigint not null default 0,
  onchain_coop_close_cost_sats bigint not null default 0,
  onchain_coop_close_cost_msat bigint not null default 0,
  onchain_local_force_cost_sats bigint not null default 0,
  onchain_local_force_cost_msat bigint not null default 0,
  onchain_remote_force_cost_sats bigint not null default 0,
  onchain_remote_force_cost_msat bigint not null default 0,
  keysend_received_sats bigint not null default 0,
  keysend_received_msat bigint not null default 0,
  keysend_received_count integer not null default 0,
  net_routing_profit_sats bigint not null default 0,
  net_routing_profit_msat bigint not null default 0,
  net_with_keysend_sats bigint not null default 0,
  net_with_keysend_msat bigint not null default 0,
  forward_count integer not null default 0,
  rebalance_count integer not null default 0,
  rebalance_volume_sats bigint not null default 0,
  rebalance_volume_msat bigint not null default 0,
  payment_count integer not null default 0,
  routed_volume_sats bigint not null default 0,
  routed_volume_msat bigint not null default 0,
  onchain_balance_sats bigint null,
  lightning_balance_sats bigint null,
  total_balance_sats bigint null,
  provenance_last_sync_at timestamptz null,
  provenance_last_sync_age_hours double precision null,
  provenance_health_alert boolean null,
  provenance_last_error text null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists reports_movement_daily (
  report_date date primary key,
  outbound_target_sats bigint not null default 0,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists reports_live_cache (
  timezone text not null,
  lookback_hours integer not null default 0,
  start_local timestamptz not null,
  end_local timestamptz not null,
  metrics jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (timezone, lookback_hours)
);

alter table reports_daily add column if not exists forward_fee_revenue_msat bigint not null default 0;
alter table reports_daily add column if not exists rebalance_fee_cost_msat bigint not null default 0;
alter table reports_daily add column if not exists payment_fee_cost_sats bigint not null default 0;
alter table reports_daily add column if not exists payment_fee_cost_msat bigint not null default 0;
alter table reports_daily add column if not exists onchain_fee_cost_sats bigint not null default 0;
alter table reports_daily add column if not exists onchain_fee_cost_msat bigint not null default 0;
alter table reports_daily add column if not exists onchain_coop_close_cost_sats bigint not null default 0;
alter table reports_daily add column if not exists onchain_coop_close_cost_msat bigint not null default 0;
alter table reports_daily add column if not exists onchain_local_force_cost_sats bigint not null default 0;
alter table reports_daily add column if not exists onchain_local_force_cost_msat bigint not null default 0;
alter table reports_daily add column if not exists onchain_remote_force_cost_sats bigint not null default 0;
alter table reports_daily add column if not exists onchain_remote_force_cost_msat bigint not null default 0;
alter table reports_daily add column if not exists keysend_received_sats bigint not null default 0;
alter table reports_daily add column if not exists keysend_received_msat bigint not null default 0;
alter table reports_daily add column if not exists keysend_received_count integer not null default 0;
alter table reports_daily add column if not exists net_routing_profit_msat bigint not null default 0;
alter table reports_daily add column if not exists net_with_keysend_sats bigint not null default 0;
alter table reports_daily add column if not exists net_with_keysend_msat bigint not null default 0;
alter table reports_daily add column if not exists rebalance_volume_sats bigint not null default 0;
alter table reports_daily add column if not exists rebalance_volume_msat bigint not null default 0;
alter table reports_daily add column if not exists payment_count integer not null default 0;
alter table reports_daily add column if not exists routed_volume_msat bigint not null default 0;
alter table reports_daily add column if not exists provenance_last_sync_at timestamptz null;
alter table reports_daily add column if not exists provenance_last_sync_age_hours double precision null;
alter table reports_daily add column if not exists provenance_health_alert boolean null;
alter table reports_daily add column if not exists provenance_last_error text null;
alter table reports_daily add column if not exists sales_revenue_sats bigint not null default 0;
alter table reports_daily add column if not exists sales_revenue_msat bigint not null default 0;
alter table reports_daily add column if not exists sales_count integer not null default 0;
alter table reports_daily add column if not exists net_total_sats bigint not null default 0;
alter table reports_daily add column if not exists net_total_msat bigint not null default 0;
`)
	return err
}

func UpsertDaily(ctx context.Context, db *pgxpool.Pool, row Row) error {
	if db == nil {
		return nil
	}
	query, args := buildUpsertDaily(row)
	_, err := db.Exec(ctx, query, args...)
	return err
}

func buildUpsertDaily(row Row) (string, []any) {
	reportDate := normalizeReportDate(row.ReportDate)
	metrics := row.Metrics

	args := []any{
		reportDate,
		metrics.ForwardFeeRevenueSat,
		metrics.ForwardFeeRevenueMsat,
		metrics.RebalanceFeeCostSat,
		metrics.RebalanceFeeCostMsat,
		metrics.PaymentFeeCostSat,
		metrics.PaymentFeeCostMsat,
		metrics.OnchainFeeCostSat,
		metrics.OnchainFeeCostMsat,
		metrics.OnchainCoopCloseCostSat,
		metrics.OnchainCoopCloseCostMsat,
		metrics.OnchainLocalForceCostSat,
		metrics.OnchainLocalForceCostMsat,
		metrics.OnchainRemoteForceCostSat,
		metrics.OnchainRemoteForceCostMsat,
		metrics.KeysendReceivedSat,
		metrics.KeysendReceivedMsat,
		metrics.KeysendReceivedCount,
		metrics.NetRoutingProfitSat,
		metrics.NetRoutingProfitMsat,
		metrics.NetWithKeysendSat,
		metrics.NetWithKeysendMsat,
		metrics.ForwardCount,
		metrics.RebalanceCount,
		metrics.RebalanceVolumeSat,
		metrics.RebalanceVolumeMsat,
		metrics.PaymentCount,
		metrics.RoutedVolumeSat,
		metrics.RoutedVolumeMsat,
		nullableInt64(metrics.OnchainBalanceSat),
		nullableInt64(metrics.LightningBalanceSat),
		nullableInt64(metrics.TotalBalanceSat),
		nullableTime(metrics.ProvenanceLastSyncAt),
		nullableFloat64(metrics.ProvenanceLastSyncAgeHours),
		nullableBool(metrics.ProvenanceHealthAlert),
		nullableString(metrics.ProvenanceLastError),
		metrics.SalesRevenueSat,
		metrics.SalesRevenueMsat,
		metrics.SalesCount,
		metrics.NetTotalSat,
		metrics.NetTotalMsat,
	}

	query := `
insert into reports_daily (
  report_date,
  forward_fee_revenue_sats,
  forward_fee_revenue_msat,
  rebalance_fee_cost_sats,
  rebalance_fee_cost_msat,
  payment_fee_cost_sats,
  payment_fee_cost_msat,
  onchain_fee_cost_sats,
  onchain_fee_cost_msat,
  onchain_coop_close_cost_sats,
  onchain_coop_close_cost_msat,
  onchain_local_force_cost_sats,
  onchain_local_force_cost_msat,
  onchain_remote_force_cost_sats,
  onchain_remote_force_cost_msat,
  keysend_received_sats,
  keysend_received_msat,
  keysend_received_count,
  net_routing_profit_sats,
  net_routing_profit_msat,
  net_with_keysend_sats,
  net_with_keysend_msat,
  forward_count,
  rebalance_count,
  rebalance_volume_sats,
  rebalance_volume_msat,
  payment_count,
  routed_volume_sats,
  routed_volume_msat,
  onchain_balance_sats,
  lightning_balance_sats,
  total_balance_sats,
  provenance_last_sync_at,
  provenance_last_sync_age_hours,
  provenance_health_alert,
  provenance_last_error,
  sales_revenue_sats,
  sales_revenue_msat,
  sales_count,
  net_total_sats,
  net_total_msat
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41)
on conflict (report_date) do update set
  forward_fee_revenue_sats = excluded.forward_fee_revenue_sats,
  forward_fee_revenue_msat = excluded.forward_fee_revenue_msat,
  rebalance_fee_cost_sats = excluded.rebalance_fee_cost_sats,
  rebalance_fee_cost_msat = excluded.rebalance_fee_cost_msat,
  payment_fee_cost_sats = excluded.payment_fee_cost_sats,
  payment_fee_cost_msat = excluded.payment_fee_cost_msat,
  onchain_fee_cost_sats = excluded.onchain_fee_cost_sats,
  onchain_fee_cost_msat = excluded.onchain_fee_cost_msat,
  onchain_coop_close_cost_sats = excluded.onchain_coop_close_cost_sats,
  onchain_coop_close_cost_msat = excluded.onchain_coop_close_cost_msat,
  onchain_local_force_cost_sats = excluded.onchain_local_force_cost_sats,
  onchain_local_force_cost_msat = excluded.onchain_local_force_cost_msat,
  onchain_remote_force_cost_sats = excluded.onchain_remote_force_cost_sats,
  onchain_remote_force_cost_msat = excluded.onchain_remote_force_cost_msat,
  keysend_received_sats = excluded.keysend_received_sats,
  keysend_received_msat = excluded.keysend_received_msat,
  keysend_received_count = excluded.keysend_received_count,
  net_routing_profit_sats = excluded.net_routing_profit_sats,
  net_routing_profit_msat = excluded.net_routing_profit_msat,
  net_with_keysend_sats = excluded.net_with_keysend_sats,
  net_with_keysend_msat = excluded.net_with_keysend_msat,
  forward_count = excluded.forward_count,
  rebalance_count = excluded.rebalance_count,
  rebalance_volume_sats = excluded.rebalance_volume_sats,
  rebalance_volume_msat = excluded.rebalance_volume_msat,
  payment_count = excluded.payment_count,
  routed_volume_sats = excluded.routed_volume_sats,
  routed_volume_msat = excluded.routed_volume_msat,
  onchain_balance_sats = coalesce(excluded.onchain_balance_sats, reports_daily.onchain_balance_sats),
  lightning_balance_sats = coalesce(excluded.lightning_balance_sats, reports_daily.lightning_balance_sats),
  total_balance_sats = coalesce(excluded.total_balance_sats, reports_daily.total_balance_sats),
  provenance_last_sync_at = coalesce(excluded.provenance_last_sync_at, reports_daily.provenance_last_sync_at),
  provenance_last_sync_age_hours = coalesce(excluded.provenance_last_sync_age_hours, reports_daily.provenance_last_sync_age_hours),
  provenance_health_alert = coalesce(excluded.provenance_health_alert, reports_daily.provenance_health_alert),
  provenance_last_error = coalesce(excluded.provenance_last_error, reports_daily.provenance_last_error),
  sales_revenue_sats = excluded.sales_revenue_sats,
  sales_revenue_msat = excluded.sales_revenue_msat,
  sales_count = excluded.sales_count,
  net_total_sats = excluded.net_total_sats,
  net_total_msat = excluded.net_total_msat,
  updated_at = now()
`

	return query, args
}

func UpsertMovementTargetDaily(ctx context.Context, db *pgxpool.Pool, reportDate time.Time, outboundTargetSat int64) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(ctx, `
insert into reports_movement_daily (report_date, outbound_target_sats)
values ($1, $2)
on conflict (report_date) do update set
  outbound_target_sats = excluded.outbound_target_sats,
  updated_at = now()
`, normalizeReportDate(reportDate), outboundTargetSat)
	return err
}

func UpsertLiveSnapshot(ctx context.Context, db *pgxpool.Pool, snapshot liveSnapshot) error {
	if db == nil {
		return nil
	}
	query, args, err := buildUpsertLiveSnapshot(snapshot)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, query, args...)
	return err
}

func buildUpsertLiveSnapshot(snapshot liveSnapshot) (string, []any, error) {
	payload, err := json.Marshal(snapshot.Metrics)
	if err != nil {
		return "", nil, err
	}

	query := `
insert into reports_live_cache (
  timezone,
  lookback_hours,
  start_local,
  end_local,
  metrics
) values ($1,$2,$3,$4,$5)
on conflict (timezone, lookback_hours) do update set
  start_local = excluded.start_local,
  end_local = excluded.end_local,
  metrics = excluded.metrics,
  updated_at = now()
`

	args := []any{
		snapshot.Timezone,
		snapshot.LookbackHours,
		snapshot.Range.StartLocal,
		snapshot.Range.EndLocal,
		payload,
	}
	return query, args, nil
}

func FetchLiveSnapshot(ctx context.Context, db *pgxpool.Pool, timezone string, lookbackHours int) (liveSnapshot, bool, error) {
	if db == nil {
		return liveSnapshot{}, false, nil
	}

	var startLocal time.Time
	var endLocal time.Time
	var metricsRaw []byte
	var updatedAt time.Time
	err := db.QueryRow(ctx, `
select start_local, end_local, metrics, updated_at
from reports_live_cache
where timezone = $1 and lookback_hours = $2
`, timezone, lookbackHours).Scan(&startLocal, &endLocal, &metricsRaw, &updatedAt)
	if err != nil {
		if isNotFound(err) {
			return liveSnapshot{}, false, nil
		}
		return liveSnapshot{}, false, err
	}

	metrics := Metrics{}
	if len(metricsRaw) > 0 {
		if err := json.Unmarshal(metricsRaw, &metrics); err != nil {
			return liveSnapshot{}, false, err
		}
	}
	fillMsatFromSat(&metrics)

	loc, locErr := ResolveLocation(timezone, time.Local)
	if locErr != nil {
		loc = time.Local
	}
	startLocal = startLocal.In(loc)
	endLocal = endLocal.In(loc)

	return liveSnapshot{
		UpdatedAt: updatedAt,
		Range: TimeRange{
			StartLocal: startLocal,
			EndLocal:   endLocal,
			StartUTC:   startLocal.UTC(),
			EndUTC:     endLocal.UTC(),
		},
		Metrics:       metrics,
		LookbackHours: lookbackHours,
		Timezone:      timezone,
	}, true, nil
}

func FetchMovementTargetDaily(ctx context.Context, db *pgxpool.Pool, reportDate time.Time) (int64, bool, error) {
	if db == nil {
		return 0, false, nil
	}
	var target int64
	err := db.QueryRow(ctx, `
select outbound_target_sats
from reports_movement_daily
where report_date = $1
`, normalizeReportDate(reportDate)).Scan(&target)
	if err != nil {
		if isNotFound(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return target, true, nil
}

func FetchMovementTargetRangeSum(ctx context.Context, db *pgxpool.Pool, startDate, endDate time.Time) (int64, error) {
	if db == nil {
		return 0, nil
	}
	var total int64
	err := db.QueryRow(ctx, `
select coalesce(sum(outbound_target_sats), 0)
from reports_movement_daily
where report_date >= $1 and report_date <= $2
`, normalizeReportDate(startDate), normalizeReportDate(endDate)).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total, nil
}

func FetchMovementTargetAllSum(ctx context.Context, db *pgxpool.Pool) (int64, error) {
	if db == nil {
		return 0, nil
	}
	var total int64
	err := db.QueryRow(ctx, `
select coalesce(sum(outbound_target_sats), 0)
from reports_movement_daily
`).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total, nil
}

func FetchRange(ctx context.Context, db *pgxpool.Pool, startDate, endDate time.Time) ([]Row, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(ctx, `
select report_date,
  forward_fee_revenue_sats,
  forward_fee_revenue_msat,
  rebalance_fee_cost_sats,
  rebalance_fee_cost_msat,
  payment_fee_cost_sats,
  payment_fee_cost_msat,
  onchain_fee_cost_sats,
  onchain_fee_cost_msat,
  onchain_coop_close_cost_sats,
  onchain_coop_close_cost_msat,
  onchain_local_force_cost_sats,
  onchain_local_force_cost_msat,
  onchain_remote_force_cost_sats,
  onchain_remote_force_cost_msat,
  keysend_received_sats,
  keysend_received_msat,
  keysend_received_count,
  net_routing_profit_sats,
  net_routing_profit_msat,
  net_with_keysend_sats,
  net_with_keysend_msat,
  forward_count,
  rebalance_count,
  rebalance_volume_sats,
  rebalance_volume_msat,
  payment_count,
  routed_volume_sats,
  routed_volume_msat,
  onchain_balance_sats,
  lightning_balance_sats,
  total_balance_sats,
  provenance_last_sync_at,
  provenance_last_sync_age_hours,
  provenance_health_alert,
  provenance_last_error,
  sales_revenue_sats,
  sales_revenue_msat,
  sales_count,
  net_total_sats,
  net_total_msat
from reports_daily
where report_date >= $1 and report_date <= $2
order by report_date asc
`, normalizeReportDate(startDate), normalizeReportDate(endDate))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Row
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

func FetchAll(ctx context.Context, db *pgxpool.Pool) ([]Row, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(ctx, `
select report_date,
  forward_fee_revenue_sats,
  forward_fee_revenue_msat,
  rebalance_fee_cost_sats,
  rebalance_fee_cost_msat,
  payment_fee_cost_sats,
  payment_fee_cost_msat,
  onchain_fee_cost_sats,
  onchain_fee_cost_msat,
  onchain_coop_close_cost_sats,
  onchain_coop_close_cost_msat,
  onchain_local_force_cost_sats,
  onchain_local_force_cost_msat,
  onchain_remote_force_cost_sats,
  onchain_remote_force_cost_msat,
  keysend_received_sats,
  keysend_received_msat,
  keysend_received_count,
  net_routing_profit_sats,
  net_routing_profit_msat,
  net_with_keysend_sats,
  net_with_keysend_msat,
  forward_count,
  rebalance_count,
  rebalance_volume_sats,
  rebalance_volume_msat,
  payment_count,
  routed_volume_sats,
  routed_volume_msat,
  onchain_balance_sats,
  lightning_balance_sats,
  total_balance_sats,
  provenance_last_sync_at,
  provenance_last_sync_age_hours,
  provenance_health_alert,
  provenance_last_error,
  sales_revenue_sats,
  sales_revenue_msat,
  sales_count,
  net_total_sats,
  net_total_msat
from reports_daily
order by report_date asc
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Row
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

func FetchSummaryRange(ctx context.Context, db *pgxpool.Pool, startDate, endDate time.Time) (Summary, error) {
	if db == nil {
		return Summary{}, nil
	}
	var days int64
	totals := Metrics{}
	err := db.QueryRow(ctx, `
select
  count(*),
  coalesce(sum(forward_fee_revenue_sats), 0),
  coalesce(sum(forward_fee_revenue_msat), 0),
  coalesce(sum(rebalance_fee_cost_sats), 0),
  coalesce(sum(rebalance_fee_cost_msat), 0),
  coalesce(sum(payment_fee_cost_sats), 0),
  coalesce(sum(payment_fee_cost_msat), 0),
  coalesce(sum(onchain_fee_cost_sats), 0),
  coalesce(sum(onchain_fee_cost_msat), 0),
  coalesce(sum(onchain_coop_close_cost_sats), 0),
  coalesce(sum(onchain_coop_close_cost_msat), 0),
  coalesce(sum(onchain_local_force_cost_sats), 0),
  coalesce(sum(onchain_local_force_cost_msat), 0),
  coalesce(sum(onchain_remote_force_cost_sats), 0),
  coalesce(sum(onchain_remote_force_cost_msat), 0),
  coalesce(sum(keysend_received_sats), 0),
  coalesce(sum(keysend_received_msat), 0),
  coalesce(sum(keysend_received_count), 0),
  coalesce(sum(net_routing_profit_sats), 0),
  coalesce(sum(net_routing_profit_msat), 0),
  coalesce(sum(net_with_keysend_sats), 0),
  coalesce(sum(net_with_keysend_msat), 0),
  coalesce(sum(forward_count), 0),
  coalesce(sum(rebalance_count), 0),
  coalesce(sum(rebalance_volume_sats), 0),
  coalesce(sum(rebalance_volume_msat), 0),
  coalesce(sum(payment_count), 0),
  coalesce(sum(routed_volume_sats), 0),
  coalesce(sum(routed_volume_msat), 0)
from reports_daily
where report_date >= $1 and report_date <= $2
`, normalizeReportDate(startDate), normalizeReportDate(endDate)).Scan(
		&days,
		&totals.ForwardFeeRevenueSat,
		&totals.ForwardFeeRevenueMsat,
		&totals.RebalanceFeeCostSat,
		&totals.RebalanceFeeCostMsat,
		&totals.PaymentFeeCostSat,
		&totals.PaymentFeeCostMsat,
		&totals.OnchainFeeCostSat,
		&totals.OnchainFeeCostMsat,
		&totals.OnchainCoopCloseCostSat,
		&totals.OnchainCoopCloseCostMsat,
		&totals.OnchainLocalForceCostSat,
		&totals.OnchainLocalForceCostMsat,
		&totals.OnchainRemoteForceCostSat,
		&totals.OnchainRemoteForceCostMsat,
		&totals.KeysendReceivedSat,
		&totals.KeysendReceivedMsat,
		&totals.KeysendReceivedCount,
		&totals.NetRoutingProfitSat,
		&totals.NetRoutingProfitMsat,
		&totals.NetWithKeysendSat,
		&totals.NetWithKeysendMsat,
		&totals.ForwardCount,
		&totals.RebalanceCount,
		&totals.RebalanceVolumeSat,
		&totals.RebalanceVolumeMsat,
		&totals.PaymentCount,
		&totals.RoutedVolumeSat,
		&totals.RoutedVolumeMsat,
	)
	if err != nil {
		return Summary{}, err
	}

	fillMsatFromSat(&totals)
	return Summary{Days: days, Totals: totals, Averages: averageMetrics(totals, days)}, nil
}

func FetchSummaryAll(ctx context.Context, db *pgxpool.Pool) (Summary, error) {
	if db == nil {
		return Summary{}, nil
	}
	var days int64
	totals := Metrics{}
	err := db.QueryRow(ctx, `
select
  count(*),
  coalesce(sum(forward_fee_revenue_sats), 0),
  coalesce(sum(forward_fee_revenue_msat), 0),
  coalesce(sum(rebalance_fee_cost_sats), 0),
  coalesce(sum(rebalance_fee_cost_msat), 0),
  coalesce(sum(payment_fee_cost_sats), 0),
  coalesce(sum(payment_fee_cost_msat), 0),
  coalesce(sum(onchain_fee_cost_sats), 0),
  coalesce(sum(onchain_fee_cost_msat), 0),
  coalesce(sum(onchain_coop_close_cost_sats), 0),
  coalesce(sum(onchain_coop_close_cost_msat), 0),
  coalesce(sum(onchain_local_force_cost_sats), 0),
  coalesce(sum(onchain_local_force_cost_msat), 0),
  coalesce(sum(onchain_remote_force_cost_sats), 0),
  coalesce(sum(onchain_remote_force_cost_msat), 0),
  coalesce(sum(keysend_received_sats), 0),
  coalesce(sum(keysend_received_msat), 0),
  coalesce(sum(keysend_received_count), 0),
  coalesce(sum(net_routing_profit_sats), 0),
  coalesce(sum(net_routing_profit_msat), 0),
  coalesce(sum(net_with_keysend_sats), 0),
  coalesce(sum(net_with_keysend_msat), 0),
  coalesce(sum(forward_count), 0),
  coalesce(sum(rebalance_count), 0),
  coalesce(sum(rebalance_volume_sats), 0),
  coalesce(sum(rebalance_volume_msat), 0),
  coalesce(sum(payment_count), 0),
  coalesce(sum(routed_volume_sats), 0),
  coalesce(sum(routed_volume_msat), 0)
from reports_daily
`).Scan(
		&days,
		&totals.ForwardFeeRevenueSat,
		&totals.ForwardFeeRevenueMsat,
		&totals.RebalanceFeeCostSat,
		&totals.RebalanceFeeCostMsat,
		&totals.PaymentFeeCostSat,
		&totals.PaymentFeeCostMsat,
		&totals.OnchainFeeCostSat,
		&totals.OnchainFeeCostMsat,
		&totals.OnchainCoopCloseCostSat,
		&totals.OnchainCoopCloseCostMsat,
		&totals.OnchainLocalForceCostSat,
		&totals.OnchainLocalForceCostMsat,
		&totals.OnchainRemoteForceCostSat,
		&totals.OnchainRemoteForceCostMsat,
		&totals.KeysendReceivedSat,
		&totals.KeysendReceivedMsat,
		&totals.KeysendReceivedCount,
		&totals.NetRoutingProfitSat,
		&totals.NetRoutingProfitMsat,
		&totals.NetWithKeysendSat,
		&totals.NetWithKeysendMsat,
		&totals.ForwardCount,
		&totals.RebalanceCount,
		&totals.RebalanceVolumeSat,
		&totals.RebalanceVolumeMsat,
		&totals.PaymentCount,
		&totals.RoutedVolumeSat,
		&totals.RoutedVolumeMsat,
	)
	if err != nil {
		return Summary{}, err
	}

	fillMsatFromSat(&totals)
	return Summary{Days: days, Totals: totals, Averages: averageMetrics(totals, days)}, nil
}

func averageMetrics(totals Metrics, days int64) Metrics {
	if days <= 0 {
		return Metrics{}
	}
	return Metrics{
		ForwardFeeRevenueSat:       totals.ForwardFeeRevenueSat / days,
		ForwardFeeRevenueMsat:      totals.ForwardFeeRevenueMsat / days,
		RebalanceFeeCostSat:        totals.RebalanceFeeCostSat / days,
		RebalanceFeeCostMsat:       totals.RebalanceFeeCostMsat / days,
		PaymentFeeCostSat:          totals.PaymentFeeCostSat / days,
		PaymentFeeCostMsat:         totals.PaymentFeeCostMsat / days,
		OnchainFeeCostSat:          totals.OnchainFeeCostSat / days,
		OnchainFeeCostMsat:         totals.OnchainFeeCostMsat / days,
		OnchainCoopCloseCostSat:    totals.OnchainCoopCloseCostSat / days,
		OnchainCoopCloseCostMsat:   totals.OnchainCoopCloseCostMsat / days,
		OnchainLocalForceCostSat:   totals.OnchainLocalForceCostSat / days,
		OnchainLocalForceCostMsat:  totals.OnchainLocalForceCostMsat / days,
		OnchainRemoteForceCostSat:  totals.OnchainRemoteForceCostSat / days,
		OnchainRemoteForceCostMsat: totals.OnchainRemoteForceCostMsat / days,
		KeysendReceivedSat:         totals.KeysendReceivedSat / days,
		KeysendReceivedMsat:        totals.KeysendReceivedMsat / days,
		KeysendReceivedCount:       totals.KeysendReceivedCount / days,
		NetRoutingProfitSat:        totals.NetRoutingProfitSat / days,
		NetRoutingProfitMsat:       totals.NetRoutingProfitMsat / days,
		NetWithKeysendSat:          totals.NetWithKeysendSat / days,
		NetWithKeysendMsat:         totals.NetWithKeysendMsat / days,
		ForwardCount:               totals.ForwardCount / days,
		RebalanceCount:             totals.RebalanceCount / days,
		RebalanceVolumeSat:         totals.RebalanceVolumeSat / days,
		RebalanceVolumeMsat:        totals.RebalanceVolumeMsat / days,
		PaymentCount:               totals.PaymentCount / days,
		RoutedVolumeSat:            totals.RoutedVolumeSat / days,
		RoutedVolumeMsat:           totals.RoutedVolumeMsat / days,
	}
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRow(scanner rowScanner) (Row, error) {
	var reportDate time.Time
	var metrics Metrics
	var onchain pgtype.Int8
	var lightning pgtype.Int8
	var total pgtype.Int8
	var provenanceLastSync pgtype.Timestamptz
	var provenanceAge pgtype.Float8
	var provenanceAlert pgtype.Bool
	var provenanceLastError pgtype.Text
	err := scanner.Scan(
		&reportDate,
		&metrics.ForwardFeeRevenueSat,
		&metrics.ForwardFeeRevenueMsat,
		&metrics.RebalanceFeeCostSat,
		&metrics.RebalanceFeeCostMsat,
		&metrics.PaymentFeeCostSat,
		&metrics.PaymentFeeCostMsat,
		&metrics.OnchainFeeCostSat,
		&metrics.OnchainFeeCostMsat,
		&metrics.OnchainCoopCloseCostSat,
		&metrics.OnchainCoopCloseCostMsat,
		&metrics.OnchainLocalForceCostSat,
		&metrics.OnchainLocalForceCostMsat,
		&metrics.OnchainRemoteForceCostSat,
		&metrics.OnchainRemoteForceCostMsat,
		&metrics.KeysendReceivedSat,
		&metrics.KeysendReceivedMsat,
		&metrics.KeysendReceivedCount,
		&metrics.NetRoutingProfitSat,
		&metrics.NetRoutingProfitMsat,
		&metrics.NetWithKeysendSat,
		&metrics.NetWithKeysendMsat,
		&metrics.ForwardCount,
		&metrics.RebalanceCount,
		&metrics.RebalanceVolumeSat,
		&metrics.RebalanceVolumeMsat,
		&metrics.PaymentCount,
		&metrics.RoutedVolumeSat,
		&metrics.RoutedVolumeMsat,
		&onchain,
		&lightning,
		&total,
		&provenanceLastSync,
		&provenanceAge,
		&provenanceAlert,
		&provenanceLastError,
		&metrics.SalesRevenueSat,
		&metrics.SalesRevenueMsat,
		&metrics.SalesCount,
		&metrics.NetTotalSat,
		&metrics.NetTotalMsat,
	)
	if err != nil {
		return Row{}, err
	}
	if onchain.Valid {
		val := onchain.Int64
		metrics.OnchainBalanceSat = &val
	}
	if lightning.Valid {
		val := lightning.Int64
		metrics.LightningBalanceSat = &val
	}
	if total.Valid {
		val := total.Int64
		metrics.TotalBalanceSat = &val
	}
	if provenanceLastSync.Valid {
		val := provenanceLastSync.Time.UTC()
		metrics.ProvenanceLastSyncAt = &val
	}
	if provenanceAge.Valid {
		val := provenanceAge.Float64
		metrics.ProvenanceLastSyncAgeHours = &val
	}
	if provenanceAlert.Valid {
		val := provenanceAlert.Bool
		metrics.ProvenanceHealthAlert = &val
	}
	if provenanceLastError.Valid {
		val := provenanceLastError.String
		metrics.ProvenanceLastError = &val
	}
	fillMsatFromSat(&metrics)
	return Row{ReportDate: reportDate, Metrics: metrics}, nil
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableFloat64(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func normalizeReportDate(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func fillMsatFromSat(metrics *Metrics) {
	if metrics == nil {
		return
	}
	if metrics.ForwardFeeRevenueMsat == 0 && metrics.ForwardFeeRevenueSat != 0 {
		metrics.ForwardFeeRevenueMsat = metrics.ForwardFeeRevenueSat * 1000
	}
	if metrics.RebalanceFeeCostMsat == 0 && metrics.RebalanceFeeCostSat != 0 {
		metrics.RebalanceFeeCostMsat = metrics.RebalanceFeeCostSat * 1000
	}
	if metrics.RebalanceVolumeMsat == 0 && metrics.RebalanceVolumeSat != 0 {
		metrics.RebalanceVolumeMsat = metrics.RebalanceVolumeSat * 1000
	}
	if metrics.PaymentFeeCostMsat == 0 && metrics.PaymentFeeCostSat != 0 {
		metrics.PaymentFeeCostMsat = metrics.PaymentFeeCostSat * 1000
	}
	if metrics.OnchainFeeCostMsat == 0 && metrics.OnchainFeeCostSat != 0 {
		metrics.OnchainFeeCostMsat = metrics.OnchainFeeCostSat * 1000
	}
	if metrics.OnchainCoopCloseCostMsat == 0 && metrics.OnchainCoopCloseCostSat != 0 {
		metrics.OnchainCoopCloseCostMsat = metrics.OnchainCoopCloseCostSat * 1000
	}
	if metrics.OnchainLocalForceCostMsat == 0 && metrics.OnchainLocalForceCostSat != 0 {
		metrics.OnchainLocalForceCostMsat = metrics.OnchainLocalForceCostSat * 1000
	}
	if metrics.OnchainRemoteForceCostMsat == 0 && metrics.OnchainRemoteForceCostSat != 0 {
		metrics.OnchainRemoteForceCostMsat = metrics.OnchainRemoteForceCostSat * 1000
	}
	if metrics.KeysendReceivedMsat == 0 && metrics.KeysendReceivedSat != 0 {
		metrics.KeysendReceivedMsat = metrics.KeysendReceivedSat * 1000
	}
	if metrics.NetRoutingProfitMsat == 0 && metrics.NetRoutingProfitSat != 0 {
		metrics.NetRoutingProfitMsat = metrics.NetRoutingProfitSat * 1000
	}
	if metrics.NetWithKeysendMsat == 0 && metrics.NetWithKeysendSat != 0 {
		metrics.NetWithKeysendMsat = metrics.NetWithKeysendSat * 1000
	}
	if metrics.RoutedVolumeMsat == 0 && metrics.RoutedVolumeSat != 0 {
		metrics.RoutedVolumeMsat = metrics.RoutedVolumeSat * 1000
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
