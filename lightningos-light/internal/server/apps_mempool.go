package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lightningos-light/internal/system"
)

// Versions are pinned to upstream mempool v3.2.1 (2025-04-14) so a broken
// :latest tag never breaks installs. Bump these constants together when
// upgrading; mariadb tracks what upstream's docker-compose.yml ships with.
const (
	mempoolAppID            = "mempool"
	mempoolBackendImage     = "mempool/backend:v3.2.1"
	mempoolFrontendImage    = "mempool/frontend:v3.2.1"
	mempoolDBImage          = "mariadb:10.5.21"
	mempoolFrontendHostPort = 8999
	mempoolFrontendInternal = 8080
	mempoolBackendInternal  = 8999
	mempoolDBVolumeName     = "mempool_dbdata"
)

type mempoolPaths struct {
	Root        string
	ComposePath string
	EnvPath     string
}

type mempoolRuntimeValues struct {
	BitcoinRPCUser string
	BitcoinRPCPass string
	BitcoinRPCPort int
	DBPassword     string
	DBRootPassword string
}

type mempoolApp struct {
	server *Server
}

func newMempoolApp(s *Server) appHandler {
	return mempoolApp{server: s}
}

func mempoolDefinition() appDefinition {
	return appDefinition{
		ID:          mempoolAppID,
		Name:        "Mempool",
		Description: "Self-hosted mempool.space dashboard for your Bitcoin Core node. Requires Electrs installed and running.",
		Port:        mempoolFrontendHostPort,
	}
}

func (a mempoolApp) Definition() appDefinition {
	return mempoolDefinition()
}

func (a mempoolApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	paths := mempoolAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	status, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, "mempool-web")
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a mempoolApp) Install(ctx context.Context) error {
	return a.server.applyMempool(ctx)
}

func (a mempoolApp) Start(ctx context.Context) error {
	return a.server.applyMempool(ctx)
}

func (a mempoolApp) Stop(ctx context.Context) error {
	paths := mempoolAppPaths()
	if !fileExists(paths.ComposePath) {
		return errors.New("Mempool is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func (a mempoolApp) Uninstall(ctx context.Context) error {
	paths := mempoolAppPaths()
	if fileExists(paths.ComposePath) {
		// --volumes wipes the named DB volume. Mempool's statistics history is
		// a consulting app's cache — all rebuildable from Bitcoin Core, so a
		// clean uninstall is preferred over preserving an orphaned DB that
		// would then go stale vs. rotated credentials on the next install.
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--volumes", "--remove-orphans")
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func mempoolAppPaths() mempoolPaths {
	root := filepath.Join(appsRoot, mempoolAppID)
	return mempoolPaths{
		Root:        root,
		ComposePath: filepath.Join(root, "docker-compose.yaml"),
		EnvPath:     filepath.Join(root, ".env"),
	}
}

func (s *Server) applyMempool(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	if err := s.requireFullIndexApps(ctx); err != nil {
		return err
	}

	bitcoinPaths := bitcoinCoreAppPaths()
	if !fileExists(bitcoinPaths.ComposePath) {
		return errors.New("Mempool requires the Bitcoin Core app to be installed")
	}
	if !fileExists(electrsAppPaths().ComposePath) {
		return errors.New("Mempool requires the Electrs app to be installed")
	}
	electrsInfo, err := newElectrsApp(s).Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to read Electrs status: %w", err)
	}
	if electrsInfo.Status != "running" {
		return errors.New("Mempool requires Electrs to be installed and running")
	}

	paths := mempoolAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}

	if err := ensureDockerImage(ctx, mempoolBackendImage); err != nil {
		return err
	}
	if err := ensureDockerImage(ctx, mempoolFrontendImage); err != nil {
		return err
	}
	if err := ensureDockerImage(ctx, mempoolDBImage); err != nil {
		return err
	}

	values, err := s.resolveMempoolRuntimeValues(ctx, bitcoinPaths, paths)
	if err != nil {
		return err
	}

	if err := ensureMempoolEnv(paths, values); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, mempoolComposeContents(paths)); err != nil {
		return err
	}

	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureMempoolUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("mempool: ufw rule failed: %v", err)
	}
	return nil
}

