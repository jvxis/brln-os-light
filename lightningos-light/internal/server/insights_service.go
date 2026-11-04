package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	insightDoneHideWindow  = 7 * 24 * time.Hour
	insightSnoozeWindow    = 24 * time.Hour
	insightDismissLater    = "later"
	insightDismissDisagree = "disagree"
	insightDismissDoneByMe = "done_manually"
	insightsCacheTTL       = 5 * time.Minute
	insightsFollowedWindow = 30 * 24 * time.Hour
)

var errInsightNotFound = errors.New("insight not found")

// InsightsService persists only what the rule engine cannot recompute: the
// operator's decision on each recommendation (dismissed, done) and when it
// was first and last seen. Everything else is rebuilt on every read.
type InsightsService struct {
	db     *pgxpool.Pool
	logger *log.Logger
}

type insightRow struct {
	Status        string
	DismissReason string
	FirstSeenAt   time.Time
	LastSeenAt    time.Time
	ResolvedAt    *time.Time
	SnoozeUntil   *time.Time
}

func NewInsightsService(db *pgxpool.Pool, logger *log.Logger) *InsightsService {
	return &InsightsService{db: db, logger: logger}
}

func (s *InsightsService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("insights database unavailable")
	}
	_, err := s.db.Exec(ctx, `
create table if not exists insights_recommendations (
    id text primary key,
    kind text not null,
    rule text not null,
    channel_point text,
    peer_pubkey text,
    status text not null default 'open',
    dismiss_reason text,
    first_seen_at timestamptz not null,
    last_seen_at timestamptz not null,
    resolved_at timestamptz,
    snooze_until timestamptz,
    snapshot jsonb,
    outcome jsonb
);
create index if not exists insights_recommendations_status_idx on insights_recommendations (status, last_seen_at desc);
`)
	return err
}

// Reconcile merges the freshly computed list with the stored decisions and
// marks stored recommendations that no longer apply as expired.
func (s *InsightsService) Reconcile(ctx context.Context, items []Insight, now time.Time) ([]Insight, error) {
	if s == nil || s.db == nil {
		return items, nil
	}
	rows, err := s.db.Query(ctx, `select id, status, coalesce(dismiss_reason,''), first_seen_at, last_seen_at, resolved_at, snooze_until from insights_recommendations where status <> 'expired'`)
	if err != nil {
		return items, err
	}
	stored := map[string]insightRow{}
	for rows.Next() {
		var id string
		var row insightRow
		if err := rows.Scan(&id, &row.Status, &row.DismissReason, &row.FirstSeenAt, &row.LastSeenAt, &row.ResolvedAt, &row.SnoozeUntil); err != nil {
			rows.Close()
			return items, err
		}
		stored[id] = row
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return items, err
	}

	seen := make(map[string]bool, len(items))
	batch := &pgx.Batch{}
	for i := range items {
		item := &items[i]
		seen[item.ID] = true
		if row, ok := stored[item.ID]; ok {
			item.FirstSeenAt = row.FirstSeenAt
			item.Status, item.DismissReason, item.ResolvedAt = insightEffectiveStatus(row, now)
		}
		snapshot, _ := json.Marshal(item)
		batch.Queue(`
insert into insights_recommendations (id, kind, rule, channel_point, peer_pubkey, status, dismiss_reason, first_seen_at, last_seen_at, resolved_at, snapshot)
values ($1,$2,$3,$4,$5,$6,nullif($7,''),$8,$9,$10,$11)
on conflict (id) do update set
    kind = excluded.kind,
    rule = excluded.rule,
    channel_point = excluded.channel_point,
    peer_pubkey = excluded.peer_pubkey,
    status = excluded.status,
    dismiss_reason = excluded.dismiss_reason,
    last_seen_at = excluded.last_seen_at,
    resolved_at = excluded.resolved_at,
    snapshot = excluded.snapshot`,
			item.ID, item.Kind, item.Rule, nullIfEmpty(item.ChannelPoint), nullIfEmpty(item.PeerPubkey), item.Status, item.DismissReason, item.FirstSeenAt, now, item.ResolvedAt, snapshot)
	}
	for id, row := range stored {
		if seen[id] || row.Status == insightStatusExpired {
			continue
		}
		batch.Queue(`update insights_recommendations set status = 'expired', resolved_at = coalesce(resolved_at, $2) where id = $1`, id, now)
	}
	if batch.Len() > 0 {
		results := s.db.SendBatch(ctx, batch)
		for i := 0; i < batch.Len(); i++ {
			if _, err := results.Exec(); err != nil {
				results.Close()
				return items, err
			}
		}
		if err := results.Close(); err != nil {
			return items, err
		}
	}
	return items, nil
}

// insightEffectiveStatus decides what a stored decision still means now: a
// snooze runs out after a day, a "done" hides the card for a week and then
// lets it come back if the condition is still there.
func insightEffectiveStatus(row insightRow, now time.Time) (string, string, *time.Time) {
	switch row.Status {
	case insightStatusDismissed:
		if row.DismissReason == insightDismissLater {
			if row.SnoozeUntil != nil && now.Before(*row.SnoozeUntil) {
				return row.Status, row.DismissReason, row.ResolvedAt
			}
			return insightStatusOpen, "", nil
		}
		return row.Status, row.DismissReason, row.ResolvedAt
	case insightStatusDone:
		if row.ResolvedAt != nil && now.Sub(*row.ResolvedAt) < insightDoneHideWindow {
			return row.Status, row.DismissReason, row.ResolvedAt
		}
		return insightStatusOpen, "", nil
	default:
		return insightStatusOpen, "", nil
	}
}

// SetStatus records the operator's decision. Only recommendations that the
// engine produced at least once can be acted on.
func (s *InsightsService) SetStatus(ctx context.Context, id, status, reason string, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("insights database unavailable")
	}
	id = strings.TrimSpace(id)
	var snooze *time.Time
	var resolved *time.Time
	switch status {
	case insightStatusDismissed:
		switch reason {
		case insightDismissLater:
			until := now.Add(insightSnoozeWindow)
			snooze = &until
		case insightDismissDisagree, insightDismissDoneByMe:
		default:
			return errors.New("invalid dismiss reason")
		}
		resolved = &now
	case insightStatusDone:
		reason = ""
		resolved = &now
	case insightStatusOpen:
		reason = ""
	default:
		return errors.New("invalid status")
	}
	tag, err := s.db.Exec(ctx, `update insights_recommendations set status = $2, dismiss_reason = nullif($3,''), resolved_at = $4, snooze_until = $5 where id = $1`,
		id, status, reason, resolved, snooze)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errInsightNotFound
	}
	return nil
}

// FollowedSince counts recommendations the operator marked as done in the
// window. The before/after measurement of each one is phase 2.
func (s *InsightsService) FollowedSince(ctx context.Context, since time.Time) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var n int
	err := s.db.QueryRow(ctx, `select count(*) from insights_recommendations where status = 'done' and resolved_at >= $1`, since).Scan(&n)
	return n, err
}

func nullIfEmpty(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
