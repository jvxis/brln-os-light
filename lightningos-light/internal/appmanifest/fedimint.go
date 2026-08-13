package appmanifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	FedimintGuardianID             = "fedimint-guardian"
	FedimintGuardianProject        = "fedimint-guardian"
	FedimintGuardianComposeFile    = "docker-compose.yaml"
	FedimintGuardianRuntimeFile    = "runtime.json"
	FedimintGuardianPrimaryService = "fedimintd"
	FedimintGuardianDataDir        = "/var/lib/lightningos/apps-data/fedimint-guardian/fedimintd"
	FedimintDataDirEnv             = "FEDIMINT_DATA_DIR"
	FedimintGuardianNetwork        = "fedimint-guardian_default"
	FedimintGuardianP2PPort        = 8173
	FedimintGuardianAPIPort        = 8174
	FedimintGuardianUIPort         = 8175

	FedimintGatewayID             = "fedimint-gateway"
	FedimintGatewayProject        = "fedimint-gateway"
	FedimintGatewayComposeFile    = "docker-compose.yaml"
	FedimintGatewayRuntimeFile    = "runtime.json"
	FedimintGatewayTLSFile        = "tls.cert"
	FedimintGatewayMacaroonFile   = "fedimint-gateway.macaroon"
	FedimintGatewayPrimaryService = "gatewayd"
	FedimintGatewayDataDir        = "/var/lib/lightningos/apps-data/fedimint-gateway/gatewayd"
	FedimintGatewayNetwork        = "fedimint-gateway_default"
	FedimintGatewayCredentialRoot = "FEDIMINT_GATEWAY_CREDENTIAL_ROOT"
	FedimintGatewayUIPort         = 8176
	FedimintGatewayIrohPort       = 8177
	FedimintGatewayTLSPath        = "/run/lnd/tls.cert"
	FedimintGatewayMacaroonPath   = "/run/lnd/fedimint-gateway.macaroon"

	FedimintStopTimeout  = 60
	FedimintContainerUID = 1000
	FedimintContainerGID = 1000
	FedimintRelease      = "0.11.1"

	// Official multi-architecture manifests for the stable upstream release.
	FedimintGuardianImage = "fedimint/fedimintd:v0.11.1@sha256:ba23a29ae5b71cf7685c0a4f6594ac92dc9fd6c8915fccc09c8100710177283e"
	FedimintGatewayImage  = "fedimint/gatewayd:v0.11.1@sha256:65365fceb2e85dfb01837553be53a3372f118e094a23f672ef52bc38baf28ddf"

	FedimintBitcoinModeApp    = "app"
	FedimintBitcoinModeNative = "native"
	FedimintBitcoinModeRemote = "remote"

	FedimintImageApp AppImageVariant = "app"
)

type FedimintBitcoinRuntime struct {
	Mode string `json:"mode"`
	URL  string `json:"url"`
	User string `json:"user"`
	Pass string `json:"pass"`
}

type FedimintGuardianRuntime struct {
	Bitcoin FedimintBitcoinRuntime `json:"bitcoin"`
}

type FedimintGatewayRuntime struct {
	Bitcoin             FedimintBitcoinRuntime `json:"bitcoin"`
	GatewayPasswordHash string                 `json:"gateway_password_hash"`
}

func FedimintImageForApp(appID string, variant AppImageVariant) (string, error) {
	if variant != FedimintImageApp {
		return "", errors.New("Fedimint image variant is not allowed")
	}
	switch appID {
	case FedimintGuardianID:
		return FedimintGuardianImage, nil
	case FedimintGatewayID:
		return FedimintGatewayImage, nil
	default:
		return "", errors.New("Fedimint image manifest is not allowed")
	}
}

