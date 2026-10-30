package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

func financialNativeTransactionFixture(t *testing.T) (financialIntentBinding, *lnrpc.Transaction) {
	t.Helper()
	hash, err := chainhash.NewHashFromStr(strings.Repeat("12", 32))
	if err != nil {
		t.Fatal(err)
	}
	point := wire.OutPoint{Hash: *hash, Index: 1}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&point, nil, nil))
	script := append([]byte{0, 20}, bytes.Repeat([]byte{1}, 20)...)
	tx.AddTxOut(wire.NewTxOut(1000, script))
	tx.AddTxOut(wire.NewTxOut(8000, []byte{0x51}))
	var raw bytes.Buffer
	if tx.Serialize(&raw) != nil {
		t.Fatal("transaction fixture serialization")
	}
	fee := int64(2000)
	binding := financialIntentBinding{Kind: "onchain_send", NativeLabel: "los-intent:" + strings.Repeat("a", 32), DestinationScript: hex.EncodeToString(script), Inputs: []lndclient.FinancialIntentInput{{Outpoint: point.String(), AmountSat: 10000}}, Summary: financialIntentSummary{Destination: "fixture-destination", AmountSat: 1000, SatPerVbyte: 2, MaxFeeSat: &fee}}
	transaction := &lnrpc.Transaction{TxHash: tx.TxHash().String(), RawTxHex: hex.EncodeToString(raw.Bytes()), Label: binding.NativeLabel, OutputDetails: []*lnrpc.OutputDetail{{OutputIndex: 1, IsOurAddress: true}}}
	return binding, transaction
}

func TestFinancialIntentNativeTransactionProof(t *testing.T) {
	binding, tx := financialNativeTransactionFixture(t)
	if err := validateFinancialOnchainResult(binding, tx); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*financialIntentBinding, *lnrpc.Transaction){
		func(b *financialIntentBinding, tx *lnrpc.Transaction) { b.Summary.AmountSat++ },
		func(b *financialIntentBinding, tx *lnrpc.Transaction) { b.DestinationScript = "51" },
		func(b *financialIntentBinding, tx *lnrpc.Transaction) {
			b.Inputs[0].Outpoint = strings.Repeat("0", 64) + ":0"
		},
		func(b *financialIntentBinding, tx *lnrpc.Transaction) { tx.TxHash = strings.Repeat("0", 64) },
		func(b *financialIntentBinding, tx *lnrpc.Transaction) { tx.Label = "operator-label" },
		func(b *financialIntentBinding, tx *lnrpc.Transaction) { tx.OutputDetails = nil },
		func(b *financialIntentBinding, tx *lnrpc.Transaction) { tx.RawTxHex += "00" },
		func(b *financialIntentBinding, tx *lnrpc.Transaction) { fee := int64(999); b.Summary.MaxFeeSat = &fee },
	} {
		b, transaction := financialNativeTransactionFixture(t)
		change(&b, transaction)
		if validateFinancialOnchainResult(b, transaction) == nil {
			t.Fatal("changed native evidence accepted")
		}
	}
}

