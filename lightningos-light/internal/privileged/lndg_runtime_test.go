package privileged

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
)

type testLNDgFixture struct {
	manager           *ComposeAppManager
	runner            *composeRecordingRunner
	appRoot           string
	dataRoot          string
	lndDataRoot       string
	privilegedRoot    string
	composePath       string
	envPath           string
	entrypointPath    string
	macaroonPath      string
	adminMacaroonPath string
	configPath        string
	certificate       []byte
	runtime           appmanifest.LNDgRuntime
}

func writeTestLNDgFixture(t *testing.T) *testLNDgFixture {
	t.Helper()
	root := t.TempDir()
	appsRoot := filepath.Join(root, "apps")
	appsDataRoot := filepath.Join(root, "apps-data")
	privilegedRoot := filepath.Join(root, "privileged-apps")
	lndDataRoot := filepath.Join(root, "native-lnd")
	appRoot := filepath.Join(appsRoot, appmanifest.LNDgID)
	dataRoot := filepath.Join(appsDataRoot, appmanifest.LNDgID)
	dataDir := filepath.Join(dataRoot, "data")
	pgDir := filepath.Join(dataRoot, "pgdata")
	lndDir := filepath.Join(dataRoot, appmanifest.LNDgLNDDir)
	for _, directory := range []string{
		appRoot,
		dataDir,
		pgDir,
		lndDir,
		filepath.Join(lndDataRoot, "data", "chain", "bitcoin", "mainnet"),
		filepath.Join(lndDataRoot, "data", "graph", "mainnet"),
	} {
		if err := os.MkdirAll(directory, 0750); err != nil {
			t.Fatal(err)
		}
	}
	runtime := appmanifest.LNDgRuntime{
		AdminPassword: base64.RawURLEncoding.EncodeToString([]byte("01234567890123456789")),
		DBPassword:    base64.RawURLEncoding.EncodeToString([]byte("012345678901234567890123")),
		AllowedHosts:  []string{"localhost", "127.0.0.1", "host.docker.internal", "10.42.0.92"},
	}
	env, err := appmanifest.LNDgRuntimeEnv(runtime)
	if err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(appRoot, appmanifest.LNDgComposeFile)
	envPath := filepath.Join(appRoot, appmanifest.LNDgEnvFile)
	entrypointPath := filepath.Join(appRoot, appmanifest.LNDgEntrypointFile)
	macaroonPath := filepath.Join(lndDir, appmanifest.LNDgMacaroonFile)
	adminMacaroonPath := filepath.Join(lndDataRoot, "data", "chain", "bitcoin", "mainnet", "admin.macaroon")
	certificate := testLNDgCertificate(t, "host.docker.internal")
	mustWriteTestFile(t, composePath, []byte(appmanifest.LNDgCompose(appmanifest.LNDgComposePaths{
		DataDir:        dataDir,
		PgDir:          pgDir,
		LogPath:        filepath.Join(dataDir, "lndg-controller.log"),
		LndDir:         lndDir,
		ChannelDBPath:  filepath.Join(lndDataRoot, "data", "graph", "mainnet", "channel.db"),
		EntrypointPath: entrypointPath,
	})), 0640)
	mustWriteTestFile(t, envPath, []byte(env), 0600)
	mustWriteTestFile(t, entrypointPath, []byte(appmanifest.LNDgEntrypoint), 0750)
	mustWriteTestFile(t, filepath.Join(lndDir, appmanifest.LNDgTLSCertFile), certificate, 0640)
	mustWriteTestFile(t, macaroonPath, []byte("dedicated-lndg-macaroon"), 0600)
	mustWriteTestFile(t, adminMacaroonPath, []byte("native-admin-macaroon"), 0600)
	mustWriteTestFile(t, filepath.Join(lndDataRoot, "data", "graph", "mainnet", "channel.db"), []byte("channel-db"), 0600)
	mustWriteTestFile(t, filepath.Join(lndDataRoot, "tls.cert"), certificate, 0640)
	mustWriteTestFile(t, filepath.Join(lndDataRoot, "tls.key"), []byte("test-key"), 0600)
	mustWriteTestFile(t, filepath.Join(dataDir, "lndg-controller.log"), nil, 0640)
	mustWriteTestFile(t, filepath.Join(dataDir, "lndg-admin.txt"), []byte(runtime.AdminPassword+"\n"), 0600)
	mustWriteTestFile(t, filepath.Join(dataDir, "lndg-db-password.txt"), []byte(runtime.DBPassword+"\n"), 0600)
	configPath := filepath.Join(lndDataRoot, "lnd.conf")
	mustWriteTestFile(t, configPath, []byte("[Application Options]\ntlsextraip=172.17.0.1\ntlsextradomain=host.docker.internal\nrpclisten=127.0.0.1:10009\nrpclisten=172.17.0.1:10009\nalias=test\n"), 0640)
	runner := &composeRecordingRunner{}
	return &testLNDgFixture{
		manager: &ComposeAppManager{
			Runner:             runner,
			AppsRoot:           appsRoot,
			AppsDataRoot:       appsDataRoot,
			PrivilegedAppsRoot: privilegedRoot,
			LNDDataRoot:        lndDataRoot,
			LNDConfigPath:      configPath,
		},
		runner:            runner,
		appRoot:           appRoot,
		dataRoot:          dataRoot,
		lndDataRoot:       lndDataRoot,
		privilegedRoot:    privilegedRoot,
		composePath:       composePath,
		envPath:           envPath,
		entrypointPath:    entrypointPath,
		macaroonPath:      macaroonPath,
		adminMacaroonPath: adminMacaroonPath,
		configPath:        configPath,
		certificate:       certificate,
		runtime:           runtime,
	}
}

