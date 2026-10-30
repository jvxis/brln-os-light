package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"lightningos-light/internal/config"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func balancedSecurityPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LOS_BALANCED_OPEN_TEST_DSN")
	if dsn == "" {
		t.Skip("requires dedicated los_balanced_test_* PostgreSQL database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "los_balanced_test_") {
		t.Fatal("dedicated database required")
	}
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	svc := NewBalancedOpenService(p, nil, log.New(io.Discard, "", 0), nil)
	if err = svc.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(context.Background(), `truncate balanced_open_sessions cascade`); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestBalancedFinancialActionsRequireApprovalBeforeService(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	cfg := &config.Config{}
	auth := NewAuthService(cfg, logger)
	session, err := auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: cfg, auth: auth, logger: logger, balancedOpenInitAt: time.Now(), balancedOpenErr: "fixture unavailable", auditLogInitAt: time.Now()}
	call := func(action string, intentIDs ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://node.example/api/lnops/balanced-open/sessions/test-session/"+action, strings.NewReader(`{}`))
		r.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.ID})
		r.Header.Set("Origin", "https://node.example")
		r.Header.Set("X-CSRF-Token", session.CSRFToken)
		if len(intentIDs) > 0 {
			r.Header.Set(financialIntentHeader, intentIDs[0])
		}
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		return w
	}
	for _, action := range []string{"propose", "accept"} {
		w := call(action)
		if w.Code != 428 || !strings.Contains(w.Body.String(), "lightning_funds_reauth_required") {
			t.Errorf("%s reached service before approval: HTTP %d", action, w.Code)
		}
	}
	auth.mu.Lock()
	auth.sessions[session.ID].ReauthScopes[authScopeLightningFunds] = time.Now().Add(time.Minute)
	auth.mu.Unlock()
	for _, action := range []string{"propose", "accept"} {
		if w := call(action); w.Code != 428 {
			t.Errorf("%s broad grant authorized an unbound workflow: HTTP %d", action, w.Code)
		}
		path := "/api/lnops/balanced-open/sessions/test-session/" + action
		intent := financialHandlerApproval(t, s, session, path, []byte(`{}`), financialIntentBinding{Kind: "balanced_open"})
		if w := call(action, intent.ID); w.Code != 503 {
			t.Errorf("%s approved handler did not preserve unavailable-service failure: HTTP %d", action, w.Code)
		}
	}
}
func TestBalancedPeerCannotRewriteProposalOrRegressState(t *testing.T) {
	p := balancedSecurityPool(t)
	ctx := context.Background()
	svc := NewBalancedOpenService(p, nil, log.New(io.Discard, "", 0), nil)
	a := "02" + strings.Repeat("11", 32)
	b := "03" + strings.Repeat("22", 32)
	for _, state := range []string{balancedOpenStateProposalReceived, balancedOpenStateAccepted, balancedOpenStateFundingTxHalfSigned, balancedOpenStateActive, balancedOpenStateCanceled, balancedOpenStateRecoveryRequired} {
		msg := balancedOpenProtocolMessage{Version: 1, Kind: "proposal", SessionID: "security-" + state, CapacitySat: 100000, FeeRateSatVb: 1, ExecutionMode: balancedOpenExecutionModePush}
		if _, err := svc.upsertSessionFromProposal(ctx, a, msg); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Exec(ctx, `update balanced_open_sessions set state=$1,metadata=metadata || '{"local_marker":"preserve"}'::jsonb where session_id=$2`, state, msg.SessionID); err != nil {
			t.Fatal(err)
		}
		before, _ := svc.GetSession(ctx, msg.SessionID)
		if _, err := svc.upsertSessionFromProposal(ctx, b, msg); err == nil {
			t.Errorf("third-party collision accepted in %s", state)
		}
		changed := msg
		changed.CapacitySat = 200000
		changed.FeeRateSatVb = 999
		changed.CloseAddress = "changed-destination"
		if _, err := svc.upsertSessionFromProposal(ctx, a, changed); err == nil {
			t.Errorf("same peer rewrote terms in %s", state)
		}
		if _, err := svc.upsertSessionFromProposal(ctx, a, msg); err != nil {
			t.Errorf("identical retry rejected in %s: %v", state, err)
		}
		after, err := svc.GetSession(ctx, msg.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		x, _ := json.Marshal(before)
		y, _ := json.Marshal(after)
		if string(x) != string(y) {
			t.Errorf("proposal changed existing session in %s", state)
		}
	}
}
func TestBalancedConcurrentProposalOwnership(t *testing.T) {
	p := balancedSecurityPool(t)
	svc := NewBalancedOpenService(p, nil, log.New(io.Discard, "", 0), nil)
	ctx := context.Background()
	msg := balancedOpenProtocolMessage{Version: 1, Kind: "proposal", SessionID: "concurrent-owner", CapacitySat: 100000, ExecutionMode: balancedOpenExecutionModePush}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, peer := range []string{"02" + strings.Repeat("11", 32), "03" + strings.Repeat("22", 32)} {
		wg.Add(1)
		go func(peer string) {
			defer wg.Done()
			_, err := svc.upsertSessionFromProposal(ctx, peer, msg)
			results <- err
		}(peer)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("owner collision succeeded %d times", success)
	}
}
func TestBalancedAcceptRequiresInitiatorAndProposalSent(t *testing.T) {
	p := balancedSecurityPool(t)
	ctx := context.Background()
	svc := NewBalancedOpenService(p, nil, log.New(io.Discard, "", 0), nil)
	peer := "02" + strings.Repeat("11", 32)
	for _, role := range []string{balancedOpenRoleInitiator, balancedOpenRoleAccepter} {
		session, err := svc.CreateSession(ctx, BalancedOpenCreateParams{PeerPubkey: peer, CapacitySat: 100000, Role: role, Metadata: json.RawMessage(`{"execution_mode":"push_sat_v1"}`)})
		if err != nil {
			t.Fatal(err)
		}
		msg := balancedOpenProtocolMessage{Version: 1, Kind: "accept", SessionID: session.SessionID, ExecutionMode: balancedOpenExecutionModePush}
		if _, err = svc.markAcceptedFromPeer(ctx, peer, msg); err == nil {
			t.Errorf("unsolicited accept advanced %s draft", role)
		}
		if role == balancedOpenRoleInitiator {
			if _, err = p.Exec(ctx, `update balanced_open_sessions set state='proposal_sent' where session_id=$1`, session.SessionID); err != nil {
				t.Fatal(err)
			}
			accepted, err := svc.markAcceptedFromPeer(ctx, peer, msg)
			if err != nil || accepted.State != balancedOpenStateAccepted {
				t.Fatal("valid acceptance broken", err)
			}
			after, err := svc.markAcceptedFromPeer(ctx, peer, msg)
			if err != nil || !after.UpdatedAt.Equal(accepted.UpdatedAt) {
				t.Fatal("accept retry is not idempotent", err)
			}
		}
	}
}

func TestBalancedProposalBoundsQuotaAndExpiry(t *testing.T) {
	p := balancedSecurityPool(t)
	ctx := context.Background()
	svc := NewBalancedOpenService(p, nil, log.New(io.Discard, "", 0), nil)
	peer := "02" + strings.Repeat("11", 32)
	msg := balancedOpenProtocolMessage{Version: 1, Kind: "proposal", SessionID: "quota", CapacitySat: 100000, ExecutionMode: balancedOpenExecutionModePush}
	for _, bad := range []string{"", strings.Repeat("a", 129), "line\nbreak", "path/id"} {
		copy := msg
		copy.SessionID = bad
		if _, err := svc.upsertSessionFromProposal(ctx, peer, copy); err == nil {
			t.Fatal("invalid ID accepted")
		}
	}
	wrong := msg
	wrong.FromPubkey = "03" + strings.Repeat("22", 32)
	if _, err := svc.upsertSessionFromProposal(ctx, peer, wrong); err == nil {
		t.Fatal("forged sender accepted")
	}
	wrong = msg
	wrong.ExecutionMode = "unknown"
	if _, err := svc.upsertSessionFromProposal(ctx, peer, wrong); err == nil {
		t.Fatal("unknown execution mode silently downgraded")
	}
	for i := 0; i < 16; i++ {
		msg.SessionID = fmt.Sprintf("quota-%d", i)
		if _, err := svc.upsertSessionFromProposal(ctx, peer, msg); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.upsertSessionFromProposal(ctx, peer, msg); err != nil {
		t.Fatal("quota blocked idempotent retry", err)
	}
	msg.SessionID = "quota-overflow"
	if _, err := svc.upsertSessionFromProposal(ctx, peer, msg); !errors.Is(err, ErrBalancedOpenProposalQuota) {
		t.Fatal("per-peer quota failed", err)
	}
	if _, err := p.Exec(ctx, `update balanced_open_sessions set created_at=now()-interval '8 days',state_updated_at=now()-interval '8 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcceptSession(ctx, "quota-0"); !errors.Is(err, ErrBalancedOpenProposalExpired) {
		t.Fatal("expired proposal accepted", err)
	}
	if _, err := svc.CancelSession(ctx, "quota-0", "expired"); err != nil {
		t.Fatal("expired proposal cannot be canceled", err)
	}
	if _, err := svc.upsertSessionFromProposal(ctx, peer, msg); err != nil {
		t.Fatal("expired proposals kept quota forever", err)
	}
	if _, err := p.Exec(ctx, `truncate balanced_open_sessions cascade; insert into balanced_open_sessions(session_id,role,peer_pubkey,capacity_sat,state,metadata) select 'global-'||n,'accepter','other-peer',100000,'proposal_received','{"execution_mode":"push_sat_v1"}'::jsonb from generate_series(1,256)n`); err != nil {
		t.Fatal(err)
	}
	msg.SessionID = "global-overflow"
	if _, err := svc.upsertSessionFromProposal(ctx, peer, msg); !errors.Is(err, ErrBalancedOpenProposalQuota) {
		t.Fatal("global quota failed", err)
	}
	if _, err := balancedTransitContribution(100000, int64(1<<63-1)); !errors.Is(err, ErrBalancedOpenInvalidFeeRate) {
		t.Fatal("fee arithmetic overflow accepted")
	}
}

func TestBalancedPreallocatedAccepterReceivesMatchingProposal(t *testing.T) {
	p := balancedSecurityPool(t)
	ctx := context.Background()
	svc := NewBalancedOpenService(p, nil, log.New(io.Discard, "", 0), nil)
	peer := "02" + strings.Repeat("11", 32)
	draft, err := svc.CreateSession(ctx, BalancedOpenCreateParams{Role: balancedOpenRoleAccepter, PeerPubkey: peer, PeerHost: "operator-approved-host", CapacitySat: 100000, FeeRateSatVb: 1, Metadata: json.RawMessage(`{"execution_mode":"push_sat_v1","local_marker":"keep"}`)})
	if err != nil {
		t.Fatal(err)
	}
	msg := balancedOpenProtocolMessage{Version: 1, Kind: "proposal", SessionID: draft.SessionID, CapacitySat: 100000, FeeRateSatVb: 1, ExecutionMode: balancedOpenExecutionModePush, PeerHost: "remote-hint"}
	received, err := svc.upsertSessionFromProposal(ctx, peer, msg)
	if err != nil || received.State != balancedOpenStateProposalReceived || received.ID != draft.ID || received.PeerHost != draft.PeerHost || !received.CreatedAt.Equal(draft.CreatedAt) || !strings.Contains(string(received.Metadata), "keep") {
		t.Fatal("preallocated accepter workflow changed", err)
	}
	retry, err := svc.upsertSessionFromProposal(ctx, peer, msg)
	if err != nil || !retry.UpdatedAt.Equal(received.UpdatedAt) {
		t.Fatal("preallocated retry is not idempotent", err)
	}
}
