package privileged

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordingLightningOSUpgradeManager struct {
	params LightningOSUpgradeStartParams
	dryRun bool
	state  LightningOSUpgradeState
}

func (manager *recordingLightningOSUpgradeManager) Start(_ context.Context, params LightningOSUpgradeStartParams, dryRun bool) (LightningOSUpgradeState, error) {
	manager.params, manager.dryRun = params, dryRun
	return manager.state, nil
}

func validLightningOSUpgradeParams() LightningOSUpgradeStartParams {
	return LightningOSUpgradeStartParams{
		Version: "0.5.3-beta", Tag: "0.5.3-Beta", Commit: strings.Repeat("a", 40), HelperContent: "trusted helper", VerifyOnly: true,
	}
}

func TestLightningOSUpgradeHelperDigestMatchesEmbeddedAsset(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "server", "assets", "upgrade-app.sh"))
	if err != nil {
		t.Fatalf("read embedded upgrade helper: %v", err)
	}
	canonical := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n")
	digest := sha256.Sum256([]byte(canonical))
	if got := hex.EncodeToString(digest[:]); got != lightningOSUpgradeHelperSHA256 {
		t.Fatalf("upgrade helper digest = %s, want %s", got, lightningOSUpgradeHelperSHA256)
	}
}

func TestLightningOSUpgradeRequestIsClosedAndSerialized(t *testing.T) {
	params := validLightningOSUpgradeParams()
	raw, err := MarshalParams(params)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Version: ProtocolVersion, RequestID: "lightningos_upgrade_test", Operation: OperationLightningOSUpgradeStart, DryRun: true, Params: raw}
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("valid LightningOS request rejected: %v", err)
	}
	manager := &recordingLightningOSUpgradeManager{state: LightningOSUpgradeState{
		Status: "validated", Version: params.Version, Commit: params.Commit, Unit: lightningOSVerifyUnit, VerifyOnly: true,
	}}
	locker := &recordingLocker{}
	broker := &Broker{Runner: &recordingRunner{}, Audit: &recordingAudit{}, Locker: locker, LightningOSUpgrade: manager, Caller: "test"}
	response := broker.Handle(context.Background(), request)
	if !response.OK || !manager.dryRun || locker.locks != 0 {
		t.Fatalf("unexpected dry-run dispatch: response=%#v manager=%#v locker=%#v", response, manager, locker)
	}
	request.DryRun = false
	manager.state.Status = "started"
	response = broker.Handle(context.Background(), request)
	if !response.OK || manager.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("LightningOS mutation was not serialized: response=%#v manager=%#v locker=%#v", response, manager, locker)
	}
}

func TestLightningOSUpgradeProtocolRejectsInjectedOrMismatchedSource(t *testing.T) {
	params := validLightningOSUpgradeParams()
	tests := []json.RawMessage{
		json.RawMessage(`{"version":"0.5.3-beta","tag":"0.5.3-Beta","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","helper_content":"trusted","repo_url":"https://evil.invalid"}`),
		mustMarshalRaw(t, LightningOSUpgradeStartParams{Version: params.Version, Tag: "0.5.2-Beta", Commit: params.Commit, HelperContent: params.HelperContent}),
		mustMarshalRaw(t, LightningOSUpgradeStartParams{Version: params.Version, Tag: params.Tag, Commit: "main", HelperContent: params.HelperContent}),
		mustMarshalRaw(t, LightningOSUpgradeStartParams{Version: params.Version, Tag: params.Tag, Commit: params.Commit, HelperContent: strings.Repeat("x", 49*1024)}),
	}
	for _, raw := range tests {
		request := Request{Version: ProtocolVersion, RequestID: "lightningos_upgrade_bad", Operation: OperationLightningOSUpgradeStart, Params: raw}
		if err := ValidateRequest(request); err == nil {
			t.Fatalf("unsafe LightningOS source request accepted: %s", raw)
		}
	}
}

func mustMarshalRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := MarshalParams(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestClientLightningOSUpgradeRequiresExactTypedState(t *testing.T) {
	params := validLightningOSUpgradeParams()
	transport := &fakeTransport{result: LightningOSUpgradeState{
		Status: "validated", Version: params.Version, Commit: params.Commit, Unit: lightningOSVerifyUnit, VerifyOnly: true,
	}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	status, unit, err := client.StartLightningOSUpgrade(context.Background(), params.Version, params.Tag, params.Commit, params.HelperContent, true, true)
	if err != nil || status != "validated" || unit != lightningOSVerifyUnit || transport.request.Operation != OperationLightningOSUpgradeStart {
		t.Fatalf("unexpected LightningOS client result/request: %q %q %v %#v", status, unit, err, transport.request)
	}
	if _, _, err := client.StartLightningOSUpgrade(context.Background(), params.Version, params.Tag, params.Commit, params.HelperContent, true, false); err == nil {
		t.Fatal("client accepted dry-run LightningOS state for a real request")
	}
	transport.result = map[string]any{"status": "started", "version": params.Version, "commit": params.Commit, "unit": "ssh", "verify_only": true}
	if _, _, err := client.StartLightningOSUpgrade(context.Background(), params.Version, params.Tag, params.Commit, params.HelperContent, true, false); err == nil {
		t.Fatal("client accepted caller-selected LightningOS unit")
	}
}
