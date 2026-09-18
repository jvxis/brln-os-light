package privileged

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func testBRLNCommunityManager(t *testing.T, runner CommandRunner) *NativeBRLNCommunityManager {
	t.Helper()
	root := t.TempDir()
	snapshot := filepath.Join(root, "privileged", appmanifest.BRLNCommunityID)
	dataRoot := filepath.Join(root, "apps-data", appmanifest.BRLNCommunityID)
	tlsDir := filepath.Join(snapshot, appmanifest.BRLNCommunityTLSDir)
	managerCA := filepath.Join(root, "manager-ca.crt")
	managerCertificate, err := generateTestBarkManagerCA()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managerCA, managerCertificate, 0644); err != nil {
		t.Fatal(err)
	}
	return &NativeBRLNCommunityManager{Runner: runner, Paths: BRLNCommunityPaths{
		SnapshotRoot: snapshot, SignerDir: filepath.Join(dataRoot, "signer"), AuthDir: filepath.Join(dataRoot, "auth"),
		KeyPasswordPath:    filepath.Join(dataRoot, "auth", "key_password"),
		AccessPasswordPath: filepath.Join(dataRoot, "auth", "access_password"),
		ComposePath:        filepath.Join(snapshot, appmanifest.BRLNCommunityComposeFile),
		CaddyfilePath:      filepath.Join(snapshot, appmanifest.BRLNCommunityCaddyfile), TLSDir: tlsDir,
		TLSCertificate: filepath.Join(tlsDir, "server.crt"), TLSPrivateKey: filepath.Join(tlsDir, "server.key"),
		ManagerCACertificate: managerCA,
	}}
}

func TestNativeBRLNCommunityProductionPathsAreFixed(t *testing.T) {
	manager := NewNativeBRLNCommunityManager(&composeRecordingRunner{})
	if !manager.requireFixed {
		t.Fatal("production manager must require the fixed manager CA path")
	}
	for _, path := range []string{manager.Paths.SignerDir, manager.Paths.AuthDir, manager.Paths.KeyPasswordPath, manager.Paths.AccessPasswordPath} {
		if !strings.HasPrefix(filepath.ToSlash(path), filepath.ToSlash(filepath.Join(defaultAppsDataRoot, appmanifest.BRLNCommunityID))+"/") {
			t.Fatalf("app data path escapes apps-data: %q", path)
		}
	}
	if !strings.HasPrefix(filepath.ToSlash(manager.Paths.ComposePath), filepath.ToSlash(filepath.Join(defaultPrivilegedAppsRoot, appmanifest.BRLNCommunityID))+"/") {
		t.Fatalf("compose is outside the broker snapshot: %q", manager.Paths.ComposePath)
	}
}

