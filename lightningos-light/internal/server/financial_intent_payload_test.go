package server

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFinancialIntentCanonicalPayloadPreservesNumbersAndRejectsAmbiguity(t *testing.T) {
	first, err := canonicalFinancialPayload([]byte(` { "amount_sat":2100000000000000,"address":"destination","confirm_password":"fictional-private-password" } `))
	if err != nil || bytes.Contains(first, []byte("fictional-private-password")) {
		t.Fatal("password reached canonical financial payload")
	}
	second, err := canonicalFinancialPayload([]byte(`{"address":"destination","amount_sat":2100000000000000}`))
	if err != nil || !bytes.Equal(first, second) || !bytes.Contains(first, []byte("2100000000000000")) {
		t.Fatal("canonical payload changed exact amount or key-order meaning")
	}
	for _, invalid := range []string{
		`{"address":"first","address":"second"}`,
		`{"address":"first","Address":"second"}`,
		`{"channels":[{"local_funding_sat":1,"LOCAL_FUNDING_SAT":9}]}`,
		`{} {}`, `null`, `[]`,
		`{"private_key":"fictional-key-material"}`,
		strings.Repeat(`{"nested":`, 34) + `0` + strings.Repeat(`}`, 34),
	} {
		if _, err := canonicalFinancialPayload([]byte(invalid)); err == nil {
			t.Fatal("ambiguous or unsupported financial payload accepted")
		}
	}
}

func TestFinancialIntentApprovalConsumesOnceAndRevokesWithSession(t *testing.T) {
	auth := NewAuthService(nil, nil)
	session, err := auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := newFinancialIntentID()
	scope := financialIntentScopePrefix + id
	auth.sessions[session.ID].ReauthScopes[scope] = time.Now().Add(time.Minute)
	var consumed atomic.Int64
	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			if auth.consumeFinancialApproval(session.ID, scope) {
				consumed.Add(1)
			}
		}()
	}
	group.Wait()
	if consumed.Load() != 1 {
		t.Fatal("financial approval was reused")
	}
	auth.sessions[session.ID].ReauthScopes[scope] = time.Now().Add(time.Minute)
	auth.logout(session.ID)
	if auth.consumeFinancialApproval(session.ID, scope) {
		t.Fatal("logout retained financial approval")
	}
	other, err := auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	auth.sessions[other.ID].ReauthScopes[scope] = time.Now().Add(time.Minute)
	auth.resetSessionsAndCreateNew()
	if auth.consumeFinancialApproval(other.ID, scope) {
		t.Fatal("password/session reset retained financial approval")
	}
}

func TestFinancialIntentScopeRequiresOwnedLiveDraft(t *testing.T) {
	s, r := financialStoreFixture(t)
	auth := NewAuthService(nil, nil)
	auth.setFinancialScopeValidator(func(session, scope string) bool {
		return s.approvalAllowed(strings.TrimPrefix(scope, financialIntentScopePrefix), session)
	})
	for _, id := range []string{"", "../unsafe", strings.Repeat("A", 32), strings.Repeat("f", 31)} {
		if authScopeValid(financialIntentScopePrefix + id) {
			t.Fatal("invalid financial scope accepted")
		}
	}
	request := httptest.NewRequest("POST", "/api/auth/reauth", nil)
	request = request.WithContext(context.Background())
	// Ownership is rejected before reading real password configuration.
	if _, err := auth.reauth(httptest.NewRecorder(), request, "another-session", "not-a-password", financialIntentScopePrefix+r.ID); err != errInvalidScope {
		t.Fatal("unowned intent reached password verification")
	}
	if !auth.financialScopeAllowed("fictional-private-cookie", financialIntentScopePrefix+r.ID) {
		t.Fatal("owned live draft was rejected")
	}
	s.now = func() time.Time { return r.ExpiresAt }
	if auth.financialScopeAllowed("fictional-private-cookie", financialIntentScopePrefix+r.ID) {
		t.Fatal("expired financial scope accepted")
	}
}
