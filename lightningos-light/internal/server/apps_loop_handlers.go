package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const loopSwapReauthRequiredCode = "loop_swap_reauth_required"

type loopStatusResponse struct {
	Installed    bool       `json:"installed"`
	Running      bool       `json:"running"`
	Version      string     `json:"version,omitempty"`
	Network      string     `json:"network,omitempty"`
	PendingCount int        `json:"pending_count"`
	Terms        *loopTerms `json:"terms,omitempty"`
	Autoloop     bool       `json:"autoloop_enabled"`
}

type loopTerms struct {
	LoopOutMinSat int64 `json:"loop_out_min_sat"`
	LoopOutMaxSat int64 `json:"loop_out_max_sat"`
	LoopInMinSat  int64 `json:"loop_in_min_sat"`
	LoopInMaxSat  int64 `json:"loop_in_max_sat"`
}

type loopQuoteRequest struct {
	Direction          string `json:"direction"`
	AmountSat          int64  `json:"amount_sat"`
	ConfTarget         int32  `json:"conf_target"`
	LastHopPubkey      string `json:"last_hop_pubkey"`
	Fast               bool   `json:"fast"`
	RoutingFeeLimitPPM int64  `json:"routing_fee_limit_ppm"`
}

type loopQuoteResponse struct {
	Direction              string `json:"direction"`
	AmountSat              int64  `json:"amount_sat"`
	ConfTarget             int32  `json:"conf_target"`
	SwapFeeSat             int64  `json:"swap_fee_sat"`
	OnchainFeeSat          int64  `json:"onchain_fee_sat"`
	PrepayAmountSat        int64  `json:"prepay_amount_sat,omitempty"`
	RoutingFeeLimitSat     int64  `json:"routing_fee_limit_sat,omitempty"`
	PrepayRoutingLimitSat  int64  `json:"prepay_routing_limit_sat,omitempty"`
	EstimatedFeeSat        int64  `json:"estimated_fee_sat"`
	RecommendedMaxMinerSat int64  `json:"recommended_max_miner_fee_sat"`
	CLTVDelta              int32  `json:"cltv_delta"`
	ExpiresAt              string `json:"expires_at"`
}

type loopSwapRequest struct {
	loopQuoteRequest
	DestinationAddress         string   `json:"destination_address"`
	OutgoingChannelIDs         []string `json:"outgoing_channel_ids"`
	ApprovedSwapFeeSat         int64    `json:"approved_swap_fee_sat"`
	ApprovedOnchainFeeSat      int64    `json:"approved_onchain_fee_sat"`
	ApprovedRoutingFeeLimitSat int64    `json:"approved_routing_fee_limit_sat"`
	MaxMinerFeeSat             int64    `json:"max_miner_fee_sat"`
	ConfirmPassword            string   `json:"confirm_password"`
}

type loopSwapStatus struct {
	ID                 string   `json:"id"`
	Type               string   `json:"type"`
	State              string   `json:"state"`
	FailureReason      string   `json:"failure_reason,omitempty"`
	AmountSat          int64    `json:"amount_sat"`
	InitiationTime     int64    `json:"initiation_time"`
	LastUpdateTime     int64    `json:"last_update_time"`
	CostServerSat      int64    `json:"cost_server_sat"`
	CostOnchainSat     int64    `json:"cost_onchain_sat"`
	CostOffchainSat    int64    `json:"cost_offchain_sat"`
	OutgoingChannelIDs []string `json:"outgoing_channel_ids,omitempty"`
	Label              string   `json:"label,omitempty"`
}

type loopRawSwap struct {
	ID              string   `json:"id"`
	Type            string   `json:"type"`
	State           string   `json:"state"`
	FailureReason   string   `json:"failure_reason"`
	Amt             string   `json:"amt"`
	InitiationTime  string   `json:"initiation_time"`
	LastUpdateTime  string   `json:"last_update_time"`
	CostServer      string   `json:"cost_server"`
	CostOnchain     string   `json:"cost_onchain"`
	CostOffchain    string   `json:"cost_offchain"`
	OutgoingChanSet []string `json:"outgoing_chan_set"`
	Label           string   `json:"label"`
}

