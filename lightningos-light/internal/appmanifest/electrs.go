package appmanifest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	ElectrsID             = "electrs"
	ElectrsBitcoinRPCUser = "electrs"
	ElectrsProject        = "electrs"
	ElectrsComposeFile    = "docker-compose.yaml"
	ElectrsEnvFile        = ".env"
	ElectrsCookieFile     = "bitcoin.cookie"
	ElectrsPrimaryService = "electrs"
	ElectrsVolume         = "electrs_data"
	ElectrsRPCPort        = 50001
	ElectrsMonitorPort    = 4224
	ElectrsStopTimeout    = 60
	ElectrsNoFileLimit    = 65536
	ElectrsContainerUID   = 1000
	ElectrsContainerGID   = 1000

	ElectrsRelease      = "0.11.1"
	ElectrsSourceTag    = "v0.11.1"
	ElectrsTagObject    = "8e9bf28431b00286552248bd438ba5c2d4efaada"
	ElectrsSourceCommit = "35216c6d30148be8e6763d913d437330f431fc03"
	ElectrsSourceURL    = "https://codeload.github.com/romanz/electrs/tar.gz/refs/tags/" + ElectrsSourceTag
	ElectrsSourceSHA256 = "d51db4ffe2eac77deb62b6cf51745c3c9ef3965ca1bd72d3fd5c69f64540e33f"
	ElectrsSourceDir    = "electrs-" + ElectrsRelease
	ElectrsBaseImage    = "debian:trixie-slim@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258"
	ElectrsImage        = "lightningos/electrs:" + ElectrsRelease

	ElectrsBitcoinModeApp    = "app"
	ElectrsBitcoinModeNative = "native"

	ElectrsImageApp AppImageVariant = "app"
)

type ElectrsRuntime struct {
	BitcoinMode string
	Network     string
	DataDir     string
}

type ElectrsNetwork struct {
	ElectrsName string
	BitcoinName string
	RPCPort     int
	P2PPort     int
}

func ElectrsImageForVariant(variant AppImageVariant) (string, error) {
	if variant != ElectrsImageApp {
		return "", errors.New("electrs image variant is not allowed")
	}
	return ElectrsImage, nil
}

func ElectrsNetworkForName(network string) (ElectrsNetwork, error) {
	switch network {
	case "bitcoin":
		return ElectrsNetwork{ElectrsName: "bitcoin", BitcoinName: "main", RPCPort: 8332, P2PPort: 8333}, nil
	case "testnet":
		return ElectrsNetwork{ElectrsName: "testnet", BitcoinName: "test", RPCPort: 18332, P2PPort: 18333}, nil
	case "signet":
		return ElectrsNetwork{ElectrsName: "signet", BitcoinName: "signet", RPCPort: 38332, P2PPort: 38333}, nil
	case "regtest":
		return ElectrsNetwork{ElectrsName: "regtest", BitcoinName: "regtest", RPCPort: 18443, P2PPort: 18444}, nil
	default:
		return ElectrsNetwork{}, errors.New("electrs network is not allowed")
	}
}

func ValidateElectrsRuntime(runtime ElectrsRuntime) error {
	if runtime.BitcoinMode != ElectrsBitcoinModeApp && runtime.BitcoinMode != ElectrsBitcoinModeNative {
		return errors.New("electrs bitcoin mode is not allowed")
	}
	if _, err := ElectrsNetworkForName(runtime.Network); err != nil {
		return err
	}
	if runtime.DataDir != "" {
		_, err := NormalizeCatalogDataDir(ElectrsID, runtime.DataDir)
		return err
	}
	return nil
}

func ElectrsRuntimeEnv(runtime ElectrsRuntime) (string, error) {
	if err := ValidateElectrsRuntime(runtime); err != nil {
		return "", err
	}
	raw := "ELECTRS_BITCOIN_MODE=" + runtime.BitcoinMode + "\nELECTRS_NETWORK=" + runtime.Network + "\n"
	if runtime.DataDir != "" {
		raw += "ELECTRS_DATA_DIR=" + runtime.DataDir + "\n"
	}
	return raw, nil
}

