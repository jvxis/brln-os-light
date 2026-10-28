package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func financialValueOr[T any](current T, next *T) *T {
	if next != nil {
		current = *next
	}
	return &current
}

func freezeSuccessionConfigRequest(current SuccessionConfig, request successionConfigPostRequest) successionConfigPostRequest {
	request.Enabled = financialValueOr(current.Enabled, request.Enabled)
	request.DryRun = financialValueOr(current.DryRun, request.DryRun)
	request.DestinationAddress = financialValueOr(current.DestinationAddress, request.DestinationAddress)
	request.PreapproveFCOffline = financialValueOr(current.PreapproveFCOffline, request.PreapproveFCOffline)
	request.PreapproveFCStuckHTLC = financialValueOr(current.PreapproveFCStuckHTLC, request.PreapproveFCStuckHTLC)
	request.StuckHTLCThresholdSec = financialValueOr(current.StuckHTLCThresholdSec, request.StuckHTLCThresholdSec)
	request.SweepMinConfs = financialValueOr(current.SweepMinConfs, request.SweepMinConfs)
	request.SweepSatPerVbyte = financialValueOr(current.SweepSatPerVbyte, request.SweepSatPerVbyte)
	request.CheckPeriodDays = financialValueOr(current.CheckPeriodDays, request.CheckPeriodDays)
	request.ReminderPeriodDays = financialValueOr(current.ReminderPeriodDays, request.ReminderPeriodDays)
	*request.DestinationAddress = strings.TrimSpace(*request.DestinationAddress)
	if *request.CheckPeriodDays <= 0 {
		*request.CheckPeriodDays = successionDefaultCheckPeriodDays
	}
	if *request.ReminderPeriodDays <= 0 {
		*request.ReminderPeriodDays = successionDefaultReminderDays
	}
	if *request.StuckHTLCThresholdSec <= 0 {
		*request.StuckHTLCThresholdSec = successionDefaultStuckHTLCSec
	}
	if *request.SweepMinConfs <= 0 {
		*request.SweepMinConfs = successionDefaultSweepMinConfs
	}
	if *request.SweepSatPerVbyte < 0 {
		*request.SweepSatPerVbyte = successionDefaultSweepSatPerVbyte
	}
	return request
}

func financialSnapshot(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	canonical, err := canonicalFinancialPayload(raw)
	if err != nil {
		return nil
	}
	return canonical
}

func validateFinancialSnapshot(ctx context.Context, kind string, snapshot any) error {
	execution, bound := ctx.Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !bound {
		return nil
	}
	var binding financialIntentBinding
	if json.Unmarshal(execution.record.Binding, &binding) != nil || binding.Kind != kind || len(binding.Snapshot) == 0 || string(binding.Snapshot) != string(financialSnapshot(snapshot)) {
		return errors.New("financial operation terms changed; review and confirm again")
	}
	return nil
}

func (s *Server) prepareAuxFinancialIntent(ctx context.Context, path string, payload []byte) ([]byte, financialIntentBinding, error) {
	binding := financialIntentBinding{Kind: "request", Summary: financialIntentSummary{Operation: strings.TrimPrefix(path, "/api/"), Details: payload}}
	switch path {
	case "/api/wallet/spending-guard":
		var request struct {
			Enabled            bool  `json:"enabled"`
			MaxPaymentSat      int64 `json:"max_payment_sat"`
			Rolling24hLimitSat int64 `json:"rolling_24h_limit_sat"`
		}
		if err := decodeFinancialRequest(payload, &request); err != nil {
			return nil, binding, err
		}
		next := SpendingGuardSettings{Enabled: request.Enabled, MaxPaymentSat: request.MaxPaymentSat, Rolling24hLimitSat: request.Rolling24hLimitSat}
		if err := validateSpendingGuardSettings(next); err != nil {
			return nil, binding, err
		}
		svc, _ := s.spendingGuardService()
		if svc == nil {
			return nil, binding, errors.New("spending guard unavailable")
		}
		current, err := svc.Settings(ctx)
		if err != nil {
			return nil, binding, errors.New("spending policy unavailable")
		}
		if !spendingGuardNeedsReauth(current, next) {
			binding.Kind = "not_required"
		}
		return payload, binding, nil
	case "/api/lnops/succession/config":
		var request successionConfigPostRequest
		if err := decodeFinancialRequest(payload, &request); err != nil {
			return nil, binding, err
		}
		svc, _ := s.successionService()
		if svc == nil {
			return nil, binding, errors.New("succession service unavailable")
		}
		current, err := svc.GetConfig(ctx)
		if err != nil {
			return nil, binding, errors.New("succession policy unavailable")
		}
		request = freezeSuccessionConfigRequest(current, request)
		if !successionConfigPostRequiresLiveReauth(current, request) {
			binding.Kind = "not_required"
		}
		request.ConfirmPassword = ""
		normalized, err := json.Marshal(request)
		binding.Summary.Destination = *request.DestinationAddress
		binding.Summary.Details = normalized
		return normalized, binding, err
	}
	return s.prepareWorkflowFinancialIntent(ctx, path, payload, binding)
}
