package server

import (
  "context"
  "encoding/json"
  "errors"
  "net/http"
  "strconv"
  "strings"
  "time"
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

  ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
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
  limit := parseAutofeeLimit(r.URL.Query().Get("lines"))
  if limit <= 0 {
    limit = 50
  }
  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()
  rows, err := s.db.Query(ctx, `
select line, payload
from autofee_logs
order by coalesce(run_id, '0')::bigint desc, seq asc
limit $1
`, limit)
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
