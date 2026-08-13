package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5"
)

const (
	nodeRetirementLoopInterval                       = 12 * time.Second
	nodeRetirementStepTimeout                        = 25 * time.Second
	nodeRetirementCoopCloseAttemptTimeout            = 12 * time.Minute
	nodeRetirementTransferRetry                      = 2 * time.Minute
	nodeRetirementDefaultStuckHTLCThresholdSec int64 = 86400
	nodeRetirementTransferSkippedNoWalletFunds       = "skipped_no_wallet_funds"
)

type nodeRetirementRuntimeConfig struct {
	AutoConfirmCoopClose  bool
	PreapproveFCOffline   bool
	PreapproveFCStuck     bool
	StuckHTLCThresholdSec int64
	DestinationAddress    string
	SweepMinConfs         int32
	SweepSatPerVbyte      int64
}

func (s *NodeRetirementService) runLoop() {
	ticker := time.NewTicker(nodeRetirementLoopInterval)
	defer ticker.Stop()

	s.tick()
	for range ticker.C {
		s.tick()
	}
}

func (s *NodeRetirementService) tick() {
	if s == nil || s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), nodeRetirementStepTimeout)
	defer cancel()
	if err := s.processActiveSession(ctx); err != nil && s.logger != nil {
		s.logger.Printf("node-retirement: tick failed: %v", err)
	}
}

func (s *NodeRetirementService) processActiveSession(ctx context.Context) error {
	session, ok, err := s.activeSession(ctx)
	if err != nil || !ok {
		return err
	}

	switch strings.TrimSpace(session.State) {
	case nodeRetirementStateCreated:
		return s.stepSnapshot(ctx, session)
	case nodeRetirementStateSnapshotTaken:
		return s.updateSessionState(ctx, session.SessionID, nodeRetirementStateQuiescing, "")
	case nodeRetirementStateQuiescing:
		return s.stepQuiesce(ctx, session)
	case nodeRetirementStateDrainingHTLCs:
		return s.stepDrainHTLCs(ctx, session)
	case nodeRetirementStateReadyForConfirm:
		return s.stepAwaitCoopConfirm(ctx, session)
	case nodeRetirementStateClosingCoop:
		return s.stepCloseCoop(ctx, session)
	case nodeRetirementStateAwaitingDecision:
		return s.stepAwaitDecision(ctx, session)
	case nodeRetirementStateForceClosing:
		return s.stepForceClose(ctx, session)
	case nodeRetirementStateMonitoring:
		return s.stepMonitor(ctx, session)
	default:
		return nil
	}
}

func (s *NodeRetirementService) activeSession(ctx context.Context) (NodeRetirementSession, bool, error) {
	if s == nil || s.db == nil {
		return NodeRetirementSession{}, false, ErrNodeRetirementDBUnavailable
	}
	var sessionID string
	err := s.db.QueryRow(ctx, `
select session_id
from node_retirement_sessions
where state not in ('completed', 'dry_run_completed', 'failed', 'canceled')
order by created_at asc
limit 1
`).Scan(&sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return NodeRetirementSession{}, false, nil
		}
		return NodeRetirementSession{}, false, err
	}
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return NodeRetirementSession{}, false, err
	}
	return session, true, nil
}

func (s *NodeRetirementService) stepSnapshot(ctx context.Context, session NodeRetirementSession) error {
	if s.lnd == nil {
		_ = s.failSession(ctx, session.SessionID, "lnd unavailable for snapshot")
		return nil
	}
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		_ = s.failSession(ctx, session.SessionID, "failed to list channels for snapshot: "+err.Error())
		return nil
	}
	pending, err := s.lnd.ListPendingChannels(ctx)
	if err != nil {
		_ = s.failSession(ctx, session.SessionID, "failed to list pending channels for snapshot: "+err.Error())
		return nil
	}
	balances, err := s.lnd.GetBalances(ctx)
	if err != nil {
		_ = s.failSession(ctx, session.SessionID, "failed to read balances for snapshot: "+err.Error())
		return nil
	}

	pendingHTLCs := 0
	for _, ch := range channels {
		pendingHTLCs += ch.PendingHtlcCount
	}
	snapshot := map[string]any{
		"taken_at": time.Now().UTC().Format(time.RFC3339),
		"balances": balances,
		"channels": channels,
		"pending":  pending,
		"summary": map[string]any{
			"open_channels":         len(channels),
			"pending_channels":      len(pending),
			"pending_htlc_channels": pendingHTLCs,
		},
	}
	if err := s.storeSessionSnapshot(ctx, session.SessionID, snapshot); err != nil {
		return err
	}
	for _, ch := range channels {
		state := map[string]any{
			"active":             ch.Active,
			"remote_pubkey":      ch.RemotePubkey,
			"peer_alias":         ch.PeerAlias,
			"capacity_sat":       ch.CapacitySat,
			"local_balance_sat":  ch.LocalBalanceSat,
			"remote_balance_sat": ch.RemoteBalanceSat,
			"pending_htlc_count": ch.PendingHtlcCount,
			"captured_at":        time.Now().UTC().Format(time.RFC3339),
		}
		if err := s.upsertSessionChannel(ctx, session.SessionID, ch.ChannelPoint, int64(ch.ChannelID), state, state); err != nil {
			return err
		}
	}
	if err := s.updateSessionState(ctx, session.SessionID, nodeRetirementStateSnapshotTaken, ""); err != nil {
		return err
	}
	return s.insertEvent(ctx, session.SessionID, "snapshot_taken", "info", map[string]any{
		"channels":      len(channels),
		"pending":       len(pending),
		"pending_htlcs": pendingHTLCs,
	})
}

