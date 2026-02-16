package server

import (
  "context"
  "encoding/json"
  "errors"
  "fmt"
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

  cfg, err := svc.UpdateConfig(ctx, req)
  if err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, cfg)
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

  settings, err := svc.LoadChannelSettings(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }

  payload := []map[string]any{}
  for channelID, enabled := range settings {
    payload = append(payload, map[string]any{
      "channel_id": channelID,
      "enabled": enabled,
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
    ApplyAll bool `json:"apply_all"`
    Enabled *bool `json:"enabled"`
    ChannelID *uint64 `json:"channel_id"`
    ChannelPoint string `json:"channel_point"`
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
    DryRun bool `json:"dry_run"`
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
