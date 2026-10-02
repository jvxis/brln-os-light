package privileged

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryHelperTrustIsLimitedToBridge(t *testing.T) {
	legacy, err := os.ReadFile("testdata/upgrade-app-0.5.33.sh")
	if err != nil {
		t.Fatal(err)
	}
	legacyContent := strings.ReplaceAll(string(legacy), "\r\n", "\n")
	if !trustedLightningOSUpgradeHelper(legacyContent, "0.5.40-beta") {
		t.Fatal("actual shipped 0.5.33 helper cannot reach bridge")
	}
	if trustedLightningOSUpgradeHelper(legacyContent+"\n", "0.5.40-beta") {
		t.Fatal("modified legacy helper was trusted")
	}
	for _, version := range []string{"0.5.40-beta", "0.5.40-Beta", "0.5.40"} {
		if !trustedLightningOSUpgradeDigest(legacyRecoveryHelperSHA256, version) {
			t.Fatalf("legacy bridge rejected: %s", version)
		}
	}
	for _, version := range []string{"0.5.39-beta", "0.5.41-beta", "1.0.0", "0.5.40-beta.evil"} {
		if trustedLightningOSUpgradeDigest(legacyRecoveryHelperSHA256, version) {
			t.Fatalf("legacy helper accepted outside bridge: %s", version)
		}
	}
	if trustedLightningOSUpgradeHelper("caller supplied shell code", "0.5.40-beta") {
		t.Fatal("untrusted helper accepted")
	}
	if !trustedLightningOSUpgradeDigest(lightningOSUpgradeHelperSHA256, "0.5.41-beta") {
		t.Fatal("current helper cannot perform next upgrade")
	}
}

type failingCredentialManager struct{ err error }

func (m failingCredentialManager) Ensure(context.Context, bool) (LNDManagerCredentialState, error) {
	return LNDManagerCredentialState{}, m.err
}
func (m failingCredentialManager) Rollback(context.Context, bool) (LNDManagerCredentialState, error) {
	return LNDManagerCredentialState{}, m.err
}

func TestCredentialDiagnosticsReachResponseAndAuditWithoutSecrets(t *testing.T) {
	for _, code := range []string{"admin_macaroon_mode", "admin_macaroon_owner", "lnd_unit_identity", "macaroon_path_unsupported", "transaction_incomplete", "rpc_failed", "unknown"} {
		t.Run(code, func(t *testing.T) {
			const secret = "DO-NOT-EXPOSE-CREDENTIAL"
			err := fmt.Errorf("%s: %w", secret, lndCredentialError(code))
			audit := &recordingAudit{}
			broker := &Broker{Runner: &recordingRunner{}, Audit: audit, Locker: &recordingLocker{}, LNDManagerCredential: failingCredentialManager{err}, Caller: "test"}
			response := broker.Handle(context.Background(), Request{Version: ProtocolVersion, RequestID: "credential_diagnostic", Operation: OperationLNDManagerCredentialEnsure, DryRun: true, Params: []byte(`{}`)})
			want := code
			if want == "unknown" {
				want = "lnd_manager_credential_failed"
			}
			if response.OK || response.Error == nil || response.Error.Code != want {
				t.Fatalf("diagnostic = %#v", response)
			}
			if strings.Contains(response.Error.Message, secret) {
				t.Fatal("response leaked an underlying error")
			}
			if len(audit.events) == 0 || audit.events[len(audit.events)-1].ErrorCode != want {
				t.Fatalf("missing audit diagnostic: %#v", audit.events)
			}
		})
	}
}

func TestMissingBrokerTransportHasTypedError(t *testing.T) {
	transport := &SocketTransport{Path: filepath.Join(t.TempDir(), "missing.sock")}
	_, err := transport.Do(context.Background(), Request{Version: ProtocolVersion})
	if !errors.Is(err, ErrBrokerUnavailable) {
		t.Fatalf("missing socket error = %v", err)
	}
}