func (s *NodeRetirementService) stepQuiesce(ctx context.Context, session NodeRetirementSession) error {
	if err := s.updateSessionState(ctx, session.SessionID, nodeRetirementStateQuiescing, ""); err != nil {
		return err
	}
	cfg := parseNodeRetirementRuntimeConfig(session.Config)
	if session.DryRun {
		_ = s.insertEvent(ctx, session.SessionID, "quiesce_simulated", "info", map[string]any{
			"note": "dry-run mode, no runtime changes applied",
		})
		return s.updateSessionState(ctx, session.SessionID, nodeRetirementStateDrainingHTLCs, "")
	}

	errs := make([]string, 0, 4)
	autofeeAction := "unavailable"
	if s.autofee != nil {
		current, err := s.autofee.GetConfig(ctx)
		if err == nil && current.Enabled {
			disabled := false
			if _, err := s.autofee.UpdateConfig(ctx, AutofeeConfigUpdate{Enabled: &disabled}); err != nil {
				errs = append(errs, "autofee disable failed: "+err.Error())
				autofeeAction = "disable_failed"
			} else {
				autofeeAction = "disabled"
			}
		} else if err != nil {
			errs = append(errs, "autofee config load failed: "+err.Error())
			autofeeAction = "config_load_failed"
		} else {
			autofeeAction = "already_disabled"
		}
	}
	if s.rebalance != nil {
		current, err := s.rebalance.GetConfig(ctx)
		if err == nil && current.AutoEnabled {
			current.AutoEnabled = false
			if _, err := s.rebalance.UpdateConfig(ctx, current); err != nil {
				errs = append(errs, "rebalance auto disable failed: "+err.Error())
			}
		}
		if jobs, _, err := s.rebalance.Queue(ctx); err == nil {
			for _, job := range jobs {
				s.rebalance.StopJob(job.ID)
			}
		}
	}

	if s.lnd != nil {
		channels, err := s.lnd.ListChannels(ctx)
		if err != nil {
			errs = append(errs, "list channels for forwarding disable failed: "+err.Error())
		} else {
			for _, ch := range channels {
				if err := s.lnd.UpdateChanStatus(ctx, ch.ChannelPoint, false); err != nil {
					errs = append(errs, fmt.Sprintf("disable forwarding failed for %s: %v", ch.ChannelPoint, err))
				}
			}
		}
	}

	payload := map[string]any{
		"autofee":                  autofeeAction,
		"rebalance":                "best-effort stop",
		"forwarding_disabled":      true,
		"preapprove_fc_offline":    cfg.PreapproveFCOffline,
		"preapprove_fc_stuck_htlc": cfg.PreapproveFCStuck,
		"auto_confirm_coop_close":  cfg.AutoConfirmCoopClose,
		"error_count":              len(errs),
		"errors":                   errs,
	}
	severity := "info"
	if len(errs) > 0 {
		severity = "warn"
	}
	_ = s.insertEvent(ctx, session.SessionID, "node_quiesced", severity, payload)
	return s.updateSessionState(ctx, session.SessionID, nodeRetirementStateDrainingHTLCs, "")
}

