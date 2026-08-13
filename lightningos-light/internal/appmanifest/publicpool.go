package appmanifest

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const (
	PublicPoolID             = "publicpool"
	PublicPoolProject        = "publicpool"
	PublicPoolComposeFile    = "docker-compose.yaml"
	PublicPoolEnvFile        = ".env"
	PublicPoolCaddyfile      = "Caddyfile"
	PublicPoolPrimaryService = "public-pool"
	PublicPoolUIService      = "public-pool-ui"
	PublicPoolStopTimeout    = 30
	PublicPoolStratumPort    = 3333
	PublicPoolAPIPort        = 3334
	PublicPoolUIPort         = 8081
	PublicPoolUIInternalPort = 8080
	PublicPoolContainerUID   = 65532
	PublicPoolContainerGID   = 65532
	PublicPoolNetwork        = "mainnet"
	PublicPoolIdentifier     = "LightningOS-PublicPool"

	PublicPoolBackendCommit = "9938a05d2dfd65f3fe7c48d51033d72b9f8d5925"
	PublicPoolBackendDigest = "96347cbbc2ec6f27ec1b282e8353e41197fad6ca56e011873e69e7c3b6555b14"
	PublicPoolBackendImage  = "sethforprivacy/public-pool:9938a05@sha256:" + PublicPoolBackendDigest
	PublicPoolUICommit      = "20798ece23d351bceb8e6cce15b108a22585f225"
	PublicPoolUIDigest      = "69d9e88eba5f8acd6cb097c03286c0bb49fb64294149aec22b916b21788981c1"
	PublicPoolUIImage       = "sethforprivacy/public-pool-ui:20798ec@sha256:" + PublicPoolUIDigest

	PublicPoolBackendVersionOutput = "v22.11.0"
	PublicPoolUIVersionOutput      = "v2.8.4 h1:q3pe0wpBj1OcHFZ3n/1nl4V4bxBrYoSoab7rL9BMYNk="

	PublicPoolImageBackend AppImageVariant = "backend"
	PublicPoolImageUI      AppImageVariant = "ui"
)

type PublicPoolBitcoinMode string

const (
	PublicPoolBitcoinLocalApp      PublicPoolBitcoinMode = "local_app"
	PublicPoolBitcoinLocalExternal PublicPoolBitcoinMode = "local_external"
	PublicPoolBitcoinRemote        PublicPoolBitcoinMode = "remote"
)

// PublicPoolRuntime is the complete dynamic part of the pool declaration.
// Images, paths, ports, process identities, mounts and executable arguments
// remain catalog-owned and cannot be supplied by the manager.
type PublicPoolRuntime struct {
	BitcoinMode    PublicPoolBitcoinMode `json:"bitcoin_mode"`
	BitcoinRPCURL  string                `json:"bitcoin_rpc_url"`
	BitcoinRPCPort int                   `json:"bitcoin_rpc_port"`
	BitcoinRPCUser string                `json:"bitcoin_rpc_user"`
	BitcoinRPCPass string                `json:"bitcoin_rpc_pass"`
	BitcoinZMQHost string                `json:"bitcoin_zmq_host,omitempty"`
}

type PublicPoolComposePaths struct {
	DataDir       string
	CaddyfilePath string
}

func PublicPoolImageForVariant(variant AppImageVariant) (string, error) {
	switch variant {
	case PublicPoolImageBackend:
		return PublicPoolBackendImage, nil
	case PublicPoolImageUI:
		return PublicPoolUIImage, nil
	default:
		return "", errors.New("public pool image variant is not allowed")
	}
}

func PublicPoolImageVariants() []AppImageVariant {
	return []AppImageVariant{PublicPoolImageBackend, PublicPoolImageUI}
}

func PublicPoolImages() []string {
	return []string{PublicPoolBackendImage, PublicPoolUIImage}
}

