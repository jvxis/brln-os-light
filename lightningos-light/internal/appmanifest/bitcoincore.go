package appmanifest

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const (
	BitcoinCoreID      = "bitcoincore"
	BitcoinCoreRelease = "31.1"

	// BitcoinCoreImage is built locally by the privileged broker from the
	// official bitcoincore.org release archive after checksum and multisignature
	// verification. It is never pulled from a container registry.
	BitcoinCoreImage = "lightningos/bitcoin-core:31.1"

	BitcoinCoreImageNode AppImageVariant = "node"

	BitcoinCoreSignatureThreshold = 3

	BitcoinCoreDefaultDataDir   = "/data/bitcoin"
	BitcoinCoreProject          = "bitcoincore"
	BitcoinConsumerNetwork      = "bitcoincore_default"
	BitcoinConsumerRPCSubnet    = "172.31.253.0/24"
	BitcoinConsumerHostGateway  = "172.31.253.1"
	BitcoinCoreNetwork          = BitcoinConsumerNetwork
	BitcoinCoreRPCSubnet        = BitcoinConsumerRPCSubnet
	BitcoinCoreComposeFile      = "docker-compose.yaml"
	BitcoinCorePrimaryService   = "bitcoind"
	BitcoinCoreStopTimeout      = 10
	BitcoinCoreExecutionRoot    = "/var/lib/lightningos-privileged/apps/bitcoincore"
	BitcoinCoreStorageGuardFile = "storage-guard.sh"
	BitcoinCoreStorageIDPath    = "/var/lib/lightningos-privileged/apps/bitcoincore/storage-id"
	BitcoinCoreStorageMarker    = ".lightningos-storage-id"
	BitcoinCoreContainerDataDir = "/home/bitcoin/.bitcoin"
	BitcoinCoreContainerConfig  = BitcoinCoreContainerDataDir + "/bitcoin.conf"
	BitcoinCoreRPCUser          = "lightningos"
)

var bitcoinCoreDataDirPattern = regexp.MustCompile(`^[A-Za-z0-9/._-]+$`)

var bitcoinCoreBlockedDataDirPrefixes = []string{
	"/bin", "/boot", "/data/bitcoin", "/data/elements", "/data/lnd",
	"/dev", "/etc", "/home", "/lib", "/lib64", "/proc", "/root",
	"/run", "/sbin", "/sys", "/tmp", "/usr", "/var",
}

type BitcoinCoreReleaseArtifact struct {
	GOARCH        string
	Archive       string
	ArchiveSHA256 string
	BaseImage     string
}

type BitcoinCoreTrustedBuilder struct {
	Name        string
	Fingerprint string
}

var bitcoinCoreReleaseArtifacts = map[string]BitcoinCoreReleaseArtifact{
	"amd64": {
		GOARCH:        "amd64",
		Archive:       "bitcoin-31.1-x86_64-linux-gnu.tar.gz",
		ArchiveSHA256: "b80d9c3e04da78fb6f0569685673418cf686fadba9042d926d13fb87ff503f9e",
		BaseImage:     "debian:bookworm-slim@sha256:362e64223cc0da95422b3b13c045186fc0a81250e765d31c025fbddf257f6143",
	},
	"arm64": {
		GOARCH:        "arm64",
		Archive:       "bitcoin-31.1-aarch64-linux-gnu.tar.gz",
		ArchiveSHA256: "dcf1873f2208ba4f962f3398d47e154c39c0084be8f4553e05c940d0ace3d004",
		BaseImage:     "debian:bookworm-slim@sha256:817e6cf99d6fc127ff4ffe8580049b60deba0adfbbb2bd65ddc3ef8fbb7aade0",
	},
	"arm": {
		GOARCH:        "arm",
		Archive:       "bitcoin-31.1-arm-linux-gnueabihf.tar.gz",
		ArchiveSHA256: "66b2b45359efa161031a49898f96aa7cf1455db46ca6102acd16a7197dc3b96f",
		BaseImage:     "debian:bookworm-slim@sha256:67275dca5c395b1010017c291799bdb3e2d31bfdc4786c400f502e2ad3187c07",
	},
}

var bitcoinCoreTrustedBuilders = []BitcoinCoreTrustedBuilder{
	{Name: "achow101", Fingerprint: "152812300785C96444D3334D17565732E08E5E41"},
	{Name: "benthecarman", Fingerprint: "0AD83877C1F0CD1EE9BD660AD7CC770B81FD22A8"},
	{Name: "Sjors", Fingerprint: "ED9BDF7AD6A55E232E84524257FF9BDBCC301009"},
	{Name: "guggero", Fingerprint: "F4FC70F07310028424EFC20A8E4256593F177720"},
	{Name: "hebasto", Fingerprint: "D1DBF2C4B96F2DEBF4C16654410108112E7EA81F"},
	{Name: "marcofleon", Fingerprint: "5B286407E1EA6FE01CF9AF48BF131C2D0536F8AC"},
	{Name: "pinheadmz", Fingerprint: "E61773CD6E01040E2F1BD78CE7E2984B6289C93A"},
}

func BitcoinCoreImageForVariant(variant AppImageVariant) (string, error) {
	if variant != BitcoinCoreImageNode {
		return "", errors.New("bitcoin core image variant is not allowed")
	}
	return BitcoinCoreImage, nil
}

