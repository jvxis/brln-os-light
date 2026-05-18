package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

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
