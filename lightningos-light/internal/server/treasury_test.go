package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTreasuryValidatesSignedBaseTransfer(t *testing.T) {
	// Generated with the ethers signer using a publicly known synthetic key.
	const raw = "0x02f8b082210507830f4240839896808301117094833589fcd6edb6e08f4c7c32d4f71b54bda0291380b844a9059cbb000000000000000000000000000000000000000000000000000000000000123400000000000000000000000000000000000000000000000000000000000d750cc001a0e62aced225f6bcbf1d2990108b44ba6e6f4bdb53388333d0dbe588a010f11845a02e0f5d3fdc32580617a8e783b8b1ec9f8569de7f4ab05a53d7634d93b962d484"
	const wanted = "0xbd4f954eaac094375c794bae4f556236fb398213299ea9a8e4e05eb25ae07e2d"
	signing := treasurySigningRequest{
		ChainID: 8453, Nonce: 7,
		From:      "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf",
		Recipient: "0x0000000000000000000000000000000000001234",
		Micros:    "881932", MaxFee: "10000000", Priority: "1000000", GasLimit: "70000",
	}
	if got, err := checkTreasurySignedTransfer(raw, signing); err != nil || got != wanted {
		t.Fatalf("valid exact transfer rejected: %v", err)
	}
	for _, scenario := range []struct {
		name   string
		mutate func(*treasurySigningRequest)
	}{
		{"wrong chain", func(q *treasurySigningRequest) { q.ChainID = 1 }},
		{"wrong sender", func(q *treasurySigningRequest) { q.From = q.Recipient }},
		{"wrong recipient", func(q *treasurySigningRequest) { q.Recipient = q.From }},
		{"higher fee", func(q *treasurySigningRequest) { q.MaxFee = "1" }},
		{"wrong amount", func(q *treasurySigningRequest) { q.Micros = "881931" }},
		{"wrong nonce", func(q *treasurySigningRequest) { q.Nonce++ }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			changed := signing
			scenario.mutate(&changed)
			if _, err := checkTreasurySignedTransfer(raw, changed); err == nil {
				t.Fatal("mismatched signed transfer accepted")
			}
		})
	}
}

func TestTreasuryBaseProofChecksNetworkContractRecipientAndAmount(t *testing.T) {
	const recipient = "0x0000000000000000000000000000000000001234"
	const from = "0x0000000000000000000000000000000000004321"
	hash := "0x" + strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name, network, contract, topic, status string
		expected                               bool
	}{
		{"valid", treasuryBaseChainID, treasuryBaseUSDC, treasuryAddressTopic(recipient), "0x1", true},
		{"wrong network", "0x1", treasuryBaseUSDC, treasuryAddressTopic(recipient), "0x1", false},
		{"wrong token", treasuryBaseChainID, from, treasuryAddressTopic(recipient), "0x1", false},
		{"wrong recipient", treasuryBaseChainID, treasuryBaseUSDC, treasuryAddressTopic(from), "0x1", false},
		{"reverted", treasuryBaseChainID, treasuryBaseUSDC, treasuryAddressTopic(recipient), "0x0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct{ Method string }
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				var result any = tc.network
				if input.Method == "eth_blockNumber" {
					result = "0x110"
				}
				if input.Method == "eth_getTransactionReceipt" {
					result = treasuryBaseReceipt{Hash: hash, Status: tc.status, Block: "0x100", Logs: []treasuryBaseLog{{
						Address: tc.contract, Topics: []string{treasuryTransfer, treasuryAddressTopic(from), tc.topic},
						Data: "0x" + strings.Repeat("0", 63) + "a",
					}}}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
			}))
			defer server.Close()
			rpc := treasuryRPC{client: server.Client(), url: server.URL}
			verified, _ := rpc.verifyTransfer(t.Context(), hash, from, recipient, 10)
			if verified != tc.expected {
				t.Fatalf("verified %v, want %v", verified, tc.expected)
			}
			verified, _ = rpc.verifyTransfer(t.Context(), hash, from, recipient, 11)
			if verified {
				t.Fatal("wrong amount accepted")
			}
		})
	}
}

func TestTreasurySignerHasAnOpaqueNonNetworkedOrigin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "treasury-signer.html"), []byte("<!doctype html><title>test</title>"), 0600); err != nil {
		t.Fatal(err)
	}
	resp := httptest.NewRecorder()
	serveTreasurySigner(dir).ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "https://localhost/treasury-signer.html", nil))
	policy := resp.Header().Get("Content-Security-Policy")
	if resp.Code != http.StatusOK || resp.Header().Get("Cache-Control") != "no-store" ||
		resp.Header().Get("X-Frame-Options") != "DENY" ||
		!strings.Contains(policy, "sandbox allow-scripts allow-downloads") || strings.Contains(policy, "allow-same-origin") ||
		!strings.Contains(policy, "default-src 'none'") || strings.Contains(policy, "connect-src") {
		t.Fatalf("signer isolation lost: %d %q", resp.Code, policy)
	}
}
