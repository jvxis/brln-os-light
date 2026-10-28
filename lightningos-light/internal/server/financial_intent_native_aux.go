package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"lightningos-light/internal/lndclient"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type financialBumpTarget struct {
	Outpoint    string `json:"outpoint"`
	SatPerVbyte int64  `json:"sat_per_vbyte"`
	BudgetSat   int64  `json:"budget_sat"`
	Immediate   bool   `json:"immediate"`
}

func scbFinancialSnapshot(points []string) map[string]any {
	ordered := append([]string(nil), points...)
	sort.Strings(ordered)
	return map[string]any{"channel_points": ordered}
}

type financialNativeAuxRequest struct {
	ChannelPoint string   `json:"channel_point,omitempty"`
	Force        bool     `json:"force,omitempty"`
	SatPerVbyte  int64    `json:"sat_per_vbyte,omitempty"`
	Preset       string   `json:"preset,omitempty"`
	Outpoint     string   `json:"outpoint,omitempty"`
	Outpoints    []string `json:"outpoints,omitempty"`
	TargetConf   uint32   `json:"target_conf,omitempty"`
	BudgetSat    int64    `json:"budget_sat,omitempty"`
	ExpirySec    uint64   `json:"expiry_sec,omitempty"`
}

func closeManagerFinancialTerms(session CloseManagerSession) any {
	return map[string]any{"id": session.ID, "channel_point": session.ChannelPoint, "source": session.Source, "source_ref": session.SourceRef, "close_txid": session.CloseTxid}
}

func validateFinancialCloseManagerSnapshot(ctx context.Context, session CloseManagerSession) error {
	execution, bound := ctx.Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !bound {
		return nil
	}
	var binding financialIntentBinding
	var snapshot struct {
		CloseManager json.RawMessage `json:"close_manager"`
	}
	if json.Unmarshal(execution.record.Binding, &binding) != nil || binding.Kind != "close_manager" || json.Unmarshal(binding.Snapshot, &snapshot) != nil || string(snapshot.CloseManager) != string(financialSnapshot(closeManagerFinancialTerms(session))) {
		return errors.New("close session terms changed; review and confirm again")
	}
	return nil
}

func validateFinancialRetirementSnapshot(ctx context.Context, session NodeRetirementSession) error {
	execution, bound := ctx.Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !bound {
		return nil
	}
	var binding financialIntentBinding
	if json.Unmarshal(execution.record.Binding, &binding) != nil {
		return errFinancialNativeNotDispatched
	}
	if binding.Kind == "close_manager" {
		var snapshot struct {
			Retirement json.RawMessage `json:"retirement"`
		}
		if json.Unmarshal(binding.Snapshot, &snapshot) != nil || string(snapshot.Retirement) != string(financialSnapshot(retirementFinancialTerms(session))) {
			return errFinancialNativeNotDispatched
		}
		return nil
	}
	return validateFinancialSnapshot(ctx, "node_retirement", retirementFinancialTerms(session))
}

