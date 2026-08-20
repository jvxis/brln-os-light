package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	releaseinfo "lightningos-light"
)

func TestLegacyTransitionBuildVersionMatchesUIVersion(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(moduleRoot(t), "ui", "public", "version.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if embedded, source := releaseinfo.Version(), strings.TrimSpace(string(data)); embedded == "" || embedded != source {
		t.Fatalf("embedded release version %q does not match UI version %q", embedded, source)
	}
	if version := legacyTransitionBuildVersion(); version == "" || version != normalizeAppVersion(string(data)) {
		t.Fatalf("legacy transition build version %q does not match UI version %q", version, string(data))
	}
}

func TestLegacyTransitionReleaseIsLimitedToInstalledBuild(t *testing.T) {
	valid := legacyTransitionTestRelease(strings.Repeat("a", 40))
	if err := validateLegacyTransitionRelease(valid, releaseinfo.Version()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []appReleaseInfo{
		{Version: "0.5.7-beta", Tag: "0.5.7-Beta", Commit: valid.Commit},
		{Version: valid.Version, Tag: "release/" + valid.Tag, Commit: valid.Commit},
		{Version: valid.Version, Tag: valid.Tag, Commit: "not-a-commit"},
	} {
		if err := validateLegacyTransitionRelease(test, releaseinfo.Version()); err == nil {
			t.Fatalf("unsafe legacy transition release accepted: %#v", test)
		}
	}
	for _, installed := range []string{"0.5.2-Beta", "0.5.3-Beta", "0.5.7-Beta", "0.5.8-Beta"} {
		if err := validateLegacyTransitionRelease(valid, installed); err == nil {
			t.Fatalf("legacy transition accepted non-target installed version %q", installed)
		}
	}
}

func TestLegacyTransitionRootCommandVerifiesStagedHelper(t *testing.T) {
	info := legacyTransitionTestRelease(strings.Repeat("b", 40))
	digest := strings.Repeat("c", 64)
	command, err := buildLegacyTransitionRootCommand(
		info,
		"/var/lib/lightningos/upgrade-staging/upgrade-app-cccccccccccccccc.sh",
		digest,
		1234,
		1234,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"set -Eeuo pipefail",
		"1234:1234:700",
		"/usr/bin/sha256sum -c -",
		"/usr/bin/install -o root -g root -m 0755",
		"--version " + info.Version + " --tag " + info.Tag + " --commit " + info.Commit,
		"trap cleanup EXIT",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("legacy transition root command lacks %q:\n%s", expected, command)
		}
	}
	for _, forbidden := range []string{"--repo-url", "curl ", "wget ", "docker ", "rpcpassword", "macaroon"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("legacy transition root command contains forbidden input %q", forbidden)
		}
	}
}

func TestLegacyTransitionRejectsUnsafeStagingInputs(t *testing.T) {
	info := legacyTransitionTestRelease(strings.Repeat("d", 40))
	for _, path := range []string{
		"/tmp/helper.sh",
		"/var/lib/lightningos/upgrade-staging/../helper.sh",
		"/var/lib/lightningos/upgrade-staging/helper;id.sh",
		"/var/lib/lightningos/upgrade-staging/helper name.sh",
	} {
		if _, err := buildLegacyTransitionRootCommand(info, path, strings.Repeat("e", 64), 1, 1); err == nil {
			t.Fatalf("unsafe staging path accepted: %q", path)
		}
	}
	if _, err := buildLegacyTransitionRootCommand(info, "/var/lib/lightningos/upgrade-staging/helper.sh", "bad", 1, 1); err == nil {
		t.Fatal("invalid helper digest accepted")
	}
}

func legacyTransitionTestRelease(commit string) appReleaseInfo {
	return appReleaseInfo{
		Version: legacyTransitionBuildVersion(),
		Tag:     releaseinfo.Version(),
		Commit:  commit,
	}
}
