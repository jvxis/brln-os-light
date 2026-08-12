package privileged

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

type testLNbitsFixture struct {
	manager           *ComposeAppManager
	runner            *composeRecordingRunner
	appRoot           string
	dataRoot          string
	dataDir           string
	lndDataRoot       string
	privilegedRoot    string
	composePath       string
	envPath           string
	macaroonPath      string
	adminMacaroonPath string
	configPath        string
}

func testLNbitsEnv() []byte {
	lines := make([]string, 0, len(appmanifest.LNbitsManagedEnv())+2)
	for _, item := range appmanifest.LNbitsManagedEnv() {
		lines = append(lines, item[0]+"="+item[1])
	}
	lines = append(lines, "LNBITS_SITE_TITLE=Preserved Node", "")
	return []byte(strings.Join(lines, "\n"))
}

func writeTestLNbitsFixture(t *testing.T) *testLNbitsFixture {
	t.Helper()
	root := t.TempDir()
	appsRoot := filepath.Join(root, "apps")
	appsDataRoot := filepath.Join(root, "apps-data")
	privilegedRoot := filepath.Join(root, "privileged-apps")
	lndDataRoot := filepath.Join(root, "native-lnd")
	appRoot := filepath.Join(appsRoot, appmanifest.LNbitsID)
	dataRoot := filepath.Join(appsDataRoot, appmanifest.LNbitsID)
	dataDir := filepath.Join(dataRoot, "data")
	lndDir := filepath.Join(dataRoot, appmanifest.LNbitsLNDDir)
	for _, directory := range []string{
		appRoot,
		dataDir,
		filepath.Join(dataDir, "extensions"),
		lndDir,
		filepath.Join(lndDataRoot, "data", "chain", "bitcoin", "mainnet"),
	} {
		if err := os.MkdirAll(directory, 0750); err != nil {
			t.Fatal(err)
		}
	}
	composePath := filepath.Join(appRoot, appmanifest.LNbitsComposeFile)
	envPath := filepath.Join(appRoot, appmanifest.LNbitsEnvFile)
	certificatePath := filepath.Join(lndDir, appmanifest.LNbitsTLSCertFile)
	macaroonPath := filepath.Join(lndDir, appmanifest.LNbitsMacaroonFile)
	adminMacaroonPath := filepath.Join(lndDataRoot, "data", "chain", "bitcoin", "mainnet", "admin.macaroon")
	certificate := testLNDgCertificate(t, "host.docker.internal")
	mustWriteTestFile(t, composePath, []byte(appmanifest.LNbitsCompose(appmanifest.LNbitsComposePaths{
		DataDir:      dataDir,
		TLSCertPath:  certificatePath,
		MacaroonPath: macaroonPath,
	})), 0640)
	mustWriteTestFile(t, envPath, testLNbitsEnv(), 0600)
	mustWriteTestFile(t, certificatePath, certificate, 0640)
	mustWriteTestFile(t, macaroonPath, []byte("dedicated-lnbits-macaroon"), 0600)
	mustWriteTestFile(t, adminMacaroonPath, []byte("native-admin-macaroon"), 0600)
	mustWriteTestFile(t, filepath.Join(lndDataRoot, "tls.cert"), certificate, 0640)
	mustWriteTestFile(t, filepath.Join(lndDataRoot, "tls.key"), []byte("test-key"), 0600)
	mustWriteTestFile(t, filepath.Join(dataDir, "database.sqlite3"), []byte("preserved-data"), 0640)
	configPath := filepath.Join(lndDataRoot, "lnd.conf")
	mustWriteTestFile(t, configPath, []byte("[Application Options]\ntlsextraip=172.17.0.1\ntlsextradomain=host.docker.internal\nrestlisten=127.0.0.1:8080\nrestlisten=172.17.0.1:8080\nalias=test\n"), 0640)
	runner := &composeRecordingRunner{}
	return &testLNbitsFixture{
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
		dataDir:           dataDir,
		lndDataRoot:       lndDataRoot,
		privilegedRoot:    privilegedRoot,
		composePath:       composePath,
		envPath:           envPath,
		macaroonPath:      macaroonPath,
		adminMacaroonPath: adminMacaroonPath,
		configPath:        configPath,
	}
}