func (s *NodeRetirementService) stepDrainHTLCs(ctx context.Context, session NodeRetirementSession) error {
	if s.lnd == nil {
		return s.failSession(ctx, session.SessionID, "lnd unavailable while draining htlcs")
	}
	cfg := parseNodeRetirementRuntimeConfig(session.Config)
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return s.failSession(ctx, session.SessionID, "failed to list channels while draining htlcs: "+err.Error())
	}

	sessionRows, err := s.ListSessionChannels(ctx, session.SessionID)
	if err != nil {
		return s.failSession(ctx, session.SessionID, "failed to read session channels while draining htlcs: "+err.Error())
	}
	existingByPoint := make(map[string]NodeRetirementChannel, len(sessionRows))
	for _, row := range sessionRows {
		point := strings.TrimSpace(row.ChannelPoint)
		if point == "" {
			continue
		}
		existingByPoint[point] = row
	}

	now := time.Now().UTC()
	pendingCount := 0
	pendingChannels := 0
	stuckCandidates := make([]string, 0, 8)
	stuckChannels := make([]map[string]any, 0, 8)
	for _, ch := range channels {
		pendingCount += ch.PendingHtlcCount
		point := strings.TrimSpace(ch.ChannelPoint)
		firstSeen := time.Time{}
		if existing, ok := existingByPoint[point]; ok {
			prev := parseNodeRetirementJSONMap(existing.CurrentState)
			if value, ok := parseNodeRetirementTime(prev["pending_htlc_first_seen_at"]); ok {
				firstSeen = value
			}
		}
		if ch.PendingHtlcCount > 0 {
			pendingChannels++
			if firstSeen.IsZero() {
				firstSeen = now
			}
		}
		ageSec := int64(0)
		if !firstSeen.IsZero() {
			ageSec = int64(now.Sub(firstSeen).Seconds())
			if ageSec < 0 {
				ageSec = 0
			}
		}
		state := map[string]any{
			"active":             ch.Active,
			"peer_alias":         ch.PeerAlias,
			"pending_htlc_count": ch.PendingHtlcCount,
			"updated_at":         now.Format(time.RFC3339),
		}
		if !firstSeen.IsZero() && ch.PendingHtlcCount > 0 {
			state["pending_htlc_first_seen_at"] = firstSeen.Format(time.RFC3339)
			state["pending_htlc_age_sec"] = ageSec
			if cfg.PreapproveFCStuck && ageSec >= cfg.StuckHTLCThresholdSec {
				stuckCandidates = append(stuckCandidates, point)
				stuckChannels = append(stuckChannels, map[string]any{
					"channel_point": point,
					"peer_alias":    ch.PeerAlias,
					"pending_htlcs": ch.PendingHtlcCount,
					"age_sec":       ageSec,
				})
				state["stuck_htlc"] = true
			}
		}
		_ = s.upsertSessionChannel(ctx, session.SessionID, ch.ChannelPoint, int64(ch.ChannelID), nil, state)
	}
	if pendingCount > 0 {
		msg := fmt.Sprintf("%d pending htlcs across %d channels", pendingCount, pendingChannels)
		_ = s.updateSessionLastError(ctx, session.SessionID, msg)
		if cfg.PreapproveFCStuck && len(stuckCandidates) > 0 {
			for _, point := range stuckCandidates {
				_ = s.updateSessionChannelCloseResult(ctx, session.SessionID, point, "", "", "pending htlcs exceeded configured stuck threshold", nodeRetirementDecisionForceClose)
			}
			_ = s.insertEvent(ctx, session.SessionID, "stuck_htlc_force_preapproved", "warn", map[string]any{
				"threshold_sec": cfg.StuckHTLCThresholdSec,
				"channels":      stuckChannels,
				"count":         len(stuckCandidates),
			})
			return s.updateSessionState(ctx, session.SessionID, nodeRetirementStateClosingCoop, msg)
		}
		return nil
	}
	_ = s.updateSessionLastError(ctx, session.SessionID, "")
	if err := s.insertEvent(ctx, session.SessionID, "htlcs_drained", "info", map[string]any{"pending_htlcs": 0}); err != nil {
		return err
	}
	if shouldAutoConfirmNodeRetirementCoopClose(session) {
		_ = s.insertEvent(ctx, session.SessionID, "coop_close_auto_confirmed", "info", map[string]any{
			"source":  session.Source,
			"dry_run": session.DryRun,
		})
		return s.updateSessionState(ctx, session.SessionID, nodeRetirementStateClosingCoop, "")
	}
	return s.updateSessionState(ctx, session.SessionID, nodeRetirementStateReadyForConfirm, "")
}

func (s *NodeRetirementService) stepAwaitCoopConfirm(ctx context.Context, session NodeRetirementSession) error {
	if shouldAutoConfirmNodeRetirementCoopClose(session) {
		_ = s.insertEvent(ctx, session.SessionID, "coop_close_auto_confirmed", "info", map[string]any{
			"source":  session.Source,
			"dry_run": session.DryRun,
		})
		return s.updateSessionState(ctx, session.SessionID, nodeRetirementStateClosingCoop, "")
	}
	return nil
}

func shouldAutoConfirmNodeRetirementCoopClose(session NodeRetirementSession) bool {
	if session.DryRun {
		return true
	}
	cfg := parseNodeRetirementRuntimeConfig(session.Config)
	return session.Source == nodeRetirementSourceSuccession || cfg.AutoConfirmCoopClose
}

func (s *NodeRetirementService) stepCloseCoop(ctx context.Context, session NodeRetirementSession) error {
	if s.lnd == nil {
		return s.failSession(ctx, session.SessionID, "lnd unavailable for cooperative close")
	}
	stepCtx, stepCancel := context.WithTimeout(context.WithoutCancel(ctx), nodeRetirementCoopCloseAttemptTimeout)
	defer stepCancel()

	cfg := parseNodeRetirementRuntimeConfig(session.Config)
	channels, err := s.ListSessionChannels(stepCtx, session.SessionID)
	if err != nil {
		return s.failSession(ctx, session.SessionID, "failed to read session channels: "+err.Error())
	}
	openNow, err := s.lnd.ListChannels(stepCtx)
	if err != nil {
		return s.failSession(ctx, session.SessionID, "failed to list open channels: "+err.Error())
	}
	openByPoint := make(map[string]lndclient.ChannelInfo, len(openNow))
	for _, ch := range openNow {
		openByPoint[strings.TrimSpace(ch.ChannelPoint)] = ch
	}

	exceptions := 0
	autoForce := 0
	submitted := 0
	pendingTimeout := 0
	for _, item := range channels {
		point := strings.TrimSpace(item.ChannelPoint)
		if point == "" {
			continue
		}
		live, ok := openByPoint[point]
		if !ok {
			continue
		}
		if session.DryRun {
			_ = s.updateSessionChannelCloseResult(stepCtx, session.SessionID, point, "coop_dry_run", "dry-run", "", "")
			submitted++
			continue
		}
		txid, closeErr := s.lnd.CloseChannel(stepCtx, point, false, 0)
		if closeErr == nil || isNodeRetirementAlreadyClosing(closeErr) {
			_ = s.updateSessionChannelCloseResult(stepCtx, session.SessionID, point, "coop", strings.TrimSpace(txid), "", "")
			submitted++
			continue
		}
		if isNodeRetirementCloseAttemptTimeout(closeErr) {
			pendingTimeout++
			continue
		}
		exceptions++
		decision := ""
		if cfg.PreapproveFCOffline && isNodeRetirementLikelyOffline(closeErr) {
			decision = nodeRetirementDecisionForceClose
			autoForce++
		}
		_ = s.updateSessionChannelCloseResult(stepCtx, session.SessionID, point, "", "", closeErr.Error(), decision)
		_ = s.insertEvent(stepCtx, session.SessionID, "coop_close_failed", "warn", map[string]any{
			"channel_point": point,
			"peer_alias":    live.PeerAlias,
			"error":         closeErr.Error(),
			"decision":      decision,
		})
	}

	_ = s.insertEvent(stepCtx, session.SessionID, "coop_close_attempted", "info", map[string]any{
		"submitted":       submitted,
		"exceptions":      exceptions,
		"pending_timeout": pendingTimeout,
		"auto_force":      autoForce,
		"dry_run":         session.DryRun,
	})

	if autoForce > 0 {
		return s.updateSessionState(stepCtx, session.SessionID, nodeRetirementStateForceClosing, "")
	}
	if exceptions > 0 {
		return s.updateSessionState(stepCtx, session.SessionID, nodeRetirementStateAwaitingDecision, "")
	}
	if pendingTimeout > 0 {
		return nil
	}
	return s.updateSessionState(stepCtx, session.SessionID, nodeRetirementStateMonitoring, "")
}

