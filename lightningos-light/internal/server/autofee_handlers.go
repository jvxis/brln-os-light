package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) handleAutofeeConfigGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.autofeeService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "autofee unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cfg, err := svc.GetConfig(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleAutofeeConfigPost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.autofeeService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "autofee unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	var req AutofeeConfigUpdate
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	prevCfg, err := svc.GetConfig(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cfg, err := svc.UpdateConfig(ctx, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if prevCfg.OperationMode != cfg.OperationMode {
		syncCtx, syncCancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer syncCancel()
		if err := s.syncAutofeeOperationMode(syncCtx, svc, prevCfg.OperationMode, cfg.OperationMode); err != nil {
			prevMode := prevCfg.OperationMode
			_, _ = svc.UpdateConfig(ctx, AutofeeConfigUpdate{OperationMode: &prevMode})
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) syncAutofeeOperationMode(ctx context.Context, svc *AutofeeService, previousMode string, nextMode string) error {
	previousMode = normalizeAutofeeOperationMode(previousMode)
	nextMode = normalizeAutofeeOperationMode(nextMode)
	if previousMode == nextMode {
		return nil
	}
	s.initRebalance()
	if s.rebalance == nil {
		if s.rebalanceErr != "" {
			return errors.New(s.rebalanceErr)
		}
		return errors.New("rebalance unavailable")
	}
	rebCfg, err := s.rebalance.GetConfig(ctx)
	if err != nil {
		return err
	}
	switch {
	case nextMode == autofeeOperationModeMarketRefill:
		if err := svc.SaveMarketRefillRebalanceBackup(ctx, rebCfg.AutoEnabled, rebCfg.ManualRestartWatch); err != nil {
			return err
		}
		if err := svc.SaveMarketRefillFeeSnapshot(ctx); err != nil {
			_ = svc.ClearMarketRefillRebalanceBackup(ctx)
			return err
		}
		rebCfg.AutoEnabled = false
		rebCfg.ManualRestartWatch = false
		if _, err = s.rebalance.UpdateConfig(ctx, rebCfg); err != nil {
			_ = svc.ClearMarketRefillFeeSnapshot(ctx)
			_ = svc.ClearMarketRefillRebalanceBackup(ctx)
			return err
		}
		return nil
	case previousMode == autofeeOperationModeMarketRefill:
		if err := svc.RestoreMarketRefillFeeSnapshot(ctx); err != nil {
			return err
		}
		saved, autoEnabled, manualRestartWatch, err := svc.LoadMarketRefillRebalanceBackup(ctx)
		if err != nil {
			return err
		}
		if !saved {
			return svc.ClearMarketRefillFeeSnapshot(ctx)
		}
		rebCfg.AutoEnabled = autoEnabled
		rebCfg.ManualRestartWatch = manualRestartWatch
		if _, err := s.rebalance.UpdateConfig(ctx, rebCfg); err != nil {
			return err
		}
		if err := svc.ClearMarketRefillRebalanceBackup(ctx); err != nil {
			return err
		}
		return svc.ClearMarketRefillFeeSnapshot(ctx)
	default:
		return nil
	}
}

func (s *Server) handleAutofeeChannelsGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.autofeeService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "autofee unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	settings, err := svc.LoadChannelSettingsDetailed(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	payload := []map[string]any{}
	for _, entry := range settings {
		payload = append(payload, map[string]any{
			"channel_id":     entry.ChannelID,
			"channel_id_str": strconv.FormatUint(entry.ChannelID, 10),
			"channel_point":  entry.ChannelPoint,
			"enabled":        entry.Enabled,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"settings": payload})
}

func (s *Server) handleAutofeeChannelsPost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.autofeeService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "autofee unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	var req struct {
		ApplyAll     bool    `json:"apply_all"`
		Enabled      *bool   `json:"enabled"`
		ChannelID    *uint64 `json:"channel_id"`
		ChannelPoint string  `json:"channel_point"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if req.ApplyAll {
		if err := svc.SetAllChannelsEnabled(ctx, *req.Enabled); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	if req.ChannelID == nil && strings.TrimSpace(req.ChannelPoint) == "" {
		writeError(w, http.StatusBadRequest, "channel_id or channel_point required")
		return
	}
	channelID := uint64(0)
	if req.ChannelID != nil {
		channelID = *req.ChannelID
	}
	if err := svc.SetChannelEnabled(ctx, channelID, req.ChannelPoint, *req.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAutofeeRun(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.autofeeService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "autofee unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}
	var req struct {
		DryRun         bool `json:"dry_run"`
		IncludeInbound bool `json:"include_inbound"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	// Manual non-dry runs can touch many channels and exceed short HTTP deadlines.
	// Keep request cancellation semantics, but allow enough time for the full batch.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	if err := svc.Run(ctx, req.DryRun, "manual"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAutofeeRefresh(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.autofeeService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "autofee unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	var req struct {
		DryRun         bool   `json:"dry_run"`
		IncludeInbound bool   `json:"include_inbound"`
		ChannelPoint   string `json:"channel_point"`
		ChannelID      uint64 `json:"channel_id"`
		ChannelIDStr   string `json:"channel_id_str"`
	}
	if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	channelID := req.ChannelID
	if raw := strings.TrimSpace(req.ChannelIDStr); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid channel_id_str")
			return
		}
		channelID = parsed
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	var result AutofeeRefreshResult
	var err error
	if autofeeRefreshTargetSpecified(req.ChannelPoint, channelID) {
		result, err = svc.RefreshReferenceFeesForChannel(ctx, req.DryRun, req.IncludeInbound, req.ChannelPoint, channelID)
	} else {
		result, err = svc.RefreshReferenceFees(ctx, req.DryRun, req.IncludeInbound)
	}
	if err != nil {
		if errors.Is(err, errAutofeeRefreshChannelNotFound) {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAutofeeStatus(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.autofeeService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "autofee unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}
	writeJSON(w, http.StatusOK, svc.Status())
}

// Hook into /api/logs when service=autofee
func (s *Server) readAutofeeLogLines(ctx context.Context, limit int) ([]string, error) {
	if s.db == nil {
		return nil, errors.New("db unavailable")
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(ctx, `
select max(occurred_at) as occurred_at,
  max(case when seq = 1 then line else '' end) as summary,
  max(case when seq = 2 then line else '' end) as seed
from autofee_logs
where seq in (1,2)
group by run_id
order by max(id) desc
limit $1
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	lines := []string{}
	for rows.Next() {
		var ts time.Time
		var summary string
		var seed string
		if err := rows.Scan(&ts, &summary, &seed); err != nil {
			return nil, err
		}
		line := ts.Local().Format(time.RFC3339) + " | " + strings.TrimSpace(summary)
		if strings.TrimSpace(seed) != "" {
			line = line + " | " + strings.TrimSpace(seed)
		}
		lines = append(lines, line)
	}
	return lines, rows.Err()
}

func (s *Server) handleAutofeeResults(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.autofeeService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "autofee unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}
	query := r.URL.Query()
	runs := parseAutofeeRuns(query.Get("runs"))
	fromTime, fromOk := parseAutofeeTime(query.Get("from"))
	toTime, toOk := parseAutofeeTime(query.Get("to"))
	useRuns := runs > 0 || query.Get("runs") != "" || query.Get("from") != "" || query.Get("to") != ""
	if useRuns && runs <= 0 {
		runs = 4
	}
	limit := 0
	if !useRuns {
		limit = parseAutofeeLimit(query.Get("lines"))
		if limit <= 0 {
			limit = 50
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var rows pgx.Rows
	var err error
	if useRuns {
		conditions := []string{"run_id is not null"}
		args := []any{}
		if fromOk {
			conditions = append(conditions, fmt.Sprintf("occurred_at >= $%d", len(args)+1))
			args = append(args, fromTime)
		}
		if toOk {
			conditions = append(conditions, fmt.Sprintf("occurred_at <= $%d", len(args)+1))
			args = append(args, toTime)
		}
		whereClause := strings.Join(conditions, " and ")
		limitArg := len(args) + 1
		args = append(args, runs)
		sql := fmt.Sprintf(`
with selected_runs as (
  select run_id, max(occurred_at) as last_at
  from autofee_logs
  where %s
  group by run_id
  order by max(occurred_at) desc
  limit $%d
)
select l.line, l.payload
from autofee_logs l
join selected_runs r on l.run_id = r.run_id
order by r.last_at desc, l.seq asc
`, whereClause, limitArg)
		rows, err = s.db.Query(ctx, sql, args...)
	} else {
		rows, err = s.db.Query(ctx, `
select line, payload
from autofee_logs
order by coalesce(run_id, '0')::bigint desc, seq asc
limit $1
`, limit)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []string{}
	items := []map[string]any{}
	for rows.Next() {
		var line string
		var raw []byte
		if err := rows.Scan(&line, &raw); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, line)
		var item map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &item); err != nil {
				item = nil
			}
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": out, "items": items})
}

func parseAutofeeLimit(raw string) int {
	if raw == "" {
		return 200
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 200
	}
	return v
}

func parseAutofeeRuns(raw string) int {
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	if v < 1 {
		return 0
	}
	if v > 50 {
		return 50
	}
	return v
}

func parseAutofeeTime(raw string) (time.Time, bool) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts, true
	}
	if ts, err := time.ParseInLocation("2006-01-02T15:04", raw, time.Local); err == nil {
		return ts, true
	}
	if ts, err := time.ParseInLocation("2006-01-02 15:04", raw, time.Local); err == nil {
		return ts, true
	}
	return time.Time{}, false
}

func (s *Server) handleAutofeeOutcomesGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.autofeeService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "autofee unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "db unavailable")
		return
	}

	q := r.URL.Query()
	now := time.Now().UTC()
	until := now
	if ts, ok := parseAutofeeTime(q.Get("until")); ok {
		until = ts
	}
	since := until.Add(-30 * 24 * time.Hour)
	if ts, ok := parseAutofeeTime(q.Get("since")); ok {
		since = ts
	}
	if since.After(until) {
		writeError(w, http.StatusBadRequest, "since must be before until")
		return
	}

	kindFilter := strings.ToLower(strings.TrimSpace(q.Get("kind")))
	switch kindFilter {
	case "", "outbound", "inbound":
	default:
		writeError(w, http.StatusBadRequest, "kind must be outbound or inbound")
		return
	}

	limit := 100
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	totals := map[string]int{}
	totalsRows, err := s.db.Query(ctx, `
select measurement_status, count(*)
from autofee_outcomes
where decided_at >= $1 and decided_at <= $2
group by measurement_status
`, since, until)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for totalsRows.Next() {
		var status string
		var count int
		if err := totalsRows.Scan(&status, &count); err != nil {
			totalsRows.Close()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		totals[status] = count
	}
	totalsRows.Close()
	if err := totalsRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type aggRow struct {
		Key           string  `json:"key"`
		Count         int     `json:"count"`
		AvgFwd24h     float64 `json:"avg_fwd_24h"`
		AvgAmtMsat24h float64 `json:"avg_amt_msat_24h"`
		AvgFeeMsat24h float64 `json:"avg_fee_msat_24h"`
		AvgPpmDelta   float64 `json:"avg_ppm_delta"`
	}

	byTag := []aggRow{}
	tagRows, err := s.db.Query(ctx, `
select tag,
  count(*)::bigint,
  coalesce(avg(fwd_count_24h_after), 0)::float8,
  coalesce(avg(fwd_amt_msat_24h_after), 0)::float8,
  coalesce(avg(fwd_fee_msat_24h_after), 0)::float8,
  coalesce(avg(new_ppm - prev_ppm), 0)::float8
from autofee_outcomes,
     jsonb_array_elements_text(tags) as tag
where decided_at >= $1 and decided_at <= $2
  and measurement_status='measured'
group by tag
order by count(*) desc
limit 50
`, since, until)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for tagRows.Next() {
		var row aggRow
		if err := tagRows.Scan(&row.Key, &row.Count, &row.AvgFwd24h, &row.AvgAmtMsat24h, &row.AvgFeeMsat24h, &row.AvgPpmDelta); err != nil {
			tagRows.Close()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		byTag = append(byTag, row)
	}
	tagRows.Close()
	if err := tagRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	byClass := []aggRow{}
	classRows, err := s.db.Query(ctx, `
select coalesce(nullif(class_label, ''), 'unknown'),
  count(*)::bigint,
  coalesce(avg(fwd_count_24h_after), 0)::float8,
  coalesce(avg(fwd_amt_msat_24h_after), 0)::float8,
  coalesce(avg(fwd_fee_msat_24h_after), 0)::float8,
  coalesce(avg(new_ppm - prev_ppm), 0)::float8
from autofee_outcomes
where decided_at >= $1 and decided_at <= $2
  and measurement_status='measured'
group by coalesce(nullif(class_label, ''), 'unknown')
order by count(*) desc
`, since, until)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for classRows.Next() {
		var row aggRow
		if err := classRows.Scan(&row.Key, &row.Count, &row.AvgFwd24h, &row.AvgAmtMsat24h, &row.AvgFeeMsat24h, &row.AvgPpmDelta); err != nil {
			classRows.Close()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		byClass = append(byClass, row)
	}
	classRows.Close()
	if err := classRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	listRows, err := s.db.Query(ctx, `
select id, run_id, channel_id, channel_point, kind, decided_at,
  prev_ppm, new_ppm, target_ppm, floor_ppm, floor_src,
  class_label, tags,
  out_ratio_pre, out_ppm7d_pre, margin_ppm7d_pre, fwd_count_7d_pre,
  measured_at, measurement_status, measurement_error,
  fwd_count_24h_after, fwd_amt_msat_24h_after, fwd_fee_msat_24h_after,
  rebal_amt_msat_24h_after, rebal_fee_msat_24h_after
from autofee_outcomes
where decided_at >= $1 and decided_at <= $2
  and ($3 = '' or kind = $3)
order by decided_at desc
limit $4
`, since, until, kindFilter, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer listRows.Close()

	items := []map[string]any{}
	for listRows.Next() {
		var (
			id              int64
			runID           string
			channelID       int64
			channelPoint    string
			kind            string
			decidedAt       time.Time
			prevPpm         int
			newPpm          int
			targetPpm       *int
			floorPpm        *int
			floorSrc        *string
			classLabel      *string
			tagsRaw         []byte
			outRatioPre     *float64
			outPpm7dPre     *int
			marginPpm7dPre  *int
			fwdCount7dPre   *int
			measuredAt      *time.Time
			status          string
			measurementErr  *string
			fwdCount24h     *int
			fwdAmtMsat24h   *int64
			fwdFeeMsat24h   *int64
			rebalAmtMsat24h *int64
			rebalFeeMsat24h *int64
		)
		if err := listRows.Scan(
			&id, &runID, &channelID, &channelPoint, &kind, &decidedAt,
			&prevPpm, &newPpm, &targetPpm, &floorPpm, &floorSrc,
			&classLabel, &tagsRaw,
			&outRatioPre, &outPpm7dPre, &marginPpm7dPre, &fwdCount7dPre,
			&measuredAt, &status, &measurementErr,
			&fwdCount24h, &fwdAmtMsat24h, &fwdFeeMsat24h,
			&rebalAmtMsat24h, &rebalFeeMsat24h,
		); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var tagsJSON any
		if len(tagsRaw) > 0 {
			_ = json.Unmarshal(tagsRaw, &tagsJSON)
		}
		row := map[string]any{
			"id":                  id,
			"run_id":              runID,
			"channel_id":          channelID,
			"channel_point":       channelPoint,
			"kind":                kind,
			"decided_at":          decidedAt.Format(time.RFC3339),
			"prev_ppm":            prevPpm,
			"new_ppm":             newPpm,
			"target_ppm":          targetPpm,
			"floor_ppm":           floorPpm,
			"floor_src":           floorSrc,
			"class_label":         classLabel,
			"tags":                tagsJSON,
			"out_ratio_pre":       outRatioPre,
			"out_ppm7d_pre":       outPpm7dPre,
			"margin_ppm7d_pre":    marginPpm7dPre,
			"fwd_count_7d_pre":    fwdCount7dPre,
			"measurement_status":  status,
			"measurement_error":   measurementErr,
			"fwd_count_24h_after": fwdCount24h,
			"fwd_amt_msat_24h":    fwdAmtMsat24h,
			"fwd_fee_msat_24h":    fwdFeeMsat24h,
			"rebal_amt_msat_24h":  rebalAmtMsat24h,
			"rebal_fee_msat_24h":  rebalFeeMsat24h,
		}
		if measuredAt != nil {
			row["measured_at"] = measuredAt.Format(time.RFC3339)
		}
		items = append(items, row)
	}
	if err := listRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"since":    since.Format(time.RFC3339),
		"until":    until.Format(time.RFC3339),
		"window":   "24h",
		"totals":   totals,
		"by_tag":   byTag,
		"by_class": byClass,
		"items":    items,
	})
}

func (s *Server) handleAutofeeOutcomesMeasureNow(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.autofeeService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "autofee unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if err := svc.MeasureOutcomes(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