func TestLNDgValidationAndSnapshotAreClosedAndPrivate(t *testing.T) {
	fixture := writeTestLNDgFixture(t)
	files, err := fixture.manager.validatedLNDgFiles()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := fixture.manager.createLNDgSnapshot(files)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(fixture.privilegedRoot, appmanifest.LNDgID)
	if snapshot.root != wantRoot {
		t.Fatalf("snapshot root=%q want=%q", snapshot.root, wantRoot)
	}
	composeRaw, err := os.ReadFile(snapshot.composePath)
	if err != nil {
		t.Fatal(err)
	}
	compose := string(composeRaw)
	for _, required := range []string{
		appmanifest.LNDgImage,
		appmanifest.LNDgPostgresImage,
		filepath.Join(wantRoot, appmanifest.LNDgLNDDir),
		filepath.Join(wantRoot, appmanifest.LNDgEntrypointFile),
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("execution compose missing %q", required)
		}
	}
	for _, forbidden := range []string{fixture.appRoot, "admin.macaroon", "docker.sock", "build:"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("execution compose contains %q", forbidden)
		}
	}
	for _, path := range []string{
		snapshot.envPath,
		filepath.Join(wantRoot, appmanifest.LNDgLNDDir, appmanifest.LNDgTLSCertFile),
		filepath.Join(wantRoot, appmanifest.LNDgLNDDir, appmanifest.LNDgMacaroonFile),
	} {
		info, err := os.Lstat(path)
		unsafeMode := runtime.GOOS == "linux" && info != nil && info.Mode().Perm()&0007 != 0
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || unsafeMode {
			t.Fatalf("unsafe snapshot file %s: %#v/%v", path, info, err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(wantRoot, appmanifest.LNDgLNDDir, appmanifest.LNDgMacaroonFile)); err != nil || string(raw) != "dedicated-lndg-macaroon" {
		t.Fatalf("unexpected LNDg credential snapshot: %q/%v", raw, err)
	}
	if _, _, err := fixture.manager.createLNDgSnapshot(files); err != nil {
		t.Fatalf("second idempotent LNDg snapshot failed: %v", err)
	}
}

func TestLNDgPostgresBackedLNDUsesPrivateEmptyChannelDBPlaceholder(t *testing.T) {
	fixture := writeTestLNDgFixture(t)
	nativeChannelDB := filepath.Join(fixture.lndDataRoot, "data", "graph", "mainnet", appmanifest.LNDgChannelDBFile)
	if err := os.Remove(nativeChannelDB); err != nil {
		t.Fatal(err)
	}
	placeholder := filepath.Join(fixture.dataRoot, appmanifest.LNDgLNDDir, appmanifest.LNDgChannelDBFile)
	mustWriteTestFile(t, placeholder, nil, 0640)
	compose := appmanifest.LNDgCompose(appmanifest.LNDgComposePaths{
		DataDir:        filepath.Join(fixture.dataRoot, "data"),
		PgDir:          filepath.Join(fixture.dataRoot, "pgdata"),
		LogPath:        filepath.Join(fixture.dataRoot, "data", "lndg-controller.log"),
		LndDir:         filepath.Join(fixture.dataRoot, appmanifest.LNDgLNDDir),
		ChannelDBPath:  placeholder,
		EntrypointPath: fixture.entrypointPath,
	})
	mustWriteTestFile(t, fixture.composePath, []byte(compose), 0640)

	files, err := fixture.manager.validatedLNDgFiles()
	if err != nil {
		t.Fatal(err)
	}
	if !files.placeholderDB || files.channelDBPath != placeholder {
		t.Fatalf("unexpected placeholder selection: path=%q placeholder=%v", files.channelDBPath, files.placeholderDB)
	}
	snapshot, _, err := fixture.manager.createLNDgSnapshot(files)
	if err != nil {
		t.Fatal(err)
	}
	snapshotPlaceholder := filepath.Join(snapshot.root, appmanifest.LNDgLNDDir, appmanifest.LNDgChannelDBFile)
	info, err := os.Lstat(snapshotPlaceholder)
	if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
		t.Fatalf("invalid broker placeholder: %#v/%v", info, err)
	}
	raw, err := os.ReadFile(snapshot.composePath)
	if err != nil || !strings.Contains(string(raw), snapshotPlaceholder+":/etc/lnd/channel.db:ro") {
		t.Fatalf("snapshot does not mount its private placeholder: %v", err)
	}
}

