
package server

import (
  "context"
  "encoding/json"
  "fmt"
  "net/http"
  "strconv"
  "strings"
  "time"
)

type rebalanceConfigPayload struct {
  AutoEnabled *bool `json:"auto_enabled,omitempty"`
  ScanIntervalSec *int `json:"scan_interval_sec,omitempty"`
  DeadbandPct *float64 `json:"deadband_pct,omitempty"`
  SourceMinLocalPct *float64 `json:"source_min_local_pct,omitempty"`
  EconRatio *float64 `json:"econ_ratio,omitempty"`
  ROIMin *float64 `json:"roi_min,omitempty"`
  DailyBudgetPct *float64 `json:"daily_budget_pct,omitempty"`
  MaxConcurrent *int `json:"max_concurrent,omitempty"`
  MinAmountSat *int64 `json:"min_amount_sat,omitempty"`
  MaxAmountSat *int64 `json:"max_amount_sat,omitempty"`
  FeeLadderSteps *int `json:"fee_ladder_steps,omitempty"`
  AmountProbeSteps *int `json:"amount_probe_steps,omitempty"`
  AmountProbeAdaptive *bool `json:"amount_probe_adaptive,omitempty"`
  AttemptTimeoutSec *int `json:"attempt_timeout_sec,omitempty"`
  RebalanceTimeoutSec *int `json:"rebalance_timeout_sec,omitempty"`
  PaybackModeFlags *int `json:"payback_mode_flags,omitempty"`
  UnlockDays *int `json:"unlock_days,omitempty"`
  CriticalReleasePct *float64 `json:"critical_release_pct,omitempty"`
  CriticalMinSources *int `json:"critical_min_sources,omitempty"`
  CriticalMinAvailableSats *int64 `json:"critical_min_available_sats,omitempty"`
  CriticalCycles *int `json:"critical_cycles,omitempty"`
}

type rebalanceRunPayload struct {
  ChannelID uint64 `json:"channel_id"`
  ChannelPoint string `json:"channel_point"`
  TargetOutboundPct *float64 `json:"target_outbound_pct,omitempty"`
}

type rebalanceChannelTargetPayload struct {
  ChannelID uint64 `json:"channel_id"`
  ChannelPoint string `json:"channel_point"`
  TargetOutboundPct *float64 `json:"target_outbound_pct,omitempty"`
}

type rebalanceChannelAutoPayload struct {
  ChannelID uint64 `json:"channel_id"`
  ChannelPoint string `json:"channel_point"`
  AutoEnabled bool `json:"auto_enabled"`
}

type rebalanceExcludePayload struct {
  ChannelID uint64 `json:"channel_id"`
  ChannelPoint string `json:"channel_point"`
  Excluded bool `json:"excluded"`
}

type rebalanceStopPayload struct {
  JobID int64 `json:"job_id"`
}

