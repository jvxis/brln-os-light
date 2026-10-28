package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"lightningos-light/internal/lndclient"
	"strings"
	"time"
)

type financialChatRequest struct {
	PeerPubkey string `json:"peer_pubkey"`
	Message    string `json:"message"`
	AmountSat  *int64 `json:"amount_sat"`
}

func prepareChatFinancialIntent(payload []byte) ([]byte, financialIntentBinding, error) {
	var request financialChatRequest
	if err := decodeFinancialRequest(payload, &request); err != nil {
		return nil, financialIntentBinding{}, err
	}
	request.PeerPubkey = strings.ToLower(strings.TrimSpace(request.PeerPubkey))
	request.Message = strings.TrimSpace(request.Message)
	if !isValidPubkeyHex(request.PeerPubkey) || validateChatMessage(request.Message) != nil {
		return nil, financialIntentBinding{}, errors.New("invalid chat destination or message")
	}
	amount, err := resolveChatAmountSat(request.AmountSat)
	if err != nil || amount > 21_000_000*100_000_000 {
		return nil, financialIntentBinding{}, errors.New("invalid chat payment amount")
	}
	request.AmountSat = &amount
	fee := suggestedSpendingGuardFeeSat(amount, 0)
	feeMsat := lndclient.FinancialIntentPaymentFeeLimitMsat(lndclient.DecodedInvoice{AmountSat: amount, AmountMsat: amount * 1000}, 0)
	binding := financialIntentBinding{Kind: "chat_keysend", Summary: financialIntentSummary{Operation: "chat_keysend", Destination: request.PeerPubkey, AmountSat: amount, AmountMsat: amount * 1000, MaxFeeSat: &fee, MaxFeeMsat: &feeMsat}}
	normalized, err := json.Marshal(request)
	return normalized, binding, err
}

func validateFinancialChatExecution(ctx context.Context, peer string, amount int64, message string) error {
	execution, bound := ctx.Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !bound {
		return nil
	}
	var request financialChatRequest
	var binding financialIntentBinding
	if json.Unmarshal(execution.record.Binding, &binding) != nil || binding.Kind != "chat_keysend" || json.Unmarshal(execution.record.Request, &request) != nil || request.AmountSat == nil || *request.AmountSat != amount || request.PeerPubkey != strings.ToLower(strings.TrimSpace(peer)) || request.Message != message {
		return errFinancialNativeNotDispatched
	}
	return nil
}

func commitFinancialChatDispatch(ctx context.Context, hash string) error {
	execution, bound := ctx.Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !bound {
		return nil
	}
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != 32 {
		return errFinancialNativeNotDispatched
	}
	proof, _ := json.Marshal(financialNativeProof{Kind: "chat_keysend", PaymentHash: strings.ToLower(hash), DispatchStarted: true, StartedAt: time.Now()})
	if execution.store.saveProof(execution.record.ID, proof) != nil {
		return errFinancialNativeNotDispatched
	}
	return nil
}
