package server

import (
	"context"
	"errors"
	"fmt"
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

func TestAuthRateLimiterCleansStaleBuckets(t *testing.T) {
	limiter := newAuthRateLimiter()
	start := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	if allowed, _ := limiter.allow("client:login:old", start, 5, 0.1); !allowed {
		t.Fatal("initial request should be allowed")
	}
	if allowed, _ := limiter.allow("client:login:new", start.Add(authRateBucketRetention+authRateCleanupInterval+time.Second), 5, 0.1); !allowed {
		t.Fatal("new request should be allowed")
	}
	limiter.mu.Lock()
	_, stalePresent := limiter.buckets["client:login:old"]
	limiter.mu.Unlock()
	if stalePresent {
		t.Fatal("stale rate-limit bucket should have been removed")
	}
}

func TestAuthRateLimiterCapsBucketCount(t *testing.T) {
	limiter := newAuthRateLimiter()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	for index := 0; index < authRateMaxBuckets+100; index++ {
		key := fmt.Sprintf("client:login:192.0.2.%d", index)
		_, _ = limiter.allow(key, now.Add(time.Duration(index)*time.Millisecond), 5, 0.1)
	}
	limiter.mu.Lock()
	bucketCount := len(limiter.buckets)
	limiter.mu.Unlock()
	if bucketCount > authRateMaxBuckets {
		t.Fatalf("rate-limit bucket count = %d, want <= %d", bucketCount, authRateMaxBuckets)
	}
}

func TestAuthRateLimitAuditIsThrottled(t *testing.T) {
	limiter := newAuthRateLimiter()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	key := "client:login:192.168.1.25"
	_, _ = limiter.allow(key, now, 1, 0.1)
	if !limiter.shouldAudit(key, now) {
		t.Fatal("first rate-limit audit should be emitted")
	}
	if limiter.shouldAudit(key, now.Add(30*time.Second)) {
		t.Fatal("repeated rate-limit audit inside the interval should be suppressed")
	}
	if !limiter.shouldAudit(key, now.Add(authRateLimitAuditInterval)) {
		t.Fatal("rate-limit audit should resume after the interval")
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

func TestBarkWalletRevealAuthorizationRequiresRecentReauth(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
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
	request := httptest.NewRequest(http.MethodGet, "/api/apps/bark-wallet/reveal-authorization", nil)
	request = request.WithContext(context.WithValue(request.Context(), authSessionContextKey, snapshot))

	recorder := httptest.NewRecorder()
	server.handleBarkWalletRevealAuthorization(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired || !strings.Contains(recorder.Body.String(), "bark_seed_reauth_required") {
		t.Fatalf("expected fresh reauth challenge, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("reveal authorization challenge must not be cached")
	}

	auth.sessions["session-id"].ReauthScopes[authScopeBarkSeedReveal] = now.Add(-4 * time.Minute)
	recorder = httptest.NewRecorder()
	server.handleBarkWalletRevealAuthorization(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("expired Bark reauth was accepted: status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	auth.sessions["session-id"].ReauthScopes[authScopeBarkSeedReveal] = now.Add(time.Minute)
	recorder = httptest.NewRecorder()
	server.handleBarkWalletRevealAuthorization(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("recent reauth was rejected: status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	server.auth = &AuthService{enabled: false}
	recorder = httptest.NewRecorder()
	server.handleBarkWalletRevealAuthorization(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("login-disabled install should retain Bark app authentication only: status=%d", recorder.Code)
	}
}

func TestAuthAuditReasonDoesNotExposeErrorText(t *testing.T) {
	if got := authAuditReason(errors.New("password=should-never-appear")); got != "request_failed" {
		t.Fatalf("unexpected audit reason: %q", got)
	}
}
