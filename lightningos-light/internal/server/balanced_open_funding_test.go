package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/wire"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"io"
	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/signrpc"
	"lightningos-light/lnrpc/walletrpc"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func balancedTestKey(n byte) []byte {
	seed := make([]byte, 32)
	seed[31] = n
	_, pub := btcec.PrivKeyFromBytes(seed)
	return pub.SerializeCompressed()
}

type balancedFundingRPC struct {
	lnrpc.UnimplementedLightningServer
	openEntered                                chan struct{}
	openRelease                                chan struct{}
	opens                                      atomic.Int64
	mu                                         sync.Mutex
	utxos                                      []*lnrpc.Utxo
	history                                    []*lnrpc.Transaction
	exposeHistory                              atomic.Bool
	sends, signs, keys, messages               atomic.Int64
	loseReply, hideUTXO, failMessage, failSign atomic.Bool
	onMessage                                  func(balancedOpenProtocolMessage)
}

func (r *balancedFundingRPC) OpenChannelSync(ctx context.Context, _ *lnrpc.OpenChannelRequest) (*lnrpc.ChannelPoint, error) {
	n := r.opens.Add(1)
	if r.openEntered != nil {
		if n == 1 {
			close(r.openEntered)
		}
		select {
		case <-r.openRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, status.Error(codes.PermissionDenied, "simulated channel-open rejection; no funds exist")
}

func (r *balancedFundingRPC) WalletBalance(context.Context, *lnrpc.WalletBalanceRequest) (*lnrpc.WalletBalanceResponse, error) {
	return &lnrpc.WalletBalanceResponse{TotalBalance: 10000000, ConfirmedBalance: 10000000}, nil
}
func (r *balancedFundingRPC) FundingStateStep(context.Context, *lnrpc.FundingTransitionMsg) (*lnrpc.FundingStateStepResp, error) {
	return &lnrpc.FundingStateStepResp{}, nil
}
func (r *balancedFundingRPC) GetInfo(context.Context, *lnrpc.GetInfoRequest) (*lnrpc.GetInfoResponse, error) {
	return &lnrpc.GetInfoResponse{IdentityPubkey: hex.EncodeToString(balancedTestKey(99))}, nil
}
func (r *balancedFundingRPC) ListUnspent(context.Context, *lnrpc.ListUnspentRequest) (*lnrpc.ListUnspentResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hideUTXO.Load() {
		return &lnrpc.ListUnspentResponse{}, nil
	}
	return &lnrpc.ListUnspentResponse{Utxos: append([]*lnrpc.Utxo(nil), r.utxos...)}, nil
}
func (r *balancedFundingRPC) GetTransactions(context.Context, *lnrpc.GetTransactionsRequest) (*lnrpc.TransactionDetails, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.exposeHistory.Load() {
		return &lnrpc.TransactionDetails{}, nil
	}
	return &lnrpc.TransactionDetails{Transactions: append([]*lnrpc.Transaction(nil), r.history...)}, nil
}
func (r *balancedFundingRPC) SendCustomMessage(_ context.Context, req *lnrpc.SendCustomMessageRequest) (*lnrpc.SendCustomMessageResponse, error) {
	r.messages.Add(1)
	if r.failMessage.Swap(false) {
		return nil, status.Error(codes.Unavailable, "simulated transport failure")
	}
	if r.onMessage != nil {
		var msg balancedOpenProtocolMessage
		_ = json.Unmarshal(req.Data, &msg)
		r.onMessage(msg)
	}
	return &lnrpc.SendCustomMessageResponse{}, nil
}

type balancedWalletRPC struct {
	walletrpc.UnimplementedWalletKitServer
	capture *balancedFundingRPC
}

func (w *balancedWalletRPC) DeriveNextKey(_ context.Context, req *walletrpc.KeyReq) (*signrpc.KeyDescriptor, error) {
	n := w.capture.keys.Add(1)
	return &signrpc.KeyDescriptor{RawKeyBytes: balancedTestKey(byte(n)), KeyLoc: &signrpc.KeyLocator{KeyFamily: req.KeyFamily, KeyIndex: int32(n)}}, nil
}
func (w *balancedWalletRPC) SendOutputs(_ context.Context, req *walletrpc.SendOutputsRequest) (*walletrpc.SendOutputsResponse, error) {
	r := w.capture
	n := r.sends.Add(1)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Index: uint32(n)}})
	for _, out := range req.Outputs {
		tx.AddTxOut(&wire.TxOut{Value: out.Value, PkScript: out.PkScript})
	}
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.history = append(r.history, &lnrpc.Transaction{TxHash: tx.TxHash().String(), RawTxHex: hex.EncodeToString(raw.Bytes()), Label: req.Label})
	for i, out := range tx.TxOut {
		r.utxos = append(r.utxos, &lnrpc.Utxo{AmountSat: out.Value, PkScript: hex.EncodeToString(out.PkScript), Outpoint: &lnrpc.OutPoint{TxidStr: tx.TxHash().String(), OutputIndex: uint32(i)}})
	}
	r.mu.Unlock()
	if r.loseReply.Swap(false) {
		return nil, status.Error(codes.Unavailable, "simulated lost prefunding response")
	}
	return &walletrpc.SendOutputsResponse{RawTx: raw.Bytes()}, nil
}

