package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleAuditEventsList(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.auditLogService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	limit, err := parseAuditEventsLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid limit")
		return
	}

	filter := AuditEventFilter{
		Action:    strings.TrimSpace(r.URL.Query().Get("action")),
		SessionID: strings.TrimSpace(r.URL.Query().Get("session_id")),
		Target:    strings.TrimSpace(r.URL.Query().Get("target")),
		Limit:     limit,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	items, err := svc.List(ctx, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"limit": normalizeAuditEventsLimit(limit),
	})
}

func parseAuditEventsLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return auditEventsDefaultLimit, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return 0, strconv.ErrSyntax
	}
	return normalizeAuditEventsLimit(parsed), nil
}
