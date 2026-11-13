package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/privileged"
	"lightningos-light/internal/system"
)

// removeCaptureClient records the declaration files exactly as the broker
// would read them at the moment the removal is requested.
type removeCaptureClient struct {
	*cpuMinerPrivilegedClient
	files    []string
	captured map[string]string
}

const capturedMissing = "<missing>"

func (client *removeCaptureClient) RemoveApp(ctx context.Context, appID string, dryRun bool) error {
	client.captured = make(map[string]string, len(client.files))
	for _, path := range client.files {
		raw, err := os.ReadFile(path)
		if err != nil {
			client.captured[path] = capturedMissing
			continue
		}
		client.captured[path] = string(raw)
	}
	return client.cpuMinerPrivilegedClient.RemoveApp(ctx, appID, dryRun)
}

func configureRemoveCaptureClient(t *testing.T, removeErr error, files ...string) *removeCaptureClient {
	t.Helper()
	client := &removeCaptureClient{
		cpuMinerPrivilegedClient: &cpuMinerPrivilegedClient{mode: "enforce", removeErr: removeErr},
		files:                    files,
	}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
	return client
}

// assertOnlyRemoveRequested proves removal stayed offline: no image pull or
// probe, no lifecycle, no network, firewall or Docker request reached the broker.
func assertOnlyRemoveRequested(t *testing.T, client *removeCaptureClient, appID string) {
	t.Helper()
	inner := client.cpuMinerPrivilegedClient
	if inner.removeCalls != 1 || inner.removeAppID != appID || inner.removeDryRun {
		t.Fatalf("unexpected broker removal: calls=%d app=%q dryRun=%v", inner.removeCalls, inner.removeAppID, inner.removeDryRun)
	}
	if inner.appCalls != 0 || inner.prepareCalls != 0 || inner.probeCalls != 0 || inner.statusCalls != 0 ||
		inner.networkCalls != 0 || inner.firewallCalls != 0 || inner.dockerCalls != 0 || inner.dockerStatusCalls != 0 ||
		inner.inspectCalls != 0 || inner.storageCalls != 0 || inner.packageCalls != 0 {
		t.Fatalf("removal made broker calls beyond app removal: %#v", inner)
	}
}

func mustWriteServerTestFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func testBtcpayRemovalPaths(t *testing.T) btcpayPaths {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "apps", btcpayAppID)
	dataRoot := filepath.Join(base, "apps-data", btcpayAppID)
	lndDir := filepath.Join(dataRoot, "lnd")
	return btcpayPaths{
		Root:           root,
		DataDir:        filepath.Join(dataRoot, "data"),
		NbxDir:         filepath.Join(dataRoot, "nbxplorer"),
		PgDir:          filepath.Join(dataRoot, "pgdata"),
		LndDir:         lndDir,
		ComposePath:    filepath.Join(root, appmanifest.BTCPayComposeFile),
		EnvPath:        filepath.Join(root, appmanifest.BTCPayEnvFile),
		DbInitPath:     filepath.Join(root, appmanifest.BTCPayDBInitFile),
		DbPasswordPath: filepath.Join(dataRoot, "btcpay-db-password.txt"),
		MacaroonPath:   filepath.Join(lndDir, appmanifest.BTCPayMacaroonFile),
		TLSCertPath:    filepath.Join(lndDir, appmanifest.BTCPayTLSCertFile),
	}
}

func testBtcpayRemovalEnv(rpcURL string, nodeEndpoint string, socks bool) string {
	lines := []string{
		"BTCPAY_DB_PASSWORD=" + strings.Repeat("d", 32),
		"NBXPLORER_BTCRPCURL=" + rpcURL,
		"NBXPLORER_BTCRPCUSER=rpc-user",
		"NBXPLORER_BTCRPCPASSWORD=rpc-secret-pass",
		"NBXPLORER_BTCNODEENDPOINT=" + nodeEndpoint,
	}
	if socks {
		lines = append(lines, "NBXPLORER_SOCKSENDPOINT="+btcpayTorSOCKSEndpoint)
	}
	return strings.Join(lines, "\n") + "\n"
}