func (s *Server) prepareNativeAuxFinancialIntent(ctx context.Context, path string, payload []byte, binding financialIntentBinding) ([]byte, financialIntentBinding, error) {
	if path == "/api/lnops/channel/scb/restore" {
		var request struct {
			MultiChanBackup string `json:"multi_chan_backup"`
			ConfirmPhrase   string `json:"confirm_phrase"`
		}
		if err := decodeFinancialRequest(payload, &request); err != nil {
			return nil, binding, err
		}
		backup, err := decodeSCBBackup(request.MultiChanBackup)
		if err != nil || normalizeSCBConfirmPhrase(request.ConfirmPhrase) != scbRecoveryConfirmPhrase || s.lnd == nil {
			return nil, binding, errors.New("invalid channel backup or recovery confirmation")
		}
		points, err := s.lnd.VerifyChannelBackup(ctx, backup)
		if err != nil || len(points) == 0 {
			return nil, binding, errors.New("channel backup verification failed")
		}
		digest := sha256.Sum256(backup)
		binding.Kind = "scb_restore"
		binding.Snapshot = financialSnapshot(scbFinancialSnapshot(points))
		binding.Summary.Details = financialSnapshot(map[string]any{"backup_sha256": hex.EncodeToString(digest[:]), "channel_points": points, "force_close": true})
		return payload, binding, nil
	}
	var request financialNativeAuxRequest
	if err := decodeFinancialRequest(payload, &request); err != nil {
		return nil, binding, err
	}
	if request.SatPerVbyte < 0 || request.SatPerVbyte > 21_000_000*100_000_000 || request.BudgetSat < 0 || request.TargetConf > math.MaxInt32 {
		return nil, binding, errors.New("invalid fee rate or budget")
	}
	request.ChannelPoint = strings.ToLower(strings.TrimSpace(request.ChannelPoint))
	request.Outpoint = strings.ToLower(strings.TrimSpace(request.Outpoint))
	switch path {
	case "/api/lnops/channel/close":
		if request.ChannelPoint == "" {
			return nil, binding, errors.New("channel point required")
		}
		binding.Kind = "channel_close"
		if request.Force {
			request.SatPerVbyte = 0
		} else if request.SatPerVbyte == 0 {
			rate, err := s.lnd.FinancialIntentFeeRate(ctx, 6)
			if err != nil {
				return nil, binding, errors.New("closing fee estimate unavailable")
			}
			request.SatPerVbyte = rate
		}
		binding.Summary.Destination = request.ChannelPoint
		if request.Force {
			binding.Summary.FeeMode = "existing_commitment"
		} else {
			binding.Summary.MaxFeeRateSatVbyte = lndclient.FinancialIntentCloseFeeCeiling(request.SatPerVbyte)
		}
	case "/api/onchain/utxos/lock", "/api/onchain/utxos/unlock":
		if len(request.Outpoints) == 0 {
			return nil, binding, errors.New("selected outpoints required")
		}
		request.Outpoints = normalizeOutpointBatch(request.Outpoints)
		if path == "/api/onchain/utxos/lock" {
			if request.ExpirySec == 0 {
				request.ExpirySec = utxoLeaseDefaultExpirySec
			}
			if request.ExpirySec > utxoLeaseMaxExpirySec {
				request.ExpirySec = utxoLeaseMaxExpirySec
			}
		}
	case "/api/onchain/utxos/bump":
		if request.Outpoint == "" || request.SatPerVbyte == 0 && request.TargetConf == 0 {
			return nil, binding, errors.New("outpoint and fee preference required")
		}
		if request.SatPerVbyte == 0 {
			rate, err := s.lnd.FinancialIntentFeeRate(ctx, int32(request.TargetConf))
			if err != nil {
				return nil, binding, errors.New("fee estimate unavailable")
			}
			request.SatPerVbyte = rate
		}
		request.TargetConf = 0
		if request.BudgetSat == 0 {
			budget, err := s.lnd.FinancialIntentBumpBudget(ctx, request.Outpoint)
			if err != nil {
				return nil, binding, errors.New("native fee budget unavailable")
			}
			request.BudgetSat = budget
		}
		binding.Kind = "fee_bump"
		binding.NativeBumps = []financialBumpTarget{{Outpoint: request.Outpoint, SatPerVbyte: request.SatPerVbyte, BudgetSat: request.BudgetSat, Immediate: true}}
	case "/api/lnops/channel/pending-open/bump-fee":
		pending, err := s.lnd.ListPendingChannels(ctx)
		if err != nil {
			return nil, binding, errors.New("pending channel state unavailable")
		}
		found := false
		for _, item := range pending {
			if item.ChannelPoint != request.ChannelPoint {
				continue
			}
			if !item.FundingBumpEligible || item.FundingBumpOutpoint == "" {
				return nil, binding, errors.New("pending channel is not eligible for a fee bump")
			}
			plan, err := resolvePendingOpenBumpPlan(item, request.Preset, request.SatPerVbyte, closeManagerLoadBumpFeeRecommendation(ctx))
			if err != nil {
				return nil, binding, err
			}
			budget, err := s.lnd.FinancialIntentBumpBudget(ctx, item.FundingBumpOutpoint)
			if err != nil {
				return nil, binding, errors.New("native fee budget unavailable")
			}
			request.SatPerVbyte, request.Preset = plan.SatPerVbyte, "custom"
			binding.NativeBumps = []financialBumpTarget{{Outpoint: item.FundingBumpOutpoint, SatPerVbyte: plan.SatPerVbyte, BudgetSat: budget, Immediate: plan.Immediate}}
			found = true
			break
		}
		if !found {
			return nil, binding, errors.New("pending channel unavailable")
		}
		binding.Kind = "fee_bump"
	default:
		parts := financialWorkflowParts(path)
		if len(parts) != 6 || parts[2] != "close-manager" {
			return nil, binding, errors.New("unsupported financial operation")
		}
		id, err := strconv.ParseInt(parts[4], 10, 64)
		if err != nil || id <= 0 {
			return nil, binding, errors.New("invalid close session")
		}
		svc, _ := s.closeManagerService()
		if svc == nil {
			return nil, binding, errors.New("close manager unavailable")
		}
		session, err := svc.GetSession(ctx, id)
		if err != nil {
			return nil, binding, errors.New("close session unavailable")
		}
		binding.Kind = "close_manager"
		snapshot := map[string]any{"close_manager": closeManagerFinancialTerms(*session)}
		if session.Source == "node_retirement" && session.SourceRef != "" {
			node, _ := s.nodeRetirementService()
			if node == nil {
				return nil, binding, errors.New("retirement session unavailable")
			}
			retirement, err := node.GetSession(ctx, session.SourceRef)
			if err != nil {
				return nil, binding, errors.New("retirement terms unavailable")
			}
			snapshot["retirement"] = retirementFinancialTerms(retirement)
		}
		binding.Snapshot = financialSnapshot(snapshot)
		binding.Summary.Destination = session.ChannelPoint
		if parts[5] == "bump-fee" {
			sweeps, err := s.lnd.ListPendingSweeps(ctx)
			if err != nil {
				return nil, binding, errors.New("pending sweep state unavailable")
			}
			matches := filterCloseManagerPendingSweeps(*session, sweeps)
			if len(matches) == 0 {
				return nil, binding, errors.New("no pending sweep outputs")
			}
			plan, err := resolveCloseManagerBumpPlan(*session, matches, request.Preset, request.SatPerVbyte, closeManagerLoadBumpFeeRecommendation(ctx))
			if err != nil {
				return nil, binding, err
			}
			request.SatPerVbyte, request.Preset = plan.SatPerVbyte, "custom"
			for _, item := range matches {
				if item.BudgetSat <= 0 {
					return nil, binding, errors.New("pending sweep budget unavailable")
				}
				binding.NativeBumps = append(binding.NativeBumps, financialBumpTarget{Outpoint: item.Outpoint, SatPerVbyte: plan.SatPerVbyte, BudgetSat: item.BudgetSat, Immediate: plan.Immediate})
			}
		}
	}
	if len(binding.NativeBumps) > 0 {
		var budget int64
		for _, target := range binding.NativeBumps {
			if target.BudgetSat <= 0 || target.BudgetSat > 21_000_000*100_000_000-budget {
				return nil, binding, errors.New("invalid aggregate fee budget")
			}
			budget += target.BudgetSat
		}
		binding.Summary.MaxFeeSat = &budget
	}
	normalized, err := json.Marshal(request)
	binding.Summary.SatPerVbyte = request.SatPerVbyte
	binding.Summary.Details = financialSnapshot(map[string]any{"request": json.RawMessage(normalized), "fee_bumps": binding.NativeBumps})
	return normalized, binding, err
}

func (s *Server) bumpFinancialFee(ctx context.Context, params lndclient.BumpFeeParams) error {
	execution, bound := ctx.Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !bound {
		return s.lnd.BumpFee(ctx, params)
	}
	var binding financialIntentBinding
	if json.Unmarshal(execution.record.Binding, &binding) != nil {
		return errFinancialNativeNotDispatched
	}
	for _, target := range binding.NativeBumps {
		if target.Outpoint != params.Outpoint {
			continue
		}
		if params.SatPerVbyte != target.SatPerVbyte {
			return errors.New("fee rate changed; review the operation again")
		}
		params.BudgetSat, params.Immediate, params.TargetConf = target.BudgetSat, target.Immediate, 0
		proof, _ := json.Marshal(financialNativeProof{Kind: binding.Kind, DispatchStarted: true, StartedAt: time.Now()})
		if execution.store.saveProof(execution.record.ID, proof) != nil {
			return errFinancialNativeNotDispatched
		}
		return s.lnd.BumpFee(ctx, params)
	}
	return errors.New("fee bump outpoint changed; review the operation again")
}
