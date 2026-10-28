package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

func financialObservedResult(status int, value any) (int, []byte, error) {
	body, err := json.Marshal(value)
	return status, body, err
}

func (s *Server) observeFinancialWorkflow(ctx context.Context, record *financialIntentRecord, binding financialIntentBinding) (int, []byte, error) {
	var proof financialNativeProof
	if len(record.Proof) > 0 && (json.Unmarshal(record.Proof, &proof) != nil || proof.Kind != binding.Kind || !proof.DispatchStarted) {
		return 0, nil, errFinancialIntentPending
	}
	if record.Path == "/api/wallet/spending-guard" {
		var request struct {
			Enabled            bool  `json:"enabled"`
			MaxPaymentSat      int64 `json:"max_payment_sat"`
			Rolling24hLimitSat int64 `json:"rolling_24h_limit_sat"`
		}
		if json.Unmarshal(record.Request, &request) != nil {
			return 0, nil, errFinancialIntentPending
		}
		svc, _ := s.spendingGuardService()
		if svc == nil {
			return 0, nil, errFinancialIntentPending
		}
		settings, err := svc.Settings(ctx)
		if err != nil || settings.Enabled != request.Enabled || settings.MaxPaymentSat != request.MaxPaymentSat || settings.Rolling24hLimitSat != request.Rolling24hLimitSat {
			return 0, nil, errFinancialIntentPending
		}
		status, err := svc.Status(ctx)
		if err != nil {
			return 0, nil, errFinancialIntentPending
		}
		return financialObservedResult(200, status)
	}
	if record.Path == "/api/lnops/succession/config" {
		var request successionConfigPostRequest
		if json.Unmarshal(record.Request, &request) != nil {
			return 0, nil, errFinancialIntentPending
		}
		svc, _ := s.successionService()
		if svc == nil {
			return 0, nil, errFinancialIntentPending
		}
		current, err := svc.GetConfig(ctx)
		if err != nil {
			return 0, nil, errFinancialIntentPending
		}
		nowPolicy := freezeSuccessionConfigRequest(current, successionConfigPostRequest{})
		approved := freezeSuccessionConfigRequest(current, request)
		if string(financialSnapshot(nowPolicy)) != string(financialSnapshot(approved)) {
			return 0, nil, errFinancialIntentPending
		}
		return financialObservedResult(200, current)
	}
	if binding.Kind == "node_retirement_create" {
		expectedID := "nr_" + record.CreatedAt.UTC().Format("20060102T150405") + "_" + record.ID
		if len(record.Proof) == 0 {
			return financialObservedResult(409, map[string]string{"code": "financial_intent_not_submitted", "error": "retirement was not submitted; prepare and confirm again"})
		}
		if proof.WorkflowID != expectedID {
			return 0, nil, errFinancialIntentPending
		}
		svc, _ := s.nodeRetirementService()
		if svc == nil {
			return 0, nil, errFinancialIntentPending
		}
		session, err := svc.GetSession(ctx, expectedID)
		if err != nil {
			return 0, nil, errFinancialIntentPending
		}
		var request nodeRetirementCreateSessionRequest
		if json.Unmarshal(record.Request, &request) != nil || session.Source != nodeRetirementSourceManual || session.DryRun != request.DryRun || session.DisclaimerAccepted != request.DisclaimerAccepted {
			return 0, nil, errFinancialIntentPending
		}
		config := financialSnapshot(request.Config)
		if request.Config == nil {
			config = []byte(`{}`)
		}
		if string(financialSnapshot(json.RawMessage(session.Config))) != string(config) {
			return 0, nil, errFinancialIntentPending
		}
		return financialObservedResult(201, session)
	}
	parts := financialWorkflowParts(record.Path)
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "lnops" && parts[3] == "sessions" {
		id, action := parts[4], parts[5]
		switch binding.Kind {
		case "node_retirement":
			if len(record.Proof) == 0 {
				return financialObservedResult(409, map[string]string{"code": "financial_intent_not_submitted", "error": "retirement command was not submitted; prepare and confirm again"})
			}
			if proof.WorkflowID != id {
				return 0, nil, errFinancialIntentPending
			}
			svc, _ := s.nodeRetirementService()
			if svc == nil {
				return 0, nil, errFinancialIntentPending
			}
			session, err := svc.GetSession(ctx, id)
			if err != nil {
				return 0, nil, errFinancialIntentPending
			}
			if action == "confirm-coop" {
				switch session.State {
				case nodeRetirementStateClosingCoop, nodeRetirementStateAwaitingDecision, nodeRetirementStateForceClosing, nodeRetirementStateMonitoring, nodeRetirementStateCompleted:
					return financialObservedResult(200, session)
				}
			} else if action == "decision" {
				var request nodeRetirementDecisionRequest
				if json.Unmarshal(record.Request, &request) != nil {
					return 0, nil, errFinancialIntentPending
				}
				channels, err := svc.ListSessionChannels(ctx, id)
				if err != nil {
					return 0, nil, errFinancialIntentPending
				}
				for _, channel := range channels {
					if channel.ChannelPoint == request.ChannelPoint && channel.Decision == strings.ToLower(strings.TrimSpace(request.Decision)) {
						return financialObservedResult(200, map[string]bool{"ok": true})
					}
				}
			}
		case "balanced_open":
			svc, _ := s.balancedOpenService()
			if svc == nil {
				return 0, nil, errFinancialIntentPending
			}
			session, err := svc.GetSession(ctx, id)
			if err != nil {
				return 0, nil, errFinancialIntentPending
			}
			if !balancedFinancialContinuationMatches(binding.Snapshot, session) {
				return 0, nil, errFinancialIntentPending
			}
			resolved := false
			switch action {
			case "propose", "accept":
				resolved = session.State == balancedOpenStateAccepted || session.State == balancedOpenStateFundingTxFullySigned || session.State == balancedOpenStateFundingBroadcasted || session.State == balancedOpenStatePendingOpenDetected || session.State == balancedOpenStateActive
			case "execute":
				resolved = session.State == balancedOpenStateFundingBroadcasted || session.State == balancedOpenStatePendingOpenDetected || session.State == balancedOpenStateActive
			}
			if resolved {
				return financialObservedResult(200, session)
			}
			if action == "propose" || action == "accept" {
				return s.observeBalancedFinancialPrefund(ctx, record, binding, session)
			}
		case "close_manager":
			return s.observeFinancialCloseManager(ctx, record, binding, id, action)
		}
		return 0, nil, errFinancialIntentPending
	}
	if binding.Kind == "channel_close" {
		var request financialNativeAuxRequest
		if json.Unmarshal(record.Request, &request) != nil || s.lnd == nil {
			return 0, nil, errFinancialIntentPending
		}
		txid := s.observedFinancialCloseTxid(ctx, request.ChannelPoint)
		if txid != "" {
			return financialObservedResult(200, map[string]any{"ok": true, "closing_txid": txid})
		}
	}
	if binding.Kind == "fee_bump" && s.financialBumpsObserved(ctx, binding.NativeBumps) {
		if record.Path == "/api/onchain/utxos/bump" {
			return financialObservedResult(200, map[string]bool{"ok": true})
		}
		var request financialNativeAuxRequest
		if json.Unmarshal(record.Request, &request) != nil || len(binding.NativeBumps) != 1 {
			return 0, nil, errFinancialIntentPending
		}
		target := binding.NativeBumps[0]
		return financialObservedResult(200, map[string]any{"ok": true, "preset": "custom", "sat_per_vbyte": target.SatPerVbyte, "immediate": target.Immediate, "outpoint": target.Outpoint})
	}
	if binding.Kind == "scb_restore" && s.lnd != nil {
		var snapshot struct {
			Points []string `json:"channel_points"`
		}
		if json.Unmarshal(binding.Snapshot, &snapshot) != nil || len(snapshot.Points) == 0 {
			return 0, nil, errFinancialIntentPending
		}
		pending, err := s.lnd.ListPendingChannels(ctx)
		if err != nil {
			return 0, nil, errFinancialIntentPending
		}
		seen := map[string]bool{}
		for _, channel := range pending {
			seen[channel.ChannelPoint] = true
		}
		for _, point := range snapshot.Points {
			if !seen[point] {
				return 0, nil, errFinancialIntentPending
			}
		}
		return financialObservedResult(200, map[string]any{"ok": true, "num_restored": len(snapshot.Points), "verified_channels": len(snapshot.Points)})
	}
	return 0, nil, errFinancialIntentPending
}

