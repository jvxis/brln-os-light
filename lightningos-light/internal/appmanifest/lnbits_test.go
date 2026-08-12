package appmanifest

import (
	"strings"
	"testing"
)

func canonicalLNbitsEnvForTest() []byte {
	lines := make([]string, 0, len(LNbitsManagedEnv())+1)
	for _, item := range LNbitsManagedEnv() {
		lines = append(lines, item[0]+"="+item[1])
	}
	return []byte(strings.Join(append(lines, ""), "\n"))
}

func TestValidateLNbitsEnvPreservesSafeCustomSettings(t *testing.T) {
	raw := append(canonicalLNbitsEnvForTest(), []byte("LNBITS_SITE_TITLE=My Node\nLNBITS_ADMIN_UI=true\n")...)
	if err := ValidateLNbitsEnv(raw); err != nil {
		t.Fatalf("validate LNbits environment: %v", err)
	}
}

func TestNormalizeLNbitsEnvPreservesCustomSettingsAndScrubsLegacyCredentials(t *testing.T) {
	legacy := []byte(strings.Join([]string{
		"LNBITS_SITE_TITLE=Preserved Node",
		"LND_REST_ENDPOINT=https://old:8080/",
		"LND_REST_MACAROON=/data/lnd/admin.macaroon",
		"LND_REST_MACAROON_ENCRYPTED=legacy-secret",
		"AUTH_HTTPS_ONLY=true",
		"",
	}, "\n"))
	normalized, err := NormalizeLNbitsEnv(legacy)
	if err != nil {
		t.Fatal(err)
	}
	got := string(normalized)
	for _, required := range []string{
		"LNBITS_SITE_TITLE=Preserved Node\n",
		"LND_REST_ENDPOINT=https://host.docker.internal:8080/\n",
		"LND_REST_MACAROON=/etc/lnd/lnbits.macaroon\n",
		"AUTH_HTTPS_ONLY=false\n",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("normalized environment missing %q\n%s", required, got)
		}
	}
	for _, forbidden := range []string{"legacy-secret", "admin.macaroon", "LND_REST_MACAROON_ENCRYPTED"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("normalized environment retained %q\n%s", forbidden, got)
		}
	}
}

func TestValidateLNbitsEnvRejectsRuntimeAndCredentialOverrides(t *testing.T) {
	base := string(canonicalLNbitsEnvForTest())
	for _, entry := range []string{
		"PYTHONPATH=/hostile\n",
		"LD_PRELOAD=/hostile.so\n",
		"LND_REST_MACAROON_ENCRYPTED=admin-secret\n",
		"LND_GRPC_MACAROON=/data/lnd/admin.macaroon\n",
	} {
		t.Run(strings.SplitN(entry, "=", 2)[0], func(t *testing.T) {
			if err := ValidateLNbitsEnv([]byte(base + entry)); err == nil {
				t.Fatalf("expected %q to be rejected", entry)
			}
		})
	}
}

func TestValidateLNbitsEnvRejectsManagedDriftAndDuplicates(t *testing.T) {
	base := string(canonicalLNbitsEnvForTest())
	if err := ValidateLNbitsEnv([]byte(strings.Replace(base, "LND_REST_ENDPOINT=https://host.docker.internal:8080/", "LND_REST_ENDPOINT=https://elsewhere:8080/", 1))); err == nil {
		t.Fatal("expected managed endpoint drift to be rejected")
	}
	if err := ValidateLNbitsEnv([]byte(base + "LNBITS_PORT=5000\n")); err == nil {
		t.Fatal("expected duplicate environment key to be rejected")
	}
}

func TestLNbitsComposeClosesRuntimeAndMountsOnlyDedicatedCredential(t *testing.T) {
	compose := LNbitsCompose(LNbitsComposePaths{
		DataDir:      "/apps-data/lnbits/data",
		TLSCertPath:  "/snapshot/lnbits/lnd/tls.cert",
		MacaroonPath: "/snapshot/lnbits/lnd/lnbits.macaroon",
	})
	for _, required := range []string{
		"image: " + LNbitsImage,
		`user: "65532:65532"`,
		"read_only: true",
		"cap_drop:\n      - ALL",
		"no-new-privileges:true",
		"/apps-data/lnbits/data:/app/data:rw",
		"/snapshot/lnbits/lnd/tls.cert:/etc/lnd/tls.cert:ro",
		"/snapshot/lnbits/lnd/lnbits.macaroon:/etc/lnd/lnbits.macaroon:ro",
		"name: lnbits_default",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("compose missing %q\n%s", required, compose)
		}
	}
	for _, forbidden := range []string{"admin.macaroon", "/data/lnd", "/var/run/docker.sock", "privileged: true", "network_mode: host"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("compose contains forbidden input %q\n%s", forbidden, compose)
		}
	}
}

func TestLNbitsCatalogProvidesClosedLifecycleAndFirewall(t *testing.T) {
	manifest, err := ComposeManifestForApp(LNbitsID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Project != LNbitsProject || manifest.PrimaryService != LNbitsPrimaryService || manifest.StopTimeoutSeconds != LNbitsStopTimeout {
		t.Fatalf("unexpected LNbits manifest: %#v", manifest)
	}
	port, err := CatalogExternalTCPPort(LNbitsID)
	if err != nil || port != LNbitsPort {
		t.Fatalf("unexpected LNbits firewall port: %d, %v", port, err)
	}
}