// ensureMempoolUfwAccess opens the frontend port on ufw when the firewall is
// active. Matches the lnbits helper — silently no-ops when ufw is disabled so
// dev machines without a firewall still work.
func ensureMempoolUfwAccess(ctx context.Context) error {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}
	_, err = system.RunCommandWithSudo(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", mempoolFrontendHostPort))
	return err
}

func (s *Server) resolveMempoolRuntimeValues(ctx context.Context, bitcoinPaths bitcoinCorePaths, paths mempoolPaths) (mempoolRuntimeValues, error) {
	localCfg, updated, err := readBitcoinLocalRPCConfig(ctx)
	if err != nil {
		return mempoolRuntimeValues{}, fmt.Errorf("local bitcoin RPC unavailable: %w", err)
	}
	if strings.TrimSpace(localCfg.User) == "" || strings.TrimSpace(localCfg.Pass) == "" {
		return mempoolRuntimeValues{}, errors.New("local bitcoin RPC credentials missing")
	}
	if updated {
		if err := runCompose(ctx, bitcoinPaths.Root, bitcoinPaths.ComposePath, "restart", "bitcoind"); err != nil {
			return mempoolRuntimeValues{}, fmt.Errorf("failed to restart local bitcoind after RPC allowlist update: %w", err)
		}
	}
	_, rpcPort := parseMainchainRPC(localCfg.Host)

	values := mempoolRuntimeValues{
		BitcoinRPCUser: localCfg.User,
		BitcoinRPCPass: localCfg.Pass,
		BitcoinRPCPort: rpcPort,
	}

	// Reuse generated DB credentials from a previous install so the rocksdb-
	// like history under DBDataDir stays usable across reinstalls.
	if fileExists(paths.EnvPath) {
		if _, existing, err := envValueState(paths.EnvPath, "MEMPOOL_DB_PASSWORD"); err == nil {
			values.DBPassword = strings.TrimSpace(existing)
		}
		if _, existing, err := envValueState(paths.EnvPath, "MEMPOOL_DB_ROOT_PASSWORD"); err == nil {
			values.DBRootPassword = strings.TrimSpace(existing)
		}
	}
	if values.DBPassword == "" {
		token, err := randomToken(24)
		if err != nil {
			return mempoolRuntimeValues{}, fmt.Errorf("failed to generate db password: %w", err)
		}
		values.DBPassword = token
	}
	if values.DBRootPassword == "" {
		token, err := randomToken(24)
		if err != nil {
			return mempoolRuntimeValues{}, fmt.Errorf("failed to generate db root password: %w", err)
		}
		values.DBRootPassword = token
	}
	return values, nil
}

