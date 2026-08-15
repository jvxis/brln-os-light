package appmanifest

import (
	"errors"
	"fmt"
	"strings"
)

const (
	MempoolID             = "mempool"
	MempoolProject        = "mempool"
	MempoolComposeFile    = "docker-compose.yaml"
	MempoolEnvFile        = ".env"
	MempoolPrimaryService = "mempool-web"
	MempoolDatabase       = "mempool-db"
	MempoolPort           = 8999
	MempoolFrontendPort   = 8080
	MempoolBackendPort    = 8999
	MempoolStopTimeout    = 60
	MempoolContainerUID   = 1000
	MempoolContainerGID   = 1000
	MempoolDatabaseUID    = 999
	MempoolDatabaseGID    = 999
	MempoolDBVolume       = "mempool_dbdata"
	MempoolCacheVolume    = "mempool_cache"

	MempoolRelease = "3.3.1"
	// These are the official multi-architecture manifests for the upstream
	// Mempool release, selected by immutable digest rather than a mutable tag.
	MempoolFrontendImage = "mempool/frontend:v3.3.1@sha256:0a162e7e0d26a01e9686ddf69c96c4beae5fe10b0daa1020f3d392e033c058f1"
	MempoolBackendImage  = "mempool/backend:v3.3.1@sha256:358c0a517c8dcf26e7f5c02447de5bab33ec7e3fa6318685cf8012ce36098e3a"
	// MariaDB 10.11 is the supported LTS successor to the EOL 10.5 image in
	// upstream's example Compose file. AUTO_UPGRADE preserves existing 10.5
	// named volumes while moving them to this official digest-pinned release.
	MempoolDatabaseImage = "mariadb:10.11.18@sha256:de61fed4a40d3842f3ee09944ba52792156cfd9adf489b2cc670fc6ded28df8d"

	MempoolBitcoinModeApp    = "app"
	MempoolBitcoinModeNative = "native"

	MempoolImageFrontend AppImageVariant = "frontend"
	MempoolImageBackend  AppImageVariant = "backend"
	MempoolImageDatabase AppImageVariant = "database"
)

type MempoolRuntime struct {
	BitcoinMode    string
	Network        string
	BitcoinRPCUser string
	BitcoinRPCPass string
	DBPassword     string
	DBRootPassword string
	DataDir        string
}

func MempoolImageForVariant(variant AppImageVariant) (string, error) {
	switch variant {
	case MempoolImageFrontend:
		return MempoolFrontendImage, nil
	case MempoolImageBackend:
		return MempoolBackendImage, nil
	case MempoolImageDatabase:
		return MempoolDatabaseImage, nil
	default:
		return "", errors.New("mempool image variant is not allowed")
	}
}

func MempoolImageVariants() []AppImageVariant {
	return []AppImageVariant{MempoolImageFrontend, MempoolImageBackend, MempoolImageDatabase}
}

func ValidateMempoolRuntime(runtime MempoolRuntime) error {
	if runtime.BitcoinMode != MempoolBitcoinModeApp && runtime.BitcoinMode != MempoolBitcoinModeNative {
		return errors.New("mempool bitcoin mode is not allowed")
	}
	if runtime.Network != "bitcoin" && runtime.Network != "regtest" {
		return errors.New("mempool network is not allowed")
	}
	for _, value := range []struct {
		name  string
		value string
		max   int
	}{
		{"bitcoin RPC user", runtime.BitcoinRPCUser, 128},
		{"bitcoin RPC credential", runtime.BitcoinRPCPass, 512},
		{"database credential", runtime.DBPassword, 128},
		{"database root credential", runtime.DBRootPassword, 128},
	} {
		if value.value == "" || len(value.value) > value.max || strings.ContainsAny(value.value, "\r\n\x00= ") {
			return fmt.Errorf("mempool %s is invalid", value.name)
		}
		for _, char := range []byte(value.value) {
			if char < 0x21 || char > 0x7e {
				return fmt.Errorf("mempool %s is invalid", value.name)
			}
		}
	}
	if runtime.DataDir != "" {
		if _, err := NormalizeCatalogDataDir(MempoolID, runtime.DataDir); err != nil {
			return err
		}
	}
	return nil
}

func MempoolRuntimeEnv(runtime MempoolRuntime) (string, error) {
	if err := ValidateMempoolRuntime(runtime); err != nil {
		return "", err
	}
	values := []string{
		"MEMPOOL_BITCOIN_MODE=" + runtime.BitcoinMode,
		"MEMPOOL_NETWORK=" + runtime.Network,
		"MEMPOOL_BITCOIN_RPC_USER=" + runtime.BitcoinRPCUser,
		"MEMPOOL_BITCOIN_RPC_PASS=" + runtime.BitcoinRPCPass,
		"MEMPOOL_DB_PASSWORD=" + runtime.DBPassword,
		"MEMPOOL_DB_ROOT_PASSWORD=" + runtime.DBRootPassword,
	}
	if runtime.DataDir != "" {
		values = append(values, "MEMPOOL_DATA_DIR="+runtime.DataDir)
	}
	return strings.Join(append(values, ""), "\n"), nil
}

