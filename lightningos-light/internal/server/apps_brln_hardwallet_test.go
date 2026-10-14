package server

import (
	"context"
	"lightningos-light/internal/system"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHardwalletLaunchUsesConfiguredHost(t *testing.T) {
	for _, tc := range []struct {
		name, ip, source string
		installed        bool
		want             string
	}{
		{"configured", "100.100.1.1", "club", true, "https://100.100.1.1:8443/#hardwallet-open"},
		{"local electrs", "100.100.1.2", "local", true, "https://100.100.1.2:8443/#hardwallet-open"},
		{"uninstalled", "100.100.1.1", "club", false, ""},
		{"missing", "", "club", true, ""},
		{"lan", "192.168.1.2", "club", true, ""},
		{"external", "example.com", "club", true, ""},
		{"injection", "100.100.1.1@evil.example", "club", true, ""},
		{"unknown source", "100.100.1.1", "unknown", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := hardwalletLaunchURL(system.HardwalletBrokerState{Installed: tc.installed, BindIP: tc.ip, Source: tc.source})
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

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