func TestLNbitsValidationAndSnapshotAreClosedAndPrivate(t *testing.T) {
	fixture := writeTestLNbitsFixture(t)
	files, err := fixture.manager.validatedLNbitsFiles()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := fixture.manager.createLNbitsSnapshot(files)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(fixture.privilegedRoot, appmanifest.LNbitsID)
	if snapshot.root != wantRoot {
		t.Fatalf("snapshot root=%q want=%q", snapshot.root, wantRoot)
	}
	composeRaw, err := os.ReadFile(snapshot.composePath)
	if err != nil {
		t.Fatal(err)
	}
	compose := string(composeRaw)
	for _, required := range []string{
		appmanifest.LNbitsImage,
		fixture.dataDir + ":/app/data:rw",
		filepath.Join(wantRoot, appmanifest.LNbitsLNDDir, appmanifest.LNbitsTLSCertFile) + ":/etc/lnd/tls.cert:ro",
		filepath.Join(wantRoot, appmanifest.LNbitsLNDDir, appmanifest.LNbitsMacaroonFile) + ":/etc/lnd/lnbits.macaroon:ro",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("execution compose missing %q\n%s", required, compose)
		}
	}
	for _, forbidden := range []string{fixture.appRoot, "admin.macaroon", "docker.sock", "/data/lnd"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("execution compose contains %q", forbidden)
		}
	}
	for _, path := range []string{
		snapshot.envPath,
		filepath.Join(wantRoot, appmanifest.LNbitsLNDDir, appmanifest.LNbitsTLSCertFile),
		filepath.Join(wantRoot, appmanifest.LNbitsLNDDir, appmanifest.LNbitsMacaroonFile),
	} {
		info, err := os.Lstat(path)
		unsafeMode := runtime.GOOS == "linux" && info != nil && info.Mode().Perm()&0007 != 0
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || unsafeMode {
			t.Fatalf("unsafe snapshot file %s: %#v/%v", path, info, err)
		}
	}
	if _, _, err := fixture.manager.createLNbitsSnapshot(files); err != nil {
		t.Fatalf("second idempotent LNbits snapshot failed: %v", err)
	}
}

func TestLNbitsValidationRejectsDeclarationAndCredentialDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *testLNbitsFixture)
	}{
		{name: "compose", mutate: func(t *testing.T, fixture *testLNbitsFixture) {
			mustWriteTestFile(t, fixture.composePath, []byte("services: {}\n"), 0640)
		}},
		{name: "environment", mutate: func(t *testing.T, fixture *testLNbitsFixture) {
			mustWriteTestFile(t, fixture.envPath, append(testLNbitsEnv(), []byte("PYTHONPATH=/hostile\n")...), 0600)
		}},
		{name: "admin macaroon", mutate: func(t *testing.T, fixture *testLNbitsFixture) {
			admin, err := os.ReadFile(fixture.adminMacaroonPath)
			if err != nil {
				t.Fatal(err)
			}
			mustWriteTestFile(t, fixture.macaroonPath, admin, 0600)
		}},
		{name: "unexpected LND asset", mutate: func(t *testing.T, fixture *testLNbitsFixture) {
			mustWriteTestFile(t, filepath.Join(filepath.Dir(fixture.macaroonPath), "admin.macaroon"), []byte("secret"), 0600)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := writeTestLNbitsFixture(t)
			test.mutate(t, fixture)
			if _, err := fixture.manager.validatedLNbitsFiles(); err == nil {
				t.Fatal("expected LNbits validation to fail")
			}
		})
	}
}