func ParseMempoolRuntimeEnv(raw []byte) (MempoolRuntime, error) {
	var runtime MempoolRuntime
	if len(raw) == 0 || len(raw) > 2048 || raw[len(raw)-1] != '\n' || strings.ContainsAny(string(raw), "\r\x00") {
		return runtime, errors.New("invalid mempool environment encoding")
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(raw), "\n")[:len(strings.Split(string(raw), "\n"))-1] {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return runtime, errors.New("invalid mempool environment entry")
		}
		if _, exists := values[key]; exists {
			return runtime, errors.New("duplicate mempool environment entry")
		}
		switch key {
		case "MEMPOOL_BITCOIN_MODE", "MEMPOOL_NETWORK", "MEMPOOL_BITCOIN_RPC_USER", "MEMPOOL_BITCOIN_RPC_PASS", "MEMPOOL_DB_PASSWORD", "MEMPOOL_DB_ROOT_PASSWORD", "MEMPOOL_DATA_DIR":
			values[key] = value
		default:
			return runtime, errors.New("mempool environment entry is not allowed")
		}
	}
	if len(values) != 6 && len(values) != 7 {
		return runtime, errors.New("mempool environment is incomplete")
	}
	runtime = MempoolRuntime{
		BitcoinMode: values["MEMPOOL_BITCOIN_MODE"], Network: values["MEMPOOL_NETWORK"], BitcoinRPCUser: values["MEMPOOL_BITCOIN_RPC_USER"],
		BitcoinRPCPass: values["MEMPOOL_BITCOIN_RPC_PASS"], DBPassword: values["MEMPOOL_DB_PASSWORD"],
		DBRootPassword: values["MEMPOOL_DB_ROOT_PASSWORD"],
		DataDir:        values["MEMPOOL_DATA_DIR"],
	}
	if err := ValidateMempoolRuntime(runtime); err != nil {
		return MempoolRuntime{}, err
	}
	expected, _ := MempoolRuntimeEnv(runtime)
	if string(raw) != expected {
		return MempoolRuntime{}, errors.New("mempool environment is not canonical")
	}
	return runtime, nil
}

