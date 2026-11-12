package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"lightningos-light/internal/lndclient"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// financialChannelOpenStaleAfter bounds how long a dispatched channel open may
// stay unresolved with no channel carrying its memo. A published funding shows
// up in PendingChannels (with the memo) within seconds, so an hour without any
// open, pending or closing channel for the intent means LND never funded it.
const financialChannelOpenStaleAfter = time.Hour

// financialChannelOpenFailureDefinitive reports whether an OpenChannel /
// BatchOpenChannel error proves that no funding was dispatched. Timeouts,
// cancellation and transport failures stay uncertain: LND may still be
// negotiating and publish the funding after we gave up waiting.
func financialChannelOpenFailureDefinitive(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.DeadlineExceeded, codes.Canceled, codes.Unavailable, codes.Aborted, codes.Internal:
			return false
		}
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{"deadline exceeded", "context canceled", "connection refused", "transport is closing", "unavailable", "connection reset", "broken pipe"} {
		if strings.Contains(msg, needle) {
			return false
		}
	}
	return true
}

// recordFinancialChannelDispatchFailure marks the intent proof with a
// definitive native rejection. It is a no-op outside an intent execution or
// when the error may still have left a funding in flight.
func recordFinancialChannelDispatchFailure(ctx context.Context, kind string, openErr error) {
	execution, bound := ctx.Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !bound || execution == nil || execution.store == nil || execution.record == nil {
		return
	}
	if !financialChannelOpenFailureDefinitive(openErr) {
		return
	}
	latest, _, err := execution.store.get(execution.record.ID)
	if err != nil {
		return
	}
	var proof financialNativeProof
	if len(latest.Proof) == 0 || json.Unmarshal(latest.Proof, &proof) != nil || proof.Kind != kind || !proof.DispatchStarted {
		return
	}
	proof.DispatchFailed = true
	proof.FailedAt = time.Now()
	proof.FailureReason = lndDetailedErrorMessage(openErr)
	if encoded, err := json.Marshal(proof); err == nil {
		_ = execution.store.saveProof(latest.ID, encoded)
	}
}

// financialChannelOpenRejection decides whether a dispatched channel open
// with no channel carrying its memo can be closed as rejected. It returns the
// 409 body to commit, or false while the outcome must stay uncertain.
func financialChannelOpenRejection(proof financialNativeProof, now time.Time) ([]byte, bool) {
	if proof.DispatchFailed {
		reason := strings.TrimSpace(proof.FailureReason)
		if reason == "" {
			reason = "LND rejected the channel open"
		}
		body, _ := json.Marshal(map[string]string{"code": "channel_open_rejected", "error": reason + "; no channel was funded"})
		return body, true
	}
	if !proof.StartedAt.IsZero() && now.Sub(proof.StartedAt) >= financialChannelOpenStaleAfter {
		body, _ := json.Marshal(map[string]string{"code": "channel_open_not_found", "error": "no open, pending or closing channel carried this intent memo within 1h of dispatch; LND did not fund a channel for it"})
		return body, true
	}
	return nil, false
}

type financialChannelOpenRequest struct {
	PeerAddress     string   `json:"peer_address,omitempty"`
	Pubkey          string   `json:"pubkey,omitempty"`
	Host            string   `json:"host,omitempty"`
	LocalFundingSat int64    `json:"local_funding_sat"`
	PushSat         int64    `json:"push_sat,omitempty"`
	CloseAddress    string   `json:"close_address,omitempty"`
	Private         bool     `json:"private"`
	SatPerVbyte     int64    `json:"sat_per_vbyte,omitempty"`
	Outpoints       []string `json:"outpoints,omitempty"`
	FeeRatePpm      *int64   `json:"fee_rate_ppm,omitempty"`
	BaseFeeMsat     *int64   `json:"base_fee_msat,omitempty"`
}

type financialChannelBatchRequest struct {
	Channels    []financialChannelOpenRequest `json:"channels"`
	SatPerVbyte int64                         `json:"sat_per_vbyte"`
}

