package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTapdUniverseDiscoveryAllowlist(t *testing.T) {
	for _, host := range []string{
		"universe.lightning.finance",
		"UNIVERSE.LIGHTNING.FINANCE",
		"  universe.lightning.finance  ",
	} {
		if !isApprovedTapdDiscoveryUniverse(host) {
			t.Errorf("approved universe rejected: %q", host)
		}
	}

	for _, host := range []string{
		"",
		"universe.lightning.finance:443",
		"universe.lightning.finance.evil.test",
		"evil-universe.lightning.finance",
		"https://universe.lightning.finance",
		"universe.lightning.finance/path",
		"universe.lightning.finance?host=127.0.0.1",
		"universe.lightning.finance#fragment",
		"user@universe.lightning.finance",
		"127.0.0.1",
		"169.254.169.254",
	} {
		if isApprovedTapdDiscoveryUniverse(host) {
			t.Errorf("unapproved universe accepted: %q", host)
		}
	}
}

func TestTapdUniverseDiscoveryRejectsUnapprovedHostBeforeRequest(t *testing.T) {
	server := &Server{}
	for _, host := range []string{
		"universe.lightning.finance.evil.test",
		"127.0.0.1",
		"169.254.169.254",
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/apps/tapd/discover?host="+host, nil)
		recorder := httptest.NewRecorder()
		server.handleTapdDiscover(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("host %q: expected 400, got %d: %s", host, recorder.Code, recorder.Body.String())
		}
	}
}

func TestTapdUniverseDiscoveryRejectsInternalDestinations(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "127.0.0.1:10029", "169.254.169.254", "10.0.0.1", "100.64.0.1"} {
		if _, err := publicUniverseHTTPClient(context.Background(), host); err == nil {
			t.Fatalf("internal universe destination accepted: %s", host)
		}
	}
}

func TestTapdUniversePublicIPPolicy(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "::1", "fc00::1", "2001:db8::1"} {
		if isPublicUniverseIP(net.ParseIP(ip)) {
			t.Fatalf("non-public universe IP accepted: %s", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !isPublicUniverseIP(net.ParseIP(ip)) {
			t.Fatalf("public universe IP rejected: %s", ip)
		}
	}
}

func TestTapdFundMutationsRequireFreshReauthentication(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	auth := &AuthService{enabled: true, now: func() time.Time { return now }, sessions: make(map[string]*authSession)}
	auth.sessions["session-id"] = &authSession{
		ID: "session-id", CSRFToken: "csrf-token", ExpiresAt: now.Add(time.Hour), ReauthScopes: make(map[string]time.Time),
	}
	server := &Server{auth: auth}
	snapshot := authSessionSnapshot{ID: "session-id", CSRFToken: "csrf-token", ExpiresAt: now.Add(time.Hour)}

	for _, test := range []struct {
		name    string
		path    string
		body    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{name: "send", path: "/api/apps/tapd/send", body: `{"addr":"tapbc1qqqqqqqqqqqqqqqq"}`, handler: server.handleTapdSend},
		{name: "mint finalize", path: "/api/apps/tapd/mint-finalize", body: `{}`, handler: server.handleTapdMintFinalize},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body))
			request = request.WithContext(context.WithValue(request.Context(), authSessionContextKey, snapshot))
			recorder := httptest.NewRecorder()
			test.handler(recorder, request)
			if recorder.Code != http.StatusPreconditionRequired {
				t.Fatalf("expected 428, got %d: %s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Code != "lightning_funds_reauth_required" {
				t.Fatalf("unexpected reauth response: %s (%v)", recorder.Body.String(), err)
			}
		})
	}
}