func TestFinancialIntentLostNativeReplyReconcilesWithoutSecondSend(t *testing.T) {
	s, old, session := financialHTTPFixture(t)
	if _, err := s.financialIntents.cancelDraft(old.ID); err != nil {
		t.Fatal(err)
	}
	binding, transaction := financialNativeTransactionFixture(t)
	id, _ := newFinancialIntentID()
	binding.NativeLabel = "los-intent:" + id
	transaction.Label = binding.NativeLabel
	request := walletSendRequest{Address: binding.Summary.Destination, AmountSat: 1000, SatPerVbyte: 2, Outpoints: []string{binding.Inputs[0].Outpoint}, Label: "operator note"}
	payload, _ := json.Marshal(request)
	snapshot, _ := json.Marshal(binding)
	record, err := s.financialIntents.create(id, session.ID, "/api/wallet/send", authScopeWalletSendExternal, payload, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	s.auth.sessions[session.ID].ReauthScopes[financialIntentScopePrefix+id] = record.ExpiresAt
	var sends, lookups atomic.Int64
	accepted := false
	s.financialNativeOps = &financialNativeOperations{
		sendOnchain: func(ctx context.Context, p lndclient.SendCoinsParams) (string, error) {
			sends.Add(1)
			if p.Label != binding.NativeLabel || len(p.Outpoints) != 1 || p.Outpoints[0] != binding.Inputs[0].Outpoint {
				t.Error("native send lost approved inputs or durable lookup label")
			}
			stored, _, _ := s.financialIntents.get(id)
			if len(stored.Proof) == 0 {
				t.Error("native dispatch preceded durable proof")
			}
			return "", errors.New("reply lost after native send")
		},
		findOnchain: func(context.Context, string, int32) ([]*lnrpc.Transaction, error) {
			lookups.Add(1)
			return []*lnrpc.Transaction{transaction}, nil
		},
		mempoolContains: func(context.Context, string) (bool, error) { return accepted, nil },
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := s.executeFinancialOnchainIntent(r.Context(), r, request)
		if err != nil {
			writeError(w, 503, "native reply unavailable")
			return
		}
		t.Error("lost reply was reported as success")
	})
	w := financialHTTPRequest(s, handler, session, id, string(payload), context.Background())
	if w.Code != 503 || sends.Load() != 1 {
		t.Fatal("native failure fixture did not dispatch once")
	}
	stored, _, _ := s.financialIntents.get(id)
	if stored.State != financialIntentUncertainState {
		t.Fatal("lost reply reopened execution")
	}
	if _, _, err := s.observeFinancialIntent(context.Background(), stored); err == nil {
		t.Fatal("wallet history alone was treated as accepted broadcast")
	}
	accepted = true
	status, body, err := s.observeFinancialIntent(context.Background(), stored)
	if err != nil || status != 200 {
		t.Fatal("native mempool evidence failed reconciliation", err)
	}
	resolved, err := s.financialIntents.reconcile(id, status, body)
	if err != nil || resolved.State != financialIntentCompletedState {
		t.Fatal("native result not durably committed", err)
	}
	retry := financialHTTPRequest(s, handler, session, id, string(payload), context.Background())
	if retry.Code != 200 || !strings.Contains(retry.Body.String(), transaction.TxHash) || sends.Load() != 1 || lookups.Load() != 2 {
		t.Fatal("reconciled retry duplicated native send or lost original transaction")
	}
	if _, err := s.financialIntents.reconcile(id, 409, []byte(`{"error":"changed"}`)); err != nil {
		t.Fatal(err)
	}
	stable, _, _ := s.financialIntents.get(id)
	if stable.ResponseStatus != 200 {
		t.Fatal("later reconciliation changed a terminal result")
	}
}

func TestFinancialIntentUncertainOnchainBlocksAnotherDraft(t *testing.T) {
	s, first := financialStoreFixture(t)
	id, _ := newFinancialIntentID()
	second, err := s.create(id, "fictional-private-cookie", first.Path, first.BaseScope, first.Request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, started, err := s.claim(first.ID, "fictional-private-cookie", first.Path, first.Request, func(string) bool { return true }); err != nil || !started {
		t.Fatal(err)
	}
	consumed := false
	consume := func(string) bool { consumed = true; return true }
	if _, started, err := s.claim(second.ID, "fictional-private-cookie", second.Path, second.Request, consume); !errors.Is(err, errFinancialIntentPending) || started || consumed {
		t.Fatal("concurrent second on-chain operation was dispatched or consumed approval")
	}
	if err := s.finish(first.ID, 503, []byte(`{"error":"unknown"}`), true); err != nil {
		t.Fatal(err)
	}
	if _, started, err := s.claim(second.ID, "fictional-private-cookie", second.Path, second.Request, consume); !errors.Is(err, errFinancialIntentPending) || started || consumed {
		t.Fatal("uncertain operation allowed a new transfer")
	}
	s.persistHook = func(*financialIntentRecord) error { return errors.New("disk full") }
	if _, err := s.reconcile(first.ID, 200, []byte(`{"txid":"found"}`)); err == nil {
		t.Fatal("failed durable reconciliation accepted")
	}
	s.persistHook = nil
	if !s.hasUncertainOperation(first.Path) {
		t.Fatal("failed reconciliation reopened wallet")
	}
	if _, err := s.reconcile(first.ID, 200, []byte(`{"txid":"found"}`)); err != nil {
		t.Fatal(err)
	}
	if _, started, err := s.claim(second.ID, "fictional-private-cookie", second.Path, second.Request, consume); err != nil || !started || !consumed {
		t.Fatal("confirmed previous result did not permit independently approved next operation", err)
	}
}