func (s *NodeRetirementService) stepAwaitDecision(ctx context.Context, session NodeRetirementSession) error {
	channels, err := s.ListSessionChannels(ctx, session.SessionID)
	if err != nil {
		return err
	}
	for _, item := range channels {
		if strings.TrimSpace(item.Decision) == nodeRetirementDecisionForceClose {
			return s.updateSessionState(ctx, session.SessionID, nodeRetirementStateForceClosing, "")
		}
	}
	return nil
}

func (s *NodeRetirementService) stepForceClose(ctx context.Context, session NodeRetirementSession) error {
	if session.DryRun {
		_ = s.insertEvent(ctx, session.SessionID, "force_close_simulated", "info", nil)
		return s.updateSessionState(ctx, session.SessionID, nodeRetirementStateMonitoring, "")
	}
	if s.lnd == nil {
		return s.failSession(ctx, session.SessionID, "lnd unavailable for force close")
	}
	channels, err := s.ListSessionChannels(ctx, session.SessionID)
	if err != nil {
		return err
	}
	openNow, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return err
	}
	openByPoint := make(map[string]struct{}, len(openNow))
	for _, ch := range openNow {
		openByPoint[strings.TrimSpace(ch.ChannelPoint)] = struct{}{}
	}

	remainingDecision := 0
	forceFailures := 0
	for _, item := range channels {
		point := strings.TrimSpace(item.ChannelPoint)
		if point == "" {
			continue
		}
		if strings.TrimSpace(item.Decision) != nodeRetirementDecisionForceClose {
			if _, ok := openByPoint[point]; ok && (item.LastError != "" || item.Decision == "") {
				remainingDecision++
			}
			continue
		}
		if _, ok := openByPoint[point]; !ok {
			continue
		}
		txid, closeErr := s.lnd.CloseChannel(ctx, point, true, 0)
		if closeErr != nil && !isNodeRetirementAlreadyClosing(closeErr) {
			_ = s.updateSessionChannelCloseResult(ctx, session.SessionID, point, "", "", closeErr.Error(), nodeRetirementDecisionForceClose)
			forceFailures++
			continue
		}
		_ = s.updateSessionChannelCloseResult(ctx, session.SessionID, point, "force", strings.TrimSpace(txid), "", nodeRetirementDecisionForceClose)
	}
	if forceFailures > 0 {
		_ = s.updateSessionLastError(ctx, session.SessionID, fmt.Sprintf("%d force-close attempts failed and will be retried", forceFailures))
		_ = s.insertEvent(ctx, session.SessionID, "force_close_retry_needed", "warn", map[string]any{
			"failed_count": forceFailures,
		})
		return nil
	}
	if remainingDecision > 0 {
		return s.updateSessionState(ctx, session.SessionID, nodeRetirementStateAwaitingDecision, "")
	}
	_ = s.updateSessionLastError(ctx, session.SessionID, "")
	return s.updateSessionState(ctx, session.SessionID, nodeRetirementStateMonitoring, "")
}

