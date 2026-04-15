package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleChannelOpenCandidatesGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.channelOpenCandidatesService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "channel open candidates unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	items, status, err := svc.List(ctx, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel open candidates")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"available":       status.Available,
		"last_sync_at":    status.LastSyncAt,
		"last_error":      status.LastError,
		"candidate_count": status.CandidateCount,
		"items":           items,
	})
}

func (s *Server) handleChannelOpenCandidatesRecomputePost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.channelOpenCandidatesService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "channel open candidates unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), channelOpenCandidatesRefreshTimeout)
	defer cancel()
	if err := svc.Refresh(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to recompute channel open candidates")
		return
	}

	status, err := svc.Status(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel open candidates status")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"last_sync_at":    status.LastSyncAt,
		"last_error":      status.LastError,
		"candidate_count": status.CandidateCount,
	})
}
