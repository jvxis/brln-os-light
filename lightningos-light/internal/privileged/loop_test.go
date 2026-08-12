package privileged

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

type loopRecordingRunner struct {
	commands []recordedCommand
	status   string
}

func (runner *loopRecordingRunner) Run(_ context.Context, path string, args ...string) (string, error) {
	runner.commands = append(runner.commands, recordedCommand{path: path, args: append([]string(nil), args...)})
	if path == systemctlPath && len(args) > 0 && args[0] == "show" {
		if runner.status == "" {
			return "ActiveState=active\nSubState=running\n", nil
		}
		return runner.status, nil
	}
	return "", nil
}

func newLoopManagerFixture(t *testing.T) (*NativeLoopManager, *loopRecordingRunner) {
	t.Helper()
	stateRoot := t.TempDir()
	appsRoot := filepath.Join(stateRoot, "apps")
	dataRoot := filepath.Join(stateRoot, "apps-data")
	systemdRoot := filepath.Join(stateRoot, "systemd")
	for _, dir := range []string{appsRoot, dataRoot, systemdRoot} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	runner := &loopRecordingRunner{}
	manager := &NativeLoopManager{
		Runner:       runner,
		Paths:        appmanifest.LoopPathsForRoots(appsRoot, dataRoot, systemdRoot),
		StateRoot:    stateRoot,
		AppsRoot:     appsRoot,
		AppsDataRoot: dataRoot,
		TempRoot:     stateRoot,
		GOARCH:       "amd64",
	}
	return manager, runner
}

func TestNativeLoopEnsureUsesOnlyClosedManifest(t *testing.T) {
	manager, runner := newLoopManagerFixture(t)
	for _, dir := range []string{manager.Paths.Root, manager.Paths.BinDir, manager.Paths.ClientDir, manager.Paths.DataDir, manager.Paths.LNDDir} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	for path, raw := range map[string][]byte{
		manager.Paths.LoopdPath:   []byte("loopd"),
		manager.Paths.LoopCLIPath: []byte("loop"),
		manager.Paths.VersionPath: []byte(appmanifest.LoopVersion + "\n"),
	} {
		if err := os.WriteFile(path, raw, 0755); err != nil {
			t.Fatal(err)
		}
	}
	state, err := manager.Ensure(context.Background(), LoopEnsureParams{
		LNDTLSCertificate: []byte("certificate"),
		LNDMacaroon:       []byte("dedicated-macaroon"),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Installed || state.Status != "running" {
		t.Fatalf("unexpected Loop state: %+v", state)
	}
	for path, wantMode := range map[string]os.FileMode{
		manager.Paths.ConfigPath:      0640,
		manager.Paths.LNDTLSCertPath:  0640,
		manager.Paths.LNDMacaroonPath: 0600,
		manager.Paths.ServicePath:     0644,
	} {
		info, err := os.Lstat(path)
		modeWrong := runtime.GOOS != "windows" && info != nil && info.Mode().Perm() != wantMode
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || modeWrong {
			t.Fatalf("unsafe Loop output %s: %v, %v", path, info, err)
		}
	}
	for _, command := range runner.commands {
		joined := command.path + " " + strings.Join(command.args, " ")
		for _, forbidden := range []string{"/bin/sh", "docker", "bitcoin", "lnd restart", "postgres", "0.0.0.0"} {
			if strings.Contains(strings.ToLower(joined), strings.ToLower(forbidden)) {
				t.Fatalf("out-of-scope Loop command: %s", joined)
			}
		}
	}
}

func TestNativeLoopClientMaterialCopiesWithoutExposingDaemonFiles(t *testing.T) {
	manager, _ := newLoopManagerFixture(t)
	for _, dir := range []string{manager.Paths.Root, manager.Paths.ClientDir, manager.Paths.DataDir} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(manager.Paths.LoopTLSCert, []byte("tls"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(manager.Paths.LoopMacaroon), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.LoopMacaroon, []byte("mac"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.EnsureClientMaterial(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{manager.Paths.ClientTLSCert, manager.Paths.ClientMacaroon} {
		info, err := os.Lstat(path)
		modeWrong := runtime.GOOS != "windows" && info != nil && info.Mode().Perm() != 0640
		if err != nil || modeWrong {
			t.Fatalf("unsafe Loop client material %s: %v, %v", path, info, err)
		}
	}
}

func TestNativeLoopRejectsSymlinkedAppTree(t *testing.T) {
	manager, _ := newLoopManagerFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, manager.Paths.Root); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := manager.EnsurePermissions(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "unsafe Lightning Loop directory") {
		t.Fatalf("symlinked Loop root accepted: %v", err)
	}
}

func TestNativeLoopDryRunDoesNotMutate(t *testing.T) {
	manager, runner := newLoopManagerFixture(t)
	state, err := manager.Ensure(context.Background(), LoopEnsureParams{LNDTLSCertificate: []byte("cert")}, true)
	if err != nil || state.Status != "validated" {
		t.Fatalf("unexpected dry-run result: %+v, %v", state, err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("dry run executed commands: %#v", runner.commands)
	}
	if _, err := os.Lstat(manager.Paths.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created app root: %v", err)
	}
}

func TestExtractLoopBinariesAcceptsOnlyBoundedRegularFiles(t *testing.T) {
	valid := loopArchiveFixture(t, map[string][]byte{
		"loop-linux-amd64/loop":  []byte("loop"),
		"loop-linux-amd64/loopd": []byte("loopd"),
	}, nil)
	binaries, err := extractLoopBinaries(valid)
	if err != nil || string(binaries["loop"]) != "loop" || string(binaries["loopd"]) != "loopd" {
		t.Fatalf("valid Loop archive rejected: %#v, %v", binaries, err)
	}

	symlink := loopArchiveFixture(t, map[string][]byte{"loop-linux-amd64/loop": []byte("loop")}, []tar.Header{{
		Name: "loop-linux-amd64/loopd", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh", Mode: 0777,
	}})
	if _, err := extractLoopBinaries(symlink); err == nil {
		t.Fatal("symlinked Loop binary accepted")
	}
}

func loopArchiveFixture(t *testing.T, files map[string][]byte, extra []tar.Header) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, raw := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(raw)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, header := range extra {
		if err := tarWriter.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