func (s *NodeRetirementService) stepMonitor(ctx context.Context, session NodeRetirementSession) error {
	if session.DryRun {
		reconciliation := map[string]any{
			"finished_at": time.Now().UTC().Format(time.RFC3339),
			"result":      "dry-run completed",
		}
		if err := s.storeSessionReconciliation(ctx, session.SessionID, reconciliation); err != nil {
			return err
		}
		if err := s.updateSessionState(ctx, session.SessionID, nodeRetirementStateDryRunCompleted, ""); err != nil {
			return err
		}
		return s.insertEvent(ctx, session.SessionID, "retirement_completed", "info", map[string]any{"dry_run": true})
	}
	if s.lnd == nil {
		return s.failSession(ctx, session.SessionID, "lnd unavailable while monitoring closures")
	}

	rows, err := s.ListSessionChannels(ctx, session.SessionID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	points := make(map[string]struct{}, len(rows))
	for _, item := range rows {
		point := strings.TrimSpace(item.ChannelPoint)
		if point != "" {
			points[point] = struct{}{}
		}
	}

	openChannels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return err
	}
	openCount := 0
	openByPoint := make(map[string]lndclient.ChannelInfo, len(openChannels))
	for _, ch := range openChannels {
		point := strings.TrimSpace(ch.ChannelPoint)
		if _, ok := points[point]; ok {
			openCount++
			openByPoint[point] = ch
		}
	}

	pendingChannels, err := s.lnd.ListPendingChannels(ctx)
	if err != nil {
		return err
	}
	pendingCount := 0
	pendingByPoint := make(map[string]lndclient.PendingChannelInfo, len(pendingChannels))
	for _, ch := range pendingChannels {
		point := strings.TrimSpace(ch.ChannelPoint)
		if _, ok := points[point]; ok {
			pendingCount++
			pendingByPoint[point] = ch
		}
	}

	for _, item := range rows {
		point := strings.TrimSpace(item.ChannelPoint)
		if point == "" {
			continue
		}
		initial := parseNodeRetirementJSONMap(item.InitialState)
		current := parseNodeRetirementJSONMap(item.CurrentState)

		state := map[string]any{
			"active":             false,
			"pending_htlc_count": 0,
			"updated_at":         now.Format(time.RFC3339),
		}
		if alias := strings.TrimSpace(stringValueFromMaps("peer_alias", current, initial)); alias != "" {
			state["peer_alias"] = alias
		}
		if pubkey := strings.TrimSpace(stringValueFromMaps("remote_pubkey", current, initial)); pubkey != "" {
			state["remote_pubkey"] = pubkey
		}

		if live, ok := openByPoint[point]; ok {
			state["active"] = live.Active
			state["peer_alias"] = live.PeerAlias
			state["remote_pubkey"] = live.RemotePubkey
			state["local_balance_sat"] = live.LocalBalanceSat
			state["remote_balance_sat"] = live.RemoteBalanceSat
			state["pending_htlc_count"] = live.PendingHtlcCount
			state["close_status"] = "open"
		} else if pending, ok := pendingByPoint[point]; ok {
			state["peer_alias"] = fallbackString(strings.TrimSpace(pending.PeerAlias), stringValueFromMaps("peer_alias", current, initial))
			state["remote_pubkey"] = fallbackString(strings.TrimSpace(pending.RemotePubkey), stringValueFromMaps("remote_pubkey", current, initial))
			state["local_balance_sat"] = pending.LocalBalanceSat
			state["remote_balance_sat"] = pending.RemoteBalanceSat
			state["limbo_balance_sat"] = pending.LimboBalance
			state["close_status"] = pending.Status
			if txid := strings.TrimSpace(pending.ClosingTxid); txid != "" {
				state["closing_txid"] = txid
			}
		} else {
			state["close_status"] = "closed"
			state["closed"] = true
			if txid := fallbackString(strings.TrimSpace(item.CloseTxid), stringValueFromMaps("closing_txid", current, initial)); txid != "" {
				state["closing_txid"] = txid
			}
		}
		_ = s.upsertSessionChannel(ctx, session.SessionID, point, item.ChannelID, nil, state)
	}

	if openCount > 0 || pendingCount > 0 {
		for _, item := range rows {
			if strings.TrimSpace(item.Decision) != nodeRetirementDecisionForceClose {
				continue
			}
			if _, ok := openByPoint[strings.TrimSpace(item.ChannelPoint)]; ok {
				return s.updateSessionState(ctx, session.SessionID, nodeRetirementStateForceClosing, "retrying force close on channels still open")
			}
		}
		return nil
	}

	var transferInfo *NodeRetirementTransfer
	if session.Source == nodeRetirementSourceSuccession {
		transferDone, info, transferErr := s.processSuccessionTransfer(ctx, session)
		transferInfo = info
		if transferErr != nil {
			_ = s.insertEvent(ctx, session.SessionID, "succession_transfer_error", "warn", map[string]any{
				"error": transferErr.Error(),
			})
			return nil
		}
		if !transferDone {
			if transferInfo != nil {
				_ = s.storeSessionReconciliation(ctx, session.SessionID, map[string]any{
					"transfer": transferInfo,
					"note":     "waiting succession transfer readiness",
				})
			}
			return nil
		}
	}

	balances, balErr := s.lnd.GetBalances(ctx)
	reconciliation := map[string]any{
		"finished_at":      time.Now().UTC().Format(time.RFC3339),
		"open_channels":    openCount,
		"pending_channels": pendingCount,
	}
	if balErr == nil {
		reconciliation["balances"] = balances
	}
	if transferInfo != nil {
		reconciliation["transfer"] = transferInfo
		if strings.TrimSpace(transferInfo.Status) == nodeRetirementTransferSkippedNoWalletFunds {
			reconciliation["wallet_funds_absent"] = true
			reconciliation["note"] = "succession transfer skipped because no on-chain wallet funds were available"
		}
	}
	if err := s.storeSessionReconciliation(ctx, session.SessionID, reconciliation); err != nil {
		return err
	}
	if err := s.updateSessionState(ctx, session.SessionID, nodeRetirementStateCompleted, ""); err != nil {
		return err
	}
	return s.insertEvent(ctx, session.SessionID, "retirement_completed", "info", map[string]any{
		"open_channels":    0,
		"pending_channels": 0,
	})
}

func (s *NodeRetirementService) failSession(ctx context.Context, sessionID string, reason string) error {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		trimmed = "node retirement failed"
	}
	_ = s.insertEvent(ctx, sessionID, "session_failed", "error", map[string]any{"error": trimmed})
	return s.updateSessionState(ctx, sessionID, nodeRetirementStateFailed, trimmed)
}

