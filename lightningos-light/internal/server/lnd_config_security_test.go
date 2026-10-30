package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLNDConfigWritesRequireOwnReauthScope(t *testing.T) {
	// Both payloads are deliberately invalid, so the pre-fix baseline cannot
	// mutate a real config file while demonstrating the missing auth barrier.
	for _, tc := range []struct {
		name, body string
		handler    func(*Server, http.ResponseWriter, *http.Request)
	}{
		{"structured", `{"alias":"` + strings.Repeat("x", 33) + `"}`, (*Server).handleLNDConfigPost},
		{"raw", `{"raw_user_conf":"[tor]\ntor.skip-proxy-for-clearnet-targets=true\ntor.streamisolation=true\n"}`, (*Server).handleLNDConfigRaw},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &AuthService{enabled: true, now: time.Now, sessions: make(map[string]*authSession)}
			snapshot, err := auth.newSession()
			if err != nil {
				t.Fatal(err)
			}
			server := &Server{auth: auth, logger: log.New(io.Discard, "", 0)}
			request := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/api/lnd/config", strings.NewReader(tc.body))
				r = r.WithContext(context.WithValue(r.Context(), authSessionContextKey, snapshot))
				w := httptest.NewRecorder()
				tc.handler(server, w, r)
				return w
			}
			if w := request(); w.Code != 428 {
				t.Fatalf("missing approval must be rejected before config handling, got %d", w.Code)
			}
			auth.sessions[snapshot.ID].ReauthScopes[authScopeLNDMaintenance] = time.Now().Add(time.Minute)
			if w := request(); w.Code != 428 {
				t.Fatalf("maintenance approval authorized config mutation: %d", w.Code)
			}
		})
	}
}

func TestLNDConfigGetDoesNotReturnCredentials(t *testing.T) {
	raw := []byte("[Application Options]\nalias=Config Fixture\n[Bitcoind]\nbitcoind.rpcuser=fixture-user-private\nbitcoind.rpcpass=fixture-password-private\n")
	server := &Server{lndConfigOps: &lndConfigOperations{read: func() ([]byte, error) { return raw, nil }}}
	w := httptest.NewRecorder()
	server.handleLNDConfigGet(w, httptest.NewRequest("GET", "/api/lnd/config", nil))
	var response struct {
		Current struct {
			Alias string `json:"alias"`
		} `json:"current"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Current.Alias != "Config Fixture" {
		t.Fatal("config fixture was not read")
	}
	for _, secret := range []string{"fixture-user-private", "fixture-password-private"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("config response contains an RPC credential")
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("config response may be cached")
	}
}

func TestCoreLogsAndErrorsRedactCredentials(t *testing.T) {
	message := "database connection failed postgres://fixture-user:fixture-password@db.local/lnd"
	for _, value := range []string{sanitizeLogLine(message), func() string { w := httptest.NewRecorder(); writeError(w, 500, message); return w.Body.String() }()} {
		if strings.Contains(value, "fixture-password") || strings.Contains(value, "fixture-user") || !strings.Contains(value, "database connection failed") {
			t.Fatal("core log/error leaked credentials or lost its diagnostic category")
		}
	}
}

func TestLNDConfigSecurityPreservesExistingOptions(t *testing.T) {
	base := testLNDConfig + "\n[Application Options]\nrpclisten=127.0.0.1:10009\nrestlisten=localhost:8080\nwallet-unlock-password-file=/srv/custom/password\nwallet-unlock-allow-create=true\ndatadir=/srv/custom/data\nmaxpendingchannels=4\n"
	for _, tc := range []struct {
		name, updated string
		allowed       bool
	}{
		{"unchanged", base, true},
		{"alias", strings.Replace(base, "alias=Test Node", "alias=New Node", 1), true},
		{"public p2p", base + "listen=0.0.0.0:9735\nexternalip=node.example:9735\n", true},
		{"disable auth", base + "no-macaroons=true\n", false},
		{"public rpc", base + "rpclisten=0.0.0.0:10009\n", false},
		{"dns rpc", base + "rpclisten=node.example:10009\n", false},
		{"public rest", base + "restlisten=0.0.0.0:8080\n", false},
		{"disable rest TLS", base + "no-rest-tls=true\n", false},
		{"change storage", strings.Replace(base, "datadir=/srv/custom/data", "datadir=/tmp/new", 1), false},
		{"remove unlock", strings.Replace(base, "wallet-unlock-password-file=/srv/custom/password\n", "", 1), false},
		{"change unlock", strings.Replace(base, "/srv/custom/password", "/tmp/other-password", 1), false},
		{"default passphrase", base + "noseedbackup=true\n", false},
		{"unsafe replay", base + "unsafe-replay=true\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLNDConfigSecurity(base, tc.updated)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v error=%v", tc.allowed, err)
			}
		})
	}
	for _, mode := range []string{"private", "hybrid"} {
		updated, err := buildLNDConfigUpdate(base, true, "Changed", "#123456", 10000, 1000000, &mode, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateLNDConfigSecurity(base, updated); err != nil {
			t.Fatalf("legitimate %s rejected: %v", mode, err)
		}
		for _, option := range []string{"wallet-unlock-password-file=/srv/custom/password", "datadir=/srv/custom/data", "maxpendingchannels=4"} {
			if !strings.Contains(updated, option) {
				t.Fatalf("existing option lost: %s", option)
			}
		}
	}
	legacy := strings.Replace(base, "restlisten=localhost:8080", "restlisten=100.64.0.1:8080", 1)
	if err := validateLNDConfigSecurity(legacy, strings.Replace(legacy, "alias=Test Node", "alias=Legacy Node", 1)); err != nil {
		t.Fatal("existing authenticated TLS REST listener must be preserved")
	}
	if err := validateLNDConfigSecurity(legacy+"no-rest-tls=true\n", legacy+"no-rest-tls=true\n"); err == nil {
		t.Fatal("plaintext non-local REST accepted")
	}
}
