package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) automationIntentService() (*AutomationIntentService, string) {
	s.initRebalance()
	if s.automationIntents == nil {
		return nil, "automation intent interlock unavailable"
	}
	return s.automationIntents, ""
}

func (s *Server) handleAutomationIntentConfigGet(w http.ResponseWriter, r *http.Request) {
	svc, reason := s.automationIntentService()
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": reason})
		return
	}
	cfg, err := svc.GetConfig(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleAutomationIntentConfigPost(w http.ResponseWriter, r *http.Request) {
	svc, reason := s.automationIntentService()
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": reason})
		return
	}
	var payload AutomationIntentConfigUpdate
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON payload"})
		return
	}
	cfg, err := svc.UpdateConfig(r.Context(), payload)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.recordAuditEvent(r, "automation_interlock.config", "autofee-rebalance", map[string]any{
		"mode":                    cfg.Mode,
		"refill_score_multiplier": cfg.RefillScoreMultiplier,
		"min_confidence":          cfg.MinConfidence,
	})
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleAutomationIntentsGet(w http.ResponseWriter, r *http.Request) {
	svc, reason := s.automationIntentService()
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": reason})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	activeOnly := !strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("active")), "false")
	items, err := svc.List(r.Context(), strings.TrimSpace(r.URL.Query().Get("consumer")),
		strings.TrimSpace(r.URL.Query().Get("kind")), activeOnly, limit, time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAutomationIntentHistoryGet(w http.ResponseWriter, r *http.Request) {
	svc, reason := s.automationIntentService()
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": reason})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := svc.History(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
