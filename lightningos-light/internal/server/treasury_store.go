package server

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func treasuryStateNotCreated(err error) bool {
	var databaseError *pgconn.PgError
	return errors.Is(err, pgx.ErrNoRows) || errors.As(err, &databaseError) && databaseError.Code == "42P01"
}

func (s *Server) ensureTreasuryState(ctx context.Context) error {
	if s.db == nil {
		return errors.New("treasury PostgreSQL state unavailable")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return errors.New("treasury PostgreSQL state unavailable")
	}
	defer tx.Rollback(ctx)
	for _, statement := range []string{
		`SELECT pg_advisory_xact_lock(84530560)`,
		`CREATE TABLE IF NOT EXISTS stablecoin_treasury_config (
			singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
			installed boolean NOT NULL DEFAULT false,
			enabled boolean NOT NULL DEFAULT false,
			lightning_address text NOT NULL DEFAULT ''
		)`,
		`INSERT INTO stablecoin_treasury_config DEFAULT VALUES ON CONFLICT DO NOTHING`,
		`ALTER TABLE stablecoin_treasury_config ADD COLUMN IF NOT EXISTS next_quote_at timestamptz NOT NULL DEFAULT '-infinity'`,
		`CREATE TABLE IF NOT EXISTS stablecoin_treasury_orders (
			id text PRIMARY KEY,
			bridge_id text UNIQUE NOT NULL,
			invoice_hash text UNIQUE,
			delivery_ref text UNIQUE,
			direction text NOT NULL CHECK (direction IN ('to_base', 'to_lightning')),
			state text NOT NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			expires_at timestamptz NOT NULL,
			public_data jsonb NOT NULL,
			private_data jsonb NOT NULL
		)`,
	} {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return errors.New("could not initialize treasury data")
		}
	}
	if tx.Commit(ctx) != nil {
		return errors.New("could not initialize treasury data")
	}
	return nil
}

func (s *Server) setTreasuryMode(ctx context.Context, mode string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return errors.New("treasury PostgreSQL state unavailable")
	}
	defer tx.Rollback(ctx)
	var installed bool
	if err := tx.QueryRow(ctx, `SELECT installed FROM stablecoin_treasury_config WHERE singleton FOR UPDATE`).Scan(&installed); err != nil {
		return errors.New("could not read treasury state")
	}
	enabled := false
	switch mode {
	case "install":
		installed, enabled = true, true
	case "start":
		if !installed {
			return errors.New("stablecoin treasury is not installed")
		}
		enabled = true
	case "stop":
	case "uninstall":
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM stablecoin_treasury_orders WHERE
			state NOT IN ('completed', 'refunded', 'expired', 'failed') AND
			(state <> 'quoted' OR expires_at > now()))`).Scan(&active); err != nil {
			return errors.New("could not check unresolved treasury orders")
		}
		if active {
			return errors.New("resolve active treasury orders before uninstalling")
		}
		installed = false
	default:
		return errors.New("unsupported treasury action")
	}
	if _, err := tx.Exec(ctx, `UPDATE stablecoin_treasury_config SET installed=$1, enabled=$2 WHERE singleton`, installed, enabled); err != nil {
		return errors.New("could not save treasury state")
	}
	if tx.Commit(ctx) != nil {
		return errors.New("could not save treasury state")
	}
	return nil
}
