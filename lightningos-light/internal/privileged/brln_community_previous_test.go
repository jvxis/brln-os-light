package privileged

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

// 16/10/2026: with the 0.1.58 catalog installed and the app still on 0.1.57, the signer access
// password was unreadable ("admin password unavailable"), because only the 0.1.4 pair of the
// 0.5.29 catalog was accepted as a previous release. Pins below are copied independently from the
// catalog that shipped 0.1.57, so this fixture does not trust the production list.
const community057Web = "ghcr.io/jvxis/brln-community-web:0.1.57@sha256:cc604bd7abe739bdc75941bd71de5edf0c87df6b45849b09542ffa1d0a3aa2e8"
const community057Signer = "ghcr.io/jvxis/brln-signer:0.1.57@sha256:12a0f89a5d4f1999314c1347dad45ad2e817656b23cd09282972cf662c622792"

func communityInstallWith(t *testing.T, web, signer string) *NativeBRLNCommunityManager {
	t.Helper()
	manager := testBRLNCommunityManager(t, &composeRecordingRunner{})
	if _, err := manager.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	raw := string(mustReadTestFile(t, manager.Paths.ComposePath))
	raw = strings.ReplaceAll(raw, appmanifest.BRLNCommunityWebImage, web)
	raw = strings.ReplaceAll(raw, appmanifest.BRLNCommunitySignerImage, signer)
	if err := os.WriteFile(manager.Paths.ComposePath, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestNativeBRLNCommunityPasswordReadableUnderTheNextCatalog(t *testing.T) {
	manager := communityInstallWith(t, community057Web, community057Signer)
	password, err := manager.ReadPassword()
	if err != nil || password == "" {
		t.Fatalf("0.1.57 install under a newer catalog lost its signer password: %v", err)
	}
	// Reading the password is all the previous pair buys: the old images still never start.
	if _, err := manager.Lifecycle(context.Background(), AppLifecycleStart, false); err == nil {
		t.Fatal("previous snapshot was accepted for start")
	}
}

func TestNativeBRLNCommunityMixedOrUnknownPairsStayRejected(t *testing.T) {
	unknownDigest := "ghcr.io/jvxis/brln-community-web:0.1.57@sha256:" + strings.Repeat("0", 64)
	unknownRelease := "ghcr.io/jvxis/brln-community-web:9.9.9@sha256:cc604bd7abe739bdc75941bd71de5edf0c87df6b45849b09542ffa1d0a3aa2e8"
	for name, pair := range map[string][2]string{
		"mixed releases": {community057Web, communityPreviousSigner},
		"unknown digest": {unknownDigest, community057Signer},
		"unknown tag":    {unknownRelease, community057Signer},
	} {
		manager := communityInstallWith(t, pair[0], pair[1])
		if _, err := manager.ReadPassword(); err == nil {
			t.Fatalf("%s: password read from a compose no catalog ever pinned", name)
		}
	}
}

func TestBRLNCommunityPreviousReleasesAreWellFormed(t *testing.T) {
	digest := regexp.MustCompile(`^[0-9a-f]{64}$`)
	seen := map[string]bool{}
	for _, pair := range appmanifest.BRLNCommunityPreviousReleases {
		if seen[pair.Release] {
			t.Fatalf("release %s listed twice", pair.Release)
		}
		seen[pair.Release] = true
		if !digest.MatchString(pair.WebDigest) || !digest.MatchString(pair.SignerDigest) {
			t.Fatalf("release %s has a malformed digest", pair.Release)
		}
	}
	// The 0.5.29 pair stays readable, as it was before the list existed.
	if !seen["0.1.4"] || !seen["0.1.57"] || !seen["0.1.58"] {
		t.Fatal("0.1.4, 0.1.57 and 0.1.58 must stay in the list")
	}
}
