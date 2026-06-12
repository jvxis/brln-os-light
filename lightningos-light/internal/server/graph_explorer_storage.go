package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type GraphExplorerStorageConfig struct {
	HistoryRetentionDays int   `json:"history_retention_days"`
	HistoryMaxBytes      int64 `json:"history_max_bytes"`
}

type GraphExplorerStorageUpdateRequest struct {
	HistoryRetentionDays *int     `json:"history_retention_days,omitempty"`
	HistoryMaxBytes      *int64   `json:"history_max_bytes,omitempty"`
	HistoryMaxGB         *float64 `json:"history_max_gb,omitempty"`
}

type GraphExplorerStorageProjection struct {
	Days  int   `json:"days"`
	Bytes int64 `json:"bytes"`
}

type GraphExplorerStorageStatus struct {
	Config                       GraphExplorerStorageConfig       `json:"config"`
	GraphTotalBytes              int64                            `json:"graph_total_bytes"`
	HistoryBytes                 int64                            `json:"history_bytes"`
	EstimatedLiveHistoryBytes    int64                            `json:"estimated_live_history_bytes"`
	HistoryRows                  int64                            `json:"history_rows"`
	HistoryDeadTuples            int64                            `json:"history_dead_tuples"`
	HistoryLastVacuum            *time.Time                       `json:"history_last_vacuum,omitempty"`
	CoverageSince                *time.Time                       `json:"coverage_since,omitempty"`
	CoverageUntil                *time.Time                       `json:"coverage_until,omitempty"`
	CoverageDays                 float64                          `json:"coverage_days"`
	BytesPerDay                  int64                            `json:"bytes_per_day"`
	GBPerDay                     float64                          `json:"gb_per_day"`
	EffectiveRetentionDays       int                              `json:"effective_retention_days"`
	EstimatedBytesAfterCleanup   int64                            `json:"estimated_bytes_after_cleanup"`
	CleanupAvailable             bool                             `json:"cleanup_available"`
	NormalVacuumShrinksDiskFiles bool                             `json:"normal_vacuum_shrinks_disk_files"`
	Projections                  []GraphExplorerStorageProjection `json:"projections"`
}

type GraphExplorerStorageCleanupResult struct {
	RowsRemoved int64                      `json:"rows_removed"`
	CutoffAt    *time.Time                 `json:"cutoff_at,omitempty"`
	VacuumRan   bool                       `json:"vacuum_ran"`
	VacuumError string                     `json:"vacuum_error,omitempty"`
	Status      GraphExplorerStorageStatus `json:"status"`
}

func defaultGraphExplorerStorageConfig() GraphExplorerStorageConfig {
	return GraphExplorerStorageConfig{
		HistoryRetentionDays: graphExplorerDefaultHistoryRetentionDays,
		HistoryMaxBytes:      graphExplorerDefaultHistoryMaxBytes,
	}
}

func normalizeGraphExplorerStorageConfig(cfg GraphExplorerStorageConfig) GraphExplorerStorageConfig {
	if cfg.HistoryRetentionDays < graphExplorerMinHistoryRetentionDays {
		cfg.HistoryRetentionDays = graphExplorerDefaultHistoryRetentionDays
	}
	if cfg.HistoryRetentionDays > graphExplorerMaxHistoryRetentionDays {
		cfg.HistoryRetentionDays = graphExplorerMaxHistoryRetentionDays
	}
	if cfg.HistoryMaxBytes < 0 {
		cfg.HistoryMaxBytes = 0
	}
	if cfg.HistoryMaxBytes > 0 && cfg.HistoryMaxBytes < graphExplorerMinHistoryMaxBytes {
		cfg.HistoryMaxBytes = graphExplorerMinHistoryMaxBytes
	}
	return cfg
}