func MempoolCompose(runtime MempoolRuntime) (string, error) {
	if err := ValidateMempoolRuntime(runtime); err != nil {
		return "", err
	}
	bitcoinHost := "bitcoind"
	if runtime.BitcoinMode == MempoolBitcoinModeNative {
		bitcoinHost = BitcoinConsumerHostGateway
	}
	network, err := ElectrsNetworkForName(runtime.Network)
	if err != nil {
		return "", err
	}
	mempoolNetwork := "mainnet"
	frontendFlags := "      MAINNET_ENABLED: \"true\""
	if runtime.Network == "regtest" {
		mempoolNetwork = "regtest"
		frontendFlags = "      MAINNET_ENABLED: \"false\"\n      REGTEST_ENABLED: \"true\"\n      ROOT_NETWORK: \"regtest\""
	}
	cacheMount := MempoolCacheVolume + ":/run/backend/cache"
	dbMount := MempoolDBVolume + ":/var/lib/mysql"
	volumeDeclaration := fmt.Sprintf("\nvolumes:\n  %s:\n    name: %s\n  %s:\n    name: %s\n", MempoolDBVolume, MempoolDBVolume, MempoolCacheVolume, MempoolCacheVolume)
	if runtime.DataDir != "" {
		cacheMount = runtime.DataDir + "/cache:/run/backend/cache"
		dbMount = runtime.DataDir + "/db:/var/lib/mysql"
		volumeDeclaration = ""
	}
	return fmt.Sprintf(`services:
  mempool-web:
    image: %s
    container_name: mempool-web
    user: "%d:%d"
    restart: unless-stopped
    stop_grace_period: %ds
    init: true
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    depends_on:
      - mempool-api
      - mempool-db
    environment:
      FRONTEND_HTTP_PORT: "%d"
      BACKEND_MAINNET_HTTP_HOST: "mempool-api"
      BACKEND_MAINNET_HTTP_PORT: "%d"
%s
    tmpfs:
      - /run/frontend:rw,exec,nosuid,nodev,size=256m,uid=%d,gid=%d,mode=0700
      - /var/run:rw,noexec,nosuid,nodev,size=4m,uid=%d,gid=%d,mode=0700
      - /var/cache/nginx:rw,noexec,nosuid,nodev,size=32m,uid=%d,gid=%d,mode=0700
      - /var/log/nginx:rw,noexec,nosuid,nodev,size=16m,uid=%d,gid=%d,mode=0700
    entrypoint: ["/bin/sh", "-c"]
    command:
      - |
        set -eu
        mkdir -p /run/frontend/patch /run/frontend/nginx /run/frontend/www
        cp -R /patch/. /run/frontend/patch/
        cp -R /etc/nginx/. /run/frontend/nginx/
        cp -R /var/www/mempool/. /run/frontend/www/
        sed -i 's#/etc/nginx#/run/frontend/nginx#g; s#/patch#/run/frontend/patch#g; s#/var/www/mempool#/run/frontend/www#g' /run/frontend/patch/entrypoint.sh /run/frontend/nginx/nginx.conf /run/frontend/nginx/conf.d/nginx-mempool.conf
        exec /run/frontend/patch/wait-for mempool-db:3306 --timeout=720 -- /run/frontend/patch/entrypoint.sh nginx -c /run/frontend/nginx/nginx.conf -g 'daemon off;'
    ports:
      - "%d:%d"

  mempool-api:
    image: %s
    container_name: mempool-api
    user: "%d:%d"
    restart: unless-stopped
    stop_grace_period: %ds
    init: true
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    depends_on:
      - mempool-db
    environment:
      MEMPOOL_BACKEND: "electrum"
      MEMPOOL_NETWORK: "%s"
      ELECTRUM_HOST: "electrs"
      ELECTRUM_PORT: "50001"
      ELECTRUM_TLS_ENABLED: "false"
      CORE_RPC_HOST: "%s"
      CORE_RPC_PORT: "%d"
      CORE_RPC_USERNAME: "${MEMPOOL_BITCOIN_RPC_USER}"
      CORE_RPC_PASSWORD: "${MEMPOOL_BITCOIN_RPC_PASS}"
      DATABASE_ENABLED: "true"
      DATABASE_HOST: "mempool-db"
      DATABASE_PORT: "3306"
      DATABASE_DATABASE: "mempool"
      DATABASE_USERNAME: "mempool"
      DATABASE_PASSWORD: "${MEMPOOL_DB_PASSWORD}"
      STATISTICS_ENABLED: "true"
    working_dir: /run/backend
    tmpfs:
      - /run/backend:rw,exec,nosuid,nodev,size=512m,uid=%d,gid=%d,mode=0700
      - /tmp:rw,noexec,nosuid,nodev,size=32m,uid=%d,gid=%d,mode=0700
    entrypoint: ["/bin/sh", "-c"]
    command:
      - |
        set -eu
        cp -a /backend/. /run/backend/
        sed -i 's#/backend#/run/backend#g' /run/backend/start.sh /run/backend/package/config.js
        exec ./wait-for-it.sh mempool-db:3306 --timeout=720 --strict -- ./start.sh
    networks:
      - default
      - bitcoincore
      - electrs
    volumes:
      - %s

  mempool-db:
    image: %s
    container_name: mempool-db
    user: "%d:%d"
    restart: unless-stopped
    stop_grace_period: %ds
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    environment:
      MARIADB_DATABASE: "mempool"
      MARIADB_USER: "mempool"
      MARIADB_PASSWORD: "${MEMPOOL_DB_PASSWORD}"
      MARIADB_ROOT_PASSWORD: "${MEMPOOL_DB_ROOT_PASSWORD}"
      MARIADB_AUTO_UPGRADE: "1"
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 5s
      timeout: 5s
      retries: 30
    tmpfs:
      - /run/mysqld:rw,noexec,nosuid,nodev,size=16m,uid=%d,gid=%d,mode=0750
      - /tmp:rw,noexec,nosuid,nodev,size=32m,uid=%d,gid=%d,mode=0700
    volumes:
      - %s

networks:
  default:
    name: mempool_default
  bitcoincore:
    external: true
    name: %s
  electrs:
    external: true
    name: electrs_default
%s`, MempoolFrontendImage, MempoolContainerUID, MempoolContainerGID, MempoolStopTimeout,
		MempoolFrontendPort, MempoolBackendPort, frontendFlags,
		MempoolContainerUID, MempoolContainerGID, MempoolContainerUID, MempoolContainerGID,
		MempoolContainerUID, MempoolContainerGID, MempoolContainerUID, MempoolContainerGID,
		MempoolPort, MempoolFrontendPort,
		MempoolBackendImage, MempoolContainerUID, MempoolContainerGID, MempoolStopTimeout,
		mempoolNetwork, bitcoinHost, network.RPCPort,
		MempoolContainerUID, MempoolContainerGID, MempoolContainerUID, MempoolContainerGID,
		cacheMount, MempoolDatabaseImage, MempoolDatabaseUID, MempoolDatabaseGID,
		MempoolStopTimeout, MempoolDatabaseUID, MempoolDatabaseGID, MempoolDatabaseUID, MempoolDatabaseGID,
		dbMount, BitcoinConsumerNetwork, volumeDeclaration), nil
}
