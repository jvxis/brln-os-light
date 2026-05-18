package reports

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const provenanceReportHealthStaleAfter = 24 * time.Hour

type ProvenanceReportHealth struct {
	LastSyncAt       *time.Time
	LastSyncAgeHours *float64
	Alert            bool
	LastError        string
}

func FetchProvenanceReportHealth(ctx context.Context, db *pgxpool.Pool, now time.Time) (ProvenanceReportHealth, bool, error) {
	if db == nil {
		return ProvenanceReportHealth{}, false, nil
	}

	var tableExists bool
	if err := db.QueryRow(ctx, `select to_regclass('provenance_state') is not null`).Scan(&tableExists); err != nil {
		return ProvenanceReportHealth{}, false, err
	}
	if !tableExists {
		return ProvenanceReportHealth{}, false, nil
	}

	var lastSync pgtype.Timestamptz
	var lastError string
	err := db.QueryRow(ctx, `
select last_sync_at, coalesce(last_error, '')
from provenance_state
where id = true
`).Scan(&lastSync, &lastError)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return buildProvenanceReportHealth(nil, "", now), true, nil
		}
		return ProvenanceReportHealth{}, false, err
	}

	var lastSyncAt *time.Time
	if lastSync.Valid && !lastSync.Time.IsZero() {
		value := lastSync.Time.UTC()
		lastSyncAt = &value
	}
	return buildProvenanceReportHealth(lastSyncAt, lastError, now), true, nil
}

func buildProvenanceReportHealth(lastSyncAt *time.Time, lastError string, now time.Time) ProvenanceReportHealth {
	now = now.UTC()
	lastError = strings.TrimSpace(lastError)

	alert := true
	var ageHours *float64
	var syncAt *time.Time
	if lastSyncAt != nil && !lastSyncAt.IsZero() {
		value := lastSyncAt.UTC()
		syncAt = &value

		age := now.Sub(value).Hours()
		if age < 0 {
			age = 0
		}
		ageHours = &age
		alert = age > provenanceReportHealthStaleAfter.Hours()
	}
	if lastError != "" {
		alert = true
	}

	return ProvenanceReportHealth{
		LastSyncAt:       syncAt,
		LastSyncAgeHours: ageHours,
		Alert:            alert,
		LastError:        lastError,
	}
}

func (m Metrics) WithProvenanceReportHealth(health ProvenanceReportHealth) Metrics {
	alert := health.Alert
	lastError := health.LastError
	m.ProvenanceLastSyncAt = health.LastSyncAt
	m.ProvenanceLastSyncAgeHours = health.LastSyncAgeHours
	m.ProvenanceHealthAlert = &alert
	m.ProvenanceLastError = &lastError
	return m
}