func TestNativeBRLNCommunityEnsureCreatesBrokerOwnedMaterial(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := testBRLNCommunityManager(t, runner)
	state, err := manager.Ensure(context.Background(), false)
	if err != nil || !state.Installed || !state.PasswordAvailable {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	password, err := manager.ReadPassword()
	if err != nil || len(password) < 24 {
		t.Fatalf("generated access password is unavailable: length=%d err=%v", len(password), err)
	}
	keyPassword, err := readBarkWalletSecret(manager.Paths.KeyPasswordPath)
	if err != nil || keyPassword == password {
		t.Fatalf("key password must exist and differ from the access password: %v", err)
	}
	certificate, certificateErr := readRegularFile(manager.Paths.TLSCertificate, 64*1024)
	privateKey, privateKeyErr := readRegularFile(manager.Paths.TLSPrivateKey, 64*1024)
	if certificateErr != nil || privateKeyErr != nil || validateTLSKeyPair(certificate, privateKey) != nil {
		t.Fatalf("generated TLS pair is invalid: %v/%v", certificateErr, privateKeyErr)
	}
	compose, _ := os.ReadFile(manager.Paths.ComposePath)
	expected, _ := appmanifest.BRLNCommunityCompose(manager.composePaths())
	if string(compose) != expected {
		t.Fatal("broker snapshot does not match the catalog")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("ensure unexpectedly executed a command: %#v", runner.commands)
	}
}

func TestNativeBRLNCommunityEnsureKeepsExistingIdentityAndSecrets(t *testing.T) {
	manager := testBRLNCommunityManager(t, &composeRecordingRunner{})
	if _, err := manager.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	key := []byte("ncryptsec1-member-key\n")
	if err := os.WriteFile(filepath.Join(manager.Paths.SignerDir, "key.ncryptsec"), key, 0600); err != nil {
		t.Fatal(err)
	}
	keyHash := sha256.Sum256(key)
	passwordHash := sha256.Sum256(mustReadTestFile(t, manager.Paths.KeyPasswordPath))
	accessHash := sha256.Sum256(mustReadTestFile(t, manager.Paths.AccessPasswordPath))
	certificate := mustReadTestFile(t, manager.Paths.TLSCertificate)

	if _, err := manager.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	assertBarkWalletFileHash(t, filepath.Join(manager.Paths.SignerDir, "key.ncryptsec"), keyHash)
	assertBarkWalletFileHash(t, manager.Paths.KeyPasswordPath, passwordHash)
	assertBarkWalletFileHash(t, manager.Paths.AccessPasswordPath, accessHash)
	if !reflect.DeepEqual(mustReadTestFile(t, manager.Paths.TLSCertificate), certificate) {
		t.Fatal("TLS certificate was regenerated")
	}
}

func TestNativeBRLNCommunityRemovePreservesIdentity(t *testing.T) {
	manager := testBRLNCommunityManager(t, &composeRecordingRunner{})
	if _, err := manager.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	key := []byte("ncryptsec1-member-key\n")
	if err := os.WriteFile(filepath.Join(manager.Paths.SignerDir, "key.ncryptsec"), key, 0600); err != nil {
		t.Fatal(err)
	}
	passwordHash := sha256.Sum256(mustReadTestFile(t, manager.Paths.KeyPasswordPath))
	if err := manager.Remove(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	assertBarkWalletFileHash(t, filepath.Join(manager.Paths.SignerDir, "key.ncryptsec"), sha256.Sum256(key))
	assertBarkWalletFileHash(t, manager.Paths.KeyPasswordPath, passwordHash)
	if _, err := os.Stat(manager.Paths.SnapshotRoot); !os.IsNotExist(err) {
		t.Fatal("execution snapshot was not removed")
	}
}

func TestNativeBRLNCommunityRemoveWithoutInstallIsNoop(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := testBRLNCommunityManager(t, runner)
	if err := manager.Remove(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("remove without install ran commands: %#v", runner.commands)
	}
}

func TestNativeBRLNCommunityLifecycleRejectsTamperedSnapshotBeforeDocker(t *testing.T) {
	for name, tamper := range map[string]func(*NativeBRLNCommunityManager) error{
		"compose": func(manager *NativeBRLNCommunityManager) error {
			return os.WriteFile(manager.Paths.ComposePath, []byte("services: {evil: {privileged: true}}\n"), 0600)
		},
		"proxy": func(manager *NativeBRLNCommunityManager) error {
			config := strings.Replace(appmanifest.BRLNCommunityCaddyConfig(), "forward_auth", "# forward_auth", 1)
			return os.WriteFile(manager.Paths.CaddyfilePath, []byte(config), 0640)
		},
		"extra snapshot entry": func(manager *NativeBRLNCommunityManager) error {
			return os.WriteFile(filepath.Join(manager.Paths.SnapshotRoot, "override.yaml"), []byte("x"), 0600)
		},
		"access password": func(manager *NativeBRLNCommunityManager) error {
			return os.WriteFile(manager.Paths.AccessPasswordPath, []byte("short\n"), 0640)
		},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &composeRecordingRunner{}
			manager := testBRLNCommunityManager(t, runner)
			if _, err := manager.Ensure(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			if err := tamper(manager); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Lifecycle(context.Background(), AppLifecycleStart, false); err == nil {
				t.Fatal("tampered snapshot accepted")
			}
			if _, err := manager.ReadPassword(); err == nil {
				t.Fatal("password disclosed from a tampered snapshot")
			}
			if len(runner.commands) != 0 {
				t.Fatalf("tampered snapshot reached Docker: %#v", runner.commands)
			}
		})
	}
}

func TestNativeBRLNCommunityEnsureRejectsNonCAManagerCertificate(t *testing.T) {
	manager := testBRLNCommunityManager(t, &composeRecordingRunner{})
	certificate, _, err := generateLocalAppTLS("not-a-ca")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.ManagerCACertificate, certificate, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Ensure(context.Background(), true); err == nil || !strings.Contains(err.Error(), "manager CA certificate is invalid") {
		t.Fatalf("non-CA manager certificate accepted: %v", err)
	}
}

func TestNativeBRLNCommunityEnsureRejectsSymlinkedSignerState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics require POSIX")
	}
	manager := testBRLNCommunityManager(t, &composeRecordingRunner{})
	if err := os.MkdirAll(manager.Paths.SignerDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/shadow", filepath.Join(manager.Paths.SignerDir, "key.ncryptsec")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Ensure(context.Background(), false); err == nil {
		t.Fatal("symlinked signer entry accepted")
	}
}

