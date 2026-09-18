package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

type brlnCommunityTestClient struct {
	*cpuMinerPrivilegedClient
	installed      bool
	status         string
	ensureCalls    int
	lifecycleCalls []string
	firewallCalls  int
	removeCalls    int
}

func (client *brlnCommunityTestClient) BRLNCommunityStatus(context.Context) (bool, string, bool, bool, error) {
	return client.installed, client.status, false, client.installed, nil
}
func (client *brlnCommunityTestClient) EnsureBRLNCommunity(context.Context, bool) (string, error) {
	client.ensureCalls++
	return "stopped", nil
}
func (client *brlnCommunityTestClient) BRLNCommunityLifecycle(_ context.Context, action string, _ bool) (string, error) {
	client.lifecycleCalls = append(client.lifecycleCalls, action)
	return "stopped", nil
}
func (client *brlnCommunityTestClient) RemoveBRLNCommunity(context.Context, bool) error {
	client.removeCalls++
	return nil
}
func (client *brlnCommunityTestClient) EnsureBRLNCommunityFirewall(context.Context, bool) (string, error) {
	client.firewallCalls++
	return "active", nil
}
func (client *brlnCommunityTestClient) ReadBRLNCommunityPassword(context.Context) (string, error) {
	return "", nil
}

func TestBRLNCommunityDefinitionUsesClosedCatalogPortWithoutLNDAccess(t *testing.T) {
	definition := brlnCommunityDefinition()
	if definition.ID != appmanifest.BRLNCommunityID || definition.Port != appmanifest.BRLNCommunityPort {
		t.Fatalf("unexpected BR⚡LN Community definition: %#v", definition)
	}
	if len(definition.SecurityNotices) != 0 {
		t.Fatalf("BR⚡LN Community must not declare LND access: %#v", definition.SecurityNotices)
	}
	if !strings.Contains(definition.Name, "BR⚡LN") || !strings.Contains(strings.ToLower(definition.Description), "test build") {
		t.Fatalf("store entry must name BR⚡LN and say it is a test build: %#v", definition)
	}
}

func TestBRLNCommunityAccessPasswordPathIsTheBrokerSecret(t *testing.T) {
	want := filepath.Join(appsDataRoot, appmanifest.BRLNCommunityID, "auth", "access_password")
	if got := brlnCommunityAccessPasswordPath(); got != want {
		t.Fatalf("access password path = %q, want %q", got, want)
	}
}

func TestBRLNCommunityInfoFailsClosedWithoutBrokerEnforce(t *testing.T) {
	system.ConfigurePrivilegedClient(nil)
	if _, err := newBRLNCommunityApp(&Server{}).Info(context.Background()); err == nil {
		t.Fatal("status without the broker in enforce mode was accepted")
	}
	if err := (&Server{}).uninstallBRLNCommunity(context.Background()); err == nil {
		t.Fatal("removal without the broker in enforce mode was accepted")
	}
}

func TestStopBRLNCommunityIsIdempotentWhenStopped(t *testing.T) {
	client := &brlnCommunityTestClient{
		cpuMinerPrivilegedClient: &cpuMinerPrivilegedClient{mode: "enforce"},
		installed:                true,
		status:                   "stopped",
	}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := (&Server{}).stopBRLNCommunity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.ensureCalls != 0 || len(client.lifecycleCalls) != 0 {
		t.Fatalf("stopped app was mutated: %#v", client)
	}
}

func TestStopBRLNCommunityStopsRunningApp(t *testing.T) {
	client := &brlnCommunityTestClient{
		cpuMinerPrivilegedClient: &cpuMinerPrivilegedClient{mode: "enforce"},
		installed:                true,
		status:                   "running",
	}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := (&Server{}).stopBRLNCommunity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.lifecycleCalls) != 1 || client.lifecycleCalls[0] != "stop" {
		t.Fatalf("running app was not stopped: %#v", client.lifecycleCalls)
	}
}

func TestUninstallBRLNCommunityUsesOnlyTheBroker(t *testing.T) {
	client := &brlnCommunityTestClient{
		cpuMinerPrivilegedClient: &cpuMinerPrivilegedClient{mode: "enforce"},
		installed:                true,
		status:                   "stopped",
	}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := (&Server{}).uninstallBRLNCommunity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.removeCalls != 1 {
		t.Fatalf("remove calls = %d", client.removeCalls)
	}
}

func TestBRLNCommunitySignerAuthorizationRequiresRecentReauth(t *testing.T) {
	now := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
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
	request := httptest.NewRequest(http.MethodGet, "/api/apps/brln-community/signer-authorization", nil)
	request = request.WithContext(context.WithValue(request.Context(), authSessionContextKey, snapshot))

	recorder := httptest.NewRecorder()
	server.handleBRLNCommunitySignerAuthorization(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired || !strings.Contains(recorder.Body.String(), "brln_community_signer_reauth_required") {
		t.Fatalf("expected fresh reauth challenge, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("signer authorization challenge must not be cached")
	}

	auth.sessions["session-id"].ReauthScopes[authScopeBarkSeedReveal] = now.Add(time.Minute)
	recorder = httptest.NewRecorder()
	server.handleBRLNCommunitySignerAuthorization(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("another app's reauth scope was accepted: status=%d", recorder.Code)
	}

	auth.sessions["session-id"].ReauthScopes[authScopeBRLNCommunitySigner] = now.Add(-4 * time.Minute)
	recorder = httptest.NewRecorder()
	server.handleBRLNCommunitySignerAuthorization(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("expired signer reauth was accepted: status=%d", recorder.Code)
	}

	auth.sessions["session-id"].ReauthScopes[authScopeBRLNCommunitySigner] = now.Add(time.Minute)
	recorder = httptest.NewRecorder()
	server.handleBRLNCommunitySignerAuthorization(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("recent reauth was rejected: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}
