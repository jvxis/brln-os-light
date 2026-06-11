package server

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const (
	dbMaintenanceCleanupBatch = 50000
	dbMaintenanceTimeout      = 8 * time.Minute
)

type dbTableStat struct {
	Name           string     `json:"name"`
	TotalBytes     int64      `json:"total_bytes"`
	RowEstimate    int64      `json:"row_estimate"`
	DeadTuples     int64      `json:"dead_tuples"`
	LastAutovacuum *time.Time `json:"last_autovacuum,omitempty"`
	HasRetention   bool       `json:"has_retention"`
	RetentionLabel string     `json:"retention_label,omitempty"`
}

type dbMaintenanceOverview struct {
	Database   string        `json:"database"`
	TotalBytes int64         `json:"total_bytes"`
	Tables     []dbTableStat `json:"tables"`
}

type dbMaintenanceActionResult struct {
	Table       string `json:"table"`
	RowsRemoved int64  `json:"rows_removed"`
	Error       string `json:"error,omitempty"`
}

type dbMaintenanceActionResponse struct {
	Results []dbMaintenanceActionResult `json:"results"`
}

// retentionRoutine mirrors a time-windowed cleanup that already runs on its own
// scheduler elsewhere in the app. The maintenance panel surfaces these and can
// run them on demand — the effect is identical to the scheduled prune, never
// broader. Windows reference the owning service's constant where one exists.
type retentionRoutine struct {
	Table  string
	Column string
	Cutoff func(now time.Time) time.Time
	Label  string
}

func (s *Server) retentionRoutines() []retentionRoutine {
	days := func(n int) func(time.Time) time.Time {
		return func(now time.Time) time.Time { return now.AddDate(0, 0, -n) }
	}
	return []retentionRoutine{
		{"graph_channel_policy_history", "captured_at", days(graphExplorerPolicyHistoryRetentionDays), fmt.Sprintf("%dd", graphExplorerPolicyHistoryRetentionDays)},
		{"notifications", "occurred_at", days(notificationRetentionDays), fmt.Sprintf("%dd", notificationRetentionDays)},
		{"audit_events", "ts", days(auditEventsRetentionDays()), fmt.Sprintf("%dd", auditEventsRetentionDays())},
		{"chat_messages", "timestamp", days(chatRetentionDays), fmt.Sprintf("%dd", chatRetentionDays)},
		{"channel_ranking_peer_samples", "sampled_bucket", days(30), "30d"},
		{"channel_ranking_htlc_failures", "sampled_bucket", days(30), "30d"},
		{"channel_downtime_state", "updated_at", days(30), "30d"},
		{"rebalance_scan_skips", "scan_at", days(14), "14d"},
		{"rebalance_sovereign_history", "scan_at", func(now time.Time) time.Time { return now.Add(-48 * time.Hour) }, "48h"},
	}
}

// dbMaintenanceOverview reports per-table size/bloat for the connected database
// (always the LOS database — the pool never touches the LND database).
func (s *Server) dbMaintenanceOverview(ctx context.Context) (dbMaintenanceOverview, error) {
	overview := dbMaintenanceOverview{}
	if s == nil || s.db == nil {
		return overview, fmt.Errorf("db unavailable")
	}
	if s.cfg != nil {
		overview.Database = s.cfg.Postgres.DBName
	}
	_ = s.db.QueryRow(ctx, "select pg_database_size(current_database())").Scan(&overview.TotalBytes)

	retention := make(map[string]string)
	for _, r := range s.retentionRoutines() {
		retention[r.Table] = r.Label
	}

	rows, err := s.db.Query(ctx, `
select c.relname,
       pg_total_relation_size(c.oid),
       coalesce(st.n_live_tup, 0),
       coalesce(st.n_dead_tup, 0),
       greatest(st.last_autovacuum, st.last_vacuum)
from pg_class c
join pg_namespace n on n.oid = c.relnamespace
left join pg_stat_user_tables st on st.relid = c.oid
where n.nspname = 'public' and c.relkind = 'r'
order by pg_total_relation_size(c.oid) desc
`)
	if err != nil {
		return overview, err
	}
	defer rows.Close()
	for rows.Next() {
		var stat dbTableStat
		var lastVac *time.Time
		if err := rows.Scan(&stat.Name, &stat.TotalBytes, &stat.RowEstimate, &stat.DeadTuples, &lastVac); err != nil {
			return overview, err
		}
		stat.LastAutovacuum = lastVac
		if label, ok := retention[stat.Name]; ok {
			stat.HasRetention = true
			stat.RetentionLabel = label
		}
		overview.Tables = append(overview.Tables, stat)
	}
	return overview, rows.Err()
}

// runRetentionCleanup runs every registered retention prune on demand. It only
// deletes rows already past their retention window — the same rows the
// schedulers delete — so it can never remove more than the app already would.
func (s *Server) runRetentionCleanup(ctx context.Context) []dbMaintenanceActionResult {
	routines := s.retentionRoutines()
	results := make([]dbMaintenanceActionResult, 0, len(routines))
	now := time.Now().UTC()
	for _, r := range routines {
		removed, err := s.batchedRetentionDelete(ctx, r.Table, r.Column, r.Cutoff(now))
		res := dbMaintenanceActionResult{Table: r.Table, RowsRemoved: removed}
		if err != nil {
			res.Error = err.Error()
		}
		results = append(results, res)
	}
	return results
}

func (s *Server) batchedRetentionDelete(ctx context.Context, table, column string, cutoff time.Time) (int64, error) {
	// table/column come from the fixed registry above, never from user input.
	stmt := fmt.Sprintf(`delete from %s where ctid in (select ctid from %s where %s < $1 limit %d)`, table, table, column, dbMaintenanceCleanupBatch)
	var total int64
	for i := 0; i < 1000; i++ {
		tag, err := s.db.Exec(ctx, stmt, cutoff)
		if err != nil {
			return total, err
		}
		n := tag.RowsAffected()
		total += n
		if n < dbMaintenanceCleanupBatch {
			break
		}
	}
	return total, nil
}

// runVacuumAnalyze runs an online VACUUM (ANALYZE) — never VACUUM FULL — per
// retention table to mark freed space reusable and refresh planner stats.
func (s *Server) runVacuumAnalyze(ctx context.Context) []dbMaintenanceActionResult {
	routines := s.retentionRoutines()
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return []dbMaintenanceActionResult{{Table: "*", Error: err.Error()}}
	}
	defer conn.Release()

	results := make([]dbMaintenanceActionResult, 0, len(routines))
	for _, r := range routines {
		// VACUUM must use the simple query protocol (cannot run inside a tx).
		_, execErr := conn.Conn().PgConn().Exec(ctx, fmt.Sprintf("vacuum (analyze) %s", r.Table)).ReadAll()
		res := dbMaintenanceActionResult{Table: r.Table}
		if execErr != nil {
			res.Error = execErr.Error()
		}
		results = append(results, res)
	}
	return results
}

func (s *Server) handleDBMaintenanceGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	overview, err := s.dbMaintenanceOverview(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load database maintenance overview")
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) handleDBMaintenanceCleanupPost(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), dbMaintenanceTimeout)
	defer cancel()
	writeJSON(w, http.StatusOK, dbMaintenanceActionResponse{Results: s.runRetentionCleanup(ctx)})
}

func (s *Server) handleDBMaintenanceVacuumPost(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), dbMaintenanceTimeout)
	defer cancel()
	writeJSON(w, http.StatusOK, dbMaintenanceActionResponse{Results: s.runVacuumAnalyze(ctx)})
}