func TestNativeBRLNCommunityFirewallUsesOnlyFixedBridgeAndCatalogPorts(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == ufwPath && reflect.DeepEqual(args, []string{"status"}) {
			return "Status: active", nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"network", "inspect", "brln-community_default", "--format", "{{.Id}}"}) {
			return "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n", nil, true
		}
		return "", nil, false
	}}
	manager := testBRLNCommunityManager(t, runner)
	state, err := manager.EnsureFirewall(context.Background(), false)
	if err != nil || !state.UFWActive {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	want := [][]string{
		{"status"},
		{"network", "inspect", "brln-community_default", "--format", "{{.Id}}"},
		{"allow", "in", "on", "br-0123456789ab", "to", "any", "port", "8443", "proto", "tcp"},
		{"allow", "4448/tcp"},
	}
	if len(runner.commands) != len(want) {
		t.Fatalf("commands=%#v", runner.commands)
	}
	for index := range want {
		if !reflect.DeepEqual(runner.commands[index].args, want[index]) {
			t.Fatalf("command[%d]=%#v", index, runner.commands[index])
		}
	}
}

func TestNativeBRLNCommunityFirewallRejectsInjectedNetworkID(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == ufwPath && reflect.DeepEqual(args, []string{"status"}) {
			return "Status: active", nil, true
		}
		if path == dockerPath && len(args) > 0 && args[0] == "network" {
			return "0123456789ab;reboot\n", nil, true
		}
		return "", nil, false
	}}
	manager := testBRLNCommunityManager(t, runner)
	if _, err := manager.EnsureFirewall(context.Background(), false); err == nil || !strings.Contains(err.Error(), "network ID is invalid") {
		t.Fatalf("injected network ID accepted: %v", err)
	}
	for _, command := range runner.commands {
		if command.path == ufwPath && len(command.args) > 0 && command.args[0] == "allow" {
			t.Fatalf("firewall mutation occurred after invalid network ID: %#v", command)
		}
	}
}

func TestBRLNCommunityProtocolIsClosed(t *testing.T) {
	tests := []struct {
		name      string
		operation Operation
		params    any
		dryRun    bool
		wantErr   bool
	}{
		{"status", OperationBRLNCommunityStatus, struct{}{}, false, false},
		{"status dry run", OperationBRLNCommunityStatus, struct{}{}, true, true},
		{"ensure dry run", OperationBRLNCommunityEnsure, struct{}{}, true, false},
		{"start", OperationBRLNCommunityLifecycle, BRLNCommunityLifecycleParams{Action: AppLifecycleStart}, false, false},
		{"restart rejected", OperationBRLNCommunityLifecycle, BRLNCommunityLifecycleParams{Action: AppLifecycleRestart}, false, true},
		{"password read", OperationBRLNCommunityPasswordRead, struct{}{}, false, false},
		{"password read dry run", OperationBRLNCommunityPasswordRead, struct{}{}, true, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := MarshalParams(test.params)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateRequest(Request{Version: ProtocolVersion, RequestID: "brln_test", Operation: test.operation, DryRun: test.dryRun, Params: raw})
			if (err != nil) != test.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, test.wantErr)
			}
		})
	}
	for _, payload := range []string{
		`{"version":1,"request_id":"brln_test","operation":"app.brlncommunity.ensure","params":{"path":"/etc/shadow"}}`,
		`{"version":1,"request_id":"brln_test","operation":"app.brlncommunity.lifecycle","params":{"action":"start","args":["--privileged"]}}`,
		`{"version":1,"request_id":"brln_test","operation":"app.brlncommunity.password.read","params":{"file":"key_password"}}`,
		`{"version":1,"request_id":"brln_test","operation":"app.brlncommunity.key.read","params":{}}`,
	} {
		if _, err := DecodeRequest(strings.NewReader(payload)); err == nil {
			t.Fatalf("injected BR⚡LN Community request accepted: %s", payload)
		}
	}
}