func (s *Server) financialBumpsObserved(ctx context.Context, targets []financialBumpTarget) bool {
	if s.lnd == nil || len(targets) == 0 {
		return false
	}
	pending, err := s.lnd.ListPendingSweeps(ctx)
	if err != nil {
		return false
	}
	for _, target := range targets {
		found := false
		for _, input := range pending {
			if input.Outpoint == target.Outpoint && input.BudgetSat > 0 && input.BudgetSat <= target.BudgetSat && (input.RequestedSatPerVbyte >= target.SatPerVbyte || input.SatPerVbyte >= target.SatPerVbyte) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (s *Server) observedFinancialCloseTxid(ctx context.Context, point string) string {
	if s.lnd == nil {
		return ""
	}
	pending, err := s.lnd.ListPendingChannels(ctx)
	if err != nil {
		return ""
	}
	for _, channel := range pending {
		if channel.ChannelPoint == point && channel.ClosingTxid != "" {
			return channel.ClosingTxid
		}
	}
	closed, err := s.lnd.ListClosedChannels(ctx)
	if err != nil {
		return ""
	}
	for _, channel := range closed {
		if channel.ChannelPoint == point {
			return channel.ClosingTxHash
		}
	}
	return ""
}

func (s *Server) observeFinancialCloseManager(ctx context.Context, record *financialIntentRecord, binding financialIntentBinding, id, action string) (int, []byte, error) {
	key, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0, nil, errFinancialIntentPending
	}
	svc, _ := s.closeManagerService()
	if svc == nil {
		return 0, nil, errFinancialIntentPending
	}
	session, err := svc.GetSession(ctx, key)
	if err != nil {
		return 0, nil, errFinancialIntentPending
	}
	if action == "bump-fee" {
		if !s.financialBumpsObserved(ctx, binding.NativeBumps) {
			return 0, nil, errFinancialIntentPending
		}
		return financialObservedResult(200, map[string]any{"ok": true, "preset": "custom", "sat_per_vbyte": binding.Summary.SatPerVbyte, "outpoints_bumped": len(binding.NativeBumps), "item": session})
	}
	if action == "force-close" {
		if session.Source == "node_retirement" && session.SourceRef != "" {
			node, _ := s.nodeRetirementService()
			if node == nil {
				return 0, nil, errFinancialIntentPending
			}
			channels, err := node.ListSessionChannels(ctx, session.SourceRef)
			if err != nil {
				return 0, nil, errFinancialIntentPending
			}
			for _, channel := range channels {
				if channel.ChannelPoint == session.ChannelPoint && channel.Decision == nodeRetirementDecisionForceClose {
					return financialObservedResult(200, map[string]any{"ok": true, "delegated": true, "item": session, "channel_id": session.SourceRef, "message": "force close decision saved in node retirement"})
				}
			}
		} else {
			txid := s.observedFinancialCloseTxid(ctx, session.ChannelPoint)
			if txid != "" {
				return financialObservedResult(200, map[string]any{"ok": true, "delegated": false, "closing_txid": txid, "item": session})
			}
		}
	}
	return 0, nil, errFinancialIntentPending
}