func ValidatePublicPoolRuntime(runtime PublicPoolRuntime) error {
	if runtime.BitcoinRPCPort < 1 || runtime.BitcoinRPCPort > 65535 ||
		!validPublicPoolCredential(runtime.BitcoinRPCUser, 1, 512) ||
		!validPublicPoolCredential(runtime.BitcoinRPCPass, 1, 1024) {
		return errors.New("public pool Bitcoin RPC credential is invalid")
	}
	rpcURL, err := url.Parse(runtime.BitcoinRPCURL)
	if err != nil || rpcURL.Scheme != "http" || rpcURL.User != nil || rpcURL.Hostname() == "" ||
		rpcURL.Port() != "" || (rpcURL.Path != "" && rpcURL.Path != "/") ||
		rpcURL.RawQuery != "" || rpcURL.Fragment != "" || !validPublicPoolHost(rpcURL.Hostname()) {
		return errors.New("public pool Bitcoin RPC endpoint is invalid")
	}
	if runtime.BitcoinZMQHost != "" {
		if !strings.HasPrefix(runtime.BitcoinZMQHost, "tcp://") {
			return errors.New("public pool Bitcoin ZMQ endpoint is invalid")
		}
		host, portRaw, splitErr := net.SplitHostPort(strings.TrimPrefix(runtime.BitcoinZMQHost, "tcp://"))
		port, portErr := strconv.Atoi(portRaw)
		if splitErr != nil || portErr != nil || port < 1 || port > 65535 || !validPublicPoolHost(strings.Trim(host, "[]")) {
			return errors.New("public pool Bitcoin ZMQ endpoint is invalid")
		}
	}

	switch runtime.BitcoinMode {
	case PublicPoolBitcoinLocalApp:
		if runtime.BitcoinRPCURL != "http://bitcoind" || runtime.BitcoinRPCPort != 8332 ||
			runtime.BitcoinZMQHost != "tcp://bitcoind:28332" {
			return errors.New("public pool App Store Bitcoin wiring is invalid")
		}
	case PublicPoolBitcoinLocalExternal:
		if runtime.BitcoinRPCURL != "http://"+BitcoinConsumerHostGateway || runtime.BitcoinRPCPort != 8332 ||
			runtime.BitcoinZMQHost != "tcp://"+BitcoinConsumerHostGateway+":28332" {
			return errors.New("public pool native Bitcoin wiring is invalid")
		}
	case PublicPoolBitcoinRemote:
		host := strings.ToLower(rpcURL.Hostname())
		if host == "bitcoind" || host == BitcoinConsumerHostGateway || publicPoolLoopbackHost(host) {
			return errors.New("public pool remote Bitcoin RPC endpoint is invalid")
		}
		if runtime.BitcoinZMQHost != "" {
			zmqHost, _, _ := net.SplitHostPort(strings.TrimPrefix(runtime.BitcoinZMQHost, "tcp://"))
			if publicPoolLoopbackHost(strings.Trim(zmqHost, "[]")) {
				return errors.New("public pool remote Bitcoin ZMQ endpoint is invalid")
			}
		}
	default:
		return errors.New("public pool Bitcoin mode is invalid")
	}
	return nil
}

func PublicPoolEnv(runtime PublicPoolRuntime) (string, error) {
	if err := ValidatePublicPoolRuntime(runtime); err != nil {
		return "", err
	}
	return fmt.Sprintf(`BITCOIN_RPC_URL=%s
BITCOIN_RPC_USER=%s
BITCOIN_RPC_PASSWORD=%s
BITCOIN_RPC_PORT=%d
BITCOIN_RPC_TIMEOUT=10000
BITCOIN_ZMQ_HOST=%s
API_PORT=%d
STRATUM_PORT=%d
NETWORK=%s
API_SECURE=false
POOL_IDENTIFIER=%s
`, runtime.BitcoinRPCURL, runtime.BitcoinRPCUser, runtime.BitcoinRPCPass,
		runtime.BitcoinRPCPort, runtime.BitcoinZMQHost, PublicPoolAPIPort,
		PublicPoolStratumPort, PublicPoolNetwork, PublicPoolIdentifier), nil
}

func ParsePublicPoolEnv(raw []byte) (PublicPoolRuntime, error) {
	var runtime PublicPoolRuntime
	if len(raw) == 0 || len(raw) > 16*1024 || raw[len(raw)-1] != '\n' || strings.ContainsAny(string(raw), "\r\x00") {
		return runtime, errors.New("public pool environment encoding is invalid")
	}
	values := make(map[string]string)
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines[:len(lines)-1] {
		key, value, ok := strings.Cut(line, "=")
		_, duplicate := values[key]
		if !ok || key == "" || duplicate {
			return runtime, errors.New("public pool environment entry is invalid")
		}
		values[key] = value
	}
	allowed := map[string]bool{
		"BITCOIN_RPC_URL": true, "BITCOIN_RPC_USER": true, "BITCOIN_RPC_PASSWORD": true,
		"BITCOIN_RPC_PORT": true, "BITCOIN_RPC_TIMEOUT": true, "BITCOIN_ZMQ_HOST": true,
		"API_PORT": true, "STRATUM_PORT": true, "NETWORK": true, "API_SECURE": true,
		"POOL_IDENTIFIER": true,
	}
	for key := range values {
		if !allowed[key] {
			return runtime, errors.New("public pool environment key is not allowed")
		}
	}
	port, err := strconv.Atoi(values["BITCOIN_RPC_PORT"])
	if err != nil || values["BITCOIN_RPC_TIMEOUT"] != "10000" ||
		values["API_PORT"] != strconv.Itoa(PublicPoolAPIPort) ||
		values["STRATUM_PORT"] != strconv.Itoa(PublicPoolStratumPort) ||
		values["NETWORK"] != PublicPoolNetwork || values["API_SECURE"] != "false" ||
		values["POOL_IDENTIFIER"] != PublicPoolIdentifier {
		return runtime, errors.New("public pool environment does not match the catalog")
	}
	runtime = PublicPoolRuntime{
		BitcoinRPCURL: values["BITCOIN_RPC_URL"], BitcoinRPCPort: port,
		BitcoinRPCUser: values["BITCOIN_RPC_USER"], BitcoinRPCPass: values["BITCOIN_RPC_PASSWORD"],
		BitcoinZMQHost: values["BITCOIN_ZMQ_HOST"],
	}
	switch runtime.BitcoinRPCURL {
	case "http://bitcoind":
		runtime.BitcoinMode = PublicPoolBitcoinLocalApp
	case "http://" + BitcoinConsumerHostGateway:
		runtime.BitcoinMode = PublicPoolBitcoinLocalExternal
	default:
		runtime.BitcoinMode = PublicPoolBitcoinRemote
	}
	expected, envErr := PublicPoolEnv(runtime)
	if envErr != nil || string(raw) != expected {
		return PublicPoolRuntime{}, errors.New("public pool environment does not match the catalog")
	}
	return runtime, nil
}

