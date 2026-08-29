package server

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

type appVersionRecord struct {
	CatalogVersion string
	AppliedVersion string
}

type appVersionRepository interface {
	List(context.Context) (map[string]appVersionRecord, error)
	MarkApplied(context.Context, string) error
}

type AppVersionsService struct {
	db *pgxpool.Pool
}

func NewAppVersionsService(db *pgxpool.Pool) *AppVersionsService {
	return &AppVersionsService{db: db}
}

func (s *AppVersionsService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("app versions database is unavailable")
	}
	_, err := s.db.Exec(ctx, `
CREATE TABLE IF NOT EXISTS app_store_versions (
    app_id TEXT PRIMARY KEY,
    catalog_version TEXT NOT NULL CHECK (catalog_version <> ''),
    applied_version TEXT NULL,
    catalog_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    applied_updated_at TIMESTAMPTZ NULL
)
`)
	return err
}

// SyncCatalog changes only the release offered by this LightningOS build. It
// deliberately preserves applied_version, which describes the last release
// successfully applied through the existing app lifecycle.
func (s *AppVersionsService) SyncCatalog(ctx context.Context, catalog map[string]string) error {
	if s == nil || s.db == nil {
		return errors.New("app versions database is unavailable")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	appIDs := make([]string, 0, len(catalog))
	for appID := range catalog {
		appIDs = append(appIDs, appID)
	}
	sort.Strings(appIDs)
	for _, appID := range appIDs {
		version := catalog[appID]
		if appID == "" || version == "" {
			return errors.New("app version catalog contains an empty id or version")
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO app_store_versions (app_id, catalog_version)
VALUES ($1, $2)
ON CONFLICT (app_id) DO UPDATE SET
    catalog_version = EXCLUDED.catalog_version,
    catalog_updated_at = CASE
        WHEN app_store_versions.catalog_version IS DISTINCT FROM EXCLUDED.catalog_version THEN NOW()
        ELSE app_store_versions.catalog_updated_at
    END
`, appID, version); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *AppVersionsService) List(ctx context.Context) (map[string]appVersionRecord, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("app versions database is unavailable")
	}
	rows, err := s.db.Query(ctx, `
SELECT app_id, catalog_version, COALESCE(applied_version, '')
FROM app_store_versions
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := make(map[string]appVersionRecord)
	for rows.Next() {
		var appID string
		var record appVersionRecord
		if err := rows.Scan(&appID, &record.CatalogVersion, &record.AppliedVersion); err != nil {
			return nil, err
		}
		versions[appID] = record
	}
	return versions, rows.Err()
}

func (s *AppVersionsService) MarkApplied(ctx context.Context, appID string) error {
	if s == nil || s.db == nil {
		return errors.New("app versions database is unavailable")
	}
	result, err := s.db.Exec(ctx, `
UPDATE app_store_versions
SET applied_version = catalog_version,
    applied_updated_at = NOW()
WHERE app_id = $1
`, appID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("app is not present in the version catalog")
	}
	return nil
}
