package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func TestSponsorValidatesMintAndSeparatesNetworkFees(t *testing.T) {
	methods := []string{}
	missingMint := false
	var lock sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()
		if r.Header.Get("Authorization") != "Bearer synthetic-key" || r.URL.RawQuery != "" {
			t.Error("credential did not stay in the authorization header")
		}
		var request struct {
			Method string
			Params map[string]string
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		methods = append(methods, request.Method)
		var response any
		switch request.Method {
		case "getPayerSigner":
			response = map[string]string{"signer_address": solanaUSDCMint, "payment_address": devnetUSDCMint}
		case "getSupportedTokens":
			tokens := []string{devnetUSDCMint}
			if missingMint {
				tokens = nil
			}
			response = map[string][]string{"tokens": tokens}
		case "getBlockhash":
			response = map[string]string{"blockhash": solanaUSDCMint}
		case "estimateTransactionFee":
			if request.Params["fee_token"] != devnetUSDCMint || request.Params["transaction"] == "" {
				t.Error("wrong estimate parameters")
			}
			response = map[string]any{"fee_in_lamports": 10000, "fee_in_token": 1545, "payment_address": devnetUSDCMint, "signer_pubkey": solanaUSDCMint}
		default:
			t.Error("unexpected sponsor operation")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": response})
	}))
	defer server.Close()
	client := sponsorRPC{endpoint: server.URL, key: "synthetic-key", http: sponsorHTTPClient()}
	status, err := client.preflight("devnet", devnetUSDCMint)
	if err != nil || status.Mint != devnetUSDCMint || status.Status != "ready" {
		t.Fatalf("invalid sponsor preflight: %+v %v", status, err)
	}
	fee, err := client.estimate("devnet", devnetUSDCMint, base64.StdEncoding.EncodeToString(make([]byte, 200)))
	if err != nil || fee.NetworkFeeMicros != 1545 || fee.NetworkFeeLamports != 10000 {
		t.Fatalf("incorrect network fees: %+v %v", fee, err)
	}
	lock.Lock()
	gotMethods := append([]string(nil), methods...)
	missingMint = true
	lock.Unlock()
	if !reflect.DeepEqual(gotMethods, []string{"getPayerSigner", "getSupportedTokens", "getBlockhash", "estimateTransactionFee"}) {
		t.Fatalf("unexpected methods %+v", gotMethods)
	}
	if _, err := client.preflight("devnet", devnetUSDCMint); err == nil {
		t.Fatal("accepted a sponsor that cannot charge the official mint")
	}
	if _, err := client.estimate("devnet", devnetUSDCMint, "wrong"); err == nil {
		t.Fatal("accepted an invalid transaction")
	}
}
