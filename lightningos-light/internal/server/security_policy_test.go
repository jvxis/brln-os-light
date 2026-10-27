package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserSecurityPolicy(t *testing.T) {
	for _, path := range []string{"/", "/api/auth/state", "/terminal/"} {
		recorder := httptest.NewRecorder()
		handler := securityHeadersMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
		handler.ServeHTTP(recorder, httptest.NewRequest("GET", "https://los.local"+path, nil))
		policy := recorder.Header().Get("Content-Security-Policy")
		if !strings.Contains(policy, "script-src 'self'") || !strings.Contains(policy, "object-src 'none'") || !strings.Contains(policy, "base-uri 'none'") {
			t.Fatalf("missing restrictive browser policy for %s: %q", path, policy)
		}
		for _, directive := range strings.Split(policy, ";") {
			if strings.HasPrefix(strings.TrimSpace(directive), "script-src") && (strings.Contains(directive, "'unsafe-inline'") || strings.Contains(directive, "'unsafe-eval'") || strings.Contains(directive, "https:")) {
				t.Fatal("script policy allows untrusted execution")
			}
		}
		if strings.Contains(policy, "'wasm-unsafe-eval'") != strings.HasPrefix(path, "/terminal/") {
			t.Fatal("WebAssembly exception must remain terminal-only")
		}
		if recorder.Header().Get("Strict-Transport-Security") != "" {
			t.Fatal("default HSTS would interfere with private-CA bootstrap")
		}
		if recorder.Header().Get("X-Content-Type-Options") != "nosniff" || recorder.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
			t.Fatal("existing hardening lost")
		}
	}
}

func TestHSTSRequiresOptInDirectTLSAndDNS(t *testing.T) {
	for _, tc := range []struct {
		url  string
		age  int
		want bool
	}{
		{"https://node.local:8443", 0, false},
		{"https://node.local:8443", 300, true},
		{"https://node.tail-lab.ts.net:8443", 300, true},
		{"https://proxy.example.test", 300, true},
		{"http://proxy.example.test", 300, false},
		{"https://192.168.68.95:8443", 300, false},
		{"https://100.64.0.10:8443", 300, false},
		{"https://[::1]:8443", 300, false},
		{"https://localhost:8443", 300, false},
		{"https://node.local:8443", -1, false},
		{"https://node.local:8443", 31536001, false},
	} {
		r := httptest.NewRequest("GET", tc.url, nil)
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		securityHeadersMiddleware(tc.age)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
		value := w.Header().Get("Strict-Transport-Security")
		if (value != "") != tc.want {
			t.Fatalf("url=%s age=%d HSTS=%q", tc.url, tc.age, value)
		}
		if tc.want && value != "max-age=300" {
			t.Fatalf("unexpected HSTS policy %q", value)
		}
	}
}
