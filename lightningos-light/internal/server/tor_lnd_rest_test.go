package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/privileged"
)

const testLNDRESTOnion = "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"

func withTorLNDRESTBroker(t *testing.T, fake func(context.Context, bool) (privileged.TorLNDRESTState, bool, error)) {
	t.Helper()
	previous := torLNDRESTBroker
	torLNDRESTBroker = fake
	t.Cleanup(func() { torLNDRESTBroker = previous })
}

func TestTorLNDRESTEnsureRequiresConfirmationAndRejectsInput(t *testing.T) {
	withTorLNDRESTBroker(t, func(context.Context, bool) (privileged.TorLNDRESTState, bool, error) {
		t.Fatal("broker must not be called")
		return privileged.TorLNDRESTState{}, false, nil
	})
	for _, body := range []string{`{}`, `{"confirm":false}`, `{"confirm":true,"target":"127.0.0.1:22"}`, `{"confirm":true} {}`, `null`} {
		r := httptest.NewRecorder()
		(&Server{}).handleTorLNDRESTEnsure(r, httptest.NewRequest(http.MethodPost, "/api/tor/lnd-rest/ensure", strings.NewReader(body)))
		if r.Code != http.StatusBadRequest {
			t.Fatalf("body=%s code=%d", body, r.Code)
		}
	}
}

func TestTorLNDRESTEnsureRequiresFreshReauth(t *testing.T) {
	withTorLNDRESTBroker(t, func(context.Context, bool) (privileged.TorLNDRESTState, bool, error) {
		t.Fatal("broker must not be called before reauth")
		return privileged.TorLNDRESTState{}, false, nil
	})
	now := time.Now()
	auth := &AuthService{enabled: true, now: func() time.Time { return now }, sessions: map[string]*authSession{"test": {ID: "test", ExpiresAt: now.Add(time.Hour), ReauthScopes: make(map[string]time.Time)}}}
	req := httptest.NewRequest(http.MethodPost, "/api/tor/lnd-rest/ensure", strings.NewReader(`{"confirm":true}`))
	req = req.WithContext(context.WithValue(req.Context(), authSessionContextKey, authSessionSnapshot{ID: "test", ExpiresAt: now.Add(time.Hour)}))
	r := httptest.NewRecorder()
	(&Server{auth: auth}).handleTorLNDRESTEnsure(r, req)
	if r.Code != http.StatusPreconditionRequired {
		t.Fatalf("code=%d", r.Code)
	}
}

func TestTorLNDRESTEnsureReturnsTheReadyURL(t *testing.T) {
	var asked []bool
	withTorLNDRESTBroker(t, func(_ context.Context, ensure bool) (privileged.TorLNDRESTState, bool, error) {
		asked = append(asked, ensure)
		return privileged.TorLNDRESTState{Status: "configured", Hostname: testLNDRESTOnion, Managed: true, RESTReachable: true}, true, nil
	})
	s := &Server{}
	recorder := httptest.NewRecorder()
	s.handleTorLNDRESTEnsure(recorder, httptest.NewRequest(http.MethodPost, "/api/tor/lnd-rest/ensure", strings.NewReader(`{"confirm":true}`)))
	var body torLNDRESTResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || recorder.Code != http.StatusOK {
		t.Fatalf("unexpected response %d %s", recorder.Code, recorder.Body.String())
	}
	if body.URL != "https://"+testLNDRESTOnion+":8080" || !body.Managed || len(asked) != 1 || !asked[0] {
		t.Fatalf("unexpected body %#v asked=%v", body, asked)
	}
}

func TestTorLNDRESTStatusNeverEnsures(t *testing.T) {
	withTorLNDRESTBroker(t, func(_ context.Context, ensure bool) (privileged.TorLNDRESTState, bool, error) {
		if ensure {
			t.Fatal("status asked the broker to change torrc")
		}
		return privileged.TorLNDRESTState{Status: "absent", RESTReachable: true}, true, nil
	})
	recorder := httptest.NewRecorder()
	(&Server{}).handleTorLNDRESTStatus(recorder, httptest.NewRequest(http.MethodGet, "/api/tor/lnd-rest", nil))
	var body torLNDRESTResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Status != "absent" || body.URL != "" {
		t.Fatalf("unexpected status response %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestTorLNDRESTWithoutBrokerOrWithErrorFailsClosed(t *testing.T) {
	withTorLNDRESTBroker(t, func(context.Context, bool) (privileged.TorLNDRESTState, bool, error) {
		return privileged.TorLNDRESTState{}, false, nil
	})
	recorder := httptest.NewRecorder()
	(&Server{}).handleTorLNDRESTEnsure(recorder, httptest.NewRequest(http.MethodPost, "/api/tor/lnd-rest/ensure", strings.NewReader(`{"confirm":true}`)))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("without broker: %d", recorder.Code)
	}
	withTorLNDRESTBroker(t, func(context.Context, bool) (privileged.TorLNDRESTState, bool, error) {
		return privileged.TorLNDRESTState{}, true, errors.New("LND REST is not listening on 127.0.0.1:8080")
	})
	recorder = httptest.NewRecorder()
	(&Server{}).handleTorLNDRESTEnsure(recorder, httptest.NewRequest(http.MethodPost, "/api/tor/lnd-rest/ensure", strings.NewReader(`{"confirm":true}`)))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("broker error: %d %s", recorder.Code, recorder.Body.String())
	}
}