func (s *Server) handleRebalanceConfigGet(w http.ResponseWriter, r *http.Request) {
  if s.rebalance == nil {
    writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
    return
  }
  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()
  cfg, err := s.rebalance.GetConfig(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleRebalanceConfigPost(w http.ResponseWriter, r *http.Request) {
  if s.rebalance == nil {
    writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
    return
  }
  var payload rebalanceConfigPayload
  if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
    writeError(w, http.StatusBadRequest, "invalid payload")
    return
  }
  ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
  defer cancel()
  cfg, err := s.rebalance.GetConfig(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  if payload.AutoEnabled != nil {
    cfg.AutoEnabled = *payload.AutoEnabled
  }
  if payload.ScanIntervalSec != nil {
    cfg.ScanIntervalSec = *payload.ScanIntervalSec
  }
  if payload.DeadbandPct != nil {
    cfg.DeadbandPct = *payload.DeadbandPct
  }
  if payload.SourceMinLocalPct != nil {
    cfg.SourceMinLocalPct = *payload.SourceMinLocalPct
  }
  if payload.EconRatio != nil {
    cfg.EconRatio = *payload.EconRatio
  }
  if payload.ROIMin != nil {
    cfg.ROIMin = *payload.ROIMin
  }
  if payload.DailyBudgetPct != nil {
    cfg.DailyBudgetPct = *payload.DailyBudgetPct
  }
  if payload.MaxConcurrent != nil {
    cfg.MaxConcurrent = *payload.MaxConcurrent
  }
  if payload.MinAmountSat != nil {
    cfg.MinAmountSat = *payload.MinAmountSat
  }
  if payload.MaxAmountSat != nil {
    cfg.MaxAmountSat = *payload.MaxAmountSat
  }
  if payload.FeeLadderSteps != nil {
    cfg.FeeLadderSteps = *payload.FeeLadderSteps
  }
  if payload.AmountProbeSteps != nil {
    cfg.AmountProbeSteps = *payload.AmountProbeSteps
  }
  if payload.AmountProbeAdaptive != nil {
    cfg.AmountProbeAdaptive = *payload.AmountProbeAdaptive
  }
  if payload.AttemptTimeoutSec != nil {
    cfg.AttemptTimeoutSec = *payload.AttemptTimeoutSec
  }
  if payload.RebalanceTimeoutSec != nil {
    cfg.RebalanceTimeoutSec = *payload.RebalanceTimeoutSec
  }
  if payload.PaybackModeFlags != nil {
    cfg.PaybackModeFlags = *payload.PaybackModeFlags
  }
  if payload.UnlockDays != nil {
    cfg.UnlockDays = *payload.UnlockDays
  }
  if payload.CriticalReleasePct != nil {
    cfg.CriticalReleasePct = *payload.CriticalReleasePct
  }
  if payload.CriticalMinSources != nil {
    cfg.CriticalMinSources = *payload.CriticalMinSources
  }
  if payload.CriticalMinAvailableSats != nil {
    cfg.CriticalMinAvailableSats = *payload.CriticalMinAvailableSats
  }
  if payload.CriticalCycles != nil {
    cfg.CriticalCycles = *payload.CriticalCycles
  }

  updated, err := s.rebalance.UpdateConfig(ctx, cfg)
  if err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleRebalanceOverview(w http.ResponseWriter, r *http.Request) {
  if s.rebalance == nil {
    writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
    return
  }
  ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
  defer cancel()
  overview, err := s.rebalance.Overview(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, overview)
}

func (s *Server) handleRebalanceChannels(w http.ResponseWriter, r *http.Request) {
  if s.rebalance == nil {
    writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
    return
  }
  ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
  defer cancel()
  channels, err := s.rebalance.Channels(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

func (s *Server) handleRebalanceQueue(w http.ResponseWriter, r *http.Request) {
  if s.rebalance == nil {
    writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
    return
  }
  ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
  defer cancel()
  jobs, attempts, err := s.rebalance.Queue(ctx)
  if err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "attempts": attempts})
}

func (s *Server) handleRebalanceHistory(w http.ResponseWriter, r *http.Request) {
  if s.rebalance == nil {
    writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
    return
  }
  limit := 0
  if raw := r.URL.Query().Get("limit"); raw != "" {
    if parsed, err := strconv.Atoi(raw); err == nil {
      limit = parsed
    }
  }
  ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
  defer cancel()
  jobs, attempts, err := s.rebalance.History(ctx, limit)
  if err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "attempts": attempts})
}

func (s *Server) handleRebalanceRun(w http.ResponseWriter, r *http.Request) {
  if s.rebalance == nil {
    writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
    return
  }
  var payload rebalanceRunPayload
  if err := readJSON(r, &payload); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  if payload.ChannelID == 0 && strings.TrimSpace(payload.ChannelPoint) == "" {
    writeError(w, http.StatusBadRequest, "channel_id or channel_point required")
    return
  }
  ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
  defer cancel()
  resolvedID, resolvedPoint, err := s.rebalance.ResolveChannel(ctx, payload.ChannelID, payload.ChannelPoint)
  if err != nil {
    writeError(w, http.StatusBadRequest, err.Error())
    return
  }
  targetPct := payload.TargetOutboundPct
  if targetPct != nil {
    if *targetPct <= 0 || *targetPct > 100 {
      writeError(w, http.StatusBadRequest, "target_outbound_pct must be 1-100")
      return
    }
    _ = s.rebalance.SetChannelTarget(ctx, resolvedID, resolvedPoint, *targetPct)
  }
  jobID, err := s.rebalance.startJob(resolvedID, "manual", "", 0)
  if err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID})
}

