package server

import "testing"

func boolPtr(value bool) *bool {
	return &value
}

func TestAuthScopeValidSensitiveControls(t *testing.T) {
	for _, scope := range []string{
		authScopeWalletSendExternal,
		authScopeMacaroonExport,
		authScopeNodeRetirement,
		authScopeSuccessionLive,
		authScopeLoopSwap,
		authScopeLoopOutBRLN,
		authScopeTerminalCredential,
		authScopeTerminalControl,
		authScopeBarkSeedReveal,
	} {
		t.Run(scope, func(t *testing.T) {
			if !authScopeValid(scope) {
				t.Fatalf("expected scope %q to be valid", scope)
			}
		})
	}
	if authScopeValid("succession") {
		t.Fatalf("did not expect arbitrary scope to be valid")
	}
}

func TestNodeRetirementReauthPolicy(t *testing.T) {
	if !nodeRetirementAPISourceAllowed("") {
		t.Fatalf("expected empty source to be allowed")
	}
	if !nodeRetirementAPISourceAllowed(nodeRetirementSourceManual) {
		t.Fatalf("expected manual source to be allowed")
	}
	if nodeRetirementAPISourceAllowed(nodeRetirementSourceSuccession) {
		t.Fatalf("did not expect API-created succession source to be allowed")
	}

	if nodeRetirementCreateSessionRequiresControlReauth(nodeRetirementCreateSessionRequest{DryRun: true}) {
		t.Fatalf("did not expect dry-run session creation to require reauth")
	}
	if !nodeRetirementCreateSessionRequiresControlReauth(nodeRetirementCreateSessionRequest{DryRun: false}) {
		t.Fatalf("expected live session creation to require reauth")
	}
	if nodeRetirementConfirmCoopRequiresControlReauth(NodeRetirementSession{DryRun: true}) {
		t.Fatalf("did not expect dry-run coop confirmation to require reauth")
	}
	if !nodeRetirementConfirmCoopRequiresControlReauth(NodeRetirementSession{DryRun: false}) {
		t.Fatalf("expected live coop confirmation to require reauth")
	}
	if nodeRetirementDecisionRequiresControlReauth(NodeRetirementSession{DryRun: false}, nodeRetirementDecisionWait) {
		t.Fatalf("did not expect wait decision to require reauth")
	}
	if nodeRetirementDecisionRequiresControlReauth(NodeRetirementSession{DryRun: true}, nodeRetirementDecisionForceClose) {
		t.Fatalf("did not expect dry-run force-close decision to require reauth")
	}
	if !nodeRetirementDecisionRequiresControlReauth(NodeRetirementSession{DryRun: false}, nodeRetirementDecisionForceClose) {
		t.Fatalf("expected live force-close decision to require reauth")
	}
}

func TestSuccessionConfigPostRequiresLiveReauth(t *testing.T) {
	tests := []struct {
		name    string
		current SuccessionConfig
		req     successionConfigPostRequest
		want    bool
	}{
		{
			name:    "enabling dry-run does not require reauth",
			current: SuccessionConfig{Enabled: false, DryRun: true},
			req: successionConfigPostRequest{
				Enabled: boolPtr(true),
				DryRun:  boolPtr(true),
			},
			want: false,
		},
		{
			name:    "enabling live requires reauth",
			current: SuccessionConfig{Enabled: false, DryRun: true},
			req: successionConfigPostRequest{
				Enabled: boolPtr(true),
				DryRun:  boolPtr(false),
			},
			want: true,
		},
		{
			name:    "enabled true inherits current live dry-run state",
			current: SuccessionConfig{Enabled: false, DryRun: false},
			req: successionConfigPostRequest{
				Enabled: boolPtr(true),
			},
			want: true,
		},
		{
			name:    "continuing live mode requires reauth",
			current: SuccessionConfig{Enabled: true, DryRun: false},
			req:     successionConfigPostRequest{},
			want:    true,
		},
		{
			name:    "disabling live mode does not require reauth",
			current: SuccessionConfig{Enabled: true, DryRun: false},
			req: successionConfigPostRequest{
				Enabled: boolPtr(false),
			},
			want: false,
		},
		{
			name:    "switching live mode to dry-run does not require reauth",
			current: SuccessionConfig{Enabled: true, DryRun: false},
			req: successionConfigPostRequest{
				DryRun: boolPtr(true),
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := successionConfigPostRequiresLiveReauth(tc.current, tc.req)
			if got != tc.want {
				t.Fatalf("reauth policy mismatch: got %v want %v", got, tc.want)
			}
		})
	}
}
