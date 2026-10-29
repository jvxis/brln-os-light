package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/walletrpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

type inventoryFixture struct {
	lease                    *walletrpc.UtxoLease
	history                  []*lnrpc.Transaction
	leaseError, historyError bool
	historyReads, mutations  int
	releasedDuringHistory    bool
}
type inventoryLightning struct {
	lnrpc.UnimplementedLightningServer
	f *inventoryFixture
}
type inventoryWallet struct {
	walletrpc.UnimplementedWalletKitServer
	f *inventoryFixture
}

func (f *inventoryLightning) ListUnspent(context.Context, *lnrpc.ListUnspentRequest) (*lnrpc.ListUnspentResponse, error) {
	return &lnrpc.ListUnspentResponse{Utxos: []*lnrpc.Utxo{{Outpoint: &lnrpc.OutPoint{TxidStr: strings.Repeat("a", 64)}, AmountSat: 10000, Confirmations: 2}}}, nil
}
func (f *inventoryLightning) GetTransactions(context.Context, *lnrpc.GetTransactionsRequest) (*lnrpc.TransactionDetails, error) {
	f.f.historyReads++
	if f.f.releasedDuringHistory {
		f.f.lease = nil
	}
	if f.f.historyError {
		return nil, status.Error(codes.Unavailable, "history unavailable")
	}
	return &lnrpc.TransactionDetails{Transactions: f.f.history}, nil
}
func (f *inventoryWallet) ListLeases(context.Context, *walletrpc.ListLeasesRequest) (*walletrpc.ListLeasesResponse, error) {
	if f.f.leaseError {
		return nil, status.Error(codes.Unavailable, "leases unavailable")
	}
	if f.f.lease == nil {
		return &walletrpc.ListLeasesResponse{}, nil
	}
	return &walletrpc.ListLeasesResponse{LockedUtxos: []*walletrpc.UtxoLease{f.f.lease}}, nil
}
func (f *inventoryWallet) LeaseOutput(context.Context, *walletrpc.LeaseOutputRequest) (*walletrpc.LeaseOutputResponse, error) {
	f.f.mutations++
	return nil, status.Error(codes.Unimplemented, "read must not lease")
}
func (f *inventoryWallet) ReleaseOutput(context.Context, *walletrpc.ReleaseOutputRequest) (*walletrpc.ReleaseOutputResponse, error) {
	f.f.mutations++
	return nil, status.Error(codes.Unimplemented, "read must not release")
}

