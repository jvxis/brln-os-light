package appmanifest

import (
	"net"
	"regexp"
	"strings"
	"testing"
)

func TestBitcoinCoreConsumerNetworkIsFixedPrivateIPv4(t *testing.T) {
	ip, subnet, err := net.ParseCIDR(BitcoinCoreRPCSubnet)
	if err != nil || ip.To4() == nil || subnet.String() != BitcoinCoreRPCSubnet || !ip.IsPrivate() {
		t.Fatalf("invalid dedicated Bitcoin Core RPC subnet %q", BitcoinCoreRPCSubnet)
	}
}

func TestBitcoinCoreReleaseArtifactsAreClosedByArchitecture(t *testing.T) {
	for _, test := range []struct {
		arch    string
		archive string
		hash    string
	}{
		{arch: "amd64", archive: "bitcoin-31.2-x86_64-linux-gnu.tar.gz", hash: "2f38996afaa74dfa469a23bf4727887133dfc09784df1f21ddf1c74d0dc82300"},
		{arch: "arm64", archive: "bitcoin-31.2-aarch64-linux-gnu.tar.gz", hash: "1d7d8916719ed8de0f49937f8702ec5895b0af24d936e9c4d72b58f15afe3731"},
		{arch: "arm", archive: "bitcoin-31.2-arm-linux-gnueabihf.tar.gz", hash: "dd579760f72ff24212f1a13f18ee419670fe8538465034370b5a07e624273f5e"},
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
	if BitcoinCoreImage != "lightningos/bitcoin-core:31.2" || strings.Contains(BitcoinCoreImage, "bitcoin/bitcoin") {
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
		"COPY bitcoin-31.2/bin/ /usr/local/bin/",
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
		"name: " + BitcoinCoreNetwork,
		"subnet: " + BitcoinCoreRPCSubnet,
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
