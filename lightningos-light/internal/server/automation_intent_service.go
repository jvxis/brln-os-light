package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	automationIntentModeOff     = "off"
	automationIntentModeShadow  = "shadow"
	automationIntentModeEnforce = "enforce"

	automationIntentProducerAutofee   = "autofee"
	automationIntentProducerRebalance = "rebalance"

	automationIntentKindRefillTarget    = "refill_target"
	automationIntentKindProtectFeeFloor = "protect_fee_floor"
)

type AutomationIntentConfig struct {
	Mode                  string  `json:"mode"`
	RefillTargetTTLSec    int64   `json:"refill_target_ttl_sec"`
	ProtectFeeFloorTTLSec int64   `json:"protect_fee_floor_ttl_sec"`
	RefillScoreMultiplier float64 `json:"refill_score_multiplier"`
	MinConfidence         float64 `json:"min_confidence"`
	HistoryRetentionDays  int     `json:"history_retention_days"`
}

type AutomationIntentConfigUpdate struct {
	Mode                  *string  `json:"mode,omitempty"`
	RefillScoreMultiplier *float64 `json:"refill_score_multiplier,omitempty"`
	MinConfidence         *float64 `json:"min_confidence,omitempty"`
}

type AutomationIntent struct {
	ID                     int64          `json:"id"`
	ChannelID              uint64         `json:"channel_id"`
	ChannelIDStr           string         `json:"channel_id_str,omitempty"`
	ChannelPoint           string         `json:"channel_point,omitempty"`
	Producer               string         `json:"producer"`
	Consumer               string         `json:"consumer"`
	Kind                   string         `json:"kind"`
	Confidence             float64        `json:"confidence"`
	ReasonCode             string         `json:"reason_code"`
	Evidence               map[string]any `json:"evidence,omitempty"`
	ScoreMultiplier        float64        `json:"score_multiplier,omitempty"`
	FeeFloorPPM            int64          `json:"fee_floor_ppm,omitempty"`
	SourceRunID            string         `json:"source_run_id,omitempty"`
	SourceJobID            int64          `json:"source_job_id,omitempty"`
	ProducerProfile        string         `json:"producer_profile,omitempty"`
	ProducerNodeClass      string         `json:"producer_node_class,omitempty"`
	ProducerLiquidityClass string         `json:"producer_liquidity_class,omitempty"`
	Active                 bool           `json:"active"`
	FirstSeenAt            time.Time      `json:"first_seen_at"`
	LastSeenAt             time.Time      `json:"last_seen_at"`
	ExpiresAt              time.Time      `json:"expires_at"`
	ResolvedAt             *time.Time     `json:"resolved_at,omitempty"`
}

