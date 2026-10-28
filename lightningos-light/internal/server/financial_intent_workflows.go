package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

func balancedFinancialTerms(session BalancedOpenSession) any {
	return map[string]any{"session_id": session.SessionID, "role": session.Role, "peer_pubkey": session.PeerPubkey, "peer_host": session.PeerHost, "capacity_sat": session.CapacitySat, "fee_rate_sat_vb": session.FeeRateSatVb, "private": session.Private, "close_address": session.CloseAddress, "metadata": json.RawMessage(session.Metadata)}
}

func retirementFinancialTerms(session NodeRetirementSession) any {
	return map[string]any{"session_id": session.SessionID, "source": session.Source, "dry_run": session.DryRun, "config": json.RawMessage(session.Config), "snapshot": json.RawMessage(session.Snapshot)}
}

// Match chi's RawPath routing semantics while the ledger binds the escaped
// URL. Encoded slashes cannot hide a financial route from its approval gate.
func financialWorkflowParts(path string) []string {
	parsed, err := url.ParseRequestURI(path)
	if err != nil {
		return nil
	}
	routePath := parsed.Path
	if parsed.RawPath != "" {
		routePath = parsed.RawPath
	}
	return strings.Split(strings.Trim(routePath, "/"), "/")
}

func (s *Server) prepareWorkflowFinancialIntent(ctx context.Context, path string, payload []byte, binding financialIntentBinding) ([]byte, financialIntentBinding, error) {
	parts := financialWorkflowParts(path)
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "lnops" && parts[3] == "sessions" {
		id := parts[4]
		switch parts[2] {
		case "balanced-open":
			var empty struct{}
			if err := decodeFinancialRequest(payload, &empty); err != nil {
				return nil, binding, err
			}
			svc, _ := s.balancedOpenService()
			if svc == nil {
				return nil, binding, errors.New("balanced open service unavailable")
			}
			session, err := svc.GetSession(ctx, id)
			if err != nil {
				return nil, binding, errors.New("balanced open session unavailable")
			}
			binding.Kind = "balanced_open"
			binding.Snapshot = financialSnapshot(balancedFinancialTerms(session))
			if len(binding.Snapshot) == 0 {
				return nil, binding, errors.New("balanced open snapshot unavailable")
			}
			binding.Summary.Destination = session.PeerPubkey
			binding.Summary.AmountSat = session.CapacitySat
			binding.Summary.SatPerVbyte = session.FeeRateSatVb
			binding.Summary.Details = financialSnapshot(map[string]any{"session_id": id, "role": session.Role, "private": session.Private, "close_address": session.CloseAddress, "execution_mode": balancedSessionExecutionMode(session)})
			return payload, binding, nil
		case "node-retirement":
			var request nodeRetirementDecisionRequest
			if parts[5] == "confirm-coop" {
				var empty struct{}
				if err := decodeFinancialRequest(payload, &empty); err != nil {
					return nil, binding, err
				}
			} else if err := decodeFinancialRequest(payload, &request); err != nil {
				return nil, binding, err
			}
			svc, _ := s.nodeRetirementService()
			if svc == nil {
				return nil, binding, errors.New("node retirement service unavailable")
			}
			session, err := svc.GetSession(ctx, id)
			if err != nil {
				return nil, binding, errors.New("node retirement session unavailable")
			}
			if parts[5] == "confirm-coop" && !nodeRetirementConfirmCoopRequiresControlReauth(session) || parts[5] == "decision" && !nodeRetirementDecisionRequiresControlReauth(session, request.Decision) {
				binding.Kind = "not_required"
				return payload, binding, nil
			}
			binding.Kind = "node_retirement"
			binding.Snapshot = financialSnapshot(retirementFinancialTerms(session))
			if len(binding.Snapshot) == 0 {
				return nil, binding, errors.New("node retirement snapshot unavailable")
			}
			cfg := parseNodeRetirementRuntimeConfig(session.Config)
			binding.Summary.Destination = cfg.DestinationAddress
			binding.Summary.SatPerVbyte = cfg.SweepSatPerVbyte
			binding.Summary.Details = financialSnapshot(map[string]any{"session_id": id, "request": json.RawMessage(payload), "policy": cfg})
			return payload, binding, nil
		}
	}
	if path == "/api/lnops/node-retirement/sessions" {
		var request nodeRetirementCreateSessionRequest
		if err := decodeFinancialRequest(payload, &request); err != nil {
			return nil, binding, err
		}
		if !nodeRetirementAPISourceAllowed(request.Source) {
			return nil, binding, errors.New("only manual retirement sessions can be prepared")
		}
		if !nodeRetirementCreateSessionRequiresControlReauth(request) {
			binding.Kind = "not_required"
			return payload, binding, nil
		}
		config, _ := json.Marshal(request.Config)
		cfg := parseNodeRetirementRuntimeConfig(config)
		binding.Kind = "node_retirement_create"
		binding.Summary.Destination = cfg.DestinationAddress
		binding.Summary.SatPerVbyte = cfg.SweepSatPerVbyte
		binding.Summary.Details = financialSnapshot(map[string]any{"request": json.RawMessage(payload), "effective_policy": cfg})
		return payload, binding, nil
	}
	return s.prepareNativeAuxFinancialIntent(ctx, path, payload, binding)
}

func financialRetirementCreationID(ctx context.Context, fallback string) string {
	execution, bound := ctx.Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !bound || execution.record.Path != "/api/lnops/node-retirement/sessions" {
		return fallback
	}
	return "nr_" + execution.record.CreatedAt.UTC().Format("20060102T150405") + "_" + execution.record.ID
}

func commitFinancialWorkflow(ctx context.Context, workflowID string) error {
	execution, bound := ctx.Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !bound {
		return nil
	}
	var binding financialIntentBinding
	if json.Unmarshal(execution.record.Binding, &binding) != nil {
		return errFinancialNativeNotDispatched
	}
	if binding.Kind == "balanced_open" {
		parts := financialWorkflowParts(execution.record.Path)
		if len(parts) != 6 {
			return errFinancialNativeNotDispatched
		}
		workflowID = parts[4]
	}
	proof, _ := json.Marshal(financialNativeProof{Kind: binding.Kind, WorkflowID: workflowID, DispatchStarted: true, StartedAt: time.Now()})
	if execution.store.saveProof(execution.record.ID, proof) != nil {
		return errFinancialNativeNotDispatched
	}
	return nil
}

func balancedSessionExecutionMode(session BalancedOpenSession) string {
	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return "unavailable"
	}
	return normalizeBalancedExecutionMode(meta.ExecutionMode)
}