type loopRawSwaps struct {
	Swaps []loopRawSwap `json:"swaps"`
}

type loopRawTerms struct {
	MinSwapAmount string `json:"min_swap_amount"`
	MaxSwapAmount string `json:"max_swap_amount"`
}

type loopRawInfo struct {
	Version string `json:"version"`
	Network string `json:"network"`
}

type loopRawOutQuote struct {
	SwapFeeSat      string `json:"swap_fee_sat"`
	PrepayAmountSat string `json:"prepay_amt_sat"`
	MinerFeeSat     string `json:"htlc_sweep_fee_sat"`
	CLTVDelta       int32  `json:"cltv_delta"`
	ConfTarget      int32  `json:"conf_target"`
}

type loopRawInQuote struct {
	SwapFeeSat  string `json:"swap_fee_sat"`
	MinerFeeSat string `json:"htlc_publish_fee_sat"`
	CLTVDelta   int32  `json:"cltv_delta"`
	ConfTarget  int32  `json:"conf_target"`
}

func (s *Server) handleLoopStatus(w http.ResponseWriter, r *http.Request) {
	paths := loopAppPaths()
	response := loopStatusResponse{Installed: fileExists(paths.LoopdPath), Autoloop: false}
	if !response.Installed {
		writeJSON(w, http.StatusOK, response)
		return
	}
	state, err := serviceActiveState(r.Context(), loopServiceName)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to read Lightning Loop service status")
		return
	}
	response.Running = state == "running"
	if !response.Running {
		writeJSON(w, http.StatusOK, response)
		return
	}
	var info loopRawInfo
	if err := s.loopdRequest(r.Context(), http.MethodGet, "/v1/loop/info", nil, &info); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	response.Version = strings.TrimSpace(info.Version)
	response.Network = strings.TrimSpace(info.Network)
	terms, err := s.fetchLoopTerms(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	response.Terms = &terms
	swaps, err := s.fetchLoopSwaps(r.Context(), 100, true)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	response.PendingCount = len(swaps)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleLoopSwaps(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = value
	}
	swaps, err := s.fetchLoopSwaps(r.Context(), limit, false)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"swaps": swaps})
}

func (s *Server) handleLoopQuote(w http.ResponseWriter, r *http.Request) {
	var req loopQuoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	quote, err := s.fetchLoopQuote(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, quote)
}

func (s *Server) handleLoopSwap(w http.ResponseWriter, r *http.Request) {
	var req loopSwapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !s.requireSensitiveReauth(w, r, authScopeLoopSwap, req.ConfirmPassword,
		loopSwapReauthRequiredCode, "password confirmation required before starting a Lightning Loop swap") {
		return
	}
	quote, err := s.fetchLoopQuote(r.Context(), req.loopQuoteRequest)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ApprovedSwapFeeSat < quote.SwapFeeSat || req.ApprovedOnchainFeeSat < quote.OnchainFeeSat {
		writeErrorCode(w, http.StatusConflict, "loop_quote_changed", "Loop fees changed; review a new quote before continuing")
		return
	}
	if req.MaxMinerFeeSat < quote.OnchainFeeSat {
		writeError(w, http.StatusBadRequest, "max_miner_fee_sat is below the current on-chain fee estimate")
		return
	}
	if quote.Direction == "out" && req.ApprovedRoutingFeeLimitSat < quote.RoutingFeeLimitSat {
		writeErrorCode(w, http.StatusConflict, "loop_quote_changed", "routing fee limit changed; review a new quote before continuing")
		return
	}

	payload, err := loopSwapPayload(req, quote)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	endpoint := "/v1/loop/out"
	if quote.Direction == "in" {
		endpoint = "/v1/loop/in"
	}
	var result map[string]any
	if err := s.loopdRequest(r.Context(), http.MethodPost, endpoint, payload, &result); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.recordAuditEvent(r, "loop.swap.start", quote.Direction, map[string]any{
		"amount_sat": quote.AmountSat, "swap_fee_sat": quote.SwapFeeSat,
		"onchain_fee_estimate_sat": quote.OnchainFeeSat,
		"max_miner_fee_sat":        req.MaxMinerFeeSat,
		"reauth_checked":           s.auth != nil && s.auth.Enabled(),
	})
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) fetchLoopTerms(ctx context.Context) (loopTerms, error) {
	var outTerms, inTerms loopRawTerms
	if err := s.loopdRequest(ctx, http.MethodGet, "/v1/loop/out/terms", nil, &outTerms); err != nil {
		return loopTerms{}, err
	}
	if err := s.loopdRequest(ctx, http.MethodGet, "/v1/loop/in/terms", nil, &inTerms); err != nil {
		return loopTerms{}, err
	}
	return loopTerms{
		LoopOutMinSat: parseLoopInt(outTerms.MinSwapAmount), LoopOutMaxSat: parseLoopInt(outTerms.MaxSwapAmount),
		LoopInMinSat: parseLoopInt(inTerms.MinSwapAmount), LoopInMaxSat: parseLoopInt(inTerms.MaxSwapAmount),
	}, nil
}