func ParseElectrsRuntimeEnv(raw []byte) (ElectrsRuntime, error) {
	var runtime ElectrsRuntime
	if len(raw) == 0 || len(raw) > 256 || raw[len(raw)-1] != '\n' || strings.ContainsAny(string(raw), "\r\x00") {
		return runtime, errors.New("invalid electrs environment encoding")
	}
	values := make(map[string]string)
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines[:len(lines)-1] {
		key, value, ok := strings.Cut(line, "=")
		if !ok || value == "" {
			return runtime, errors.New("invalid electrs environment entry")
		}
		if _, exists := values[key]; exists {
			return runtime, errors.New("duplicate electrs environment entry")
		}
		switch key {
		case "ELECTRS_BITCOIN_MODE", "ELECTRS_NETWORK", "ELECTRS_DATA_DIR":
			values[key] = value
		default:
			return runtime, errors.New("electrs environment entry is not allowed")
		}
	}
	if len(values) != 2 && len(values) != 3 {
		return runtime, errors.New("electrs environment is incomplete")
	}
	runtime = ElectrsRuntime{BitcoinMode: values["ELECTRS_BITCOIN_MODE"], Network: values["ELECTRS_NETWORK"], DataDir: values["ELECTRS_DATA_DIR"]}
	if err := ValidateElectrsRuntime(runtime); err != nil {
		return ElectrsRuntime{}, err
	}
	expected, _ := ElectrsRuntimeEnv(runtime)
	if string(raw) != expected {
		return ElectrsRuntime{}, errors.New("electrs environment is not canonical")
	}
	return runtime, nil
}

func ElectrsCompose(runtime ElectrsRuntime) (string, error) {
	if err := ValidateElectrsRuntime(runtime); err != nil {
		return "", err
	}
	network, _ := ElectrsNetworkForName(runtime.Network)
	bitcoinHost := "bitcoind"
	if runtime.BitcoinMode == ElectrsBitcoinModeNative {
		bitcoinHost = BitcoinConsumerHostGateway
	}
	dataMount := ElectrsVolume + ":/data/db"
	volumeDeclaration := fmt.Sprintf("\nvolumes:\n  %s:\n    name: %s\n", ElectrsVolume, ElectrsVolume)
	if runtime.DataDir != "" {
		dataMount = runtime.DataDir + "/db:/data/db"
		volumeDeclaration = ""
	}
	return fmt.Sprintf(`services:
  electrs:
    image: %s
    container_name: electrs
    user: "%d:%d"
    restart: unless-stopped
    stop_grace_period: %ds
    ulimits:
      nofile:
        soft: %d
        hard: %d
    read_only: true
    init: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    environment:
      HOME: /tmp
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=16m,uid=%d,gid=%d
    networks:
      - default
      - bitcoincore
    ports:
      - "127.0.0.1:%d:%d"
      - "127.0.0.1:%d:%d"
    volumes:
      - %s
      - ./%s:/run/bitcoin.cookie:ro
    command:
      - --network=%s
      - --db-dir=/data/db
      - --daemon-rpc-addr=%s:%d
      - --daemon-p2p-addr=%s:%d
      - --electrum-rpc-addr=0.0.0.0:%d
      - --monitoring-addr=0.0.0.0:%d
      - --cookie-file=/run/bitcoin.cookie
      - --index-batch-size=10
      - --log-filters=INFO

networks:
  default:
    name: electrs_default
  bitcoincore:
    external: true
    name: %s
%s`, ElectrsImage, ElectrsContainerUID, ElectrsContainerGID, ElectrsStopTimeout,
		ElectrsNoFileLimit, ElectrsNoFileLimit,
		ElectrsContainerUID, ElectrsContainerGID,
		ElectrsRPCPort, ElectrsRPCPort, ElectrsMonitorPort, ElectrsMonitorPort,
		dataMount, ElectrsCookieFile, network.ElectrsName,
		bitcoinHost, network.RPCPort, bitcoinHost, network.P2PPort,
		ElectrsRPCPort, ElectrsMonitorPort, BitcoinConsumerNetwork, volumeDeclaration), nil
}

func ElectrsDockerfile() string {
	return fmt.Sprintf(`FROM %s AS base
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates librocksdb-dev \
    && rm -rf /var/lib/apt/lists/*

FROM base AS electrs-build
RUN apt-get update \
    && apt-get install -y --no-install-recommends cargo build-essential libclang-dev \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build/electrs
COPY %s/ ./
ENV ROCKSDB_INCLUDE_DIR=/usr/include ROCKSDB_LIB_DIR=/usr/lib
RUN cargo install --locked --path . --root /opt/electrs

FROM base AS result
RUN groupadd --gid %d electrs \
    && useradd --uid %d --gid %d --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin electrs \
    && install -d -o %d -g %d -m 0750 /data/db
COPY --from=electrs-build /opt/electrs/bin/electrs /usr/bin/electrs
USER %d:%d
WORKDIR /data
ENTRYPOINT ["/usr/bin/electrs"]
`, ElectrsBaseImage, ElectrsSourceDir,
		ElectrsContainerGID, ElectrsContainerUID, ElectrsContainerGID,
		ElectrsContainerUID, ElectrsContainerGID, ElectrsContainerUID, ElectrsContainerGID)
}

func ElectrsProbeAddress(runtime ElectrsRuntime) (string, error) {
	if err := ValidateElectrsRuntime(runtime); err != nil {
		return "", err
	}
	network, _ := ElectrsNetworkForName(runtime.Network)
	return "127.0.0.1:" + strconv.Itoa(network.RPCPort), nil
}
