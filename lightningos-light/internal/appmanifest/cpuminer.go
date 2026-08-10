package appmanifest

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	CPUMinerID          = "cpuminer"
	CPUMinerProject     = "cpuminer"
	CPUMinerComposeFile = "docker-compose.yaml"
	CPUMinerEnvFile     = ".env"
)

var (
	cpuminerWorkerPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	cpuminerAddressPattern = regexp.MustCompile(`^[A-Za-z0-9]{14,100}$`)
	cpuminerImages         = map[string]struct{}{
		"jvx1971/cpu-lottery-miner:v1": {},
		"cniweb/cpuminer-opt@sha256:8aba97834d6a6e1946b2a61c8939eee8907b7be97d8e77c1174f66579d5bd90b": {},
		"cniweb/cpuminer-opt:latest": {},
	}
)

// CPUMinerCompose is the only Compose document the privileged broker accepts
// for the first App Store migration slice. Keeping it in a dependency shared
// by the manager and broker prevents their definitions from drifting.
func CPUMinerCompose() string {
	return `services:
  cpuminer:
    image: ${CPUMINER_IMAGE}
    restart: unless-stopped
    stop_grace_period: 2s
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "127.0.0.1:4048:4048"
    cpus: "${THREADS}"
    cpu_shares: 128
    command:
      - "cpuminer"
      - "--algo"
      - "sha256d"
      - "--url"
      - "stratum+tcp://${STRATUM_HOST}:${STRATUM_PORT}"
      - "--user"
      - "${MINING_ADDRESS}.${WORKER_NAME}"
      - "--pass"
      - "x"
      - "--threads"
      - "${THREADS}"
      - "--api-bind"
      - "0.0.0.0:4048"
`
}

// ValidateCPUMinerEnv rejects unknown, missing, duplicated, or unsafe values
// before the broker copies the environment into its root-owned execution
// snapshot. The caller cannot inject a Compose field or select a new image.
func ValidateCPUMinerEnv(raw []byte) error {
	_, err := parseCPUMinerEnv(raw)
	return err
}

// CPUMinerImage returns the catalog-allowlisted image selected by a validated
// CPU Miner environment. Privileged callers can use it without accepting an
// image name directly from their request protocol.
func CPUMinerImage(raw []byte) (string, error) {
	values, err := parseCPUMinerEnv(raw)
	if err != nil {
		return "", err
	}
	return values["CPUMINER_IMAGE"], nil
}

func parseCPUMinerEnv(raw []byte) (map[string]string, error) {
	if len(raw) == 0 || len(raw) > 16*1024 {
		return nil, errors.New("invalid cpuminer environment size")
	}
	values := make(map[string]string)
	for lineNumber, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		if strings.TrimSpace(line) != line || strings.HasPrefix(line, "#") {
			return nil, fmt.Errorf("invalid cpuminer environment line %d", lineNumber+1)
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("invalid cpuminer environment line %d", lineNumber+1)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("duplicate cpuminer environment key %s", key)
		}
		values[key] = value
	}

	required := []string{"CPUMINER_IMAGE", "POOL_MODE", "STRATUM_HOST", "STRATUM_PORT", "MINING_ADDRESS", "WORKER_NAME", "THREADS"}
	if len(values) != len(required) {
		return nil, errors.New("cpuminer environment keys do not match the manifest")
	}
	for _, key := range required {
		if _, ok := values[key]; !ok {
			return nil, fmt.Errorf("cpuminer environment key %s is missing", key)
		}
	}
	if _, ok := cpuminerImages[values["CPUMINER_IMAGE"]]; !ok {
		return nil, errors.New("cpuminer image is not allowed")
	}
	switch values["POOL_MODE"] {
	case "local":
		if values["STRATUM_HOST"] != "host.docker.internal" || values["STRATUM_PORT"] != "3333" {
			return nil, errors.New("cpuminer local pool target is invalid")
		}
	case "brln":
		if values["STRATUM_HOST"] != "btcpool.br-ln.com" || values["STRATUM_PORT"] != "3332" {
			return nil, errors.New("cpuminer BR-LN pool target is invalid")
		}
	default:
		return nil, errors.New("cpuminer pool mode is invalid")
	}
	address := values["MINING_ADDRESS"]
	if !cpuminerAddressPattern.MatchString(address) || !(strings.HasPrefix(strings.ToLower(address), "bc1") || strings.HasPrefix(address, "1") || strings.HasPrefix(address, "3")) {
		return nil, errors.New("cpuminer mining address is invalid")
	}
	if !cpuminerWorkerPattern.MatchString(values["WORKER_NAME"]) {
		return nil, errors.New("cpuminer worker is invalid")
	}
	threads, err := strconv.Atoi(values["THREADS"])
	if err != nil || threads < 1 || threads > 1024 || strconv.Itoa(threads) != values["THREADS"] {
		return nil, errors.New("cpuminer thread count is invalid")
	}
	return values, nil
}
