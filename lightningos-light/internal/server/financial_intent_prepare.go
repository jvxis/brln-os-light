package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"lightningos-light/internal/lndclient"
)

type walletSendRequest struct {
	Address         string   `json:"address"`
	AmountSat       int64    `json:"amount_sat"`
	SatPerVbyte     int64    `json:"sat_per_vbyte"`
	SweepAll        bool     `json:"sweep_all"`
	Outpoints       []string `json:"outpoints,omitempty"`
	Label           string   `json:"label,omitempty"`
	ConfirmPassword string   `json:"confirm_password,omitempty"`
}

type financialIntentSummary struct {
	Operation          string          `json:"operation"`
	Destination        string          `json:"destination,omitempty"`
	AmountSat          int64           `json:"amount_sat,omitempty"`
	AmountMode         string          `json:"amount_mode,omitempty"`
	SelectedInputSat   int64           `json:"selected_input_sat,omitempty"`
	SelectedInputCount int             `json:"selected_input_count,omitempty"`
	EstimatedFeeSat    int64           `json:"estimated_fee_sat,omitempty"`
	MaxFeeSat          *int64          `json:"max_fee_sat,omitempty"`
	MaxFeeMsat         *int64          `json:"max_fee_msat,omitempty,string"`
	SatPerVbyte        int64           `json:"sat_per_vbyte,omitempty"`
	PaymentHash        string          `json:"payment_hash,omitempty"`
	AmountMsat         int64           `json:"amount_msat,omitempty,string"`
	Details            json.RawMessage `json:"details,omitempty"`
	MaxFeeRateSatVbyte int64           `json:"max_fee_rate_sat_vbyte,omitempty"`
	FeeMode            string          `json:"fee_mode,omitempty"`
}

type financialIntentBinding struct {
	Kind              string                           `json:"kind"`
	Summary           financialIntentSummary           `json:"summary"`
	Inputs            []lndclient.FinancialIntentInput `json:"inputs,omitempty"`
	NativeLabel       string                           `json:"native_label,omitempty"`
	StartHeight       int32                            `json:"start_height,omitempty"`
	DestinationScript string                           `json:"destination_script,omitempty"`
	OutgoingChanIDs   []uint64                         `json:"outgoing_channel_ids,omitempty"`
	Snapshot          json.RawMessage                  `json:"snapshot,omitempty"`
	NativeBumps       []financialBumpTarget            `json:"native_bumps,omitempty"`
}

func decodeFinancialRequest(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid fields or values for this financial operation")
	}
	return nil
}

