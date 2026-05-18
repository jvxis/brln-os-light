package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAuditDBUnavailable = errors.New("audit db unavailable")

type AuditService struct {
	db     *pgxpool.Pool
	logger *log.Logger
}

type AuditEventInsert struct {
	SessionID string
	Action    string
	Target    string
	IP        string
	Metadata  any
}

type AuditEventFilter struct {
	Action    string
	SessionID string
	Target    string
	Limit     int
}

type AuditEvent struct {
	ID        int64           `json:"id"`
	Ts        time.Time       `json:"ts"`
	SessionID string          `json:"session_id"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	Metadata  json.RawMessage `json:"metadata"`
	IP        string          `json:"ip,omitempty"`
}

func NewAuditService(db *pgxpool.Pool, logger *log.Logger) *AuditService {
	return &AuditService{db: db, logger: logger}
}

func (s *AuditService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrAuditDBUnavailable
	}
	_, err := s.db.Exec(ctx, `
create table if not exists audit_events (
  id bigserial primary key,
  ts timestamptz not null default now(),
  session_id text not null default '',
  action text not null,
  target text not null default '',
  metadata jsonb not null default '{}'::jsonb,
  ip text not null default ''
);

create index if not exists audit_events_ts_idx on audit_events (ts desc);
create index if not exists audit_events_session_idx on audit_events (session_id, ts desc);
create index if not exists audit_events_action_idx on audit_events (action, ts desc);
`)
	return err
}

func (s *AuditService) Insert(ctx context.Context, event AuditEventInsert) error {
	if s == nil || s.db == nil {
		return ErrAuditDBUnavailable
	}
	action := strings.TrimSpace(event.Action)
	if action == "" {
		return errors.New("audit action required")
	}
	sessionID := strings.TrimSpace(event.SessionID)
	target := strings.TrimSpace(event.Target)
	ip := strings.TrimSpace(event.IP)

	raw, err := json.Marshal(event.Metadata)
	if err != nil {
		return err
	}
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}

	_, err = s.db.Exec(ctx, `
insert into audit_events (session_id, action, target, metadata, ip)
values ($1, $2, $3, $4::jsonb, $5)
`, sessionID, action, target, string(raw), ip)
	return err
}

func (s *AuditService) List(ctx context.Context, filter AuditEventFilter) ([]AuditEvent, error) {
	if s == nil || s.db == nil {
		return nil, ErrAuditDBUnavailable
	}
	limit := normalizeAuditEventsLimit(filter.Limit)

	where := []string{"true"}
	args := []any{}
	addFilter := func(column string, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		args = append(args, value)
		where = append(where, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	addFilter("action", filter.Action)
	addFilter("session_id", filter.SessionID)
	addFilter("target", filter.Target)

	args = append(args, limit)
	query := fmt.Sprintf(`
select id, ts, session_id, action, target, metadata, ip
from audit_events
where %s
order by ts desc, id desc
limit $%d`, strings.Join(where, " and "), len(args))

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]AuditEvent, 0, limit)
	for rows.Next() {
		var item AuditEvent
		var raw []byte
		if err := rows.Scan(&item.ID, &item.Ts, &item.SessionID, &item.Action, &item.Target, &raw, &item.IP); err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			raw = []byte("{}")
		}
		item.Metadata = append(json.RawMessage(nil), raw...)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *AuditService) PruneBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, ErrAuditDBUnavailable
	}
	if cutoff.IsZero() {
		return 0, nil
	}
	tag, err := s.db.Exec(ctx, `delete from audit_events where ts < $1`, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