func ValidateFedimintBitcoinRuntime(runtime FedimintBitcoinRuntime) error {
	if runtime.Mode != FedimintBitcoinModeApp && runtime.Mode != FedimintBitcoinModeNative && runtime.Mode != FedimintBitcoinModeRemote {
		return errors.New("Fedimint Bitcoin mode is not allowed")
	}
	parsed, err := url.Parse(runtime.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Hostname() == "" ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("Fedimint Bitcoin RPC URL is invalid")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return errors.New("Fedimint Bitcoin RPC port is invalid")
		}
	}
	for _, value := range []struct {
		name  string
		value string
		max   int
	}{{"user", runtime.User, 256}, {"credential", runtime.Pass, 1024}} {
		if value.value == "" || len(value.value) > value.max || strings.ContainsAny(value.value, "\r\n\x00") {
			return fmt.Errorf("Fedimint Bitcoin RPC %s is invalid", value.name)
		}
	}
	return nil
}

func ValidateFedimintGuardianRuntime(runtime FedimintGuardianRuntime) error {
	return ValidateFedimintBitcoinRuntime(runtime.Bitcoin)
}

func ValidateFedimintGatewayRuntime(runtime FedimintGatewayRuntime) error {
	if err := ValidateFedimintBitcoinRuntime(runtime.Bitcoin); err != nil {
		return err
	}
	if hash := runtime.GatewayPasswordHash; len(hash) < 20 || len(hash) > 128 || strings.ContainsAny(hash, "\r\n\x00") ||
		(!strings.HasPrefix(hash, "$2a$") && !strings.HasPrefix(hash, "$2b$") && !strings.HasPrefix(hash, "$2y$")) {
		return errors.New("Fedimint gateway password hash is invalid")
	}
	return nil
}

func canonicalRuntimeJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func FedimintGuardianRuntimeJSON(runtime FedimintGuardianRuntime) ([]byte, error) {
	if err := ValidateFedimintGuardianRuntime(runtime); err != nil {
		return nil, err
	}
	return canonicalRuntimeJSON(runtime)
}

func FedimintGatewayRuntimeJSON(runtime FedimintGatewayRuntime) ([]byte, error) {
	if err := ValidateFedimintGatewayRuntime(runtime); err != nil {
		return nil, err
	}
	return canonicalRuntimeJSON(runtime)
}

