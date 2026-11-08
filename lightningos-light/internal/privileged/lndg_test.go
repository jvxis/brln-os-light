package privileged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestLNDgImageBuildScriptPinsAndVerifiesSource(t *testing.T) {
	script := lndgImageBuildScript("/var/lib/lightningos-privileged/apps/lndg/image-attestation")
	for _, required := range []string{
		appmanifest.LNDgSourceURL,
		appmanifest.LNDgSourceSHA256,
		appmanifest.LNDgSourceDir,
		appmanifest.LNDgImage,
		"sha256sum --check --strict",
		"--no-same-owner --no-same-permissions",
		appmanifest.LNDgDockerfileSHA256(),
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("build script missing %q", required)
		}
	}
	for _, forbidden := range []string{"git clone", "git fetch", "checkout master"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("build script contains mutable source action %q", forbidden)
		}
	}
}

func TestLNDgImageBuildHasBoundedSlowNodeBudget(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && len(args) > 1 && args[0] == "image" && args[1] == "inspect" {
			return "", errors.New("image absent"), true
		}
		if path == systemctlPath {
			return "LoadState=not-found\n", errors.New("not found"), true
		}
		return "", nil, false
	}}
	m := &ComposeAppManager{Runner: runner, PrivilegedAppsRoot: t.TempDir()}
	state, err := m.PrepareImage(context.Background(), appmanifest.LNDgID, appmanifest.LNDgImageApp, false)
	if err != nil || state.Status != "preparing" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	found := false
	for _, cmd := range runner.commands {
		if strings.Contains(strings.Join(cmd.args, " "), "--unit=lightningos-lndg-image-app") {
			found = true
			if !strings.Contains(strings.Join(cmd.args, " "), "--property=RuntimeMaxSec=30min") {
				t.Fatal("LNDg build must have a bounded 30-minute budget")
			}
		}
	}
	if !found {
		t.Fatal("build was not scheduled")
	}
	state, err = m.PrepareImage(context.Background(), appmanifest.LNDgID, appmanifest.LNDgImagePostgres, false)
	if err != nil || state.Status != "preparing" {
		t.Fatalf("postgres state/error=%#v/%v", state, err)
	}
	last := runner.commands[len(runner.commands)-1]
	if last.path != systemdRunPath || !strings.Contains(strings.Join(last.args, " "), "--property=RuntimeMaxSec=10min") {
		t.Fatal("registry pull budget must remain unchanged")
	}
}

func TestLNDgImageStatusRequiresMatchingAttestationAndImageID(t *testing.T) {
	root := t.TempDir()
	attestationPath := filepath.Join(root, appmanifest.LNDgID, lndgImageAttestationFile)
	if err := os.MkdirAll(filepath.Dir(attestationPath), 0700); err != nil {
		t.Fatal(err)
	}
	imageID := "sha256:" + strings.Repeat("a", 64)
	raw := "image_id=" + imageID + "\n" +
		"release=" + appmanifest.LNDgRelease + "\n" +
		"commit=" + appmanifest.LNDgSourceCommit + "\n" +
		"source_sha256=" + appmanifest.LNDgSourceSHA256 + "\n" +
		"base_image=" + appmanifest.LNDgBaseImage + "\n"
	raw += "dockerfile_sha256=" + appmanifest.LNDgDockerfileSHA256() + "\n"
	if err := os.WriteFile(attestationPath, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && len(args) == 5 && args[0] == "image" && args[1] == "inspect" && args[4] == appmanifest.LNDgImage {
			return imageID + "\n", nil, true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner, PrivilegedAppsRoot: root}
	state, err := manager.ImageStatus(context.Background(), appmanifest.LNDgID, appmanifest.LNDgImageApp)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}

	if err := os.WriteFile(attestationPath, []byte(strings.Replace(raw, appmanifest.LNDgSourceSHA256, strings.Repeat("0", 64), 1)), 0600); err != nil {
		t.Fatal(err)
	}
	runner.hook = func(path string, args []string) (string, error, bool) {
		if path == systemctlPath {
			return "LoadState=not-found\n", errors.New("not found"), true
		}
		return "", nil, false
	}
	state, err = manager.ImageStatus(context.Background(), appmanifest.LNDgID, appmanifest.LNDgImageApp)
	if err != nil || state.Status != "absent" {
		t.Fatalf("mismatched attestation state/error=%#v/%v", state, err)
	}
}
