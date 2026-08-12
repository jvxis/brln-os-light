package appmanifest

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	LNbitsID                             = "lnbits"
	LNbitsProject                        = "lnbits"
	LNbitsComposeFile                    = "docker-compose.yaml"
	LNbitsEnvFile                        = ".env"
	LNbitsPrimaryService                 = "lnbits"
	LNbitsRelease                        = "1.5.6"
	LNbitsManifestSHA256                 = "6e37fbf9b847c066d7e022e19a018b3df7f12602a370f117857165d36bfb165b"
	LNbitsImage                          = "lnbits/lnbits:v" + LNbitsRelease + "@sha256:" + LNbitsManifestSHA256
	LNbitsMacaroonFile                   = "lnbits.macaroon"
	LNbitsTLSCertFile                    = "tls.cert"
	LNbitsLNDDir                         = "lnd"
	LNbitsPort                           = 5000
	LNbitsStopTimeout                    = 30
	LNbitsContainerUID                   = 65532
	LNbitsContainerGID                   = 65532
	LNbitsImageApp       AppImageVariant = "app"
)

var lnbitsEnvKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

type LNbitsComposePaths struct {
	DataDir      string
	TLSCertPath  string
	MacaroonPath string
}

// LNbitsImageForVariant selects only the stable official LNbits image pinned
// by its registry manifest digest. The digest is multi-architecture, so Docker
// still selects the correct linux/amd64 or linux/arm64 child manifest without
// trusting a mutable tag.
func LNbitsImageForVariant(variant AppImageVariant) (string, error) {
	if variant != LNbitsImageApp {
		return "", errors.New("lnbits image variant is not allowed")
	}
	return LNbitsImage, nil
}

func LNbitsImageVariants() []AppImageVariant {
	return []AppImageVariant{LNbitsImageApp}
}

// LNbitsManagedEnv is the closed funding-source contract enforced for every
// App Store installation. Other LNbits configuration is preserved for
// backwards compatibility, but it cannot replace these values.
func LNbitsManagedEnv() [][2]string {
	return [][2]string{
		{"LNBITS_BACKEND_WALLET_CLASS", "LndRestWallet"},
		{"LND_REST_ENDPOINT", "https://host.docker.internal:8080/"},
		{"LND_REST_CERT", "/etc/lnd/" + LNbitsTLSCertFile},
		{"LND_REST_MACAROON", "/etc/lnd/" + LNbitsMacaroonFile},
		{"LNBITS_DATA_FOLDER", "/app/data"},
		{"LNBITS_EXTENSIONS_PATH", "/app/data/extensions"},
		{"LNBITS_HOST", "0.0.0.0"},
		{"LNBITS_PORT", "5000"},
		{"AUTH_HTTPS_ONLY", "false"},
	}
}

// LNbitsScrubbedEnvKeys lists alternate credential selectors which upstream
// can prefer over the dedicated file. They may remain as empty compatibility
// entries, but carrying any value is rejected by the broker.
func LNbitsScrubbedEnvKeys() []string {
	return []string{
		"LND_REST_MACAROON_ENCRYPTED",
		"LND_ADMIN_MACAROON",
		"LND_REST_ADMIN_MACAROON",
		"LND_INVOICE_MACAROON",
		"LND_REST_INVOICE_MACAROON",
		"LND_GRPC_MACAROON",
		"LND_GRPC_MACAROON_ENCRYPTED",
	}
}

// NormalizeLNbitsEnv upgrades an existing App Store declaration without
// discarding unrelated LNbits settings. Managed funding-source fields are
// replaced once, alternate macaroon selectors are removed, and the result is
// validated before it can be written or snapshotted.
func NormalizeLNbitsEnv(raw []byte) ([]byte, error) {
	if len(raw) > 64*1024 || strings.ContainsAny(string(raw), "\r\x00") {
		return nil, errors.New("invalid lnbits environment encoding")
	}
	managed := make(map[string]bool)
	for _, item := range LNbitsManagedEnv() {
		managed[item[0]] = true
	}
	for _, key := range LNbitsScrubbedEnvKeys() {
		managed[key] = true
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(raw) == 0 {
		lines = nil
	}
	preserved := make([]string, 0, len(lines)+len(LNbitsManagedEnv())+1)
	for _, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		if ok && managed[key] {
			continue
		}
		preserved = append(preserved, line)
	}
	for len(preserved) > 0 && preserved[len(preserved)-1] == "" {
		preserved = preserved[:len(preserved)-1]
	}
	for _, item := range LNbitsManagedEnv() {
		preserved = append(preserved, item[0]+"="+item[1])
	}
	normalized := []byte(strings.Join(append(preserved, ""), "\n"))
	if err := ValidateLNbitsEnv(normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// ValidateLNbitsEnv accepts user LNbits settings while closing environment
// variables capable of replacing the reviewed executable/runtime or LND
// credential. It deliberately validates the exact bytes snapshotted by root.
func ValidateLNbitsEnv(raw []byte) error {
	if len(raw) == 0 || len(raw) > 64*1024 || raw[len(raw)-1] != '\n' || strings.ContainsAny(string(raw), "\r\x00") {
		return errors.New("invalid lnbits environment encoding")
	}
	values := make(map[string]string)
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines[:len(lines)-1] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !lnbitsEnvKeyPattern.MatchString(key) || len(value) > 8192 {
			return errors.New("invalid lnbits environment entry")
		}
		if _, exists := values[key]; exists {
			return errors.New("duplicate lnbits environment entry")
		}
		values[key] = value
	}
	for _, item := range LNbitsManagedEnv() {
		if values[item[0]] != item[1] {
			return errors.New("lnbits environment does not match the catalog")
		}
	}
	for _, key := range LNbitsScrubbedEnvKeys() {
		if values[key] != "" {
			return errors.New("lnbits environment contains an alternate LND credential")
		}
	}
	for _, key := range []string{
		"PATH", "HOME", "PYTHONHOME", "PYTHONPATH", "LD_PRELOAD", "LD_LIBRARY_PATH",
		"VIRTUAL_ENV", "UV_CONFIG_FILE", "UV_PROJECT_ENVIRONMENT", "UV_WORKING_DIR", "XDG_CACHE_HOME",
	} {
		if _, exists := values[key]; exists {
			return errors.New("lnbits environment overrides the closed runtime")
		}
	}
	return nil
}

// LNbitsCompose returns the only Compose document the broker accepts. The
// application has no Docker socket or host namespace access; its two LND
// inputs are individual read-only files and its root filesystem is read-only.
func LNbitsCompose(paths LNbitsComposePaths) string {
	return fmt.Sprintf(`services:
  lnbits:
    image: %s
    container_name: lnbits
    user: "%d:%d"
    restart: unless-stopped
    stop_grace_period: %ds
    init: true
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    env_file:
      - ./.env
    environment:
      HOME: /app/data
      XDG_CACHE_HOME: /app/data/.cache
      PYTHONDONTWRITEBYTECODE: "1"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "%d:%d"
    command:
      - /app/.venv/bin/lnbits
      - --port
      - "5000"
      - --host
      - "0.0.0.0"
      - --forwarded-allow-ips=*
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777
    volumes:
      - %s:/app/data:rw
      - %s:/etc/lnd/tls.cert:ro
      - %s:/etc/lnd/lnbits.macaroon:ro

networks:
  default:
    name: lnbits_default
`, LNbitsImage, LNbitsContainerUID, LNbitsContainerGID, LNbitsStopTimeout,
		LNbitsPort, LNbitsPort, paths.DataDir, paths.TLSCertPath, paths.MacaroonPath)
}
