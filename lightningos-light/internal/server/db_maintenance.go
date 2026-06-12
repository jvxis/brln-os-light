package server

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const (
	dbMaintenanceCleanupBatch              = 50000
	dbMaintenanceTimeout                   = 8 * time.Minute
	dbMaintenanceCompactTimeout            = 2 * time.Hour
	dbMaintenanceCompactLockTimeout        = 30 * time.Second
	dbMaintenanceCompactMinReclaimable     = 256 * 1024 * 1024
	dbMaintenanceCompactMinReclaimableRate = 0.20
	dbMaintenanceGraphHistoryTable         = "graph_channel_policy_history"
	dbMaintenanceDatabaseLabel             = "LightningOS"
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
	Database               string                         `json:"database"`
	TotalBytes             int64                          `json:"total_bytes"`
	Tables                 []dbTableStat                  `json:"tables"`
	GraphHistoryCompaction dbGraphHistoryCompactionStatus `json:"graph_history_compaction"`
}

type dbMaintenanceActionResult struct {
	Table       string `json:"table"`
	RowsRemoved int64  `json:"rows_removed"`
	Error       string `json:"error,omitempty"`
}

type dbMaintenanceActionResponse struct {
	Results []dbMaintenanceActionResult `json:"results"`
}

type dbGraphHistoryCompactionStatus struct {
	Table                   string     `json:"table"`
	Available               bool       `json:"available"`
	Running                 bool       `json:"running"`
	Reason                  string     `json:"reason,omitempty"`
	PhysicalBytes           int64      `json:"physical_bytes"`
	EstimatedLiveBytes      int64      `json:"estimated_live_bytes"`
	HistoryMaxBytes         int64      `json:"history_max_bytes"`
	ReclaimableBytes        int64      `json:"reclaimable_bytes"`
	ReclaimableRatio        float64    `json:"reclaimable_ratio"`
	CleanupAvailable        bool       `json:"cleanup_available"`
	EstimatedLiveExceedsCap bool       `json:"estimated_live_exceeds_cap"`
	EffectiveRetentionDays  int        `json:"effective_retention_days"`
	MinReclaimableBytes     int64      `json:"min_reclaimable_bytes"`
	MinReclaimableRatio     float64    `json:"min_reclaimable_ratio"`
	LockTimeoutSeconds      int        `json:"lock_timeout_seconds"`
	StartedAt               *time.Time `json:"started_at,omitempty"`
	CompletedAt             *time.Time `json:"completed_at,omitempty"`
	LastError               string     `json:"last_error,omitempty"`
	PhysicalBytesBefore     int64      `json:"physical_bytes_before,omitempty"`
	PhysicalBytesAfter      int64      `json:"physical_bytes_after,omitempty"`
	LastReclaimedBytes      int64      `json:"last_reclaimed_bytes,omitempty"`
}

