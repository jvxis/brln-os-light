package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
)

// Opt-in integration test for the dedicated native lab only. The runner must
// bind a scratch directory over /var/lib/lightningos in its mount namespace so
// the wallet activity ledger cannot overwrite the lab panels' own state.
func TestLNURLWithdrawNativeRegtest(t *testing.T) {
	if os.Getenv("LOS_LNURL_NATIVE_TEST") != "LOS_REGTEST_ONLY" {
		t.Skip("dedicated native regtest lab required")
	}
	host, _ := os.Hostname()
	marker, err := os.ReadFile("/etc/los-regtest/LAB_ONLY")
	if host != "los-regtest" || err != nil || strings.TrimSpace(string(marker)) != "LOS_REGTEST_ONLY" {
		t.Fatal("refusing non-lab host")
	}
	scratch, err := os.ReadFile("/var/lib/lightningos/LNURL_TEST_ONLY")
	if err != nil || string(scratch) != "LNURL_TEST_ONLY" {
		t.Fatal("isolated wallet ledger required")
	}
	cli := func(node, port string, args ...string) map[string]any {
		t.Helper()
		argv := append([]string{"--lnddir=/var/lib/los-regtest/" + node, "--network=regtest", "--rpcserver=127.0.0.1:" + port}, args...)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/opt/los-regtest/bin/lncli", argv...).Output()
		if err != nil {
			t.Fatal("native regtest RPC failed (output withheld)")
		}
		var result map[string]any
		if json.Unmarshal(out, &result) != nil {
			t.Fatal("invalid native RPC response")
		}
		return result
	}
	for _, node := range []struct{ name, port string }{{"alice", "10009"}, {"bob", "11009"}} {
		info := cli(node.name, node.port, "getinfo")
		chains, ok := info["chains"].([]any)
		if !ok || len(chains) != 1 || chains[0].(map[string]any)["network"] != "regtest" || info["synced_to_chain"] != true {
			t.Fatal("refusing unsynced or non-regtest LND")
		}
	}
	cfg := &config.Config{}
	cfg.LND.GRPCHost = "127.0.0.1:10009"
	cfg.LND.TLSCertPath = "/var/lib/los-regtest/alice/tls.cert"
	cfg.LND.AdminMacaroonPath = "/var/lib/los-regtest/alice/data/chain/bitcoin/regtest/admin.macaroon"
	logger := log.New(io.Discard, "", 0)
	client := lndclient.New(cfg, logger)
	defer client.Close()
	s := &Server{lnd: client, logger: logger, auditLogInitAt: time.Now(), auditLogErr: "test audit disabled"}
	var callbacks atomic.Int64
	var firstInvoice atomic.Value
	withdrawMerchant(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/voucher" {
			json.NewEncoder(w).Encode(lnurlWithdrawDescriptor{Tag: "withdrawRequest", Callback: "https://regtest-provider.example/callback", K1: "REGTEST_ONLY", Min: 2000, Max: 2000, Description: "Regtest receiving test"})
			return
		}
		call := callbacks.Add(1)
		if r.URL.Query().Get("k1") != "REGTEST_ONLY" {
			t.Error("wrong provider challenge")
		}
		if call > 1 {
			if r.URL.Query().Get("pr") != firstInvoice.Load().(string) {
				t.Error("retry changed native receiving invoice")
			}
			w.Write([]byte(`{"status":"OK"}`))
			return
		}
		firstInvoice.Store(r.URL.Query().Get("pr"))
		paid := cli("bob", "11009", "payinvoice", "--force", "--json", "--fee_limit=10", r.URL.Query().Get("pr"))
		if paid["status"] != "SUCCEEDED" {
			t.Error("native regtest payment did not settle")
		}
		// Deliberately lose the first response after successful payment. The API
		// must report uncertainty, then reuse the same invoice on retry.
		if callbacks.Load() == 1 {
			w.Write([]byte("lost response"))
			return
		}
		w.Write([]byte(`{"status":"OK"}`))
	})
	previewBody, _ := json.Marshal(map[string]string{"lnurl": withdrawCode(t, "https://regtest-provider.example/voucher")})
	w := httptest.NewRecorder()
	s.handleLNURLWithdrawPreview(w, withdrawRequest(string(previewBody), "native-test"))
	if w.Code != 200 {
		t.Fatalf("preview: %d", w.Code)
	}
	var preview struct {
		Token string `json:"token"`
	}
	json.Unmarshal(w.Body.Bytes(), &preview)
	body, _ := json.Marshal(map[string]any{"token": preview.Token, "amount_sat": 2})
	w = httptest.NewRecorder()
	s.handleLNURLWithdraw(w, withdrawRequest(string(body), "native-test"))
	if w.Code != 200 {
		t.Fatalf("redeem: %d", w.Code)
	}
	var result struct {
		Status string `json:"status"`
		Hash   string `json:"payment_hash"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Status != "unknown" || len(result.Hash) != 64 {
		t.Fatal("lost callback response claimed receipt")
	}
	invoice := cli("alice", "10009", "lookupinvoice", result.Hash)
	if invoice["settled"] != true || invoice["amt_paid_sat"] != "2" {
		t.Fatal("native invoice not settled for exact amount")
	}
	// The provider acknowledges the repeated callback without paying twice.
	entry := s.lnurlWithdrawals[preview.Token]
	originalInvoice := entry.invoice.PaymentRequest
	for i := 0; i < 2; i++ {
		w = httptest.NewRecorder()
		s.handleLNURLWithdraw(w, withdrawRequest(string(body), "native-test"))
		if w.Code != 200 || entry.status != "accepted" || entry.invoice.PaymentRequest != originalInvoice || callbacks.Load() != 2 {
			t.Fatal("retry changed invoice or sent duplicate accepted callback")
		}
	}
	if _, err := os.Stat(filepath.Join("/var/lib/lightningos", "wallet-activity.json")); err != nil {
		t.Fatal("new invoice not recorded in isolated wallet activity")
	}
}