// olderBtcpayCompose is the declaration a BTCPay installed at catalog 2.4.4
// left on disk: the same layout with the previous closed release tag.
func olderBtcpayCompose(t *testing.T, current string) string {
	t.Helper()
	older := strings.ReplaceAll(current, btcpayImage, "btcpayserver/btcpayserver:2.4.4")
	if older == current {
		t.Fatal("test fixture did not change the BTCPay release")
	}
	return older
}

func TestRemoveBtcpayAppReconcilesOlderCatalogBeforeBrokerRemoval(t *testing.T) {
	onion := strings.Repeat("b", 56) + ".onion"
	for _, test := range []struct {
		name   string
		env    string
		wiring btcpayBitcoinWiring
	}{
		{name: "remote clearnet", env: testBtcpayRemovalEnv("http://bitcoin.example.com:8332/", "bitcoin.example.com:8333", false), wiring: btcpayBitcoinWiring{}},
		{name: "remote onion", env: testBtcpayRemovalEnv("http://bitcoin.example.com:8332/", onion+":8333", true), wiring: btcpayBitcoinWiring{UseTorProxy: true}},
		{name: "app store bitcoin", env: testBtcpayRemovalEnv("http://bitcoind:8332/", "bitcoind:8333", false), wiring: btcpayBitcoinWiring{JoinBitcoinNetwork: true}},
		{name: "native bitcoin", env: testBtcpayRemovalEnv("http://"+appmanifest.BitcoinConsumerHostGateway+":8332/", appmanifest.BitcoinConsumerHostGateway+":8333", false), wiring: btcpayBitcoinWiring{JoinBitcoinNetwork: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := testBtcpayRemovalPaths(t)
			current := btcpayComposeContents(paths, test.wiring)
			mustWriteServerTestFile(t, paths.EnvPath, test.env, 0600)
			mustWriteServerTestFile(t, paths.ComposePath, olderBtcpayCompose(t, current), 0640)
			mustWriteServerTestFile(t, paths.DbInitPath, "-- init written by an older catalog\n", 0640)
			mustWriteServerTestFile(t, paths.MacaroonPath, "dedicated-btcpay-macaroon", 0600)
			mustWriteServerTestFile(t, filepath.Join(paths.DataDir, "store.db"), "store data", 0600)
			client := configureRemoveCaptureClient(t, nil, paths.ComposePath, paths.DbInitPath, paths.EnvPath)

			if err := removeBtcpayApp(context.Background(), paths); err != nil {
				t.Fatal(err)
			}
			assertOnlyRemoveRequested(t, client, btcpayAppID)
			if got := client.captured[paths.ComposePath]; got != current {
				t.Fatalf("broker removal was requested before the Compose was reconciled to the current catalog:\n%s", got)
			}
			if got := client.captured[paths.DbInitPath]; got != appmanifest.BTCPayDBInit() {
				t.Fatalf("database initialization was not reconciled: %q", got)
			}
			if got := client.captured[paths.EnvPath]; got != test.env {
				t.Fatalf("secret-bearing environment was rewritten: %q", got)
			}
			if strings.Contains(client.captured[paths.ComposePath], "rpc-secret-pass") || strings.Contains(client.captured[paths.ComposePath], strings.Repeat("d", 32)) {
				t.Fatal("reconciled Compose embeds environment credentials")
			}
			if _, err := os.Stat(paths.Root); !os.IsNotExist(err) {
				t.Fatalf("manager declaration still exists after removal: %v", err)
			}
			for _, kept := range []string{paths.MacaroonPath, filepath.Join(paths.DataDir, "store.db")} {
				if _, err := os.Stat(kept); err != nil {
					t.Fatalf("BTCPay data was not preserved: %v", err)
				}
			}
		})
	}
}

