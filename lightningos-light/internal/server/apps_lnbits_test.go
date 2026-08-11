package server

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
)

func TestLnbitsComposeUsesPinnedOfficialImageAndDedicatedCredential(t *testing.T) {
	paths := lnbitsPaths{
		DataDir: "/var/lib/lightningos/apps-data/lnbits/data",
		LndDir:  "/var/lib/lightningos/apps-data/lnbits/lnd",
	}
	compose := lnbitsComposeContents(paths)

	for _, required := range []string{
		"image: " + appmanifest.LNbitsImage,
		paths.DataDir + ":/app/data",
		paths.LndDir + ":/etc/lnd:ro",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("compose missing %q\n%s", required, compose)
		}
	}
	for _, forbidden := range []string{"lnbits/lnbits:latest", "/data/lnd", "admin.macaroon"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("compose exposes mutable or privileged input %q\n%s", forbidden, compose)
		}
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

func TestEnsureLnbitsEnvMigratesLegacyAdminCredentialSelectors(t *testing.T) {
	paths := lnbitsPaths{EnvPath: filepath.Join(t.TempDir(), ".env")}
	legacy := strings.Join([]string{
		"LNBITS_BACKEND_WALLET_CLASS=LndRestWallet",
		"LND_REST_ENDPOINT=https://old-gateway:8080/",
		"LND_REST_CERT=/data/lnd/tls.cert",
		"LND_REST_MACAROON=/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon",
		"LND_REST_MACAROON_ENCRYPTED=legacy-secret",
		"CUSTOM_SETTING=preserved",
		"",
	}, "\n")
	if err := os.WriteFile(paths.EnvPath, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureLnbitsEnv(paths); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(paths.EnvPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(content)
	for _, required := range []string{
		"LND_REST_ENDPOINT=https://host.docker.internal:8080/",
		"LND_REST_CERT=/etc/lnd/tls.cert",
		"LND_REST_MACAROON=/etc/lnd/lnbits.macaroon",
		"LND_REST_MACAROON_ENCRYPTED=",
		"CUSTOM_SETTING=preserved",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("migrated env missing %q\n%s", required, got)
		}
	}
	for _, forbidden := range []string{"admin.macaroon", "legacy-secret", "/data/lnd"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("migrated env retained %q\n%s", forbidden, got)
		}
	}
}

func TestLnbitsMacaroonPermissionsMatchUpstreamRESTInventory(t *testing.T) {
	got := lndclient.MacaroonPermissionStrings(lnbitsMacaroonPermissions())
	want := []string{
		"info:read",
		"invoices:read",
		"invoices:write",
		"offchain:read",
		"offchain:write",
		"onchain:read",
		"onchain:write",
		"peers:read",
		"peers:write",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LNbits permissions=%v want=%v", got, want)
	}
	for _, forbidden := range []string{
		"address:read", "address:write", "info:write", "macaroon:generate",
		"macaroon:read", "macaroon:write", "message:write", "signer:generate",
		"signer:read",
	} {
		if stringInSlice(forbidden, got) {
			t.Fatalf("LNbits permission set contains forbidden authority %q", forbidden)
		}
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
