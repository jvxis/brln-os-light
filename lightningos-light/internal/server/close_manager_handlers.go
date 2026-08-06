package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/go-chi/chi/v5"
)

type closeManagerMempoolTxStatus struct {
	Status struct {
		Confirmed bool  `json:"confirmed"`
		BlockTime int64 `json:"block_time"`
	} `json:"status"`
}

type closeManagerExternalTxCheck struct {
	Seen      bool
	Confirmed bool
	BlockTime *time.Time
}

func (s *Server) handleCloseManagerStatusGet(w http.ResponseWriter, r *http.Request) {
	svc, svcErr := s.closeManagerService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, svcErr)
		return
	}
	ctx := r.Context()
	status, err := svc.GetStatus(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCloseManagerSessionsGet(w http.ResponseWriter, r *http.Request) {
	svc, svcErr := s.closeManagerService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, svcErr)
		return
	}
	ctx := r.Context()
	limit := closeManagerDefaultListLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	items, err := svc.ListSessions(ctx, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.enrichCloseManagerSessionsWithMempool(ctx, items)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCloseManagerSessionGet(w http.ResponseWriter, r *http.Request) {
	svc, svcErr := s.closeManagerService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, svcErr)
		return
	}
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid close session id")
		return
	}
	item, err := svc.GetSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "close session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.enrichCloseManagerSessionWithMempool(ctx, item, map[string]*closeManagerExternalTxCheck{})
	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}

func (s *Server) handleCloseManagerSessionEventsGet(w http.ResponseWriter, r *http.Request) {
	svc, svcErr := s.closeManagerService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, svcErr)
		return
	}
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid close session id")
		return
	}
	limit := closeManagerDefaultEventLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	items, err := svc.ListEvents(ctx, id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCloseManagerSessionRecoverPost(w http.ResponseWriter, r *http.Request) {
	svc, svcErr := s.closeManagerService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, svcErr)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid close session id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	session, err := svc.GetSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "close session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if strings.TrimSpace(session.ChannelPoint) == "" {
		writeError(w, http.StatusBadRequest, "session missing channel_point")
		return
	}

	txid, attempted, recoverErr := s.lnd.RecoverWaitingCloseTx(ctx, session.ChannelPoint)
	if recoverErr != nil {
		if s.notifier != nil {
			s.notifier.updateWaitingCloseRecoveryResult(session.ChannelPoint, "recover_failed", recoverErr.Error(), "")
		}
		_ = svc.insertEvent(ctx, session.ID, "recover_failed", "warn", map[string]any{
			"channel_point": session.ChannelPoint,
			"error":         recoverErr.Error(),
		})
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(recoverErr))
		return
	}

	result := "no_raw_tx_available"
	if strings.TrimSpace(txid) != "" && attempted {
		result = "rebroadcast_ok"
	} else if strings.TrimSpace(txid) != "" {
		result = "closing_txid_detected"
	} else if attempted {
		result = "recovery_submitted_no_txid"
	}
	if s.notifier != nil {
		s.notifier.updateWaitingCloseRecoveryResult(session.ChannelPoint, result, "", strings.TrimSpace(txid))
	}
	_ = svc.insertEvent(ctx, session.ID, "recover_triggered", "info", map[string]any{
		"channel_point": session.ChannelPoint,
		"attempted":     attempted,
		"result":        result,
		"closing_txid":  strings.TrimSpace(txid),
	})
	if err := svc.RefreshNow(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := svc.GetSession(ctx, session.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"attempted":    attempted,
		"result":       result,
		"closing_txid": strings.TrimSpace(txid),
		"item":         updated,
	})
}

