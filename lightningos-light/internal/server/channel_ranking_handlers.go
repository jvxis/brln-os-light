package server

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleChannelRankingGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.channelRankingService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "channel ranking unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	state := normalizeChannelRankingState(r.URL.Query().Get("state"))

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	items, status, err := svc.List(ctx, limit, state)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel ranking")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available":    status.Available,
		"last_sync_at": status.LastSyncAt,
		"state_counts": status.StateCounts,
		"items":        items,
	})
}

func (s *Server) handleChannelRankingItemGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.channelRankingService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "channel ranking unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	channelPoint := strings.TrimSpace(chi.URLParam(r, "channel_point"))
	if decoded, err := url.PathUnescape(channelPoint); err == nil {
		channelPoint = strings.TrimSpace(decoded)
	}
	if channelPoint == "" {
		writeError(w, http.StatusBadRequest, "channel_point required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	detail, err := svc.GetDetail(ctx, channelPoint)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel ranking not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"item":                   detail.Item,
		"history":                detail.History,
		"peer_channels":          detail.PeerChannels,
		"top_forward_in_sources": detail.TopForwardInSources,
		"top_forward_out_sinks":  detail.TopForwardOutSinks,
		"feedback":               detail.Feedback,
	})
}

func (s *Server) handleChannelRankingRecomputePost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.channelRankingService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "channel ranking unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := svc.Refresh(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to recompute channel ranking")
		return
	}
	status, err := svc.Status(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel ranking status")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"last_sync_at": status.LastSyncAt,
		"state_counts": status.StateCounts,
	})
}
