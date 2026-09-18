package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestLndgBuildUsesCompatiblePinnedUpstream(t *testing.T) {
	dockerfile := appmanifest.LNDgDockerfile()
	if !strings.Contains(dockerfile, "FROM python:3.12-slim") {
		t.Fatal("LNDg Django 6 build must use Python 3.12")
	}
	if len(appmanifest.LNDgSourceCommit) != 40 {
		t.Fatalf("LNDg source commit must be pinned, got %q", appmanifest.LNDgSourceCommit)
	}
	if strings.Contains(lndgComposeContents(lndgAppPaths()), "build:") {
		t.Fatal("LNDg runtime declaration must not retain a manager-owned build path")
	}
}

func TestLNDgPostgresBackedLNDUsesPrivateChannelDBPlaceholder(t *testing.T) {
	paths := lndgPaths{ChannelDBPath: "/var/lib/lightningos/apps-data/lndg/lnd/channel.db"}
	if got := lndgChannelDBSource(paths); got != paths.ChannelDBPath {
		t.Fatalf("channel DB source=%q want=%q", got, paths.ChannelDBPath)
	}
}

func TestLNDgAccessHostComesFromAuthenticatedManagerRequest(t *testing.T) {
	ctx := withLNDgAccessHost(context.Background(), "192.168.68.92:8443")
	dynamic := mergeLNDgAccessHosts(
		[]string{"localhost", "127.0.0.1", "host.docker.internal", "100.101.102.103", "*", "invalid"},
		lndgAccessHost(ctx),
	)
	hosts, origins := lndgHosts(dynamic)
	if strings.Contains(strings.Join(hosts, ","), "*") {
		t.Fatal("LNDg allowed hosts must remain closed")
	}
	for _, required := range []string{"192.168.68.92", "100.101.102.103", "http://192.168.68.92:8889", "https://100.101.102.103:8889"} {
		if !strings.Contains(strings.Join(append(hosts, origins...), ","), required) {
			t.Fatalf("LNDg access list is missing %q", required)
		}
	}
}

func TestReconcileLNDgCatalogDeclarationUpdatesOnlyStaticAssets(t *testing.T) {
	root := t.TempDir()
	paths := lndgPaths{
		Root:           root,
		DataDir:        filepath.Join(root, "data"),
		PgDir:          filepath.Join(root, "pgdata"),
		LndDir:         filepath.Join(root, "lnd"),
		LogPath:        filepath.Join(root, "data", "lndg-controller.log"),
		ComposePath:    filepath.Join(root, appmanifest.LNDgComposeFile),
		EntrypointPath: filepath.Join(root, appmanifest.LNDgEntrypointFile),
		ChannelDBPath:  filepath.Join(root, "lnd", appmanifest.LNDgChannelDBFile),
		EnvPath:        filepath.Join(root, appmanifest.LNDgEnvFile),
	}
	if err := os.WriteFile(paths.ComposePath, []byte("old compose\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.EntrypointPath, []byte("old entrypoint\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.EnvPath, []byte("SECRET=preserved\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := reconcileLndgCatalogDeclaration(paths); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(paths.EntrypointPath); err != nil || string(raw) != lndgEntrypoint {
		t.Fatalf("entrypoint was not reconciled: %v", err)
	}
	if raw, err := os.ReadFile(paths.ComposePath); err != nil || string(raw) != lndgComposeContents(paths) {
		t.Fatalf("compose was not reconciled: %v", err)
	}
	if raw, err := os.ReadFile(paths.EnvPath); err != nil || string(raw) != "SECRET=preserved\n" {
		t.Fatalf("environment changed during static reconciliation: %q/%v", raw, err)
	}
}

func TestReconcileLNDgCatalogDeclarationRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("must remain\n"), 0640); err != nil {
		t.Fatal(err)
	}
	paths := lndgPaths{
		ComposePath:    filepath.Join(root, appmanifest.LNDgComposeFile),
		EntrypointPath: filepath.Join(root, appmanifest.LNDgEntrypointFile),
	}
	if err := os.WriteFile(paths.ComposePath, []byte("old compose\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, paths.EntrypointPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := reconcileLndgCatalogDeclaration(paths); err == nil {
		t.Fatal("expected symlink declaration to be rejected")
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "must remain\n" {
		t.Fatalf("symlink target changed: %q/%v", raw, err)
	}
}
