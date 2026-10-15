package privileged

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

// Exercises the real file replacement helper, not running systemd services.
// On Linux run as root in a disposable lab with the fixed service identities.
func TestNativePeerSwapBundleReplacesOldBinariesAndPreservesState(t *testing.T) {
	if runtime.GOOS == "linux" {
		if os.Geteuid() != 0 {
			t.Skip("requires root for fixed PeerSwap file ownership")
		}
		if _, err := user.Lookup(appmanifest.PeerSwapUser); err != nil {
			t.Skip("requires the PeerSwap service user in a disposable Linux lab")
		}
		if _, err := user.LookupGroup(appmanifest.PeerSwapManagerGroup); err != nil {
			t.Skip("requires the LightningOS group in a disposable Linux lab")
		}
	}
	root := t.TempDir()
	paths := appmanifest.PeerSwapPathsForRoots(filepath.Join(root, "apps"), filepath.Join(root, "apps-data"), filepath.Join(root, "systemd"))
	for _, dir := range []string{paths.BinDir, paths.LNDDir} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	preserved := map[string]string{
		paths.ConfigPath: "operator-config", paths.PSWebConfigPath: "operator-web-config",
		paths.ElementsSourcePath: "remote-elements-policy", paths.LNDMacaroonPath: "dedicated-credential",
		paths.LNDTLSCertPath: "tls-certificate", filepath.Join(paths.RuntimeDir, "swaps.db"): "existing-swap-state",
	}
	for path, content := range preserved {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, asset := range appmanifest.PeerSwapBinaries() {
		if err := os.WriteFile(filepath.Join(paths.BinDir, asset.Name), []byte("older-bundle-"+asset.Name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(paths.VersionPath, []byte("older-version"), 0640); err != nil {
		t.Fatal(err)
	}
	manager := &NativePeerSwapManager{Paths: paths, StagedAssetsRoot: filepath.Join("..", "..", "assets", "binaries", "peerswap", appmanifest.PeerSwapAssetDirectory, appmanifest.PeerSwapAssetArch)}
	for attempt := 0; attempt < 2; attempt++ {
		if err := manager.verifyStagedBinaries(); err != nil {
			t.Fatal(err)
		}
		if err := manager.installBinaries(); err != nil {
			t.Fatal(err)
		}
		for _, asset := range appmanifest.PeerSwapBinaries() {
			raw, err := os.ReadFile(filepath.Join(paths.BinDir, asset.Name))
			if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != asset.SHA256 {
				t.Fatalf("installed %s does not match bundle: %v", asset.Name, err)
			}
		}
		if raw, err := os.ReadFile(paths.VersionPath); err != nil || string(raw) != appmanifest.PeerSwapVersionMarker()+"\n" {
			t.Fatalf("incorrect installed version marker: %q (%v)", raw, err)
		}
		for path, content := range preserved {
			if raw, err := os.ReadFile(path); err != nil || string(raw) != content {
				t.Fatalf("bundle replacement modified %s: %v", filepath.Base(path), err)
			}
		}
	}
}

func TestNativePeerSwapRejectsMissingAndUnpinnedStagedBundle(t *testing.T) {
	manager := &NativePeerSwapManager{StagedAssetsRoot: t.TempDir()}
	if err := manager.verifyStagedBinaries(); err == nil {
		t.Fatal("missing bundle accepted")
	}
	if err := os.WriteFile(filepath.Join(manager.StagedAssetsRoot, "peerswapd"), []byte("unapproved-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := manager.verifyStagedBinaries(); err == nil {
		t.Fatal("unapproved bundle accepted")
	}
}

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

func TestNativePeerSwapStopDoesNotRequireElementsOrCredentials(t *testing.T) {
	runner := &composeRecordingRunner{}
	paths := appmanifest.DefaultPeerSwapPaths()
	paths.PeerswapdPath = filepath.Join(t.TempDir(), "missing-daemon")
	paths.PSCLIPath = paths.PeerswapdPath
	paths.PSWebPath = paths.PeerswapdPath
	paths.LNDMacaroonPath = paths.PeerswapdPath
	paths.DataRoot = filepath.Join(t.TempDir(), "missing-source")
	manager := &NativePeerSwapManager{Runner: runner, Paths: paths}
	if _, err := manager.Lifecycle(context.Background(), AppLifecycleStop, false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("stop touched dependencies: %#v", runner.commands)
	}
	for i, unit := range []string{appmanifest.PeerSwapWebService, appmanifest.PeerSwapService} {
		c := runner.commands[i]
		if c.path != systemctlPath || strings.Join(c.args, " ") != "disable --now "+unit {
			t.Fatalf("unexpected command: %#v", c)
		}
	}
	runner.commands = nil
	if _, err := manager.Lifecycle(context.Background(), AppLifecycleStart, false); err == nil {
		t.Fatal("start bypassed readiness")
	}
	if len(runner.commands) != 0 {
		t.Fatal("unsafe start executed")
	}
}
