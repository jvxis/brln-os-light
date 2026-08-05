package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthRandomTokenFailsClosed(t *testing.T) {
	original := authRandomRead
	authRandomRead = func([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
	t.Cleanup(func() { authRandomRead = original })

	if token, err := authRandomToken("session", authRawTokenBytes); err == nil || token != "" {
		t.Fatalf("expected secure random failure, got token=%q err=%v", token, err)
	}
}

func TestAuthRateLimiterRejectsBurstByRemoteIP(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	auth := &AuthService{
		enabled:  true,
		now:      func() time.Time { return now },
		limiter:  newAuthRateLimiter(),
		sessions: make(map[string]*authSession),
	}

	for attempt := 0; attempt < 5; attempt++ {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"bad"}`))
		request.RemoteAddr = "192.168.1.25:4242"
		if !auth.allowAuthRequest(recorder, request, "login") {
			t.Fatalf("attempt %d should be allowed", attempt+1)
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"bad"}`))
	request.RemoteAddr = "192.168.1.25:4243"
	if auth.allowAuthRequest(recorder, request, "login") {
		t.Fatal("sixth attempt should be rate limited")
	}
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", recorder.Code)
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestReadAuthJSONRejectsOversizedBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	body := `{"password":"` + strings.Repeat("x", int(authRequestBodyMaxBytes)) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	var payload struct {
		Password string `json:"password"`
	}
	if readAuthJSON(recorder, request, &payload) {
		t.Fatal("oversized body should be rejected")
	}
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", recorder.Code)
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	handler := securityHeadersMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/auth/state", nil)
	handler.ServeHTTP(recorder, request)

	for name, expected := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
	} {
		if actual := recorder.Header().Get(name); actual != expected {
			t.Errorf("%s: expected %q, got %q", name, expected, actual)
		}
	}
}

func TestLightningFundsReauthRequiredOnlyWhenLoginEnabled(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	auth := &AuthService{
		enabled:  true,
		now:      func() time.Time { return now },
		sessions: make(map[string]*authSession),
	}
	auth.sessions["session-id"] = &authSession{
		ID:           "session-id",
		CSRFToken:    "csrf-token",
		ExpiresAt:    now.Add(time.Hour),
		ReauthScopes: make(map[string]time.Time),
	}
	server := &Server{auth: auth}
	snapshot := authSessionSnapshot{ID: "session-id", CSRFToken: "csrf-token", ExpiresAt: now.Add(time.Hour)}
	request := httptest.NewRequest(http.MethodPost, "/api/wallet/pay", nil)
	request = request.WithContext(context.WithValue(request.Context(), authSessionContextKey, snapshot))
	recorder := httptest.NewRecorder()

	if server.requireLightningFundsReauth(recorder, request, "") {
		t.Fatal("enabled login should require fresh reauthentication")
	}
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("expected 428, got %d", recorder.Code)
	}

	server.auth = &AuthService{enabled: false}
	if !server.requireLightningFundsReauth(httptest.NewRecorder(), request, "") {
		t.Fatal("login-disabled installations must preserve existing behavior")
	}
}

func TestLightningFundsScopeIsValid(t *testing.T) {
	if !authScopeValid(authScopeLightningFunds) {
		t.Fatal("lightning funds scope should be accepted")
	}
}
