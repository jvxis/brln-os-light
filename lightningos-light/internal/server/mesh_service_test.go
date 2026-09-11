package server

import (
	"bytes"
	"context"
	"errors"
	"github.com/btcsuite/btcd/wire"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/mesh"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type meshTestWallet struct {
	published, paid, signed int
	readyErr, publishErr    error
}

func TestMeshInvoiceRequestNeedsLocalAction(t *testing.T) {
	m := &meshService{pending: map[string]*meshPending{}}
	p := mesh.Packet{Kind: mesh.PaymentRequest, From: 1, To: 2, Expires: time.Now().Add(time.Minute).Unix()}
	p.Session[0] = 1
	if got := m.acceptRequest(context.Background(), p, []byte(`{"kind":"invoice_request","amount_sat":25,"memo":"test"}`)); got != "awaiting_approval" {
		t.Fatal(got)
	}
	if pending := m.pending[sessionID(p)]; pending.Kind != "invoice_request" || pending.Amount != 25 || pending.Invoice != "" {
		t.Fatal("invoice request was not preserved for explicit local action")
	}
	for _, raw := range []string{
		`{"kind":"invoice_request","amount_sat":0}`,
		`{"kind":"invoice_request","amount_sat":100000001}`,
		`{"kind":"invoice_request","amount_sat":1,"address":"unexpected"}`,
		`{"kind":"unsupported","amount_sat":1}`,
		`{"kind":"invoice_request","amount_sat":1} {}`,
	} {
		if got := m.acceptRequest(context.Background(), p, []byte(raw)); got != "rejected" {
			t.Fatal("accepted invalid request", raw)
		}
	}
}

func (w *meshTestWallet) MeshMainnetReady(context.Context) error { return w.readyErr }
func (w *meshTestWallet) PublishTransaction(context.Context, string, string) error {
	w.published++
	return w.publishErr
}
func (w *meshTestWallet) FundMeshTransaction(context.Context, string, int64, int64) (*lndclient.MeshFundedTransaction, error) {
	return nil, errors.New("no funds in test")
}
func (w *meshTestWallet) FinalizeMeshTransaction(context.Context, *lndclient.MeshFundedTransaction) ([]byte, error) {
	w.signed++
	return nil, errors.New("not authorized in test")
}
func (w *meshTestWallet) ReleaseMeshTransaction(context.Context, *lndclient.MeshFundedTransaction) {}
func (w *meshTestWallet) CreateInvoice(context.Context, int64, string, int64, *lndclient.CreateInvoiceOptions) (lndclient.CreatedInvoice, error) {
	return lndclient.CreatedInvoice{}, errors.New("not used")
}
func (w *meshTestWallet) DecodeInvoice(context.Context, string) (lndclient.DecodedInvoice, error) {
	return lndclient.DecodedInvoice{AmountSat: 10, AmountMsat: 10000, PaymentHash: strings.Repeat("a", 64), Destination: strings.Repeat("b", 66), Expiry: 1200, Timestamp: time.Now().Unix()}, nil
}
func (w *meshTestWallet) PayMeshInvoice(context.Context, string, int64) error {
	w.paid++
	return nil
}

type meshRoundTrip func(*http.Request) (*http.Response, error)

