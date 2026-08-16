package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTerminalStatusDoesNotExposeCredentials(t *testing.T) {
	originalPath := terminalRuntimeEnvPath
	terminalRuntimeEnvPath = filepath.Join(t.TempDir(), "terminal.env")
	t.Cleanup(func() { terminalRuntimeEnvPath = originalPath })
	if err := os.WriteFile(terminalRuntimeEnvPath, []byte("TERMINAL_ENABLED=1\nTERMINAL_CREDENTIAL=losop:AbCdEfGhIjKlMnOpQrStUvWxYz012345\nTERMINAL_ALLOW_WRITE=0\nTERMINAL_OPERATOR_USER=losop\n"), 0600); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/terminal/status", nil)
	(&Server{}).handleTerminalStatus(recorder, request)

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := body["credential"]; ok {
		t.Fatal("terminal status must not expose the GoTTY credential")
	}
	if _, ok := body["operator_password"]; ok {
		t.Fatal("terminal status must not expose the operator password")
	}
	if configured, ok := body["credential_configured"].(bool); !ok || !configured {
		t.Fatalf("expected credential_configured=true, got %#v", body["credential_configured"])
	}
	if got := body["operator_user"]; got != "losop" {
		t.Fatalf("operator_user = %#v, want losop", got)
	}
}

func TestTerminalControlRequiresExplicitStateAndFreshReauth(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	auth := &AuthService{enabled: true, now: func() time.Time { return now }, sessions: make(map[string]*authSession)}
	auth.sessions["session-id"] = &authSession{
		ID:           "session-id",
		CSRFToken:    "csrf-token",
		ExpiresAt:    now.Add(time.Hour),
		ReauthScopes: make(map[string]time.Time),
	}
	server := &Server{auth: auth}
	snapshot := authSessionSnapshot{ID: "session-id", CSRFToken: "csrf-token", ExpiresAt: now.Add(time.Hour)}

	request := httptest.NewRequest(http.MethodPost, "/api/terminal/control", bytes.NewBufferString(`{"enabled":true,"allow_write":true}`))
	request = request.WithContext(context.WithValue(request.Context(), authSessionContextKey, snapshot))
	recorder := httptest.NewRecorder()
	server.handleTerminalControl(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired || !strings.Contains(recorder.Body.String(), "terminal_control_reauth_required") {
		t.Fatalf("expected terminal control reauth challenge, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	auth.sessions["session-id"].ReauthScopes[authScopeTerminalControl] = now.Add(time.Minute)
	for _, body := range []string{`{}`, `{"enabled":true}`, `{"enabled":true,"allow_write":true,"command":"/bin/sh"}`, `{"enabled":false,"allow_write":true}`, `{"enabled":true,"allow_write":true}{"enabled":false}`} {
		request = httptest.NewRequest(http.MethodPost, "/api/terminal/control", bytes.NewBufferString(body))
		request = request.WithContext(context.WithValue(request.Context(), authSessionContextKey, snapshot))
		recorder = httptest.NewRecorder()
		server.handleTerminalControl(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("unsafe terminal control body accepted: body=%q status=%d response=%q", body, recorder.Code, recorder.Body.String())
		}
	}
}

func TestTerminalStatusKeepsLegacyCredentialAvailableButDisabledWithoutRuntimeEnvironment(t *testing.T) {
	originalPath := terminalRuntimeEnvPath
	terminalRuntimeEnvPath = filepath.Join(t.TempDir(), "missing.env")
	t.Cleanup(func() { terminalRuntimeEnvPath = originalPath })
	t.Setenv("TERMINAL_ENABLED", "1")
	t.Setenv("TERMINAL_CREDENTIAL", "losop:AbCdEfGhIjKlMnOpQrStUvWxYz012345")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/terminal/status", nil)
	(&Server{}).handleTerminalStatus(recorder, request)
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["enabled"] != false || body["credential_configured"] != true {
		t.Fatalf("legacy terminal status was not safely represented: %#v", body)
	}
}

func TestTerminalCredentialConfigured(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "losop:AbCdEfGhIjKlMnOpQrStUvWxYz012345", want: true},
		{value: " losop:AbCdEfGhIjKlMnOpQrStUvWxYz012345 ", want: true},
		{value: "losop:password", want: false},
		{value: "losop:", want: false},
		{value: ":password", want: false},
		{value: "password", want: false},
		{value: "", want: false},
	}
	for _, tc := range tests {
		if got := terminalCredentialConfigured(tc.value); got != tc.want {
			t.Errorf("terminalCredentialConfigured(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