func newInventoryTestServer(t *testing.T, f *inventoryFixture) *Server {
	t.Helper()
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(tlsServer.Close)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: tlsServer.TLS.Certificates, MinVersion: tls.VersionTLS12})))
	lnrpc.RegisterLightningServer(g, &inventoryLightning{f: f})
	walletrpc.RegisterWalletKitServer(g, &inventoryWallet{f: f})
	go g.Serve(l)
	t.Cleanup(g.Stop)
	dir := t.TempDir()
	cert, mac := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "fixture.macaroon")
	if err = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(mac, []byte("fictional-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	c := lndclient.New(&config.Config{LND: config.LNDConfig{GRPCHost: l.Addr().String(), TLSCertPath: cert, AdminMacaroonPath: mac}}, nil)
	t.Cleanup(func() { _ = c.Close() })
	// Metadata persistence is covered by native lab integration, not a fake DB.
	return &Server{lnd: c, logger: log.New(io.Discard, "", 0), utxoManagerInitAt: time.Now(), utxoManagerErr: "test metadata store unavailable"}
}
func inventoryCase() *inventoryFixture {
	txid := strings.Repeat("b", 64)
	id := sha256.Sum256([]byte("brln-os-utxo-mgr:v1:" + txid + ":1"))
	script := append([]byte{0, 20}, make([]byte, 20)...)
	return &inventoryFixture{lease: &walletrpc.UtxoLease{Outpoint: &lnrpc.OutPoint{TxidStr: txid, OutputIndex: 1}, Id: id[:], Expiration: uint64(time.Now().Add(time.Hour).Unix()), Value: 30000, PkScript: script}, history: []*lnrpc.Transaction{{TxHash: txid, NumConfirmations: 7, OutputDetails: []*lnrpc.OutputDetail{{OutputIndex: 1, Amount: 30000, Address: "native-wallet-address", PkScript: hex.EncodeToString(script), IsOurAddress: true}}}}}
}
func readInventory(t *testing.T, s *Server, query string) ([]EnrichedOnchainUtxo, int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleOnchainUtxos(w, httptest.NewRequest("GET", "/api/onchain/utxos"+query, nil))
	var result struct {
		Items []EnrichedOnchainUtxo `json:"items"`
	}
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result.Items, w.Code, w.Body.String()
}
func TestUtxoInventoryRetainsLeasedOutputAndPreservesSpendableReads(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "own", true: "external"}[foreign], func(t *testing.T) {
			f := inventoryCase()
			if foreign {
				f.lease.Id = make([]byte, 32)
			}
			s := newInventoryTestServer(t, f)
			rows, code, body := readInventory(t, s, "")
			if code != 200 || len(rows) != 2 {
				t.Fatalf("leased output disappeared: status=%d rows=%d body=%s", code, len(rows), body)
			}
			var locked *EnrichedOnchainUtxo
			for i := range rows {
				if rows[i].Txid == strings.Repeat("b", 64) {
					locked = &rows[i]
				}
			}
			if locked == nil || !locked.Locked || locked.LeaseManagedHere == foreign || locked.Confirmations != 7 || locked.AmountSat != 30000 || locked.Address != "native-wallet-address" || locked.PkScript != hex.EncodeToString(f.lease.PkScript) || locked.AddressType != "p2wkh" {
				t.Fatalf("incorrect leased row: %+v", locked)
			}
			if strings.Contains(body, hex.EncodeToString(f.lease.Id)) {
				t.Fatal("lease ID exposed")
			}
			spendable, err := s.lnd.ListOnchainUtxos(context.Background(), 0, 100)
			if err != nil || len(spendable) != 1 {
				t.Fatal("spendable caller behavior changed")
			}
			if f.mutations != 0 {
				t.Fatal("inventory mutated a lease")
			}
		})
	}
}
func TestUtxoInventoryFiltersAfterFullEnrichment(t *testing.T) {
	f := inventoryCase()
	s := newInventoryTestServer(t, f)
	for _, tc := range []struct {
		query string
		want  int
		txid  string
	}{{"?min_conf=6&limit=1", 1, strings.Repeat("b", 64)}, {"?max_conf=3", 1, strings.Repeat("a", 64)}, {"?min_conf=8", 0, ""}, {"?limit=1", 1, strings.Repeat("a", 64)}} {
		rows, code, _ := readInventory(t, s, tc.query)
		if code != 200 || len(rows) != tc.want || (tc.want > 0 && rows[0].Txid != tc.txid) {
			t.Fatalf("filter %s: status %d rows %+v", tc.query, code, rows)
		}
		live, seen := s.latestUtxoPruneLive()
		if !seen || len(live) != 2 {
			t.Fatal("filtered response discarded live metadata candidates")
		}
	}
}
func TestUtxoInventoryExpiredSpentAndMissingLeaseData(t *testing.T) {
	for _, mode := range []string{"none", "expired", "spent", "raw-spent", "unowned", "wrong-value", "wrong-script", "missing", "leases-error", "history-error", "released-during-read"} {
		t.Run(mode, func(t *testing.T) {
			f := inventoryCase()
			want := 1
			wantError := false
			switch mode {
			case "none":
				f.lease = nil
			case "expired":
				f.lease.Expiration = 1
			case "spent":
				f.history = append(f.history, &lnrpc.Transaction{TxHash: strings.Repeat("c", 64), PreviousOutpoints: []*lnrpc.PreviousOutPoint{{Outpoint: strings.Repeat("b", 64) + ":1"}}})
			case "raw-spent":
				hash, _ := chainhash.NewHashFromStr(strings.Repeat("b", 64))
				transaction := wire.NewMsgTx(2)
				transaction.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: *hash, Index: 1}, nil, nil))
				transaction.AddTxOut(wire.NewTxOut(20000, []byte{0x51}))
				var raw bytes.Buffer
				if err := transaction.Serialize(&raw); err != nil {
					t.Fatal(err)
				}
				f.history = append(f.history, &lnrpc.Transaction{TxHash: transaction.TxHash().String(), RawTxHex: hex.EncodeToString(raw.Bytes())})
			case "released-during-read":
				f.releasedDuringHistory = true
			case "wrong-value":
				f.lease.Value++
				wantError = true
			case "wrong-script":
				f.lease.PkScript = []byte{0x51}
				wantError = true
			case "unowned":
				f.history[0].OutputDetails[0].IsOurAddress = false
				wantError = true
			case "missing":
				f.history = nil
				wantError = true
			case "leases-error":
				f.leaseError = true
				wantError = true
			case "history-error":
				f.historyError = true
				wantError = true
			}
			s := newInventoryTestServer(t, f)
			rows, code, _ := readInventory(t, s, "")
			if wantError {
				if code == 200 {
					t.Fatal("incomplete native data returned a successful inventory")
				}
				if _, seen := s.latestUtxoPruneLive(); seen {
					t.Fatal("incomplete inventory can prune metadata")
				}
			} else if code != 200 || len(rows) != want {
				t.Fatalf("status=%d rows=%d", code, len(rows))
			}
			if (mode == "none" || mode == "expired") && f.historyReads != 0 {
				t.Fatal("ordinary read fetched wallet history")
			}
			if f.mutations != 0 {
				t.Fatal("inventory mutated native leases")
			}
		})
	}
}

func TestUtxoInventoryNormalizesByteOutpointAndUnconfirmedDepth(t *testing.T) {
	f := inventoryCase()
	f.lease.Outpoint.TxidStr = ""
	f.lease.Outpoint.TxidBytes = bytes.Repeat([]byte{0xbb}, 32)
	f.history[0].NumConfirmations = 0
	s := newInventoryTestServer(t, f)
	rows, code, _ := readInventory(t, s, "?max_conf=0")
	if code != 200 || len(rows) != 1 || rows[0].Outpoint != strings.Repeat("b", 64)+":1" || rows[0].Confirmations != 0 || !rows[0].Locked {
		t.Fatalf("bad byte outpoint/unconfirmed snapshot: %d %+v", code, rows)
	}
	rows, code, _ = readInventory(t, s, "?include_unconfirmed=false")
	if code != 200 || len(rows) != 1 || rows[0].Txid != strings.Repeat("a", 64) {
		t.Fatal("unconfirmed filter changed")
	}
}
