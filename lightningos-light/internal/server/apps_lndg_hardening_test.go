package server

import (
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestLNDgComposeSelectsCatalogImageAndFixedEntrypoint(t *testing.T) {
	paths := lndgPaths{
		PgDir:          "/var/lib/lightningos/apps-data/lndg/pgdata",
		DataDir:        "/var/lib/lightningos/apps-data/lndg/data",
		LogPath:        "/var/lib/lightningos/apps-data/lndg/data/lndg-controller.log",
		EntrypointPath: "/var/lib/lightningos/apps/lndg/entrypoint.sh",
	}
	compose := lndgComposeContents(paths)
	for _, required := range []string{
		"image: " + appmanifest.LNDgImage,
		`entrypoint: ["/entrypoint.sh"]`,
		paths.EntrypointPath + ":/entrypoint.sh:ro",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("LNDg Compose missing %q", required)
		}
	}
}

func TestLNDgCompatibilityBuildUsesClosedReleaseSource(t *testing.T) {
	for _, required := range []string{
		"FROM " + appmanifest.LNDgBaseImage,
		appmanifest.LNDgSourceURL,
		appmanifest.LNDgSourceSHA256,
		"sha256sum --check --strict",
		"supervisor==" + appmanifest.LNDgSupervisor,
	} {
		if !strings.Contains(lndgDockerfile, required) {
			t.Fatalf("compatibility Dockerfile missing %q", required)
		}
	}
	for _, forbidden := range []string{"git clone", "git fetch", "checkout", "master"} {
		if strings.Contains(lndgDockerfile, forbidden) {
			t.Fatalf("compatibility Dockerfile contains mutable selector %q", forbidden)
		}
	}
}
