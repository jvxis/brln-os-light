package reports

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMissingDailyDatesUnavailable(t *testing.T) {
	if _, err := MissingDailyDates(context.Background(), nil, time.Now()); err == nil {
		t.Fatal("expected an error for an unavailable reports database")
	}
}

// Run with LIGHTNINGOS_TEST_POSTGRES_DSN pointing to a PostgreSQL instance.
// All fixture writes use a session-local temporary table, never an app table.
func TestMissingDailyDatesPostgres(t *testing.T) {
	dsn := os.Getenv("LIGHTNINGOS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LIGHTNINGOS_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test PostgreSQL configuration")
	}
	// Keep every query on the session that owns the temporary table. Restrict
	// lookup to pg_temp so losing that session cannot access a real table.
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.ConnConfig.RuntimeParams["search_path"] = "pg_temp"
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("could not create test PostgreSQL pool")
	}
	defer db.Close()
	if _, err := db.Exec(ctx, "create temporary table reports_daily (report_date date primary key)"); err != nil {
		t.Fatal(err)
	}

	loc := time.FixedZone("UTC-3", -3*60*60)
	endDate := time.Date(2026, 9, 5, 15, 0, 0, 0, loc)
	for _, tc := range []struct {
		name  string
		dates []string
		want  []string
	}{
		{name: "new installation has no history", want: []string{}},
		{name: "first report is today", dates: []string{"2026-09-06"}, want: []string{}},
		{name: "first report is yesterday", dates: []string{"2026-09-05"}, want: []string{}},
		{name: "complete history", dates: []string{"2026-09-03", "2026-09-04", "2026-09-05"}, want: []string{}},
		{name: "interior and trailing gaps", dates: []string{"2026-09-01", "2026-09-03"}, want: []string{"2026-09-02", "2026-09-04", "2026-09-05"}},
		{name: "today does not hide past gaps", dates: []string{"2026-09-03", "2026-09-06"}, want: []string{"2026-09-04", "2026-09-05"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(ctx, "truncate pg_temp.reports_daily"); err != nil {
				t.Fatal(err)
			}
			for _, date := range tc.dates {
				if _, err := db.Exec(ctx, "insert into pg_temp.reports_daily (report_date) values ($1::text::date)", date); err != nil {
					t.Fatal(err)
				}
			}
			dates, err := MissingDailyDates(ctx, db, endDate)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(dates))
			for _, date := range dates {
				got = append(got, date.Format("2006-01-02"))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("missing dates = %v, want %v", got, tc.want)
			}
		})
	}
}