func (s *Server) prepareChannelFinancialIntent(ctx context.Context, path string, payload []byte) ([]byte, financialIntentBinding, error) {
	batch := financialChannelBatchRequest{}
	if path == "/api/lnops/channel/open" {
		var single financialChannelOpenRequest
		if err := decodeFinancialRequest(payload, &single); err != nil {
			return nil, financialIntentBinding{}, err
		}
		if single.Host != "" {
			return nil, financialIntentBinding{}, errors.New("use peer_address with an explicit host and port")
		}
		batch.Channels = []financialChannelOpenRequest{single}
		batch.SatPerVbyte = single.SatPerVbyte
	} else if err := decodeFinancialRequest(payload, &batch); err != nil {
		return nil, financialIntentBinding{}, err
	}
	if len(batch.Channels) == 0 || len(batch.Channels) > batchOpenMaxChannels || batch.SatPerVbyte < 0 || batch.SatPerVbyte > 21_000_000*100_000_000 || s.lnd == nil {
		return nil, financialIntentBinding{}, errors.New("invalid channel count or fee rate")
	}
	if batch.SatPerVbyte == 0 {
		rate, err := s.lnd.FinancialIntentFeeRate(ctx, 6)
		if err != nil {
			return nil, financialIntentBinding{}, errors.New("automatic funding fee unavailable; choose an explicit fee rate")
		}
		batch.SatPerVbyte = rate
	}
	var amount int64
	seen := map[string]bool{}
	for index := range batch.Channels {
		item := &batch.Channels[index]
		if item.LocalFundingSat <= 0 || item.LocalFundingSat > 21_000_000*100_000_000-amount || item.PushSat < 0 || item.PushSat > item.LocalFundingSat {
			return nil, financialIntentBinding{}, errors.New("invalid channel funding amount or push amount")
		}
		if _, err := parseChannelOpenPolicy(item.FeeRatePpm, item.BaseFeeMsat); err != nil {
			return nil, financialIntentBinding{}, err
		}
		peer := strings.TrimSpace(item.PeerAddress)
		if peer == "" {
			peer = strings.TrimSpace(item.Pubkey)
			if item.Host != "" {
				peer += "@" + strings.TrimSpace(item.Host)
			}
		}
		pubkey := peer
		if strings.Contains(peer, "@") {
			key, host, err := parsePeerAddress(peer)
			if err != nil || !strings.Contains(host, ":") {
				return nil, financialIntentBinding{}, errors.New("invalid channel peer address")
			}
			pubkey = key
			peer = key + "@" + host
		} else if path == "/api/lnops/channel/open" {
			return nil, financialIntentBinding{}, errors.New("single channel opening requires pubkey@host:port")
		}
		pubkey = strings.ToLower(pubkey)
		if !isValidPubkeyHex(pubkey) || seen[pubkey] {
			return nil, financialIntentBinding{}, errors.New("invalid or duplicate channel peer")
		}
		seen[pubkey] = true
		item.PeerAddress, item.Pubkey, item.Host = peer, pubkey, ""
		if path != "/api/lnops/channel/open" && (item.PushSat != 0 || len(item.Outpoints) != 0 || item.SatPerVbyte != 0) {
			return nil, financialIntentBinding{}, errors.New("unsupported per-channel batch funding fields")
		}
		amount += item.LocalFundingSat
	}
	kind := "channel_open_batch"
	var normalized []byte
	var err error
	if path == "/api/lnops/channel/open" {
		kind = "channel_open"
		batch.Channels[0].SatPerVbyte = batch.SatPerVbyte
		if len(batch.Channels[0].Outpoints) > 0 {
			inputs, inputErr := s.lnd.FinancialIntentInputs(ctx, batch.Channels[0].Outpoints)
			if inputErr != nil {
				return nil, financialIntentBinding{}, errors.New("selected channel funding inputs are unavailable")
			}
			batch.Channels[0].Outpoints = nil
			for _, input := range inputs {
				batch.Channels[0].Outpoints = append(batch.Channels[0].Outpoints, input.Outpoint)
			}
		}
		normalized, err = json.Marshal(batch.Channels[0])
	} else {
		normalized, err = json.Marshal(batch)
	}
	binding := financialIntentBinding{Kind: kind, Summary: financialIntentSummary{Operation: kind, AmountSat: amount, SatPerVbyte: batch.SatPerVbyte, Details: normalized}}
	if len(batch.Channels) == 1 {
		binding.Summary.Destination = batch.Channels[0].Pubkey
	}
	return normalized, binding, err
}