type AutomationIntentEvent struct {
	ID           int64          `json:"id"`
	IntentID     int64          `json:"intent_id,omitempty"`
	ChannelID    uint64         `json:"channel_id"`
	ChannelIDStr string         `json:"channel_id_str,omitempty"`
	Producer     string         `json:"producer"`
	Consumer     string         `json:"consumer"`
	Kind         string         `json:"kind"`
	EventType    string         `json:"event_type"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	OccurredAt   time.Time      `json:"occurred_at"`
}

type AutomationIntentService struct {
	db     *pgxpool.Pool
	logger loggerLike
}

func NewAutomationIntentService(db *pgxpool.Pool, logger loggerLike) *AutomationIntentService {
	return &AutomationIntentService{db: db, logger: logger}
}

func defaultAutomationIntentConfig() AutomationIntentConfig {
	return AutomationIntentConfig{
		Mode:                  automationIntentModeOff,
		RefillTargetTTLSec:    int64((6 * time.Hour) / time.Second),
		ProtectFeeFloorTTLSec: int64((6 * time.Hour) / time.Second),
		RefillScoreMultiplier: 1.20,
		MinConfidence:         0.70,
		HistoryRetentionDays:  30,
	}
}

func normalizeAutomationIntentMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case automationIntentModeShadow:
		return automationIntentModeShadow
	case automationIntentModeEnforce:
		return automationIntentModeEnforce
	default:
		return automationIntentModeOff
	}
}

func (s *AutomationIntentService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("automation intent db unavailable")
	}
	_, err := s.db.Exec(ctx, `
create table if not exists automation_interlock_config (
  id smallint primary key check (id = 1),
  mode text not null default 'off',
  refill_target_ttl_sec bigint not null default 21600,
  protect_fee_floor_ttl_sec bigint not null default 21600,
  refill_score_multiplier double precision not null default 1.20,
  min_confidence double precision not null default 0.70,
  history_retention_days integer not null default 30,
  updated_at timestamptz not null default now()
);
insert into automation_interlock_config (id) values (1) on conflict (id) do nothing;

create table if not exists automation_channel_intents (
  id bigserial primary key,
  channel_id bigint not null,
  channel_point text not null default '',
  producer text not null,
  consumer text not null,
  kind text not null,
  confidence double precision not null default 0,
  reason_code text not null default '',
  evidence jsonb not null default '{}'::jsonb,
  score_multiplier double precision not null default 0,
  fee_floor_ppm bigint not null default 0,
  source_run_id text not null default '',
  source_job_id bigint not null default 0,
  producer_profile text not null default '',
  producer_node_class text not null default '',
  producer_liquidity_class text not null default '',
  active boolean not null default true,
  first_seen_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  expires_at timestamptz not null,
  resolved_at timestamptz,
  updated_at timestamptz not null default now(),
  unique (channel_id, producer, consumer, kind)
);
create index if not exists automation_channel_intents_consumer_active_idx
  on automation_channel_intents (consumer, active, expires_at desc);
create index if not exists automation_channel_intents_channel_idx
  on automation_channel_intents (channel_id, updated_at desc);

create table if not exists automation_intent_events (
  id bigserial primary key,
  intent_id bigint,
  channel_id bigint not null,
  producer text not null,
  consumer text not null,
  kind text not null,
  event_type text not null,
  metadata jsonb not null default '{}'::jsonb,
  occurred_at timestamptz not null default now()
);
create index if not exists automation_intent_events_occurred_idx
  on automation_intent_events (occurred_at desc);
create index if not exists automation_intent_events_channel_idx
  on automation_intent_events (channel_id, occurred_at desc);
`)
	return err
}

func (s *AutomationIntentService) GetConfig(ctx context.Context) (AutomationIntentConfig, error) {
	cfg := defaultAutomationIntentConfig()
	if s == nil || s.db == nil {
		return cfg, errors.New("automation intent db unavailable")
	}
	err := s.db.QueryRow(ctx, `
select mode, refill_target_ttl_sec, protect_fee_floor_ttl_sec,
       refill_score_multiplier, min_confidence, history_retention_days
from automation_interlock_config where id=1
`).Scan(&cfg.Mode, &cfg.RefillTargetTTLSec, &cfg.ProtectFeeFloorTTLSec,
		&cfg.RefillScoreMultiplier, &cfg.MinConfidence, &cfg.HistoryRetentionDays)
	cfg.Mode = normalizeAutomationIntentMode(cfg.Mode)
	return cfg, err
}

func validateAutomationIntentConfigUpdate(update AutomationIntentConfigUpdate) error {
	if update.Mode != nil {
		value := strings.ToLower(strings.TrimSpace(*update.Mode))
		if value != automationIntentModeOff && value != automationIntentModeShadow && value != automationIntentModeEnforce {
			return fmt.Errorf("invalid mode %q", *update.Mode)
		}
	}
	if update.RefillScoreMultiplier != nil {
		value := *update.RefillScoreMultiplier
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 1 || value > 1.5 {
			return errors.New("refill_score_multiplier must be between 1 and 1.5")
		}
	}
	if update.MinConfidence != nil {
		value := *update.MinConfidence
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0.5 || value > 1 {
			return errors.New("min_confidence must be between 0.5 and 1")
		}
	}
	return nil
}

func (s *AutomationIntentService) UpdateConfig(ctx context.Context, update AutomationIntentConfigUpdate) (AutomationIntentConfig, error) {
	if err := validateAutomationIntentConfigUpdate(update); err != nil {
		return AutomationIntentConfig{}, err
	}
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return AutomationIntentConfig{}, err
	}
	if update.Mode != nil {
		cfg.Mode = normalizeAutomationIntentMode(*update.Mode)
	}
	if update.RefillScoreMultiplier != nil {
		cfg.RefillScoreMultiplier = *update.RefillScoreMultiplier
	}
	if update.MinConfidence != nil {
		cfg.MinConfidence = *update.MinConfidence
	}
	_, err = s.db.Exec(ctx, `
update automation_interlock_config
set mode=$1, refill_score_multiplier=$2, min_confidence=$3, updated_at=now()
where id=1`, cfg.Mode, cfg.RefillScoreMultiplier, cfg.MinConfidence)
	return cfg, err
}

func intentEvidenceJSON(evidence map[string]any) []byte {
	if len(evidence) == 0 {
		return []byte(`{}`)
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}

func (s *AutomationIntentService) SyncProducerKind(ctx context.Context, producer, consumer, kind, runID string, now time.Time, desired []AutomationIntent) error {
	if s == nil || s.db == nil {
		return errors.New("automation intent db unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, intent := range desired {
		if intent.ChannelID == 0 || intent.ExpiresAt.IsZero() {
			continue
		}
		var intentID int64
		var wasActive bool
		var previousReason string
		err := tx.QueryRow(ctx, `
select id, active, reason_code
from automation_channel_intents
where channel_id=$1 and producer=$2 and consumer=$3 and kind=$4
`, int64(intent.ChannelID), producer, consumer, kind).Scan(&intentID, &wasActive, &previousReason)
		eventType := "refreshed"
		if errors.Is(err, pgx.ErrNoRows) {
			eventType = "published"
		} else if err != nil {
			return err
		} else if !wasActive || previousReason != intent.ReasonCode {
			eventType = "published"
		}
		err = tx.QueryRow(ctx, `
insert into automation_channel_intents (
  channel_id, channel_point, producer, consumer, kind, confidence, reason_code,
  evidence, score_multiplier, fee_floor_ppm, source_run_id, source_job_id,
  producer_profile, producer_node_class, producer_liquidity_class,
  active, first_seen_at, last_seen_at, expires_at, resolved_at, updated_at
) values ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11,$12,$13,$14,$15,true,$16,$16,$17,null,$16)
on conflict (channel_id, producer, consumer, kind) do update set
  channel_point=excluded.channel_point,
  confidence=excluded.confidence,
  reason_code=excluded.reason_code,
  evidence=excluded.evidence,
  score_multiplier=excluded.score_multiplier,
  fee_floor_ppm=excluded.fee_floor_ppm,
  source_run_id=excluded.source_run_id,
  source_job_id=excluded.source_job_id,
  producer_profile=excluded.producer_profile,
  producer_node_class=excluded.producer_node_class,
  producer_liquidity_class=excluded.producer_liquidity_class,
  active=true,
  first_seen_at=case when automation_channel_intents.active then automation_channel_intents.first_seen_at else excluded.first_seen_at end,
  last_seen_at=excluded.last_seen_at,
  expires_at=excluded.expires_at,
  resolved_at=null,
  updated_at=excluded.updated_at
