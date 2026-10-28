package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

type financialNativeOperations struct {
	sendOnchain     func(context.Context, lndclient.SendCoinsParams) (string, error)
	findOnchain     func(context.Context, string, int32) ([]*lnrpc.Transaction, error)
	restoreLabel    func(context.Context, string, string) error
	mempoolContains func(context.Context, string) (bool, error)
}

type financialNativeProof struct {
	Kind            string    `json:"kind"`
	DispatchStarted bool      `json:"dispatch_started"`
	NativeLabel     string    `json:"native_label,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	PaymentHash     string    `json:"payment_hash,omitempty"`
	WorkflowID      string    `json:"workflow_id,omitempty"`
}

func (s *Server) financialNativeOperations() financialNativeOperations {
	var ops financialNativeOperations
	if s.financialNativeOps != nil {
		ops = *s.financialNativeOps
	}
	if ops.sendOnchain == nil {
		ops.sendOnchain = s.lnd.SendCoinsAdvanced
	}
	if ops.findOnchain == nil {
		ops.findOnchain = s.lnd.FinancialIntentTransactions
	}
	if ops.restoreLabel == nil {
		ops.restoreLabel = s.lnd.RestoreFinancialIntentLabel
	}
	if ops.mempoolContains == nil {
		ops.mempoolContains = s.financialMempoolContains
	}
	return ops
}

func (s *Server) executeFinancialOnchainIntent(ctx context.Context, r *http.Request, request walletSendRequest) (string, error) {
	execution, ok := r.Context().Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !ok {
		return "", errors.New("financial intent execution context unavailable")
	}
	var binding financialIntentBinding
	if json.Unmarshal(execution.record.Binding, &binding) != nil || binding.Kind != "onchain_send" || binding.NativeLabel != "los-intent:"+execution.record.ID || binding.Summary.Destination != request.Address || binding.Summary.SatPerVbyte != request.SatPerVbyte || binding.Summary.AmountSat != request.AmountSat || len(request.Outpoints) != len(binding.Inputs) {
		return "", errors.New("on-chain operation does not match its approved snapshot")
	}
	allowed := map[string]bool{}
	for _, input := range binding.Inputs {
		allowed[input.Outpoint] = true
	}
	for _, point := range request.Outpoints {
		if !allowed[point] {
			return "", errors.New("on-chain inputs do not match the approved snapshot")
		}
		delete(allowed, point)
	}
	proof, _ := json.Marshal(financialNativeProof{Kind: "onchain_send", DispatchStarted: true, NativeLabel: binding.NativeLabel, StartedAt: time.Now()})
	// This durable barrier is the only gateway to native SendCoins. Neither
	// an HTTP retry nor reconciliation may invoke SendCoins a second time.
	if err := execution.store.saveProof(execution.record.ID, proof); err != nil {
		return "", errors.New("financial dispatch could not be committed; native send was not submitted")
	}
	ops := s.financialNativeOperations()
	txid, err := ops.sendOnchain(ctx, lndclient.SendCoinsParams{Address: request.Address, AmountSat: request.AmountSat, SatPerVbyte: request.SatPerVbyte, SendAll: request.SweepAll, Outpoints: request.Outpoints, Label: binding.NativeLabel})
	if err != nil {
		return "", err
	}
	if txid == "" {
		return "", errors.New("native send outcome unavailable; reconcile the intent before retrying")
	}
	// Keep the managed label while the response might be lost. Restore the
	// operator's original label only after the result is durably committed.
	execution.postCommit = func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		label := request.Label
		if label == "" {
			label = "external"
		}
		if err := ops.restoreLabel(cleanup, txid, label); err != nil {
			s.recordAuditEventAsync(r, "financial_intent.label_restore_failed", execution.record.ID, map[string]any{"txid": txid})
		}
	}
	return txid, nil
}

func (s *Server) financialMempoolContains(ctx context.Context, txid string) (bool, error) {
	var cfg bitcoinRPCConfig
	var err error
	if readBitcoinSource() == "local" {
		cfg, err = readBitcoinLocalRPCConfig(ctx)
	} else {
		cfg = resolveBitcoinRemoteRPCConfig(s.cfg)
	}
	if err != nil || cfg.Host == "" {
		return false, errors.New("active Bitcoin RPC unavailable")
	}
	body, err := fetchBitcoinRPCParams(ctx, cfg.Host, cfg.User, cfg.Pass, "getmempoolentry", []any{txid})
	if err != nil {
		return false, errors.New("Bitcoin mempool proof unavailable")
	}
	var result struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(body, &result) != nil || len(result.Result) == 0 || string(result.Result) == "null" {
		return false, errors.New("Bitcoin mempool proof unavailable")
	}
	return true, nil
}