func graphExplorerStorageBytesFromGB(gb float64) (int64, error) {
	if math.IsNaN(gb) || math.IsInf(gb, 0) || gb < 0 {
		return 0, errors.New("history_max_gb must be zero or positive")
	}
	if gb == 0 {
		return 0, nil
	}
	bytes := int64(math.Round(gb * 1024 * 1024 * 1024))
	if bytes < graphExplorerMinHistoryMaxBytes {
		return 0, fmt.Errorf("history_max_gb must be at least %.2f GB or zero", float64(graphExplorerMinHistoryMaxBytes)/(1024*1024*1024))
	}
	return bytes, nil
}

func graphExplorerValidateStorageConfig(cfg GraphExplorerStorageConfig) error {
	if cfg.HistoryRetentionDays < graphExplorerMinHistoryRetentionDays || cfg.HistoryRetentionDays > graphExplorerMaxHistoryRetentionDays {
		return fmt.Errorf("history_retention_days must be between %d and %d", graphExplorerMinHistoryRetentionDays, graphExplorerMaxHistoryRetentionDays)
	}
	if cfg.HistoryMaxBytes < 0 {
		return errors.New("history_max_bytes must be zero or positive")
	}
	if cfg.HistoryMaxBytes > 0 && cfg.HistoryMaxBytes < graphExplorerMinHistoryMaxBytes {
		return fmt.Errorf("history_max_bytes must be at least %d or zero", graphExplorerMinHistoryMaxBytes)
	}
	return nil
}

func graphExplorerEffectiveHistoryDays(retentionDays int, maxBytes int64, bytesPerDay float64) int {
	days := retentionDays
	if days < graphExplorerMinHistoryRetentionDays {
		days = graphExplorerMinHistoryRetentionDays
	}
	if maxBytes > 0 && bytesPerDay > 0 {
		bySize := int(math.Floor(float64(maxBytes) / bytesPerDay))
		if bySize < graphExplorerMinHistoryRetentionDays {
			bySize = graphExplorerMinHistoryRetentionDays
		}
		if bySize < days {
			days = bySize
		}
	}
	if days > graphExplorerMaxHistoryRetentionDays {
		days = graphExplorerMaxHistoryRetentionDays
	}
	return days
}

func graphExplorerProjectionBytes(bytesPerDay float64, days int) int64 {
	if bytesPerDay <= 0 || days <= 0 {
		return 0
	}
	return int64(math.Round(bytesPerDay * float64(days)))
}

func graphExplorerCleanupAvailable(coverageDays float64, effectiveDays int, sizingBytes int64, maxBytes int64) bool {
	if effectiveDays < graphExplorerMinHistoryRetentionDays {
		effectiveDays = graphExplorerMinHistoryRetentionDays
	}
	if coverageDays > float64(effectiveDays)+0.5 {
		return true
	}
	if maxBytes > 0 && sizingBytes > maxBytes && coverageDays > 1 {
		return true
	}
	return false
}

func (s *GraphExplorerService) loadStorageConfig(ctx context.Context) (GraphExplorerStorageConfig, error) {
	cfg := defaultGraphExplorerStorageConfig()
	if s == nil || s.db == nil {
		return cfg, ErrGraphExplorerDBUnavailable
	}

	err := s.db.QueryRow(ctx, `
select history_retention_days, history_max_bytes
from graph_explorer_config
where id = $1
`, graphExplorerConfigID).Scan(&cfg.HistoryRetentionDays, &cfg.HistoryMaxBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cfg, nil
		}
		return cfg, err
	}
	return normalizeGraphExplorerStorageConfig(cfg), nil
}

