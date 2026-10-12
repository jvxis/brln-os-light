package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

func (s *Server) handleLNChannelAutomationPost(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "postgres unavailable")
		return
	}

	var req struct {
		ChannelID       uint64 `json:"channel_id"`
		ChannelPoint    string `json:"channel_point"`
		Mode            string `json:"automation_mode"`
		FixedFeePPM     *int64 `json:"fixed_fee_ppm"`
		ReviewAt        string `json:"review_at"`
		Note            string `json:"automation_note"`
		RestorePrevious bool   `json:"restore_previous"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	mode := normalizeChannelAutomationMode(req.Mode)
	if mode == "" {
		writeError(w, http.StatusBadRequest, "invalid automation_mode")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	channel, err := s.resolveLNChannelForAutomation(ctx, req.ChannelID, req.ChannelPoint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fixedFee := req.FixedFeePPM
	if mode == channelAutomationModeParked && fixedFee == nil && channel.FeeRatePpm != nil {
		current := *channel.FeeRatePpm
		if current >= 0 {
			fixedFee = &current
		}
	}
	reviewAt, err := parseChannelAutomationReviewAt(req.ReviewAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	policy, err := setChannelAutomationPolicy(ctx, s.db, setChannelAutomationParams{
		ChannelID:       channel.ChannelID,
		ChannelPoint:    channel.ChannelPoint,
		Mode:            mode,
		FixedFeePPM:     fixedFee,
		ReviewAt:        reviewAt,
		Note:            req.Note,
		RestorePrevious: req.RestorePrevious,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if mode == channelAutomationModeParked {
		s.clearInboundDiscountOnPark(ctx, channel.ChannelPoint)
	}

	s.recordAuditEvent(r, "channel.automation_mode", channel.ChannelPoint, map[string]any{
		"channel_id":       channel.ChannelID,
		"automation_mode":  policy.Mode,
		"fixed_fee_ppm":    policy.FixedFeePPM,
		"review_at":        policy.ReviewAt,
		"restore_previous": req.RestorePrevious,
		"status":           "success",
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "policy": policy})
}

// parkedInboundPolicyUpdate: a parked channel leaves AutoFee, so the inbound
// discount AutoFee negotiated stays frozen in LND at its last value. Parking
// clears it; the outbound fee is left as it is.
func parkedInboundPolicyUpdate(policy lndclient.ChannelPolicy) (bool, int64) {
	if policy.InboundFeeRatePpm >= 0 && policy.InboundBaseMsat >= 0 {
		return false, 0
	}
	return true, 0
}

func (s *Server) clearInboundDiscountOnPark(ctx context.Context, channelPoint string) {
	if s == nil || s.lnd == nil || strings.TrimSpace(channelPoint) == "" {
		return
	}
	policy, err := s.lnd.GetChannelPolicy(ctx, channelPoint)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("channel automation: park %s: policy unavailable, inbound discount kept: %v", channelPoint, err)
		}
		return
	}
	update, inboundRate := parkedInboundPolicyUpdate(policy)
	if !update {
		return
	}
	timeLock := policy.TimeLockDelta
	if timeLock <= 0 {
		timeLock = 144
	}
	ctx = lndclient.WithPolicyObservationSource(ctx, "channel-automation")
	if err := s.lnd.UpdateChannelFees(ctx, channelPoint, false, policy.BaseFeeMsat, policy.FeeRatePpm, timeLock, true, 0, inboundRate); err != nil && s.logger != nil {
		s.logger.Printf("channel automation: park %s: inbound discount reset failed: %v", channelPoint, err)
	}
}

func (s *Server) resolveLNChannelForAutomation(ctx context.Context, channelID uint64, channelPoint string) (lndclient.ChannelInfo, error) {
	if s == nil || s.lnd == nil {
		return lndclient.ChannelInfo{}, errors.New("lnd unavailable")
	}
	channelPoint = strings.TrimSpace(channelPoint)
	if channelID == 0 && channelPoint == "" {
		return lndclient.ChannelInfo{}, errors.New("channel_id or channel_point required")
	}
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return lndclient.ChannelInfo{}, err
	}
	for _, ch := range channels {
		if channelID != 0 && ch.ChannelID == channelID {
			return ch, nil
		}
		if channelPoint != "" && strings.EqualFold(strings.TrimSpace(ch.ChannelPoint), channelPoint) {
			return ch, nil
		}
	}
	return lndclient.ChannelInfo{}, errors.New("channel not found")
}

func parseChannelAutomationReviewAt(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	if parsed, err := time.Parse("2006-01-02", raw); err == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	return nil, errors.New("review_at must be RFC3339 or YYYY-MM-DD")
}