type balancedSignerRPC struct {
	signrpc.UnimplementedSignerServer
	capture *balancedFundingRPC
}

func (s *balancedSignerRPC) ComputeInputScript(context.Context, *signrpc.SignReq) (*signrpc.InputScriptResp, error) {
	s.capture.signs.Add(1)
	if s.capture.failSign.Swap(false) {
		return nil, status.Error(codes.Unavailable, "simulated signing failure")
	}
	return &signrpc.InputScriptResp{InputScripts: []*signrpc.InputScript{{Witness: [][]byte{{1, 2, 3}, balancedTestKey(2)}}}}, nil
}
func balancedFundingFixture(t *testing.T, p *pgxpool.Pool) (*Server, *balancedFundingRPC, authSessionSnapshot) {
	t.Helper()
	cert := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer cert.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &balancedFundingRPC{}
	g := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: cert.TLS.Certificates, MinVersion: tls.VersionTLS12})))
	lnrpc.RegisterLightningServer(g, r)
	walletrpc.RegisterWalletKitServer(g, &balancedWalletRPC{capture: r})
	signrpc.RegisterSignerServer(g, &balancedSignerRPC{capture: r})
	go g.Serve(listener)
	t.Cleanup(g.Stop)
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	macPath := filepath.Join(dir, "fixture.macaroon")
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(macPath, []byte("fixture-not-a-real-macaroon"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.LND.GRPCHost = listener.Addr().String()
	cfg.LND.TLSCertPath = certPath
	cfg.LND.AdminMacaroonPath = macPath
	logger := log.New(io.Discard, "", 0)
	client := lndclient.New(cfg, logger)
	t.Cleanup(func() { client.Close() })
	auth := NewAuthService(cfg, logger)
	session, err := auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	svc := NewBalancedOpenService(p, client, logger, nil)
	return &Server{cfg: cfg, lnd: client, auth: auth, logger: logger, balancedOpen: svc, auditLogInitAt: time.Now()}, r, session
}
func balancedFundingCall(s *Server, snap authSessionSnapshot, id, action string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "https://node.example/api/lnops/balanced-open/sessions/"+id+"/"+action, strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: snap.ID})
	req.Header.Set("Origin", "https://node.example")
	req.Header.Set("X-CSRF-Token", snap.CSRFToken)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	return w
}
func balancedApprove(s *Server, snap authSessionSnapshot) {
	s.auth.mu.Lock()
	s.auth.sessions[snap.ID].ReauthScopes[authScopeLightningFunds] = time.Now().Add(time.Minute)
	s.auth.mu.Unlock()
}
func TestBalancedPrefundingApprovalAndLostResponse(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(map[bool]string{false: "reconcile", true: "unresolved"}[hidden], func(t *testing.T) {
			p := balancedSecurityPool(t)
			s, r, snap := balancedFundingFixture(t, p)
			ctx := context.Background()
			svc := s.balancedOpen
			session, err := svc.CreateSession(ctx, BalancedOpenCreateParams{PeerPubkey: hex.EncodeToString(balancedTestKey(88)), CapacitySat: 100000, FeeRateSatVb: 1})
			if err != nil {
				t.Fatal(err)
			}
			if w := balancedFundingCall(s, snap, session.SessionID, "propose"); w.Code != 428 || r.sends.Load() != 0 || r.keys.Load() != 0 || r.signs.Load() != 0 {
				t.Fatal("unapproved proposal reached wallet")
			}
			balancedApprove(s, snap)
			r.loseReply.Store(true)
			r.hideUTXO.Store(hidden)
			w := balancedFundingCall(s, snap, session.SessionID, "propose")
			if w.Code != 409 || r.sends.Load() != 1 {
				t.Fatalf("lost reply must become unresolved, HTTP=%d sends=%d", w.Code, r.sends.Load())
			}
			s.balancedOpen = NewBalancedOpenService(p, s.lnd, s.logger, nil) // restart service state
			w = balancedFundingCall(s, snap, session.SessionID, "propose")
			expected := 200
			if hidden {
				expected = 409
			}
			if w.Code != expected || r.sends.Load() != 1 {
				t.Fatalf("retry duplicated funding or failed: HTTP=%d sends=%d", w.Code, r.sends.Load())
			}
			if hidden {
				if _, err = svc.CancelSession(ctx, session.SessionID, "fixture cancellation"); err != nil {
					t.Fatal(err)
				}
				r.hideUTXO.Store(false)
				fresh, _ := svc.GetSession(ctx, session.SessionID)
				meta, _ := decodeBalancedOpenMetadata(fresh.Metadata)
				meta, err = svc.reconcileBalancedPrefund(ctx, fresh, meta)
				if err != nil || !hasBalancedTransit(meta.InitiatorTransit, true) || r.sends.Load() != 1 {
					t.Fatal("canceled session could not reconcile without another transfer", err)
				}
			} else {
				w = balancedFundingCall(s, snap, session.SessionID, "propose")
				if w.Code != 200 || r.sends.Load() != 1 {
					t.Fatal("idempotent proposal retry failed")
				}
			}
		})
	}
}
func TestBalancedAcceptApprovalSigningAndRetry(t *testing.T) {
	p := balancedSecurityPool(t)
	s, r, snap := balancedFundingFixture(t, p)
	ctx := context.Background()
	svc := s.balancedOpen
	peer := hex.EncodeToString(balancedTestKey(88))
	script, _ := balancedP2WPKHScriptHex(peer)
	msg := balancedOpenProtocolMessage{Version: 1, Kind: "proposal", SessionID: "accept-funding", CapacitySat: 100000, FeeRateSatVb: 1, ExecutionMode: balancedOpenExecutionModeDual, MultisigPubkey: peer, TransitTxID: strings.Repeat("11", 32), TransitOutputSat: 50095, TransitOutputPk: script}
	if _, err := svc.upsertSessionFromProposal(ctx, peer, msg); err != nil {
		t.Fatal(err)
	}
	if w := balancedFundingCall(s, snap, msg.SessionID, "accept"); w.Code != 428 || r.sends.Load() != 0 || r.signs.Load() != 0 {
		t.Fatal("unapproved accept reached funding/signing")
	}
	balancedApprove(s, snap)
	r.failSign.Store(true)
	if w := balancedFundingCall(s, snap, msg.SessionID, "accept"); w.Code != 500 || r.sends.Load() != 1 {
		t.Fatalf("sign failure fixture, HTTP=%d sends=%d body=%s", w.Code, r.sends.Load(), w.Body.String())
	}
	if _, err := p.Exec(ctx, `update balanced_open_sessions set created_at=now()-interval '8 days',state_updated_at=now()-interval '8 days' where session_id=$1`, msg.SessionID); err != nil {
		t.Fatal(err)
	}
	if w := balancedFundingCall(s, snap, msg.SessionID, "accept"); w.Code != 200 || r.sends.Load() != 1 || r.signs.Load() != 2 {
		t.Fatalf("accept retry duplicated prefunding, HTTP=%d sends=%d signs=%d body=%s", w.Code, r.sends.Load(), r.signs.Load(), w.Body.String())
	}
	if w := balancedFundingCall(s, snap, msg.SessionID, "accept"); w.Code != 200 || r.sends.Load() != 1 || r.signs.Load() != 2 {
		t.Fatal("accepted retry mutated artifacts")
	}
	before, _ := svc.GetSession(ctx, msg.SessionID)
	changed := msg
	changed.TransitTxID = strings.Repeat("22", 32)
	if _, err := svc.upsertSessionFromProposal(ctx, peer, changed); err == nil {
		t.Fatal("accepted dual session allowed remote artifact replacement")
	}
	if _, err := svc.upsertSessionFromProposal(ctx, peer, msg); err != nil {
		t.Fatal("original dual proposal retry rejected", err)
	}
	after, _ := svc.GetSession(ctx, msg.SessionID)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("dual proposal retry changed local keys/witness/state")
	}
}