func (s *NodeRetirementService) updateSessionState(ctx context.Context, sessionID string, state string, lastError string) error {
	trimmedState := strings.TrimSpace(state)
	if trimmedState == "" {
		return nil
	}
	trimmedErr := strings.TrimSpace(lastError)
	final := trimmedState == nodeRetirementStateCompleted || trimmedState == nodeRetirementStateDryRunCompleted || trimmedState == nodeRetirementStateFailed || trimmedState == nodeRetirementStateCanceled
	_, err := s.db.Exec(ctx, `
update node_retirement_sessions
set state = $2,
  last_error = $3,
  started_at = coalesce(started_at, now()),
  completed_at = case when $4 then now() else completed_at end,
  updated_at = now()
where session_id = $1
`, sessionID, trimmedState, trimmedErr, final)
	return err
}

func (s *NodeRetirementService) updateSessionLastError(ctx context.Context, sessionID string, lastError string) error {
	_, err := s.db.Exec(ctx, `
update node_retirement_sessions
set last_error = $2, updated_at = now()
where session_id = $1
`, sessionID, strings.TrimSpace(lastError))
	return err
}

func (s *NodeRetirementService) storeSessionSnapshot(ctx context.Context, sessionID string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
update node_retirement_sessions
set snapshot_json = $2::jsonb,
  updated_at = now()
where session_id = $1
`, sessionID, string(raw))
	return err
}

func (s *NodeRetirementService) storeSessionReconciliation(ctx context.Context, sessionID string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
update node_retirement_sessions
set reconciliation_json = $2::jsonb,
  updated_at = now()
where session_id = $1
`, sessionID, string(raw))
	return err
}

func (s *NodeRetirementService) upsertSessionChannel(ctx context.Context, sessionID string, channelPoint string, channelID int64, initial any, current any) error {
	trimmedPoint := strings.TrimSpace(channelPoint)
	if trimmedPoint == "" {
		return nil
	}
	initialRaw := []byte(`{}`)
	if initial != nil {
		encoded, err := json.Marshal(initial)
		if err != nil {
			return err
		}
		initialRaw = encoded
	}
	currentRaw := []byte(`{}`)
	if current != nil {
		encoded, err := json.Marshal(current)
		if err != nil {
			return err
		}
		currentRaw = encoded
	}
	_, err := s.db.Exec(ctx, `
insert into node_retirement_channels (
  session_id, channel_point, channel_id, initial_state_json, current_state_json
) values ($1, $2, $3, $4::jsonb, $5::jsonb)
on conflict (session_id, channel_point) do update set
  channel_id = excluded.channel_id,
  current_state_json = excluded.current_state_json,
  updated_at = now()
`, sessionID, trimmedPoint, channelID, string(initialRaw), string(currentRaw))
	return err
}

func (s *NodeRetirementService) updateSessionChannelCloseResult(ctx context.Context, sessionID string, channelPoint string, closeMode string, closeTxid string, lastError string, decision string) error {
	trimmedPoint := strings.TrimSpace(channelPoint)
	if trimmedPoint == "" {
		return nil
	}
	_, err := s.db.Exec(ctx, `
update node_retirement_channels
set close_mode = coalesce(nullif($3, ''), close_mode),
  close_txid = coalesce(nullif($4, ''), close_txid),
  last_error = $5,
  decision = coalesce(nullif($6, ''), decision),
  updated_at = now()
where session_id = $1 and channel_point = $2
`, sessionID, trimmedPoint, strings.TrimSpace(closeMode), strings.TrimSpace(closeTxid), strings.TrimSpace(lastError), strings.TrimSpace(decision))
	return err
}

