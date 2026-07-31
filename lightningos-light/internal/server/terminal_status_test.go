package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestTerminalStatusDoesNotExposeCredentials(t *testing.T) {
	t.Setenv("TERMINAL_ENABLED", "1")
	t.Setenv("TERMINAL_CREDENTIAL", "losop:secret-value")
	t.Setenv("TERMINAL_OPERATOR_USER", "losop")
	t.Setenv("TERMINAL_OPERATOR_PASSWORD", "secret-value")

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

func TestTerminalCredentialConfigured(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "losop:password", want: true},
		{value: " losop:password ", want: true},
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