func PublicPoolCaddyConfig() string {
	return fmt.Sprintf(`:%d {
  @api path /api*
  reverse_proxy @api %s:%d
  root * /run/public-pool-ui
  file_server
  log {
    output stdout
    format json
    level INFO
  }
}
`, PublicPoolUIInternalPort, PublicPoolPrimaryService, PublicPoolAPIPort)
}

func PublicPoolCompose(paths PublicPoolComposePaths, mode PublicPoolBitcoinMode) (string, error) {
	if paths.DataDir == "" || paths.CaddyfilePath == "" {
		return "", errors.New("public pool compose path is invalid")
	}
	backendNetworks := "    networks:\n      - default\n"
	externalNetwork := ""
	if mode == PublicPoolBitcoinLocalApp || mode == PublicPoolBitcoinLocalExternal {
		backendNetworks += "      - bitcoincore\n"
		externalNetwork = fmt.Sprintf("\n  bitcoincore:\n    external: true\n    name: %s\n", BitcoinConsumerNetwork)
	} else if mode != PublicPoolBitcoinRemote {
		return "", errors.New("public pool Bitcoin mode is invalid")
	}
	return fmt.Sprintf(`services:
  public-pool:
    image: %s
    restart: unless-stopped
    stop_grace_period: %ds
    init: true
    user: "%d:%d"
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    env_file:
      - ./.env
    environment:
      HOME: /tmp
      NODE_ENV: production
    ports:
      - "%d:%d"
      - "127.0.0.1:%d:%d"
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777
    volumes:
      - %s:/public-pool/DB:rw
%s
  public-pool-ui:
    image: %s
    restart: unless-stopped
    stop_grace_period: %ds
    init: true
    user: "%d:%d"
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    depends_on:
      - public-pool
    ports:
      - "%d:%d"
    environment:
      HOME: /tmp
      XDG_CONFIG_HOME: /config
      XDG_DATA_HOME: /data
    entrypoint:
      - /bin/sh
      - -c
    command:
      - |
        set -eu
        cp /usr/bin/caddy /run/lightningos-bin/caddy
        chmod 0500 /run/lightningos-bin/caddy
        cp -R /var/www/html/. /run/public-pool-ui/
        find /run/public-pool-ui -type f \( -name '*.br' -o -name '*.gz' \) -delete
        find /run/public-pool-ui -type f -name '*.js' -exec sed -i 's#https://public-pool.io:40557##g; s#http://localhost:3334##g; s#public-pool.io:21496##g' {} +
        exec /run/lightningos-bin/caddy run --config /etc/caddy/Caddyfile
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=16m,mode=1777
      - /run/lightningos-bin:rw,exec,nosuid,nodev,size=64m,uid=%d,gid=%d,mode=0700
      - /run/public-pool-ui:rw,noexec,nosuid,nodev,size=64m,uid=%d,gid=%d,mode=0700
      - /config:rw,noexec,nosuid,nodev,size=16m,uid=%d,gid=%d,mode=0700
      - /data:rw,noexec,nosuid,nodev,size=16m,uid=%d,gid=%d,mode=0700
    volumes:
      - %s:/etc/caddy/Caddyfile:ro
    networks:
      - default

networks:
  default:
    name: publicpool_default
%s`, PublicPoolBackendImage, PublicPoolStopTimeout, PublicPoolContainerUID,
		PublicPoolContainerGID, PublicPoolStratumPort, PublicPoolStratumPort,
		PublicPoolAPIPort, PublicPoolAPIPort, paths.DataDir, backendNetworks,
		PublicPoolUIImage, PublicPoolStopTimeout, PublicPoolContainerUID,
		PublicPoolContainerGID, PublicPoolUIPort, PublicPoolUIInternalPort,
		PublicPoolContainerUID, PublicPoolContainerGID, PublicPoolContainerUID,
		PublicPoolContainerGID, PublicPoolContainerUID, PublicPoolContainerGID,
		PublicPoolContainerUID, PublicPoolContainerGID, paths.CaddyfilePath,
		externalNetwork), nil
}

func validPublicPoolCredential(value string, minBytes, maxBytes int) bool {
	if len(value) < minBytes || len(value) > maxBytes {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e || strings.ContainsRune("$\\'\"`#", char) {
			return false
		}
	}
	return true
}

func validPublicPoolHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func publicPoolLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}