func TestBalancedLostReplyReconcilesWalletHistoryWithoutManagedUTXO(t *testing.T) {
	p := balancedSecurityPool(t)
	s, r, snap := balancedFundingFixture(t, p)
	session, err := s.balancedOpen.CreateSession(context.Background(), BalancedOpenCreateParams{PeerPubkey: hex.EncodeToString(balancedTestKey(88)), CapacitySat: 100000, FeeRateSatVb: 1})
	if err != nil {
		t.Fatal(err)
	}
	balancedApprove(s, snap)
	r.loseReply.Store(true)
	r.hideUTXO.Store(true) // Custom key-family transit is not a managed wallet UTXO in real LND.
	r.exposeHistory.Store(true)
	if w := balancedFundingCall(s, snap, session.SessionID, "propose"); w.Code != 409 || r.sends.Load() != 1 {
		t.Fatalf("expected uncertain first send, HTTP=%d sends=%d", w.Code, r.sends.Load())
	}
	s.balancedOpen = NewBalancedOpenService(p, s.lnd, s.logger, nil)
	if w := balancedFundingCall(s, snap, session.SessionID, "propose"); w.Code != 200 || r.sends.Load() != 1 {
		t.Fatalf("wallet history must reconcile the original un-managed output, HTTP=%d sends=%d", w.Code, r.sends.Load())
	}
}
func TestBalancedImmediateAcceptDoesNotRegressState(t *testing.T) {
	p := balancedSecurityPool(t)
	s, r, _ := balancedFundingFixture(t, p)
	ctx := context.Background()
	peer := hex.EncodeToString(balancedTestKey(88))
	svc := s.balancedOpen
	session, err := svc.CreateSession(ctx, BalancedOpenCreateParams{PeerPubkey: peer, CapacitySat: 100000, Metadata: json.RawMessage(`{"execution_mode":"push_sat_v1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	r.onMessage = func(msg balancedOpenProtocolMessage) {
		_, err := svc.markAcceptedFromPeer(ctx, peer, balancedOpenProtocolMessage{Version: 1, Kind: "accept", SessionID: msg.SessionID, ExecutionMode: balancedOpenExecutionModePush})
		if err != nil {
			t.Error("immediate accept", err)
		}
	}
	proposed, err := svc.ProposeSession(ctx, session.SessionID)
	if err != nil || proposed.State != balancedOpenStateAccepted {
		t.Fatalf("immediate acceptance lost: state=%s err=%v", proposed.State, err)
	}
}
func TestBalancedConcurrentPrefundingAcrossServiceInstances(t *testing.T) {
	p := balancedSecurityPool(t)
	s, r, _ := balancedFundingFixture(t, p)
	ctx := context.Background()
	session, err := s.balancedOpen.CreateSession(ctx, BalancedOpenCreateParams{PeerPubkey: hex.EncodeToString(balancedTestKey(88)), CapacitySat: 100000, FeeRateSatVb: 1})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc := NewBalancedOpenService(p, s.lnd, s.logger, nil)
			_, _, _ = svc.ensureBalancedTransit(ctx, session, balancedOpenRoleInitiator)
		}()
	}
	wg.Wait()
	if r.sends.Load() != 1 {
		t.Fatalf("concurrent prefunding sent %d times", r.sends.Load())
	}
}

func TestBalancedConcurrentExecutionCannotSubmitTwice(t *testing.T) {
	p := balancedSecurityPool(t)
	s, r, _ := balancedFundingFixture(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	svc := s.balancedOpen
	session, err := svc.CreateSession(ctx, BalancedOpenCreateParams{PeerPubkey: hex.EncodeToString(balancedTestKey(88)), CapacitySat: 100000, Metadata: json.RawMessage(`{"execution_mode":"push_sat_v1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `update balanced_open_sessions set state='accepted' where session_id=$1`, session.SessionID); err != nil {
		t.Fatal(err)
	}
	r.openEntered = make(chan struct{})
	r.openRelease = make(chan struct{})
	done := make(chan struct{})
	go func() { defer close(done); _, _ = svc.ExecuteSession(ctx, session.SessionID) }()
	select {
	case <-r.openEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("channel-open fixture did not start")
	}
	_, err = svc.ExecuteSession(ctx, session.SessionID)
	close(r.openRelease)
	<-done
	if err == nil || r.opens.Load() != 1 {
		t.Fatalf("concurrent execution submitted twice: count=%d err=%v", r.opens.Load(), err)
	}
}

func TestBalancedProtocolRecipientAndPayloadBoundaries(t *testing.T) {
	p := balancedSecurityPool(t)
	s, r, _ := balancedFundingFixture(t, p)
	ctx := context.Background()
	peer := hex.EncodeToString(balancedTestKey(88))
	svc := s.balancedOpen
	msg := balancedOpenProtocolMessage{Version: 1, Kind: "proposal", SessionID: "recipient-check", FromPubkey: peer, ToPubkey: peer, CapacitySat: 100000, ExecutionMode: balancedOpenExecutionModePush}
	dispatch := func(m balancedOpenProtocolMessage) {
		raw, _ := json.Marshal(m)
		svc.handleIncomingCustomMessage(lndclient.CustomPeerMessage{Type: balancedOpenCustomMsgType, PeerPubkey: peer, Data: raw})
	}
	dispatch(msg)
	if _, err := svc.GetSession(ctx, msg.SessionID); err == nil {
		t.Fatal("message for a different node accepted")
	}
	msg.ToPubkey = hex.EncodeToString(balancedTestKey(99))
	dispatch(msg)
	if _, err := svc.GetSession(ctx, msg.SessionID); err != nil {
		t.Fatal("correctly addressed proposal rejected", err)
	}
	msg.SessionID = "oversize-check"
	msg.Reason = strings.Repeat("x", 65536)
	dispatch(msg)
	if _, err := svc.GetSession(ctx, msg.SessionID); err == nil {
		t.Fatal("oversized message accepted")
	}
	if r.sends.Load() != 0 || r.signs.Load() != 0 {
		t.Fatal("incoming proposal moved funds without approval")
	}
}

func TestBalancedRemoteAcceptArtifactsBecomeImmutable(t *testing.T) {
	p := balancedSecurityPool(t)
	s, _, _ := balancedFundingFixture(t, p)
	svc := s.balancedOpen
	ctx := context.Background()
	peer := hex.EncodeToString(balancedTestKey(88))
	script, _ := balancedP2WPKHScriptHex(peer)
	session, err := svc.CreateSession(ctx, BalancedOpenCreateParams{PeerPubkey: peer, CapacitySat: 100000, FeeRateSatVb: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ProposeSession(ctx, session.SessionID); err != nil {
		t.Fatal(err)
	}
	msg := balancedOpenProtocolMessage{Version: 1, Kind: "accept", SessionID: session.SessionID, ExecutionMode: balancedOpenExecutionModeDual, MultisigPubkey: peer, TransitTxID: strings.Repeat("11", 32), TransitOutputSat: 50095, TransitOutputPk: script, TransitInputStack: []string{"010203"}}
	before, err := svc.markAcceptedFromPeer(ctx, peer, msg)
	if err != nil {
		t.Fatal(err)
	}
	changed := msg
	changed.TransitTxID = strings.Repeat("22", 32)
	if _, err = svc.markAcceptedFromPeer(ctx, peer, changed); err == nil {
		t.Fatal("accepted remote artifacts changed")
	}
	changed = msg
	changed.ExecutionMode = balancedOpenExecutionModePush
	if _, err = svc.markAcceptedFromPeer(ctx, peer, changed); err == nil {
		t.Fatal("dual acceptance downgraded to push")
	}
	after, err := svc.markAcceptedFromPeer(ctx, peer, msg)
	if err != nil {
		t.Fatal("identical accept retry failed", err)
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("accept retry changed approved metadata")
	}
}
