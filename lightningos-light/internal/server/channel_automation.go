package server

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	channelAutomationModeNormal         = "normal"
	channelAutomationModeParked         = "parked"
	channelAutomationModeCloseCandidate = "close_candidate"
)

var errChannelAutomationParked = errors.New("channel is parked")

type ChannelAutomationPolicy struct {
	ChannelID                    uint64     `json:"channel_id"`
	ChannelPoint                 string     `json:"channel_point"`
	Mode                         string     `json:"automation_mode"`
	FixedFeePPM                  *int64     `json:"fixed_fee_ppm,omitempty"`
	ReviewAt                     *time.Time `json:"review_at,omitempty"`
	Note                         string     `json:"automation_note,omitempty"`
	PreviousAutofeeEnabled       *bool      `json:"previous_autofee_enabled,omitempty"`
	PreviousRebalanceAutoEnabled *bool      `json:"previous_rebalance_auto_enabled,omitempty"`
	PreviousManualRestartEnabled *bool      `json:"previous_manual_restart_enabled,omitempty"`
	PreviousExcludedAsSource     *bool      `json:"previous_excluded_as_source,omitempty"`
	ParkedAt                     *time.Time `json:"parked_at,omitempty"`
	UpdatedAt                    time.Time  `json:"automation_updated_at,omitempty"`
}

type setChannelAutomationParams struct {
	ChannelID       uint64
	ChannelPoint    string
	Mode            string
	FixedFeePPM     *int64
	ReviewAt        *time.Time
	Note            string
	RestorePrevious bool
}

func ensureChannelAutomationSchema(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return errors.New("db unavailable")
	}
	_, err := db.Exec(ctx, `
create table if not exists channel_automation_policies (
  channel_id bigint primary key,
  channel_point text not null,
  automation_mode text not null default 'normal',
  fixed_fee_ppm bigint,
  review_at timestamptz,
  note text not null default '',
  previous_autofee_enabled boolean,
  previous_rebalance_auto_enabled boolean,
  previous_manual_restart_enabled boolean,
  previous_excluded_as_source boolean,
  parked_at timestamptz,
  updated_at timestamptz not null default now()
);
create index if not exists channel_automation_policies_mode_idx on channel_automation_policies (automation_mode, updated_at desc);
alter table channel_automation_policies
  add column if not exists fixed_fee_ppm bigint,
  add column if not exists review_at timestamptz,
  add column if not exists note text not null default '',
  add column if not exists previous_autofee_enabled boolean,
  add column if not exists previous_rebalance_auto_enabled boolean,
  add column if not exists previous_manual_restart_enabled boolean,
  add column if not exists previous_excluded_as_source boolean,
  add column if not exists parked_at timestamptz,
  add column if not exists updated_at timestamptz not null default now();
`)
	return err
}

func normalizeChannelAutomationMode(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", channelAutomationModeNormal:
		return channelAutomationModeNormal
	case "park", channelAutomationModeParked:
		return channelAutomationModeParked
	case "close", "close-candidate", channelAutomationModeCloseCandidate:
		return channelAutomationModeCloseCandidate
	default:
		return ""
	}
}

func isChannelAutomationParked(mode string) bool {
	return normalizeChannelAutomationMode(mode) == channelAutomationModeParked
}

func loadChannelAutomationPolicies(ctx context.Context, db *pgxpool.Pool) (map[uint64]ChannelAutomationPolicy, error) {
	items := map[uint64]ChannelAutomationPolicy{}
	if db == nil {
		return items, errors.New("db unavailable")
	}
	rows, err := db.Query(ctx, `
select channel_id, channel_point, automation_mode, fixed_fee_ppm, review_at, note,
       previous_autofee_enabled, previous_rebalance_auto_enabled, previous_manual_restart_enabled,
       previous_excluded_as_source, parked_at, updated_at
from channel_automation_policies
`)
	if err != nil {
		return items, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanChannelAutomationPolicy(rows)
		if err != nil {
			return items, err
		}
		if item.ChannelID != 0 {
			items[item.ChannelID] = item
		}
	}
	return items, rows.Err()
}