returning id
`, int64(intent.ChannelID), intent.ChannelPoint, producer, consumer, kind,
			intent.Confidence, intent.ReasonCode, intentEvidenceJSON(intent.Evidence), intent.ScoreMultiplier,
			intent.FeeFloorPPM, runID, intent.SourceJobID, intent.ProducerProfile,
			intent.ProducerNodeClass, intent.ProducerLiquidityClass, now, intent.ExpiresAt).Scan(&intentID)
		if err != nil {
			return err
		}
		if eventType == "published" {
			if _, err := tx.Exec(ctx, `
insert into automation_intent_events (intent_id, channel_id, producer, consumer, kind, event_type, metadata, occurred_at)
values ($1,$2,$3,$4,$5,$6,$7::jsonb,$8)
`, intentID, int64(intent.ChannelID), producer, consumer, kind, eventType,
				intentEvidenceJSON(map[string]any{"reason_code": intent.ReasonCode, "confidence": intent.Confidence}), now); err != nil {
				return err
			}
		}
	}
	rows, err := tx.Query(ctx, `
update automation_channel_intents
set active=false, resolved_at=$5, updated_at=$5
where producer=$1 and consumer=$2 and kind=$3 and active=true
  and source_run_id <> $4
returning id, channel_id
`, producer, consumer, kind, runID, now)
	if err != nil {
		return err
	}
	resolved := [][2]int64{}
	for rows.Next() {
		var intentID, channelID int64
		if err := rows.Scan(&intentID, &channelID); err != nil {
			rows.Close()
			return err
		}
		resolved = append(resolved, [2]int64{intentID, channelID})
	}
	rows.Close()
	for _, item := range resolved {
		if _, err := tx.Exec(ctx, `
