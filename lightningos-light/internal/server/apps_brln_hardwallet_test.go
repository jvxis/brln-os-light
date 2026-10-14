package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHardwalletAuthorizationRequiresEnabledSession(t *testing.T) {
	for _, tc := range []struct {
		enabled, session bool
		want             int
	}{{false, false, 401}, {false, true, 401}, {true, false, 401}, {true, true, 204}} {
		s := &Server{auth: &AuthService{enabled: tc.enabled}}
		r := httptest.NewRequest("GET", "https://node/api/apps/brln-hardwallet/authorization", nil)
		if tc.session {
			r = r.WithContext(context.WithValue(r.Context(), authSessionContextKey, authSessionSnapshot{ID: "test", ExpiresAt: time.Now().Add(time.Hour)}))
		}
		w := httptest.NewRecorder()
		s.handleHardwalletAuthorization(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v: %d", tc, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("authorization response cached")
		}
	}
}
func TestHardwalletAuthorizationRejectsExpiredCookieThroughMiddleware(t *testing.T) {
	a := &AuthService{enabled: true, sessions: map[string]*authSession{"expired": {ID: "expired", ExpiresAt: time.Now().Add(-time.Minute)}}}
	s := &Server{auth: a}
	r := httptest.NewRequest("GET", "https://node/api/apps/brln-hardwallet/authorization", nil)
	r.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: "expired"})
	w := httptest.NewRecorder()
	a.Middleware()(http.HandlerFunc(s.handleHardwalletAuthorization)).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