func TestLNbitsLifecycleUsesBrokerSnapshotWithoutRestartingConfiguredLND(t *testing.T) {
	fixture := writeTestLNbitsFixture(t)
	fixture.runner.hook = func(path string, args []string) (string, error, bool) {
		switch {
		case path == dockerPath && reflect.DeepEqual(args, []string{"network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}"}):
			return "172.17.0.1\n", nil, true
		case path == ufwPath && reflect.DeepEqual(args, []string{"status"}):
			return "Status: inactive\n", nil, true
		}
		return "", nil, false
	}
	if err := fixture.manager.Lifecycle(context.Background(), appmanifest.LNbitsID, AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	migrationObserved := false
	createObserved := false
	upObserved := false
	for _, command := range fixture.runner.commands {
		if command.path == systemctlPath && reflect.DeepEqual(command.args, []string{"restart", "lnd"}) {
			t.Fatalf("configured LND was unexpectedly restarted: %#v", command)
		}
		if strings.Contains(strings.Join(command.args, " "), fixture.appRoot) {
			t.Fatalf("manager-owned declaration reached Compose: %#v", command)
		}
		if command.path == dockerPath && len(command.args) > 2 && command.args[0] == "run" && command.args[len(command.args)-2] == "-c" {
			migrationObserved = true
			joined := strings.Join(command.args, " ")
			for _, required := range []string{"--network none", "--user 65532:65532", "--read-only", "--cap-drop ALL", "no-new-privileges", fixture.dataDir + ":/app/data:rw"} {
				if !strings.Contains(joined, required) {
					t.Fatalf("migration command missing %q: %#v", required, command)
				}
			}
		}
		if hasArgsSuffix(command.args, "create") {
			createObserved = true
		}
		if hasArgsSuffix(command.args, "up", "-d") {
			if !createObserved {
				t.Fatal("LNbits was started before its stopped container and network were prepared")
			}
			upObserved = true
		}
	}
	if !migrationObserved {
		t.Fatal("LNbits legacy settings migration was not executed")
	}
	if !createObserved || !upObserved {
		t.Fatalf("LNbits lifecycle did not execute create then up: create=%v up=%v", createObserved, upObserved)
	}
	if !strings.Contains(fixture.runner.composeSnapshot, filepath.Join(fixture.privilegedRoot, appmanifest.LNbitsID)) {
		t.Fatal("Compose did not execute the broker-owned LNbits snapshot")
	}
	if !strings.Contains(fixture.runner.envSnapshot, "LNBITS_SITE_TITLE=Preserved Node") {
		t.Fatal("safe existing LNbits configuration was not preserved")
	}
	dataRaw, err := os.ReadFile(filepath.Join(fixture.dataDir, "database.sqlite3"))
	if err != nil || string(dataRaw) != "preserved-data" {
		t.Fatalf("LNbits data changed during lifecycle: %q/%v", dataRaw, err)
	}
}

func TestLNbitsSettingsMigrationIsNarrowAndParameterized(t *testing.T) {
	for _, required := range []string{
		`"lnbits_backend_wallet_class": "LndRestWallet"`,
		`"lnd_rest_endpoint": "https://host.docker.internal:8080/"`,
		`"lnd_rest_cert": "/etc/lnd/tls.cert"`,
		`"lnd_rest_macaroon": "/etc/lnd/lnbits.macaroon"`,
		"update system_settings set value=? where id=?",
		"json.dumps(value)",
	} {
		if !strings.Contains(lnbitsSettingsMigrationScript, required) {
			t.Fatalf("settings migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"insert into", "delete from", "drop table", "/data/lnd", "admin.macaroon"} {
		if strings.Contains(strings.ToLower(lnbitsSettingsMigrationScript), forbidden) {
			t.Fatalf("settings migration contains forbidden operation %q", forbidden)
		}
	}
}

func TestLNbitsHostAccessPreservesListenersAndRestartsOnlyWhenRequired(t *testing.T) {
	fixture := writeTestLNbitsFixture(t)
	mustWriteTestFile(t, fixture.configPath, []byte("[Application Options]\nrestlisten=172.22.0.1:8080\nalias=test\n"), 0640)
	fixture.runner.hook = func(path string, args []string) (string, error, bool) {
		switch {
		case path == dockerPath && reflect.DeepEqual(args, []string{"network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}"}):
			return "172.17.0.1\n", nil, true
		case path == systemctlPath && reflect.DeepEqual(args, []string{"restart", "lnd"}):
			return "", nil, true
		case path == ufwPath && reflect.DeepEqual(args, []string{"status"}):
			return "Status: inactive\n", nil, true
		}
		return "", nil, false
	}
	if err := fixture.manager.ensureLNbitsHostAccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"restlisten=172.22.0.1:8080",
		"restlisten=172.17.0.1:8080",
		"tlsextraip=172.17.0.1",
		"tlsextradomain=host.docker.internal",
	} {
		if !strings.Contains(string(raw), required) {
			t.Fatalf("updated LND config lost %q: %s", required, raw)
		}
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

func TestUpdateLNbitsRESTOptionsAcceptsExistingWildcardWithoutConflictingBinds(t *testing.T) {
	lines := []string{"[Application Options]", "restlisten=0.0.0.0:8080", "alias=test"}
	got, changed := updateLNbitsRESTOptions(lines, "172.17.0.1")
	if !changed {
		t.Fatal("expected missing TLS identities to be added")
	}
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "restlisten=172.17.0.1:8080") {
		t.Fatalf("specific listeners conflict with existing wildcard: %s", joined)
	}
}

func TestLNbitsRemovePreservesPersistentData(t *testing.T) {
	fixture := writeTestLNbitsFixture(t)
	if err := fixture.manager.Remove(context.Background(), appmanifest.LNbitsID, false); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(fixture.dataDir, "database.sqlite3")); err != nil || string(raw) != "preserved-data" {
		t.Fatalf("LNbits uninstall removed persistent data: %q/%v", raw, err)
	}
	if _, err := os.Lstat(filepath.Join(fixture.privilegedRoot, appmanifest.LNbitsID)); !os.IsNotExist(err) {
		t.Fatalf("LNbits execution snapshot still exists: %v", err)
	}
}