func (s *Server) allowFinancialIntentPreparation(w http.ResponseWriter, r *http.Request, session string) bool {
	if s.auth.limiter == nil {
		return false
	}
	key := "financial_prepare:" + auditCorrelationID(session)
	allowed, retry := s.auth.limiter.allow(key, s.auth.currentTime(), 10, 1.0/3)
	if allowed {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
	writeErrorCode(w, 429, "financial_intent_rate_limited", "too many financial preparation requests; try again later")
	return false
}

func (s *Server) handleFinancialIntentPrepare(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if s.auth == nil || !s.auth.Enabled() {
		writeJSON(w, 200, map[string]bool{"required": false})
		return
	}
	session, ok := authSessionFromContext(r.Context())
	if !ok {
		writeErrorCode(w, 401, "auth_required", "authentication required")
		return
	}
	if !s.allowFinancialIntentPreparation(w, r, session.ID) {
		return
	}
	var request struct {
		Method  string          `json:"method"`
		Path    string          `json:"path"`
		Payload json.RawMessage `json:"payload"`
	}
	if readJSON(r, &request) != nil {
		writeError(w, 400, "invalid financial intent request")
		return
	}
	if request.Method == "" {
		request.Method = http.MethodPost
	}
	operation, covered := coreFinancialOperation(request.Path)
	parsedPath, pathErr := url.ParseRequestURI(request.Path)
	if !covered || pathErr != nil || parsedPath.IsAbs() || parsedPath.Host != "" || parsedPath.RawQuery != "" || parsedPath.Fragment != "" || parsedPath.EscapedPath() != request.Path || request.Method != financialOperationMethod(operation) {
		writeErrorCode(w, 400, "financial_intent_operation_invalid", "unsupported financial intent operation")
		return
	}
	payload, err := canonicalFinancialPayload(request.Payload)
	if err != nil {
		writeErrorCode(w, 400, "financial_intent_payload_invalid", err.Error())
		return
	}
	if int64(len(payload)) > requestBodyLimit(request.Path) {
		writeErrorCode(w, 413, "request_too_large", "financial operation payload is too large")
		return
	}
	id, err := newFinancialIntentID()
	if err != nil {
		writeError(w, 503, "financial intent identifier unavailable")
		return
	}
	store, err := s.financialIntentService()
	if err != nil {
		writeErrorCode(w, 503, "financial_intent_storage_unavailable", "durable financial intent storage unavailable; no operation submitted")
		return
	}
	if request.Path == "/api/wallet/send" && store.hasUncertainOperation(request.Path) {
		writeErrorCode(w, 409, "financial_intent_pending", "reconcile the previous on-chain operation before preparing another transfer")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	normalized, binding, err := s.prepareFinancialOperation(ctx, id, request.Path, payload)
	if err != nil {
		writeErrorCode(w, 400, "financial_intent_prepare_failed", err.Error())
		return
	}
	if binding.Kind == "not_required" {
		writeJSON(w, 200, map[string]bool{"required": false})
		return
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		writeError(w, 503, "financial intent snapshot unavailable")
		return
	}
	record, err := store.create(id, session.ID, request.Path, operation.scope, normalized, encoded, request.Method)
	if err != nil {
		writeErrorCode(w, 503, "financial_intent_storage_unavailable", "financial intent could not be prepared; no operation submitted")
		return
	}
	s.recordAuditEventAsync(r, "financial_intent.prepared", id, map[string]any{"operation": record.Path, "request_digest": record.Fingerprint})
	writeJSON(w, 201, map[string]any{"required": true, "intent_id": id, "scope": financialIntentScopePrefix + id, "method": record.Method, "path": record.Path, "payload": json.RawMessage(record.Request), "summary": binding.Summary, "expires_at": record.ExpiresAt})
}

func (s *Server) prepareFinancialOperation(ctx context.Context, id, path string, payload []byte) ([]byte, financialIntentBinding, error) {
	if path == "/api/wallet/send" {
		return s.prepareOnchainFinancialIntent(ctx, id, payload)
	}
	if path == "/api/wallet/pay" || path == "/api/wallet/pay/mpp" || path == "/api/wallet/pay/validated-route" {
		return s.prepareInvoiceFinancialIntent(ctx, path, payload)
	}
	if path == "/api/chat/send" {
		return prepareChatFinancialIntent(payload)
	}
	if path == "/api/lnops/channel/open" || path == "/api/lnops/channel/open-batch" {
		return s.prepareChannelFinancialIntent(ctx, path, payload)
	}
	return s.prepareAuxFinancialIntent(ctx, path, payload)
}

func (s *Server) prepareOnchainFinancialIntent(ctx context.Context, id string, payload []byte) ([]byte, financialIntentBinding, error) {
	var request walletSendRequest
	if err := decodeFinancialRequest(payload, &request); err != nil {
		return nil, financialIntentBinding{}, err
	}
	request.Address = strings.TrimSpace(request.Address)
	request.Label = strings.TrimSpace(request.Label)
	request.ConfirmPassword = ""
	if request.Address == "" || !request.SweepAll && request.AmountSat <= 0 || request.AmountSat > 21_000_000*100_000_000 || request.SatPerVbyte < 0 || request.SatPerVbyte > 21_000_000*100_000_000 || len(request.Label) > 500 {
		return nil, financialIntentBinding{}, errors.New("invalid on-chain destination, amount, fee rate or label")
	}
	if s.lnd == nil {
		return nil, financialIntentBinding{}, errors.New("LND unavailable")
	}
	node, err := s.lnd.FinancialIntentNodeState(ctx)
	if err != nil {
		return nil, financialIntentBinding{}, errors.New("LND node state unavailable")
	}
	var network *chaincfg.Params
	for _, chain := range node.GetChains() {
		if chain.GetChain() == "bitcoin" {
			switch chain.GetNetwork() {
			case "mainnet":
				network = &chaincfg.MainNetParams
			case "testnet":
				network = &chaincfg.TestNet3Params
			case "regtest":
				network = &chaincfg.RegressionNetParams
			case "signet":
				network = &chaincfg.SigNetParams
			}
		}
	}
	if network == nil {
		return nil, financialIntentBinding{}, errors.New("LND Bitcoin network unavailable")
	}
	address, err := btcutil.DecodeAddress(request.Address, network)
	if err != nil || !address.IsForNet(network) {
		return nil, financialIntentBinding{}, errors.New("destination does not belong to the node's Bitcoin network")
	}
	if _, pubkey := address.(*btcutil.AddressPubKey); pubkey {
		return nil, financialIntentBinding{}, errors.New("bare public keys are not valid on-chain destinations")
	}
	request.Address = address.EncodeAddress()
	destinationScript, err := txscript.PayToAddrScript(address)
	if err != nil {
		return nil, financialIntentBinding{}, errors.New("unsupported destination script")
	}
	if request.SatPerVbyte == 0 {
		request.SatPerVbyte, err = s.lnd.FinancialIntentFeeRate(ctx, 6)
		if err != nil {
			return nil, financialIntentBinding{}, errors.New("automatic fee estimate unavailable; choose an explicit fee rate")
		}
	}
	if request.SweepAll {
		request.AmountSat = 0
	}
	seen := map[string]bool{}
	for _, point := range request.Outpoints {
		key := strings.ToLower(strings.TrimSpace(point))
		if key == "" || seen[key] {
			return nil, financialIntentBinding{}, errors.New("duplicate or empty selected input")
		}
		seen[key] = true
	}
	preview, err := s.lnd.PreviewOnchainSend(ctx, request.Address, request.AmountSat, request.SatPerVbyte, request.SweepAll, request.Outpoints)
	if err != nil {
		return nil, financialIntentBinding{}, errors.New("on-chain funding preview unavailable")
	}
	if !preview.EnoughFunds || len(preview.SelectedOutpoints) == 0 {
		return nil, financialIntentBinding{}, errors.New("selected confirmed inputs cannot fund this operation")
	}
	selection := preview.SelectedOutpoints
	if len(request.Outpoints) > 0 {
		selection = request.Outpoints
	}
	inputs, err := s.lnd.FinancialIntentInputs(ctx, selection)
	if err != nil {
		return nil, financialIntentBinding{}, errors.New("selected inputs changed; review the operation again")
	}
	request.Outpoints = nil
	var selected int64
	for _, input := range inputs {
		if input.AmountSat <= 0 || input.AmountSat > 21_000_000*100_000_000-selected {
			return nil, financialIntentBinding{}, errors.New("invalid selected input total")
		}
		selected += input.AmountSat
		request.Outpoints = append(request.Outpoints, input.Outpoint)
	}
	summary := financialIntentSummary{Operation: "onchain_send", Destination: request.Address, AmountSat: request.AmountSat, SelectedInputSat: selected, SelectedInputCount: len(inputs), EstimatedFeeSat: preview.FeeSat, SatPerVbyte: request.SatPerVbyte}
	// Native SendCoins has at most a destination and a change output for this
	// request. Bound fees conservatively even for its largest supported witness
	// inputs; never present an estimate as an absolute approval limit.
	maxVBytes := int64(1000) * int64(len(inputs)+1)
	maxFee := selected
	if request.SatPerVbyte <= selected/maxVBytes {
		maxFee = request.SatPerVbyte * maxVBytes
	}
	summary.MaxFeeSat = &maxFee
	if request.SweepAll {
		summary.AmountMode = "selected_maximum"
	}
	start := int32(0)
	if node.GetBlockHeight() > 6 && node.GetBlockHeight() < math.MaxInt32 {
		start = int32(node.GetBlockHeight()) - 6
	}
	binding := financialIntentBinding{Kind: "onchain_send", Summary: summary, Inputs: inputs, NativeLabel: "los-intent:" + id, StartHeight: start, DestinationScript: hex.EncodeToString(destinationScript)}
	normalized, err := json.Marshal(request)
	return normalized, binding, err
}