func (s *Server) fetchLoopSwaps(ctx context.Context, limit int, pendingOnly bool) ([]loopSwapStatus, error) {
	query := url.Values{}
	query.Set("max_swaps", strconv.Itoa(limit))
	if pendingOnly {
		query.Set("list_swap_filter.pending_only", "true")
	}
	var raw loopRawSwaps
	if err := s.loopdRequest(ctx, http.MethodGet, "/v1/loop/swaps?"+query.Encode(), nil, &raw); err != nil {
		return nil, err
	}
	result := make([]loopSwapStatus, 0, len(raw.Swaps))
	for _, item := range raw.Swaps {
		if pendingOnly && !isPendingLoopState(item.State) {
			continue
		}
		channels := make([]string, 0, len(item.OutgoingChanSet))
		for _, channel := range item.OutgoingChanSet {
			if _, err := strconv.ParseUint(channel, 10, 64); err == nil {
				channels = append(channels, channel)
			}
		}
		result = append(result, loopSwapStatus{
			ID: item.ID, Type: item.Type, State: item.State, FailureReason: item.FailureReason,
			AmountSat: parseLoopInt(item.Amt), InitiationTime: parseLoopInt(item.InitiationTime),
			LastUpdateTime: parseLoopInt(item.LastUpdateTime), CostServerSat: parseLoopInt(item.CostServer),
			CostOnchainSat: parseLoopInt(item.CostOnchain), CostOffchainSat: parseLoopInt(item.CostOffchain),
			OutgoingChannelIDs: channels, Label: item.Label,
		})
	}
	return result, nil
}

func (s *Server) ensureNoPendingLoopSwaps(ctx context.Context, operation string) error {
	swaps, err := s.fetchLoopSwaps(ctx, 500, true)
	if err != nil {
		return fmt.Errorf("cannot %s Lightning Loop because pending swaps could not be verified: %w", operation, err)
	}
	if len(swaps) > 0 {
		return fmt.Errorf("cannot %s Lightning Loop while %d swap(s) are pending", operation, len(swaps))
	}
	return nil
}