func (f meshRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func meshIntegrationService(t *testing.T) (*meshService, *meshTestWallet) {
	t.Helper()
	dsn := os.Getenv("LOS_MESH_TEST_DSN")
	if dsn == "" {
		t.Skip("set LOS_MESH_TEST_DSN to a dedicated los_mesh_test_* PostgreSQL database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "los_mesh_test_") {
		t.Fatal("dedicated test database required")
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{db: pool, auth: &AuthService{enabled: true}, shutdownCtx: ctx}
	m, err := s.meshService()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	// The database name guard above is mandatory before clearing test fixtures.
	if _, err = pool.Exec(context.Background(), "TRUNCATE los_mesh_sessions,los_mesh_publications,los_mesh_payments,los_mesh_peers"); err != nil {
		t.Fatal(err)
	}
	wallet := &meshTestWallet{}
	m.wallet = wallet
	m.client = &http.Client{Transport: meshRoundTrip(func(r *http.Request) (*http.Response, error) {
		body := "{}"
		status := 202
		if r.URL.Path == "/status" {
			body = `{"state":"running","node":20}`
			status = 200
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	return m, wallet
}
func meshTestRaw() []byte {
	tx := wire.NewMsgTx(2)
	var hash [32]byte
	hash[0] = 1
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: hash, Index: 0}, []byte{1, 1}, nil))
	tx.AddTxOut(wire.NewTxOut(100, []byte{0x51}))
	var raw bytes.Buffer
	_ = tx.Serialize(&raw)
	return raw.Bytes()
}
func TestMeshRelayDurableIdempotency(t *testing.T) {
	m, w := meshIntegrationService(t)
	ctx := context.Background()
	raw := meshTestRaw()
	packets, _ := mesh.Fragment(10, 20, mesh.Transaction, raw, time.Now())
	peer := meshContact{Node: 10, Paired: true, AllowRelay: true, Key: bytes.Repeat([]byte{7}, 32)}
	m.receive(ctx, packets[0], peer, "send")
	if w.published != 0 {
		t.Fatal("default mode published")
	}
	m.receive(ctx, packets[0], peer, "relay")
	m.receive(ctx, packets[0], peer, "relay")
	if w.published != 1 {
		t.Fatalf("published %d times", w.published)
	}
	newSession, _ := mesh.Fragment(10, 20, mesh.Transaction, raw, time.Now())
	m.receive(ctx, newSession[0], peer, "relay")
	if w.published != 1 {
		t.Fatal("duplicate txid bypassed persistent ledger")
	}
	var count int
	if err := m.db.QueryRow(ctx, "SELECT count(*) FROM los_mesh_publications WHERE state='published'").Scan(&count); err != nil || count != 1 {
		t.Fatal("publication not durably recorded")
	}
}
func TestMeshRelayFailsClosedAndNeverRetriesAmbiguousPublication(t *testing.T) {
	m, w := meshIntegrationService(t)
	ctx := context.Background()
	w.readyErr = errors.New("wrong network")
	if m.publish(ctx, meshTestRaw()) != "rejected" || w.published != 0 {
		t.Fatal("wrong network broadcast")
	}
	w.readyErr = nil
	w.publishErr = errors.New("transport failure")
	if m.publish(ctx, meshTestRaw()) != "publication_unknown" {
		t.Fatal("uncertain result misrepresented")
	}
	if m.publish(ctx, meshTestRaw()) != "publication_unknown" || w.published != 1 {
		t.Fatal("ambiguous publication retried")
	}
	m.server.auth.enabled = false
	if m.publish(ctx, meshTestRaw()) != "rejected" {
		t.Fatal("disabled login accepted")
	}
}
func TestMeshInvoiceArrivalCannotSpend(t *testing.T) {
	m, w := meshIntegrationService(t)
	ctx := context.Background()
	raw := []byte("lnbc10n1simulated")
	packets, _ := mesh.Fragment(10, 20, mesh.Invoice, raw, time.Now())
	peer := meshContact{Node: 10, Paired: true, Key: bytes.Repeat([]byte{3}, 32)}
	m.receive(ctx, packets[0], peer, "send")
	if w.paid != 0 || w.signed != 0 || w.published != 0 {
		t.Fatal("radio receipt spent funds")
	}
	if len(m.pending) != 1 {
		t.Fatal("invoice not queued for manual approval")
	}
	var state string
	if err := m.db.QueryRow(ctx, "SELECT state FROM los_mesh_sessions WHERE id=$1", sessionID(packets[0])).Scan(&state); err != nil || state != "awaiting_approval" {
		t.Fatal("missing approval state", state)
	}
	if _, err := m.pay(ctx, meshAPIRequest{ID: sessionID(packets[0]), Amount: 11, MaxFee: 1}); err == nil || w.paid != 0 {
		t.Fatal("changed amount accepted")
	}
	if err := m.cancel(ctx, sessionID(packets[0]), ""); err != nil || len(m.pending) != 0 {
		t.Fatal("cancel did not remove invoice")
	}
}
func TestMeshRestartInterruptsPartialSession(t *testing.T) {
	m, w := meshIntegrationService(t)
	ctx := context.Background()
	packets, _ := mesh.Fragment(10, 20, mesh.Transaction, bytes.Repeat([]byte{9}, 241), time.Now())
	peer := meshContact{Node: 10, Paired: true, AllowRelay: true, Key: bytes.Repeat([]byte{3}, 32)}
	m.receive(ctx, packets[0], peer, "relay")
	boot, cancel := context.WithCancel(context.Background())
	s := &Server{db: m.db, auth: m.server.auth, shutdownCtx: boot}
	restarted, err := s.meshService()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	restarted.wallet = w
	restarted.client = m.client
	for _, p := range packets {
		restarted.receive(ctx, p, peer, "relay")
	}
	if len(restarted.incoming) != 0 || w.published != 0 {
		t.Fatal("restart replay revived interrupted session")
	}
}
func TestMeshRequestValidation(t *testing.T) {
	if validateMeshAddress(meshAddressRequest{Address: "tb1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq0l98cr", Amount: 1}) == nil {
		t.Fatal("testnet request accepted")
	}
	var raw []byte
	if _, err := mesh.ValidateTransaction(raw); err == nil {
		t.Fatal("empty transaction accepted")
	}
	wallet := &meshTestWallet{}
	m := &meshService{wallet: wallet}
	for _, invoice := range []string{"lntb123", "lnbcrt123", "", strings.Repeat("x", 5000)} {
		if _, err := m.decodeInvoice(context.Background(), invoice); err == nil {
			t.Fatal("invalid invoice accepted")
		}
	}
}

func TestMeshPreviewCannotBeUsedAcrossLoginSessions(t *testing.T) {
	wallet := &meshTestWallet{}
	m := &meshService{wallet: wallet, proposals: map[string]*meshProposal{"token": {Owner: "owner", Expires: time.Now().Add(time.Minute)}}}
	if _, err := m.approveSend(context.Background(), meshAPIRequest{ID: "token", Owner: "other"}); err == nil {
		t.Fatal("foreign preview approved")
	}
	if err := m.cancel(context.Background(), "token", "other"); err == nil {
		t.Fatal("foreign preview cancelled")
	}
	if wallet.signed != 0 || wallet.published != 0 {
		t.Fatal("foreign session spent funds")
	}
}