type recordingBRLNCommunityManager struct {
	operation string
	action    AppLifecycleAction
	dryRun    bool
	state     BRLNCommunityState
	password  string
	err       error
}

func (manager *recordingBRLNCommunityManager) Status(context.Context) (BRLNCommunityState, error) {
	manager.operation = "status"
	return manager.state, manager.err
}
func (manager *recordingBRLNCommunityManager) Ensure(_ context.Context, dryRun bool) (BRLNCommunityState, error) {
	manager.operation, manager.dryRun = "ensure", dryRun
	return manager.state, manager.err
}
func (manager *recordingBRLNCommunityManager) Lifecycle(_ context.Context, action AppLifecycleAction, dryRun bool) (BRLNCommunityState, error) {
	manager.operation, manager.action, manager.dryRun = "lifecycle", action, dryRun
	return manager.state, manager.err
}
func (manager *recordingBRLNCommunityManager) Remove(_ context.Context, dryRun bool) error {
	manager.operation, manager.dryRun = "remove", dryRun
	return manager.err
}
func (manager *recordingBRLNCommunityManager) EnsureFirewall(_ context.Context, dryRun bool) (BRLNCommunityState, error) {
	manager.operation, manager.dryRun = "firewall", dryRun
	return manager.state, manager.err
}
func (manager *recordingBRLNCommunityManager) ReadPassword() (string, error) {
	manager.operation = "password-read"
	return manager.password, manager.err
}

func TestBrokerDispatchesBRLNCommunityOperationsWithoutAuditingPassword(t *testing.T) {
	const password = "Signer_Access_Password_Never_Audit_123"
	tests := []struct {
		name          string
		operation     Operation
		params        any
		dryRun        bool
		wantOperation string
		wantLocks     int
	}{
		{"status", OperationBRLNCommunityStatus, struct{}{}, false, "status", 0},
		{"ensure", OperationBRLNCommunityEnsure, struct{}{}, false, "ensure", 1},
		{"lifecycle", OperationBRLNCommunityLifecycle, BRLNCommunityLifecycleParams{Action: AppLifecycleStop}, false, "lifecycle", 1},
		{"remove dry run", OperationBRLNCommunityRemove, struct{}{}, true, "remove", 0},
		{"firewall", OperationBRLNCommunityFirewall, struct{}{}, false, "firewall", 1},
		{"password read", OperationBRLNCommunityPasswordRead, struct{}{}, false, "password-read", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &recordingBRLNCommunityManager{
				state:    BRLNCommunityState{Installed: true, Status: "stopped", PasswordAvailable: true},
				password: password,
			}
			audit := &recordingAudit{}
			locker := &recordingLocker{}
			broker := &Broker{Runner: &recordingRunner{}, Audit: audit, Locker: locker, BRLNCommunity: manager, Caller: "test"}
			params, err := MarshalParams(test.params)
			if err != nil {
				t.Fatal(err)
			}
			response := broker.Handle(context.Background(), Request{
				Version: ProtocolVersion, RequestID: "brln_broker", Operation: test.operation,
				DryRun: test.dryRun, Params: params,
			})
			if !response.OK || manager.operation != test.wantOperation || locker.locks != test.wantLocks {
				t.Fatalf("response=%#v operation=%q locks=%d", response, manager.operation, locker.locks)
			}
			encodedAudit, err := json.Marshal(audit.events)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encodedAudit), password) {
				t.Fatal("signer access password leaked into privileged audit")
			}
		})
	}
}