func (s *NodeRetirementService) processSuccessionTransfer(ctx context.Context, session NodeRetirementSession) (bool, *NodeRetirementTransfer, error) {
	cfg := parseNodeRetirementRuntimeConfig(session.Config)
	if strings.TrimSpace(cfg.DestinationAddress) == "" {
		_ = s.insertEvent(ctx, session.SessionID, "succession_transfer_skipped", "warn", map[string]any{
			"reason": "destination_address missing",
		})
		return true, nil, nil
	}
	if cfg.SweepMinConfs <= 0 {
		cfg.SweepMinConfs = 3
	}
	if cfg.SweepSatPerVbyte < 0 {
		cfg.SweepSatPerVbyte = 0
	}

	transfer, found, err := s.getOrCreateTransfer(ctx, session.SessionID, cfg)
	if err != nil {
		return false, nil, err
	}
	if found {
		if transfer.Status == "confirmed" || transfer.Status == "completed" || transfer.Status == nodeRetirementTransferSkippedNoWalletFunds {
			return true, &transfer, nil
		}
		if transfer.Status == "submitted" {
			confirmed, confErr := s.isTransferConfirmed(ctx, transfer.Txid)
			if confErr != nil {
				return false, &transfer, confErr
			}
			if confirmed {
				if err := s.updateTransferStatus(ctx, session.SessionID, "confirmed", transfer.Txid, transfer.AmountSat, transfer.Attempts, ""); err != nil {
					return false, &transfer, err
				}
				_ = s.insertEvent(ctx, session.SessionID, "succession_transfer_confirmed", "info", map[string]any{
					"txid": transfer.Txid,
				})
				transfer.Status = "confirmed"
				return true, &transfer, nil
			}
			return false, &transfer, nil
		}
		if transfer.LastAttemptAt != nil && time.Since(transfer.LastAttemptAt.UTC()) < nodeRetirementTransferRetry {
			return false, &transfer, nil
		}
	}

	if s.lnd == nil {
		return false, &transfer, errors.New("lnd unavailable for succession transfer")
	}

	utxos, utxoErr := s.lnd.ListOnchainUtxos(ctx, cfg.SweepMinConfs, 9999999)
	if utxoErr != nil {
		return false, &transfer, utxoErr
	}
	confirmedAmount := int64(0)
	for _, item := range utxos {
		confirmedAmount += item.AmountSat
	}
	if confirmedAmount <= 0 {
		allUtxos, allErr := s.lnd.ListOnchainUtxos(ctx, 0, 9999999)
		if allErr != nil {
			return false, &transfer, allErr
		}
		totalWalletAmount := sumOnchainUtxoAmount(allUtxos)
		done, status, lastError := classifySuccessionTransferWalletFunds(confirmedAmount, totalWalletAmount)
		if done {
			_ = s.updateTransferStatus(ctx, session.SessionID, status, "", 0, transfer.Attempts, lastError)
			_ = s.insertEvent(ctx, session.SessionID, "succession_transfer_skipped", "info", map[string]any{
				"reason": "no_wallet_funds",
				"note":   lastError,
			})
			transfer.Status = status
			transfer.LastError = lastError
			return true, &transfer, nil
		}
		_ = s.updateTransferStatus(ctx, session.SessionID, status, "", 0, transfer.Attempts, lastError)
		transfer.Status = status
		transfer.LastError = lastError
		return false, &transfer, nil
	}

	txid, sendErr := s.lnd.SendCoins(ctx, cfg.DestinationAddress, 0, cfg.SweepSatPerVbyte, true)
	attempts := transfer.Attempts + 1
	if sendErr != nil {
		_ = s.updateTransferStatus(ctx, session.SessionID, "failed", "", confirmedAmount, attempts, sendErr.Error())
		_ = s.insertEvent(ctx, session.SessionID, "succession_transfer_failed", "warn", map[string]any{
			"attempts": attempts,
			"error":    sendErr.Error(),
		})
		return false, &transfer, nil
	}
	txid = strings.TrimSpace(txid)
	if err := s.updateTransferStatus(ctx, session.SessionID, "submitted", txid, confirmedAmount, attempts, ""); err != nil {
		return false, &transfer, err
	}
	_ = s.insertEvent(ctx, session.SessionID, "succession_transfer_submitted", "info", map[string]any{
		"txid":          txid,
		"amount_sat":    confirmedAmount,
		"min_confs":     cfg.SweepMinConfs,
		"sat_per_vbyte": cfg.SweepSatPerVbyte,
	})
	_ = s.sendTelegram("Succession transfer submitted with txid " + txid + ".")

	transfer.Status = "submitted"
	transfer.Txid = txid
	transfer.AmountSat = confirmedAmount
	transfer.Attempts = attempts
	return false, &transfer, nil
}

func sumOnchainUtxoAmount(utxos []lndclient.OnchainUtxo) int64 {
	total := int64(0)
	for _, item := range utxos {
		total += item.AmountSat
	}
	return total
}

func classifySuccessionTransferWalletFunds(confirmedAmount int64, totalWalletAmount int64) (bool, string, string) {
	if confirmedAmount > 0 {
		return false, "", ""
	}
	if totalWalletAmount > 0 {
		return false, "waiting_funds", "no confirmed UTXOs available"
	}
	return true, nodeRetirementTransferSkippedNoWalletFunds, "no wallet funds available for succession transfer"
}

func (s *NodeRetirementService) isTransferConfirmed(ctx context.Context, txid string) (bool, error) {
	trimmed := strings.TrimSpace(strings.ToLower(txid))
	if trimmed == "" {
		return false, nil
	}
	if s.lnd == nil {
		return false, errors.New("lnd unavailable")
	}
	items, err := s.lnd.ListOnchainTransactions(ctx, 400)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if strings.ToLower(strings.TrimSpace(item.Txid)) != trimmed {
			continue
		}
		return item.Confirmations > 0, nil
	}
	return false, nil
}