func parseCanonicalRuntimeJSON(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > 4096 || raw[len(raw)-1] != '\n' || bytes.ContainsAny(raw, "\r\x00") {
		return errors.New("Fedimint runtime encoding is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("Fedimint runtime is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("Fedimint runtime contains trailing data")
	}
	expected, err := canonicalRuntimeJSON(target)
	if err != nil || !bytes.Equal(raw, expected) {
		return errors.New("Fedimint runtime is not canonical")
	}
	return nil
}

func ParseFedimintGuardianRuntimeJSON(raw []byte) (FedimintGuardianRuntime, error) {
	var runtime FedimintGuardianRuntime
	if err := parseCanonicalRuntimeJSON(raw, &runtime); err != nil {
		return runtime, err
	}
	if err := ValidateFedimintGuardianRuntime(runtime); err != nil {
		return FedimintGuardianRuntime{}, err
	}
	return runtime, nil
}

func ParseFedimintGatewayRuntimeJSON(raw []byte) (FedimintGatewayRuntime, error) {
	var runtime FedimintGatewayRuntime
	if err := parseCanonicalRuntimeJSON(raw, &runtime); err != nil {
		return runtime, err
	}
	if err := ValidateFedimintGatewayRuntime(runtime); err != nil {
		return FedimintGatewayRuntime{}, err
	}
	return runtime, nil
}

func fedimintNetworks(bitcoin FedimintBitcoinRuntime) (string, string) {
	if bitcoin.Mode == FedimintBitcoinModeRemote {
		return "", ""
	}
	return `    networks:
      - default
      - bitcoincore
`, fmt.Sprintf(`
  bitcoincore:
    external: true
    name: %s
`, BitcoinConsumerNetwork)
}

func composeYAMLSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func composeEscapeDollar(value string) string { return strings.ReplaceAll(value, "$", "$$") }

func FedimintGuardianCompose(runtime FedimintGuardianRuntime) (string, error) {
	if err := ValidateFedimintGuardianRuntime(runtime); err != nil {
		return "", err
	}
	extraNetworks, networkDecl := fedimintNetworks(runtime.Bitcoin)
	return fmt.Sprintf(`services:
  fedimintd:
    image: %s
    user: "%d:%d"
    restart: unless-stopped
    stop_grace_period: %ds
    init: true
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    extra_hosts:
      - "host.docker.internal:host-gateway"
    environment:
      HOME: /data
      FM_ENABLE_IROH: "true"
      FM_BITCOIN_NETWORK: bitcoin
      FM_BITCOIND_URL: %s
      FM_BITCOIND_USERNAME: %s
      FM_BITCOIND_PASSWORD: %s
      FM_BIND_P2P: "0.0.0.0:%d"
      FM_BIND_API: "0.0.0.0:%d"
      FM_BIND_UI: "0.0.0.0:%d"
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=32m,uid=%d,gid=%d,mode=0700
    ports:
      - "%d:%d/tcp"
      - "%d:%d/udp"
      - "%d:%d/udp"
      - "%d:%d/tcp"
    volumes:
      - %s:/data
%s
networks:
  default:
    name: %s
%s`, FedimintGuardianImage, FedimintContainerUID, FedimintContainerGID, FedimintStopTimeout,
		composeYAMLSingleQuote(runtime.Bitcoin.URL), composeYAMLSingleQuote(runtime.Bitcoin.User), composeYAMLSingleQuote(runtime.Bitcoin.Pass),
		FedimintGuardianP2PPort, FedimintGuardianAPIPort, FedimintGuardianUIPort,
		FedimintContainerUID, FedimintContainerGID,
		FedimintGuardianP2PPort, FedimintGuardianP2PPort, FedimintGuardianP2PPort, FedimintGuardianP2PPort,
		FedimintGuardianAPIPort, FedimintGuardianAPIPort, FedimintGuardianUIPort, FedimintGuardianUIPort,
		"${"+FedimintDataDirEnv+"}", extraNetworks, FedimintGuardianNetwork, networkDecl), nil
}

func FedimintGatewayCompose(runtime FedimintGatewayRuntime) (string, error) {
	if err := ValidateFedimintGatewayRuntime(runtime); err != nil {
		return "", err
	}
	extraNetworks, networkDecl := fedimintNetworks(runtime.Bitcoin)
	return fmt.Sprintf(`services:
  gatewayd:
    image: %s
    user: "%d:%d"
    command: ["gatewayd", "lnd"]
    restart: unless-stopped
    stop_grace_period: %ds
    init: true
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    extra_hosts:
      - "host.docker.internal:host-gateway"
    environment:
      HOME: /data
      FM_GATEWAY_DATA_DIR: /data
      FM_GATEWAY_LISTEN_ADDR: "0.0.0.0:%d"
      FM_GATEWAY_NETWORK: bitcoin
      FM_GATEWAY_IROH_LISTEN_ADDR: "0.0.0.0:%d"
      FM_GATEWAY_BCRYPT_PASSWORD_HASH: "%s"
      FM_BITCOIND_URL: %s
      FM_BITCOIND_USERNAME: %s
      FM_BITCOIND_PASSWORD: %s
      FM_LND_RPC_ADDR: "https://host.docker.internal:10009"
      FM_LND_TLS_CERT: %s
      FM_LND_MACAROON: %s
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=32m,uid=%d,gid=%d,mode=0700
    ports:
      - "%d:%d/tcp"
      - "%d:%d/udp"
    volumes:
      - %s:/data
      - ${%s}:/run/lnd:ro
%s
networks:
  default:
    name: %s
%s`, FedimintGatewayImage, FedimintContainerUID, FedimintContainerGID, FedimintStopTimeout,
		FedimintGatewayUIPort, FedimintGatewayIrohPort, composeEscapeDollar(runtime.GatewayPasswordHash),
		composeYAMLSingleQuote(runtime.Bitcoin.URL), composeYAMLSingleQuote(runtime.Bitcoin.User), composeYAMLSingleQuote(runtime.Bitcoin.Pass),
		FedimintGatewayTLSPath, FedimintGatewayMacaroonPath, FedimintContainerUID, FedimintContainerGID,
		FedimintGatewayUIPort, FedimintGatewayUIPort, FedimintGatewayIrohPort, FedimintGatewayIrohPort,
		"${"+FedimintDataDirEnv+"}", FedimintGatewayCredentialRoot, extraNetworks, FedimintGatewayNetwork, networkDecl), nil
}