func (s *GraphExplorerService) UpdateStorageConfig(ctx context.Context, req GraphExplorerStorageUpdateRequest) (GraphExplorerStorageConfig, error) {
	current, err := s.loadStorageConfig(ctx)
	if err != nil {
		return GraphExplorerStorageConfig{}, err
	}
	next := current
	if req.HistoryRetentionDays != nil {
		next.HistoryRetentionDays = *req.HistoryRetentionDays
	}
	if req.HistoryMaxBytes != nil {
		next.HistoryMaxBytes = *req.HistoryMaxBytes
	}
	if req.HistoryMaxGB != nil {
		bytes, err := graphExplorerStorageBytesFromGB(*req.HistoryMaxGB)
		if err != nil {
			return GraphExplorerStorageConfig{}, err
		}
		next.HistoryMaxBytes = bytes
	}
	if err := graphExplorerValidateStorageConfig(next); err != nil {
		return GraphExplorerStorageConfig{}, err
	}

	if _, err := s.db.Exec(ctx, `
insert into graph_explorer_config (id, history_retention_days, history_max_bytes, updated_at)
values ($1, $2, $3, now())
on conflict (id) do update set
  history_retention_days = excluded.history_retention_days,
  history_max_bytes = excluded.history_max_bytes,
  updated_at = now()
`, graphExplorerConfigID, next.HistoryRetentionDays, next.HistoryMaxBytes); err != nil {
		return GraphExplorerStorageConfig{}, err
	}
	return next, nil
}

func (s *GraphExplorerService) StorageStatus(ctx context.Context) (GraphExplorerStorageStatus, error) {
	out := GraphExplorerStorageStatus{
		NormalVacuumShrinksDiskFiles: false,
	}
	if s == nil || s.db == nil {
		return out, ErrGraphExplorerDBUnavailable
	}

	cfg, err := s.loadStorageConfig(ctx)
	if err != nil {
		return out, err
	}
	out.Config = cfg

	_ = s.db.QueryRow(ctx, `
select coalesce(sum(pg_total_relation_size(c.oid)), 0)
from pg_class c
join pg_namespace n on n.oid = c.relnamespace
where n.nspname = 'public'
  and c.relkind = 'r'
  and c.relname like 'graph_%'
`).Scan(&out.GraphTotalBytes)

	_ = s.db.QueryRow(ctx, `select pg_total_relation_size('graph_channel_policy_history'::regclass)`).Scan(&out.HistoryBytes)

	var lastVac *time.Time
	_ = s.db.QueryRow(ctx, `
select coalesce(n_live_tup, 0),
       coalesce(n_dead_tup, 0),
       greatest(last_autovacuum, last_vacuum)
from pg_stat_user_tables
where relname = 'graph_channel_policy_history'
`).Scan(&out.HistoryRows, &out.HistoryDeadTuples, &lastVac)
	out.HistoryLastVacuum = lastVac
	out.EstimatedLiveHistoryBytes = s.estimatePolicyHistoryLiveBytes(ctx, out.HistoryBytes, out.HistoryRows)

	var since *time.Time
	var until *time.Time
	_ = s.db.QueryRow(ctx, `select min(captured_at), max(captured_at) from graph_channel_policy_history`).Scan(&since, &until)
	out.CoverageSince = since
	out.CoverageUntil = until
	if since != nil && until != nil && until.After(*since) {
		out.CoverageDays = until.Sub(*since).Hours() / 24
	}

	bytesPerDay := 0.0
	sizingBytes := out.EstimatedLiveHistoryBytes
	if sizingBytes <= 0 {
		sizingBytes = out.HistoryBytes
	}
	if out.CoverageDays > 0 && sizingBytes > 0 {
		bytesPerDay = float64(sizingBytes) / out.CoverageDays
		out.BytesPerDay = int64(math.Round(bytesPerDay))
		out.GBPerDay = bytesPerDay / (1024 * 1024 * 1024)
	}
	out.EffectiveRetentionDays = graphExplorerEffectiveHistoryDays(cfg.HistoryRetentionDays, cfg.HistoryMaxBytes, bytesPerDay)
	out.EstimatedBytesAfterCleanup = graphExplorerProjectionBytes(bytesPerDay, out.EffectiveRetentionDays)
	out.CleanupAvailable = graphExplorerCleanupAvailable(out.CoverageDays, out.EffectiveRetentionDays, sizingBytes, cfg.HistoryMaxBytes)

	for _, days := range []int{7, 30, 60, 90} {
		out.Projections = append(out.Projections, GraphExplorerStorageProjection{
			Days:  days,
			Bytes: graphExplorerProjectionBytes(bytesPerDay, days),
		})
	}

	return out, nil
}