func (s *Server) handleRebalanceStop(w http.ResponseWriter, r *http.Request) {
  if s.rebalance == nil {
    writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
    return
  }
  var payload rebalanceStopPayload
  if err := readJSON(r, &payload); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  if payload.JobID <= 0 {
    writeError(w, http.StatusBadRequest, "job_id required")
    return
  }
  s.rebalance.StopJob(payload.JobID)
  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRebalanceChannelTarget(w http.ResponseWriter, r *http.Request) {
  if s.rebalance == nil {
    writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
    return
  }
  var payload rebalanceChannelTargetPayload
  if err := readJSON(r, &payload); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  if payload.ChannelID == 0 && strings.TrimSpace(payload.ChannelPoint) == "" {
    writeError(w, http.StatusBadRequest, "channel_id or channel_point required")
    return
  }
  targetPct := payload.TargetOutboundPct
  if targetPct == nil || *targetPct <= 0 || *targetPct > 100 {
    writeError(w, http.StatusBadRequest, "channel_id and target_outbound_pct required")
    return
  }
  ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
  defer cancel()
  resolvedID, resolvedPoint, err := s.rebalance.ResolveChannel(ctx, payload.ChannelID, payload.ChannelPoint)
  if err != nil {
    writeError(w, http.StatusBadRequest, err.Error())
    return
  }
  if err := s.rebalance.SetChannelTarget(ctx, resolvedID, resolvedPoint, *targetPct); err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRebalanceChannelAuto(w http.ResponseWriter, r *http.Request) {
  if s.rebalance == nil {
    writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
    return
  }
  var payload rebalanceChannelAutoPayload
  if err := readJSON(r, &payload); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
  defer cancel()
  resolvedID, resolvedPoint, err := s.rebalance.ResolveChannel(ctx, payload.ChannelID, payload.ChannelPoint)
  if err != nil {
    writeError(w, http.StatusBadRequest, err.Error())
    return
  }
  if err := s.rebalance.SetChannelAuto(ctx, resolvedID, resolvedPoint, payload.AutoEnabled); err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRebalanceExclude(w http.ResponseWriter, r *http.Request) {
  if s.rebalance == nil {
    writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
    return
  }
  var payload rebalanceExcludePayload
  if err := readJSON(r, &payload); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }
  ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
  defer cancel()
  resolvedID, resolvedPoint, err := s.rebalance.ResolveChannel(ctx, payload.ChannelID, payload.ChannelPoint)
  if err != nil {
    writeError(w, http.StatusBadRequest, err.Error())
    return
  }
  if err := s.rebalance.SetSourceExcluded(ctx, resolvedID, resolvedPoint, payload.Excluded); err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRebalanceStream(w http.ResponseWriter, r *http.Request) {
  if s.rebalance == nil {
    writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
    return
  }
  flusher, ok := w.(http.Flusher)
  if !ok {
    writeError(w, http.StatusInternalServerError, "stream not supported")
    return
  }

  w.Header().Set("Content-Type", "text/event-stream")
  w.Header().Set("Cache-Control", "no-cache")
  w.Header().Set("Connection", "keep-alive")

  ch := s.rebalance.Subscribe()
  defer s.rebalance.Unsubscribe(ch)

  _, _ = w.Write([]byte("event: ready\ndata: {}\n\n"))
  flusher.Flush()

  ticker := time.NewTicker(25 * time.Second)
  defer ticker.Stop()

  for {
    select {
    case <-r.Context().Done():
      return
    case evt := <-ch:
      payload, err := json.Marshal(evt)
      if err != nil {
        continue
      }
      _, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
      flusher.Flush()
    case <-ticker.C:
      _, _ = w.Write([]byte("event: heartbeat\ndata: {}\n\n"))
      flusher.Flush()
    }
  }
}