func TestRemoveBtcpayAppMatchesBrokerCatalogDerivation(t *testing.T) {
	// The Manager derives the wiring from the environment with the shared
	// catalog helper; the result must be the broker's expected declaration.
	paths := testBtcpayRemovalPaths(t)
	onion := strings.Repeat("c", 56) + ".onion"
	env := testBtcpayRemovalEnv("https://bitcoin.example.com:8332/", onion+":8333", true)
	join, tor, err := appmanifest.BTCPayComposeWiringFromEnv([]byte(env))
	if err != nil || join || !tor {
		t.Fatalf("join/tor/error = %v/%v/%v", join, tor, err)
	}
	want := appmanifest.BTCPayCompose(appmanifest.BTCPayComposePaths{
		DataDir: paths.DataDir, NbxDir: paths.NbxDir, PgDir: paths.PgDir, DbInitPath: paths.DbInitPath, LndDir: paths.LndDir,
	}, join, tor)
	if got := btcpayComposeContents(paths, btcpayBitcoinWiring{JoinBitcoinNetwork: join, UseTorProxy: tor}); got != want {
		t.Fatal("Manager BTCPay Compose diverged from the broker catalog")
	}
}

func TestRemoveBtcpayAppLeavesComposeWhenEnvironmentCannotBeDerived(t *testing.T) {
	for _, test := range []struct {
		name string
		env  *string
	}{
		{name: "missing environment"},
		{name: "unreadable wiring", env: func() *string { value := "NBXPLORER_BTCRPCURL=\n"; return &value }()},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := testBtcpayRemovalPaths(t)
			older := olderBtcpayCompose(t, btcpayComposeContents(paths, btcpayBitcoinWiring{}))
			mustWriteServerTestFile(t, paths.ComposePath, older, 0640)
			mustWriteServerTestFile(t, paths.DbInitPath, appmanifest.BTCPayDBInit(), 0640)
			if test.env != nil {
				mustWriteServerTestFile(t, paths.EnvPath, *test.env, 0600)
			}
			refusal := &privileged.ClientError{Code: privileged.ErrorCodeAppCatalogMismatch, Message: "refused"}
			client := configureRemoveCaptureClient(t, refusal, paths.ComposePath, paths.EnvPath)

			err := removeBtcpayApp(context.Background(), paths)
			var brokerErr *privileged.ClientError
			if !errors.As(err, &brokerErr) || brokerErr.Code != privileged.ErrorCodeAppCatalogMismatch {
				t.Fatalf("broker refusal was not preserved: %v", err)
			}
			if got := client.captured[paths.ComposePath]; got != older {
				t.Fatal("Compose was guessed without a derivable environment")
			}
			wantEnv := capturedMissing
			if test.env != nil {
				wantEnv = *test.env
			}
			if got := client.captured[paths.EnvPath]; got != wantEnv {
				t.Fatalf("environment was created or rewritten: %q", got)
			}
			if _, err := os.Stat(paths.ComposePath); err != nil {
				t.Fatalf("declaration removed after broker refusal: %v", err)
			}
		})
	}
}

func TestRemoveBtcpayAppNeverCreatesMissingDeclarationFiles(t *testing.T) {
	paths := testBtcpayRemovalPaths(t)
	mustWriteServerTestFile(t, paths.EnvPath, testBtcpayRemovalEnv("http://bitcoin.example.com:8332/", "bitcoin.example.com:8333", false), 0600)
	mustWriteServerTestFile(t, paths.ComposePath, "older", 0640)
	client := configureRemoveCaptureClient(t, errors.New("refused"), paths.ComposePath, paths.DbInitPath)

	if err := removeBtcpayApp(context.Background(), paths); err == nil {
		t.Fatal("broker refusal ignored")
	}
	if got := client.captured[paths.DbInitPath]; got != capturedMissing {
		t.Fatalf("missing declaration file was created: %q", got)
	}
	if got := client.captured[paths.ComposePath]; got != btcpayComposeContents(paths, btcpayBitcoinWiring{}) {
		t.Fatal("existing Compose was not reconciled")
	}
}

