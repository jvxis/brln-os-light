package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"
)

const devnetUSDCMint = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"

type sponsorRPC struct {
	endpoint string
	key      string
	http     *http.Client
}

func sponsorHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func sponsorRequest(req bridgeCommand) (bridgeResult, error) {
	var mint string
	switch req.Cluster {
	case "mainnet":
		mint = solanaUSDCMint
	case "devnet":
		mint = devnetUSDCMint
	default:
		return bridgeResult{}, errors.New("invalid Solana cluster")
	}
	client := sponsorRPC{
		endpoint: "https://solana.pimlico.io/v1/" + req.Cluster + "/rpc",
		key:      req.APIKey, http: sponsorHTTPClient(),
	}
	if req.Action == "sponsor-preflight" && req.Transaction == "" {
		return client.preflight(req.Cluster, mint)
	}
	if req.Action == "sponsor-estimate" {
		return client.estimate(req.Cluster, mint, req.Transaction)
	}
	return bridgeResult{}, errors.New("invalid sponsor request")
}

func (s sponsorRPC) call(method string, params any, result any) error {
	body, err := json.Marshal(struct {
		Version string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{Version: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return errors.New("invalid sponsor RPC request")
	}
	request, err := http.NewRequest(http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid sponsor RPC request")
	}
	request.Header.Set("Content-Type", "application/json")
	if s.key != "" {
		request.Header.Set("Authorization", "Bearer "+s.key)
	}
	resp, err := s.http.Do(request)
	if err != nil {
		return errors.New("Solana fee sponsor unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("Solana fee sponsor rejected the request or credential")
	}
	var response struct {
		Version string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Error   json.RawMessage `json:"error"`
		Result  json.RawMessage `json:"result"`
	}
	const ceiling = 32 << 10
	data, err := io.ReadAll(io.LimitReader(resp.Body, ceiling+1))
	if err != nil || len(data) > ceiling || json.Unmarshal(data, &response) != nil ||
		response.Version != "2.0" || response.ID != 1 || len(response.Error) != 0 ||
		len(response.Result) == 0 || bytes.Equal(response.Result, []byte("null")) ||
		json.Unmarshal(response.Result, result) != nil {
		return errors.New("Solana fee sponsor returned no valid result")
	}
	return nil
}

func (s sponsorRPC) preflight(cluster, mint string) (bridgeResult, error) {
	var signers struct {
		Signer  string `json:"signer_address"`
		Payment string `json:"payment_address"`
	}
	if err := s.call("getPayerSigner", nil, &signers); err != nil {
		return bridgeResult{}, err
	}
	var tokens struct {
		Tokens []string `json:"tokens"`
	}
	if err := s.call("getSupportedTokens", nil, &tokens); err != nil {
		return bridgeResult{}, err
	}
	var block struct {
		Hash string `json:"blockhash"`
	}
	if err := s.call("getBlockhash", nil, &block); err != nil {
		return bridgeResult{}, err
	}
	if !validSolanaAddress(signers.Signer) || !validSolanaAddress(signers.Payment) ||
		!validSolanaAddress(block.Hash) || !slices.Contains(tokens.Tokens, mint) {
		return bridgeResult{}, errors.New("Solana fee sponsor does not support the configured USDC mint")
	}
	return bridgeResult{
		Status: "ready", Cluster: cluster, Mint: mint, Sponsor: signers.Signer, FeeAccount: signers.Payment, Blockhash: block.Hash,
	}, nil
}

func (s sponsorRPC) estimate(cluster, mint, transaction string) (bridgeResult, error) {
	const maxWireBytes = 1232
	if len(transaction) > base64.StdEncoding.EncodedLen(maxWireBytes) {
		return bridgeResult{}, errors.New("invalid Solana transaction size")
	}
	wire, err := base64.StdEncoding.Strict().DecodeString(transaction)
	if err != nil || len(wire) < 129 || len(wire) > maxWireBytes {
		return bridgeResult{}, errors.New("invalid Solana transaction encoding")
	}
	var fee struct {
		Lamports int64  `json:"fee_in_lamports"`
		Token    int64  `json:"fee_in_token"`
		Payment  string `json:"payment_address"`
		Signer   string `json:"signer_pubkey"`
	}
	if err := s.call("estimateTransactionFee", map[string]string{"transaction": transaction, "fee_token": mint}, &fee); err != nil {
		return bridgeResult{}, err
	}
	if fee.Token <= 0 || fee.Token > 100_000_000 || fee.Lamports <= 0 || fee.Lamports > 100_000_000 ||
		!validSolanaAddress(fee.Payment) || !validSolanaAddress(fee.Signer) {
		return bridgeResult{}, errors.New("invalid Solana sponsor fee or destination")
	}
	return bridgeResult{
		Cluster: cluster, Mint: mint, Sponsor: fee.Signer, FeeAccount: fee.Payment,
		NetworkFeeMicros: fee.Token, NetworkFeeLamports: fee.Lamports,
	}, nil
}