func (s *Server) fetchLoopQuote(ctx context.Context, req loopQuoteRequest) (loopQuoteResponse, error) {
	direction := strings.ToLower(strings.TrimSpace(req.Direction))
	if direction != "out" && direction != "in" {
		return loopQuoteResponse{}, errors.New("direction must be out or in")
	}
	if req.AmountSat <= 0 {
		return loopQuoteResponse{}, errors.New("amount_sat must be positive")
	}
	if req.ConfTarget == 0 {
		if direction == "out" {
			req.ConfTarget = 9
		} else {
			req.ConfTarget = 6
		}
	}
	if req.ConfTarget < 1 || req.ConfTarget > 2016 {
		return loopQuoteResponse{}, errors.New("conf_target must be between 1 and 2016")
	}
	if req.RoutingFeeLimitPPM == 0 {
		req.RoutingFeeLimitPPM = 2500
	}
	if req.RoutingFeeLimitPPM < 0 || req.RoutingFeeLimitPPM > 100000 {
		return loopQuoteResponse{}, errors.New("routing_fee_limit_ppm must be between 0 and 100000")
	}
	query := url.Values{}
	query.Set("conf_target", strconv.Itoa(int(req.ConfTarget)))
	expires := time.Now().UTC().Add(30 * time.Minute)
	if req.Fast {
		expires = time.Now().UTC()
	}
	query.Set("swap_publication_deadline", strconv.FormatInt(expires.Unix(), 10))
	quote := loopQuoteResponse{Direction: direction, AmountSat: req.AmountSat, ConfTarget: req.ConfTarget, ExpiresAt: expires.Format(time.RFC3339)}
	if direction == "out" {
		var raw loopRawOutQuote
		endpoint := "/v1/loop/out/quote/" + strconv.FormatInt(req.AmountSat, 10) + "?" + query.Encode()
		if err := s.loopdRequest(ctx, http.MethodGet, endpoint, nil, &raw); err != nil {
			return loopQuoteResponse{}, err
		}
		quote.SwapFeeSat = parseLoopInt(raw.SwapFeeSat)
		quote.OnchainFeeSat = parseLoopInt(raw.MinerFeeSat)
		quote.PrepayAmountSat = parseLoopInt(raw.PrepayAmountSat)
		quote.CLTVDelta = raw.CLTVDelta
		quote.RoutingFeeLimitSat = ppmFee(req.AmountSat, req.RoutingFeeLimitPPM)
		quote.PrepayRoutingLimitSat = ppmFee(quote.PrepayAmountSat, req.RoutingFeeLimitPPM)
		quote.EstimatedFeeSat = quote.SwapFeeSat + quote.OnchainFeeSat
		quote.RecommendedMaxMinerSat = saturatingMultiply(quote.OnchainFeeSat, 250)
	} else {
		if pubkey := strings.TrimSpace(req.LastHopPubkey); pubkey != "" {
			rawPubkey, err := hex.DecodeString(pubkey)
			if err != nil || len(rawPubkey) != 33 {
				return loopQuoteResponse{}, errors.New("last_hop_pubkey must be a 33-byte compressed public key")
			}
			query.Set("loop_in_last_hop", base64.StdEncoding.EncodeToString(rawPubkey))
		}
		var raw loopRawInQuote
		endpoint := "/v1/loop/in/quote/" + strconv.FormatInt(req.AmountSat, 10) + "?" + query.Encode()
		if err := s.loopdRequest(ctx, http.MethodGet, endpoint, nil, &raw); err != nil {
			return loopQuoteResponse{}, err
		}
		quote.SwapFeeSat = parseLoopInt(raw.SwapFeeSat)
		quote.OnchainFeeSat = parseLoopInt(raw.MinerFeeSat)
		if quote.OnchainFeeSat < 0 {
			return loopQuoteResponse{}, errors.New("insufficient confirmed on-chain balance for this Loop In")
		}
		quote.CLTVDelta = raw.CLTVDelta
		quote.EstimatedFeeSat = quote.SwapFeeSat + quote.OnchainFeeSat
		quote.RecommendedMaxMinerSat = saturatingMultiply(quote.OnchainFeeSat, 3)
	}
	return quote, nil
}

