package appmanifest

import (
	"regexp"
	"strings"
	"testing"
)

func TestBitcoinCoreReleaseArtifactsAreClosedByArchitecture(t *testing.T) {
	for _, test := range []struct {
		arch    string
		archive string
		hash    string
	}{
		{arch: "amd64", archive: "bitcoin-31.1-x86_64-linux-gnu.tar.gz", hash: "b80d9c3e04da78fb6f0569685673418cf686fadba9042d926d13fb87ff503f9e"},
		{arch: "arm64", archive: "bitcoin-31.1-aarch64-linux-gnu.tar.gz", hash: "dcf1873f2208ba4f962f3398d47e154c39c0084be8f4553e05c940d0ace3d004"},
		{arch: "arm", archive: "bitcoin-31.1-arm-linux-gnueabihf.tar.gz", hash: "66b2b45359efa161031a49898f96aa7cf1455db46ca6102acd16a7197dc3b96f"},
	} {
		artifact, err := BitcoinCoreArtifactForGOARCH(test.arch)
		if err != nil || artifact.GOARCH != test.arch || artifact.Archive != test.archive || artifact.ArchiveSHA256 != test.hash {
			t.Fatalf("artifact/error for %s = %#v/%v", test.arch, artifact, err)
		}
		if !strings.HasPrefix(artifact.BaseImage, "debian:bookworm-slim@sha256:") || strings.Contains(artifact.BaseImage, ":latest") {
			t.Fatalf("base image is not digest-pinned: %q", artifact.BaseImage)
		}
	}
	if _, err := BitcoinCoreArtifactForGOARCH("386;reboot"); err == nil {
		t.Fatal("unsupported architecture was accepted")
	}
}

func TestNormalizeBitcoinCoreDataDirUsesClosedPathPolicy(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
		ok    bool
	}{
		{input: "", want: BitcoinCoreDefaultDataDir, ok: true},
		{input: "/data/bitcoin", want: BitcoinCoreDefaultDataDir, ok: true},
		{input: "/mnt/bitcoin-ssd/bitcoin", want: "/mnt/bitcoin-ssd/bitcoin", ok: true},
		{input: "/mnt/bitcoin/../bitcoin-data", want: "/mnt/bitcoin-data", ok: true},
		{input: "/"}, {input: "/data"}, {input: "/data/bitcoin/child"},
		{input: "/etc/bitcoin"}, {input: "/home/admin/bitcoin"},
		{input: "/mnt/bitcoin data"}, {input: "mnt/bitcoin"}, {input: `C:\bitcoin`},
	} {
		got, err := NormalizeBitcoinCoreDataDir(test.input)
		if test.ok && (err != nil || got != test.want) {
			t.Fatalf("NormalizeBitcoinCoreDataDir(%q)=%q/%v want %q", test.input, got, err, test.want)
		}
		if !test.ok && err == nil {
			t.Fatalf("NormalizeBitcoinCoreDataDir(%q) unexpectedly returned %q", test.input, got)
		}
	}
}

func TestBitcoinCoreUsesLocalImageAndIndependentTrustedBuilderList(t *testing.T) {
	if BitcoinCoreImage != "lightningos/bitcoin-core:31.1" || strings.Contains(BitcoinCoreImage, "bitcoin/bitcoin") {
		t.Fatalf("unexpected Bitcoin Core image: %q", BitcoinCoreImage)
	}
	fingerprintPattern := regexp.MustCompile(`^[A-F0-9]{40}$`)
	builders := BitcoinCoreTrustedBuilders()
	if len(builders) < BitcoinCoreSignatureThreshold || len(builders) != 7 {
		t.Fatalf("unexpected trusted builder set: %#v", builders)
	}
	seen := make(map[string]bool)
	for _, builder := range builders {
		if builder.Name == "" || !fingerprintPattern.MatchString(builder.Fingerprint) || seen[builder.Fingerprint] {
			t.Fatalf("invalid trusted builder: %#v", builder)
		}
		seen[builder.Fingerprint] = true
	}
	builders[0].Fingerprint = "EVIL"
	if BitcoinCoreTrustedBuilders()[0].Fingerprint == "EVIL" {
		t.Fatal("caller mutated the trusted builder list")
	}
}

func TestBitcoinCoreImageRecipeDropsPrivilegesWithoutNetworkInstall(t *testing.T) {
	artifact, err := BitcoinCoreArtifactForGOARCH("amd64")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := BitcoinCoreDockerfile(artifact.BaseImage)
	for _, expected := range []string{
		"FROM " + artifact.BaseImage,
		"COPY bitcoin-31.1/bin/ /usr/local/bin/",
		"useradd --uid 101 --gid 101",
		`ENTRYPOINT ["/entrypoint.sh"]`,
	} {
		if !strings.Contains(dockerfile, expected) {
			t.Fatalf("Dockerfile missing %q:\n%s", expected, dockerfile)
		}
	}
	for _, forbidden := range []string{"apt-get", "curl", "wget", "bitcoin/bitcoin"} {
		if strings.Contains(dockerfile, forbidden) {
			t.Fatalf("Dockerfile contains forbidden dependency %q:\n%s", forbidden, dockerfile)
		}
	}
	entrypoint := BitcoinCoreEntrypoint()
	for _, expected := range []string{"chown 101:101", "setpriv --reuid=101 --regid=101 --init-groups", `set -- bitcoind "$@"`} {
		if !strings.Contains(entrypoint, expected) {
			t.Fatalf("entrypoint missing %q:\n%s", expected, entrypoint)
		}
	}
}

func TestBitcoinCoreComposeIsClosedAndUsesBrokerOwnedAssets(t *testing.T) {
	raw, err := BitcoinCoreCompose("/mnt/bitcoin-ssd/bitcoin", BitcoinCoreExecutionRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"image: " + BitcoinCoreImage,
		"- /mnt/bitcoin-ssd/bitcoin:/home/bitcoin/.bitcoin",
		"- " + BitcoinCoreExecutionRoot + "/storage-guard.sh:/lightningos-storage-guard.sh:ro",
		"- " + BitcoinCoreExecutionRoot + "/storage-id:/lightningos-expected-storage-id:ro",
	} {
		if !strings.Contains(raw, expected) {
			t.Fatalf("compose missing %q:\n%s", expected, raw)
		}
	}
	for _, forbidden := range []string{"privileged: true", "/var/run/docker.sock", ":latest"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("compose contains forbidden value %q:\n%s", forbidden, raw)
		}
	}
	for _, test := range []struct{ dataDir, root string }{
		{dataDir: "/etc/bitcoin", root: BitcoinCoreExecutionRoot},
		{dataDir: "/mnt/bitcoin/../bitcoin-data", root: BitcoinCoreExecutionRoot},
		{dataDir: "/data/bitcoin", root: "relative/root"},
		{dataDir: "/data/bitcoin", root: "/var/lib/../tmp"},
	} {
		if _, err := BitcoinCoreCompose(test.dataDir, test.root); err == nil {
			t.Fatalf("unsafe compose inputs accepted: %#v", test)
		}
	}
}