insert into automation_intent_events (intent_id, channel_id, producer, consumer, kind, event_type, occurred_at)
values ($1,$2,$3,$4,$5,'resolved',$6)
`, item[0], item[1], producer, consumer, kind, now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *AutomationIntentService) UpsertIntent(ctx context.Context, intent AutomationIntent, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("automation intent db unavailable")
	}
	if intent.ChannelID == 0 || intent.ExpiresAt.IsZero() {
		return errors.New("invalid automation intent")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var intentID int64
	err := s.db.QueryRow(ctx, `
insert into automation_channel_intents (
  channel_id, channel_point, producer, consumer, kind, confidence, reason_code,
  evidence, score_multiplier, fee_floor_ppm, source_run_id, source_job_id,
  producer_profile, producer_node_class, producer_liquidity_class,
  active, first_seen_at, last_seen_at, expires_at, resolved_at, updated_at
) values ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11,$12,$13,$14,$15,true,$16,$16,$17,null,$16)
on conflict (channel_id, producer, consumer, kind) do update set
  channel_point=excluded.channel_point,
  confidence=excluded.confidence,
  reason_code=excluded.reason_code,
  evidence=excluded.evidence,
  score_multiplier=excluded.score_multiplier,
  fee_floor_ppm=excluded.fee_floor_ppm,
  source_run_id=excluded.source_run_id,
  source_job_id=excluded.source_job_id,
  producer_profile=excluded.producer_profile,
  producer_node_class=excluded.producer_node_class,
  producer_liquidity_class=excluded.producer_liquidity_class,
  active=true,
  first_seen_at=case when automation_channel_intents.active then automation_channel_intents.first_seen_at else excluded.first_seen_at end,
  last_seen_at=excluded.last_seen_at,
  expires_at=excluded.expires_at,
  resolved_at=null,
  updated_at=excluded.updated_at
returning id
`, int64(intent.ChannelID), intent.ChannelPoint, intent.Producer, intent.Consumer, intent.Kind,
		intent.Confidence, intent.ReasonCode, intentEvidenceJSON(intent.Evidence), intent.ScoreMultiplier,
		intent.FeeFloorPPM, intent.SourceRunID, intent.SourceJobID, intent.ProducerProfile,
		intent.ProducerNodeClass, intent.ProducerLiquidityClass, now, intent.ExpiresAt).Scan(&intentID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
insert into automation_intent_events (intent_id, channel_id, producer, consumer, kind, event_type, metadata, occurred_at)
values ($1,$2,$3,$4,$5,'published',$6::jsonb,$7)
`, intentID, int64(intent.ChannelID), intent.Producer, intent.Consumer, intent.Kind,
		intentEvidenceJSON(map[string]any{"reason_code": intent.ReasonCode, "confidence": intent.Confidence}), now)
	return err
}

func (s *AutomationIntentService) ActiveForConsumer(ctx context.Context, consumer string, now time.Time) (map[uint64][]AutomationIntent, error) {
	items, err := s.List(ctx, consumer, "", true, 1000, now)
	if err != nil {
		return nil, err
	}
	out := make(map[uint64][]AutomationIntent)
	for _, item := range items {
		out[item.ChannelID] = append(out[item.ChannelID], item)
	}
	return out, nil
}

