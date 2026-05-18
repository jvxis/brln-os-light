package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUtxoManagerDBUnavailable = errors.New("utxo manager db unavailable")

// UtxoMetadata is the local-only annotation we keep for a wallet UTXO. LND has
// no per-output label, so this lives in our DB and is keyed by "txid:vout".
type UtxoMetadata struct {
	Outpoint  string    `json:"outpoint"`
	Label     string    `json:"label"`
	Tag       string    `json:"tag"`
	Color     string    `json:"color"`
	GroupID   string    `json:"group_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UtxoGroup is a user-defined bundle of UTXOs that travel together in the UI.
type UtxoGroup struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UtxoMetadataUpdate carries partial updates: nil pointer = leave field
// untouched; empty string = clear field. Outpoint is required.
type UtxoMetadataUpdate struct {
	Outpoint string
	Label    *string
	Tag      *string
	Color    *string
	GroupID  *string
}

// UtxoGroupUpsert creates or edits a group. Empty ID = create new (uuid-ish
// random hex). Name and Color are required on create; on update they replace
// only when non-empty.
type UtxoGroupUpsert struct {
	ID    string
	Name  string
	Color string
}

type UtxoManagerService struct {
	db     *pgxpool.Pool
	logger *log.Logger
}

func NewUtxoManagerService(db *pgxpool.Pool, logger *log.Logger) *UtxoManagerService {
	return &UtxoManagerService{db: db, logger: logger}
}

func (s *UtxoManagerService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrUtxoManagerDBUnavailable
	}
	_, err := s.db.Exec(ctx, `
create table if not exists utxo_groups (
  id text primary key,
  name text not null default '',
  color text not null default '',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists utxo_metadata (
  outpoint text primary key,
  label text not null default '',
  tag text not null default '',
  color text not null default '',
  group_id text references utxo_groups(id) on delete set null,
  updated_at timestamptz not null default now()
);

create index if not exists utxo_metadata_group_idx on utxo_metadata (group_id) where group_id is not null;
`)
	return err
}

// ListMetadata returns all stored UTXO metadata keyed by outpoint.
func (s *UtxoManagerService) ListMetadata(ctx context.Context) (map[string]UtxoMetadata, error) {
	if s == nil || s.db == nil {
		return nil, ErrUtxoManagerDBUnavailable
	}
	rows, err := s.db.Query(ctx, `
select outpoint, label, tag, color, coalesce(group_id, ''), updated_at
from utxo_metadata`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]UtxoMetadata, 32)
	for rows.Next() {
		var m UtxoMetadata
		if err := rows.Scan(&m.Outpoint, &m.Label, &m.Tag, &m.Color, &m.GroupID, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out[m.Outpoint] = m
	}
	return out, rows.Err()
}

// UpsertMetadata applies a partial update. Nil pointers leave the field as-is
// when the row exists, or use empty defaults on insert.
func (s *UtxoManagerService) UpsertMetadata(ctx context.Context, update UtxoMetadataUpdate) error {
	if s == nil || s.db == nil {
		return ErrUtxoManagerDBUnavailable
	}
	outpoint := strings.ToLower(strings.TrimSpace(update.Outpoint))
	if outpoint == "" {
		return errors.New("outpoint required")
	}

	// Resolve nil pointers as "no change on conflict". On fresh insert the
	// COALESCE collapses to '' / NULL appropriately.
	label, labelSet := pointerOrEmpty(update.Label)
	tag, tagSet := pointerOrEmpty(update.Tag)
	color, colorSet := pointerOrEmpty(update.Color)
	groupID, groupSet := pointerOrEmpty(update.GroupID)

	if groupSet && groupID != "" {
		var exists bool
		if err := s.db.QueryRow(ctx, `select exists(select 1 from utxo_groups where id = $1)`, groupID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errors.New("group not found")
		}
	}

	var groupValue any
	if groupSet && groupID == "" {
		groupValue = nil
	} else if groupSet {
		groupValue = groupID
	} else {
		groupValue = nil
	}

	_, err := s.db.Exec(ctx, `
insert into utxo_metadata (outpoint, label, tag, color, group_id, updated_at)
values ($1, $2, $3, $4, $5, now())
on conflict (outpoint) do update set
  label = case when $6 then excluded.label else utxo_metadata.label end,
  tag = case when $7 then excluded.tag else utxo_metadata.tag end,
  color = case when $8 then excluded.color else utxo_metadata.color end,
  group_id = case when $9 then excluded.group_id else utxo_metadata.group_id end,
  updated_at = now()`,
		outpoint,
		label,
		tag,
		color,
		groupValue,
		labelSet,
		tagSet,
		colorSet,
		groupSet,
	)
	return err
}

// ListGroups returns all stored groups ordered by creation time.
func (s *UtxoManagerService) ListGroups(ctx context.Context) ([]UtxoGroup, error) {
	if s == nil || s.db == nil {
		return nil, ErrUtxoManagerDBUnavailable
	}
	rows, err := s.db.Query(ctx, `
select id, name, color, created_at, updated_at
from utxo_groups
order by created_at asc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]UtxoGroup, 0, 8)
	for rows.Next() {
		var g UtxoGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Color, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpsertGroup creates a new group when ID is empty, or updates an existing one.
// Returns the resolved ID.
func (s *UtxoManagerService) UpsertGroup(ctx context.Context, upsert UtxoGroupUpsert) (string, error) {
	if s == nil || s.db == nil {
		return "", ErrUtxoManagerDBUnavailable
	}
	name := strings.TrimSpace(upsert.Name)
	color := strings.TrimSpace(upsert.Color)
	id := strings.TrimSpace(upsert.ID)

	if id == "" {
		generated, err := newGroupID()
		if err != nil {
			return "", err
		}
		_, err = s.db.Exec(ctx, `
insert into utxo_groups (id, name, color)
values ($1, $2, $3)`, generated, name, color)
		if err != nil {
			return "", err
		}
		return generated, nil
	}

	cmd, err := s.db.Exec(ctx, `
update utxo_groups set
  name = case when $2 <> '' then $2 else name end,
  color = case when $3 <> '' then $3 else color end,
  updated_at = now()
where id = $1`, id, name, color)
	if err != nil {
		return "", err
	}
	if cmd.RowsAffected() == 0 {
		return "", errors.New("group not found")
	}
	return id, nil
}

// DeleteGroup removes a group. Member rows have their group_id cleared by the
// FK ON DELETE SET NULL.
func (s *UtxoManagerService) DeleteGroup(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return ErrUtxoManagerDBUnavailable
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("id required")
	}
	_, err := s.db.Exec(ctx, `delete from utxo_groups where id = $1`, id)
	return err
}