func ensureMempoolEnv(paths mempoolPaths, values mempoolRuntimeValues) error {
	required := [][2]string{
		{"MEMPOOL_BITCOIN_RPC_USER", values.BitcoinRPCUser},
		{"MEMPOOL_BITCOIN_RPC_PASS", values.BitcoinRPCPass},
		{"MEMPOOL_BITCOIN_RPC_PORT", strconv.Itoa(values.BitcoinRPCPort)},
		{"MEMPOOL_DB_PASSWORD", values.DBPassword},
		{"MEMPOOL_DB_ROOT_PASSWORD", values.DBRootPassword},
	}
	if !fileExists(paths.EnvPath) {
		lines := make([]string, 0, len(required)+1)
		for _, kv := range required {
			lines = append(lines, kv[0]+"="+kv[1])
		}
		lines = append(lines, "")
		return writeFile(paths.EnvPath, strings.Join(lines, "\n"), 0600)
	}
	for _, kv := range required {
		exists, value, err := envValueState(paths.EnvPath, kv[0])
		if err != nil {
			return err
		}
		if !exists {
			if err := appendEnvLine(paths.EnvPath, kv[0], kv[1]); err != nil {
				return err
			}
			continue
		}
		if strings.TrimSpace(value) != kv[1] {
			if err := setEnvValue(paths.EnvPath, kv[0], kv[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// mempoolComposeContents builds the docker-compose file. Three services share
// an internal `default` network for db talk; the backend additionally joins the
// existing bitcoincore_default and electrs_default external networks (created
// by those apps' compose files) so it can reach bitcoind:8332 and electrs:50001
// without needing to publish those on the host. Substitution variables come
// from the sibling `.env` file written by ensureMempoolEnv.
//
// Persistence model: the MariaDB data dir lives in a Docker-managed named
// volume so the manager (running as `lightningos`) never has to mkdir/chown
// host paths that need uid 999 escalation. Mempool's /backend/cache is left
// ephemeral inside the container — everything there is regenerable from
// bitcoind + electrs, so wiping it between restarts costs nothing.
func mempoolComposeContents(_ mempoolPaths) string {
	return fmt.Sprintf(`services:
  mempool-web:
    image: %[1]s
    container_name: mempool-web
    restart: unless-stopped
    stop_grace_period: 1m
    depends_on:
      - mempool-api
    environment:
      FRONTEND_HTTP_PORT: "%[6]d"
      BACKEND_MAINNET_HTTP_HOST: "mempool-api"
      BACKEND_MAINNET_HTTP_PORT: "%[7]d"
    command: "./wait-for mempool-db:3306 --timeout=720 -- nginx -g 'daemon off;'"
    ports:
      - "%[5]d:%[6]d"
  mempool-api:
    image: %[2]s
    container_name: mempool-api
    restart: unless-stopped
    stop_grace_period: 1m
    depends_on:
      - mempool-db
    environment:
      MEMPOOL_BACKEND: "electrum"
      ELECTRUM_HOST: "electrs"
      ELECTRUM_PORT: "50001"
      ELECTRUM_TLS_ENABLED: "false"
      CORE_RPC_HOST: "bitcoind"
      CORE_RPC_PORT: "${MEMPOOL_BITCOIN_RPC_PORT}"
      CORE_RPC_USERNAME: "${MEMPOOL_BITCOIN_RPC_USER}"
      CORE_RPC_PASSWORD: "${MEMPOOL_BITCOIN_RPC_PASS}"
      DATABASE_ENABLED: "true"
      DATABASE_HOST: "mempool-db"
      DATABASE_PORT: "3306"
      DATABASE_DATABASE: "mempool"
      DATABASE_USERNAME: "mempool"
      DATABASE_PASSWORD: "${MEMPOOL_DB_PASSWORD}"
      STATISTICS_ENABLED: "true"
    command: "./wait-for-it.sh mempool-db:3306 --timeout=720 --strict -- ./start.sh"
    networks:
      - default
      - bitcoincore
      - electrs
  mempool-db:
    image: %[3]s
    container_name: mempool-db
    restart: unless-stopped
    stop_grace_period: 1m
    environment:
      MYSQL_DATABASE: "mempool"
      MYSQL_USER: "mempool"
      MYSQL_PASSWORD: "${MEMPOOL_DB_PASSWORD}"
      MYSQL_ROOT_PASSWORD: "${MEMPOOL_DB_ROOT_PASSWORD}"
    volumes:
      - %[4]s:/var/lib/mysql

networks:
  default:
    name: mempool_default
  bitcoincore:
    external: true
    name: bitcoincore_default
  electrs:
    external: true
    name: electrs_default

volumes:
  %[4]s:
    name: %[4]s
`,
		mempoolFrontendImage,
		mempoolBackendImage,
		mempoolDBImage,
		mempoolDBVolumeName,
		mempoolFrontendHostPort,
		mempoolFrontendInternal,
		mempoolBackendInternal,
	)
}