func (s *AutomationIntentService) List(ctx context.Context, consumer, kind string, activeOnly bool, limit int, now time.Time) ([]AutomationIntent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("automation intent db unavailable")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows, err := s.db.Query(ctx, `
select id, channel_id, channel_point, producer, consumer, kind, confidence,
       reason_code, evidence, score_multiplier, fee_floor_ppm, source_run_id,
       source_job_id, producer_profile, producer_node_class, producer_liquidity_class,
       active, first_seen_at, last_seen_at, expires_at, resolved_at
from automation_channel_intents
where ($1='' or consumer=$1)
  and ($2='' or kind=$2)
  and (not $3 or (active=true and expires_at > $4))
order by active desc, updated_at desc
limit $5
`, consumer, kind, activeOnly, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutomationIntent{}
	for rows.Next() {
		var item AutomationIntent
		var channelID int64
		var evidenceRaw []byte
		if err := rows.Scan(&item.ID, &channelID, &item.ChannelPoint, &item.Producer,
			&item.Consumer, &item.Kind, &item.Confidence, &item.ReasonCode, &evidenceRaw,
			&item.ScoreMultiplier, &item.FeeFloorPPM, &item.SourceRunID, &item.SourceJobID,
			&item.ProducerProfile, &item.ProducerNodeClass, &item.ProducerLiquidityClass,
			&item.Active, &item.FirstSeenAt, &item.LastSeenAt, &item.ExpiresAt, &item.ResolvedAt); err != nil {
			return nil, err
		}
		if channelID != 0 {
			item.ChannelID = uint64(channelID)
			item.ChannelIDStr = strconv.FormatUint(item.ChannelID, 10)
		}
		_ = json.Unmarshal(evidenceRaw, &item.Evidence)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *AutomationIntentService) RecordApplied(ctx context.Context, intent AutomationIntent, metadata map[string]any, now time.Time) error {
	if s == nil || s.db == nil || intent.ID <= 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.db.Exec(ctx, `
insert into automation_intent_events (intent_id, channel_id, producer, consumer, kind, event_type, metadata, occurred_at)
values ($1,$2,$3,$4,$5,'applied',$6::jsonb,$7)
`, intent.ID, int64(intent.ChannelID), intent.Producer, intent.Consumer, intent.Kind,
		intentEvidenceJSON(metadata), now)
	return err
}

func (s *AutomationIntentService) History(ctx context.Context, limit int) ([]AutomationIntentEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("automation intent db unavailable")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
select id, coalesce(intent_id,0), channel_id, producer, consumer, kind, event_type, metadata, occurred_at
from automation_intent_events
order by occurred_at desc, id desc
limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutomationIntentEvent{}
	for rows.Next() {
		var item AutomationIntentEvent
		var channelID int64
		var metadataRaw []byte
		if err := rows.Scan(&item.ID, &item.IntentID, &channelID, &item.Producer,
			&item.Consumer, &item.Kind, &item.EventType, &metadataRaw, &item.OccurredAt); err != nil {
			return nil, err
		}
		if channelID != 0 {
			item.ChannelID = uint64(channelID)
			item.ChannelIDStr = strconv.FormatUint(item.ChannelID, 10)
		}
		_ = json.Unmarshal(metadataRaw, &item.Metadata)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *AutomationIntentService) Prune(ctx context.Context) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return
	}
	if cfg.HistoryRetentionDays <= 0 {
		cfg.HistoryRetentionDays = 30
	}
	_, _ = s.db.Exec(ctx, `delete from automation_intent_events where occurred_at < now() - ($1 * interval '1 day')`, cfg.HistoryRetentionDays)
	_, _ = s.db.Exec(ctx, `
update automation_channel_intents
set active=false, resolved_at=coalesce(resolved_at, expires_at), updated_at=now()
where active=true and expires_at <= now()`)
}
