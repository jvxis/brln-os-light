package server

import (
	"testing"
	"time"
)

func TestIsSemverNewer(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{
			name:    "rc to final beta is upgrade",
			current: "0.20.1-beta.rc1",
			latest:  "0.20.1-beta",
			want:    true,
		},
		{
			name:    "beta to rc is not upgrade",
			current: "0.20.1-beta",
			latest:  "0.20.1-beta.rc1",
			want:    false,
		},
		{
			name:    "rc numeric comparison",
			current: "0.20.1-beta.rc2",
			latest:  "0.20.1-beta.rc10",
			want:    true,
		},
		{
			name:    "rc numeric comparison inverse",
			current: "0.20.1-beta.rc10",
			latest:  "0.20.1-beta.rc2",
			want:    false,
		},
		{
			name:    "same version no upgrade",
			current: "0.20.1-beta.rc1",
			latest:  "0.20.1-beta.rc1",
			want:    false,
		},
		{
			name:    "stable to prerelease is not upgrade",
			current: "0.20.1",
			latest:  "0.20.1-beta",
			want:    false,
		},
		{
			name:    "patch bump remains upgrade",
			current: "0.20.0-beta",
			latest:  "0.20.1-beta",
			want:    true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := isSemverNewer(tc.current, tc.latest)
			if got != tc.want {
				t.Fatalf("isSemverNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestSelectLndReleaseSkipsBackportWhenCurrentIsNewerLine(t *testing.T) {
	releases := []ghRelease{
		testLndRelease("v0.20.2-beta.rc1", true),
		testLndRelease("v0.21.1-beta", false),
		testLndRelease("v0.21.0-beta", false),
	}

	got, err := selectLndRelease(releases, "0.21.1-beta", time.Date(2026, 6, 30, 15, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("selectLndRelease returned error: %v", err)
	}
	if got.Version != "0.21.1-beta" {
		t.Fatalf("expected current 0.21 line release, got %q", got.Version)
	}
}

func TestSelectLndReleaseUsesHighestSemanticReleaseForOlderLine(t *testing.T) {
	releases := []ghRelease{
		testLndRelease("v0.20.2-beta.rc1", true),
		testLndRelease("v0.21.1-beta", false),
		testLndRelease("v0.21.0-beta", false),
		testLndRelease("v0.20.1-beta", false),
	}

	got, err := selectLndRelease(releases, "0.20.1-beta", time.Date(2026, 6, 30, 15, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("selectLndRelease returned error: %v", err)
	}
	if got.Version != "0.21.1-beta" {
		t.Fatalf("expected highest semantic release, got %q", got.Version)
	}
}

func TestSelectLndReleaseFallsBackToCurrentWhenFeedOnlyHasOlderReleases(t *testing.T) {
	releases := []ghRelease{
		testLndRelease("v0.20.2-beta.rc1", true),
		testLndRelease("v0.21.1-beta", false),
	}

	got, err := selectLndRelease(releases, "0.22.0-beta", time.Date(2026, 6, 30, 15, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("selectLndRelease returned error: %v", err)
	}
	if got.Version != "0.22.0-beta" {
		t.Fatalf("expected current-version fallback, got %q", got.Version)
	}
}

func TestLndReleaseCacheMatchesCurrent(t *testing.T) {
	if lndReleaseCacheMatchesCurrent("", "0.21.1-beta") {
		t.Fatalf("legacy cache without current version should not match a known current version")
	}
	if !lndReleaseCacheMatchesCurrent("0.21.1-beta", "v0.21.1-beta") {
		t.Fatalf("expected normalized current versions to match")
	}
	if lndReleaseCacheMatchesCurrent("0.20.2-beta.rc1", "0.21.1-beta") {
		t.Fatalf("expected cache from older current version to be invalid for newer current version")
	}
}

func TestLNDReleaseArchitectureIsClosed(t *testing.T) {
	tests := map[string]string{"amd64": "amd64", "arm64": "arm64", "arm": "armv7", "386": "", "mips": ""}
	for input, want := range tests {
		if got := lndReleaseArchitecture(input); got != want {
			t.Fatalf("lndReleaseArchitecture(%q) = %q, want %q", input, got, want)
		}
	}
}

func testLndRelease(tag string, prerelease bool) ghRelease {
	version := normalizeLndVersion(tag)
	return ghRelease{
		TagName:    tag,
		Prerelease: prerelease,
		HtmlURL:    "https://github.com/lightningnetwork/lnd/releases/tag/" + tag,
		Assets: []ghAsset{
			{
				Name:               "lnd-linux-amd64-v" + version + ".tar.gz",
				BrowserDownloadURL: "https://github.com/lightningnetwork/lnd/releases/download/" + tag + "/lnd-linux-amd64-v" + version + ".tar.gz",
			},
		},
	}
}
