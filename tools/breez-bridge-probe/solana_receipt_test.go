package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSolanaProofRequiresFinalizedOfficialUSDCAtTheQuotedWallet(t *testing.T) {
	const wallet = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
	signature := strings.Repeat("1", 63) + "2"
	for _, scenario := range []struct {
		name, network, mint, owner, program, after, txError string
		valid                                               bool
	}{
		{"exact credit", solanaMainnetGenesis, solanaUSDCMint, wallet, splTokenProgram, "1020000", "null", true},
		{"wrong cluster", solanaDevnetGenesis, solanaUSDCMint, wallet, splTokenProgram, "1020000", "null", false},
		{"different mint", solanaMainnetGenesis, devnetUSDCMint, wallet, splTokenProgram, "1020000", "null", false},
		{"different owner", solanaMainnetGenesis, solanaUSDCMint, solanaUSDCMint, splTokenProgram, "1020000", "null", false},
		{"different token program", solanaMainnetGenesis, solanaUSDCMint, wallet, solanaUSDCMint, "1020000", "null", false},
		{"incorrect amount", solanaMainnetGenesis, solanaUSDCMint, wallet, splTokenProgram, "1000000", "null", false},
		{"failed instruction", solanaMainnetGenesis, solanaUSDCMint, wallet, splTokenProgram, "1020000", `{"InstructionError":[]}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Method string
					Params []json.RawMessage
				}
				if json.NewDecoder(r.Body).Decode(&req) != nil {
					t.Error("malformed RPC request")
					return
				}
				var result any = scenario.network
				if req.Method == "getTransaction" {
					if len(req.Params) != 2 || string(req.Params[0]) != `"`+signature+`"` ||
						!strings.Contains(string(req.Params[1]), `"commitment":"finalized"`) {
						t.Error("wrong signed transaction or finality policy")
					}
					var receipt solanaReceipt
					receipt.Slot = 123
					receipt.Transaction.Signatures = []string{signature}
					receipt.Meta.Error = json.RawMessage(scenario.txError)
					receipt.Meta.Before = []solanaBalance{}
					item := solanaBalance{Mint: scenario.mint, Owner: scenario.owner, Program: scenario.program}
					item.Amount.Micros = scenario.after
					item.Amount.Decimals = 6
					receipt.Meta.After = []solanaBalance{item}
					result = receipt
				} else if req.Method != "getGenesisHash" {
					t.Error("unexpected RPC operation")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
			}))
			defer server.Close()
			rpc := sponsorRPC{endpoint: server.URL, http: sponsorHTTPClient()}
			got, err := rpc.verifySolanaCredit(bridgeCommand{
				Cluster: "mainnet", Recipient: wallet, Amount: 1020000, Reference: signature,
			}, solanaUSDCMint, solanaMainnetGenesis)
			if (err == nil) != scenario.valid || scenario.valid && (got.Status != "verified" || got.AmountOut != 1020000 || got.Slot != 123) {
				t.Fatalf("unexpected proof result %+v, error: %v", got, err)
			}
		})
	}
}
