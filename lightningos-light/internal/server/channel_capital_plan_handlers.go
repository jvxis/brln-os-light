package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

func (s *Server) handleChannelCapitalPlanGet(w http.ResponseWriter, r *http.Request) {
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
	items, status, err := svc.List(ctx, 500, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel capital plan")
		return
	}

	commitments, magmaStateKnown := s.channelCapitalPlanCommitments(ctx)
	plan := buildChannelCapitalPlan(items, commitments, magmaStateKnown)
	writeJSON(w, http.StatusOK, map[string]any{
		"available":         status.Available,
		"last_sync_at":      status.LastSyncAt,
		"state_counts":      status.StateCounts,
		"magma_state_known": magmaStateKnown,
		"summary":           plan.Summary,
		"items":             plan.Items,
	})
}

func (s *Server) channelCapitalPlanCommitments(ctx context.Context) ([]MagmaChannelCommitment, bool) {
	svc, _ := s.magmaService()
	if svc == nil {
		return nil, false
	}
	items, err := svc.ActiveCommitments(ctx)
	if err != nil {
		return nil, false
	}
	return items, true
}

func (s *Server) activeMagmaCommitmentForChannel(ctx context.Context, channelPoint string) (*MagmaChannelCommitment, error) {
	svc, reason := s.magmaService()
	if svc == nil {
		if strings.TrimSpace(reason) == "" {
			reason = "Magma commitment state unavailable"
		}
		return nil, errors.New(reason)
	}
	items, err := svc.ActiveCommitments(ctx)
	if err != nil {
		return nil, err
	}
	wanted := strings.ToLower(strings.TrimSpace(channelPoint))
	for _, item := range items {
		if strings.ToLower(strings.TrimSpace(item.ChannelPoint)) == wanted {
			copy := item
			return &copy, nil
		}
	}
	return nil, nil
}
