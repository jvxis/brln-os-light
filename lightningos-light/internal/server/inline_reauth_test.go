package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInlineReauthCannotBypassDedicatedQuota(t *testing.T) {
	now := time.Now()
	auth := &AuthService{enabled: true, now: func() time.Time { return now }, limiter: newAuthRateLimiter(), sessions: map[string]*authSession{"test": {ID: "test", ExpiresAt: now.Add(time.Hour), ReauthScopes: map[string]time.Time{}}}}
	s := &Server{auth: auth, auditLogInitAt: now}
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/api/auth/reauth", strings.NewReader(`{"password":"bad","scope":"lightning_funds"}`))
		r.RemoteAddr = "192.0.2.10:1234"
		return r.WithContext(context.WithValue(r.Context(), authSessionContextKey, authSessionSnapshot{ID: "test"}))
	}
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		if !auth.allowAuthRequest(rec, request(), "reauth") {
			t.Fatalf("initial attempt %d blocked", i)
		}
	}
	for _, check := range []struct {
		name string
		run  func(http.ResponseWriter, *http.Request) bool
	}{
		{"funds", func(w http.ResponseWriter, r *http.Request) bool { return s.requireLightningFundsReauth(w, r, "bad") }},
		{"macaroons", func(w http.ResponseWriter, r *http.Request) bool { return s.requireMacaroonExportReauth(w, r, "bad") }},
		{"terminal", func(w http.ResponseWriter, r *http.Request) bool {
			return s.requireSensitiveReauth(w, r, authScopeTerminalControl, "bad", "reauth_required", "approval required")
		}},
		{"dedicated", func(w http.ResponseWriter, r *http.Request) bool { auth.HandleReauth(w, r); return false }},
		{"balanced propose", func(w http.ResponseWriter, r *http.Request) bool {
			r.Body = io.NopCloser(strings.NewReader(`{"confirm_password":"bad"}`))
			s.handleBalancedOpenSessionProposePost(w, r)
			return false
		}},
		{"balanced accept", func(w http.ResponseWriter, r *http.Request) bool {
			r.Body = io.NopCloser(strings.NewReader(`{"confirm_password":"bad"}`))
			s.handleBalancedOpenSessionAcceptPost(w, r)
			return false
		}},
		{"utxo bump", func(w http.ResponseWriter, r *http.Request) bool {
			r.Body = io.NopCloser(strings.NewReader(`{"outpoint":"test:0","sat_per_vbyte":1,"confirm_password":"bad"}`))
			s.handleUtxoBump(w, r)
			return false
		}},
	} {
		t.Run(check.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if check.run(rec, request()) || rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
				t.Fatalf("quota bypass: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestReauthSessionQuotaSurvivesChangingIPAndScopes(t *testing.T) {
	now := time.Now()
	auth := &AuthService{now: func() time.Time { return now }, limiter: newAuthRateLimiter()}
	for i := 0; i < 6; i++ {
		r := httptest.NewRequest("POST", "/api/auth/reauth", nil)
		r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1000", i+1)
		r.Header.Set("X-Forwarded-For", "198.51.100.1")
		rec := httptest.NewRecorder()
		allowed := auth.allowReauthRequest(rec, r, "same-session")
		if allowed != (i < 5) {
			t.Fatalf("attempt %d allowed=%v", i, allowed)
		}
		if i == 5 && (rec.Code != 429 || rec.Header().Get("Retry-After") == "") {
			t.Fatal("session limit lacks HTTP contract")
		}
	}
	now = now.Add(11 * time.Second)
	r := httptest.NewRequest("POST", "/api/auth/reauth", nil)
	r.RemoteAddr = "192.0.2.99:1000"
	if !auth.allowReauthRequest(httptest.NewRecorder(), r, "same-session") {
		t.Fatal("quota did not refill")
	}
}

func TestInlineReauthPreservesChallengesAndGrants(t *testing.T) {
	now := time.Now()
	auth := &AuthService{enabled: true, now: func() time.Time { return now }, limiter: newAuthRateLimiter(), sessions: map[string]*authSession{"test": {ID: "test", ExpiresAt: now.Add(time.Hour), ReauthScopes: map[string]time.Time{authScopeLightningFunds: now.Add(time.Minute)}}}}
	s := &Server{auth: auth}
	r := httptest.NewRequest("POST", "/api/wallet/pay", nil)
	r = r.WithContext(context.WithValue(r.Context(), authSessionContextKey, authSessionSnapshot{ID: "test"}))
	if !s.requireLightningFundsReauth(httptest.NewRecorder(), r, "") {
		t.Fatal("valid grant rejected")
	}
	now = now.Add(2 * time.Minute)
	rec := httptest.NewRecorder()
	if s.requireLightningFundsReauth(rec, r, "") || rec.Code != 428 {
		t.Fatal("expired grant did not challenge")
	}
	if len(auth.limiter.buckets) != 0 {
		t.Fatal("a challenge consumed a password attempt")
	}
	if _, err := auth.reauth(httptest.NewRecorder(), r, "test", strings.Repeat("x", authPasswordMaxRunes+1), authScopeLightningFunds); err != errInvalidCredentials {
		t.Fatalf("oversized password reached secret loading or verification: %v", err)
	}
}