func commitFinancialChannelDispatch(ctx context.Context, kind string) (string, error) {
	execution, bound := ctx.Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !bound {
		return "", nil
	}
	var binding financialIntentBinding
	if json.Unmarshal(execution.record.Binding, &binding) != nil || binding.Kind != kind {
		return "", errFinancialNativeNotDispatched
	}
	proof, _ := json.Marshal(financialNativeProof{Kind: kind, DispatchStarted: true, StartedAt: time.Now()})
	if execution.store.saveProof(execution.record.ID, proof) != nil {
		return "", errFinancialNativeNotDispatched
	}
	return "los-intent:" + execution.record.ID, nil
}

func (s *Server) observeFinancialChannelOpen(ctx context.Context, record *financialIntentRecord, binding financialIntentBinding) (int, []byte, error) {
	if len(record.Proof) == 0 {
		return 409, []byte(`{"code":"financial_intent_not_submitted","error":"native channel funding was not submitted; prepare and confirm again"}`), nil
	}
	var proof financialNativeProof
	if json.Unmarshal(record.Proof, &proof) != nil || proof.Kind != binding.Kind || !proof.DispatchStarted || s.lnd == nil {
		return 0, nil, errFinancialIntentPending
	}
	batch := financialChannelBatchRequest{}
	if binding.Kind == "channel_open" {
		var single financialChannelOpenRequest
		if json.Unmarshal(record.Request, &single) != nil {
			return 0, nil, errFinancialIntentPending
		}
		batch.Channels = []financialChannelOpenRequest{single}
	} else if json.Unmarshal(record.Request, &batch) != nil {
		return 0, nil, errFinancialIntentPending
	}
	channels, err := s.lnd.FinancialIntentChannels(ctx, "los-intent:"+record.ID)
	if err != nil {
		return 0, nil, errFinancialIntentPending
	}
	if len(channels) == 0 {
		// A channel that exists always wins over a recorded failure; only an
		// empty native result may close the intent as rejected.
		if body, ok := financialChannelOpenRejection(proof, time.Now()); ok {
			return 409, body, nil
		}
		return 0, nil, errFinancialIntentPending
	}
	if len(channels) != len(batch.Channels) {
		return 0, nil, errFinancialIntentPending
	}
	results := make([]lndclient.BatchOpenChannelResult, len(batch.Channels))
	for index, item := range batch.Channels {
		memo := "los-intent:" + record.ID
		if binding.Kind == "channel_open_batch" {
			memo += fmt.Sprintf(":%d", index)
		}
		found := false
		for _, channel := range channels {
			if channel.Memo != memo {
				continue
			}
			if found || channel.Pubkey != item.Pubkey || channel.CapacitySat != item.LocalFundingSat || channel.Private != item.Private {
				return 0, nil, errFinancialIntentPending
			}
			parts := strings.Split(channel.ChannelPoint, ":")
			if len(parts) != 2 || len(parts[0]) != 64 {
				return 0, nil, errFinancialIntentPending
			}
			output, err := strconv.ParseUint(parts[1], 10, 32)
			if err != nil {
				return 0, nil, errFinancialIntentPending
			}
			results[index] = lndclient.BatchOpenChannelResult{ChannelPoint: channel.ChannelPoint, Txid: parts[0], OutputIndex: uint32(output)}
			found = true
		}
		if !found {
			return 0, nil, errFinancialIntentPending
		}
	}
	var body []byte
	if binding.Kind == "channel_open" {
		body, _ = json.Marshal(map[string]string{"channel_point": results[0].ChannelPoint})
	} else {
		body, _ = json.Marshal(map[string]any{"pending_channels": results, "count": len(results)})
	}
	return 200, body, nil
}
