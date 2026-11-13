package privileged

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
)

func appRemoveRequest(t *testing.T, appID string, dryRun bool) Request {
	t.Helper()
	params, err := MarshalParams(AppRemoveParams{AppID: appID})
	if err != nil {
		t.Fatal(err)
	}
	return Request{Version: ProtocolVersion, RequestID: "app_remove_diag_1", Operation: OperationAppRemove, DryRun: dryRun, Params: params}
}

func assertNoSecretMaterial(t *testing.T, response Response, forbidden ...string) {
	t.Helper()
	if response.Error == nil {
		t.Fatalf("expected an error response: %#v", response)
	}
	for _, text := range []string{response.Error.Code, response.Error.Message} {
		if strings.Contains(text, "/") || strings.Contains(text, `\`) {
			t.Fatalf("broker diagnostic names a path: %q", text)
		}
		for _, value := range forbidden {
			if value != "" && strings.Contains(text, value) {
				t.Fatalf("broker diagnostic leaked %q: %q", value, text)
			}
		}
	}
}

func TestBrokerAppRemoveCatalogMismatchHasSpecificCode(t *testing.T) {
	for _, err := range []error{
		catalogMismatchError("BTCPay compose manifest does not match the catalog"),
		fmt.Errorf("wrapped: %w", catalogMismatchError("app proxy configuration does not match the catalog")),
	} {
		broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
		broker.Apps = &recordingApps{err: err}
		response := broker.Handle(context.Background(), appRemoveRequest(t, appmanifest.BTCPayID, false))
		if response.OK || response.Error.Code != ErrorCodeAppCatalogMismatch {
			t.Fatalf("unexpected response: %#v", response)
		}
		assertNoSecretMaterial(t, response)
	}
}

func TestBrokerAppRemoveOtherFailuresStayGenericWithoutSecrets(t *testing.T) {
	secretPath := "/var/lib/lightningos/apps-data/btcpay/lnd/btcpay.macaroon"
	for _, err := range []error{
		errors.New("BTCPay LND credential must not be the admin macaroon " + secretPath),
		errors.New("app remove command failed"),
		errLNbitsCredentialNotEnrolled,
	} {
		broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
		broker.Apps = &recordingApps{err: err}
		response := broker.Handle(context.Background(), appRemoveRequest(t, appmanifest.BTCPayID, false))
		if response.OK || response.Error.Code != ErrorCodeAppRemoveFailed || response.Error.Message != "app remove operation failed" {
			t.Fatalf("unexpected response: %#v", response)
		}
		assertNoSecretMaterial(t, response, secretPath, "macaroon")
	}
}

func TestComposeAppBTCPayRemoveRefusesOlderCatalogWithSpecificCode(t *testing.T) {
	fixture := writeTestBTCPayApp(t, false, true)
	current, err := os.ReadFile(fixture.composePath)
	if err != nil {
		t.Fatal(err)
	}
	older := strings.ReplaceAll(string(current), appmanifest.BTCPayServerImage, "btcpayserver/btcpayserver:2.4.4")
	if older == string(current) {
		t.Fatal("fixture did not change the BTCPay release")
	}
	mustWriteTestFile(t, fixture.composePath, []byte(older), 0600)

	// The validation is as strict as before: the older declaration is refused
	// before any Compose command, now with a classified diagnostic.
	err = fixture.manager.Remove(context.Background(), appmanifest.BTCPayID, false)
	if !errors.Is(err, errAppCatalogMismatch) || err.Error() != "BTCPay compose manifest does not match the catalog" {
		t.Fatalf("older catalog declaration was not refused as a mismatch: %v", err)
	}
	runner := fixture.manager.Runner.(*composeRecordingRunner)
	for _, command := range runner.commands {
		if strings.Contains(strings.Join(command.args, " "), "down") {
			t.Fatalf("Compose ran for a refused declaration: %#v", command)
		}
	}

	broker := testBroker(runner, &recordingAudit{}, &recordingLocker{})
	broker.Apps = fixture.manager
	response := broker.Handle(context.Background(), appRemoveRequest(t, appmanifest.BTCPayID, false))
	if response.OK || response.Error.Code != ErrorCodeAppCatalogMismatch {
		t.Fatalf("unexpected response: %#v", response)
	}
	assertNoSecretMaterial(t, response, fixture.appRoot, testBTCPayDedicatedMacaroon, testBTCPayAdminMacaroon, "rpc-pass")
	if _, err := os.Stat(filepath.Join(fixture.privilegedRoot, appmanifest.BTCPayID)); !os.IsNotExist(err) {
		t.Fatalf("refused removal created a privileged snapshot: %v", err)
	}
}

func TestComposeAppBTCPayRemoveAcceptsManagerReconciledDeclaration(t *testing.T) {
	// The Manager rewrites the Compose from the existing environment with
	// appmanifest.BTCPayComposeWiringFromEnv. The unchanged broker validation
	// must accept exactly that result for every supported wiring.
	for _, wiring := range []struct{ join, tor bool }{{false, false}, {false, true}, {true, false}} {
		t.Run(fmt.Sprintf("join=%v tor=%v", wiring.join, wiring.tor), func(t *testing.T) {
			fixture := writeTestBTCPayApp(t, wiring.join, wiring.tor)
			mustWriteTestFile(t, fixture.composePath, []byte("services: {} # older catalog\n"), 0600)
			if err := fixture.manager.Remove(context.Background(), appmanifest.BTCPayID, true); !errors.Is(err, errAppCatalogMismatch) {
				t.Fatalf("stale declaration was not refused: %v", err)
			}
			join, tor, err := appmanifest.BTCPayComposeWiringFromEnv([]byte(fixture.envRaw))
			if err != nil || join != wiring.join || tor != wiring.tor {
				t.Fatalf("derived join/tor/error = %v/%v/%v", join, tor, err)
			}
			mustWriteTestFile(t, fixture.composePath, []byte(appmanifest.BTCPayCompose(fixture.composePaths, join, tor)), 0600)
			if err := fixture.manager.Remove(context.Background(), appmanifest.BTCPayID, false); err != nil {
				t.Fatalf("reconciled declaration was refused: %v", err)
			}
		})
	}
}

func TestBTCPayComposeWiringFromEnvMatchesBrokerValidation(t *testing.T) {
	onion := strings.Repeat("a", 56) + ".onion"
	for _, env := range []string{
		testBTCPayEnv("http://bitcoin.example.com:8332/", "bitcoin.example.com:8333", ""),
		testBTCPayEnv("https://bitcoin.example.com:8332/", onion+":8333", "tor:9050"),
		testBTCPayEnv("http://"+strings.ToUpper(onion)+":8332/", onion+":8333", "tor:9050"),
		testBTCPayEnv("http://bitcoind:8332/", "bitcoind:8333", ""),
		testBTCPayEnv("http://"+appmanifest.BitcoinConsumerHostGateway+":18443/", appmanifest.BitcoinConsumerHostGateway+":8333", ""),
		testBTCPayEnv("http://203.0.113.10:8332/", "203.0.113.10:8333", ""),
	} {
		wantJoin, wantTor, err := validateBTCPayEnv([]byte(env))
		if err != nil {
			t.Fatalf("fixture rejected by broker: %v\n%s", err, env)
		}
		join, tor, err := appmanifest.BTCPayComposeWiringFromEnv([]byte(env))
		if err != nil || join != wantJoin || tor != wantTor {
			t.Fatalf("Manager derivation %v/%v/%v differs from broker %v/%v for\n%s", join, tor, err, wantJoin, wantTor, env)
		}
	}
}

func TestComposeAppRemoveClassifiesOtherCatalogDeclarations(t *testing.T) {
	appsRoot, validEnv := writeTestCPUMinerApp(t)
	manager := &ComposeAppManager{Runner: &composeRecordingRunner{}, AppsRoot: appsRoot}
	appRoot := filepath.Join(appsRoot, appmanifest.CPUMinerID)
	mustWriteTestFile(t, filepath.Join(appRoot, appmanifest.CPUMinerComposeFile), []byte("services: {} # older catalog\n"), 0600)
	if err := manager.Remove(context.Background(), appmanifest.CPUMinerID, true); !errors.Is(err, errAppCatalogMismatch) {
		t.Fatalf("CPU miner stale Compose not classified: %v", err)
	}
	mustWriteTestFile(t, filepath.Join(appRoot, appmanifest.CPUMinerComposeFile), []byte(appmanifest.CPUMinerCompose()), 0600)
	if err := manager.Remove(context.Background(), appmanifest.CPUMinerID, true); err != nil {
		t.Fatalf("current CPU miner declaration refused: %v (env %q)", err, validEnv)
	}

	roboSatsRoot := writeTestRoboSatsApp(t)
	roboSats := &ComposeAppManager{Runner: &composeRecordingRunner{}, AppsRoot: roboSatsRoot}
	caddyfilePath := filepath.Join(roboSatsRoot, appmanifest.RoboSatsID, appmanifest.RoboSatsCaddyfileFile)
	mustWriteTestFile(t, caddyfilePath, []byte(":443 {}\n"), 0600)
	if err := roboSats.Remove(context.Background(), appmanifest.RoboSatsID, true); !errors.Is(err, errAppCatalogMismatch) {
		t.Fatalf("RoboSats stale proxy configuration not classified: %v", err)
	}
	if err := os.Remove(caddyfilePath); err != nil {
		t.Fatal(err)
	}
	if err := roboSats.Remove(context.Background(), appmanifest.RoboSatsID, true); err == nil || errors.Is(err, errAppCatalogMismatch) {
		t.Fatalf("missing proxy configuration must stay a generic refusal: %v", err)
	}
}

type lnbitsUnavailableLNDRPC struct{ lnbitsRevokeTestRPC }

func (r *lnbitsUnavailableLNDRPC) ListMacaroonIDs(context.Context) ([]uint64, error) {
	return nil, errors.New("rpc error: connection refused to 127.0.0.1:10009")
}

func revokeResponse(t *testing.T, manager *ComposeAppManager) Response {
	t.Helper()
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
	broker.Apps = manager
	return broker.Handle(context.Background(), emptyParamsRequest(t, OperationLNbitsLegacyCredentialRevoke, false))
}

func TestLNbitsRevocationDiagnosticsAreSpecificAndSecretSafe(t *testing.T) {
	root := uint64(time.Now().Add(-time.Hour).UnixMilli())
	rootText := strconv.FormatUint(root, 10)
	enrollmentPath := func(m *ComposeAppManager) string {
		return filepath.Join(m.PrivilegedAppsRoot, appmanifest.LNbitsID, appmanifest.LNbitsLNDDir, appmanifest.LNbitsMacaroonFile)
	}
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, m *ComposeAppManager, rpc *lnbitsRevokeTestRPC)
		code    string
	}{
		{
			name: "enrollment missing",
			prepare: func(t *testing.T, m *ComposeAppManager, _ *lnbitsRevokeTestRPC) {
				if err := os.RemoveAll(filepath.Join(m.PrivilegedAppsRoot, appmanifest.LNbitsID)); err != nil {
					t.Fatal(err)
				}
			},
			code: ErrorCodeLNbitsCredentialEnrollmentMissing,
		},
		{
			name: "enrollment differs",
			prepare: func(t *testing.T, m *ComposeAppManager, _ *lnbitsRevokeTestRPC) {
				mustWriteTestFile(t, enrollmentPath(m), []byte("different-enrolled-credential"), 0640)
			},
			code: ErrorCodeLNbitsCredentialEnrollmentMissing,
		},
		{
			name: "LND unavailable",
			prepare: func(_ *testing.T, m *ComposeAppManager, rpc *lnbitsRevokeTestRPC) {
				m.lnbitsRevocationRPC = &lnbitsUnavailableLNDRPC{lnbitsRevokeTestRPC: *rpc}
			},
			code: ErrorCodeLNbitsCredentialLNDUnavailable,
		},
		{
			name: "forged credential",
			prepare: func(_ *testing.T, _ *ComposeAppManager, rpc *lnbitsRevokeTestRPC) {
				rpc.authenticated = false
			},
			code: ErrorCodeLNbitsCredentialRevokeFailed,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, rpc := lnbitsRevokeFixture(t, root, legacyLNbitsTestKeys)
			test.prepare(t, m, rpc)
			response := revokeResponse(t, m)
			if response.OK || response.Error.Code != test.code {
				t.Fatalf("unexpected response: %#v", response)
			}
			assertNoSecretMaterial(t, response, rootText, m.AppsDataRoot, m.PrivilegedAppsRoot, "fixture-only", "127.0.0.1", "connection refused")
			if rpc.deleted != 0 {
				t.Fatal("refused migration revoked a root")
			}
		})
	}
}

func TestLNbitsRevocationEnrollmentRefusalStaysIntentional(t *testing.T) {
	m, rpc := lnbitsRevokeFixture(t, uint64(time.Now().Add(-time.Hour).UnixMilli()), legacyLNbitsTestKeys)
	if err := os.RemoveAll(filepath.Join(m.PrivilegedAppsRoot, appmanifest.LNbitsID)); err != nil {
		t.Fatal(err)
	}
	for _, dryRun := range []bool{true, false} {
		err := m.RevokeLNbitsLegacyCredential(context.Background(), dryRun)
		if !errors.Is(err, errLNbitsCredentialNotEnrolled) {
			t.Fatalf("unenrolled credential not refused (dryRun=%v): %v", dryRun, err)
		}
	}
	if rpc.deleted != 0 {
		t.Fatal("unenrolled credential was revoked automatically")
	}
	if _, err := os.Stat(filepath.Join(m.PrivilegedAppsRoot, "lnbits-credential-revocation.json")); !os.IsNotExist(err) {
		t.Fatalf("refused revocation wrote a receipt: %v", err)
	}
}
