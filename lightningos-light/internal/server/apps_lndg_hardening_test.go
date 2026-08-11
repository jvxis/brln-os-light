package server

import (
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
)

func TestLNDgComposeSelectsCatalogImageAndFixedEntrypoint(t *testing.T) {
	paths := lndgPaths{
		PgDir:          "/var/lib/lightningos/apps-data/lndg/pgdata",
		DataDir:        "/var/lib/lightningos/apps-data/lndg/data",
		LogPath:        "/var/lib/lightningos/apps-data/lndg/data/lndg-controller.log",
		EntrypointPath: "/var/lib/lightningos/apps/lndg/entrypoint.sh",
		LndDir:         "/var/lib/lightningos/apps-data/lndg/lnd",
	}
	compose := lndgComposeContents(paths)
	for _, required := range []string{
		"image: " + appmanifest.LNDgImage,
		`entrypoint: ["/entrypoint.sh"]`,
		paths.EntrypointPath + ":/entrypoint.sh:ro",
		paths.LndDir + ":/etc/lnd:ro",
		"/data/lnd/data/graph/mainnet/channel.db:/etc/lnd/channel.db:ro",
		"LNDG_MACAROON_PATH: /etc/lnd/lndg.macaroon",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("LNDg Compose missing %q", required)
		}
	}
	for _, forbidden := range []string{"/data/lnd:/root/.lnd", "admin.macaroon"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("LNDg Compose exposes forbidden LND material %q", forbidden)
		}
	}
}

func TestLNDgMacaroonPermissionsMatchUpstreamRPCInventory(t *testing.T) {
	got := lndclient.MacaroonPermissionStrings(lndgMacaroonPermissions())
	want := []string{
		"address:write",
		"info:read",
		"invoices:read",
		"invoices:write",
		"message:write",
		"offchain:read",
		"offchain:write",
		"onchain:read",
		"onchain:write",
		"peers:read",
		"peers:write",
		"signer:generate",
		"signer:read",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("LNDg permissions=%v want=%v", got, want)
	}
	for _, forbidden := range []string{"macaroon:generate", "macaroon:read", "macaroon:write", "info:write"} {
		if stringInSlice(forbidden, got) {
			t.Fatalf("LNDg permission set contains forbidden authority %q", forbidden)
		}
	}
	for _, required := range []string{"-tls \"$LNDG_TLS_PATH\"", "-mcrn \"$LNDG_MACAROON_PATH\"", "-lnddb \"$LNDG_DATABASE_PATH\""} {
		if !strings.Contains(lndgEntrypoint, required) {
			t.Fatalf("LNDg entrypoint missing dedicated path argument %q", required)
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