func (s *NodeRetirementService) getOrCreateTransfer(ctx context.Context, sessionID string, cfg nodeRetirementRuntimeConfig) (NodeRetirementTransfer, bool, error) {
	var item NodeRetirementTransfer
	err := s.db.QueryRow(ctx, `
select id, session_id, destination_address, sweep_all, sat_per_vbyte, min_confirmations, status, txid, amount_sat,
  attempts, last_error, last_attempt_at, created_at, updated_at
from node_retirement_transfers
where session_id = $1
`, sessionID).Scan(
		&item.ID,
		&item.SessionID,
		&item.DestinationAddress,
		&item.SweepAll,
		&item.SatPerVbyte,
		&item.MinConfirmations,
		&item.Status,
		&item.Txid,
		&item.AmountSat,
		&item.Attempts,
		&item.LastError,
		scanOptionalTime(&item.LastAttemptAt),
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err == nil {
		return item, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return NodeRetirementTransfer{}, false, err
	}

	err = s.db.QueryRow(ctx, `
insert into node_retirement_transfers (
  session_id, destination_address, sweep_all, sat_per_vbyte, min_confirmations, status
) values ($1, $2, true, $3, $4, 'waiting')
returning id, session_id, destination_address, sweep_all, sat_per_vbyte, min_confirmations, status, txid, amount_sat,
  attempts, last_error, last_attempt_at, created_at, updated_at
`, sessionID, strings.TrimSpace(cfg.DestinationAddress), cfg.SweepSatPerVbyte, cfg.SweepMinConfs).Scan(
		&item.ID,
		&item.SessionID,
		&item.DestinationAddress,
		&item.SweepAll,
		&item.SatPerVbyte,
		&item.MinConfirmations,
		&item.Status,
		&item.Txid,
		&item.AmountSat,
		&item.Attempts,
		&item.LastError,
		scanOptionalTime(&item.LastAttemptAt),
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return NodeRetirementTransfer{}, false, err
	}
	_ = s.insertEvent(ctx, sessionID, "succession_transfer_armed", "info", map[string]any{
		"destination_address": strings.TrimSpace(cfg.DestinationAddress),
		"min_confirmations":   cfg.SweepMinConfs,
		"sat_per_vbyte":       cfg.SweepSatPerVbyte,
	})
	return item, false, nil
}

func (s *NodeRetirementService) updateTransferStatus(ctx context.Context, sessionID string, status string, txid string, amountSat int64, attempts int, lastError string) error {
	_, err := s.db.Exec(ctx, `
update node_retirement_transfers
set status = $2,
  txid = coalesce(nullif($3, ''), txid),
  amount_sat = case when $4 > 0 then $4 else amount_sat end,
  attempts = case when $5 >= 0 then $5 else attempts end,
  last_error = $6,
  last_attempt_at = now(),
  updated_at = now()
where session_id = $1
`, sessionID, strings.TrimSpace(status), strings.TrimSpace(txid), amountSat, attempts, strings.TrimSpace(lastError))
	return err
}

func (s *NodeRetirementService) sendTelegram(message string) error {
	cfg := readTelegramBackupConfig()
	if !cfg.configured() {
		return errors.New("telegram not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return sendTelegramMessage(ctx, cfg.BotToken, cfg.ChatID, strings.TrimSpace(message))
}

func parseNodeRetirementRuntimeConfig(raw json.RawMessage) nodeRetirementRuntimeConfig {
	cfg := nodeRetirementRuntimeConfig{
		StuckHTLCThresholdSec: nodeRetirementDefaultStuckHTLCThresholdSec,
	}
	if len(raw) == 0 {
		return cfg
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return cfg
	}
	cfg.AutoConfirmCoopClose = parseBoolAny(parsed["auto_confirm_coop_close"])
	cfg.PreapproveFCOffline = parseBoolAny(parsed["preapprove_fc_offline"])
	cfg.PreapproveFCStuck = parseBoolAny(parsed["preapprove_fc_stuck_htlc"])
	cfg.StuckHTLCThresholdSec = parseInt64Any(parsed["stuck_htlc_threshold_sec"], nodeRetirementDefaultStuckHTLCThresholdSec)
	cfg.DestinationAddress = parseStringAny(parsed["destination_address"])
	cfg.SweepMinConfs = parseInt32Any(parsed["sweep_min_confs"], 3)
	cfg.SweepSatPerVbyte = parseInt64Any(parsed["sweep_sat_per_vbyte"], 0)
	if cfg.StuckHTLCThresholdSec <= 0 {
		cfg.StuckHTLCThresholdSec = nodeRetirementDefaultStuckHTLCThresholdSec
	}
	if cfg.SweepMinConfs <= 0 {
		cfg.SweepMinConfs = 3
	}
	if cfg.SweepSatPerVbyte < 0 {
		cfg.SweepSatPerVbyte = 0
	}
	return cfg
}

func parseNodeRetirementJSONMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed == nil {
		return map[string]any{}
	}
	return parsed
}

func stringValueFromMaps(key string, maps ...map[string]any) string {
	for _, item := range maps {
		if item == nil {
			continue
		}
		if value, ok := item[key]; ok {
			if text := strings.TrimSpace(fmt.Sprintf("%v", value)); text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func fallbackString(primary string, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return strings.TrimSpace(primary)
	}
	return strings.TrimSpace(fallback)
}

func parseNodeRetirementTime(value any) (time.Time, bool) {
	text, ok := value.(string)
	if !ok {
		return time.Time{}, false
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func parseBoolAny(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.TrimSpace(strings.ToLower(v)) {
		case "1", "true", "yes", "y", "on":
			return true
		default:
			return false
		}
	case float64:
		return v != 0
	default:
		return false
	}
}

func parseStringAny(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func parseInt32Any(value any, fallback int32) int32 {
	switch v := value.(type) {
	case float64:
		if v < math.MinInt32 || v > math.MaxInt32 || math.Trunc(v) != v {
			return fallback
		}
		return int32(v)
	case int:
		return intToInt32Or(v, fallback)
	case int64:
		if v < math.MinInt32 || v > math.MaxInt32 {
			return fallback
		}
		return int32(v)
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return fallback
		}
		parsed, err := strconv.ParseInt(trimmed, 10, 32)
		if err != nil {
			return fallback
		}
		return int32(parsed)
	default:
		return fallback
	}
}

func parseInt64Any(value any, fallback int64) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return fallback
		}
		parsed, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return fallback
		}
		return parsed
	default:
		return fallback
	}
}

func isNodeRetirementAlreadyClosing(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "already in the process of closure") ||
		strings.Contains(value, "already pending channel close") ||
		strings.Contains(value, "channel is being closed")
}

func isNodeRetirementLikelyOffline(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "peer is offline") ||
		strings.Contains(value, "peer not connected") ||
		strings.Contains(value, "peer is not online") ||
		strings.Contains(value, "link not active")
}

func isNodeRetirementCloseAttemptTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return isTimeoutError(err)
}