// AssignToGroup sets group_id on a batch of outpoints, creating metadata rows
// if needed. Pass an empty groupID to detach.
func (s *UtxoManagerService) AssignToGroup(ctx context.Context, groupID string, outpoints []string) error {
	if s == nil || s.db == nil {
		return ErrUtxoManagerDBUnavailable
	}
	cleaned := normalizeOutpointBatch(outpoints)
	if len(cleaned) == 0 {
		return nil
	}
	groupID = strings.TrimSpace(groupID)
	if groupID != "" {
		var exists bool
		if err := s.db.QueryRow(ctx, `select exists(select 1 from utxo_groups where id = $1)`, groupID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errors.New("group not found")
		}
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var groupValue any
	if groupID == "" {
		groupValue = nil
	} else {
		groupValue = groupID
	}
	for _, outpoint := range cleaned {
		if _, err := tx.Exec(ctx, `
insert into utxo_metadata (outpoint, group_id, updated_at)
values ($1, $2, now())
on conflict (outpoint) do update set
  group_id = excluded.group_id,
  updated_at = now()`, outpoint, groupValue); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Prune removes metadata rows whose outpoint is not in the live set and then
// removes groups that no longer hold any members. Pass the current wallet UTXO
// outpoints. A nil slice is treated as "skip pruning" — only an explicit empty
// slice forces full cleanup.
func (s *UtxoManagerService) Prune(ctx context.Context, liveOutpoints []string) error {
	if s == nil || s.db == nil {
		return ErrUtxoManagerDBUnavailable
	}
	if liveOutpoints == nil {
		return nil
	}
	cleaned := normalizeOutpointBatch(liveOutpoints)

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if len(cleaned) == 0 {
		if _, err := tx.Exec(ctx, `delete from utxo_metadata`); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `delete from utxo_metadata where outpoint <> all($1)`, cleaned); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `
delete from utxo_groups g
where not exists (select 1 from utxo_metadata m where m.group_id = g.id)`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func pointerOrEmpty(p *string) (string, bool) {
	if p == nil {
		return "", false
	}
	return strings.TrimSpace(*p), true
}

func normalizeOutpointBatch(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, raw := range items {
		trimmed := strings.ToLower(strings.TrimSpace(raw))
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func newGroupID() (string, error) {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "g_" + hex.EncodeToString(buf[:]), nil
}
