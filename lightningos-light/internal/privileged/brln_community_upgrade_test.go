package privileged

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

// Pins independently copied from the published 0.5.29-Beta catalog. Keep this
// fixture independent of the production compatibility matcher.
const communityPreviousWeb = "ghcr.io/jvxis/brln-community-web:0.1.4@sha256:3aec012bfdb29f3e4cf6f7626b25333a8505844c56decb6d522ffbc211cd68d6"
const communityPreviousSigner = "ghcr.io/jvxis/brln-signer:0.1.4@sha256:c1c5b282cd0fa5ea5720f7c021a5fa32da6c7a00b1e45552193d207c89a3b2bc"

func communityPreviousInstall(t *testing.T, runner *composeRecordingRunner) *NativeBRLNCommunityManager {
	t.Helper()
	manager := testBRLNCommunityManager(t, runner)
	if _, err := manager.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	raw := string(mustReadTestFile(t, manager.Paths.ComposePath))
	raw = strings.ReplaceAll(raw, appmanifest.BRLNCommunityWebImage, communityPreviousWeb)
	raw = strings.ReplaceAll(raw, appmanifest.BRLNCommunitySignerImage, communityPreviousSigner)
	if err := os.WriteFile(manager.Paths.ComposePath, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return manager
}

func communityPersistentHashes(t *testing.T, manager *NativeBRLNCommunityManager) map[string][32]byte {
	t.Helper()
	identity := filepath.Join(manager.Paths.SignerDir, "key.ncryptsec")
	if err := os.WriteFile(identity, []byte("ncryptsec1-existing-member\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result := make(map[string][32]byte)
	for _, path := range []string{identity, manager.Paths.KeyPasswordPath, manager.Paths.AccessPasswordPath,
		manager.Paths.TLSCertificate, manager.Paths.TLSPrivateKey} {
		result[path] = sha256.Sum256(mustReadTestFile(t, path))
	}
	return result
}

func TestNativeBRLNCommunityUpgradeStopThenStart(t *testing.T) {
	runner := catalogStopRunner(t, appmanifest.BRLNCommunityID, "proxy", "web", "signer")
	manager := communityPreviousInstall(t, runner)
	preserved := communityPersistentHashes(t, manager)
	oldCompose := mustReadTestFile(t, manager.Paths.ComposePath)
	ctx := context.Background()

	// A stale snapshot must never launch the old pinned images.
	if _, err := manager.Lifecycle(ctx, AppLifecycleStart, false); err == nil {
		t.Fatal("old snapshot was accepted for start")
	}
	if len(runner.commands) != 0 {
		t.Fatal("rejected start reached Docker")
	}
	state, err := manager.Lifecycle(ctx, AppLifecycleStop, true)
	if err != nil || state.Status != "validated" || len(runner.commands) != 0 {
		t.Fatalf("stop dry run failed or executed commands: %v", err)
	}
	state, err = manager.Lifecycle(ctx, AppLifecycleStop, false)
	if err != nil || state.Status != "stopped" {
		t.Fatalf("stop failed: %v", err)
	}
	assertCatalogStopOnly(t, runner, 3)
	if !reflect.DeepEqual(oldCompose, mustReadTestFile(t, manager.Paths.ComposePath)) {
		t.Fatal("stop rewrote the installed snapshot")
	}
	password, err := manager.ReadPassword()
	if err != nil || password == "" {
		t.Fatalf("existing access password unavailable: %v", err)
	}

	// The normal server Start path prepares the current catalog before starting.
	if _, err := manager.Ensure(ctx, false); err != nil {
		t.Fatal(err)
	}
	runner.commands = nil
	if _, err := manager.Lifecycle(ctx, AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	current, err := appmanifest.BRLNCommunityCompose(manager.composePaths())
	if err != nil || runner.composeSnapshot != current {
		t.Fatal("start did not use the current catalog")
	}
	for i, image := range appmanifest.BRLNCommunityImages() {
		if !reflect.DeepEqual(runner.commands[i].args, []string{"image", "inspect", image}) {
			t.Fatal("start did not inspect the current pinned images")
		}
	}
	if !hasArgsSuffix(runner.commands[len(runner.commands)-2].args, "up", "-d") {
		t.Fatal("start did not recreate services through compose up")
	}
	if _, err := manager.Lifecycle(ctx, AppLifecycleStop, false); err != nil {
		t.Fatalf("current catalog stop regressed: %v", err)
	}
	for path, hash := range preserved {
		assertBarkWalletFileHash(t, path, hash)
	}
}

func TestNativeBRLNCommunityPreviousReleaseRemovePreservesIdentity(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := communityPreviousInstall(t, runner)
	preserved := communityPersistentHashes(t, manager)
	if err := manager.Remove(context.Background(), true); err != nil || len(runner.commands) != 0 {
		t.Fatalf("remove dry run failed or executed commands: %v", err)
	}
	if err := manager.Remove(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || !hasArgsSuffix(runner.commands[1].args, "down", "--remove-orphans", "--timeout", "30") {
		t.Fatalf("unexpected remove commands: %#v", runner.commands)
	}
	for _, path := range []string{filepath.Join(manager.Paths.SignerDir, "key.ncryptsec"), manager.Paths.KeyPasswordPath, manager.Paths.AccessPasswordPath} {
		assertBarkWalletFileHash(t, path, preserved[path])
	}
	if _, err := os.Stat(manager.Paths.SnapshotRoot); !os.IsNotExist(err) {
		t.Fatal("execution snapshot was not removed")
	}
}

func TestNativeBRLNCommunityPreviousReleaseRejectsTampering(t *testing.T) {
	for name, change := range map[string]func(*testing.T, *NativeBRLNCommunityManager){
		"unknown image": func(t *testing.T, m *NativeBRLNCommunityManager) {
			communityReplaceFile(t, m.Paths.ComposePath, communityPreviousWeb, "attacker/web:latest")
		},
		"unknown digest": func(t *testing.T, m *NativeBRLNCommunityManager) {
			communityReplaceFile(t, m.Paths.ComposePath, "sha256:3aec", "sha256:0000")
		},
		"mixed releases": func(t *testing.T, m *NativeBRLNCommunityManager) {
			communityReplaceFile(t, m.Paths.ComposePath, communityPreviousSigner, appmanifest.BRLNCommunitySignerImage)
		},
		"extra mount": func(t *testing.T, m *NativeBRLNCommunityManager) {
			communityReplaceFile(t, m.Paths.ComposePath, "    volumes:", "    volumes:\n      - /:/host:rw")
		},
		"privileges": func(t *testing.T, m *NativeBRLNCommunityManager) {
			communityReplaceFile(t, m.Paths.ComposePath, "    read_only: true", "    privileged: true")
		},
		"proxy auth": func(t *testing.T, m *NativeBRLNCommunityManager) {
			communityReplaceFile(t, m.Paths.CaddyfilePath, "forward_auth", "# forward_auth")
		},
		"extra entry": func(t *testing.T, m *NativeBRLNCommunityManager) {
			if err := os.WriteFile(filepath.Join(m.Paths.SnapshotRoot, "override.yaml"), []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &composeRecordingRunner{}
			manager := communityPreviousInstall(t, runner)
			change(t, manager)
			for _, dryRun := range []bool{true, false} {
				if _, err := manager.Lifecycle(context.Background(), AppLifecycleStart, dryRun); err == nil {
					t.Fatal("tampered start accepted")
				}
				if err := manager.Remove(context.Background(), dryRun); err == nil {
					t.Fatal("tampered removal accepted")
				}
			}
			if _, err := manager.ReadPassword(); err == nil {
				t.Fatal("password disclosed from tampered snapshot")
			}
			if len(runner.commands) != 0 {
				t.Fatal("tampered snapshot reached Docker")
			}
		})
	}
}

func communityReplaceFile(t *testing.T, path, old, replacement string) {
	t.Helper()
	raw := string(mustReadTestFile(t, path))
	if !strings.Contains(raw, old) {
		t.Fatal("fixture replacement did not match")
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(raw, old, replacement)), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeBRLNCommunityPreviousReleaseStopFailurePreservesState(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(_ string, args []string) (string, error, bool) {
		if len(args) > 0 && args[0] == "ps" {
			return "", errors.New("docker unavailable"), true
		}
		return "", nil, false
	}}
	manager := communityPreviousInstall(t, runner)
	preserved := communityPersistentHashes(t, manager)
	preserved[manager.Paths.ComposePath] = sha256.Sum256(mustReadTestFile(t, manager.Paths.ComposePath))
	if _, err := manager.Lifecycle(context.Background(), AppLifecycleStop, false); err == nil {
		t.Fatal("Docker failure was hidden")
	}
	for path, hash := range preserved {
		assertBarkWalletFileHash(t, path, hash)
	}
}