func TestLNDgValidationRejectsTamperingAndBroadSecrets(t *testing.T) {
	for _, test := range []struct {
		name   string
		tamper func(*testing.T, *testLNDgFixture)
	}{
		{"compose", func(t *testing.T, fixture *testLNDgFixture) {
			mustWriteTestFile(t, fixture.composePath, []byte("services: {}\n"), 0640)
		}},
		{"environment", func(t *testing.T, fixture *testLNDgFixture) {
			raw, _ := os.ReadFile(fixture.envPath)
			mustWriteTestFile(t, fixture.envPath, []byte(strings.Replace(string(raw), "LNDG_NETWORK=mainnet", "LNDG_NETWORK=regtest", 1)), 0600)
		}},
		{"entrypoint", func(t *testing.T, fixture *testLNDgFixture) {
			mustWriteTestFile(t, fixture.entrypointPath, []byte(appmanifest.LNDgEntrypoint+"echo tampered\n"), 0750)
		}},
		{"broad macaroon", func(t *testing.T, fixture *testLNDgFixture) {
			if runtime.GOOS != "linux" {
				t.Skip("POSIX mode validation is Linux-specific")
			}
			if err := os.Chmod(fixture.macaroonPath, 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"admin macaroon", func(t *testing.T, fixture *testLNDgFixture) {
			admin, _ := os.ReadFile(fixture.adminMacaroonPath)
			mustWriteTestFile(t, fixture.macaroonPath, admin, 0600)
		}},
		{"unexpected app asset", func(t *testing.T, fixture *testLNDgFixture) {
			mustWriteTestFile(t, filepath.Join(fixture.appRoot, "evil.sh"), []byte("reboot\n"), 0600)
		}},
		{"password mismatch", func(t *testing.T, fixture *testLNDgFixture) {
			mustWriteTestFile(t, filepath.Join(fixture.dataRoot, "data", "lndg-admin.txt"), []byte("wrong\n"), 0600)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := writeTestLNDgFixture(t)
			test.tamper(t, fixture)
			if _, err := fixture.manager.validatedLNDgFiles(); err == nil {
				t.Fatal("expected tampered LNDg declaration to be rejected")
			}
		})
	}
}

func TestLNDgLifecycleUsesOnlyBrokerSnapshotAndDoesNotRestartConfiguredLND(t *testing.T) {
	fixture := writeTestLNDgFixture(t)
	imageID := "sha256:" + strings.Repeat("a", 64)
	attestationRoot := filepath.Join(fixture.privilegedRoot, appmanifest.LNDgID)
	if err := os.MkdirAll(attestationRoot, 0700); err != nil {
		t.Fatal(err)
	}
	attestation := "image_id=" + imageID + "\n" +
		"release=" + appmanifest.LNDgRelease + "\n" +
		"commit=" + appmanifest.LNDgSourceCommit + "\n" +
		"source_sha256=" + appmanifest.LNDgSourceSHA256 + "\n" +
		"base_image=" + appmanifest.LNDgBaseImage + "\n" +
		"dockerfile_sha256=" + appmanifest.LNDgDockerfileSHA256() + "\n"
	mustWriteTestFile(t, filepath.Join(attestationRoot, lndgImageAttestationFile), []byte(attestation), 0600)
	containerID := strings.Repeat("b", 64)
	fixture.runner.hook = func(path string, args []string) (string, error, bool) {
		joined := strings.Join(args, " ")
		switch {
		case path == dockerPath && reflect.DeepEqual(args, []string{"image", "inspect", "--format", "{{.Id}}", appmanifest.LNDgImage}):
			return imageID + "\n", nil, true
		case path == dockerPath && len(args) == 3 && args[0] == "image" && args[1] == "inspect":
			return "ok\n", nil, true
		case strings.Contains(joined, "ps -q "+appmanifest.LNDgDatabaseService):
			return containerID + "\n", nil, true
		case path == dockerPath && len(args) >= 2 && args[0] == "exec" && args[1] == "-i":
			return "ok\n", nil, true
		case path == dockerPath && reflect.DeepEqual(args, []string{"network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}"}):
			return "172.17.0.1\n", nil, true
		case path == ufwPath && reflect.DeepEqual(args, []string{"status"}):
			return "Status: inactive\n", nil, true
		}
		return "", nil, false
	}
	if err := fixture.manager.Lifecycle(context.Background(), appmanifest.LNDgID, AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	for _, command := range fixture.runner.commands {
		if command.path == systemctlPath && len(command.args) > 0 && command.args[0] == "restart" {
			t.Fatalf("configured LND was unexpectedly restarted: %#v", command)
		}
		joined := strings.Join(command.args, " ")
		if strings.Contains(joined, fixture.runtime.AdminPassword) || strings.Contains(joined, fixture.runtime.DBPassword) {
			t.Fatalf("secret leaked into command arguments: %#v", command)
		}
		if strings.Contains(joined, fixture.appRoot) {
			t.Fatalf("manager-owned declaration reached Compose: %#v", command)
		}
	}
	if !strings.Contains(fixture.runner.composeSnapshot, filepath.Join(fixture.privilegedRoot, appmanifest.LNDgID)) {
		t.Fatal("Compose did not execute the broker-owned LNDg snapshot")
	}
	databaseSyncChecked := false
	for _, command := range fixture.runner.commands {
		if command.path != dockerPath || len(command.args) < 5 || command.args[0] != "exec" {
			continue
		}
		script := command.args[len(command.args)-1]
		if strings.Contains(script, "ALTER USER lndg") {
			databaseSyncChecked = true
			if !strings.Contains(script, "<<'SQL'") || strings.Contains(script, `-c "ALTER USER`) {
				t.Fatalf("database credential sync must use psql stdin substitution: %q", script)
			}
		}
	}
	if !databaseSyncChecked {
		t.Fatal("database credential sync command was not observed")
	}
}

func TestLNDgHostAccessPreservesUnrelatedListenersAndRestartsOnlyOnChange(t *testing.T) {
	fixture := writeTestLNDgFixture(t)
	mustWriteTestFile(t, fixture.configPath, []byte("[Application Options]\nrpclisten=127.0.0.1:10009\nrpclisten=172.22.0.1:10009\nalias=test\n"), 0640)
	fixture.runner.hook = func(path string, args []string) (string, error, bool) {
		if path == dockerPath && reflect.DeepEqual(args, []string{"network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}"}) {
			return "172.17.0.1\n", nil, true
		}
		if path == systemctlPath && reflect.DeepEqual(args, []string{"restart", "lnd"}) {
			return "", nil, true
		}
		if path == ufwPath && reflect.DeepEqual(args, []string{"status"}) {
			return "Status: inactive\n", nil, true
		}
		return "", nil, false
	}
	if err := fixture.manager.ensureLNDgHostAccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"rpclisten=172.22.0.1:10009",
		"rpclisten=172.17.0.1:10009",
		"tlsextraip=172.17.0.1",
		"tlsextradomain=host.docker.internal",
	} {
		if !strings.Contains(string(raw), required) {
			t.Fatalf("updated LND config lost %q: %s", required, raw)
		}
	}
	if _, err := os.Lstat(filepath.Join(fixture.lndDataRoot, "tls.cert")); !os.IsNotExist(err) {
		t.Fatal("LND certificate was not removed before the required restart")
	}
	restarts := 0
	for _, command := range fixture.runner.commands {
		if command.path == systemctlPath && reflect.DeepEqual(command.args, []string{"restart", "lnd"}) {
			restarts++
		}
	}
	if restarts != 1 {
		t.Fatalf("LND restart count=%d want=1", restarts)
	}
}

func TestLNDgAdminResetUsesContainerEnvironmentWithoutSecretArguments(t *testing.T) {
	fixture := writeTestLNDgFixture(t)
	containerID := strings.Repeat("c", 64)
	fixture.runner.hook = func(path string, args []string) (string, error, bool) {
		if strings.Contains(strings.Join(args, " "), "ps -q "+appmanifest.LNDgPrimaryService) {
			return containerID + "\n", nil, true
		}
		if path == dockerPath && len(args) > 2 && args[0] == "exec" {
			return "ok\n", nil, true
		}
		return "", nil, false
	}
	if err := fixture.manager.ResetAdmin(context.Background(), appmanifest.LNDgID, false); err != nil {
		t.Fatal(err)
	}
	for _, command := range fixture.runner.commands {
		joined := strings.Join(command.args, " ")
		if strings.Contains(joined, fixture.runtime.AdminPassword) || strings.Contains(joined, fixture.runtime.DBPassword) {
			t.Fatalf("secret leaked into reset command: %#v", command)
		}
	}
	if err := fixture.manager.ResetAdmin(context.Background(), "lndg;reboot", true); err == nil {
		t.Fatal("expected unknown admin reset app to be rejected")
	}
}

func testLNDgCertificate(t *testing.T, dnsName string) []byte {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
}

func TestLNDgHostAccessRejectsUntrustedGateway(t *testing.T) {
	fixture := writeTestLNDgFixture(t)
	fixture.runner.hook = func(path string, args []string) (string, error, bool) {
		if path == dockerPath {
			return "203.0.113.1\n", nil, true
		}
		return "", errors.New("unexpected command"), true
	}
	if err := fixture.manager.ensureLNDgHostAccess(context.Background()); err == nil {
		t.Fatal("expected public Docker gateway to be rejected")
	}
}
