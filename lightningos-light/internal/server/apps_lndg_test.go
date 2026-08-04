package server

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestLndgBuildUsesCompatiblePinnedUpstream(t *testing.T) {
	if !strings.Contains(lndgDockerfile, "FROM python:3.12-slim") {
		t.Fatal("LNDg Django 6 build must use Python 3.12")
	}
	if len(lndgDefaultGitSHA) != 40 {
		t.Fatalf("LNDg default git SHA must be pinned, got %q", lndgDefaultGitSHA)
	}
	if !strings.Contains(lndgDockerignore, "*") || strings.Contains(lndgDockerignore, "!.env") {
		t.Fatalf("LNDg build context must exclude .env: %q", lndgDockerignore)
	}

	ref, sha := resolveLndgGit(context.Background(), "", "")
	if ref != lndgDefaultGitRef || sha != lndgDefaultGitSHA {
		t.Fatalf("fresh install resolved to %s@%s", ref, sha)
	}

	ref, sha = resolveLndgGit(context.Background(), "stable", "1234567890abcdef")
	if ref != "stable" || sha != "1234567890abcdef" {
		t.Fatalf("existing install pin changed to %s@%s", ref, sha)
	}
}

func TestUpdateLndGrpcOptionsRemovesStaleDockerBridgeListeners(t *testing.T) {
	lines := []string{
		"[Application Options]",
		"tlsextraip=172.19.0.1",
		"tlsextradomain=host.docker.internal",
		"rpclisten=172.19.0.1:10009",
		"rpclisten=127.0.0.1:10009",
		"alias=LightningOS-Node",
	}

	got, changed := updateLndGrpcOptions(lines, []string{"172.17.0.1"})
	want := []string{
		"[Application Options]",
		"tlsextraip=172.17.0.1",
		"tlsextradomain=host.docker.internal",
		"rpclisten=127.0.0.1:10009",
		"rpclisten=172.17.0.1:10009",
		"alias=LightningOS-Node",
	}

	if !changed {
		t.Fatalf("expected stale Docker listener cleanup")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected LND config\nwant: %#v\ngot:  %#v", want, got)
	}
}
