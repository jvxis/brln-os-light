package privileged

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestNativePeerSwapSourceNormalizesLegacyLocalPolicyAtFixedPath(t *testing.T) {
	stateRoot := t.TempDir()
	appsRoot := filepath.Join(stateRoot, "apps")
	appsDataRoot := filepath.Join(stateRoot, "apps-data")
	paths := appmanifest.PeerSwapPathsForRoots(appsRoot, appsDataRoot, filepath.Join(stateRoot, "systemd"))
	if err := os.MkdirAll(paths.DataRoot, 0751); err != nil {
		t.Fatal(err)
	}
	// Older installations omitted mode and wallet because both defaulted to
	// the store-managed local Elements instance.
	if err := os.WriteFile(paths.ElementsSourcePath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := &NativePeerSwapManager{
		Runner:       &recordingRunner{},
		Paths:        paths,
		StateRoot:    stateRoot,
		AppsRoot:     appsRoot,
		AppsDataRoot: appsDataRoot,
	}
	state, err := manager.Source(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !state.Configured || state.Source.Mode != appmanifest.PeerSwapElementsModeLocal || state.Source.Wallet != "peerswap" {
		t.Fatalf("legacy source was not normalized: %#v", state)
	}
}

func TestNativePeerSwapWriteSourceUsesRootOnlyFixedPolicyFile(t *testing.T) {
	stateRoot := t.TempDir()
	appsRoot := filepath.Join(stateRoot, "apps")
	appsDataRoot := filepath.Join(stateRoot, "apps-data")
	if err := os.MkdirAll(appsDataRoot, 0751); err != nil {
		t.Fatal(err)
	}
	paths := appmanifest.PeerSwapPathsForRoots(appsRoot, appsDataRoot, filepath.Join(stateRoot, "systemd"))
	manager := &NativePeerSwapManager{Runner: &recordingRunner{}, Paths: paths, StateRoot: stateRoot, AppsRoot: appsRoot, AppsDataRoot: appsDataRoot}
	source := PeerSwapSource{Mode: appmanifest.PeerSwapElementsModeRemote, URL: "https://elements.example:7041", User: "rpc", Password: "secret", Wallet: "peerswap_node"}
	if err := manager.WriteSource(context.Background(), source, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(paths.ElementsSourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
		t.Fatalf("unexpected policy file mode: %v", info.Mode())
	}
	raw, err := os.ReadFile(paths.ElementsSourcePath)
	if err != nil {
		t.Fatal(err)
	}
	var stored PeerSwapSource
	if err := json.Unmarshal(raw, &stored); err != nil || stored != source {
		t.Fatalf("unexpected stored source: %#v (%v)", stored, err)
	}
}

func TestNativePeerSwapSourceRejectsSymlinkedPolicy(t *testing.T) {
	stateRoot := t.TempDir()
	appsRoot := filepath.Join(stateRoot, "apps")
	appsDataRoot := filepath.Join(stateRoot, "apps-data")
	paths := appmanifest.PeerSwapPathsForRoots(appsRoot, appsDataRoot, filepath.Join(stateRoot, "systemd"))
	if err := os.MkdirAll(paths.DataRoot, 0751); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(stateRoot, "outside.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, paths.ElementsSourcePath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	manager := &NativePeerSwapManager{Runner: &recordingRunner{}, Paths: paths, StateRoot: stateRoot, AppsRoot: appsRoot, AppsDataRoot: appsDataRoot}
	if _, err := manager.Source(context.Background()); err == nil {
		t.Fatal("symlinked PeerSwap policy was accepted")
	}
}

func TestCopyPeerSwapLegacyTreePreservesRegularStateAndRejectsLinks(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "swap.db"), []byte("state"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyPeerSwapLegacyTree(source, destination); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(destination, "nested", "swap.db")); err != nil || string(raw) != "state" {
		t.Fatalf("legacy state was not copied: %q (%v)", raw, err)
	}
	link := filepath.Join(source, "unsafe")
	if err := os.Symlink(filepath.Join(source, "nested", "swap.db"), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := copyPeerSwapLegacyTree(source, t.TempDir()); err == nil {
		t.Fatal("legacy symlink was accepted")
	}
}

func TestNativePeerSwapLifecycleRejectsArbitraryActionWithoutExecution(t *testing.T) {
	runner := &recordingRunner{}
	manager := &NativePeerSwapManager{Runner: runner, Paths: appmanifest.DefaultPeerSwapPaths()}
	if _, err := manager.Lifecycle(context.Background(), AppLifecycleAction("exec"), false); err == nil {
		t.Fatal("arbitrary lifecycle action accepted")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("rejected action executed commands: %#v", runner.commands)
	}
}
