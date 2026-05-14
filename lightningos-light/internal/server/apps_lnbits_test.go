package server

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLnbitsComposeUsesLatestImage(t *testing.T) {
	compose := lnbitsComposeContents(lnbitsPaths{DataDir: "/var/lib/lightningos/apps-data/lnbits/data"})

	if !strings.Contains(compose, "image: lnbits/lnbits:latest") {
		t.Fatalf("compose must use latest LNbits image\n%s", compose)
	}
}

func TestEnsureLnbitsEnvAllowsLocalHTTPAuth(t *testing.T) {
	paths := lnbitsPaths{EnvPath: filepath.Join(t.TempDir(), ".env")}

	if err := ensureLnbitsEnv(paths); err != nil {
		t.Fatalf("ensure env: %v", err)
	}
	content, err := os.ReadFile(paths.EnvPath)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !strings.Contains(string(content), "AUTH_HTTPS_ONLY=false\n") {
		t.Fatalf("env must allow local HTTP auth\n%s", string(content))
	}
}

func TestUpdateLndRestOptionsReplacesStaleManagedGateways(t *testing.T) {
	lines := []string{
		"[Application Options]",
		"tlsextraip=172.19.0.1",
		"tlsextradomain=host.docker.internal",
		"restlisten=172.19.0.1:8080",
		"tlsextraip=172.18.0.1",
		"tlsextradomain=host.docker.internal",
		"restlisten=172.18.0.1:8080",
		"# alias=LightningOS-Node",
		"color=#ff9900",
	}

	got, changed := updateLndRestOptions(lines, []string{"172.17.0.1"})
	want := []string{
		"[Application Options]",
		"tlsextraip=172.17.0.1",
		"tlsextradomain=host.docker.internal",
		"restlisten=172.17.0.1:8080",
		"# alias=LightningOS-Node",
		"color=#ff9900",
	}

	if !changed {
		t.Fatalf("expected change when stale gateway listeners exist")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected updated lines\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestUpdateLndRestOptionsPreservesWildcardRestlisten(t *testing.T) {
	lines := []string{
		"[Application Options]",
		"tlsextraip=172.19.0.1",
		"tlsextradomain=host.docker.internal",
		"restlisten=172.19.0.1:8080",
		"restlisten=0.0.0.0:8080",
		"# alias=LightningOS-Node",
	}

	got, changed := updateLndRestOptions(lines, []string{"172.17.0.1"})
	want := []string{
		"[Application Options]",
		"tlsextraip=172.17.0.1",
		"tlsextradomain=host.docker.internal",
		"restlisten=0.0.0.0:8080",
		"# alias=LightningOS-Node",
	}

	if !changed {
		t.Fatalf("expected change when replacing stale managed block")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected updated lines\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestUpdateLndRestOptionsNoChangeWhenManagedBlockIsCurrent(t *testing.T) {
	lines := []string{
		"[Application Options]",
		"tlsextraip=172.17.0.1",
		"tlsextradomain=host.docker.internal",
		"restlisten=172.17.0.1:8080",
		"# alias=LightningOS-Node",
	}

	got, changed := updateLndRestOptions(lines, []string{"172.17.0.1"})

	if changed {
		t.Fatalf("expected no change for current managed block")
	}
	if !reflect.DeepEqual(got, lines) {
		t.Fatalf("unexpected updated lines\nwant: %#v\ngot:  %#v", lines, got)
	}
}