func TestRemoveElectrsAppReconcilesOlderCatalogBeforeBrokerRemoval(t *testing.T) {
	root := t.TempDir()
	paths := electrsPaths{
		Root:        root,
		ComposePath: filepath.Join(root, appmanifest.ElectrsComposeFile),
		EnvPath:     filepath.Join(root, appmanifest.ElectrsEnvFile),
		CookiePath:  filepath.Join(root, appmanifest.ElectrsCookieFile),
	}
	runtime := appmanifest.ElectrsRuntime{BitcoinMode: appmanifest.ElectrsBitcoinModeApp, Network: "bitcoin"}
	env, err := appmanifest.ElectrsRuntimeEnv(runtime)
	if err != nil {
		t.Fatal(err)
	}
	current, err := appmanifest.ElectrsCompose(runtime)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteServerTestFile(t, paths.EnvPath, env, 0640)
	mustWriteServerTestFile(t, paths.ComposePath, "services: {} # older catalog\n", 0640)
	mustWriteServerTestFile(t, paths.CookiePath, "rpc-user:rpc-secret", 0600)
	client := configureRemoveCaptureClient(t, nil, paths.ComposePath, paths.EnvPath, paths.CookiePath)

	if err := removeElectrsApp(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	assertOnlyRemoveRequested(t, client, electrsAppID)
	if client.captured[paths.ComposePath] != current {
		t.Fatal("Electrs Compose was not reconciled before broker removal")
	}
	if client.captured[paths.EnvPath] != env || client.captured[paths.CookiePath] != "rpc-user:rpc-secret" {
		t.Fatal("Electrs environment or credential was rewritten")
	}
}

func TestRemoveMempoolAppReconcilesOlderCatalogBeforeBrokerRemoval(t *testing.T) {
	root := t.TempDir()
	paths := mempoolPaths{
		Root:        root,
		ComposePath: filepath.Join(root, appmanifest.MempoolComposeFile),
		EnvPath:     filepath.Join(root, appmanifest.MempoolEnvFile),
	}
	runtime := appmanifest.MempoolRuntime{
		BitcoinMode: appmanifest.MempoolBitcoinModeApp, Network: "bitcoin", BitcoinRPCUser: "rpc-user",
		BitcoinRPCPass: "rpc-secret-pass", DBPassword: "db-secret-pass", DBRootPassword: "db-root-secret-pass",
	}
	env, err := appmanifest.MempoolRuntimeEnv(runtime)
	if err != nil {
		t.Fatal(err)
	}
	current, err := appmanifest.MempoolCompose(runtime)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteServerTestFile(t, paths.EnvPath, env, 0600)
	mustWriteServerTestFile(t, paths.ComposePath, "services: {} # older catalog\n", 0640)
	client := configureRemoveCaptureClient(t, nil, paths.ComposePath, paths.EnvPath)

	if err := removeMempoolApp(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	assertOnlyRemoveRequested(t, client, mempoolAppID)
	if client.captured[paths.ComposePath] != current {
		t.Fatal("Mempool Compose was not reconciled before broker removal")
	}
	if client.captured[paths.EnvPath] != env {
		t.Fatal("Mempool secret-bearing environment was rewritten")
	}
	if strings.Contains(client.captured[paths.ComposePath], "secret-pass") {
		t.Fatal("reconciled Mempool Compose embeds credentials")
	}
}

func TestRemoveRoboSatsAppReconcilesOlderCatalogBeforeBrokerRemoval(t *testing.T) {
	root := t.TempDir()
	paths := robosatsPaths{
		Root:          root,
		ComposePath:   filepath.Join(root, appmanifest.RoboSatsComposeFile),
		TLSDir:        filepath.Join(root, "tls"),
		CaddyfilePath: filepath.Join(root, "Caddyfile"),
	}
	keyPath := filepath.Join(paths.TLSDir, "server.key")
	mustWriteServerTestFile(t, paths.ComposePath, "services: {} # older catalog\n", 0640)
	mustWriteServerTestFile(t, paths.CaddyfilePath, ":443 { # older catalog }\n", 0640)
	mustWriteServerTestFile(t, keyPath, "private-key", 0600)
	client := configureRemoveCaptureClient(t, nil, paths.ComposePath, paths.CaddyfilePath, keyPath)

	if err := removeRoboSatsApp(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	assertOnlyRemoveRequested(t, client, appmanifest.RoboSatsID)
	if client.captured[paths.ComposePath] != robosatsComposeContents(paths) {
		t.Fatal("RoboSats Compose was not reconciled before broker removal")
	}
	if client.captured[paths.CaddyfilePath] != appmanifest.RoboSatsCaddyfile() {
		t.Fatal("RoboSats proxy configuration was not reconciled before broker removal")
	}
	if client.captured[keyPath] != "private-key" {
		t.Fatal("RoboSats TLS key was rewritten")
	}
}

func TestRemoveCpuMinerAppReconcilesOlderCatalogBeforeBrokerRemoval(t *testing.T) {
	root := t.TempDir()
	paths := cpuMinerPaths{Root: root, ComposePath: filepath.Join(root, "docker-compose.yaml"), EnvPath: filepath.Join(root, ".env")}
	env := "CPUMINER_IMAGE=old\nMINING_ADDRESS=bc1qexample\n"
	mustWriteServerTestFile(t, paths.ComposePath, "services: {} # older catalog\n", 0640)
	mustWriteServerTestFile(t, paths.EnvPath, env, 0600)
	client := configureRemoveCaptureClient(t, nil, paths.ComposePath, paths.EnvPath)

	if err := removeCpuMinerApp(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	assertOnlyRemoveRequested(t, client, cpuMinerAppID)
	if client.captured[paths.ComposePath] != appmanifest.CPUMinerCompose() {
		t.Fatal("CPU miner Compose was not reconciled before broker removal")
	}
	if client.captured[paths.EnvPath] != env {
		t.Fatal("CPU miner environment was rewritten")
	}
}

func TestReconcileInstalledCatalogFileIfPresentNeverCreates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sql")
	if err := reconcileInstalledCatalogFileIfPresent(path, "content"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("missing catalog file was created: %v", err)
	}
}

func TestWriteAppOperationErrorExplainsActionableBrokerCodes(t *testing.T) {
	secretPath := "/var/lib/lightningos/apps-data/lnbits/lnd/lnbits.macaroon"
	for _, test := range []struct {
		code    string
		want    string
		message string
	}{
		{code: privileged.ErrorCodeAppCatalogMismatch, want: appCatalogMismatchMessage},
		{code: privileged.ErrorCodeLNbitsCredentialEnrollmentMissing, want: appLNbitsCredentialNotEnrolledMessage},
		{code: privileged.ErrorCodeLNbitsCredentialLNDUnavailable, want: appLNbitsCredentialLNDUnavailableMessage},
	} {
		t.Run(test.code, func(t *testing.T) {
			err := fmt.Errorf("BTCPay removal failed: %w", &privileged.ClientError{Code: test.code, Message: "broker detail " + secretPath + " root 1700000000000"})
			recorder := httptest.NewRecorder()
			writeAppOperationError(recorder, http.StatusInternalServerError, err)
			body := recorder.Body.String()
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d, body = %s", recorder.Code, body)
			}
			if !strings.Contains(body, `"code":"`+test.code+`"`) {
				t.Fatalf("code missing from response: %s", body)
			}
			if !strings.Contains(body, test.want) {
				t.Fatalf("actionable message missing: %s", body)
			}
			if strings.Contains(body, secretPath) || strings.Contains(body, "1700000000000") || strings.Contains(body, "broker detail") {
				t.Fatalf("response forwarded broker detail: %s", body)
			}
		})
	}
	for _, text := range []string{appCatalogMismatchMessage, appLNbitsCredentialNotEnrolledMessage, appLNbitsCredentialLNDUnavailableMessage} {
		if strings.Contains(text, "/") {
			t.Fatalf("actionable message names a path: %q", text)
		}
	}
}

func TestWriteAppOperationErrorKeepsOtherFailuresUnchanged(t *testing.T) {
	err := fmt.Errorf("BTCPay removal failed: %w", &privileged.ClientError{Code: privileged.ErrorCodeAppRemoveFailed, Message: "app remove operation failed"})
	recorder := httptest.NewRecorder()
	writeAppOperationError(recorder, http.StatusInternalServerError, err)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "app_remove_failed: app remove operation failed") {
		t.Fatalf("generic failure changed: %d %s", recorder.Code, recorder.Body.String())
	}
}
