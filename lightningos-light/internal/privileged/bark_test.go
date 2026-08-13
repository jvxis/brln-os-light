package privileged

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func testBarkWalletManager(t *testing.T, runner CommandRunner) *NativeBarkWalletManager {
	t.Helper()
	root := t.TempDir()
	snapshot := filepath.Join(root, "privileged", appmanifest.BarkWalletID)
	dataRoot := filepath.Join(root, "apps-data", appmanifest.BarkWalletID)
	legacyRoot := filepath.Join(root, "apps", appmanifest.BarkWalletID)
	tlsDir := filepath.Join(snapshot, appmanifest.BarkWalletTLSDir)
	return &NativeBarkWalletManager{Runner: runner, Paths: BarkWalletPaths{
		SnapshotRoot: snapshot, WalletDir: filepath.Join(dataRoot, "wallet"), AuthDir: filepath.Join(dataRoot, "auth"),
		AdminPasswordPath: filepath.Join(dataRoot, "auth", "ui_password"),
		SessionSecretPath: filepath.Join(dataRoot, "auth", "ui_session_secret"),
		ComposePath:       filepath.Join(snapshot, appmanifest.BarkWalletComposeFile),
		CaddyfilePath:     filepath.Join(snapshot, appmanifest.BarkWalletCaddyfile), TLSDir: tlsDir,
		TLSCertificate: filepath.Join(tlsDir, "server.crt"), TLSPrivateKey: filepath.Join(tlsDir, "server.key"),
		LegacyComposePath: filepath.Join(legacyRoot, appmanifest.BarkWalletComposeFile),
		LegacyTLSCert:     filepath.Join(legacyRoot, appmanifest.BarkWalletTLSDir, "server.crt"),
		LegacyTLSKey:      filepath.Join(legacyRoot, appmanifest.BarkWalletTLSDir, "server.key"),
	}}
}

func seedLegacyBarkWallet(t *testing.T, manager *NativeBarkWalletManager) (walletHash, passwordHash, sessionHash [32]byte, certificate, privateKey []byte) {
	t.Helper()
	for _, directory := range []string{manager.Paths.WalletDir, manager.Paths.AuthDir, filepath.Dir(manager.Paths.LegacyComposePath), filepath.Dir(manager.Paths.LegacyTLSCert)} {
		if err := os.MkdirAll(directory, 0750); err != nil {
			t.Fatal(err)
		}
	}
	wallet := []byte("existing-wallet-and-seed-state")
	password := []byte("Existing_Bark_Password_123\n")
	session := []byte("Existing_Bark_Session_Secret_456\n")
	if err := os.WriteFile(filepath.Join(manager.Paths.WalletDir, "wallet.sqlite"), wallet, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.AdminPasswordPath, password, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.SessionSecretPath, session, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.LegacyComposePath, []byte("legacy compose\n"), 0600); err != nil {
		t.Fatal(err)
	}
	certificate, privateKey, err := generateBarkWalletTLS()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.LegacyTLSCert, certificate, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.LegacyTLSKey, privateKey, 0600); err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(wallet), sha256.Sum256(password), sha256.Sum256(session), certificate, privateKey
}