func loadChannelAutomationPolicy(ctx context.Context, db *pgxpool.Pool, channelID uint64) (ChannelAutomationPolicy, bool, error) {
	if db == nil {
		return ChannelAutomationPolicy{}, false, errors.New("db unavailable")
	}
	row := db.QueryRow(ctx, `
select channel_id, channel_point, automation_mode, fixed_fee_ppm, review_at, note,
       previous_autofee_enabled, previous_rebalance_auto_enabled, previous_manual_restart_enabled,
       previous_excluded_as_source, parked_at, updated_at
from channel_automation_policies
where channel_id = $1
`, int64(channelID))
	item, err := scanChannelAutomationPolicy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChannelAutomationPolicy{}, false, nil
	}
	if err != nil {
		return ChannelAutomationPolicy{}, false, err
	}
	return item, true, nil
}

type channelAutomationScanner interface {
	Scan(dest ...any) error
}

func scanChannelAutomationPolicy(row channelAutomationScanner) (ChannelAutomationPolicy, error) {
	var (
		channelID        int64
		fixedFee         sql.NullInt64
		reviewAt         sql.NullTime
		previousAutofee  sql.NullBool
		previousAuto     sql.NullBool
		previousRestart  sql.NullBool
		previousExcluded sql.NullBool
		parkedAt         sql.NullTime
		updatedAt        sql.NullTime
		item             ChannelAutomationPolicy
	)
	if err := row.Scan(
		&channelID,
		&item.ChannelPoint,
		&item.Mode,
		&fixedFee,
		&reviewAt,
		&item.Note,
		&previousAutofee,
		&previousAuto,
		&previousRestart,
		&previousExcluded,
		&parkedAt,
		&updatedAt,
	); err != nil {
		return item, err
	}
	item.ChannelID = uint64(channelID)
	item.ChannelPoint = strings.TrimSpace(item.ChannelPoint)
	item.Mode = normalizeChannelAutomationMode(item.Mode)
	if item.Mode == "" {
		item.Mode = channelAutomationModeNormal
	}
	item.Note = strings.TrimSpace(item.Note)
	if fixedFee.Valid {
		value := fixedFee.Int64
		item.FixedFeePPM = &value
	}
	if reviewAt.Valid {
		value := reviewAt.Time.UTC()
		item.ReviewAt = &value
	}
	if previousAutofee.Valid {
		value := previousAutofee.Bool
		item.PreviousAutofeeEnabled = &value
	}
	if previousAuto.Valid {
		value := previousAuto.Bool
		item.PreviousRebalanceAutoEnabled = &value
	}
	if previousRestart.Valid {
		value := previousRestart.Bool
		item.PreviousManualRestartEnabled = &value
	}
	if previousExcluded.Valid {
		value := previousExcluded.Bool
		item.PreviousExcludedAsSource = &value
	}
	if parkedAt.Valid {
		value := parkedAt.Time.UTC()
		item.ParkedAt = &value
	}
	if updatedAt.Valid {
		item.UpdatedAt = updatedAt.Time.UTC()
	}
	return item, nil
}