func (s *GraphExplorerService) estimatePolicyHistoryLiveBytes(ctx context.Context, physicalBytes int64, liveRows int64) int64 {
	if s == nil || s.db == nil || liveRows <= 0 {
		return 0
	}

	avgRowBytes := 0.0
	if err := s.db.QueryRow(ctx, `
select coalesce(avg(pg_column_size(h)::double precision), 0)
from (
  select *
  from graph_channel_policy_history tablesample system (0.5)
  limit 5000
) h
`).Scan(&avgRowBytes); err != nil || avgRowBytes <= 0 {
		_ = s.db.QueryRow(ctx, `
select coalesce(avg(pg_column_size(h)::double precision), 0)
from (
  select *
  from graph_channel_policy_history
  limit 5000
) h
`).Scan(&avgRowBytes)
	}
	if avgRowBytes <= 0 {
		return physicalBytes
	}

	estimated := int64(math.Round(avgRowBytes * graphExplorerHistoryStorageOverhead * float64(liveRows)))
	if estimated <= 0 {
		return 0
	}
	if physicalBytes > 0 && estimated > physicalBytes {
		return physicalBytes
	}
	return estimated
}

func (s *GraphExplorerService) policyHistoryCoverageSince(ctx context.Context) (*time.Time, error) {
	if s == nil || s.db == nil {
		return nil, ErrGraphExplorerDBUnavailable
	}
	var since *time.Time
	if err := s.db.QueryRow(ctx, `select min(captured_at) from graph_channel_policy_history`).Scan(&since); err != nil {
		return nil, err
	}
	return since, nil
}

func (s *GraphExplorerService) CleanupStorage(ctx context.Context, runVacuum bool) (GraphExplorerStorageCleanupResult, error) {
	var out GraphExplorerStorageCleanupResult
	status, err := s.StorageStatus(ctx)
	if err != nil {
		return out, err
	}

	effectiveDays := status.EffectiveRetentionDays
	if effectiveDays < graphExplorerMinHistoryRetentionDays {
		effectiveDays = graphExplorerMinHistoryRetentionDays
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -effectiveDays)
	out.CutoffAt = &cutoff

	removed, err := s.deletePolicyHistoryBefore(ctx, cutoff)
	if err != nil {
		return out, err
	}
	out.RowsRemoved = removed

	if runVacuum {
		out.VacuumRan = true
		if err := s.vacuumAnalyzePolicyHistory(ctx); err != nil {
			out.VacuumError = err.Error()
		}
	} else if removed > 0 {
		if err := s.analyzePolicyHistory(ctx); err != nil && s.logger != nil {
			s.logger.Printf("graph explorer policy history analyze failed: %v", err)
		}
	}

	nextStatus, statusErr := s.StorageStatus(ctx)
	if statusErr != nil {
		return out, statusErr
	}
	out.Status = nextStatus
	return out, nil
}

func (s *GraphExplorerService) deletePolicyHistoryBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, ErrGraphExplorerDBUnavailable
	}
	var total int64
	for iter := 0; iter < 1000; iter++ {
		tag, err := s.db.Exec(ctx, `
delete from graph_channel_policy_history
where ctid in (
  select ctid from graph_channel_policy_history
  where captured_at < $1
  limit $2
)
`, cutoff, graphExplorerPolicyHistoryPruneBatch)
		if err != nil {
			return total, err
		}
		removed := tag.RowsAffected()
		total += removed
		if removed < int64(graphExplorerPolicyHistoryPruneBatch) {
			break
		}
	}
	return total, nil
}

func (s *GraphExplorerService) vacuumAnalyzePolicyHistory(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrGraphExplorerDBUnavailable
	}
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	_, err = conn.Conn().PgConn().Exec(ctx, "vacuum (analyze) graph_channel_policy_history").ReadAll()
	if err != nil && strings.TrimSpace(err.Error()) == "" {
		return errors.New("vacuum analyze failed")
	}
	return err
}

func (s *GraphExplorerService) analyzePolicyHistory(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrGraphExplorerDBUnavailable
	}
	_, err := s.db.Exec(ctx, "analyze graph_channel_policy_history")
	return err
}