func (s *Server) handleCloseManagerSessionForceClosePost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := readOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}

	svc, svcErr := s.closeManagerService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, svcErr)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid close session id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	session, err := svc.GetSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "close session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if strings.TrimSpace(session.ChannelPoint) == "" {
		writeError(w, http.StatusBadRequest, "session missing channel_point")
		return
	}

	if session.Source == "node_retirement" && strings.TrimSpace(session.SourceRef) != "" {
		nodeSvc, nodeSvcErr := s.nodeRetirementService()
		if nodeSvc == nil {
			writeError(w, http.StatusServiceUnavailable, nodeSvcErr)
			return
		}
		if err := nodeSvc.SetChannelDecision(ctx, session.SourceRef, session.ChannelPoint, nodeRetirementDecisionForceClose); err != nil {
			s.recordAuditEventAsync(r, "channel.close_manager_force.failed", session.ChannelPoint, map[string]any{"delegated": true})
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		_ = svc.insertEvent(ctx, session.ID, "force_close_delegated_to_node_retirement", "info", map[string]any{
			"channel_point": session.ChannelPoint,
			"session_id":    session.SourceRef,
		})
		if err := svc.RefreshNow(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updated, _ := svc.GetSession(ctx, session.ID)
		s.recordAuditEventAsync(r, "channel.close_manager_force.submitted", session.ChannelPoint, map[string]any{
			"delegated":  true,
			"session_id": session.ID,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"delegated":  true,
			"item":       updated,
			"message":    "force close decision saved in node retirement",
			"channel_id": session.SourceRef,
		})
		return
	}

	txid, closeErr := s.lnd.CloseChannel(ctx, session.ChannelPoint, true, 0)
	if closeErr != nil {
		s.recordAuditEventAsync(r, "channel.close_manager_force.failed", session.ChannelPoint, map[string]any{
			"delegated":  false,
			"session_id": session.ID,
		})
		_ = svc.insertEvent(ctx, session.ID, "force_close_failed", "warn", map[string]any{
			"channel_point": session.ChannelPoint,
			"error":         closeErr.Error(),
		})
		writeError(w, http.StatusInternalServerError, lndCloseErrorMessage(closeErr))
		return
	}
	_ = svc.insertEvent(ctx, session.ID, "force_close_requested", "warn", map[string]any{
		"channel_point": session.ChannelPoint,
		"closing_txid":  strings.TrimSpace(txid),
	})
	if err := svc.RefreshNow(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := svc.GetSession(ctx, session.ID)
	s.recordAuditEventAsync(r, "channel.close_manager_force.submitted", session.ChannelPoint, map[string]any{
		"delegated":    false,
		"session_id":   session.ID,
		"closing_txid": strings.TrimSpace(txid),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"delegated":    false,
		"closing_txid": strings.TrimSpace(txid),
		"item":         updated,
	})
}

func (s *Server) handleCloseManagerSessionBumpFeePost(w http.ResponseWriter, r *http.Request) {
	svc, svcErr := s.closeManagerService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, svcErr)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid close session id")
		return
	}
	var req struct {
		Preset          string `json:"preset"`
		SatPerVbyte     int64  `json:"sat_per_vbyte"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	session, err := svc.GetSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "close session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if strings.TrimSpace(session.CloseTxid) == "" {
		writeError(w, http.StatusBadRequest, "session missing close txid for sweep bump")
		return
	}

	pendingSweeps, err := s.lnd.ListPendingSweeps(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
		return
	}
	matches := filterCloseManagerPendingSweeps(*session, pendingSweeps)
	if len(matches) == 0 {
		writeError(w, http.StatusBadRequest, "no pending sweep outputs available for this session")
		return
	}

	fees := closeManagerLoadBumpFeeRecommendation(ctx)
	plan, err := resolveCloseManagerBumpPlan(*session, matches, req.Preset, req.SatPerVbyte, fees)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	bumpedOutpoints := make([]string, 0, len(matches))
	for _, item := range matches {
		if strings.TrimSpace(item.Outpoint) == "" {
			continue
		}
		if err := s.lnd.BumpFee(ctx, lndclient.BumpFeeParams{
			Outpoint:    item.Outpoint,
			SatPerVbyte: plan.SatPerVbyte,
			Immediate:   plan.Immediate,
		}); err != nil {
			s.recordAuditEventAsync(r, "channel.close_manager_bump.failed", session.ChannelPoint, map[string]any{
				"session_id":    session.ID,
				"preset":        plan.Preset,
				"sat_per_vbyte": plan.SatPerVbyte,
			})
			_ = svc.insertEvent(ctx, session.ID, "bump_fee_failed", "warn", map[string]any{
				"channel_point":   session.ChannelPoint,
				"close_txid":      session.CloseTxid,
				"preset":          plan.Preset,
				"sat_per_vbyte":   plan.SatPerVbyte,
				"immediate":       plan.Immediate,
				"failed_outpoint": item.Outpoint,
				"error":           err.Error(),
			})
			writeError(w, http.StatusInternalServerError, lndDetailedErrorMessage(err))
			return
		}
		bumpedOutpoints = append(bumpedOutpoints, item.Outpoint)
	}

	_ = svc.insertEvent(ctx, session.ID, "bump_fee_requested", "warn", map[string]any{
		"channel_point":    session.ChannelPoint,
		"close_txid":       session.CloseTxid,
		"preset":           plan.Preset,
		"sat_per_vbyte":    plan.SatPerVbyte,
		"immediate":        plan.Immediate,
		"outpoints_bumped": bumpedOutpoints,
	})
	if err := svc.RefreshNow(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := svc.GetSession(ctx, session.ID)
	s.recordAuditEventAsync(r, "channel.close_manager_bump.submitted", session.ChannelPoint, map[string]any{
		"session_id":       session.ID,
		"preset":           plan.Preset,
		"sat_per_vbyte":    plan.SatPerVbyte,
		"outpoints_bumped": len(bumpedOutpoints),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"preset":           plan.Preset,
		"sat_per_vbyte":    plan.SatPerVbyte,
		"immediate":        plan.Immediate,
		"outpoints_bumped": len(bumpedOutpoints),
		"item":             updated,
	})
}

type closeManagerBumpPlan struct {
	Preset      string
	SatPerVbyte int64
	Immediate   bool
}

func filterCloseManagerPendingSweeps(session CloseManagerSession, items []lndclient.PendingSweepInfo) []lndclient.PendingSweepInfo {
	closeTxid := strings.ToLower(strings.TrimSpace(session.CloseTxid))
	if closeTxid == "" {
		return nil
	}
	matches := make([]lndclient.PendingSweepInfo, 0, len(items))
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Txid), closeTxid) {
			matches = append(matches, item)
		}
	}
	return matches
}

func closeManagerLoadBumpFeeRecommendation(ctx context.Context) *mempoolFeeRecommendation {
	feeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	var fees mempoolFeeRecommendation
	if err := fetchMempoolJSON(feeCtx, "https://mempool.space/api/v1/fees/recommended", &fees); err != nil {
		return nil
	}
	return &fees
}

func resolveCloseManagerBumpPlan(session CloseManagerSession, sweeps []lndclient.PendingSweepInfo, preset string, explicitSatPerVbyte int64, fees *mempoolFeeRecommendation) (closeManagerBumpPlan, error) {
	normalizedPreset := strings.ToLower(strings.TrimSpace(preset))
	if explicitSatPerVbyte > 0 {
		current := closeManagerCurrentSweepFee(session, sweeps)
		if explicitSatPerVbyte <= current {
			explicitSatPerVbyte = current + 1
		}
		return closeManagerBumpPlan{
			Preset:      "custom",
			SatPerVbyte: explicitSatPerVbyte,
			Immediate:   explicitSatPerVbyte >= closeManagerMaxInt64(current+5, closeManagerFastestFeeTarget(fees)),
		}, nil
	}

	if normalizedPreset == "" {
		normalizedPreset = "normal"
	}
	current := closeManagerCurrentSweepFee(session, sweeps)
	economic := closeManagerEconomicFeeTarget(session, fees)
	normal := closeManagerNormalFeeTarget(session, fees)
	urgent := closeManagerUrgentFeeTarget(session, fees)

	switch normalizedPreset {
	case "economic":
		return closeManagerBumpPlan{
			Preset:      normalizedPreset,
			SatPerVbyte: closeManagerMaxInt64(current+1, economic),
			Immediate:   false,
		}, nil
	case "normal":
		return closeManagerBumpPlan{
			Preset:      normalizedPreset,
			SatPerVbyte: closeManagerMaxInt64(current+2, normal),
			Immediate:   false,
		}, nil
	case "urgent":
		return closeManagerBumpPlan{
			Preset:      normalizedPreset,
			SatPerVbyte: closeManagerMaxInt64(current+5, urgent),
			Immediate:   true,
		}, nil
	default:
		return closeManagerBumpPlan{}, fmt.Errorf("unsupported bump preset: %s", preset)
	}
}

func closeManagerCurrentSweepFee(session CloseManagerSession, sweeps []lndclient.PendingSweepInfo) int64 {
	current := session.SweepFeeRateSatVB
	if session.SweepRequestedFeeRateSatVB > current {
		current = session.SweepRequestedFeeRateSatVB
	}
	for _, item := range sweeps {
		if item.SatPerVbyte > current {
			current = item.SatPerVbyte
		}
		if item.RequestedSatPerVbyte > current {
			current = item.RequestedSatPerVbyte
		}
	}
	return current
}

func closeManagerEconomicFeeTarget(session CloseManagerSession, fees *mempoolFeeRecommendation) int64 {
	value := int64(1)
	if fees != nil {
		switch {
		case fees.EconomyFee > 0:
			value = int64(fees.EconomyFee)
		case fees.MinimumFee > 0:
			value = int64(fees.MinimumFee)
		}
	}
	if session.MempoolTargetSatVB > 0 && (value <= 1 || value > session.MempoolTargetSatVB) {
		value = closeManagerMaxInt64(1, session.MempoolTargetSatVB-2)
	}
	return closeManagerMaxInt64(1, value)
}

func closeManagerNormalFeeTarget(session CloseManagerSession, fees *mempoolFeeRecommendation) int64 {
	if session.MempoolTargetSatVB > 0 {
		return session.MempoolTargetSatVB
	}
	if fees != nil {
		switch {
		case fees.HourFee > 0:
			return int64(fees.HourFee)
		case fees.HalfHourFee > 0:
			return int64(fees.HalfHourFee)
		}
	}
	return closeManagerEconomicFeeTarget(session, fees) + 1
}

func closeManagerUrgentFeeTarget(session CloseManagerSession, fees *mempoolFeeRecommendation) int64 {
	if fees != nil && fees.FastestFee > 0 {
		return int64(fees.FastestFee)
	}
	return closeManagerMaxInt64(closeManagerNormalFeeTarget(session, fees)+3, session.MempoolTargetSatVB+3)
}

func closeManagerFastestFeeTarget(fees *mempoolFeeRecommendation) int64 {
	if fees == nil || fees.FastestFee <= 0 {
		return 0
	}
	return int64(fees.FastestFee)
}

func closeManagerMaxInt64(values ...int64) int64 {
	var best int64
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}

func (s *Server) enrichCloseManagerSessionsWithMempool(ctx context.Context, items []CloseManagerSession) {
	if len(items) == 0 {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cache := map[string]*closeManagerExternalTxCheck{}
	for i := range items {
		s.enrichCloseManagerSessionWithMempool(checkCtx, &items[i], cache)
	}
}

func (s *Server) enrichCloseManagerSessionWithMempool(ctx context.Context, item *CloseManagerSession, cache map[string]*closeManagerExternalTxCheck) {
	if item == nil {
		return
	}
	if check := loadCloseManagerExternalTxCheck(ctx, item.CloseTxid, cache); check != nil {
		seen := check.Seen
		confirmed := check.Confirmed
		item.CloseTxExternalSeen = &seen
		item.CloseTxExternalConfirmed = &confirmed
		if check.BlockTime != nil {
			blockTime := check.BlockTime.UTC()
			item.CloseTxExternalBlockTime = &blockTime
		}
	}
	if check := loadCloseManagerExternalTxCheck(ctx, item.SweepTxid, cache); check != nil {
		seen := check.Seen
		confirmed := check.Confirmed
		item.SweepTxExternalSeen = &seen
		item.SweepTxExternalConfirmed = &confirmed
		if check.BlockTime != nil {
			blockTime := check.BlockTime.UTC()
			item.SweepTxExternalBlockTime = &blockTime
		}
	}
}

func loadCloseManagerExternalTxCheck(ctx context.Context, txid string, cache map[string]*closeManagerExternalTxCheck) *closeManagerExternalTxCheck {
	clean := strings.ToLower(strings.TrimSpace(txid))
	if clean == "" {
		return nil
	}
	if cache != nil {
		if cached, ok := cache[clean]; ok {
			return cached
		}
	}

	var parsed closeManagerMempoolTxStatus
	if err := fetchMempoolJSON(ctx, "https://mempool.space/api/tx/"+clean, &parsed); err != nil {
		if cache != nil {
			cache[clean] = nil
		}
		return nil
	}

	check := &closeManagerExternalTxCheck{
		Seen:      true,
		Confirmed: parsed.Status.Confirmed,
	}
	if parsed.Status.BlockTime > 0 {
		blockTime := time.Unix(parsed.Status.BlockTime, 0).UTC()
		check.BlockTime = &blockTime
	}
	if cache != nil {
		cache[clean] = check
	}
	return check
}