func setChannelAutomationPolicy(ctx context.Context, db *pgxpool.Pool, params setChannelAutomationParams) (ChannelAutomationPolicy, error) {
	if db == nil {
		return ChannelAutomationPolicy{}, errors.New("db unavailable")
	}
	mode := normalizeChannelAutomationMode(params.Mode)
	if mode == "" {
		return ChannelAutomationPolicy{}, errors.New("invalid automation mode")
	}
	if params.ChannelID == 0 || strings.TrimSpace(params.ChannelPoint) == "" {
		return ChannelAutomationPolicy{}, errors.New("channel_id and channel_point required")
	}
	if params.FixedFeePPM != nil && *params.FixedFeePPM < 0 {
		return ChannelAutomationPolicy{}, errors.New("fixed_fee_ppm must be non-negative")
	}
	if err := ensureChannelAutomationSchema(ctx, db); err != nil {
		return ChannelAutomationPolicy{}, err
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return ChannelAutomationPolicy{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	existing, hasExisting, err := loadChannelAutomationPolicyTx(ctx, tx, params.ChannelID)
	if err != nil {
		return ChannelAutomationPolicy{}, err
	}

	prevAutofee := true
	prevAuto := false
	prevRestart := false
	prevExcluded := false
	if mode == channelAutomationModeParked && !(hasExisting && existing.Mode == channelAutomationModeParked) {
		prevAutofee = currentAutofeeEnabledTx(ctx, tx, params.ChannelID)
		prevAuto, prevRestart = currentRebalanceAutomationTx(ctx, tx, params.ChannelID)
		prevExcluded = currentSourceExcludedTx(ctx, tx, params.ChannelID)
	} else if hasExisting {
		prevAutofee = boolPtrValue(existing.PreviousAutofeeEnabled, prevAutofee)
		prevAuto = boolPtrValue(existing.PreviousRebalanceAutoEnabled, prevAuto)
		prevRestart = boolPtrValue(existing.PreviousManualRestartEnabled, prevRestart)
		prevExcluded = boolPtrValue(existing.PreviousExcludedAsSource, prevExcluded)
	}

	fixedFee := params.FixedFeePPM
	if mode != channelAutomationModeParked {
		fixedFee = nil
	}
	reviewAt := params.ReviewAt
	if mode != channelAutomationModeParked {
		reviewAt = nil
	}
	note := strings.TrimSpace(params.Note)
	if mode == channelAutomationModeNormal {
		note = ""
	} else if mode != channelAutomationModeParked && note == "" && hasExisting {
		note = existing.Note
	}

	_, err = tx.Exec(ctx, `
insert into channel_automation_policies (
  channel_id, channel_point, automation_mode, fixed_fee_ppm, review_at, note,
  previous_autofee_enabled, previous_rebalance_auto_enabled, previous_manual_restart_enabled,
  previous_excluded_as_source, parked_at, updated_at
) values (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
  case when $3 = 'parked' then coalesce($11, now()) else null end,
  now()
)
on conflict (channel_id) do update set
  channel_point = excluded.channel_point,
  automation_mode = excluded.automation_mode,
  fixed_fee_ppm = excluded.fixed_fee_ppm,
  review_at = excluded.review_at,
  note = excluded.note,
  previous_autofee_enabled = excluded.previous_autofee_enabled,
  previous_rebalance_auto_enabled = excluded.previous_rebalance_auto_enabled,
  previous_manual_restart_enabled = excluded.previous_manual_restart_enabled,
  previous_excluded_as_source = excluded.previous_excluded_as_source,
  parked_at = excluded.parked_at,
  updated_at = excluded.updated_at
`, int64(params.ChannelID), strings.TrimSpace(params.ChannelPoint), mode, nullableInt64(fixedFee), channelAutomationNullableTime(reviewAt), note,
		prevAutofee, prevAuto, prevRestart, prevExcluded, channelAutomationNullableTime(existing.ParkedAt))
	if err != nil {
		return ChannelAutomationPolicy{}, err
	}

	switch mode {
	case channelAutomationModeParked:
		if err := applyParkedAutomationTx(ctx, tx, params.ChannelID, params.ChannelPoint); err != nil {
			return ChannelAutomationPolicy{}, err
		}
	default:
		restoreAutofee := false
		restoreAuto := false
		restoreRestart := false
		if params.RestorePrevious {
			restoreAutofee = prevAutofee
			restoreAuto = prevAuto
			restoreRestart = prevRestart
		}
		if err := applyUnparkedAutomationTx(ctx, tx, params.ChannelID, params.ChannelPoint, restoreAutofee, restoreAuto, restoreRestart, prevExcluded); err != nil {
			return ChannelAutomationPolicy{}, err
		}
	}

	policy, _, err := loadChannelAutomationPolicyTx(ctx, tx, params.ChannelID)
	if err != nil {
		return ChannelAutomationPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ChannelAutomationPolicy{}, err
	}
	return policy, nil
}

func loadChannelAutomationPolicyTx(ctx context.Context, tx pgx.Tx, channelID uint64) (ChannelAutomationPolicy, bool, error) {
	row := tx.QueryRow(ctx, `
select channel_id, channel_point, automation_mode, fixed_fee_ppm, review_at, note,
       previous_autofee_enabled, previous_rebalance_auto_enabled, previous_manual_restart_enabled,
       previous_excluded_as_source, parked_at, updated_at
from channel_automation_policies
where channel_id = $1
`, int64(channelID))
	item, err := scanChannelAutomationPolicy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChannelAutomationPolicy{}, false, nil
	}
	if err != nil {
		return ChannelAutomationPolicy{}, false, err
	}
	return item, true, nil
}

func currentAutofeeEnabledTx(ctx context.Context, tx pgx.Tx, channelID uint64) bool {
	var enabled bool
	err := tx.QueryRow(ctx, `select enabled from autofee_channel_settings where channel_id = $1`, int64(channelID)).Scan(&enabled)
	if err != nil {
		return true
	}
	return enabled
}

func currentRebalanceAutomationTx(ctx context.Context, tx pgx.Tx, channelID uint64) (bool, bool) {
	var autoEnabled, manualRestart bool
	err := tx.QueryRow(ctx, `select auto_enabled, manual_restart_enabled from rebalance_channel_settings where channel_id = $1`, int64(channelID)).Scan(&autoEnabled, &manualRestart)
	if err != nil {
		return false, false
	}
	return autoEnabled, manualRestart
}

func currentSourceExcludedTx(ctx context.Context, tx pgx.Tx, channelID uint64) bool {
	var excluded bool
	err := tx.QueryRow(ctx, `select exists(select 1 from rebalance_source_exclusions where channel_id = $1)`, int64(channelID)).Scan(&excluded)
	return err == nil && excluded
}

func applyParkedAutomationTx(ctx context.Context, tx pgx.Tx, channelID uint64, channelPoint string) error {
	if _, err := tx.Exec(ctx, `
insert into autofee_channel_settings (channel_id, channel_point, enabled, updated_at)
values ($1, $2, false, now())
on conflict (channel_id) do update set channel_point=excluded.channel_point, enabled=false, updated_at=now()
`, int64(channelID), strings.TrimSpace(channelPoint)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
insert into rebalance_channel_settings (channel_id, channel_point, target_outbound_pct, auto_enabled, manual_restart_enabled, updated_at)
values ($1, $2, $3, false, false, now())
on conflict (channel_id) do update set channel_point=excluded.channel_point, auto_enabled=false, manual_restart_enabled=false, updated_at=now()
`, int64(channelID), strings.TrimSpace(channelPoint), rebalanceDefaultTargetOutboundPct); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
insert into rebalance_source_exclusions (channel_id, channel_point, reason)
values ($1, $2, 'parked')
on conflict (channel_id) do update set channel_point=excluded.channel_point, reason='parked'
`, int64(channelID), strings.TrimSpace(channelPoint))
	return err
}

func applyUnparkedAutomationTx(ctx context.Context, tx pgx.Tx, channelID uint64, channelPoint string, autofeeEnabled bool, autoEnabled bool, manualRestart bool, excluded bool) error {
	if autoEnabled && manualRestart {
		manualRestart = false
	}
	if _, err := tx.Exec(ctx, `
insert into autofee_channel_settings (channel_id, channel_point, enabled, updated_at)
values ($1, $2, $3, now())
on conflict (channel_id) do update set channel_point=excluded.channel_point, enabled=excluded.enabled, updated_at=now()
`, int64(channelID), strings.TrimSpace(channelPoint), autofeeEnabled); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
insert into rebalance_channel_settings (channel_id, channel_point, target_outbound_pct, auto_enabled, manual_restart_enabled, updated_at)
values ($1, $2, $3, $4, $5, now())
on conflict (channel_id) do update set channel_point=excluded.channel_point, auto_enabled=excluded.auto_enabled, manual_restart_enabled=excluded.manual_restart_enabled, updated_at=now()
`, int64(channelID), strings.TrimSpace(channelPoint), rebalanceDefaultTargetOutboundPct, autoEnabled, manualRestart); err != nil {
		return err
	}
	if excluded {
		_, err := tx.Exec(ctx, `
insert into rebalance_source_exclusions (channel_id, channel_point, reason)
values ($1, $2, 'restored')
on conflict (channel_id) do update set channel_point=excluded.channel_point, reason=excluded.reason
`, int64(channelID), strings.TrimSpace(channelPoint))
		return err
	}
	_, err := tx.Exec(ctx, `delete from rebalance_source_exclusions where channel_id = $1`, int64(channelID))
	return err
}

func boolPtrValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func channelAutomationNullableTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC()
}