type dbGraphHistoryCompactJob struct {
	Running             bool
	StartedAt           time.Time
	CompletedAt         time.Time
	Error               string
	PhysicalBytesBefore int64
	EstimatedLiveBytes  int64
	HistoryMaxBytes     int64
	ReclaimableBytes    int64
	ReclaimableRatio    float64
	DatabaseTotalBytes  int64
	PhysicalBytesAfter  int64
	ReclaimedBytes      int64
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

func (s *Server) graphExplorerMaintenanceRetentionDays(ctx context.Context) int {
	days := defaultGraphExplorerStorageConfig().HistoryRetentionDays
	if s == nil || s.db == nil {
		return days
	}

	var storedDays int
	if err := s.db.QueryRow(ctx, `
select history_retention_days
from graph_explorer_config
where id = $1
`, graphExplorerConfigID).Scan(&storedDays); err != nil {
		return days
	}
	if storedDays < graphExplorerMinHistoryRetentionDays {
		return graphExplorerMinHistoryRetentionDays
	}
	if storedDays > graphExplorerMaxHistoryRetentionDays {
		return graphExplorerMaxHistoryRetentionDays
	}
	return storedDays
}

func (s *Server) retentionRoutines(ctx context.Context) []retentionRoutine {
	days := func(n int) func(time.Time) time.Time {
		return func(now time.Time) time.Time { return now.AddDate(0, 0, -n) }
	}
	graphHistoryRetentionDays := s.graphExplorerMaintenanceRetentionDays(ctx)
	return []retentionRoutine{
		{"graph_channel_policy_history", "captured_at", days(graphHistoryRetentionDays), fmt.Sprintf("%dd", graphHistoryRetentionDays)},
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
	overview := dbMaintenanceOverview{Database: dbMaintenanceDatabaseLabel}
	if s == nil || s.db == nil {
		return overview, fmt.Errorf("db unavailable")
	}

	if runningStatus, running := s.graphHistoryCompactionRunningStatus(); running {
		graphHistoryRetentionDays := s.graphExplorerMaintenanceRetentionDays(ctx)
		overview.GraphHistoryCompaction = runningStatus
		overview.TotalBytes = s.graphHistoryCompactDatabaseTotalBytes()
		overview.Tables = []dbTableStat{{
			Name:           dbMaintenanceGraphHistoryTable,
			TotalBytes:     runningStatus.PhysicalBytes,
			HasRetention:   true,
			RetentionLabel: fmt.Sprintf("%dd", graphHistoryRetentionDays),
		}}
		return overview, nil
	}

	_ = s.db.QueryRow(ctx, "select pg_database_size(current_database())").Scan(&overview.TotalBytes)

	retention := make(map[string]string)
	for _, r := range s.retentionRoutines(ctx) {
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
	if err := rows.Err(); err != nil {
		return overview, err
	}
	overview.GraphHistoryCompaction = s.graphHistoryCompactionStatus(ctx)
	return overview, nil
}

// runRetentionCleanup runs every registered retention prune on demand. It only
// deletes rows already past their retention window — the same rows the
// schedulers delete — so it can never remove more than the app already would.
func (s *Server) runRetentionCleanup(ctx context.Context) []dbMaintenanceActionResult {
	routines := s.retentionRoutines(ctx)
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
	routines := s.retentionRoutines(ctx)
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

func (s *Server) graphHistoryCompactionStatus(ctx context.Context) dbGraphHistoryCompactionStatus {
	status := dbGraphHistoryCompactionStatus{
		Table:               dbMaintenanceGraphHistoryTable,
		Reason:              "unavailable",
		MinReclaimableBytes: dbMaintenanceCompactMinReclaimable,
		MinReclaimableRatio: dbMaintenanceCompactMinReclaimableRate,
		LockTimeoutSeconds:  int(dbMaintenanceCompactLockTimeout.Seconds()),
	}
	if s == nil || s.db == nil {
		status.Reason = "db_unavailable"
		return status
	}

	if runningStatus, running := s.graphHistoryCompactionRunningStatus(); running {
		return runningStatus
	}

	s.dbMaintenanceMu.Lock()
	job := s.graphHistoryCompact
	s.dbMaintenanceMu.Unlock()
	if !job.CompletedAt.IsZero() {
		completedAt := job.CompletedAt
		status.CompletedAt = &completedAt
		status.LastError = job.Error
		status.PhysicalBytesBefore = job.PhysicalBytesBefore
		status.PhysicalBytesAfter = job.PhysicalBytesAfter
		status.LastReclaimedBytes = job.ReclaimedBytes
	}

	svc, errMsg := s.graphExplorerService()
	if svc == nil {
		if errMsg != "" {
			status.Reason = errMsg
		}
		return status
	}
	graphStatus, err := svc.StorageStatus(ctx)
	if err != nil {
		status.Reason = err.Error()
		return status
	}

	status.PhysicalBytes = graphStatus.HistoryBytes
	status.EstimatedLiveBytes = graphStatus.EstimatedLiveHistoryBytes
	status.HistoryMaxBytes = graphStatus.Config.HistoryMaxBytes
	status.CleanupAvailable = graphStatus.CleanupAvailable
	status.EffectiveRetentionDays = graphStatus.EffectiveRetentionDays
	if status.EstimatedLiveBytes > 0 {
		status.ReclaimableBytes = status.PhysicalBytes - status.EstimatedLiveBytes
		if status.ReclaimableBytes < 0 {
			status.ReclaimableBytes = 0
		}
	}
	if status.PhysicalBytes > 0 {
		status.ReclaimableRatio = float64(status.ReclaimableBytes) / float64(status.PhysicalBytes)
	}
	status.EstimatedLiveExceedsCap = status.HistoryMaxBytes > 0 && status.EstimatedLiveBytes > status.HistoryMaxBytes

	switch {
	case status.HistoryMaxBytes <= 0:
		status.Reason = "graph_storage_cap_required"
	case status.CleanupAvailable:
		status.Reason = "graph_cleanup_required"
	case status.PhysicalBytes <= status.HistoryMaxBytes:
		status.Reason = "physical_size_within_cap"
	case status.EstimatedLiveBytes <= 0:
		status.Reason = "live_size_unknown"
	case status.ReclaimableBytes < dbMaintenanceCompactMinReclaimable && status.ReclaimableRatio < dbMaintenanceCompactMinReclaimableRate:
		status.Reason = "reclaimable_space_too_small"
	default:
		status.Available = true
		status.Reason = ""
	}

	return status
}

func (s *Server) graphHistoryCompactionRunningStatus() (dbGraphHistoryCompactionStatus, bool) {
	status := dbGraphHistoryCompactionStatus{
		Table:               dbMaintenanceGraphHistoryTable,
		Reason:              "compaction_running",
		MinReclaimableBytes: dbMaintenanceCompactMinReclaimable,
		MinReclaimableRatio: dbMaintenanceCompactMinReclaimableRate,
		LockTimeoutSeconds:  int(dbMaintenanceCompactLockTimeout.Seconds()),
	}
	if s == nil {
		return status, false
	}

	s.dbMaintenanceMu.Lock()
	job := s.graphHistoryCompact
	s.dbMaintenanceMu.Unlock()
	if !job.Running {
		return status, false
	}

	startedAt := job.StartedAt
	status.Running = true
	status.StartedAt = &startedAt
	status.PhysicalBytesBefore = job.PhysicalBytesBefore
	status.PhysicalBytes = job.PhysicalBytesBefore
	status.EstimatedLiveBytes = job.EstimatedLiveBytes
	status.HistoryMaxBytes = job.HistoryMaxBytes
	status.ReclaimableBytes = job.ReclaimableBytes
	status.ReclaimableRatio = job.ReclaimableRatio
	return status, true
}

func (s *Server) graphHistoryCompactDatabaseTotalBytes() int64 {
	if s == nil {
		return 0
	}
	s.dbMaintenanceMu.Lock()
	defer s.dbMaintenanceMu.Unlock()
	return s.graphHistoryCompact.DatabaseTotalBytes
}

func (s *Server) startGraphHistoryPhysicalCompaction(ctx context.Context) (dbGraphHistoryCompactionStatus, error) {
	status := s.graphHistoryCompactionStatus(ctx)
	if status.Running {
		return status, nil
	}
	if !status.Available {
		return status, fmt.Errorf("%s", status.Reason)
	}

	now := time.Now().UTC()
	var databaseTotalBytes int64
	_ = s.db.QueryRow(ctx, "select pg_database_size(current_database())").Scan(&databaseTotalBytes)

	s.dbMaintenanceMu.Lock()
	if s.graphHistoryCompact.Running {
		job := s.graphHistoryCompact
		s.dbMaintenanceMu.Unlock()
		startedAt := job.StartedAt
		status.Running = true
		status.Available = false
		status.Reason = "compaction_running"
		status.StartedAt = &startedAt
		return status, nil
	}
	s.graphHistoryCompact = dbGraphHistoryCompactJob{
		Running:             true,
		StartedAt:           now,
		PhysicalBytesBefore: status.PhysicalBytes,
		EstimatedLiveBytes:  status.EstimatedLiveBytes,
		HistoryMaxBytes:     status.HistoryMaxBytes,
		ReclaimableBytes:    status.ReclaimableBytes,
		ReclaimableRatio:    status.ReclaimableRatio,
		DatabaseTotalBytes:  databaseTotalBytes,
	}
	s.dbMaintenanceMu.Unlock()

	go s.runGraphHistoryPhysicalCompaction(status)

	status.Running = true
	status.Available = false
	status.Reason = "compaction_running"
	status.StartedAt = &now
	return status, nil
}

func (s *Server) runGraphHistoryPhysicalCompaction(before dbGraphHistoryCompactionStatus) {
	ctx, cancel := context.WithTimeout(context.Background(), dbMaintenanceCompactTimeout)
	defer cancel()

	err := s.vacuumFullGraphHistory(ctx)
	var physicalAfter int64
	if err == nil && s != nil && s.db != nil {
		measureCtx, measureCancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = s.db.QueryRow(measureCtx, `select pg_total_relation_size('graph_channel_policy_history'::regclass)`).Scan(&physicalAfter)
		measureCancel()
	}

	completedAt := time.Now().UTC()
	job := dbGraphHistoryCompactJob{
		Running:             false,
		StartedAt:           time.Now().UTC(),
		CompletedAt:         completedAt,
		PhysicalBytesBefore: before.PhysicalBytes,
		PhysicalBytesAfter:  physicalAfter,
	}
	if err != nil {
		job.Error = err.Error()
	}
	if before.PhysicalBytes > physicalAfter && physicalAfter > 0 {
		job.ReclaimedBytes = before.PhysicalBytes - physicalAfter
	}

	s.dbMaintenanceMu.Lock()
	previous := s.graphHistoryCompact
	if !previous.StartedAt.IsZero() {
		job.StartedAt = previous.StartedAt
	}
	s.graphHistoryCompact = job
	s.dbMaintenanceMu.Unlock()
}

func (s *Server) vacuumFullGraphHistory(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("db unavailable")
	}
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	lockSeconds := int(dbMaintenanceCompactLockTimeout.Seconds())
	if _, err := conn.Conn().PgConn().Exec(ctx, fmt.Sprintf("set lock_timeout = '%ds'", lockSeconds)).ReadAll(); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Conn().PgConn().Exec(context.Background(), "reset lock_timeout").ReadAll()
	}()

	_, err = conn.Conn().PgConn().Exec(ctx, fmt.Sprintf("vacuum (full, analyze) %s", dbMaintenanceGraphHistoryTable)).ReadAll()
	return err
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

func (s *Server) handleDBMaintenanceCompactGraphHistoryPost(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status, err := s.startGraphHistoryPhysicalCompaction(ctx)
	if err != nil {
		writeError(w, http.StatusBadRequest, status.Reason)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
