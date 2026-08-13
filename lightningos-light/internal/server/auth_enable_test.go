package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"lightningos-light/internal/config"
	"lightningos-light/internal/system"
)

type authEnablePrivilegedClient struct {
	*cpuMinerPrivilegedClient
	enableCalls  int
	enableDryRun bool
}

func (client *authEnablePrivilegedClient) EnableLogin(_ context.Context, dryRun bool) error {
	client.enableCalls++
	client.enableDryRun = dryRun
	return nil
}

func TestAuthEnableFailsClosedOutsideBrokerEnforce(t *testing.T) {
	for _, test := range []struct {
		mode       string
		wantCalls  int
		wantDryRun bool
	}{
		{mode: "disabled"},
		{mode: "shadow", wantCalls: 1, wantDryRun: true},
	} {
		t.Run(test.mode, func(t *testing.T) {
			enabled := false
			client := &authEnablePrivilegedClient{cpuMinerPrivilegedClient: &cpuMinerPrivilegedClient{mode: test.mode}}
			system.ConfigurePrivilegedClient(client)
			t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

			server := &Server{cfg: &config.Config{
				Path:     authDefaultConfigPath,
				Features: config.FeaturesConfig{EnableLogin: &enabled},
			}}
			request := httptest.NewRequest(http.MethodPost, "/api/auth/enable-login", nil)
			response := httptest.NewRecorder()
			server.handleAuthEnableLogin(response, request)

			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusServiceUnavailable, response.Body.String())
			}
			if enabled || server.cfg.Features.LoginEnabled() {
				t.Fatal("failed-closed request changed the in-memory login setting")
			}
			if client.enableCalls != test.wantCalls || client.enableDryRun != test.wantDryRun {
				t.Fatalf("unexpected broker validation: calls=%d dry_run=%v", client.enableCalls, client.enableDryRun)
			}
		})
	}
}