func BitcoinCoreArtifactForGOARCH(goarch string) (BitcoinCoreReleaseArtifact, error) {
	artifact, ok := bitcoinCoreReleaseArtifacts[goarch]
	if !ok {
		return BitcoinCoreReleaseArtifact{}, fmt.Errorf("bitcoin core does not support architecture %s", goarch)
	}
	return artifact, nil
}

func BitcoinCoreTrustedBuilders() []BitcoinCoreTrustedBuilder {
	return append([]BitcoinCoreTrustedBuilder(nil), bitcoinCoreTrustedBuilders...)
}

func NormalizeBitcoinCoreDataDir(dataDir string) (string, error) {
	trimmed := strings.TrimSpace(dataDir)
	if trimmed == "" {
		return BitcoinCoreDefaultDataDir, nil
	}
	if strings.Contains(trimmed, `\`) || !strings.HasPrefix(trimmed, "/") {
		return "", errors.New("bitcoin data directory must be a Linux absolute path")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "/" || cleaned == "/data" {
		return "", errors.New("bitcoin data directory is not allowed")
	}
	if !bitcoinCoreDataDirPattern.MatchString(cleaned) {
		return "", errors.New("bitcoin data directory contains invalid characters")
	}
	if cleaned == BitcoinCoreDefaultDataDir {
		return cleaned, nil
	}
	for _, blocked := range bitcoinCoreBlockedDataDirPrefixes {
		if cleaned == blocked || strings.HasPrefix(cleaned, blocked+"/") {
			return "", errors.New("bitcoin data directory is inside a blocked system path")
		}
	}
	return cleaned, nil
}

// BitcoinCoreCompose returns the closed execution manifest used by the
// privileged broker. Both paths are derived from broker-owned state; neither
// is accepted from an API request.
func BitcoinCoreCompose(dataDir string, executionRoot string) (string, error) {
	normalized, err := NormalizeBitcoinCoreDataDir(dataDir)
	if err != nil || normalized != dataDir {
		return "", errors.New("bitcoin data directory is not canonical")
	}
	if executionRoot == "" || !strings.HasPrefix(executionRoot, "/") || strings.Contains(executionRoot, `\`) ||
		!bitcoinCoreDataDirPattern.MatchString(executionRoot) || path.Clean(executionRoot) != executionRoot {
		return "", errors.New("bitcoin execution root is not canonical")
	}
	guardPath := path.Join(executionRoot, BitcoinCoreStorageGuardFile)
	storageIDPath := path.Join(executionRoot, "storage-id")
	return fmt.Sprintf(`services:
  bitcoind:
    image: %s
    user: "0:0"
    restart: unless-stopped
    entrypoint: ["/bin/sh", "/lightningos-storage-guard.sh"]
    command: ["bitcoind"]
    ports:
      - "8333:8333"
      - "127.0.0.1:8332:8332"
      - "127.0.0.1:28332:28332"
      - "127.0.0.1:28333:28333"
    volumes:
      - %s:/home/bitcoin/.bitcoin
      - %s:/lightningos-storage-guard.sh:ro
      - %s:/lightningos-expected-storage-id:ro
networks:
  default:
    name: %s
    ipam:
      config:
        - subnet: %s
          gateway: %s
`, BitcoinCoreImage, dataDir, guardPath, storageIDPath, BitcoinCoreNetwork, BitcoinCoreRPCSubnet, BitcoinConsumerHostGateway), nil
}

func BitcoinCoreStorageGuard() string {
	return `#!/bin/sh
set -eu

expected="$(tr -d '\r\n' < /lightningos-expected-storage-id)"
actual="$(tr -d '\r\n' < /home/bitcoin/.bitcoin/.lightningos-storage-id 2>/dev/null || true)"

if [ -z "$expected" ] || [ "$actual" != "$expected" ]; then
  echo "LightningOS storage guard: the configured Bitcoin data volume is missing or has the wrong identity; refusing to start bitcoind" >&2
  exit 78
fi

exec /entrypoint.sh "$@"
`
}

func BitcoinCoreDockerfile(baseImage string) string {
	return fmt.Sprintf(`FROM %s
RUN groupadd --gid 101 bitcoin \
    && useradd --uid 101 --gid 101 --home-dir /home/bitcoin --create-home --shell /usr/sbin/nologin bitcoin
COPY bitcoin-%s/bin/ /usr/local/bin/
COPY entrypoint.sh /entrypoint.sh
RUN chmod 0755 /entrypoint.sh /usr/local/bin/bitcoin* \
    && chown -R 101:101 /home/bitcoin
ENV HOME=/home/bitcoin BITCOIN_DATA=/home/bitcoin/.bitcoin
WORKDIR /home/bitcoin
EXPOSE 8332 8333 28332 28333
ENTRYPOINT ["/entrypoint.sh"]
CMD ["bitcoind"]
`, baseImage, BitcoinCoreRelease)
}

func BitcoinCoreEntrypoint() string {
	return `#!/bin/sh
set -eu

data_dir="${BITCOIN_DATA:-/home/bitcoin/.bitcoin}"
if [ "$#" -eq 0 ]; then
  set -- bitcoind
elif [ "${1#-}" != "$1" ]; then
  set -- bitcoind "$@"
fi

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$data_dir"
  chown 101:101 "$data_dir"
  exec /usr/bin/setpriv --reuid=101 --regid=101 --init-groups "$@"
fi

exec "$@"
`
}