func TestNativeBarkWalletEnsureMigratesWithoutChangingWalletOrSecrets(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := testBarkWalletManager(t, runner)
	walletHash, passwordHash, sessionHash, certificate, privateKey := seedLegacyBarkWallet(t, manager)
	state, err := manager.Ensure(context.Background(), false)
	if err != nil || !state.Installed || !state.PasswordAvailable {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	assertBarkWalletFileHash(t, filepath.Join(manager.Paths.WalletDir, "wallet.sqlite"), walletHash)
	assertBarkWalletFileHash(t, manager.Paths.AdminPasswordPath, passwordHash)
	assertBarkWalletFileHash(t, manager.Paths.SessionSecretPath, sessionHash)
	if got, _ := os.ReadFile(manager.Paths.TLSCertificate); !reflect.DeepEqual(got, certificate) {
		t.Fatal("legacy TLS certificate was not preserved")
	}
	if got, _ := os.ReadFile(manager.Paths.TLSPrivateKey); !reflect.DeepEqual(got, privateKey) {
		t.Fatal("legacy TLS private key was not preserved")
	}
	compose, _ := os.ReadFile(manager.Paths.ComposePath)
	expected, _ := appmanifest.BarkWalletCompose(manager.composePaths())
	if string(compose) != expected {
		t.Fatal("broker snapshot does not match Bark catalog")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("ensure unexpectedly executed a command: %#v", runner.commands)
	}
}

func TestNativeBarkWalletEnsureFreshCreatesBrokerOwnedMaterial(t *testing.T) {
	manager := testBarkWalletManager(t, &composeRecordingRunner{})
	state, err := manager.Ensure(context.Background(), false)
	if err != nil || !state.Installed || !state.PasswordAvailable {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	password, err := manager.ReadPassword()
	if err != nil || len(password) < 24 {
		t.Fatalf("generated password is unavailable: length=%d err=%v", len(password), err)
	}
	if _, err := readBarkWalletSecret(manager.Paths.SessionSecretPath); err != nil {
		t.Fatalf("generated session secret is unavailable: %v", err)
	}
	certificate, certificateErr := readRegularFile(manager.Paths.TLSCertificate, 64*1024)
	privateKey, privateKeyErr := readRegularFile(manager.Paths.TLSPrivateKey, 64*1024)
	if certificateErr != nil || privateKeyErr != nil || validateTLSKeyPair(certificate, privateKey) != nil {
		t.Fatalf("generated TLS pair is invalid: %v/%v", certificateErr, privateKeyErr)
	}
}

func TestNativeBarkWalletRemovePreservesWalletAndAuthentication(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := testBarkWalletManager(t, runner)
	walletHash, passwordHash, sessionHash, _, _ := seedLegacyBarkWallet(t, manager)
	if _, err := manager.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	assertBarkWalletFileHash(t, filepath.Join(manager.Paths.WalletDir, "wallet.sqlite"), walletHash)
	assertBarkWalletFileHash(t, manager.Paths.AdminPasswordPath, passwordHash)
	assertBarkWalletFileHash(t, manager.Paths.SessionSecretPath, sessionHash)
	if _, err := os.Stat(manager.Paths.SnapshotRoot); !os.IsNotExist(err) {
		t.Fatal("Bark execution snapshot was not removed")
	}
}

func TestNativeBarkWalletLifecycleRejectsTamperedSnapshotBeforeDocker(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := testBarkWalletManager(t, runner)
	seedLegacyBarkWallet(t, manager)
	if _, err := manager.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.ComposePath, []byte("services: {evil: {privileged: true}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Lifecycle(context.Background(), AppLifecycleStart, false); err == nil {
		t.Fatal("tampered Bark snapshot accepted")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("tampered snapshot reached Docker: %#v", runner.commands)
	}
}

func TestNativeBarkWalletEnsureRejectsSymlinkedWalletState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics require POSIX")
	}
	manager := testBarkWalletManager(t, &composeRecordingRunner{})
	seedLegacyBarkWallet(t, manager)
	if err := os.Symlink("/etc/passwd", filepath.Join(manager.Paths.WalletDir, "seed-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Ensure(context.Background(), false); err == nil {
		t.Fatal("symlinked Bark wallet entry accepted")
	}
}

func TestNativeBarkWalletPasswordResetPreservesSessionSecret(t *testing.T) {
	manager := testBarkWalletManager(t, &composeRecordingRunner{})
	_, _, sessionHash, _, _ := seedLegacyBarkWallet(t, manager)
	if _, err := manager.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	before, err := manager.ReadPassword()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ResetPassword(false); err != nil {
		t.Fatal(err)
	}
	after, err := manager.ReadPassword()
	if err != nil || after == before {
		t.Fatalf("password was not replaced: before=%q after=%q err=%v", before, after, err)
	}
	assertBarkWalletFileHash(t, manager.Paths.SessionSecretPath, sessionHash)
	stable := sha256.Sum256(mustReadTestFile(t, manager.Paths.AdminPasswordPath))
	if _, err := manager.ResetPassword(true); err != nil {
		t.Fatal(err)
	}
	assertBarkWalletFileHash(t, manager.Paths.AdminPasswordPath, stable)
}

func TestNativeBarkWalletFirewallUsesOnlyFixedCatalogPort(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == ufwPath && reflect.DeepEqual(args, []string{"status"}) {
			return "Status: active", nil, true
		}
		return "", nil, false
	}}
	manager := testBarkWalletManager(t, runner)
	state, err := manager.EnsureFirewall(context.Background(), false)
	if err != nil || !state.UFWActive {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	want := [][]string{{"status"}, {"allow", "4004/tcp"}}
	if len(runner.commands) != len(want) {
		t.Fatalf("commands=%#v", runner.commands)
	}
	for index := range want {
		if !reflect.DeepEqual(runner.commands[index].args, want[index]) {
			t.Fatalf("command[%d]=%#v", index, runner.commands[index])
		}
	}
}

func TestNativeBarkWalletLegacyRemoveFailsClosedOnAmbiguousContainer(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && len(args) > 1 && args[0] == "ps" {
			return "0123456789ab\nabcdef012345\n", nil, true
		}
		return "", nil, false
	}}
	manager := testBarkWalletManager(t, runner)
	if err := os.MkdirAll(filepath.Dir(manager.Paths.LegacyComposePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.LegacyComposePath, []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), false); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected fail closed, got %v", err)
	}
}

func TestNativeBarkWalletInvalidLegacyTLSFailsClosed(t *testing.T) {
	manager := testBarkWalletManager(t, &composeRecordingRunner{})
	seedLegacyBarkWallet(t, manager)
	if err := os.WriteFile(manager.Paths.LegacyTLSKey, []byte("not a private key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Ensure(context.Background(), false); err == nil {
		t.Fatal("invalid legacy TLS was silently replaced")
	}
}

func assertBarkWalletFileHash(t *testing.T, path string, want [32]byte) {
	t.Helper()
	got := sha256.Sum256(mustReadTestFile(t, path))
	if got != want {
		t.Fatalf("content changed for %s", path)
	}
}

func mustReadTestFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
