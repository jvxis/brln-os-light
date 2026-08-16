package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyTransitionTargetMatchesBuiltUIVersion(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(moduleRoot(t), "ui", "public", "version.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if version := normalizeAppVersion(string(data)); version != legacyTransitionTargetVersion {
		t.Fatalf("legacy transition target %q does not match UI version %q", legacyTransitionTargetVersion, version)
	}
}

func TestLegacyTransitionReleaseIsLimitedToExactTargetRelease(t *testing.T) {
	valid := appReleaseInfo{
		Version: "0.5.10-beta",
		Tag:     "0.5.10-Beta",
		Commit:  strings.Repeat("a", 40),
	}
	if err := validateLegacyTransitionRelease(valid, "0.5.10-Beta"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []appReleaseInfo{
		{Version: "0.5.7-beta", Tag: "0.5.7-Beta", Commit: valid.Commit},
		{Version: valid.Version, Tag: "release/0.5.10-beta", Commit: valid.Commit},
		{Version: valid.Version, Tag: valid.Tag, Commit: "not-a-commit"},
	} {
		if err := validateLegacyTransitionRelease(test, "0.5.10-Beta"); err == nil {
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
	info := appReleaseInfo{
		Version: "0.5.10-beta",
		Tag:     "0.5.10-Beta",
		Commit:  strings.Repeat("b", 40),
	}
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
		"--version 0.5.10-beta --tag 0.5.10-Beta --commit " + info.Commit,
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
	info := appReleaseInfo{Version: "0.5.10-beta", Tag: "0.5.10-Beta", Commit: strings.Repeat("d", 40)}
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