func loopSwapPayload(req loopSwapRequest, quote loopQuoteResponse) (map[string]any, error) {
	payload := map[string]any{
		"amt": strconv.FormatInt(quote.AmountSat, 10), "max_swap_fee": strconv.FormatInt(quote.SwapFeeSat, 10),
		"max_miner_fee": strconv.FormatInt(req.MaxMinerFeeSat, 10), "label": "lightningos-loop", "initiator": "lightningos",
	}
	if quote.Direction == "in" {
		payload["htlc_conf_target"] = quote.ConfTarget
		payload["private"] = true
		if pubkey := strings.TrimSpace(req.LastHopPubkey); pubkey != "" {
			raw, _ := hex.DecodeString(pubkey)
			payload["last_hop"] = base64.StdEncoding.EncodeToString(raw)
		}
		return payload, nil
	}
	if len(req.OutgoingChannelIDs) == 0 {
		return nil, errors.New("at least one outgoing_channel_id is required for Loop Out")
	}
	channels := make([]string, 0, len(req.OutgoingChannelIDs))
	for _, channelID := range req.OutgoingChannelIDs {
		parsed, err := strconv.ParseUint(strings.TrimSpace(channelID), 10, 64)
		if err != nil || parsed == 0 {
			return nil, errors.New("outgoing_channel_ids must be positive")
		}
		channels = append(channels, strconv.FormatUint(parsed, 10))
	}
	payload["outgoing_chan_set"] = channels
	payload["max_swap_routing_fee"] = strconv.FormatInt(quote.RoutingFeeLimitSat, 10)
	payload["max_prepay_routing_fee"] = strconv.FormatInt(quote.PrepayRoutingLimitSat, 10)
	payload["max_prepay_amt"] = strconv.FormatInt(quote.PrepayAmountSat, 10)
	payload["sweep_conf_target"] = quote.ConfTarget
	payload["htlc_confirmations"] = int32(1)
	deadline, _ := time.Parse(time.RFC3339, quote.ExpiresAt)
	payload["swap_publication_deadline"] = strconv.FormatInt(deadline.Unix(), 10)
	if destination := strings.TrimSpace(req.DestinationAddress); destination != "" {
		if strings.ContainsAny(destination, " \t\r\n") || len(destination) > 128 {
			return nil, errors.New("invalid destination_address")
		}
		payload["dest"] = destination
		payload["is_external_addr"] = true
	}
	return payload, nil
}

func (s *Server) loopdRequest(ctx context.Context, method, endpoint string, body any, target any) error {
	paths := loopAppPaths()
	if err := ensureLoopClientMaterial(ctx, paths); err != nil {
		return err
	}
	certPEM, err := os.ReadFile(paths.ClientTLSCert)
	if err != nil {
		return fmt.Errorf("Lightning Loop API certificate is unavailable to the manager: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		return errors.New("Lightning Loop API certificate is invalid")
	}
	macaroon, err := os.ReadFile(paths.ClientMacaroon)
	if err != nil || len(macaroon) == 0 {
		if err != nil {
			return fmt.Errorf("Lightning Loop API macaroon is unavailable to the manager: %w", err)
		}
		return errors.New("Lightning Loop API macaroon is empty")
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://localhost:"+strconv.Itoa(loopRESTPort)+endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Grpc-Metadata-macaroon", hex.EncodeToString(macaroon))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			TLSClientConfig:   &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Lightning Loop API is unavailable: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return errors.New("failed to read Lightning Loop response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var rpcErr struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(responseBody, &rpcErr)
		message := strings.TrimSpace(rpcErr.Message)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("Lightning Loop rejected the request: %s", message)
	}
	if target != nil && len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, target); err != nil {
			return errors.New("invalid response from Lightning Loop")
		}
	}
	return nil
}

func parseLoopInt(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func ppmFee(amount, ppm int64) int64 {
	if amount <= 0 || ppm <= 0 {
		return 10
	}
	return 10 + int64(math.Ceil(float64(amount)*float64(ppm)/1_000_000))
}

func saturatingMultiply(value, multiplier int64) int64 {
	if value <= 0 || multiplier <= 0 {
		return 0
	}
	if value > math.MaxInt64/multiplier {
		return math.MaxInt64
	}
	return value * multiplier
}
